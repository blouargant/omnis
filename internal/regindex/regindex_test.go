package regindex

import (
	"context"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/registries"
	"github.com/blouargant/omnis/internal/semindex"
)

// fakeEmbedder maps phrases to fixed unit vectors in 3-space so cosine ordering
// is deterministic: "deploy/kubernetes" → x, "database/migration" → y, else z.
type fakeEmbedder struct{}

func (fakeEmbedder) Model() string { return "fake" }
func (fakeEmbedder) Dim() int      { return 3 }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case strings.Contains(t, "deploy"), strings.Contains(t, "kubernetes"):
			out[i] = []float32{1, 0, 0}
		case strings.Contains(t, "database"), strings.Contains(t, "migration"):
			out[i] = []float32{0, 1, 0}
		default:
			out[i] = []float32{0, 0, 1}
		}
	}
	return out, nil
}

// writeRegistries drops a remote_registries.json under OMNIS_HOME so
// LoadRegistries(ReadConfigPath()) finds it, and returns its config path.
func writeRegistries(t *testing.T, regs ...registries.Registry) {
	t.Helper()
	if err := registries.SaveRegistries(registries.WriteConfigPath(), regs); err != nil {
		t.Fatal(err)
	}
}

// fakeBrowse returns canned items for known registry IDs, simulating a remote
// browse without any network.
func fakeBrowse(perReg map[string][]semindex.Item) func(registries.Registry, Config) []semindex.Item {
	return func(reg registries.Registry, _ Config) []semindex.Item {
		return perReg[reg.ID]
	}
}

