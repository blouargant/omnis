package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestNewSession_PersistsStartingCwd verifies that a plain new chat (no
// "Open Chat here" dir) durably records the working directory it starts in, so
// a server restart in a different process cwd resumes the session in its
// original folder instead of silently re-resolving to the new root.
func TestNewSession_PersistsStartingCwd(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Isolated cwd store with a known fixed root + the same persist hook main.go
	// wires so a set() reaches the conversation file.
	bashCwd = newBashCwdStore()
	const root = "/home/user/projectA"
	bashCwd.root = root
	bashCwd.setPersist(func(id, dir string) {
		if err := sessions.SetConversationCwd(id, dir); err != nil {
			t.Errorf("persist cwd: %v", err)
		}
	})

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	// Create a normal chat with no pinned dir.
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create session: status %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if resp.SessionID == "" {
		t.Fatal("expected a new session id")
	}

	// A turn keeps the session restorable (LoadPersistedSessions drops 0-turn files).
	if err := sessions.AppendConversationTurn(resp.SessionID, "hi", "hello"); err != nil {
		t.Fatalf("append turn: %v", err)
	}

	// The starting root must have been written to the conversation file.
	f, err := sessions.LoadConversationFile(resp.SessionID)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if f.Cwd != root {
		t.Fatalf("persisted cwd: got %q, want %q (starting root)", f.Cwd, root)
	}

	// Simulate a restart in a DIFFERENT directory: the rebuilt registry must
	// surface the persisted cwd on the session meta so the boot loop seeds it,
	// rather than falling back to the new process root.
	restarted := sessions.NewRegistry()
	var meta *sessions.SessionMeta
	for _, m := range restarted.List() {
		if m.ID == resp.SessionID {
			meta = m
			break
		}
	}
	if meta == nil {
		t.Fatalf("session %s was not restored after restart", resp.SessionID)
	}
	if meta.Cwd != root {
		t.Errorf("restored meta cwd: got %q, want %q", meta.Cwd, root)
	}
}
