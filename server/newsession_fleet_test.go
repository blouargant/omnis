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

// TestNewSession_ScopesToFleet verifies that POST /sessions accepts an
// optional "fleet" field (the "Coordinate" action) and, when it names an
// existing fleet, scopes the created session to it — both in the registry
// and on the persisted conversation file. An unknown fleet name is ignored
// (the session stays Ungrouped), mirroring the existing collection-fold
// behaviour for an unknown collection.
func TestNewSession_ScopesToFleet(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Isolated cwd store, mirroring newsession_cwd_test.go's setup so the
	// handler's cwd-persistence code path doesn't panic on a nil store.
	bashCwd = newBashCwdStore()

	reg := sessions.NewEmptyRegistry()
	engine := newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})

	if _, _, err := sessions.AddFleet("Payments", sessions.FleetMetaData{}); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}

	post := func(body string) (*httptest.ResponseRecorder, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body))
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
		return w, resp.SessionID
	}

	// A known fleet scopes the session, in-memory and persisted.
	_, id := post(`{"squad":"system","fleet":"Payments"}`)
	meta, ok := reg.Get(id)
	if !ok {
		t.Fatalf("session %s not found in registry", id)
	}
	if meta.Fleet != "Payments" {
		t.Fatalf("registry Fleet = %q, want %q", meta.Fleet, "Payments")
	}
	f, err := sessions.LoadConversationFile(id)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if f.Fleet != "Payments" {
		t.Fatalf("persisted Fleet = %q, want %q", f.Fleet, "Payments")
	}

	// An unknown fleet name is ignored (⇒ Ungrouped).
	_, ghostID := post(`{"squad":"system","fleet":"GhostFleet"}`)
	ghostMeta, ok := reg.Get(ghostID)
	if !ok {
		t.Fatalf("session %s not found in registry", ghostID)
	}
	if ghostMeta.Fleet != "" {
		t.Fatalf("registry Fleet for unknown fleet = %q, want empty", ghostMeta.Fleet)
	}
	ghostFile, err := sessions.LoadConversationFile(ghostID)
	if err != nil {
		t.Fatalf("load conversation: %v", err)
	}
	if ghostFile.Fleet != "" {
		t.Fatalf("persisted Fleet for unknown fleet = %q, want empty", ghostFile.Fleet)
	}
}
