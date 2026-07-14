package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/sessions"
)

// searchResponse mirrors the JSON handleSearchSessions returns.
type searchResponse struct {
	Mode    string `json:"mode"`
	Warning string `json:"warning"`
	Scanned int    `json:"scanned"`
	Results []struct {
		SessionID string `json:"session_id"`
		Title     string `json:"title"`
		Archived  bool   `json:"archived"`
		TurnIndex int    `json:"turn_index"`
		Snippet   string `json:"snippet"`
	} `json:"results"`
}

// TestSearchSessionsRoute drives GET /api/search/sessions through the REAL router.
//
// Two things are under test. (1) The route registers at all: a `search` segment
// under /api/sessions/… would collide with the /sessions/:id wildcard in gin's
// route tree and panic at startup, which is why search lives at /api/search/… —
// newEngine() here is the regression guard for that. (2) With no embedder wired
// (SessionIndex nil — the default for a server with no embedding model), the
// search still answers by scanning the conversation files, and says so, rather
// than returning nothing.
func TestSearchSessionsRoute(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	now := time.Now()
	if err := sessions.SaveConversationFile("teaching-kite", &sessions.ConversationFile{
		Title: "Kubernetes auditor",
		Turns: []sessions.ConversationTurn{
			{UserText: "how do we improve audit precision?", AssistantText: "Add a second-pass auditor agent.", At: now},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// An ARCHIVED session must be searchable — being able to find it is the point.
	if err := sessions.SaveConversationFile("brave-otter", &sessions.ConversationFile{
		Title:    "Old notes",
		Archived: true,
		Turns: []sessions.ConversationTurn{
			{UserText: "what about audit precision here?", AssistantText: "Same conclusion.", At: now.Add(-72 * time.Hour)},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// A HIDDEN session (the in-Settings assistant, the search agent's own chat)
	// must never surface, or the search results would be polluted by the searching.
	if err := sessions.SaveConversationFile("quiet-mole", &sessions.ConversationFile{
		Title:  "Settings assistant",
		Hidden: true,
		Turns: []sessions.ConversationTurn{
			{UserText: "audit precision", AssistantText: "theme changed", At: now},
		},
	}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/search/sessions?q=audit+precision", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Mode != "scan" || got.Warning != warnNoEmbedder {
		t.Errorf("mode/warning = %q/%q, want scan/%s — the UI must be told the search was a direct scan",
			got.Mode, got.Warning, warnNoEmbedder)
	}
	if len(got.Results) != 2 {
		t.Fatalf("want 2 results (active + archived, hidden excluded), got %d: %+v", len(got.Results), got.Results)
	}
	if got.Scanned != 2 {
		t.Errorf("scanned = %d, want 2 (the hidden session must not even be read)", got.Scanned)
	}
	ids := map[string]bool{}
	for _, r := range got.Results {
		ids[r.SessionID] = true
		if r.Snippet == "" {
			t.Errorf("result %s has no snippet — the row would render empty", r.SessionID)
		}
	}
	if !ids["teaching-kite"] || !ids["brave-otter"] {
		t.Errorf("missing expected sessions: %+v", got.Results)
	}
	if ids["quiet-mole"] {
		t.Error("hidden session surfaced in search results")
	}

	// exclude_archived narrows it back to active sessions.
	req = httptest.NewRequest(http.MethodGet, "/api/search/sessions?q=audit+precision&exclude_archived=1", nil)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	got = searchResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Results) != 1 || got.Results[0].SessionID != "teaching-kite" {
		t.Fatalf("exclude_archived ignored: %+v", got.Results)
	}
}

// The status route tells the UI, before the user types, whether searching will be
// semantic or a slow scan.
func TestSessionSearchStatusRoute(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	engine := newEngine(serverDeps{Registry: sessions.NewEmptyRegistry(), rootCtx: context.Background()})

	req := httptest.NewRequest(http.MethodGet, "/api/search/sessions/status", nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Semantic bool   `json:"semantic"`
		Chunks   int    `json:"chunks"`
		Squad    string `json:"squad"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Semantic {
		t.Error("semantic = true with no embedder configured")
	}
	if got.Squad != SessionSearchSquad {
		t.Errorf("squad = %q, want %q — the UI runs the agent search against this squad", got.Squad, SessionSearchSquad)
	}
}

// The bug this guards: the search agent's hidden session is REUSED across
// searches, so without a reset it carries the previous search's turns into the
// next one. It then answers "you are asking again — I already found it" and stops
// calling report_sessions, and since the result list is built from that call, the
// user is told nothing was found for a query the agent answered correctly.
//
// The reset used to be a separate POST /rewind the client fired before the turn.
// That call is tryAcquire-and-409-if-busy while a turn QUEUES on the same guard,
// so a reset racing an in-flight turn was silently dropped — which is exactly what
// happened live (3 turns accumulated). It is now part of the turn itself, under
// the guard, so it cannot lose that race.
func TestResetContextClearsHistory(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	reg := sessions.NewEmptyRegistry()
	meta := reg.New("session search")

	if err := sessions.SaveConversationFile(meta.ID, &sessions.ConversationFile{
		Title: "Session search",
		Squad: "session search",
		Turns: []sessions.ConversationTurn{
			{UserText: "azure AI", AssistantText: "Found sought-turtle.", At: time.Now()},
			{UserText: "azure AI", AssistantText: "You are asking again; I already found it.", At: time.Now()},
		},
	}); err != nil {
		t.Fatal(err)
	}
	reg.SetTurns(meta.ID, 2)

	// A nil Manager is fine here: the in-memory ADK reseed is a no-op without one,
	// and the persisted-history half is what makes the next turn stateless.
	resetSessionContext(serverDeps{Registry: reg, rootCtx: context.Background()}, meta)

	turns, err := sessions.LoadConversationTurns(meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 0 {
		t.Fatalf("history not cleared: %d turn(s) survived the reset", len(turns))
	}
	if m, _ := reg.Get(meta.ID); m.Turns != 0 {
		t.Errorf("registry turn count = %d, want 0", m.Turns)
	}
}
