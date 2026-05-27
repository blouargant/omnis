// stats.go — per-skill usage and outcome counters.
//
// Counters cannot live in SKILL.md frontmatter (the loader rejects extra
// fields) so they sit in a sidecar `_stats.json` at the root of the
// softskills directory. The leading underscore keeps the file out of the
// curator's `run_glob softskills/*/SKILL.md` audit.
//
// Entries are keyed by `<agent>/<name>` for sub-agent skills, or the bare
// `<name>` for leader/global skills — matching the on-disk layout used by
// WriteTools.

package softskills

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// StatsFileName is the per-host stats sidecar.
const StatsFileName = "_stats.json"

// statsMu serialises concurrent in-process writes. Layered on top of the
// OS-level flock so single-process usage never deadlocks on itself and so
// tests on platforms without flock still get serialization.
var statsMu sync.Mutex

// StatsEntry holds per-skill counters and timestamps. JSON tags are
// snake_case for human readability in the on-disk file.
type StatsEntry struct {
	LoadedCount   int       `json:"loaded_count"`
	Helpful       int       `json:"helpful"`
	Harmful       int       `json:"harmful"`
	Neutral       int       `json:"neutral"`
	FirstLoadedAt time.Time `json:"first_loaded_at,omitempty"`
	LastLoadedAt  time.Time `json:"last_loaded_at,omitempty"`
	LastSession   string    `json:"last_session,omitempty"`
}

// Stats is the file-level envelope.
type Stats struct {
	Version int                    `json:"version"`
	Entries map[string]*StatsEntry `json:"entries"`
}

// Key joins agent and skill name with `/`. The leader uses an empty agent
// and is stored under the bare skill name — matching the on-disk layout
// (`softskills/<name>` vs `softskills/<agent>/<name>`).
func Key(agent, name string) string {
	if agent == "" {
		return name
	}
	return agent + "/" + name
}

// LoadStats reads dir/_stats.json. A missing file is not an error — the
// caller gets an empty Stats it can mutate and Save back. Malformed JSON
// returns an error so a corrupt file is never silently overwritten.
func LoadStats(dir string) (*Stats, error) {
	path := filepath.Join(dir, StatsFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Stats{Version: 1, Entries: map[string]*StatsEntry{}}, nil
		}
		return nil, fmt.Errorf("softskills: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return &Stats{Version: 1, Entries: map[string]*StatsEntry{}}, nil
	}
	var s Stats
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("softskills: parse %s: %w", path, err)
	}
	if s.Entries == nil {
		s.Entries = map[string]*StatsEntry{}
	}
	if s.Version == 0 {
		s.Version = 1
	}
	return &s, nil
}

// Save atomically writes the stats back to dir/_stats.json. Uses temp-file
// + rename so an interrupted write never leaves a half-file. The flock
// blocks competing yoke processes on the same host; statsMu blocks
// goroutines inside this process.
func (s *Stats) Save(dir string) error {
	if s == nil {
		return errors.New("softskills: Save on nil Stats")
	}
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Entries == nil {
		s.Entries = map[string]*StatsEntry{}
	}

	statsMu.Lock()
	defer statsMu.Unlock()

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("softskills: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, StatsFileName)

	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("softskills: open lock %s: %w", path, err)
	}
	defer func() { _ = lock.Close() }()
	if err := flockExclusive(lock); err != nil {
		return fmt.Errorf("softskills: lock %s: %w", path, err)
	}
	defer func() { _ = flockUnlock(lock) }()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("softskills: marshal stats: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "_stats.*.json.tmp")
	if err != nil {
		return fmt.Errorf("softskills: temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("softskills: write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("softskills: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("softskills: rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// entry returns the (possibly newly-initialised) entry for key.
func (s *Stats) entry(key string) *StatsEntry {
	if s.Entries == nil {
		s.Entries = map[string]*StatsEntry{}
	}
	e, ok := s.Entries[key]
	if !ok {
		e = &StatsEntry{}
		s.Entries[key] = e
	}
	return e
}

// RecordLoad bumps LoadedCount and updates timestamps for a single load of
// the skill identified by key. sessionName is the friendly petname (or
// session ID — caller's choice) recorded for diagnostics.
func (s *Stats) RecordLoad(key, sessionName string, t time.Time) {
	if key == "" {
		return
	}
	if t.IsZero() {
		t = time.Now().UTC()
	}
	e := s.entry(key)
	e.LoadedCount++
	if e.FirstLoadedAt.IsZero() {
		e.FirstLoadedAt = t
	}
	e.LastLoadedAt = t
	if sessionName != "" {
		e.LastSession = sessionName
	}
}

// RecordTag increments one of the three tag counters. Unknown tags are
// silently ignored — callers must not depend on tag-name validation here.
func (s *Stats) RecordTag(key, tag string) {
	if key == "" {
		return
	}
	e := s.entry(key)
	switch tag {
	case "helpful":
		e.Helpful++
	case "harmful":
		e.Harmful++
	case "neutral":
		e.Neutral++
	}
}

// Retag moves one count from `from` to `to` for the given key. Used by
// the LLM reflector pipeline to correct a previously-applied heuristic
// tag when the LLM disagrees. Either side may be empty: an empty `from`
// just increments `to` (equivalent to RecordTag); an empty `to` just
// decrements `from`. Counters never go below zero.
//
// No-op when from == to.
func (s *Stats) Retag(key, from, to string) {
	if key == "" || from == to {
		return
	}
	e := s.entry(key)
	switch from {
	case "helpful":
		if e.Helpful > 0 {
			e.Helpful--
		}
	case "harmful":
		if e.Harmful > 0 {
			e.Harmful--
		}
	case "neutral":
		if e.Neutral > 0 {
			e.Neutral--
		}
	}
	switch to {
	case "helpful":
		e.Helpful++
	case "harmful":
		e.Harmful++
	case "neutral":
		e.Neutral++
	}
}
