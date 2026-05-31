// Package shellcomplete provides bash-like tab completion for the interactive
// "!" shell-escape exposed by the TUI and web UI. It is intentionally a
// dependency-free, best-effort implementation (no shell subprocess): the
// first whitespace-delimited token completes against executable names found
// on $PATH, every other token (and any token containing a path separator)
// completes against the filesystem relative to a working directory.
package shellcomplete

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxCandidates caps the number of suggestions returned so a completion in a
// huge directory (or a bare command completion scanning all of $PATH) stays
// cheap and renders sanely.
const maxCandidates = 200

// Complete returns completion candidates for the final whitespace-delimited
// token of line, resolved against cwd. start is the byte offset in line where
// that token begins, so a caller can splice a candidate in via
// line[:start]+candidate. Each candidate is the full replacement token;
// directories carry a trailing "/".
//
// The leading "!" must already be stripped from line. cwd may be empty, in
// which case the process working directory is used for relative paths.
func Complete(line, cwd string) (start int, candidates []string) {
	start = tokenStart(line)
	token := line[start:]
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	// First token with no path separator → command-name completion. A token
	// that looks like a path (contains "/", or starts with "." or "~") is
	// completed as a path even in command position (e.g. "./script").
	firstToken := strings.TrimSpace(line[:start]) == ""
	looksLikePath := strings.ContainsRune(token, '/') ||
		strings.HasPrefix(token, ".") || strings.HasPrefix(token, "~")
	if firstToken && !looksLikePath {
		return start, completeCommands(token)
	}
	return start, completePaths(token, cwd)
}

// CompletePath returns filesystem completion candidates for a single path
// token — the text after a chat "@" reference — resolved against cwd. Unlike
// Complete it never falls back to $PATH command completion (an "@" reference is
// always a path). Each candidate is the full replacement token; directories
// carry a trailing "/".
func CompletePath(token, cwd string) []string {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	return completePaths(token, cwd)
}

// tokenStart returns the byte offset of the last unquoted whitespace-delimited
// token. Quoting is not interpreted (best-effort, matching the lightweight
// nature of this completer).
func tokenStart(line string) int {
	i := strings.LastIndexAny(line, " \t")
	if i < 0 {
		return 0
	}
	return i + 1
}

// completeCommands returns executable names on $PATH that start with prefix.
func completeCommands(prefix string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			if e.IsDir() {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Mode()&0o111 == 0 {
				continue // not executable
			}
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return limit(out)
}

// completePaths returns filesystem entries matching token, resolved against
// cwd. The directory portion the user typed (including "~/", "./", or an
// absolute prefix) is preserved on each candidate so only the leaf is
// completed; directories get a trailing "/".
func completePaths(token, cwd string) []string {
	dirPart, filePrefix := splitPath(token)

	// Resolve the directory to read, expanding "~" and joining relatives onto
	// cwd, without altering dirPart (which is echoed back verbatim).
	readDir := expandTilde(dirPart)
	if readDir == "" {
		readDir = cwd
	} else if !filepath.IsAbs(readDir) {
		readDir = filepath.Join(cwd, readDir)
	}

	entries, err := os.ReadDir(readDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, filePrefix) {
			continue
		}
		// Skip dotfiles unless the user is explicitly typing one.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(filePrefix, ".") {
			continue
		}
		cand := dirPart + name
		if e.IsDir() {
			cand += "/"
		}
		out = append(out, cand)
	}
	sort.Strings(out)
	return limit(out)
}

// splitPath divides token into the directory prefix (kept verbatim, including
// the trailing slash) and the leaf prefix to match. "src/fo" → ("src/", "fo");
// "fo" → ("", "fo"); "src/" → ("src/", "").
func splitPath(token string) (dirPart, filePrefix string) {
	i := strings.LastIndex(token, "/")
	if i < 0 {
		return "", token
	}
	return token[:i+1], token[i+1:]
}

// expandTilde replaces a leading "~/" (or a bare "~") with the user's home
// directory. Other "~user" forms are left untouched.
func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

// limit truncates s to maxCandidates entries.
func limit(s []string) []string {
	if len(s) > maxCandidates {
		return s[:maxCandidates]
	}
	return s
}
