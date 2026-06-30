package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// TestHandleFork_InheritsSourceCwd verifies the forked session starts in the
// same working directory ("environment") as the source session.
func TestHandleFork_InheritsSourceCwd(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Fresh, isolated cwd store for this test.
	bashCwd = newBashCwdStore()

	reg := sessions.NewEmptyRegistry()
	src := reg.New("default")
	if err := sessions.AppendConversationTurn(src.ID, "hello", "hi there"); err != nil {
		t.Fatalf("seed source turn: %v", err)
	}

	const wantDir = "/some/project/subdir"
	bashCwd.set(src.ID, wantDir)

	d := serverDeps{
		Registry: reg,
		RunGuard: newSessionRunGuard(),
		rootCtx:  context.Background(),
	}

	r := gin.New()
	r.POST("/api/sessions/:id/fork", handleFork(d))

	body := strings.NewReader(`{"full": true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+src.ID+"/fork", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("fork: got status %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode fork response: %v", err)
	}
	if resp.SessionID == "" || resp.SessionID == src.ID {
		t.Fatalf("expected a new fork id, got %q (src %q)", resp.SessionID, src.ID)
	}

	if got := bashCwd.get(resp.SessionID); got != wantDir {
		t.Errorf("fork cwd: got %q, want %q (source dir)", got, wantDir)
	}
}

// TestForkCwd_PersistsAndRestores verifies the forked session's working
// directory is written to disk and survives a server restart (the gap a
// purely in-memory store left open).
func TestForkCwd_PersistsAndRestores(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	bashCwd = newBashCwdStore()
	// Wire the same durable-write hook the server installs in main.go.
	bashCwd.setPersist(func(id, dir string) {
		if err := sessions.SetConversationCwd(id, dir); err != nil {
			t.Errorf("persist cwd: %v", err)
		}
	})

	reg := sessions.NewEmptyRegistry()
	src := reg.New("default")
	if err := sessions.AppendConversationTurn(src.ID, "hello", "hi"); err != nil {
		t.Fatalf("seed source turn: %v", err)
	}
	const wantDir = "/home/user/project/sub"
	bashCwd.set(src.ID, wantDir) // persists src cwd

	d := serverDeps{Registry: reg, RunGuard: newSessionRunGuard(), rootCtx: context.Background()}
	r := gin.New()
	r.POST("/api/sessions/:id/fork", handleFork(d))

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+src.ID+"/fork", strings.NewReader(`{"full":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("fork: status %d, body %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)

	// The fork's cwd must have been written to its conversation file.
	f, err := sessions.LoadConversationFile(resp.SessionID)
	if err != nil {
		t.Fatalf("load fork conversation: %v", err)
	}
	if f.Cwd != wantDir {
		t.Errorf("persisted fork cwd: got %q, want %q", f.Cwd, wantDir)
	}

	// Simulate a restart: a fresh in-memory store reseeded from disk must
	// resolve both the source and the fork back to wantDir.
	bashCwd = newBashCwdStore()
	for _, m := range sessions.LoadPersistedSessions() {
		if m.Cwd != "" {
			bashCwd.seed(m.ID, m.Cwd)
		}
	}
	if got := bashCwd.get(src.ID); got != wantDir {
		t.Errorf("after restart, source cwd: got %q, want %q", got, wantDir)
	}
	if got := bashCwd.get(resp.SessionID); got != wantDir {
		t.Errorf("after restart, fork cwd: got %q, want %q", got, wantDir)
	}
}
