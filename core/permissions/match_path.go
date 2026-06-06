package permissions

import (
	"path/filepath"
	"regexp"
	"strings"
)

// globToRegex converts a gitignore-style path glob to a regexp body (no
// anchors). "**" crosses directory separators, "*" and "?" do not.
//
//	**/   → (?:.*/)?    (any number of leading directories, including none)
//	**    → .*
//	*     → [^/]*
//	?     → [^/]
func globToRegex(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch {
		case strings.HasPrefix(glob[i:], "**/"):
			b.WriteString(`(?:.*/)?`)
			i += 2
		case strings.HasPrefix(glob[i:], "**"):
			b.WriteString(`.*`)
			i++
		case glob[i] == '*':
			b.WriteString(`[^/]*`)
		case glob[i] == '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(glob[i : i+1]))
		}
	}
	return b.String()
}

// pathSpecRegex compiles a Read/Edit/Write specifier into an anchored regexp
// over an absolute filesystem path, given the resolution roots. Returns nil
// when the spec is bare (matches all paths) — callers treat nil as "match".
func pathSpecRegex(arg, cwd, projectRoot, home string) *regexp.Regexp {
	if projectRoot == "" {
		projectRoot = cwd
	}
	pat := arg
	var full string
	switch {
	case strings.HasPrefix(pat, "//"):
		// Absolute path from the filesystem root: //Users/x → /Users/x.
		full = pat[1:]
	case strings.HasPrefix(pat, "~/"):
		full = joinGlob(home, pat[2:])
	case strings.HasPrefix(pat, "/"):
		full = joinGlob(projectRoot, strings.TrimPrefix(pat, "/"))
	default:
		pat = strings.TrimPrefix(pat, "./")
		if !strings.Contains(pat, "/") {
			// Bare filename: gitignore semantics — match at any depth under cwd.
			full = joinGlob(cwd, "**/"+pat)
		} else {
			full = joinGlob(cwd, pat)
		}
	}
	re, err := regexp.Compile("(?s)^" + globToRegex(full) + "$")
	if err != nil {
		return nil
	}
	return re
}

// joinGlob joins a base directory with a glob tail without letting
// filepath.Clean collapse the glob wildcards. base is cleaned; tail is kept
// verbatim (so "**" / "*" survive).
func joinGlob(base, tail string) string {
	base = strings.TrimRight(filepath.Clean(base), "/")
	tail = strings.TrimPrefix(tail, "/")
	if base == "" || base == "." {
		return "/" + tail
	}
	return base + "/" + tail
}

// resolveAbsPath resolves a tool's file_path argument to an absolute path
// against the session cwd.
func resolveAbsPath(filePath, cwd string) string {
	if filePath == "" {
		return ""
	}
	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}
	if cwd == "" {
		return filepath.Clean(filePath)
	}
	return filepath.Join(cwd, filePath)
}

// pathMatch reports whether a path spec matches the given file path. A bare
// spec (Read/Edit/Write with no specifier) matches every path.
func (s *Spec) pathMatch(filePath, cwd, projectRoot, home string) bool {
	if s.Bare {
		return true
	}
	if s.Arg == "" {
		return false
	}
	abs := resolveAbsPath(filePath, cwd)
	if abs == "" {
		return false
	}
	re := pathSpecRegex(s.Arg, cwd, projectRoot, home)
	if re == nil {
		return false
	}
	if re.MatchString(abs) {
		return true
	}
	// Deny rules also match the symlink target (best-effort); harmless for
	// allow/ask since a non-resolving path just falls through.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
		return re.MatchString(resolved)
	}
	return false
}
