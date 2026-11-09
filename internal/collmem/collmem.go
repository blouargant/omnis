// Package collmem stores a collection's memory as one Markdown file per fact:
// <root>/collections/<collection>/memory/<name>.md, and for General (the ""
// collection) <root>/general-memory/<name>.md. Each file carries a small front
// matter (name, description, type, updated) followed by the body.
//
// It imports only the standard library so the agent package can use it without
// an import cycle.
package collmem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	TypeFeedback  = "feedback"
	TypeUser      = "user"
	TypeProject   = "project"
	TypeReference = "reference"

	MaxBodyChars = 4000
	MaxMemories  = 200
)

var (
	ErrInvalidName = errors.New("collmem: invalid memory name")
	ErrInvalidType = errors.New("collmem: invalid memory type")
	ErrTooLarge    = errors.New("collmem: memory body too large")
	ErrFull        = errors.New("collmem: collection memory is full")
	ErrCollection  = errors.New("collmem: invalid collection name")
)

// Memory is one stored fact.
type Memory struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Updated     string `json:"updated"` // YYYY-MM-DD, set by Save
	Body        string `json:"body"`
}

// Store reads and writes memory files under one root directory.
type Store struct{ root string }

// In returns a store rooted at root.
func In(root string) *Store { return &Store{root: root} }

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// typeOrder is the List order of the memory types.
var typeOrder = map[string]int{TypeFeedback: 0, TypeUser: 1, TypeProject: 2, TypeReference: 3}

// NormalizeName lower-cases s, turns spaces and underscores into "-" and strips
// a trailing ".md". It reports false when the result does not match the name
// rule.
func NormalizeName(s string) (string, bool) {
	n := strings.ToLower(strings.TrimSpace(s))
	n = strings.TrimSuffix(n, ".md")
	n = strings.NewReplacer(" ", "-", "_", "-").Replace(n)
	if !nameRE.MatchString(n) {
		return "", false
	}
	return n, true
}

// ValidType reports whether t is one of the four memory types.
func ValidType(t string) bool {
	_, ok := typeOrder[t]
	return ok
}

// safeSegment returns a filesystem-safe single path segment for a collection
// name, or "" when the name is unusable as a directory (same rule as
// collectionctx.safeSegment).
func safeSegment(name string) string {
	n := strings.TrimSpace(name)
	if n == "" || n == "." || n == ".." {
		return ""
	}
	if strings.ContainsAny(n, `/\`) || n != filepath.Base(n) {
		return ""
	}
	return n
}

// Dir returns the directory holding a collection's memory files: General ("")
// is <root>/general-memory; an unusable collection name returns "".
func (s *Store) Dir(collection string) string {
	if collection == "" {
		return filepath.Join(s.root, "general-memory")
	}
	seg := safeSegment(collection)
	if seg == "" {
		return ""
	}
	return filepath.Join(s.root, "collections", seg, "memory")
}

var (
	locksMu sync.Mutex
	locks   = map[string]*sync.Mutex{}
)

// dirLock returns the process-wide mutex for one memory directory.
func dirLock(dir string) *sync.Mutex {
	locksMu.Lock()
	defer locksMu.Unlock()
	l, ok := locks[dir]
	if !ok {
		l = &sync.Mutex{}
		locks[dir] = l
	}
	return l
}

// Save creates or updates a memory. created reports whether the file is new.
func (s *Store) Save(collection string, m Memory) (saved Memory, created bool, err error) {
	dir := s.Dir(collection)
	if dir == "" {
		return Memory{}, false, ErrCollection
	}
	name, ok := NormalizeName(m.Name)
	if !ok {
		return Memory{}, false, ErrInvalidName
	}
	if !ValidType(m.Type) {
		return Memory{}, false, ErrInvalidType
	}
	body := strings.TrimRight(strings.TrimLeft(m.Body, "\r\n"), " \t\r\n")
	if utf8.RuneCountInString(body) > MaxBodyChars {
		return Memory{}, false, ErrTooLarge
	}
	desc := oneLine(m.Description)
	if desc == "" {
		first, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
		desc = truncateRunes(strings.TrimSpace(first), 80)
	}
	saved = Memory{
		Name:        name,
		Description: desc,
		Type:        m.Type,
		Updated:     time.Now().Format("2006-01-02"),
		Body:        body,
	}

	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()

	path := filepath.Join(dir, name+".md")
	if _, statErr := os.Stat(path); statErr == nil {
		created = false
	} else if os.IsNotExist(statErr) {
		created = true
		if len(s.List(collection)) >= MaxMemories {
			return Memory{}, false, ErrFull
		}
	} else {
		return Memory{}, false, statErr
	}
	if err := writeAtomic(dir, path, render(saved)); err != nil {
		return Memory{}, false, err
	}
	return saved, created, nil
}

// Get returns one memory, or false when it is absent or unreadable.
func (s *Store) Get(collection, name string) (Memory, bool) {
	dir := s.Dir(collection)
	n, ok := NormalizeName(name)
	if dir == "" || !ok {
		return Memory{}, false
	}
	return readMemory(filepath.Join(dir, n+".md"), n)
}

// Delete removes a memory; it reports false when there was nothing to remove.
func (s *Store) Delete(collection, name string) (bool, error) {
	dir := s.Dir(collection)
	if dir == "" {
		return false, ErrCollection
	}
	n, ok := NormalizeName(name)
	if !ok {
		return false, ErrInvalidName
	}
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()
	if err := os.Remove(filepath.Join(dir, n+".md")); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// List returns every valid memory of a collection, ordered by type (feedback,
// user, project, reference) then name. Files that do not parse, whose name does
// not match their file stem or whose type is unknown are skipped.
func (s *Store) List(collection string) []Memory {
	dir := s.Dir(collection)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Memory
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".md")
		if m, ok := readMemory(filepath.Join(dir, e.Name()), stem); ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := typeOrder[out[i].Type], typeOrder[out[j].Type]; a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// readMemory parses the file at path and checks it against its file stem.
func readMemory(path, stem string) (Memory, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Memory{}, false
	}
	m, ok := parse(string(data))
	if !ok || m.Name != stem || !nameRE.MatchString(m.Name) || !ValidType(m.Type) {
		return Memory{}, false
	}
	return m, true
}

// parse reads the front matter by hand: the first line must be "---", the block
// ends at the next line that is exactly "---", and each line splits on the
// first ": ". The rest, leading blank lines trimmed, is the body.
func parse(text string) (Memory, bool) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return Memory{}, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return Memory{}, false
	}
	var m Memory
	for _, l := range lines[1:end] {
		k, v, ok := strings.Cut(l, ": ")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			m.Name = v
		case "description":
			m.Description = v
		case "type":
			m.Type = v
		case "updated":
			m.Updated = v
		}
	}
	body := strings.Join(lines[end+1:], "\n")
	m.Body = strings.TrimRight(strings.TrimLeft(body, "\n"), "\n")
	return m, true
}

func render(m Memory) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\ntype: %s\nupdated: %s\n---\n\n%s\n",
		m.Name, m.Description, m.Type, m.Updated, m.Body)
}

// oneLine collapses newlines to spaces and trims.
func oneLine(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(s)
	return strings.TrimSpace(s)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// writeAtomic writes text to path through a temp file in dir and a rename.
func writeAtomic(dir, path, text string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cmwrite_*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write([]byte(text)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
