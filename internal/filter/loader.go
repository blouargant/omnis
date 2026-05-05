package filter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultRulesDir is the repository-local rules directory used by tools.
const DefaultRulesDir = "config/filters"

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")
}

// LoadDir loads all YAML filters from dir. Missing directories return no filters.
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
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
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
