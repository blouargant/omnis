package codeindex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Model() string { return "fake" }
func (fakeEmbedder) Dim() int      { return 3 }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case has(t, "mcp"), has(t, "dedup"):
			out[i] = []float32{1, 0, 0}
		case has(t, "permission"), has(t, "grant"):
			out[i] = []float32{0, 1, 0}
		default:
			out[i] = []float32{0, 0, 1}
		}
	}
	return out, nil
}

func has(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReindexAndSearch(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("YOKE_HOME", t.TempDir())
	writeFile(t, repo, "mcp/pool.go", "package mcp\n// dedup mcp servers by command hash\nfunc Dedup() {}\n")
	writeFile(t, repo, "perm/grant.go", "package perm\n// permission grant scopes\nfunc Grant() {}\n")

	idx, err := Open(repo, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	indexed, _, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 2 {
		t.Fatalf("expected 2 files indexed, got %d", indexed)
	}
	hits, err := idx.Search(context.Background(), "where are mcp servers deduplicated", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Path != "mcp/pool.go" {
		t.Errorf("expected mcp/pool.go top hit, got %s", hits[0].Path)
	}
}

func TestIncrementalReindex(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("YOKE_HOME", t.TempDir())
	writeFile(t, repo, "a.go", "package a\n// mcp stuff\n")
	idx, err := Open(repo, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Unchanged → no re-index.
	indexed, _, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 0 {
		t.Errorf("expected 0 re-indexed on unchanged repo, got %d", indexed)
	}
	// Delete the file → removed count grows.
	if err := os.Remove(filepath.Join(repo, "a.go")); err != nil {
		t.Fatal(err)
	}
	_, removed, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("expected 1 file removed, got %d", removed)
	}
}

func TestIndexesTextSkipsBinary(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("YOKE_HOME", t.TempDir())
	// An extension not on any allow-list (would have been skipped before) and
	// an extensionless file: both are plain text and must be indexed.
	writeFile(t, repo, "app.vue", "<template><!-- mcp dedup --></template>\n")
	writeFile(t, repo, "Makefile", "build: ## permission grant\n\tgo build ./...\n")
	// A deny-listed binary extension and a NUL-byte blob: both must be skipped.
	writeFile(t, repo, "logo.png", "\x89PNG\x00\x00 not real")
	writeFile(t, repo, "data.dat", "header\x00\x00binary\x00payload")

	idx, err := Open(repo, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	indexed, _, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 2 {
		t.Fatalf("expected 2 text files indexed (app.vue, Makefile), got %d", indexed)
	}
}

func TestNilEmbedderSkips(t *testing.T) {
	idx, err := Open(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if idx != nil {
		t.Fatal("expected nil index when no embedder")
	}
	if idx.Tools() != nil {
		t.Fatal("expected nil tools for nil index")
	}
}
