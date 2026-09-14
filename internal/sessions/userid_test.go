package sessions

import (
	"encoding/json"
	"os"
	"testing"
)

func TestUserIDDefaultsToWebUserAndIsConfigurable(t *testing.T) {
	t.Cleanup(func() { SetUserID("") })

	if got := UserID(); got != DefaultUserID {
		t.Fatalf("UserID() before SetUserID = %q, want %q", got, DefaultUserID)
	}
	SetUserID("  alice ")
	if got := UserID(); got != "alice" {
		t.Fatalf("UserID() after SetUserID = %q, want trimmed %q", got, "alice")
	}
	reg := NewEmptyRegistry()
	if m := reg.New("system"); m.UserID != "alice" {
		t.Errorf("Registry.New: UserID = %q, want alice", m.UserID)
	}
	m, ok := reg.NewWithName("named-one", "system")
	if !ok || m.UserID != "alice" {
		t.Errorf("Registry.NewWithName: ok=%v UserID=%q, want alice", ok, m.UserID)
	}
	SetUserID("")
	if got := UserID(); got != DefaultUserID {
		t.Fatalf("SetUserID(\"\") must restore the default, got %q", got)
	}
}

func TestConversationFilePersistsUserID(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Cleanup(func() { SetUserID("") })
	SetUserID("alice")

	if err := AppendConversationTurn("teaching-kite", "hi", "hello"); err != nil {
		t.Fatal(err)
	}
	f, err := LoadConversationFile("teaching-kite")
	if err != nil {
		t.Fatal(err)
	}
	if f.UserID != "alice" {
		t.Fatalf("persisted user_id = %q, want alice", f.UserID)
	}
	// A fork is written through SaveConversationFile too, so it inherits the owner.
	if _, err := ForkConversation("teaching-kite", "teaching-kite-fork", "fork", 1); err != nil {
		t.Fatal(err)
	}
	ff, err := LoadConversationFile("teaching-kite-fork")
	if err != nil || ff == nil || ff.UserID != "alice" {
		t.Fatalf("fork user_id: file=%v err=%v, want alice", ff, err)
	}
	metas := LoadPersistedSessions()
	if len(metas) != 2 {
		t.Fatalf("LoadPersistedSessions returned %d sessions, want 2", len(metas))
	}
	for _, m := range metas {
		if m.UserID != "alice" {
			t.Errorf("LoadPersistedSessions %s: UserID = %q, want alice", m.ID, m.UserID)
		}
	}
}

func TestLoadPersistedSessionsAttributesLegacyFilesToConfiguredUser(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Cleanup(func() { SetUserID("") })
	if err := os.MkdirAll(logsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	turns := `[{"user_text":"q","assistant_text":"a","at":"2026-01-01T00:00:00Z"}]`
	// Pre-multi-user file: no user_id key at all.
	legacy := `{"title":"old","turns":` + turns + `}`
	// A file already attributed to someone else must keep its owner.
	owned := `{"title":"hers","user_id":"carol","turns":` + turns + `}`
	if err := os.WriteFile(ConversationPath("legacy-fox"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConversationPath("owned-owl"), []byte(owned), 0o644); err != nil {
		t.Fatal(err)
	}
	SetUserID("bob")

	got := map[string]string{}
	for _, m := range LoadPersistedSessions() {
		got[m.ID] = m.UserID
	}
	if got["legacy-fox"] != "bob" {
		t.Errorf("legacy file: UserID = %q, want the configured user bob", got["legacy-fox"])
	}
	if got["owned-owl"] != "carol" {
		t.Errorf("owned file: UserID = %q, want carol (never re-attributed on load)", got["owned-owl"])
	}

	// A write stamps the configured user on the legacy file only.
	if err := AppendConversationTurn("legacy-fox", "q2", "a2"); err != nil {
		t.Fatal(err)
	}
	if err := AppendConversationTurn("owned-owl", "q2", "a2"); err != nil {
		t.Fatal(err)
	}
	var lf, of ConversationFile
	for id, dst := range map[string]*ConversationFile{"legacy-fox": &lf, "owned-owl": &of} {
		data, err := os.ReadFile(ConversationPath(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, dst); err != nil {
			t.Fatal(err)
		}
	}
	if lf.UserID != "bob" || of.UserID != "carol" {
		t.Fatalf("after write: legacy=%q (want bob) owned=%q (want carol)", lf.UserID, of.UserID)
	}
}
