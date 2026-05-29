package semindex

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/blouargant/yoke/core/embed"
)

// fakeEmbedder maps known phrases to fixed unit vectors in 3-space so tests can
// reason about cosine ordering deterministically.
type fakeEmbedder struct{ vectors map[string][]float32 }

func (f *fakeEmbedder) Model() string { return "fake" }
func (f *fakeEmbedder) Dim() int      { return 3 }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = append([]float32(nil), v...)
		} else {
			out[i] = []float32{0, 0, 1}
		}
	}
	return out, nil
}

func newFake() *fakeEmbedder {
	return &fakeEmbedder{vectors: map[string][]float32{
		"cat":   {1, 0, 0},
		"dog":   {0.9, 0.1, 0},
		"car":   {0, 1, 0},
		"query": {1, 0, 0},
	}}
}

func TestUpsertQueryRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test"), newFake())
	if err != nil {
		t.Fatal(err)
	}
	items := []Item{
		{ID: 1, Text: "cat", Meta: json.RawMessage(`{"n":"cat"}`)},
		{ID: 2, Text: "dog", Meta: json.RawMessage(`{"n":"dog"}`)},
		{ID: 3, Text: "car", Meta: json.RawMessage(`{"n":"car"}`)},
	}
	if err := s.Upsert(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Query(context.Background(), "query", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].ID != 1 {
		t.Errorf("expected closest hit id=1 (cat), got %d", hits[0].ID)
	}
}

func TestPersistenceReload(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "idx")
	s, err := Open(base, newFake())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(context.Background(), []Item{{ID: 7, Text: "cat", Meta: json.RawMessage(`{"k":1}`)}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(base, newFake())
	if err != nil {
		t.Fatal(err)
	}
	if s2.Len() != 1 {
		t.Fatalf("expected 1 item after reload, got %d", s2.Len())
	}
	hits, err := s2.Query(context.Background(), "query", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != 7 {
		t.Fatalf("reload lost data: %+v", hits)
	}
	var got struct {
		K int `json:"k"`
	}
	if err := json.Unmarshal(hits[0].Meta, &got); err != nil || got.K != 1 {
		t.Errorf("meta not preserved: %s (err=%v)", hits[0].Meta, err)
	}
}

func TestRemoveAndUpsertReplace(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "i"), newFake())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(context.Background(), []Item{{ID: 1, Text: "cat"}}); err != nil {
		t.Fatal(err)
	}
	// Re-upsert same id with different text/meta should not error (remove+add).
	if err := s.Upsert(context.Background(), []Item{{ID: 1, Text: "car", Meta: json.RawMessage(`{"x":2}`)}}); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("expected 1 item after replace, got %d", s.Len())
	}
	if err := s.Remove(1); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 0 {
		t.Fatalf("expected 0 after remove, got %d", s.Len())
	}
}

func TestNilEmbedderDegrades(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "n"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Query(context.Background(), "x", 5); !errors.Is(err, embed.ErrNoEmbedder) {
		t.Fatalf("expected ErrNoEmbedder, got %v", err)
	}
	if err := s.Upsert(context.Background(), []Item{{ID: 1, Text: "x"}}); !errors.Is(err, embed.ErrNoEmbedder) {
		t.Fatalf("expected ErrNoEmbedder on upsert, got %v", err)
	}
}
