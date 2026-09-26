package teammates

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scopedAgent builds an Agent whose registry holds alice's and bob's sessions
// and installs an owner resolver keyed on the "<user>_" address prefix.
func scopedAgent(t *testing.T) (*Agent, Backend) {
	t.Helper()
	dir := t.TempDir()
	b, err := NewJSONLBackend(filepath.Join(dir, "mb"))
	if err != nil {
		t.Fatal(err)
	}
	reg := NewSessionRegistry(dir)
	_ = reg.Register("alice-chat", "alice_s1:leader")
	_ = reg.Register("alice-other", "alice_s2:leader")
	_ = reg.Register("bob-chat", "bob_s3:leader")
	a := NewAgent("leader", b)
	a.Registry = reg
	SetOwnerResolver(func(addr string) string {
		if i := strings.Index(addr, "_"); i > 0 {
			return addr[:i]
		}
		return ""
	})
	t.Cleanup(func() { SetOwnerResolver(nil) })
	return a, b
}

func TestListIsScopedToCallersOwner(t *testing.T) {
	a, _ := scopedAgent(t)
	got := a.visibleSessions("bob_s3:leader")
	if len(got) != 1 || got["bob-chat"] == "" {
		t.Fatalf("bob must only see his own sessions, got %v", got)
	}
	got = a.visibleSessions("alice_s1:leader")
	if len(got) != 2 || got["bob-chat"] != "" {
		t.Fatalf("alice must see her two sessions only, got %v", got)
	}
}

func TestCrossOwnerSendIsRefused(t *testing.T) {
	a, b := scopedAgent(t)
	err := a.tellAs(context.Background(), "bob_s3:leader", "alice_s1:leader", "run this as alice")
	if err == nil {
		t.Fatal("bob must not be able to message alice's session")
	}
	if m, _ := b.Receive(context.Background(), "alice_s1:leader", 100*time.Millisecond); m != nil {
		t.Fatalf("message was delivered anyway: %+v", m)
	}
	if _, err := a.askAs(context.Background(), "bob_s3:leader", "alice_s1:leader", "q", 100*time.Millisecond); err == nil {
		t.Fatal("teammate_ask across owners must be refused too")
	}
	// Same owner still works.
	if err := a.tellAs(context.Background(), "alice_s2:leader", "alice_s1:leader", "hi"); err != nil {
		t.Fatalf("same-owner send refused: %v", err)
	}
}

// No resolver (single-user mode): everything is visible and deliverable.
func TestNoResolverMeansNoScoping(t *testing.T) {
	a, _ := scopedAgent(t)
	SetOwnerResolver(nil)
	if got := a.visibleSessions("bob_s3:leader"); len(got) != 3 {
		t.Fatalf("unscoped list = %v", got)
	}
	if err := a.tellAs(context.Background(), "bob_s3:leader", "alice_s1:leader", "hi"); err != nil {
		t.Fatalf("unscoped send refused: %v", err)
	}
}
