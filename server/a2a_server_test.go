package main

import (
	"strings"
	"testing"
	"time"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

// stubA2AServer builds an a2aServer wired to a real in-memory registry but
// with no manager (squad validation is skipped when manager is nil).
func stubA2AServer(t *testing.T) *a2aServer {
	t.Helper()
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{
		ID:         "teaching-kite",
		UserID:     sessions.DefaultUserID,
		Squad:      "research",
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	})
	reg.Add(&sessions.SessionMeta{
		ID:         "plain-fox",
		UserID:     sessions.DefaultUserID,
		Squad:      "", // unset → server treats as default
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	})
	return newA2AServer(a2aDeps{Registry: reg}, "")
}

func TestResolveRouting_EphemeralWhenNoSessionName(t *testing.T) {
	s := stubA2AServer(t)
	got, err := s.resolveRouting(nil, "task-123")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if got.Persistent {
		t.Fatal("expected ephemeral routing")
	}
	if got.SessionID != "task-123" {
		t.Fatalf("SessionID: got %q, want task-123", got.SessionID)
	}
	if got.UserID != sessions.DefaultUserID {
		t.Fatalf("UserID: got %q, want %q", got.UserID, sessions.DefaultUserID)
	}
	if got.Squad != toolkitagent.DefaultSquadName {
		t.Fatalf("Squad: got %q, want %q", got.Squad, toolkitagent.DefaultSquadName)
	}
}

func TestResolveRouting_SquadOnlyHonored(t *testing.T) {
	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{"squad": "research"}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if got.Squad != "research" || got.Persistent {
		t.Fatalf("got %+v, want squad=research persistent=false", got)
	}
}

func TestResolveRouting_KnownSessionUsesRegistryMeta(t *testing.T) {
	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{"session_name": "teaching-kite"}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if !got.Persistent {
		t.Fatal("expected persistent routing for known session")
	}
	if got.SessionID != "teaching-kite" {
		t.Fatalf("SessionID: got %q", got.SessionID)
	}
	if got.UserID != sessions.DefaultUserID {
		t.Fatalf("UserID: got %q", got.UserID)
	}
	if got.Squad != "research" {
		t.Fatalf("Squad: got %q, want research (from registry)", got.Squad)
	}
	if got.Meta == nil || got.Meta.ID != "teaching-kite" {
		t.Fatal("Meta should reference the registry entry")
	}
}

func TestResolveRouting_UnknownSessionRejected(t *testing.T) {
	s := stubA2AServer(t)
	_, err := s.resolveRouting(map[string]any{"session_name": "missing-mouse"}, "task-1")
	if err == nil {
		t.Fatal("expected error for unknown session")
	}
	if !strings.Contains(err.Error(), "unknown session") {
		t.Fatalf("error message: %v", err)
	}
}

func TestResolveRouting_SquadConflictRejected(t *testing.T) {
	s := stubA2AServer(t)
	// teaching-kite is pinned to research; asking for default is a conflict.
	_, err := s.resolveRouting(map[string]any{
		"session_name": "teaching-kite",
		"squad":        "default",
	}, "task-1")
	if err == nil {
		t.Fatal("expected error for squad/session conflict")
	}
	if !strings.Contains(err.Error(), "pinned to squad") {
		t.Fatalf("error should mention pin; got %v", err)
	}
}

func TestResolveRouting_SquadMatchingPinAccepted(t *testing.T) {
	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{
		"session_name": "teaching-kite",
		"squad":        "RESEARCH", // case-insensitive match
	}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if got.Squad != "research" {
		t.Fatalf("Squad: got %q, want research", got.Squad)
	}
}

func TestResolveRouting_SessionWithoutSquadDefaults(t *testing.T) {
	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{"session_name": "plain-fox"}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if got.Squad != toolkitagent.DefaultSquadName {
		t.Fatalf("Squad: got %q, want %q", got.Squad, toolkitagent.DefaultSquadName)
	}
}

func TestResolveRouting_NoRegistryRejectsSessionName(t *testing.T) {
	s := newA2AServer(a2aDeps{}, "")
	_, err := s.resolveRouting(map[string]any{"session_name": "anything"}, "task-1")
	if err == nil {
		t.Fatal("expected error when registry is unavailable")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("error message: %v", err)
	}
}

func TestResolveRouting_AutoCreateOnMissing(t *testing.T) {
	// Persistence helpers below touch $OMNIS_HOME — point it at a temp dir.
	t.Setenv("OMNIS_HOME", t.TempDir())

	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{
		"session_name": "fresh-otter",
		"create":       true,
	}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if !got.Persistent {
		t.Fatal("auto-created session should be persistent")
	}
	if got.SessionID != "fresh-otter" {
		t.Fatalf("SessionID: got %q", got.SessionID)
	}
	if got.Squad != toolkitagent.DefaultSquadName {
		t.Fatalf("Squad: got %q, want default", got.Squad)
	}
	// Verify the registry now contains it.
	if _, ok := s.deps.Registry.Get("fresh-otter"); !ok {
		t.Fatal("registry should contain auto-created session")
	}
}

func TestResolveRouting_AutoCreateWithSquad(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{
		"session_name": "smart-mouse",
		"create":       true,
		"squad":        "research",
	}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if got.Squad != "research" {
		t.Fatalf("Squad: got %q, want research", got.Squad)
	}
}

func TestResolveRouting_AutoCreateRejectsInvalidName(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	s := stubA2AServer(t)
	_, err := s.resolveRouting(map[string]any{
		"session_name": "Has Spaces And UPPER",
		"create":       true,
	}, "task-1")
	if err == nil {
		t.Fatal("expected error for invalid name")
	}
	if !strings.Contains(err.Error(), "invalid session name") {
		t.Fatalf("error: %v", err)
	}
}

func TestResolveRouting_AutoCreateAcceptsExisting(t *testing.T) {
	// "create" against a name that already exists is a no-op (idempotent):
	// returns the existing session rather than failing.
	s := stubA2AServer(t)
	got, err := s.resolveRouting(map[string]any{
		"session_name": "teaching-kite",
		"create":       true,
	}, "task-1")
	if err != nil {
		t.Fatalf("resolveRouting: %v", err)
	}
	if got.SessionID != "teaching-kite" {
		t.Fatalf("SessionID: got %q", got.SessionID)
	}
}

func TestPersistA2ATurn_PushesSSE(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	bcast := newSessionPushBroadcaster()
	reg := sessions.NewEmptyRegistry()
	reg.Add(&sessions.SessionMeta{ID: "watcher-bird", UserID: sessions.DefaultUserID, CreatedAt: time.Now()})
	s := newA2AServer(a2aDeps{Registry: reg, PushEvents: bcast}, "")

	ch := bcast.subscribe("watcher-bird")
	defer bcast.unsubscribe("watcher-bird", ch)

	s.persistA2ATurn(&sessionRouting{SessionID: "watcher-bird", Persistent: true}, "p", "r")

	select {
	case <-ch:
		// Got the push — that's the contract.
	case <-time.After(1 * time.Second):
		t.Fatal("expected SSE push notification within 1s")
	}
}

func TestValidSessionName(t *testing.T) {
	cases := map[string]bool{
		"teaching-kite":         true,
		"plain-fox-1":           true,
		"abc":                   true,
		"":                      false,
		"WithUpper":             false,
		"with space":            false,
		"with/slash":            false,
		"with_underscore":       false, // underscore is intentionally excluded
		strings.Repeat("a", 81): false,
	}
	for name, want := range cases {
		if got := sessions.ValidName(name); got != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, got, want)
		}
	}
}
