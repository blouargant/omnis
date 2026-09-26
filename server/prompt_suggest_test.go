package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/configedit"
	"github.com/blouargant/omnis/internal/sessions"
)

type suggestResp struct {
	Suggestion string `json:"suggestion"`
	Turns      int    `json:"turns"`
	Busy       bool   `json:"busy"`
}

// suggestFixture builds a router with one session "s1" holding `turns` persisted
// turns, and a fake generator that counts calls and returns (text, ok).
func suggestFixture(t *testing.T, turns int, text string, ok bool) (http.Handler, *sessions.Registry, *atomic.Int32, *sessionRunGuard) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "s1", Turns: turns})
	for i := 0; i < turns; i++ {
		if err := sessions.AppendConversationTurn("s1", "question", "answer"); err != nil {
			t.Fatal(err)
		}
	}
	calls := &atomic.Int32{}
	guard := newSessionRunGuard()
	d := serverDeps{
		Registry: reg,
		RunGuard: guard,
		rootCtx:  context.Background(),
		Suggest:  newSuggestStore(),
		SuggestFn: func(ctx context.Context, sid string, ex []toolkitagent.Exchange) (string, bool) {
			calls.Add(1)
			if len(ex) == 0 || ex[len(ex)-1].User != "question" {
				t.Errorf("generator got unexpected exchanges: %+v", ex)
			}
			return text, ok
		},
	}
	return newEngine(d), reg, calls, guard
}

func getSuggestion(t *testing.T, h http.Handler, id string) (int, suggestResp) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/"+id+"/suggestion", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var r suggestResp
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatalf("decode: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, r
}

func writePrefs(t *testing.T, body string) {
	t.Helper()
	p := configedit.PreferencesPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSuggestionCachedPerTurnCount(t *testing.T) {
	h, reg, calls, _ := suggestFixture(t, 1, "Show me the diff", true)
	for i := 0; i < 2; i++ {
		code, r := getSuggestion(t, h, "s1")
		if code != 200 || r.Suggestion != "Show me the diff" || r.Turns != 1 {
			t.Fatalf("got %d %+v", code, r)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("want 1 generation for an unchanged turn count, got %d", calls.Load())
	}
	// A new turn invalidates the entry.
	_ = sessions.AppendConversationTurn("s1", "question", "answer")
	reg.SetTurns("s1", 2)
	if _, r := getSuggestion(t, h, "s1"); r.Turns != 2 {
		t.Fatalf("turns = %d, want 2", r.Turns)
	}
	if calls.Load() != 2 {
		t.Fatalf("want a new generation after a new turn, got %d calls", calls.Load())
	}
}

func TestSuggestionFailureNotCachedNoneCached(t *testing.T) {
	h, _, calls, _ := suggestFixture(t, 1, "", false)
	getSuggestion(t, h, "s1")
	getSuggestion(t, h, "s1")
	if calls.Load() != 2 {
		t.Fatalf("a failure must be retried, got %d calls", calls.Load())
	}

	h2, _, calls2, _ := suggestFixture(t, 1, "", true) // model said NONE
	getSuggestion(t, h2, "s1")
	getSuggestion(t, h2, "s1")
	if calls2.Load() != 1 {
		t.Fatalf("a NONE answer must be cached, got %d calls", calls2.Load())
	}
}

func TestSuggestionSkippedWithoutModelCall(t *testing.T) {
	t.Run("pref off", func(t *testing.T) {
		h, _, calls, _ := suggestFixture(t, 1, "x", true)
		writePrefs(t, `{"prompt_suggestions": false}`)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("pref off: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("pref explicitly on", func(t *testing.T) {
		h, _, calls, _ := suggestFixture(t, 1, "x", true)
		writePrefs(t, `{"prompt_suggestions": true}`)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "x" || calls.Load() != 1 {
			t.Fatalf("pref on: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("archived", func(t *testing.T) {
		h, reg, calls, _ := suggestFixture(t, 1, "x", true)
		reg.SetArchived("s1", true)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("archived: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("hidden", func(t *testing.T) {
		h, reg, calls, _ := suggestFixture(t, 1, "x", true)
		reg.SetHidden("s1", true)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("hidden: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("no turns", func(t *testing.T) {
		h, _, calls, _ := suggestFixture(t, 0, "x", true)
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || calls.Load() != 0 {
			t.Fatalf("no turns: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("turn in flight reports busy", func(t *testing.T) {
		h, _, calls, guard := suggestFixture(t, 1, "x", true)
		release := guard.acquire("s1")
		defer release()
		if _, r := getSuggestion(t, h, "s1"); r.Suggestion != "" || !r.Busy || calls.Load() != 0 {
			t.Fatalf("busy: %+v, %d calls", r, calls.Load())
		}
	})
	t.Run("unknown session", func(t *testing.T) {
		h, _, _, _ := suggestFixture(t, 1, "x", true)
		if code, _ := getSuggestion(t, h, "nope"); code != http.StatusNotFound {
			t.Fatalf("code %d, want 404", code)
		}
	})
}

func TestSuggestionSingleFlight(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "s1", Turns: 1})
	_ = sessions.AppendConversationTurn("s1", "question", "answer")
	var calls atomic.Int32
	gate := make(chan struct{})
	d := serverDeps{
		Registry: reg, RunGuard: newSessionRunGuard(), rootCtx: context.Background(),
		Suggest: newSuggestStore(),
		SuggestFn: func(context.Context, string, []toolkitagent.Exchange) (string, bool) {
			calls.Add(1)
			<-gate
			return "same", true
		},
	}
	h := newEngine(d)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); getSuggestion(t, h, "s1") }()
	}
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("want 1 generation for concurrent requests, got %d", calls.Load())
	}
}

func TestSuggestStoreForget(t *testing.T) {
	s := newSuggestStore()
	n := 0
	gen := func() (string, bool) { n++; return "x", true }
	s.get(context.Background(), "s1", 1, gen)
	s.forget("s1")
	s.get(context.Background(), "s1", 1, gen)
	if n != 2 {
		t.Fatalf("forget must drop the entry, got %d generations", n)
	}
	var nilStore *suggestStore
	nilStore.forget("s1") // must not panic
}
