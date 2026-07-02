// Package astgrep wires ast-grep (https://ast-grep.github.io) — a tree-sitter
// based structural search-and-rewrite tool — as two agent tools:
//
//   - ast_grep_search  : find code by structural PATTERN (not text), read-only.
//   - ast_grep_rewrite : rewrite every structural match in one call, revertible.
//
// Structural matching is the token-efficient way to do the "change every call
// site of X" / "find every `foo(a, b)`" class of task: instead of grepping,
// reading each hit, and issuing N exact-string edits (whose literal strings
// cost ~2× the changed bytes), one pattern+rewrite runs as a single call whose
// result is a diffstat. lsp_rename covers only the rename special case; this
// covers arbitrary structural codemods.
//
// ast-grep is an optional external binary. It is enforced through the same
// dependency gate as skills/MCP/LSP `requires` (SetDepGate): the first call
// checks the binary is on PATH and, if missing, asks the user to install it
// (pipx on Linux, brew on macOS, npm on Windows) and rechecks. When it stays
// unavailable the tools report that and the agent falls back to Grep + Edit —
// the additive/no-op contract every recall feature honours.
package astgrep

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/deps"
)

// binary is the ast-grep executable name. The `sg` short alias collides with the
// coreutils setgid `sg` on Linux, so we always use the long form, which every
// distribution channel (pipx `ast-grep-cli`, npm `@ast-grep/cli`, brew) installs.
const binary = "ast-grep"

// runTimeout bounds a single ast-grep invocation.
const runTimeout = 60 * time.Second

// Requirement is the dependency descriptor the gate enforces. pipx is the clean
// Linux default (isolated, and ~/.local/bin is now guaranteed on PATH by
// internal/binpath); brew on macOS; npm on Windows.
func Requirement() deps.Requirement {
	return deps.Requirement{
		Command: binary,
		Label:   "ast-grep (structural search/rewrite)",
		Install: deps.Install{
			PerOS: map[string]string{
				"linux":   "pipx install ast-grep-cli",
				"darwin":  "brew install ast-grep",
				"windows": "npm install -g @ast-grep/cli",
			},
			Default: "npm install -g @ast-grep/cli",
		},
	}
}

// DepGate mirrors the skills gate: it returns "" when ast-grep is available (or
// was just installed) and a model-facing notice otherwise. Installed once,
// process-wide, from the agent layer.
type DepGate func(tc tool.Context) string

var gate DepGate

// SetDepGate installs the process-wide dependency gate. A nil gate disables
// gating (the tools then only do a plain PATH check).
func SetDepGate(g DepGate) { gate = g }

// ensureDep runs the gate (or a plain PATH check when no gate is wired) and
// returns a non-empty notice when ast-grep is unavailable.
func ensureDep(tc tool.Context) string {
	if gate != nil {
		return gate(tc)
	}
	if !deps.Present(binary) {
		return "ast-grep is not installed — install it (`" + Requirement().Install.Command() +
			"`) or use Grep + Edit instead."
	}
	return ""
}

// Tools returns the ast_grep_* tool set. Mounted by the "astgrep" tool group.
func Tools() []tool.Tool {
	return []tool.Tool{searchTool(), rewriteTool()}
}

type searchIn struct {
	Pattern string `json:"pattern"`
	Lang    string `json:"lang"`
	Path    string `json:"path,omitempty"`
	Max     int    `json:"max,omitempty"`
}
type rewriteIn struct {
	Pattern string `json:"pattern"`
	Rewrite string `json:"rewrite"`
	Lang    string `json:"lang"`
	Path    string `json:"path,omitempty"`
	DryRun  bool   `json:"dry_run,omitempty"`
}
type out struct {
	Result string `json:"result"`
}

func searchTool() tool.Tool {
	return newTool("ast_grep_search",
		"Structurally search code with an ast-grep PATTERN (syntax-aware, not text): e.g. `foo($A, $B)` finds every "+
			"two-argument call to foo regardless of spacing/formatting, `$X == nil` finds nil comparisons. Metavariables "+
			"are `$NAME` (one node) or `$$$NAME` (a list). Read-only. Prefer this over Grep for code-shaped queries. "+
			"Arguments: `pattern` (string, required) — the ast-grep pattern; `lang` (string, required) — the language "+
			"(`go`, `rust`, `typescript`, `tsx`, `javascript`, `python`, `java`, `c`, `cpp`, …); `path` (string, optional) "+
			"— file or directory to search, default the session directory; `max` (int, optional) — cap the number of "+
			"matches shown (default 100).",
		func(ctx tool.Context, in searchIn) (out, error) {
			if notice := ensureDep(ctx); notice != "" {
				return out{Result: notice}, nil
			}
			if strings.TrimSpace(in.Pattern) == "" || strings.TrimSpace(in.Lang) == "" {
				return out{Result: "ast_grep_search: `pattern` and `lang` are both required."}, nil
			}
			cwd := fstools.CwdForContext(ctx)
			args := []string{"run", "--pattern", in.Pattern, "--lang", in.Lang, "--json"}
			args = append(args, targetArg(in.Path))
			matches, stderr, err := runAstGrep(ctx, cwd, args)
			if err != nil {
				return out{Result: astGrepErr(err, stderr)}, nil
			}
			return out{Result: formatMatches(matches, cwd, in.Max)}, nil
		})
}

