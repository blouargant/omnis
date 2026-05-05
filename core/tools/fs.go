// Package tools implements the article's "extended tool arsenal":
// bash, read, write, grep, glob, revert. Each tool is exposed as an ADK
// tool.Tool via functiontool.New, so it plugs straight into llmagent.New.
//
// Implementation notes:
//   - run_read returns numbered lines so the model can reference them in a
//     subsequent run_write call (article phase 4 / s14).
//   - run_write snapshots the previous file contents into an in-memory map;
//     run_revert restores from that snapshot. Snapshots are per-process.
//   - Errors are returned as strings in the result, never raised, matching
//     the article's harness contract ("handlers never raise to the loop").
package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/blouargant/agent-toolkit/internal/filter"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// MaxToolOutput caps the size of any tool string result, mirroring the
// article's 50_000 character truncation.
const MaxToolOutput = 50_000

// snapshots holds the previous content of files written by RunWrite.
// A nil entry marks "file did not exist" so RunRevert can delete it.
var (
	snapMu    sync.Mutex
	snapshots = map[string]*string{}

	bashFilterMu       sync.RWMutex
	bashFilterEnabled  bool
	bashFilterRegistry *filter.Registry
)

// alwaysBlock contains substrings that RunBash refuses outright. The
// permissions package implements the full three-tier YAML governance; this
// is the hard floor that always applies, even when permissions are disabled.
var alwaysBlock = []string{"rm -rf /", ":(){:|:&};:", "mkfs"}

// BashOutputFilterConfig controls optional output filtering for RunBash.
type BashOutputFilterConfig struct {
	Enabled    bool
	FiltersDir string
}

// ConfigureBashOutputFilter loads and enables/disables bash output filtering.
func ConfigureBashOutputFilter(cfg BashOutputFilterConfig) error {
	bashFilterMu.Lock()
	defer bashFilterMu.Unlock()

	bashFilterEnabled = false
	bashFilterRegistry = nil

	if !cfg.Enabled {
		return nil
	}
	rulesDir := strings.TrimSpace(cfg.FiltersDir)
	if rulesDir == "" {
		rulesDir = filter.DefaultRulesDir
	}
	filters, err := filter.LoadDir(rulesDir)
	if err != nil {
		return fmt.Errorf("bash output filter: load rules from %q: %w", rulesDir, err)
	}
	bashFilterRegistry = filter.NewRegistry(filters)
	bashFilterEnabled = true
	return nil
}

func maybeApplyBashOutputFilter(command, output string) string {
	bashFilterMu.RLock()
	enabled := bashFilterEnabled
	reg := bashFilterRegistry
	bashFilterMu.RUnlock()

	if !enabled || reg == nil || strings.TrimSpace(output) == "" {
		return output
	}
	filtered, applied, err := filter.ApplyForCommand(reg, command, output)
	if err != nil || !applied {
		return output
	}
	return strings.TrimRight(filtered, "\n")
}

