package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestConversationSquadRoundTrip exercises the on-disk persistence of the
// per-session squad: writing it via setConversationSquad and reading it
// back through loadPersistedSessions (the path that rebuilds the session
// list after a server restart).
func TestConversationSquadRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("YOKE_HOME", tmp)
	logs := filepath.Join(tmp, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "sess-test"

	// Step 1: record the squad and a couple of turns.
	if err := setConversationSquad(sid, "research"); err != nil {
		t.Fatalf("setConversationSquad: %v", err)
	}
	if err := appendConversationTurn(sid, "hi", "hello"); err != nil {
		t.Fatalf("appendConversationTurn: %v", err)
	}

	// Step 2: read it back through loadPersistedSessions (which is what
	// the server uses on startup to rebuild the in-memory registry).
	got := loadPersistedSessions()
	var meta *SessionMeta
	for _, m := range got {
		if m.ID == sid {
			meta = m
			break
		}
	}
	if meta == nil {
		t.Fatalf("session %q missing from loadPersistedSessions(): %+v", sid, got)
	}
	if meta.Squad != "research" {
		t.Fatalf("Squad = %q, want %q", meta.Squad, "research")
	}

	// Step 3: legacy conversation files (no squad field) load with an
	// empty Squad, which the server interprets as "default" at runtime.
	legacy := filepath.Join(logs, "conversation_legacy.json")
	body := `{"turns":[{"user_text":"x","assistant_text":"y","at":"2024-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(legacy, []byte(body), 0o644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	got = loadPersistedSessions()
	var legacyMeta *SessionMeta
	for _, m := range got {
		if m.ID == "legacy" {
			legacyMeta = m
			break
		}
	}
	if legacyMeta == nil {
		t.Fatal("legacy session missing")
	}
	if legacyMeta.Squad != "" {
		t.Fatalf("legacy Squad = %q, want empty (default at runtime)", legacyMeta.Squad)
	}

	// Step 4: SessionMeta marshals squad as JSON omitempty.
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"squad":"research"`)) {
		t.Fatalf("marshalled meta missing squad: %s", b)
	}
}