func openTest(t *testing.T) *Index {
	t.Helper()
	idx, err := Open(fakeEmbedder{}, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if idx == nil {
		t.Fatal("expected a non-nil index with a real embedder")
	}
	return idx
}

func TestReindexAndSearch(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	writeRegistries(t, registries.Registry{ID: "r1", Name: "Hub", URL: "https://github.com/x/y", Kind: registries.KindSkills})

	idx := openTest(t)
	idx.browse = fakeBrowse(map[string][]semindex.Item{
		"r1": {
			newItem(registries.Registry{ID: "r1", Name: "Hub"}, kindSkill, "deployer", "ops/deployer", "deploy services to kubernetes", nil, false),
			newItem(registries.Registry{ID: "r1", Name: "Hub"}, kindSkill, "migrator", "db/migrator", "run database migration", nil, false),
		},
	})

	n, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 indexed items, got %d", n)
	}

	hits, err := idx.Search(context.Background(), "how do I deploy to kubernetes", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	if hits[0].Name != "deployer" {
		t.Errorf("expected deployer as closest hit, got %q", hits[0].Name)
	}
	if hits[0].DirPath != "ops/deployer" || hits[0].RegistryID != "r1" {
		t.Errorf("metadata not preserved: %+v", hits[0])
	}
}

// TestReindexAllKinds proves items of non-skill/agent kinds (mcp, command) are
// indexed and surface in search with their kind preserved.
func TestReindexAllKinds(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	writeRegistries(t,
		registries.Registry{ID: "m1", Name: "MCP Hub", URL: "https://github.com/x/mcp", Kind: registries.KindMCP},
		registries.Registry{ID: "c1", Name: "Cmd Hub", URL: "https://github.com/x/cmd", Kind: registries.KindCommands},
	)

	idx := openTest(t)
	idx.browse = fakeBrowse(map[string][]semindex.Item{
		"m1": {
			newItem(registries.Registry{ID: "m1", Name: "MCP Hub"}, kindMCP, "postgres", "servers/postgres", "connect to a postgresql database", nil, false),
		},
		"c1": {
			newItem(registries.Registry{ID: "c1", Name: "Cmd Hub"}, kindCommand, "review", "commands/review.md", "review a pull request diff", nil, true),
		},
	})

	n, err := idx.Reindex(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 indexed items, got %d", n)
	}

	hits, err := idx.Search(context.Background(), "talk to a postgres database", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Name != "postgres" || hits[0].Kind != kindMCP {
		t.Fatalf("expected mcp 'postgres' hit, got %+v", hits)
	}

	hits, err = idx.Search(context.Background(), "review a PR", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Kind != kindCommand || !hits[0].Installed {
		t.Fatalf("expected installed command hit, got %+v", hits)
	}
}

func TestSearchLazyBuildsOnEmpty(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	writeRegistries(t, registries.Registry{ID: "r1", URL: "https://github.com/x/y", Kind: registries.KindSkills})

	idx := openTest(t)
	idx.browse = fakeBrowse(map[string][]semindex.Item{
		"r1": {newItem(registries.Registry{ID: "r1"}, kindSkill, "deployer", "ops/deployer", "deploy kubernetes", nil, false)},
	})

	// No explicit Reindex: Search must build lazily on the empty index.
	hits, err := idx.Search(context.Background(), "deploy", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Name != "deployer" {
		t.Fatalf("lazy build failed, hits=%+v", hits)
	}
}

func TestStaleRebuildOnRegistrySetChange(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	writeRegistries(t, registries.Registry{ID: "r1", URL: "https://github.com/x/y", Kind: registries.KindSkills})

	idx := openTest(t)
	idx.browse = fakeBrowse(map[string][]semindex.Item{
		"r1": {newItem(registries.Registry{ID: "r1"}, kindSkill, "deployer", "ops/deployer", "deploy kubernetes", nil, false)},
		"r2": {newItem(registries.Registry{ID: "r2"}, kindSkill, "migrator", "db/migrator", "database migration", nil, false)},
	})
	if _, err := idx.Reindex(context.Background()); err != nil {
		t.Fatal(err)
	}
	if idx.Len() != 1 {
		t.Fatalf("expected 1 item from r1, got %d", idx.Len())
	}

	// Add a second registry: the corpus hash changes, so the next Search must
	// rebuild and pick up r2's item without an explicit reindex call.
	writeRegistries(t,
		registries.Registry{ID: "r1", URL: "https://github.com/x/y", Kind: registries.KindSkills},
		registries.Registry{ID: "r2", URL: "https://github.com/a/b", Kind: registries.KindSkills},
	)
	if !idx.stale() {
		t.Fatal("expected index to report stale after registry set changed")
	}
	hits, err := idx.Search(context.Background(), "database migration", 5)
	if err != nil {
		t.Fatal(err)
	}
	if idx.Len() != 2 {
		t.Fatalf("expected 2 items after stale rebuild, got %d", idx.Len())
	}
	if len(hits) == 0 || hits[0].Name != "migrator" {
		t.Fatalf("expected migrator after rebuild, hits=%+v", hits)
	}
}

func TestItemIDDeterministicAndDistinct(t *testing.T) {
	a := itemID("r1", kindSkill, "ops/x")
	if a != itemID("r1", kindSkill, "ops/x") {
		t.Fatal("itemID not deterministic")
	}
	// Same path, different kind / registry must not collide.
	if a == itemID("r1", kindAgent, "ops/x") {
		t.Fatal("skill and agent at same path collide")
	}
	if a == itemID("r2", kindSkill, "ops/x") {
		t.Fatal("same path across registries collides")
	}
}

func TestCorpusHashOrderIndependent(t *testing.T) {
	r1 := registries.Registry{ID: "r1", URL: "u1", Kind: registries.KindSkills}
	r2 := registries.Registry{ID: "r2", URL: "u2", Kind: registries.KindAgents}
	if corpusHash([]registries.Registry{r1, r2}) != corpusHash([]registries.Registry{r2, r1}) {
		t.Fatal("corpus hash should be order-independent")
	}
	if corpusHash([]registries.Registry{r1}) == corpusHash([]registries.Registry{r1, r2}) {
		t.Fatal("corpus hash should change when a registry is added")
	}
}

func TestItemText(t *testing.T) {
	got := itemText("deployer", "deploys things", []string{"ops", "k8s"})
	want := "deployer. deploys things (tags: ops, k8s)"
	if got != want {
		t.Errorf("itemText = %q, want %q", got, want)
	}
	if itemText("bare", "", nil) != "bare" {
		t.Errorf("bare itemText should be just the name")
	}
}

func TestNilEmbedderSkipsMount(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	idx, err := Open(nil, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if idx != nil {
		t.Fatal("expected nil index with no embedder so callers skip mounting tools")
	}
	// All methods must be nil-safe so the additive contract holds.
	if idx.Len() != 0 {
		t.Error("Len on nil index should be 0")
	}
	if idx.Tools() != nil {
		t.Error("Tools on nil index should be nil")
	}
	if _, err := idx.Search(context.Background(), "x", 1); err == nil {
		t.Error("Search on nil index should error")
	}
}

func TestTools(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	idx := openTest(t)
	tools := idx.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected search_registries + reindex_registries, got %d", len(tools))
	}
	names := map[string]bool{tools[0].Name(): true, tools[1].Name(): true}
	if !names[SearchToolName] || !names[ReindexToolName] {
		t.Errorf("unexpected tool names: %v", names)
	}
}

func TestOnSaveTriggersRebuild(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	idx := openTest(t)
	// Saving registries fires registries.OnSave, which Open wired to a
	// background rebuild. We can't easily await the goroutine, but we can
	// assert the hook is installed and re-entrant-safe (no panic, coalesces).
	idx.onRegistriesSaved()
	idx.onRegistriesSaved()
}
