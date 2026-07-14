package semindex

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

// Unload is the memory-drop primitive behind the session index's idle sweeper.
// It must be transparent: the store keeps answering after it, having re-read
// both the vectors AND the metadata sidecar from disk. If the metadata did not
// come back, every hit would render with an empty snippet.
func TestUnloadThenQueryReloadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx")
	emb := newFake()

	s, err := Open(base, emb)
	if err != nil {
		t.Fatal(err)
	}
	items := []Item{
		{ID: 1, Text: "cat", Meta: json.RawMessage(`{"t":"cat"}`)},
		{ID: 2, Text: "car", Meta: json.RawMessage(`{"t":"car"}`)},
	}
	if err := s.Upsert(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	if err := s.Unload(); err != nil {
		t.Fatalf("unload: %v", err)
	}
	if s.Loaded() {
		t.Fatal("still loaded after Unload — the memory was not released")
	}
	// Len must stay answerable without paying the reload (the sweeper and the
	// cold-index check both ask it).
	if got := s.Len(); got != 2 {
		t.Errorf("Len after unload = %d, want 2", got)
	}

	hits, err := s.Query(context.Background(), "query", 1)
	if err != nil {
		t.Fatalf("query after unload: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != 1 {
		t.Fatalf("wrong hit after unload: %+v", hits)
	}
	// The sidecar stores metadata pretty-printed, so compare the decoded value,
	// not the raw bytes.
	var meta struct {
		T string `json:"t"`
	}
	if err := json.Unmarshal(hits[0].Meta, &meta); err != nil || meta.T != "cat" {
		t.Errorf("metadata lost across unload: got %q (%v) — hits would render blank", hits[0].Meta, err)
	}
	if !s.Loaded() {
		t.Error("query did not re-materialise the index")
	}
}

// Unloading with unsaved changes must persist them first: the sweeper fires on a
// timer and has no idea whether the last write was saved.
func TestUnloadPersistsUnsavedChanges(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx")
	emb := newFake()

	s, err := Open(base, emb)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(context.Background(), []Item{
		{ID: 7, Text: "cat", Meta: json.RawMessage(`{"t":"cat"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	// NOTE: no Save() here — Unload must do it.
	if err := s.Unload(); err != nil {
		t.Fatalf("unload: %v", err)
	}

	// Reopen from scratch: the data must be on disk.
	s2, err := Open(base, emb)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := s2.Query(context.Background(), "query", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 7 {
		t.Fatalf("unsaved upsert was dropped by Unload: %+v", hits)
	}
}

// Unloading a never-used store is a no-op, not a crash: the sweeper calls it on
// a timer regardless of whether anything ever touched the index.
func TestUnloadIsIdempotentOnEmptyStore(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "idx"), newFake())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Unload(); err != nil {
			t.Fatalf("unload #%d: %v", i, err)
		}
	}
	if s.Loaded() {
		t.Error("empty store reports as loaded")
	}
}
