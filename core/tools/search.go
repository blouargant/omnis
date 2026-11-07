package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GrepIn struct {
	Pattern    string `json:"pattern" jsonschema:"extended regular expression (or a literal string with literal=true)"`
	Path       string `json:"path,omitempty" jsonschema:"file or directory to search, default the current directory"`
	Glob       string `json:"glob,omitempty" jsonschema:"only search files matching this glob, e.g. *.go or src/**/*.ts; a pattern without / matches file names at any depth"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"case-insensitive search"`
	Literal    bool   `json:"literal,omitempty" jsonschema:"treat pattern as a literal string, not a regex"`
	Context    int    `json:"context,omitempty" jsonschema:"lines to show before and after each match, default 0"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum number of matches to return, default 100"`
	// Recursive is accepted for compatibility: a directory is always searched
	// recursively.
	Recursive bool `json:"recursive,omitempty"`
	// Cwd is the directory to search in. Set internally by the tool handler from
	// the session's working directory; excluded from the LLM-facing schema.
	Cwd string `json:"-"`
}
type GrepOut struct {
	Matches string `json:"matches"`
}

// Grep limits, after Pi's grep tool: a match count, a cap per line (a
// minified file or a lockfile is one huge line), and the usual byte cap.
const (
	grepDefaultLimit  = 100
	grepMaxLineLength = 500
)

// RunGrep searches like Pi's grep: inside a git work tree it runs `git grep`
// over tracked and untracked files, so .gitignore is respected (no
// node_modules, build output or vendored lockfiles) and binary files are
// skipped; elsewhere, or when git refuses (not a repository, a path outside
// it, an unsafe directory), it falls back to `grep -r`. A search of an
// explicit path that git finds nothing in is repeated with grep, so a file the
// agent names inside an ignored directory is still searched.
//
// The output stops after Limit matches (default 100), cuts lines at 500
// characters, and ends with a notice saying what was cut and how to get more.
// Lines are `path:line:text`, context lines `path-line-text`, groups separated
// by `--`, paths relative to the working directory.
func RunGrep(ctx context.Context, in GrepIn) (string, error) {
	path := in.Path
	if path == "" {
		path = "."
	}
	limit := in.Limit
	if limit <= 0 {
		limit = grepDefaultLimit
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	isMatch := grepMatcher(in)
	res, gitOK := runGrepCmd(cctx, in.Cwd, gitGrepArgs(in, path), limit, isMatch)
	explicit := path != "." && path != "./"
	if !gitOK || (res.matches == 0 && explicit) {
		res, _ = runGrepCmd(cctx, in.Cwd, plainGrepArgs(in, path), limit, isMatch)
	}
	if res.matches == 0 {
		if res.errText != "" {
			return "Error: " + res.errText, nil
		}
		return "(no matches)", nil
	}
	out := strings.Join(res.lines, "\n")
	var notices []string
	if res.limitHit {
		notices = append(notices, fmt.Sprintf("%d matches limit reached. Use limit=%d for more, or refine the pattern", limit, limit*2))
	}
	if res.longLines {
		notices = append(notices, fmt.Sprintf("Some lines truncated to %d chars. Use Read to see full lines", grepMaxLineLength))
	}
	if len(out) > MaxToolOutput {
		out = truncateAt(out, MaxToolOutput)
		if i := strings.LastIndexByte(out, '\n'); i > 0 {
			out = out[:i]
		}
		notices = append(notices, fmt.Sprintf("%d KB limit reached", MaxToolOutput/1000))
	}
	if len(notices) > 0 {
		out += "\n\n[" + strings.Join(notices, ". ") + "]"
	}
	return out, nil
}

func gitGrepArgs(in GrepIn, path string) []string {
	args := []string{"git", "--no-pager", "-c", "color.ui=never", "grep", "--untracked", "-n", "-I", "-z"}
	args = append(args, grepFlags(in)...)
	args = append(args, "-e", in.Pattern, "--")
	if in.Glob == "" {
		return append(args, path)
	}
	g := in.Glob
	if !strings.Contains(g, "/") {
		g = "**/" + g
	}
	if path == "." || path == "./" {
		return append(args, ":(glob)"+g)
	}
	return append(args, ":(glob)"+strings.TrimSuffix(filepath.ToSlash(path), "/")+"/"+g)
}

func plainGrepArgs(in GrepIn, path string) []string {
	args := []string{"grep", "-r", "-n", "-H", "-I", "-Z", "--exclude-dir=.git"}
	args = append(args, grepFlags(in)...)
	if in.Glob != "" {
		// grep matches --include against file names only.
		args = append(args, "--include="+filepath.Base(in.Glob))
	}
	return append(args, "-e", in.Pattern, "--", path)
}

func grepFlags(in GrepIn) []string {
	var f []string
	if in.Literal {
		f = append(f, "-F")
	} else {
		f = append(f, "-E")
	}
	if in.IgnoreCase {
		f = append(f, "-i")
	}
	if in.Context > 0 {
		f = append(f, "-C", strconv.Itoa(min(in.Context, 50)))
	}
	return f
}

// grepMatcher tells a match from a context line in `git grep -z` output,
// which puts a NUL after the line number of both. Without context every line
// is a match. With context the pattern is re-checked in Go; one Go cannot
// compile (a back-reference) counts every line, so the limit stops early
// rather than late.
func grepMatcher(in GrepIn) func(string) bool {
	if in.Context <= 0 {
		return func(string) bool { return true }
	}
	expr := in.Pattern
	if in.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	if in.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return func(string) bool { return true }
	}
	return re.MatchString
}

