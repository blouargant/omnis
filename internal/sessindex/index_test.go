package sessindex

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"testing"
)

// bagEmbedder is a deterministic bag-of-words embedder: each word is hashed into
// one of `bagDim` buckets and the vector is L2-normalised. It has no semantics,
// but it has the property the real thing has — texts sharing words are close —
// which is all these tests need to exercise the index end to end without a model.
type bagEmbedder struct{}

// Wide enough that unrelated words rarely collide — a narrow bag makes two
// unrelated turns score within a percent of each other and the ranking assertions
// below become coin flips.
const bagDim = 256

func (bagEmbedder) Model() string { return "bag" }
func (bagEmbedder) Dim() int      { return bagDim }
func (bagEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, bagDim)
		for _, w := range strings.Fields(strings.ToLower(t)) {
			h := fnv.New32a()
			_, _ = h.Write([]byte(strings.Trim(w, ".,?!:;")))
			v[h.Sum32()%bagDim]++
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm > 0 {
			n := float32(math.Sqrt(norm))
			for j := range v {
				v[j] /= n
			}
		}
		out[i] = v
	}
	return out, nil
}

// The semantic path end to end: index the corpus, find the right session, fold
// its turns into one row, and survive an unload.
func TestIndexReindexSearchRoundTrip(t *testing.T) {
	seedCorpus(t)
	ctx := context.Background()

	idx, err := Open(bagEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if idx == nil {
		t.Fatal("Open returned nil with an embedder present")
	}

	indexed, removed, err := idx.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Two sessions are searchable; the hidden one must not be indexed.
	if indexed != 2 || removed != 0 {
		t.Fatalf("reindex = %d indexed / %d removed, want 2/0 (hidden session excluded)", indexed, removed)
	}

	hits, err := idx.Search(ctx, "rotation matrix", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].SessionID != "brave-otter" {
		t.Fatalf("top hit = %+v, want the archived session brave-otter", hits)
	}
	if hits[0].Snippet == "" {
		t.Error("hit carries no snippet — the result row would render empty")
	}
	// One row per session, even though the session has several turns.
	seen := map[string]bool{}
	for _, h := range hits {
		if seen[h.SessionID] {
			t.Fatalf("session %s returned twice — hits were not folded", h.SessionID)
		}
		seen[h.SessionID] = true
	}

	// A second pass is a no-op: nothing changed, so nothing is re-embedded.
	indexed, removed, err = idx.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if indexed != 0 || removed != 0 {
		t.Errorf("second reindex = %d/%d, want 0/0 — unchanged sessions were re-embedded", indexed, removed)
	}

	// The idle sweeper drops the index between bursts; the next search must still
	// answer, having re-read it from disk.
	idx.Unload()
	hits, err = idx.Search(ctx, "rotation matrix", 5)
	if err != nil {
		t.Fatalf("search after unload: %v", err)
	}
	if len(hits) == 0 || hits[0].SessionID != "brave-otter" {
		t.Fatalf("search after unload lost its results: %+v", hits)
	}
}

// A session that grows must be re-indexed, and its NEW turn must be findable —
// this is the live path the idle indexer drives after every conversation.
func TestIndexSessionPicksUpNewTurns(t *testing.T) {
	seedCorpus(t)
	ctx := context.Background()

	idx, err := Open(bagEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.Reindex(ctx); err != nil {
		t.Fatal(err)
	}

	// The topic only exists in the new turn.
	hits, err := idx.Search(ctx, "grafana dashboard", 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.SessionID == "teaching-kite" && h.TurnIndex == 2 {
			t.Fatal("found a turn that has not been written yet — bad test setup")
		}
	}

	writeConv(t, "teaching-kite", conv{
		Title: "Kubernetes auditor",
		Turns: []turn{
			{UserText: "how do we improve audit precision?", AssistantText: "Add a k8s_auditor agent for a second pass.", At: at(3)},
			{UserText: "what model should it use?", AssistantText: "The hosted tier is enough; premium is 140x the cost.", At: at(3)},
			{UserText: "can we plot it on a grafana dashboard?", AssistantText: "Yes — scrape the bench metrics into a grafana dashboard.", At: at(1)},
		},
	})
	if err := idx.IndexSession(ctx, "teaching-kite"); err != nil {
		t.Fatal(err)
	}

	hits, err = idx.Search(ctx, "grafana dashboard", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].SessionID != "teaching-kite" || hits[0].TurnIndex != 2 {
		t.Fatalf("the new turn is not findable: %+v", hits)
	}
}

// A deleted session must leave the index — a hit on it would render a row that
// 404s when clicked.
func TestReindexDropsDeletedSessions(t *testing.T) {
	seedCorpus(t)
	ctx := context.Background()

	idx, err := Open(bagEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := idx.Reindex(ctx); err != nil {
		t.Fatal(err)
	}

	if err := removeConv("brave-otter"); err != nil {
		t.Fatal(err)
	}
	_, removed, err := idx.Reindex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	hits, err := idx.Search(ctx, "rotation matrix", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.SessionID == "brave-otter" {
			t.Fatal("deleted session still returned by search")
		}
	}
}
