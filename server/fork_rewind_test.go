package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestRewindForgetsSuggestionCache guards against a stale prompt-suggestion
// cache hit after a rewind: the cache is keyed only on turn count, so
// rewinding from N turns back to N-1 and then sending a new message (which
// brings the count back to N) could otherwise serve the discarded reply's
// suggestion. The rewind handler must forget the session's cached entry so
// the next /suggestion call regenerates instead of replaying stale state.
func TestRewindForgetsSuggestionCache(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "s1", Turns: 2})
	for i := 0; i < 2; i++ {
		if err := sessions.AppendConversationTurn("s1", "question", "answer"); err != nil {
			t.Fatal(err)
		}
	}
	d := serverDeps{
		Registry: reg,
		RunGuard: newSessionRunGuard(),
		rootCtx:  context.Background(),
		Suggest:  newSuggestStore(),
	}
	// Seed a cached suggestion at turns=1 — the turn count the rewind below
	// will restore, standing in for the discarded reply's stale suggestion.
	if got := d.Suggest.get(context.Background(), "s1", 1, func() (string, bool) {
		return "stale suggestion", true
	}); got != "stale suggestion" {
		t.Fatalf("seed: got %q", got)
	}

	h := newEngine(d)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s1/rewind", strings.NewReader(`{"turn_index":1}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rewind status = %d, body=%s", w.Code, w.Body.String())
	}

	calls := 0
	got := d.Suggest.get(context.Background(), "s1", 1, func() (string, bool) {
		calls++
		return "fresh suggestion", true
	})
	if calls != 1 || got != "fresh suggestion" {
		t.Fatalf("rewind must forget the cached suggestion so turns=1 regenerates: calls=%d got=%q (a stale hit means the rewind never called Suggest.forget)", calls, got)
	}
}