func rewriteTool() tool.Tool {
	return newTool("ast_grep_rewrite",
		"Structurally rewrite every match of an ast-grep PATTERN to a REWRITE template in one call — the efficient way "+
			"to do a mechanical multi-site refactor (add an argument to every call, wrap a call, replace an API) instead "+
			"of Grep + read-each-site + many exact-string Edits. Reuse the pattern's `$NAME` / `$$$NAME` metavariables in "+
			"the rewrite. Changes are written to disk through the snapshot path, so they are revertible. Always dry-run "+
			"first (`dry_run: true`) to see the match count and sample changes, then apply. "+
			"Arguments: `pattern` (string, required); `rewrite` (string, required) — the replacement template; "+
			"`lang` (string, required); `path` (string, optional) — file or directory, default the session directory; "+
			"`dry_run` (bool, optional) — when true, report what would change without writing.",
		func(ctx tool.Context, in rewriteIn) (out, error) {
			if notice := ensureDep(ctx); notice != "" {
				return out{Result: notice}, nil
			}
			if strings.TrimSpace(in.Pattern) == "" || strings.TrimSpace(in.Rewrite) == "" || strings.TrimSpace(in.Lang) == "" {
				return out{Result: "ast_grep_rewrite: `pattern`, `rewrite` and `lang` are all required."}, nil
			}
			cwd := fstools.CwdForContext(ctx)
			// Always compute replacements via --json (no --update-all): we apply
			// them ourselves through the snapshot Write path so the change is
			// revertible and consistent with the rest of omnis. dry_run just skips
			// the write.
			args := []string{"run", "--pattern", in.Pattern, "--rewrite", in.Rewrite, "--lang", in.Lang, "--json"}
			args = append(args, targetArg(in.Path))
			matches, stderr, err := runAstGrep(ctx, cwd, args)
			if err != nil {
				return out{Result: astGrepErr(err, stderr)}, nil
			}
			return out{Result: applyRewrites(matches, cwd, !in.DryRun)}, nil
		})
}

// targetArg returns the path to scan, defaulting to "." (the process runs with
// cmd.Dir = the session cwd, so "." is the session directory).
func targetArg(p string) string {
	if strings.TrimSpace(p) == "" {
		return "."
	}
	return p
}

