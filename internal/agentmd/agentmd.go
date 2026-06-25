// Package agentmd implements omnis's AGENT.md project-memory feature — the
// equivalent of Claude Code's CLAUDE.md. AGENT.md files are discovered across
// the config layers and the project tree, concatenated, and injected into the
// leader/root agent's system instruction at turn time (resolved against the
// session's working directory). The package also owns the shared "/init"
// bootstrap prompt and the "#" quick-memory append used by every surface
// (web UI, TUI, CLI).
package agentmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/paths"
)

// FileName is the project-memory filename, mirroring Claude Code's CLAUDE.md.
const FileName = "AGENT.md"

// Resolve discovers and concatenates every AGENT.md visible from cwd and
// returns a single rendered block ready to prepend to a system instruction, or
// "" when no AGENT.md exists anywhere (a zero-cost no-op).
//
// Files are concatenated in ascending precedence — lowest-priority first, so
// the most specific guidance (the project file closest to cwd) appears last:
//
//  1. System:      <system-config-dir>/AGENT.md   (/etc/omnis by default)
//  2. User global: $OMNIS_HOME/AGENT.md
//  3. .agents/:    AGENT.md in each project-local config dir
//  4. Project:     AGENT.md from the repo root down to cwd (ancestors first)
//
// Results are cached per cwd and re-rendered only when a contributing file's
// size or mtime changes, so calling this on every turn is cheap.
func Resolve(cwd string) string {
	files := discover(cwd)
	if len(files) == 0 {
		return ""
	}
	sig := signature(files)
	if cached, ok := cacheGet(cwd, sig); ok {
		return cached
	}
	out := render(files)
	cachePut(cwd, sig, out)
	return out
}

// discover returns the existing AGENT.md paths in ascending precedence order,
// deduplicated by absolute path (first occurrence wins its slot).
func discover(cwd string) []string {
	var candidates []string
	// 1. system
	if sys := strings.TrimSpace(paths.SystemDir()); sys != "" {
		candidates = append(candidates, filepath.Join(sys, FileName))
	}
	// 2. user global
	candidates = append(candidates, filepath.Join(paths.Home(), FileName))
	// 3. .agents/ (and agents/) layer
	for _, d := range paths.LocalDirs() {
		candidates = append(candidates, filepath.Join(d, FileName))
	}
	// 4. project walk-up: repo root → cwd (ancestors first, cwd last)
	candidates = append(candidates, projectChain(cwd)...)

	seen := make(map[string]struct{}, len(candidates))
	var out []string
	for _, p := range candidates {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if _, dup := seen[abs]; dup {
			continue
		}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			seen[abs] = struct{}{}
			out = append(out, abs)
		}
	}
	return out
}

// projectChain returns candidate AGENT.md paths from the repo root down to cwd
// (ancestors first). The repo root is the nearest ancestor containing a .git
// entry; with no repo it is just cwd itself.
func projectChain(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		c, err := os.Getwd()
		if err != nil {
			return nil
		}
		cwd = c
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	root := repoRoot(abs)
	// Build the dir list from root down to abs.
	var dirs []string
	for d := abs; ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if d == root || d == filepath.Dir(d) {
			break
		}
	}
	// dirs is cwd→root; reverse to root→cwd.
	out := make([]string, 0, len(dirs))
	for i := len(dirs) - 1; i >= 0; i-- {
		out = append(out, filepath.Join(dirs[i], FileName))
	}
	return out
}

// repoRoot returns the topmost ancestor of dir (inclusive) that contains a
// .git entry, or dir itself when none is found.
func repoRoot(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return dir
}

// render reads each file and wraps the concatenation in a stable container so
// the model can tell project memory apart from the agent's own instruction.
func render(files []string) string {
	var b strings.Builder
	b.WriteString("<project-context source=\"AGENT.md\">\n")
	b.WriteString("The following project memory was loaded from AGENT.md files. " +
		"Treat it as authoritative project guidance.\n")
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		body := strings.TrimRight(string(data), "\n")
		if strings.TrimSpace(body) == "" {
			continue
		}
		fmt.Fprintf(&b, "\n# From %s\n%s\n", p, body)
	}
	b.WriteString("</project-context>")
	return b.String()
}

// signature builds a cheap change key from the contributing files' paths,
// sizes, and mtimes.
func signature(files []string) string {
	var b strings.Builder
	for _, p := range files {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "%s|%d|%d\n", p, st.Size(), st.ModTime().UnixNano())
	}
	return b.String()
}

// ── per-cwd render cache ────────────────────────────────────────────────

type cacheEntry struct {
	sig  string
	text string
}

var (
	cacheMu sync.RWMutex
	cache   = map[string]cacheEntry{}
)

func cacheGet(cwd, sig string) (string, bool) {
	cacheMu.RLock()
	e, ok := cache[cwd]
	cacheMu.RUnlock()
	if ok && e.sig == sig {
		return e.text, true
	}
	return "", false
}

func cachePut(cwd, sig, text string) {
	cacheMu.Lock()
	cache[cwd] = cacheEntry{sig: sig, text: text}
	cacheMu.Unlock()
}
