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

// TestHandleFork_InheritsFleetScope verifies that forking a Fleet (Conductor)
// chat carries the source session's Fleet scope onto the fork, both in-memory
// and on disk. Without this, a forked Conductor (marked a FleetExperiment by
// handleFork) would start with Fleet=="" and silently drop to the Ungrouped
// project pool, unable to dispatch to its own fleet's projects. See
// server/fork_rewind.go handleFork and internal/sessions/history.go
// ForkConversation.
func TestHandleFork_InheritsFleetScope(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	bashCwd = newBashCwdStore()

	if _, _, err := sessions.AddFleet("Payments", sessions.FleetMetaData{}); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}

	reg := sessions.NewEmptyRegistry()
	src := reg.New("Fleet")
	if err := sessions.AppendConversationTurn(src.ID, "hello", "hi there"); err != nil {
		t.Fatalf("seed source turn: %v", err)
	}

	// Scope the source Conductor to the fleet. SetFleet updates the in-memory
	// registry and persists asynchronously; call the synchronous persist too so
	// the on-disk source file reliably carries Fleet before ForkConversation
	// (which reads from disk) runs below.
	reg.SetFleet(src.ID, "Payments")
	if err := sessions.SetConversationFleet(src.ID, "Payments"); err != nil {
		t.Fatalf("persist source fleet: %v", err)
	}

	d := serverDeps{
		Registry: reg,
		RunGuard: newSessionRunGuard(),
		rootCtx:  context.Background(),
	}

	r := gin.New()
	r.POST("/api/sessions/:id/fork", handleFork(d))

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+src.ID+"/fork", strings.NewReader(`{"full": true}`))
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

	// In-memory registry reflects the inherited fleet scope right away.
	forkMeta, ok := reg.Get(resp.SessionID)
	if !ok {
		t.Fatalf("forked session %q missing from registry", resp.SessionID)
	}
	if forkMeta.Fleet != "Payments" {
		t.Errorf("in-memory Fleet = %q, want %q", forkMeta.Fleet, "Payments")
	}

	// Persisted conversation file also carries the inherited fleet scope.
	f, err := sessions.LoadConversationFile(resp.SessionID)
	if err != nil {
		t.Fatalf("load fork conversation: %v", err)
	}
	if f.Fleet != "Payments" {
		t.Errorf("persisted Fleet = %q, want %q", f.Fleet, "Payments")
	}
}
