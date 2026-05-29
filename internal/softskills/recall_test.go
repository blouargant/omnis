package softskills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// fakeEmbedder maps phrases to fixed unit vectors so recall ordering is
// deterministic in tests.
type fakeEmbedder struct{}

func (fakeEmbedder) Model() string { return "fake" }
func (fakeEmbedder) Dim() int      { return 3 }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case contains(t, "kubernetes"), contains(t, "pod"):
			out[i] = []float32{1, 0, 0}
		case contains(t, "database"), contains(t, "sql"):
			out[i] = []float32{0, 1, 0}
		default:
			out[i] = []float32{0, 0, 1}
		}
	}
	return out, nil
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func writeSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	sd := filepath.Join(dir, name)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n# " + name + "\n"
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFrontmatter(t *testing.T) {
	name, desc := parseFrontmatter([]byte("---\nname: foo\ndescription: bar baz\n---\nbody"))
	if name != "foo" || desc != "bar baz" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
	if n, _ := parseFrontmatter([]byte("no frontmatter")); n != "" {
		t.Fatalf("expected empty name for no-frontmatter doc")
	}
}

func TestRecallerRanks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	dir := filepath.Join(home, "softskills")
	writeSkill(t, dir, "kube-triage", "diagnose kubernetes pod crashes")
	writeSkill(t, dir, "db-tuning", "optimise sql database queries")

	r, err := newRecaller(dir, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	hits, err := r.store.Query(context.Background(), "why is my kubernetes pod failing", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].ID != nameID("kube-triage") {
		t.Errorf("expected kube-triage ranked first, got id %d", hits[0].ID)
	}
}

func TestRecallerCorpusHashGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("YOKE_HOME", home)
	dir := filepath.Join(home, "softskills")
	writeSkill(t, dir, "a", "kubernetes things")

	r, err := newRecaller(dir, fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := r.store.Manifest().CorpusHash
	// Second refresh with unchanged corpus must be a no-op (same hash).
	if err := r.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.store.Manifest().CorpusHash != first {
		t.Errorf("corpus hash changed on no-op refresh")
	}
	// Add a skill → hash changes, count grows.
	writeSkill(t, dir, "b", "database stuff")
	if err := r.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.store.Len() != 2 {
		t.Errorf("expected 2 indexed skills after add, got %d", r.store.Len())
	}
}