type grepResult struct {
	lines     []string
	matches   int
	limitHit  bool
	longLines bool
	errText   string
}

// runGrepCmd runs git or grep (argv[0]) and reads its NUL-separated output
// (see splitGrepLine) until the
// match limit, then stops the process. ok is false when the command could not
// search at all: missing binary, or exit status 2+ with no output (git: not
// a repository, path outside it; grep: bad pattern).
func runGrepCmd(ctx context.Context, cwd string, argv []string, limit int, isMatch func(string) bool) (grepResult, bool) {
	var res grepResult
	cctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return res, false
	}
	if err := cmd.Start(); err != nil {
		return res, false
	}
	r := bufio.NewReaderSize(stdout, 64*1024)
	lastFile := ""
	stopped := false
	for {
		raw, err := r.ReadString('\n')
		line := strings.TrimRight(raw, "\n")
		if line != "" {
			if line == "--" {
				if res.limitHit {
					break
				}
				res.lines = append(res.lines, line)
			} else if file, num, sep, text, ok := splitGrepLine(line); ok {
				if sep == 0 {
					sep = '-'
					if isMatch(text) {
						sep = ':'
					}
				}
				if res.limitHit && (sep == ':' || file != lastFile) {
					break // trailing context of the last match is complete
				}
				if len(text) > grepMaxLineLength {
					text = truncateAt(text, grepMaxLineLength) + "…"
					res.longLines = true
				}
				res.lines = append(res.lines, file+string(sep)+num+string(sep)+text)
				lastFile = file
				if sep == ':' {
					res.matches++
					if res.matches >= limit {
						res.limitHit = true
					}
				}
			}
		}
		if err != nil {
			break
		}
	}
	if res.limitHit {
		stopped = true
		stop()
	}
	werr := cmd.Wait()
	if stopped || werr == nil {
		return res, true
	}
	var ee *exec.ExitError
	if errors.As(werr, &ee) && ee.ExitCode() == 1 {
		return res, true // no matches
	}
	if res.matches > 0 {
		return res, true
	}
	res.errText = strings.TrimSpace(stderr.String())
	return res, false
}

// splitGrepLine parses `path\0NUM:text` (match) or `path\0NUM-text`
// (context), as grep -Z writes them, or `path\0NUM\0text` (git grep -z,
// sep 0: either).
func splitGrepLine(line string) (file, num string, sep byte, text string, ok bool) {
	i := strings.IndexByte(line, 0)
	if i < 0 {
		return "", "", 0, "", false
	}
	file, rest := line[:i], line[i+1:]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if j == 0 || j >= len(rest) || (rest[j] != ':' && rest[j] != '-' && rest[j] != 0) {
		return "", "", 0, "", false
	}
	return file, rest[:j], rest[j], rest[j+1:], true
}

func isDirIn(cwd, path string) bool {
	st, err := os.Stat(resolveAgainst(cwd, path))
	return err == nil && st.IsDir()
}

type GlobIn struct {
	Pattern string `json:"pattern" jsonschema:"glob pattern, e.g. **/*.go"`
	// Cwd is the directory the pattern is matched against. Set internally by the
	// tool handler from the session's working directory; excluded from the
	// LLM-facing schema. Matches are reported relative to it.
	Cwd string `json:"-"`
}
type GlobOut struct {
	Files string `json:"files"`
}

// RunGlob expands a glob pattern and returns a sorted, newline-separated
// list of matches. Supports ** by walking when the pattern starts with **/.
// When in.Cwd is set the pattern is matched against it and matches are reported
// relative to it (so the result reads as if run from that directory).
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
		_ = filepath.Walk(resolveAgainst(in.Cwd, root), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			if ok, _ := filepath.Match(suffix, filepath.Base(p)); ok {
				matches = append(matches, p)
			}
			return nil
		})
	} else {
		m, err := filepath.Glob(resolveAgainst(in.Cwd, in.Pattern))
		if err != nil {
			return fmt.Sprintf("Error: %v", err), nil
		}
		matches = m
	}
	// Report matches relative to the session cwd so the output reads as if the
	// glob were run from that directory (and stays usable by Read, which also
	// resolves relative paths against the same cwd).
	if in.Cwd != "" {
		for i, m := range matches {
			if rel, err := filepath.Rel(in.Cwd, m); err == nil && !strings.HasPrefix(rel, "..") {
				matches[i] = rel
			}
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return truncate(strings.Join(matches, "\n")), nil
}