// sgMatch is one ast-grep JSON match. `replacement` is present only with
// --rewrite; `range.byteOffset` gives the exact splice bounds.
type sgMatch struct {
	Text        string `json:"text"`
	File        string `json:"file"`
	Lines       string `json:"lines"`
	Replacement string `json:"replacement"`
	Range       struct {
		ByteOffset struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"byteOffset"`
		Start struct {
			Line   int `json:"line"`
			Column int `json:"column"`
		} `json:"start"`
	} `json:"range"`
}

// runAstGrep executes ast-grep with explicit argv (no shell — pattern/rewrite
// are separate args, so nothing is interpolated) in dir, and parses its JSON
// output. It tolerates both the array form (`--json`) and newline-delimited form
// (`--json=stream`). Returns the matches plus captured stderr.
func runAstGrep(ctx context.Context, dir string, args []string) ([]sgMatch, string, error) {
	cctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, binary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	matches, perr := parseMatches(stdout.Bytes())
	if perr != nil && runErr != nil {
		// A real failure (bad lang, ast-grep missing at runtime) — surface it.
		return nil, stderr.String(), runErr
	}
	if perr != nil {
		return nil, stderr.String(), perr
	}
	return matches, stderr.String(), nil
}

// parseMatches decodes ast-grep JSON output as either a single array or one
// object per line (stream mode). Empty output yields no matches.
func parseMatches(b []byte) ([]sgMatch, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var arr []sgMatch
		if err := json.Unmarshal(trimmed, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var arr []sgMatch
	for _, line := range bytes.Split(trimmed, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m sgMatch
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		arr = append(arr, m)
	}
	return arr, nil
}

// formatMatches renders search hits as "file:line:col: <text>" (grep-style),
// one per line, capped at max (default 100).
func formatMatches(matches []sgMatch, cwd string, max int) string {
	if len(matches) == 0 {
		return "No structural matches."
	}
	if max <= 0 {
		max = 100
	}
	var b strings.Builder
	shown := 0
	for _, m := range matches {
		if shown >= max {
			break
		}
		fmt.Fprintf(&b, "%s:%d:%d: %s\n", displayPath(m.File, cwd),
			m.Range.Start.Line+1, m.Range.Start.Column+1, firstLine(m.Text))
		shown++
	}
	if len(matches) > shown {
		fmt.Fprintf(&b, "… %d more match(es) not shown (raise `max` to see them).", len(matches)-shown)
	} else {
		fmt.Fprintf(&b, "%d match(es).", len(matches))
	}
	return strings.TrimRight(b.String(), "\n")
}

// applyRewrites reconstructs each touched file from ast-grep's per-match
// replacements and (when apply) writes it through the snapshot path so the
// change is revertible. Matches are spliced back-to-front per file so earlier
// byte offsets stay valid. Returns a summary; in dry-run it lists sample hunks.
func applyRewrites(matches []sgMatch, cwd string, apply bool) string {
	byFile := map[string][]sgMatch{}
	var order []string
	total := 0
	for _, m := range matches {
		if m.Range.ByteOffset.End < m.Range.ByteOffset.Start {
			continue
		}
		if _, ok := byFile[m.File]; !ok {
			order = append(order, m.File)
		}
		byFile[m.File] = append(byFile[m.File], m)
		total++
	}
	if total == 0 {
		return "No structural matches — nothing to rewrite."
	}

	var samples []string
	changedFiles := 0
	var failures []string
	for _, file := range order {
		abs := file
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, file)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", displayPath(file, cwd), err))
			continue
		}
		content := string(data)
		ms := byFile[file]
		sort.Slice(ms, func(i, j int) bool {
			return ms[i].Range.ByteOffset.Start > ms[j].Range.ByteOffset.Start
		})
		ok := true
		for _, m := range ms {
			s, e := m.Range.ByteOffset.Start, m.Range.ByteOffset.End
			if s < 0 || e > len(content) || s > e {
				ok = false
				break
			}
			if len(samples) < 3 {
				samples = append(samples, fmt.Sprintf("  %s:%d  %s → %s",
					displayPath(file, cwd), m.Range.Start.Line+1, firstLine(m.Text), firstLine(m.Replacement)))
			}
			content = content[:s] + m.Replacement + content[e:]
		}
		if !ok {
			failures = append(failures, displayPath(file, cwd)+": stale offsets, skipped")
			continue
		}
		if string(data) == content {
			continue
		}
		changedFiles++
		if apply {
			if _, werr := fstools.RunWrite(context.Background(), fstools.WriteIn{Path: abs, Content: content}); werr != nil {
				failures = append(failures, fmt.Sprintf("%s: write failed: %v", displayPath(file, cwd), werr))
			}
		}
	}

	var b strings.Builder
	verb := "Applied"
	tail := " (written to disk, revertible)."
	if !apply {
		verb = "Would apply"
		tail = " (dry run — nothing written; re-run without dry_run to apply)."
	}
	fmt.Fprintf(&b, "%s %d rewrite(s) across %d file(s)%s\n", verb, total, changedFiles, tail)
	if len(samples) > 0 {
		b.WriteString("Sample changes:\n")
		b.WriteString(strings.Join(samples, "\n"))
		b.WriteString("\n")
	}
	if len(failures) > 0 {
		b.WriteString("Skipped:\n  " + strings.Join(failures, "\n  "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// astGrepErr renders an execution failure with any captured stderr tail.
func astGrepErr(err error, stderr string) string {
	msg := "ast-grep: " + err.Error()
	if s := strings.TrimSpace(stderr); s != "" {
		msg += "\n" + firstLine(s)
	}
	return msg
}

func firstLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i]) + " …"
	}
	return strings.TrimSpace(s)
}

// displayPath makes p relative to cwd when it stays within the tree.
func displayPath(p, cwd string) string {
	if cwd == "" || p == "" {
		return p
	}
	abs := p
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, p)
	}
	if rel, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}

func newTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build astgrep tool %s: %w", name, err))
	}
	return t
}
