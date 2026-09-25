package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/sessions"
)

// TestParseImportedConversation covers the three accepted import shapes: the
// export envelope, a bare ConversationFile, and a legacy plain-array transcript.
func TestParseImportedConversation(t *testing.T) {
	cases := map[string]string{
		"envelope":  `{"kind":"omnis.session.export","version":1,"conversation":{"title":"T","squad":"coding","turns":[{"user_text":"hi","assistant_text":"yo"}]}}`,
		"bare-file": `{"title":"T","squad":"coding","turns":[{"user_text":"hi","assistant_text":"yo"}]}`,
		"legacy":    `[{"user_text":"hi","assistant_text":"yo"}]`,
	}
	for name, body := range cases {
		conv, err := parseImportedConversation([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(conv.Turns) != 1 || conv.Turns[0].UserText != "hi" {
			t.Fatalf("%s: unexpected turns %+v", name, conv.Turns)
		}
	}
	if _, err := parseImportedConversation([]byte("not json")); err == nil {
		t.Fatalf("expected error for garbage input")
	}
	if _, err := parseImportedConversation([]byte("  ")); err == nil {
		t.Fatalf("expected error for empty input")
	}
}

// TestExportImportRoundTrip drives GET /export → POST /import through the real
// router (which also proves the /import/session route registers without
// conflicting with the /sessions/:id wildcard) and asserts the transcript
// survives while machine-specific fields (cwd, goal) are dropped on import.
func TestExportImportRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	bashCwd = newBashCwdStore()
	bashCwd.root = "/home/user/proj"

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	// Seed a source session with a conversation file carrying transient fields.
	src := reg.New("coding")
	if err := sessions.SaveConversationFile(src.ID, &sessions.ConversationFile{
		Title: "My chat",
		Squad: "coding",
		Cwd:   "/some/other/machine/path",
		Goal:  "finish the thing",
		Turns: []sessions.ConversationTurn{
			{UserText: "hello", AssistantText: "hi there"},
			{UserText: "again", AssistantText: "sure"},
		},
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	reg.SetTurns(src.ID, 2)
	reg.SetTitle(src.ID, "My chat")

	// Export.
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions/"+src.ID+"/export", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d, body %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Fatalf("missing attachment disposition: %q", cd)
	}
	exported := w.Body.Bytes()
	var env sessionExport
	if err := json.Unmarshal(exported, &env); err != nil {
		t.Fatalf("export not valid json: %v", err)
	}
	if env.Kind != exportKind || env.Version != exportVersion || env.Conversation == nil {
		t.Fatalf("bad envelope: %+v", env)
	}
	if len(env.Conversation.Turns) != 2 {
		t.Fatalf("exported %d turns, want 2", len(env.Conversation.Turns))
	}

	// Import the exported bytes.
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/import/session", strings.NewReader(string(exported))))
	if w.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
		Squad     string `json:"squad"`
		Title     string `json:"title"`
		Turns     int    `json:"turns"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("import response: %v", err)
	}
	if resp.SessionID == "" || resp.SessionID == src.ID {
		t.Fatalf("import should mint a NEW id, got %q (src %q)", resp.SessionID, src.ID)
	}
	if resp.Turns != 2 || resp.Title != "My chat" || resp.Squad != "coding" {
		t.Fatalf("unexpected import response: %+v", resp)
	}

	// The imported session must be registered and its file must preserve turns
	// while dropping the transient fields.
	if _, ok := reg.Get(resp.SessionID); !ok {
		t.Fatalf("imported session not registered")
	}
	f, err := sessions.LoadConversationFile(resp.SessionID)
	if err != nil {
		t.Fatalf("load imported: %v", err)
	}
	if len(f.Turns) != 2 || f.Turns[1].AssistantText != "sure" {
		t.Fatalf("imported turns wrong: %+v", f.Turns)
	}
	if f.Title != "My chat" || f.Squad != "coding" {
		t.Fatalf("imported title/squad wrong: %q / %q", f.Title, f.Squad)
	}
	if f.Cwd != "" || f.Goal != "" {
		t.Fatalf("transient fields not dropped: cwd=%q goal=%q", f.Cwd, f.Goal)
	}
}

// TestExportDropsPendingQuestions checks that a durable question pending on
// the source session never leaves the instance: it must not appear in the
// exported JSON, and the imported session must start with none of its own.
func TestExportDropsPendingQuestions(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	bashCwd = newBashCwdStore()
	bashCwd.root = "/home/user/proj"

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	src := reg.New("coding")
	if err := sessions.SaveConversationFile(src.ID, &sessions.ConversationFile{
		Title: "My chat",
		Squad: "coding",
		Turns: []sessions.ConversationTurn{
			{UserText: "hello", AssistantText: "hi there"},
		},
	}); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	reg.SetTurns(src.ID, 1)
	reg.SetTitle(src.ID, "My chat")
	if err := sessions.AddPendingQuestion(src.ID, sessions.PendingQuestion{
		Question: askuser.Question{ID: "q1", SessionID: src.ID, Kind: askuser.KindText, Prompt: "Which env?", Durable: true},
		TurnID:   "t1",
		Prompt:   "deploy it",
		Squad:    "coding",
		AskedAt:  time.Now(),
	}); err != nil {
		t.Fatalf("add pending question: %v", err)
	}

	// Export.
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sessions/"+src.ID+"/export", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("export status = %d, body %s", w.Code, w.Body.String())
	}
	exported := w.Body.Bytes()
	if strings.Contains(string(exported), "pending_questions") {
		t.Fatalf("exported JSON must not contain pending_questions: %s", exported)
	}

	// Import the exported bytes.
	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/import/session", strings.NewReader(string(exported))))
	if w.Code != http.StatusCreated {
		t.Fatalf("import status = %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("import response: %v", err)
	}
	got, err := sessions.LoadPendingQuestions(resp.SessionID)
	if err != nil {
		t.Fatalf("load pending questions: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("imported session must have no pending questions, got %+v", got)
	}
}

// TestImportRejectsEmpty checks that a body with no turns is a 400.
func TestImportRejectsEmpty(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/import/session", strings.NewReader(`{"conversation":{"turns":[]}}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty import status = %d, want 400", w.Code)
	}
}
