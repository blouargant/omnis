// Package collmem stores a collection's memory as one Markdown file per fact:
// <root>/collections/<collection>/memory/<name>.md, and for General (the ""
// collection) <root>/general-memory/<name>.md. Each file carries a small front
// matter (name, description, type, created, updated, and when the user restated
// it: confirmations, last_confirmed) followed by the body.
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
	"strconv"
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
	// MaxDescriptionRunes caps a memory's one-line description (index lines
	// are injected on every model call).
	MaxDescriptionRunes = 200
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
	Created     string `json:"created"` // YYYY-MM-DD, first save; kept on update
	Updated     string `json:"updated"` // YYYY-MM-DD, set by Save
	// Confirmations counts the times the user restated this memory after it was
	// first saved; LastConfirmed is the latest of them (YYYY-MM-DD).
	Confirmations int    `json:"confirmations,omitempty"`
	LastConfirmed string `json:"last_confirmed,omitempty"`
	Body          string `json:"body"`
}

// LastActivity is the latest of Updated and LastConfirmed: when the user last
// said or changed this.
func (m Memory) LastActivity() string {
	if m.LastConfirmed > m.Updated {
		return m.LastConfirmed
	}
	return m.Updated
}

// SaveMode says what a save means for an existing memory's dates and
// confirmations (a new memory is always created with no confirmation).
type SaveMode int

const (
	// SaveEdit keeps the confirmations: an edit by the user in the UI, a
	// migration.
	SaveEdit SaveMode = iota
	// SaveChange resets them: the agent saved new content (a correction, a new
	// fact), which the earlier confirmations did not confirm.
	SaveChange
	// SaveConfirm counts one more confirmation: the user restated what the
	// memory already says.
	SaveConfirm
)

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

// Save creates or updates a memory with SaveEdit. created reports whether the
// file is new.
func (s *Store) Save(collection string, m Memory) (saved Memory, created bool, err error) {
	return s.SaveWith(collection, m, SaveEdit)
}

// now is the clock used for the dates (a variable for tests).
var now = time.Now

// SaveWith creates or updates a memory; mode says how an existing memory's
// confirmations change. The creation date is always kept.
func (s *Store) SaveWith(collection string, m Memory, mode SaveMode) (saved Memory, created bool, err error) {
	dir := s.Dir(collection)
	if dir == "" {
		return Memory{}, false, ErrCollection
	}
	saved, err = prepare(m)
	if err != nil {
		return Memory{}, false, err
	}
	name, today := saved.Name, saved.Updated

	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()

	path := filepath.Join(dir, name+".md")
	if _, statErr := os.Stat(path); statErr == nil {
		created = false
		if old, ok := readMemory(path, name); ok {
			saved.Created = old.Created
			switch mode {
			case SaveEdit:
				saved.Confirmations, saved.LastConfirmed = old.Confirmations, old.LastConfirmed
			case SaveConfirm:
				saved.Confirmations, saved.LastConfirmed = old.Confirmations+1, today
				if old.Body == saved.Body && old.Type == saved.Type {
					saved.Updated = old.Updated // restated, not changed
				}
			}
		}
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

// prepare validates m and returns the memory to write, dated today.
func prepare(m Memory) (Memory, error) {
	name, ok := NormalizeName(m.Name)
	if !ok {
		return Memory{}, ErrInvalidName
	}
	if !ValidType(m.Type) {
		return Memory{}, ErrInvalidType
	}
	body := strings.TrimRight(strings.TrimLeft(m.Body, "\r\n"), " \t\r\n")
	if utf8.RuneCountInString(body) > MaxBodyChars {
		return Memory{}, ErrTooLarge
	}
	desc := truncateRunes(oneLine(m.Description), MaxDescriptionRunes)
	if desc == "" {
		first, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
		desc = truncateRunes(strings.TrimSpace(first), 80)
	}
	today := now().Format("2006-01-02")
	return Memory{Name: name, Description: desc, Type: m.Type, Created: today, Updated: today, Body: body}, nil
}

// ErrNotFound is returned when a memory an operation needs does not exist.
var ErrNotFound = errors.New("collmem: memory not found")

// MergeInto writes into and removes the other sources, in one locked step. The
// merged memory keeps the earliest creation date, the sum of the sources'
// confirmations and the latest confirmation date. into may reuse a source's
// name or take a new one, never the name of another memory.
func (s *Store) MergeInto(collection string, into Memory, sources []string) (Memory, error) {
	dir := s.Dir(collection)
	if dir == "" {
		return Memory{}, ErrCollection
	}
	saved, err := prepare(into)
	if err != nil {
		return Memory{}, err
	}
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()

	isSource := map[string]bool{}
	for _, raw := range sources {
		n, ok := NormalizeName(raw)
		if !ok {
			return Memory{}, ErrInvalidName
		}
		old, found := readMemory(filepath.Join(dir, n+".md"), n)
		if !found {
			return Memory{}, ErrNotFound
		}
		isSource[n] = true
		if old.Created != "" && old.Created < saved.Created {
			saved.Created = old.Created
		}
		saved.Confirmations += old.Confirmations
		if old.LastConfirmed > saved.LastConfirmed {
			saved.LastConfirmed = old.LastConfirmed
		}
	}
	path := filepath.Join(dir, saved.Name+".md")
	if !isSource[saved.Name] {
		if _, err := os.Stat(path); err == nil {
			return Memory{}, ErrInvalidName // would overwrite another memory
		}
	}
	if err := writeAtomic(dir, path, render(saved)); err != nil {
		return Memory{}, err
	}
	for n := range isSource {
		if n != saved.Name {
			if err := os.Remove(filepath.Join(dir, n+".md")); err != nil && !os.IsNotExist(err) {
				return saved, err
			}
		}
	}
	return saved, nil
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
		case "created":
			m.Created = v
		case "updated":
			m.Updated = v
		case "confirmations":
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				m.Confirmations = n
			}
		case "last_confirmed":
			m.LastConfirmed = v
		}
	}
	if m.Created == "" {
		m.Created = m.Updated // files written before the creation date existed
	}
	body := strings.Join(lines[end+1:], "\n")
	m.Body = strings.TrimRight(strings.TrimLeft(body, "\n"), "\n")
	return m, true
}

func render(m Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\ntype: %s\ncreated: %s\nupdated: %s\n",
		m.Name, m.Description, m.Type, m.Created, m.Updated)
	if m.Confirmations > 0 {
		fmt.Fprintf(&b, "confirmations: %d\nlast_confirmed: %s\n", m.Confirmations, m.LastConfirmed)
	}
	fmt.Fprintf(&b, "---\n\n%s\n", m.Body)
	return b.String()
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
