package filter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blouargant/omnis/internal/paths"
)

// DefaultRulesDir returns the rules directory used by tools. Resolved
// through the same 3-layer config search as other config files
// (.agents/filters → $OMNIS_HOME/filters → /etc/omnis/filters), so a
// packaged install and a developer checkout both work.
func DefaultRulesDir() string { return paths.FindConfigDir("filters") }

func isJSONFile(name string) bool {
	return strings.HasSuffix(name, ".json")
}

// LoadDir loads all JSON filters from dir. Missing directories return no filters.
func LoadDir(dir string) ([]Filter, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read filter dir: %w", err)
	}

	var filters []Filter
	for _, entry := range entries {
		if entry.IsDir() || !isJSONFile(entry.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read filter %s: %w", entry.Name(), err)
		}
		f, err := ParseFilter(data)
		if err != nil {
			continue
		}
		filters = append(filters, *f)
	}

	return filters, nil
}
