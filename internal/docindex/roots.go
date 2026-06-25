package docindex

import (
	"os"
	"path/filepath"
	"strings"
)

// Roots returns the directories whose markdown files make up omnis's
// documentation corpus. The result is deduplicated and contains only
// directories that actually exist.
//
// When OMNIS_DOCS_DIRS is set (colon-separated) it replaces the auto-discovered
// set wholesale. Otherwise the candidates are, in priority order:
//
//   - <webDir>/docs        (the web UI user docs; webDir = OMNIS_WEB_DIR or "web")
//   - /usr/share/omnis/web/docs   (packaged web UI docs)
//   - ./docs               (developer docs in a source checkout)
//   - /usr/share/doc/omnis/docs   (packaged developer docs)
//   - <exeDir>/docs        (developer docs next to the binary)
func Roots() []string {
	if v := strings.TrimSpace(os.Getenv("OMNIS_DOCS_DIRS")); v != "" {
		return existingDirs(strings.Split(v, ":"))
	}

	webDir := strings.TrimSpace(os.Getenv("OMNIS_WEB_DIR"))
	if webDir == "" {
		webDir = "web"
	}

	candidates := []string{
		filepath.Join(webDir, "docs"),
		"/usr/share/omnis/web/docs",
		"docs",
		"/usr/share/doc/omnis/docs",
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "docs"))
	}
	return existingDirs(candidates)
}

// existingDirs resolves each path to an absolute directory, keeps only those
// that exist as directories, and deduplicates by resolved path (preserving
// first-seen order).
func existingDirs(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	var out []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if seen[abs] {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	return out
}
