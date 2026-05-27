package softskills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestKey(t *testing.T) {
	cases := []struct {
		agent, name, want string
	}{
		{"", "wrap-session", "wrap-session"},
		{"investigator", "k8s-pod-evidence", "investigator/k8s-pod-evidence"},
	}
	for _, c := range cases {
		got := Key(c.agent, c.name)
		if got != c.want {
			t.Errorf("Key(%q,%q) = %q, want %q", c.agent, c.name, got, c.want)
		}
	}
}

func TestLoadStatsMissingFile(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadStats(dir)
	if err != nil {
		t.Fatalf("LoadStats on empty dir: %v", err)
	}
	if s == nil || s.Entries == nil {
		t.Fatalf("expected non-nil Stats with initialised map, got %+v", s)
	}
	if s.Version != 1 {
		t.Errorf("Version = %d, want 1", s.Version)
	}
}

func TestLoadStatsMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, StatsFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStats(dir); err == nil {
		t.Fatal("expected error on malformed JSON, got nil")
	}
}

func TestRecordLoadAndSaveRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 1, 4, 12, 1, 0, 0, time.UTC)
	t1 := time.Date(2026, 5, 26, 9, 14, 0, 0, time.UTC)

	s.RecordLoad("investigator/k8s-pod-evidence", "teaching-kite", t0)
	s.RecordLoad("investigator/k8s-pod-evidence", "teaching-kite", t1)
	s.RecordLoad("wrap-session", "teaching-kite", t1)
	s.RecordTag("investigator/k8s-pod-evidence", "helpful")
	s.RecordTag("investigator/k8s-pod-evidence", "helpful")
	s.RecordTag("investigator/k8s-pod-evidence", "harmful")
	s.RecordTag("investigator/k8s-pod-evidence", "neutral")
	s.RecordTag("investigator/k8s-pod-evidence", "garbage") // ignored

	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	e := got.Entries["investigator/k8s-pod-evidence"]
	if e == nil {
		t.Fatal("missing entry after round-trip")
	}
	if e.LoadedCount != 2 {
		t.Errorf("LoadedCount = %d, want 2", e.LoadedCount)
	}
	if e.Helpful != 2 || e.Harmful != 1 || e.Neutral != 1 {
		t.Errorf("tag counters = h=%d harm=%d n=%d, want 2/1/1", e.Helpful, e.Harmful, e.Neutral)
	}
	if !e.FirstLoadedAt.Equal(t0) {
		t.Errorf("FirstLoadedAt = %v, want %v", e.FirstLoadedAt, t0)
	}
	if !e.LastLoadedAt.Equal(t1) {
		t.Errorf("LastLoadedAt = %v, want %v", e.LastLoadedAt, t1)
	}
	if e.LastSession != "teaching-kite" {
		t.Errorf("LastSession = %q, want %q", e.LastSession, "teaching-kite")
	}
	if got.Entries["wrap-session"] == nil || got.Entries["wrap-session"].LoadedCount != 1 {
		t.Errorf("wrap-session entry not recorded as expected: %+v", got.Entries["wrap-session"])
	}
}

func TestRecordLoadAutoTimestamp(t *testing.T) {
	s := &Stats{Version: 1, Entries: map[string]*StatsEntry{}}
	before := time.Now().UTC().Add(-time.Second)
	s.RecordLoad("foo", "", time.Time{})
	after := time.Now().UTC().Add(time.Second)
	e := s.Entries["foo"]
	if e == nil || e.LoadedCount != 1 {
		t.Fatalf("expected entry with LoadedCount=1, got %+v", e)
	}
	if e.LastLoadedAt.Before(before) || e.LastLoadedAt.After(after) {
		t.Errorf("LastLoadedAt %v not in [%v,%v]", e.LastLoadedAt, before, after)
	}
}

func TestSaveConcurrent(t *testing.T) {
	// Two goroutines hammering Save on independent Stats values must not
	// corrupt the on-disk file: the last writer wins, and a parseable file
	// always survives.
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			s, err := LoadStats(dir)
			if err != nil {
				t.Errorf("LoadStats: %v", err)
				return
			}
			s.RecordLoad("foo", "sess", time.Now().UTC())
			s.RecordTag("foo", "helpful")
			if err := s.Save(dir); err != nil {
				t.Errorf("Save: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// File must be valid JSON after the storm.
	data, err := os.ReadFile(filepath.Join(dir, StatsFileName))
	if err != nil {
		t.Fatal(err)
	}
	var s Stats
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("post-storm file is malformed: %v\n%s", err, data)
	}
	if s.Entries["foo"] == nil || s.Entries["foo"].LoadedCount == 0 {
		t.Errorf("foo entry missing after concurrent saves: %+v", s.Entries["foo"])
	}
}

func TestRetag(t *testing.T) {
	s := &Stats{Version: 1, Entries: map[string]*StatsEntry{}}
	s.RecordTag("foo", "helpful")
	s.RecordTag("foo", "helpful")
	s.RecordTag("foo", "neutral")
	// Move one helpful → harmful.
	s.Retag("foo", "helpful", "harmful")
	e := s.Entries["foo"]
	if e.Helpful != 1 || e.Harmful != 1 || e.Neutral != 1 {
		t.Errorf("after retag h/harm/n = %d/%d/%d, want 1/1/1", e.Helpful, e.Harmful, e.Neutral)
	}
	// No-op when from == to.
	s.Retag("foo", "helpful", "helpful")
	if e.Helpful != 1 {
		t.Errorf("no-op retag mutated helpful: %d", e.Helpful)
	}
	// Decrement floor: never below zero.
	s.Retag("foo", "helpful", "")
	s.Retag("foo", "helpful", "")
	if e.Helpful != 0 {
		t.Errorf("helpful = %d, want 0 (floor)", e.Helpful)
	}
	// Empty `from` acts like RecordTag.
	s.Retag("foo", "", "neutral")
	if e.Neutral != 2 {
		t.Errorf("neutral = %d, want 2 (from=\"\" acts like RecordTag)", e.Neutral)
	}
}

func TestEmptyKeyIgnored(t *testing.T) {
	s := &Stats{Version: 1, Entries: map[string]*StatsEntry{}}
	s.RecordLoad("", "sess", time.Now())
	s.RecordTag("", "helpful")
	if len(s.Entries) != 0 {
		t.Errorf("empty key produced an entry: %+v", s.Entries)
	}
}
