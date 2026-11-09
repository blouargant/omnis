package collmem

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	legacyName = "imported-notes"
	legacyDesc = "Notes carried over from the previous collection memory"
)

var blankLinesRE = regexp.MustCompile(`\n\s*\n`)

// migrateLocks serialise the legacy memory.md migration per memory directory,
// process-wide: the agent plugin (every model call of every root) and the server
// routes (list / save / delete / count) all reach a first migration, possibly at
// the same time. Unguarded, a failing call's cleanup (RemoveAll of the memory
// dir) could wipe what a concurrent call had just imported and whose memory.md
// it had removed. Keyed by directory, so two Stores on one root share a lock.
var (
	migrateLocksMu sync.Mutex
	migrateLocks   = map[string]*sync.Mutex{}
)

// EnsureMigrated imports the collection's legacy memory.md (see MigrateLegacy)
// under the per-directory lock. Call it before any read or write of a named
// collection's memories: the first Save creates the memory directory, after
// which MigrateLegacy never imports the legacy file. Errors are logged and
// swallowed (the legacy file stays in place and the next call retries). Once
// the memory directory exists this costs one stat.
func (s *Store) EnsureMigrated(collection string) {
	if collection == "" {
		return
	}
	dir := s.Dir(collection)
	if dir == "" {
		return
	}
	migrateLocksMu.Lock()
	l, ok := migrateLocks[dir]
	if !ok {
		l = &sync.Mutex{}
		migrateLocks[dir] = l
	}
	migrateLocksMu.Unlock()
	l.Lock()
	defer l.Unlock()
	if _, err := s.MigrateLegacy(collection); err != nil {
		log.Printf("collection memory: migrating legacy memory.md of %q: %v", collection, err)
	}
}

// MigrateLegacy converts <root>/collections/<collection>/memory.md into one memory
// named "imported-notes" when the memory directory does not exist yet, then removes
// memory.md and memory.prev.md. Returns true when it converted something.
//
// A text longer than MaxBodyChars is split on paragraph boundaries into
// imported-notes, imported-notes-2, …; a single paragraph longer than the limit
// is cut at the limit.
//
// MigrateLegacy takes no lock; callers use EnsureMigrated.
func (s *Store) MigrateLegacy(collection string) (bool, error) {
	if collection == "" {
		return false, nil
	}
	seg := safeSegment(collection)
	if seg == "" {
		return false, nil
	}
	memDir := s.Dir(collection)
	if _, err := os.Stat(memDir); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	colDir := filepath.Join(s.root, "collections", seg)
	legacy := filepath.Join(colDir, "memory.md")
	data, err := os.ReadFile(legacy)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	text := strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if text == "" {
		return false, nil
	}

	for i, body := range splitBodies(text) {
		name, desc := legacyName, legacyDesc
		if i > 0 {
			name = fmt.Sprintf("%s-%d", legacyName, i+1)
			desc = fmt.Sprintf("%s (part %d)", legacyDesc, i+1)
		}
		if _, _, err := s.Save(collection, Memory{Name: name, Description: desc, Type: TypeProject, Body: body}); err != nil {
			// The directory did not exist before: drop the partial import so a
			// later call retries from the untouched legacy file.
			_ = os.RemoveAll(memDir)
			return false, err
		}
	}
	for _, f := range []string{"memory.md", "memory.prev.md"} {
		if err := os.Remove(filepath.Join(colDir, f)); err != nil && !os.IsNotExist(err) {
			return true, err
		}
	}
	return true, nil
}

// splitBodies packs the paragraphs of text into bodies of at most MaxBodyChars
// runes, joined by a blank line.
func splitBodies(text string) []string {
	var paras []string
	for _, p := range blankLinesRE.Split(text, -1) {
		p = strings.TrimSpace(p)
		for utf8.RuneCountInString(p) > MaxBodyChars {
			r := []rune(p)
			paras = append(paras, strings.TrimSpace(string(r[:MaxBodyChars])))
			p = strings.TrimSpace(string(r[MaxBodyChars:]))
		}
		if p != "" {
			paras = append(paras, p)
		}
	}
	var out []string
	cur := ""
	for _, p := range paras {
		if cur == "" {
			cur = p
			continue
		}
		if utf8.RuneCountInString(cur)+2+utf8.RuneCountInString(p) <= MaxBodyChars {
			cur += "\n\n" + p
			continue
		}
		out = append(out, cur)
		cur = p
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
