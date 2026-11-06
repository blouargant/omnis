package tools

import (
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// fileLocks holds one mutex per file (keyed by its resolved absolute path).
// ADK runs every function call of one model response concurrently, so two
// Edits of the same file in one response would otherwise both read the old
// content and the second write would silently drop the first edit.
var fileLocks sync.Map // string → *sync.Mutex

func fileLockKey(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return filepath.Clean(abs)
}

// LockFiles serialises mutations of the given files: it blocks until it holds
// every file's lock and returns the function that releases them. Locks are
// taken in a fixed (sorted) order so two multi-file callers cannot deadlock.
// Different files still mutate in parallel.
func LockFiles(paths ...string) (unlock func()) {
	seen := map[string]bool{}
	keys := make([]string, 0, len(paths))
	for _, p := range paths {
		k := fileLockKey(p)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	held := make([]*sync.Mutex, 0, len(keys))
	for _, k := range keys {
		v, _ := fileLocks.LoadOrStore(k, &sync.Mutex{})
		m := v.(*sync.Mutex)
		m.Lock()
		held = append(held, m)
	}
	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Unlock()
		}
	}
}

// matchRange is a [start, end) byte range of the original content.
type matchRange struct{ start, end int }

// findEditMatches locates oldText in content. It tries an exact match first;
// when there is none it retries on a normalized view of both texts (line
// endings → LF, trailing whitespace stripped per line, typographic quotes,
// dashes and special spaces mapped to ASCII) and maps the hits back to byte
// ranges of the ORIGINAL content, so everything outside a replaced range stays
// byte-identical. Matches are non-overlapping, left to right.
func findEditMatches(content, oldText string) (ranges []matchRange, normalized bool) {
	if oldText == "" {
		return nil, false
	}
	for off := 0; ; {
		i := strings.Index(content[off:], oldText)
		if i < 0 {
			break
		}
		start := off + i
		ranges = append(ranges, matchRange{start, start + len(oldText)})
		off = start + len(oldText)
	}
	if len(ranges) > 0 {
		return ranges, false
	}
	norm, starts, ends := normalizeWithMap(content)
	needle, _, _ := normalizeWithMap(oldText)
	if needle == "" {
		return nil, false
	}
	for off := 0; ; {
		i := strings.Index(norm[off:], needle)
		if i < 0 {
			break
		}
		s := off + i
		e := s + len(needle)
		ranges = append(ranges, matchRange{starts[s], ends[e-1]})
		off = e
	}
	return ranges, len(ranges) > 0
}

// applyEditMatches replaces every range with newText, from the last range to
// the first so earlier offsets stay valid. On a normalized match the
// replacement adopts the file's line endings: a model writes LF even when the
// file on disk is CRLF.
func applyEditMatches(content string, ranges []matchRange, newText string, normalized bool) string {
	if normalized && usesCRLF(content) {
		newText = strings.ReplaceAll(strings.ReplaceAll(newText, "\r\n", "\n"), "\n", "\r\n")
	}
	for i := len(ranges) - 1; i >= 0; i-- {
		r := ranges[i]
		content = content[:r.start] + newText + content[r.end:]
	}
	return content
}

func usesCRLF(s string) bool {
	crlf := strings.Count(s, "\r\n")
	return crlf > 0 && crlf*2 >= strings.Count(s, "\n")
}

// normalizeWithMap returns the normalized text and, for each of its bytes, the
// [start, end) range in s of the source it came from. A line's trailing
// whitespace (including a CR before LF) is dropped; the LF that ends the line
// then covers that dropped run too, so a match spanning a line break replaces
// the whole break.
func normalizeWithMap(s string) (string, []int, []int) {
	var out strings.Builder
	starts := make([]int, 0, len(s))
	ends := make([]int, 0, len(s))
	emit := func(text string, from, to int) {
		out.WriteString(text)
		for range len(text) {
			starts = append(starts, from)
			ends = append(ends, to)
		}
	}
	for i := 0; i <= len(s); {
		lineEnd := len(s)
		if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
			lineEnd = i + j
		}
		k := lineEnd
		for k > i {
			r, size := utf8.DecodeLastRuneInString(s[i:k])
			if !unicode.IsSpace(r) && !isSpecialSpace(r) {
				break
			}
			k -= size
		}
		for p := i; p < k; {
			r, size := utf8.DecodeRuneInString(s[p:])
			emit(foldRune(r, s[p:p+size]), p, p+size)
			p += size
		}
		if lineEnd == len(s) {
			break
		}
		emit("\n", k, lineEnd+1)
		i = lineEnd + 1
	}
	return out.String(), starts, ends
}

func isSpecialSpace(r rune) bool {
	switch {
	case r == ' ', r == ' ', r == ' ', r == '　', r >= ' ' && r <= ' ':
		return true
	}
	return false
}

// foldRune maps the typographic characters models routinely substitute (or
// that editors substitute in files) to their ASCII form.
func foldRune(r rune, raw string) string {
	switch r {
	case '‘', '’', '‚', '‛', '′':
		return "'"
	case '“', '”', '„', '‟', '″':
		return `"`
	case '‐', '‑', '‒', '–', '—', '―', '−':
		return "-"
	case '…':
		return "..."
	}
	if isSpecialSpace(r) {
		return " "
	}
	return raw
}
