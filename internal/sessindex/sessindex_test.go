package sessindex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConv drops a conversation file into the temp $OMNIS_HOME logs dir.
func writeConv(t *testing.T, id string, c conv) {
	t.Helper()
	dir := filepath.Join(os.Getenv("OMNIS_HOME"), "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation_"+id+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func at(days int) time.Time {
	return time.Now().Add(-time.Duration(days) * 24 * time.Hour)
}

// seedCorpus lays down a small, realistic corpus: an active session, an archived
// one, and a hidden utility session.
func seedCorpus(t *testing.T) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())

	writeConv(t, "teaching-kite", conv{
		Title: "Kubernetes auditor",
		Turns: []turn{
			{UserText: "how do we improve audit precision?", AssistantText: "Add a k8s_auditor agent for a second pass.", At: at(3)},
			{UserText: "what model should it use?", AssistantText: "The hosted tier is enough; premium is 140x the cost.", At: at(3)},
		},
	})
	writeConv(t, "brave-otter", conv{
		Title:    "Embedding dimensions",
		Archived: true,
		Turns: []turn{
			{UserText: "why is the index so big?", AssistantText: "The rotation matrix is O(dim squared); prefer dim 768.", At: at(10)},
		},
	})
	writeConv(t, "quiet-mole", conv{
		Title:  "Settings assistant",
		Hidden: true,
		Turns: []turn{
			{UserText: "change the theme", AssistantText: "Done — audit precision theme applied.", At: at(1)},
		},
	})
}

func TestScanFindsSessionByContent(t *testing.T) {
	seedCorpus(t)

	hits, stats, err := Scan(context.Background(), "audit precision", ScanOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("want 1 hit, got %d: %+v", len(hits), hits)
	}
	if hits[0].SessionID != "teaching-kite" {
		t.Errorf("session = %q, want teaching-kite", hits[0].SessionID)
	}
	if hits[0].TurnIndex != 0 {
		t.Errorf("turn = %d, want 0 (the turn that actually matches)", hits[0].TurnIndex)
	}
	if !strings.Contains(strings.ToLower(hits[0].Snippet), "audit precision") {
		t.Errorf("snippet does not show the match: %q", hits[0].Snippet)
	}
	// The hidden session's reply also contains "audit precision" — it must not be
	// scanned at all, or the search results would be polluted by the searches and
	// the settings chats themselves.
	if stats.Scanned != 2 {
		t.Errorf("scanned = %d, want 2 (hidden session excluded)", stats.Scanned)
	}
}

