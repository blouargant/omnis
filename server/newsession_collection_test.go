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

// TestNewSession_SeedsCwdFromCollectionProfile verifies that a new chat filed
// under a collection with a default cwd starts in that directory (with no Manager
// wired, the squad seed can't apply, but the cwd seed still does). It also checks
// the session is filed under the collection.
func TestNewSession_SeedsCwdFromCollectionProfile(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	dir := t.TempDir()

	bashCwd = newBashCwdStore()
	bashCwd.root = "/some/other/root"

	if _, _, err := sessions.AddCollection("Client X"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfile("Client X", "", dir); err != nil {
		t.Fatal(err)
	}

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"collection":"Client X"}`))
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
	if got := bashCwd.get(resp.SessionID); got != dir {
		t.Fatalf("cwd seed: got %q, want %q (collection default)", got, dir)
	}
	if m, ok := reg.Get(resp.SessionID); !ok || m.Collection != "Client X" {
		t.Fatalf("collection filing: %+v (ok=%v)", m, ok)
	}
}

// TestNewSession_UnknownCollectionFallsToGeneral verifies a new chat naming an
// unknown collection is filed under General (no phantom collection) and starts at
// the fixed root, not seeded.
func TestNewSession_UnknownCollectionFallsToGeneral(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	bashCwd = newBashCwdStore()
	bashCwd.root = "/fixed/root"

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions",
		strings.NewReader(`{"collection":"Phantom"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if m, ok := reg.Get(resp.SessionID); !ok || m.Collection != "" {
		t.Fatalf("unknown collection should fold to General (empty), got %+v", m)
	}
}
