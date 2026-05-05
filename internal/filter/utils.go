package filter

import "strings"

var ansiRe = NewLazyRegex(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes ANSI escape codes from s.
func StripANSI(s string) string {
	return ansiRe.Re().ReplaceAllString(s, "")
}

// CompactPath strips common prefixes like src/, lib/, internal/ from a path.
func CompactPath(path string) string {
	prefixes := []string{"src/", "lib/", "internal/", "pkg/", "vendor/"}
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return path[len(p):]
		}
	}
	return path
}