func maybeInjectBashFilterArgs(command string) string {
	bashFilterMu.RLock()
	enabled := bashFilterEnabled
	reg := bashFilterRegistry
	bashFilterMu.RUnlock()

	if !enabled || reg == nil || strings.TrimSpace(command) == "" {
		return command
	}
	// Keep shell behavior unchanged for complex expressions.
	if strings.ContainsAny(command, "|;&<>()`$") {
		return command
	}

	parts := strings.Fields(command)
	if len(parts) == 0 {
		return command
	}

	binary := parts[0]
	allArgs := []string{}
	if len(parts) > 1 {
		allArgs = parts[1:]
	}

	subcommand := ""
	args := allArgs
	if len(allArgs) > 0 {
		subcommand = allArgs[0]
		args = allArgs[1:]
	}

	f := reg.Match(filepath.Base(binary), subcommand, args)
	if f == nil || f.Inject == nil {
		return command
	}

	injectedArgs, changed := reg.ShouldInject(f, allArgs)
	if !changed {
		return command
	}

	tokens := append([]string{binary}, injectedArgs...)
	quoted := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		quoted = append(quoted, shellQuote(tok))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '=' || r == '+' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// truncate caps s at MaxToolOutput.
func truncate(s string) string {
	if len(s) > MaxToolOutput {
		return s[:MaxToolOutput] + fmt.Sprintf("\n... (truncated at %d bytes)", MaxToolOutput)
	}
	return s
}

// ------------------ bash ------------------

type BashIn struct {
	Command string `json:"command" jsonschema:"shell command to execute"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"timeout in seconds, default 120"`
}
type BashOut struct {
	Output string `json:"output"`
}

// RunBash executes a shell command via /bin/sh -c, with a default 120s
// timeout. Output is truncated at MaxToolOutput.
func RunBash(ctx context.Context, in BashIn) (string, error) {
	for _, b := range alwaysBlock {
		if strings.Contains(in.Command, b) {
			return fmt.Sprintf("Error: command blocked by safety floor (%q)", b), nil
		}
	}
	timeout := time.Duration(in.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execCommand := maybeInjectBashFilterArgs(in.Command)
	cmd := exec.CommandContext(cctx, "/bin/sh", "-c", execCommand)
	out, err := cmd.CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	if errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return fmt.Sprintf("Error: command timed out after %s\n%s", timeout, truncate(s)), nil
	}
	if err != nil && s == "" {
		return fmt.Sprintf("Error: %v", err), nil
	}
	s = maybeApplyBashOutputFilter(in.Command, s)
	if s == "" {
		return "(no output)", nil
	}
	return truncate(s), nil
}

// ------------------ read ------------------

type ReadIn struct {
	Path      string `json:"file_path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}
type ReadOut struct {
	Content string `json:"content"`
}

// RunRead returns numbered lines of a file, optionally bounded by a
// [start_line, end_line] inclusive range (1-indexed).
func RunRead(_ context.Context, in ReadIn) (string, error) {
	data, err := os.ReadFile(in.Path)
	if err != nil {
		return fmt.Sprintf("Error reading %s: %v", in.Path, err), nil
	}
	lines := strings.Split(string(data), "\n")
	// trailing newline produces a trailing empty element; drop it for sane numbering
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	start := in.StartLine
	if start <= 0 {
		start = 1
	}
	end := in.EndLine
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return "(empty range)", nil
	}
	var b strings.Builder
	for i := start - 1; i < end; i++ {
		fmt.Fprintf(&b, "%4d\t%s\n", i+1, lines[i])
	}
	if b.Len() == 0 {
		return "(empty file)", nil
	}
	return truncate(b.String()), nil
}

// ------------------ write + revert ------------------

type WriteIn struct {
	Path    string `json:"file_path"`
	Content string `json:"content"`
}
type WriteOut struct {
	Result string `json:"result"`
}

// RunWrite writes content to a file, snapshotting the previous contents (if
// any) so RunRevert can restore them.
func RunWrite(_ context.Context, in WriteIn) (string, error) {
	snapMu.Lock()
	if data, err := os.ReadFile(in.Path); err == nil {
		s := string(data)
		snapshots[in.Path] = &s
	} else if os.IsNotExist(err) {
		snapshots[in.Path] = nil // marker: file did not exist
	}
	snapMu.Unlock()
	if dir := filepath.Dir(in.Path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil {
		return fmt.Sprintf("Error writing %s: %v", in.Path, err), nil
	}
	return fmt.Sprintf("wrote %s (%d bytes; snapshot saved - call revert to undo)", in.Path, len(in.Content)), nil
}

type RevertIn struct {
	Path string `json:"file_path"`
}
type RevertOut struct {
	Result string `json:"result"`
}

// RunRevert restores a file to its pre-write state.
func RunRevert(_ context.Context, in RevertIn) (string, error) {
	snapMu.Lock()
	snap, ok := snapshots[in.Path]
	if ok {
		delete(snapshots, in.Path)
	}
	snapMu.Unlock()
	if !ok {
		return fmt.Sprintf("Error: no snapshot for %s", in.Path), nil
	}
	if snap == nil {
		// file did not exist before — delete it
		if err := os.Remove(in.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Sprintf("Error removing %s: %v", in.Path, err), nil
		}
		return fmt.Sprintf("reverted: removed %s (was newly created)", in.Path), nil
	}
	if err := os.WriteFile(in.Path, []byte(*snap), 0o644); err != nil {
		return fmt.Sprintf("Error reverting %s: %v", in.Path, err), nil
	}
	return fmt.Sprintf("reverted %s (%d bytes restored)", in.Path, len(*snap)), nil
}

// ------------------ grep ------------------

type GrepIn struct {
	Pattern   string `json:"pattern"`
	Path      string `json:"path,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
}
type GrepOut struct {
	Matches string `json:"matches"`
}

// RunGrep shells out to /usr/bin/grep -nE for portability. Returns the
// matching lines with file:line: prefixes.
func RunGrep(ctx context.Context, in GrepIn) (string, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	args := []string{"-nE"}
	if in.Recursive || isDir(path) {
		args = append(args, "-r")
	}
	args = append(args, in.Pattern, path)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "grep", args...).CombinedOutput()
	s := strings.TrimRight(string(out), "\n")
	// grep exits 1 when no matches; that is not an error for us
	if err != nil && s == "" {
		return "(no matches)", nil
	}
	return truncate(s), nil
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// ------------------ glob ------------------

type GlobIn struct {
	Pattern string `json:"pattern" jsonschema:"glob pattern, e.g. **/*.go"`
}
type GlobOut struct {
	Files string `json:"files"`
}

// RunGlob expands a glob pattern and returns a sorted, newline-separated
// list of matches. Supports ** by walking when the pattern starts with **/.
func RunGlob(_ context.Context, in GlobIn) (string, error) {
	var matches []string
	if strings.Contains(in.Pattern, "**") {
		// crude ** support: split on '**/' and walk
		parts := strings.SplitN(in.Pattern, "**/", 2)
		root := "."
		if parts[0] != "" {
			root = strings.TrimSuffix(parts[0], "/")
		}
		suffix := parts[1]
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if ok, _ := filepath.Match(suffix, filepath.Base(p)); ok {
				matches = append(matches, p)
			}
			return nil
		})
	} else {
		m, err := filepath.Glob(in.Pattern)
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		matches = m
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return truncate(strings.Join(matches, "\n")), nil
}

