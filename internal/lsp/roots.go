// roots.go — workspace-root detection. A language server is initialized against
// the project root, not the file's directory, so it can resolve imports and
// analyse the whole module. The root is found by walking up from a file's
// directory until a configured marker (go.mod, Cargo.toml, package.json, …) is
// seen. A polyglot monorepo with several markers yields several roots, each
// keyed independently by the manager — which is what makes "polyglot" fall out.
package lsp

import (
	"os"
	"path/filepath"
)

// DetectRoot walks up from startDir looking for a directory containing any of
// markers, returning the first match. When no marker is found up to the
// filesystem root — or markers is empty — it falls back to startDir. startDir
// should be absolute.
func DetectRoot(startDir string, markers []string) string {
	if len(markers) == 0 {
		return startDir
	}
	dir := startDir
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached filesystem root
			return startDir
		}
		dir = parent
	}
}
