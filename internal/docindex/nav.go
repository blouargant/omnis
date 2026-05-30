package docindex

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// NewNavTools returns the always-available documentation navigation tools —
// list_docs, read_doc, grep_docs — scoped to the directories returned by
// rootsFn. They require no embedder, so they are the fallback path when
// semantic search_docs is unavailable. rootsFn is resolved lazily on each call
// so a packaged install and a source checkout both work without rebuilding.
func NewNavTools(rootsFn func() []string) []tool.Tool {
	if rootsFn == nil {
		rootsFn = Roots
	}
	var out []tool.Tool
	if t, err := functiontool.New(functiontool.Config{
		Name: "list_docs",
		Description: "List yoke's documentation files (markdown) across all doc roots, each with its " +
			"title and root-relative path. Use it to discover what documentation exists before reading. " +
			"No arguments.",
	}, func(_ tool.Context, _ listIn) (listOut, error) {
		return listDocs(rootsFn), nil
	}); err == nil {
		out = append(out, t)
	}
	if t, err := functiontool.New(functiontool.Config{
		Name: "read_doc",
		Description: "Read a yoke documentation file by its root-relative `path` (as returned by " +
			"list_docs or search_docs). Optional `start`/`end` 1-based line numbers read just a range. " +
			"Only files inside the documentation roots can be read.",
	}, func(_ tool.Context, in readIn) (readOut, error) {
		return readDoc(rootsFn, in)
	}); err == nil {
		out = append(out, t)
	}
	if t, err := functiontool.New(functiontool.Config{
		Name: "grep_docs",
		Description: "Search yoke's documentation for a regular expression `pattern` and return matching " +
			"lines with their file path and line number. A literal-text fallback to search_docs when no " +
			"embedder is configured. Optional `max` caps results (default 50).",
	}, func(_ tool.Context, in grepIn) (grepOut, error) {
		return grepDocs(rootsFn, in)
	}); err == nil {
		out = append(out, t)
	}
	return out
}

type listIn struct{}
type docEntry struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
}
type listOut struct {
	Docs []docEntry `json:"docs"`
}

func listDocs(rootsFn func() []string) listOut {
	idx := &Index{rootsFn: rootsFn}
	files := idx.listFiles()
	out := listOut{Docs: make([]docEntry, 0, len(files))}
	for _, f := range files {
		out.Docs = append(out.Docs, docEntry{Root: f.root, Path: f.rel, Title: docTitle(f.abs)})
	}
	return out
}

// docTitle returns the first ATX H1 (or the first heading of any level) found
// in the file, or "" if none.
func docTitle(abs string) string {
	file, err := os.Open(abs)
	if err != nil {
		return ""
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var firstHeading string
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(l[2:])
		}
		if firstHeading == "" && strings.HasPrefix(l, "#") {
			firstHeading = strings.TrimSpace(strings.TrimLeft(l, "#"))
		}
	}
	return firstHeading
}

type readIn struct {
	Path  string `json:"path"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}
type readOut struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func readDoc(rootsFn func() []string, in readIn) (readOut, error) {
	p := strings.TrimSpace(in.Path)
	if p == "" {
		return readOut{}, fmt.Errorf("path is required")
	}
	abs, root, err := resolveInRoots(rootsFn, p)
	if err != nil {
		return readOut{}, err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return readOut{}, err
	}
	rel, _ := filepath.Rel(root, abs)
	if in.Start <= 0 && in.End <= 0 {
		return readOut{Path: rel, Content: string(content)}, nil
	}
	lines := strings.Split(string(content), "\n")
	start := in.Start
	if start < 1 {
		start = 1
	}
	end := in.End
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		start = len(lines)
	}
	return readOut{Path: rel, Content: strings.Join(lines[start-1:end], "\n")}, nil
}

// resolveInRoots resolves a user-supplied path (root-relative or absolute)
// against the doc roots and verifies it does not escape any root. Returns the
// cleaned absolute path and the root it lives under.
func resolveInRoots(rootsFn func() []string, p string) (abs, root string, err error) {
	clean := filepath.Clean(p)
	for _, r := range rootsFn() {
		var cand string
		if filepath.IsAbs(clean) {
			cand = clean
		} else {
			cand = filepath.Join(r, clean)
		}
		rel, rerr := filepath.Rel(r, cand)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		if info, serr := os.Stat(cand); serr == nil && !info.IsDir() {
			return cand, r, nil
		}
	}
	return "", "", fmt.Errorf("doc not found within documentation roots: %s", p)
}

type grepIn struct {
	Pattern string `json:"pattern"`
	Max     int    `json:"max"`
}
type grepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}
type grepOut struct {
	Matches   []grepMatch `json:"matches"`
	Truncated bool        `json:"truncated,omitempty"`
}

func grepDocs(rootsFn func() []string, in grepIn) (grepOut, error) {
	pat := strings.TrimSpace(in.Pattern)
	if pat == "" {
		return grepOut{}, fmt.Errorf("pattern is required")
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return grepOut{}, fmt.Errorf("invalid pattern: %w", err)
	}
	max := in.Max
	if max <= 0 {
		max = 50
	}
	idx := &Index{rootsFn: rootsFn}
	var out grepOut
	for _, f := range idx.listFiles() {
		content, rerr := os.ReadFile(f.abs)
		if rerr != nil {
			continue
		}
		for n, line := range strings.Split(string(content), "\n") {
			if !re.MatchString(line) {
				continue
			}
			if len(out.Matches) >= max {
				out.Truncated = true
				return out, nil
			}
			out.Matches = append(out.Matches, grepMatch{Path: f.rel, Line: n + 1, Text: strings.TrimSpace(line)})
		}
	}
	sort.SliceStable(out.Matches, func(a, b int) bool {
		if out.Matches[a].Path != out.Matches[b].Path {
			return out.Matches[a].Path < out.Matches[b].Path
		}
		return out.Matches[a].Line < out.Matches[b].Line
	})
	return out, nil
}
