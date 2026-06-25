package precedents

import (
	"context"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/compress"
)

type fakeEmbedder struct{}

func (fakeEmbedder) Model() string { return "fake" }
func (fakeEmbedder) Dim() int      { return 3 }
func (fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case has(t, "deploy"), has(t, "kubernetes"):
			out[i] = []float32{1, 0, 0}
		case has(t, "database"), has(t, "migration"):
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

func TestIndexAndRecall(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	st, err := Open(fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	err = st.IndexStateLog(context.Background(), "alice_001", &compress.StateLog{
		Goal:      "deploy the kubernetes service",
		Decisions: []string{"rolled back the database migration"},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	hits, err := st.Query(context.Background(), "how do I deploy to kubernetes", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no precedents recalled")
	}
	if hits[0].Kind != "goal" {
		t.Errorf("expected goal as closest deploy precedent, got %q (%s)", hits[0].Kind, hits[0].Text)
	}
	if hits[0].SessionKey != "alice_001" {
		t.Errorf("session key not preserved: %q", hits[0].SessionKey)
	}
}

func TestIdempotentReindex(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	st, err := Open(fakeEmbedder{})
	if err != nil {
		t.Fatal(err)
	}
	sl := &compress.StateLog{Goal: "deploy", Decisions: []string{"a", "b"}}
	for i := 0; i < 3; i++ {
		if err := st.Add(context.Background(), "s1", sl, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	// goal + 2 decisions = 3 unique items, regardless of how many times added.
	if st.Len() != 3 {
		t.Fatalf("expected 3 items after idempotent re-index, got %d", st.Len())
	}
}

func TestNilEmbedderNoCrash(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	st, err := Open(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Query(context.Background(), "x", 3); err == nil {
		t.Fatal("expected error querying a no-embedder store")
	}
}
