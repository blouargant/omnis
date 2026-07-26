package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionFleetFieldRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OMNIS_HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "fleet-field-test"
	if err := AppendConversationTurn(sid, "hi", "hello"); err != nil {
		t.Fatalf("AppendConversationTurn: %v", err)
	}
	reloaded := func() *SessionMeta {
		for _, m := range LoadPersistedSessions() {
			if m.ID == sid {
				return m
			}
		}
		return nil
	}

	if err := SetConversationFleet(sid, "Payments"); err != nil {
		t.Fatalf("SetConversationFleet: %v", err)
	}
	meta := reloaded()
	if meta == nil {
		t.Fatalf("session %q missing after setting fleet", sid)
	}
	if meta.Fleet != "Payments" {
		t.Fatalf("Fleet = %q after set, want Payments", meta.Fleet)
	}
	if meta.Turns != 1 {
		t.Fatalf("Turns = %d, want 1 (turns must be preserved)", meta.Turns)
	}

	if err := SetConversationFleet(sid, ""); err != nil {
		t.Fatalf("SetConversationFleet(clear): %v", err)
	}
	if meta := reloaded(); meta == nil || meta.Fleet != "" {
		t.Fatalf("Fleet still set after clearing: %+v", meta)
	}
}