// ----------------------------------------------------------------------
// ADK tool wrappers — each builds a functiontool.Tool with a short, precise
// description (the article emphasises that descriptions ARE instructions).
// ----------------------------------------------------------------------

// New returns the full extended tool set as ADK tools. Failure to build any
// tool panics (these are static configurations that should always succeed).
func New() []tool.Tool {
	return []tool.Tool{
		mustTool("bash",
			"Run a shell command via /bin/sh -c. Use for arbitrary shell operations. Times out after 120s by default.",
			func(_ tool.Context, in BashIn) (BashOut, error) {
				out, _ := RunBash(context.Background(), in)
				return BashOut{Output: out}, nil
			}),
		mustTool("read",
			"Read a file and return numbered lines. Use when you need to inspect file content or reference specific line numbers in a subsequent write. "+
				"Arguments: `file_path` (string, required), "+
				"`start_line` (int, optional, 1-based), `end_line` (int, optional). Returns up to 50,000 characters.",
			func(_ tool.Context, in ReadIn) (ReadOut, error) {
				out, _ := RunRead(context.Background(), in)
				return ReadOut{Content: out}, nil
			}),
		mustTool("write",
			"Write content to a file. Automatically snapshots the previous contents so you can revert. Creates parent directories if needed. "+
				"Arguments: `file_path` (string, required), `content` (string, required).",
			func(_ tool.Context, in WriteIn) (WriteOut, error) {
				out, _ := RunWrite(context.Background(), in)
				return WriteOut{Result: out}, nil
			}),
		mustTool("grep",
			"Search a regex pattern across files. Returns file paths and line numbers of matches. Use this rather than 'bash grep' for searching.",
			func(_ tool.Context, in GrepIn) (GrepOut, error) {
				out, _ := RunGrep(context.Background(), in)
				return GrepOut{Matches: out}, nil
			}),
		mustTool("glob",
			"Find files matching a glob pattern, e.g. '**/*.go'. Returns sorted matches.",
			func(_ tool.Context, in GlobIn) (GlobOut, error) {
				out, _ := RunGlob(context.Background(), in)
				return GlobOut{Files: out}, nil
			}),
		mustTool("revert",
			"Restore a file to its state before the last write call. Use when a write produced incorrect results. "+
				"Arguments: `file_path` (string, required).",
			func(_ tool.Context, in RevertIn) (RevertOut, error) {
				out, _ := RunRevert(context.Background(), in)
				return RevertOut{Result: out}, nil
			}),
	}
}

func mustTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build tool %s: %w", name, err))
	}
	return t
}
