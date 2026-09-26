package main

import (
	"context"
	"testing"
	"time"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/teammates"
)

func leaderAddr(user, sid string) string { return toolkitagent.SessionSuffix(user, sid) + ":leader" }

func TestMailboxOwnerResolver(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	f := mailboxOwnerResolver(d.Registry)
	if got := f(leaderAddr("alice", aliceSID)); got != "alice" {
		t.Fatalf("alice's leader address → %q", got)
	}
	if got := f(toolkitagent.SessionSuffix("bob", bobSID) + ":investigator"); got != "bob" {
		t.Fatalf("bob's intra-session address → %q", got)
	}
	if got := f("nobody_x:leader"); got != "" {
		t.Fatalf("unknown address must resolve to \"\", got %q", got)
	}
}

// Belt and braces: even if a cross-owner message reached alice's inbox (e.g.
// written before scoping was on), the watcher must drop it rather than run a
// turn — which would execute with ALICE's platform token on bob's words.
func TestInjectDropsCrossOwnerMessage(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	teammates.SetOwnerResolver(mailboxOwnerResolver(d.Registry))
	t.Cleanup(func() { teammates.SetOwnerResolver(nil) })
	d.ListSessionRegistry = func() map[string]string {
		return map[string]string{"bob-chat": leaderAddr("bob", bobSID)}
	}
	pm := newPushManager(d.RunGuard, d.PushEvents, nil, nil, true)
	// Hold alice's run guard: a message allowed through parks on it until ctx
	// ends; a dropped one returns at once.
	release := d.RunGuard.acquire(aliceSID)
	defer release()

	run := func(from string) time.Duration {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		start := time.Now()
		pm.inject(ctx, d, aliceSID, "alice", from, "do something")
		return time.Since(start)
	}
	if el := run("bob-chat"); el > 200*time.Millisecond {
		t.Fatalf("bob's message was not dropped (inject waited %v for alice's session)", el)
	}
	if el := run(leaderAddr("bob", bobSID)); el > 200*time.Millisecond {
		t.Fatalf("bob's raw-address message was not dropped (waited %v)", el)
	}
	if el := run("unknown-peer"); el > 200*time.Millisecond {
		t.Fatalf("an unattributable sender must be dropped too (waited %v)", el)
	}
	// Control: alice's own session is delivered (so it waits on the guard).
	if el := run(leaderAddr("alice", aliceSID)); el < 300*time.Millisecond {
		t.Fatalf("same-owner message must be delivered, returned after %v", el)
	}
}

// Cookie mode installs both process-wide owner resolvers; removal restores
// the unscoped single-user behaviour.
func TestInstallOwnerScoping(t *testing.T) {
	_, d, aliceSID, _ := newCookieTestEngine(t)
	undo := installOwnerScoping(d.Registry)
	if o, on := teammates.AddressOwner(leaderAddr("alice", aliceSID)); !on || o != "alice" {
		t.Fatalf("teammates resolver: %q %v", o, on)
	}
	undo()
	if _, on := teammates.AddressOwner(leaderAddr("alice", aliceSID)); on {
		t.Fatal("resolver must be removed")
	}
}