func TestScanIncludesArchivedByDefault(t *testing.T) {
	seedCorpus(t)

	hits, _, err := Scan(context.Background(), "rotation matrix", ScanOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].SessionID != "brave-otter" {
		t.Fatalf("archived session not found: %+v", hits)
	}

	// ...and can be excluded on request.
	hits, _, err = Scan(context.Background(), "rotation matrix", ScanOpts{ExcludeArchived: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("ExcludeArchived ignored: %+v", hits)
	}
}

// A scan is an AND over the query terms: every term must appear, or a long
// natural-language query would match any session sharing one common word.
func TestScanRequiresEveryTerm(t *testing.T) {
	seedCorpus(t)

	hits, _, err := Scan(context.Background(), "audit unicorn", ScanOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("want no hit when one term is absent, got %+v", hits)
	}
}

// One session must occupy one row, no matter how many of its turns match: the
// user is looking for a conversation, not a paragraph.
func TestScanFoldsHitsBySession(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	writeConv(t, "many-turns", conv{
		Title: "Repeated topic",
		Turns: []turn{
			{UserText: "tell me about widgets", AssistantText: "widgets are fine", At: at(2)},
			{UserText: "more on widgets", AssistantText: "widgets widgets widgets", At: at(2)},
			{UserText: "and widgets again", AssistantText: "still widgets", At: at(2)},
		},
	})

	hits, _, err := Scan(context.Background(), "widgets", ScanOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("want the session folded into 1 row, got %d", len(hits))
	}
	// The best-scoring turn wins — turn 1 says "widgets" most often.
	if hits[0].TurnIndex != 1 {
		t.Errorf("turn = %d, want 1 (the densest match)", hits[0].TurnIndex)
	}
}

// Enrich resolves title/archived from the conversation file at query time, so a
// session renamed or archived after indexing never shows stale metadata.
func TestEnrichResolvesLiveMetadata(t *testing.T) {
	seedCorpus(t)

	got := Enrich([]Hit{{SessionID: "brave-otter", TurnIndex: 0}})
	if len(got) != 1 {
		t.Fatalf("want 1 result, got %d", len(got))
	}
	if got[0].Title != "Embedding dimensions" || !got[0].Archived {
		t.Errorf("stale metadata: %+v", got[0])
	}

	// A session deleted after it was indexed must drop out rather than render as
	// an empty row that 404s when clicked.
	got = Enrich([]Hit{{SessionID: "gone-forever"}})
	if len(got) != 0 {
		t.Errorf("deleted session still returned: %+v", got)
	}
}

// A turn longer than one chunk must still be indexed in full, or the tail of a
// long conversation would be unfindable.
func TestChunkConvWindowsLongTurns(t *testing.T) {
	long := strings.Repeat("alpha ", maxChunkChars) // well over one window
	c := &conv{ID: "s", Title: "T", Turns: []turn{{UserText: "q", AssistantText: long}}}

	items, ids := chunkConv(c)
	if len(items) < 2 {
		t.Fatalf("long turn not windowed: %d chunk(s)", len(items))
	}
	if len(items) != len(ids) {
		t.Fatalf("items/ids mismatch: %d vs %d", len(items), len(ids))
	}
	seen := map[uint64]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate chunk id %d — chunks would overwrite each other", id)
		}
		seen[id] = true
	}
}

// The content hash must cover the turns and nothing else: flipping an unrelated
// flag (archived, harvested, cwd) must not force a re-embed of the session.
func TestContentHashIgnoresMetadata(t *testing.T) {
	turns := []turn{{UserText: "a", AssistantText: "b", At: at(1)}}
	base := &conv{ID: "s", Title: "T", Turns: turns}
	archived := &conv{ID: "s", Title: "T", Archived: true, Collection: "Work", Turns: turns}

	if contentHash(base) != contentHash(archived) {
		t.Error("metadata change altered the content hash — the session would be re-embedded for nothing")
	}

	changed := &conv{ID: "s", Turns: append(append([]turn{}, turns...), turn{UserText: "c", AssistantText: "d"})}
	if contentHash(base) == contentHash(changed) {
		t.Error("a new turn did not change the content hash — it would never be indexed")
	}
}

// The lexical boost is what stops a keyword query from burying the session that
// literally says the words. Whole-word matching is load-bearing: "ai" must not
// match inside "said"/"available"/"again", or every session would score a boost.
func TestLexicalOverlap(t *testing.T) {
	terms := queryTerms("azure AI")

	if got := lexicalOverlap("The Azure AI subscription was upgraded", terms); got != 1 {
		t.Errorf("full literal match = %v, want 1", got)
	}
	if got := lexicalOverlap("Comparing AI models on vLLM", terms); got != 0.5 {
		t.Errorf("half match = %v, want 0.5", got)
	}
	if got := lexicalOverlap("He said the tokens were available again", terms); got != 0 {
		t.Errorf("substring false positive: %v — 'ai' matched inside said/available/again", got)
	}
	if got := lexicalOverlap("nothing relevant here", terms); got != 0 {
		t.Errorf("no match = %v, want 0", got)
	}
	// Punctuation must not hide a word: "Azure AI?" still contains both terms.
	if got := lexicalOverlap("what is Azure AI?", terms); got != 1 {
		t.Errorf("punctuation broke the match: %v", got)
	}
}
