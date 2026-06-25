package docindex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Model() string { return "fake" }
func (fakeEmbedder) Dim() int      { return 3 }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		t = strings.ToLower(t)
		switch {
		case strings.Contains(t, "reload"), strings.Contains(t, "generation"):
			out[i] = []float32{1, 0, 0}
		case strings.Contains(t, "embed"), strings.Contains(t, "vector"):
			out[i] = []float32{0, 1, 0}
		default:
			out[i] = []float32{0, 0, 1}
		}
	}
	return out, nil
}

func writeDoc(t *testing.T, dir, rel, content string) {
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
	docs := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_DOCS_DIRS", docs)
	writeDoc(t, docs, "hot-reload.md", "# Hot reload\n\nThe server rebuilds the agent generation without restarting.\n")
	writeDoc(t, docs, "embedder.md", "# Embedder\n\nSemantic recall builds a vector index from an embed model.\n")

	idx, err := Open(fakeEmbedder{})
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
	hits, err := idx.Search(context.Background(), "how does the server reload the generation", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Path != "hot-reload.md" {
		t.Errorf("expected hot-reload.md top hit, got %s", hits[0].Path)
	}
	if hits[0].Heading != "Hot reload" {
		t.Errorf("expected heading captured, got %q", hits[0].Heading)
	}
	if !strings.Contains(hits[0].Text, "agent generation") {
		t.Errorf("expected quotable text in hit, got %q", hits[0].Text)
	}
}

func TestIncrementalReindex(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_DOCS_DIRS", docs)
	writeDoc(t, docs, "a.md", "# A\n\nreload notes\n")
	idx, err := Open(fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	indexed, _, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 0 {
		t.Errorf("expected 0 re-indexed on unchanged docs, got %d", indexed)
	}
	if err := os.Remove(filepath.Join(docs, "a.md")); err != nil {
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

func TestNilEmbedderSkips(t *testing.T) {
	idx, err := Open(nil)
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

func TestReadDocAndTraversal(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("OMNIS_DOCS_DIRS", docs)
	writeDoc(t, docs, "guide.md", "line1\nline2\nline3\n")

	out, err := readDoc(Roots, readIn{Path: "guide.md", Start: 2, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.Content) != "line2" {
		t.Errorf("expected line2, got %q", out.Content)
	}
	// Path traversal must be rejected.
	if _, err := readDoc(Roots, readIn{Path: "../../etc/passwd"}); err == nil {
		t.Error("expected traversal path to be rejected")
	}
}

func TestGrepDocs(t *testing.T) {
	docs := t.TempDir()
	t.Setenv("OMNIS_DOCS_DIRS", docs)
	writeDoc(t, docs, "x.md", "alpha\nOMNIS_EMBED_MODEL controls the embedder\nbeta\n")
	out, err := grepDocs(Roots, grepIn{Pattern: "OMNIS_EMBED_MODEL"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 1 || out.Matches[0].Line != 2 {
		t.Fatalf("expected one match on line 2, got %+v", out.Matches)
	}
}
