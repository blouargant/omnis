package main

import (
	"context"
	"testing"
	"time"

	fstools "github.com/blouargant/omnis/core/tools"
)

// After a restart, persisted sessions are re-watched at boot. A mailbox turn
// injected through that watcher must run with the owner's live platform token
// once the owner has logged back in — so the boot watcher must be handed the
// cookie-aware deps (it once got a hand-built serverDeps without Cookie).
func TestBootWatcherInjectsWithOwnersToken(t *testing.T) {
	_, d, aliceSID, _ := newCookieTestEngine(t)
	var onMessage func(from, body string)
	pm := newPushManager(d.RunGuard, d.PushEvents,
		func(_ context.Context, _, sid string, fn func(from, body string)) {
			if sid == aliceSID {
				onMessage = fn
			}
		}, nil, true)
	var gotDeps serverDeps
	var gotUser string
	pm.injectMailbox = func(_ context.Context, dd serverDeps, _, userID, _, _ string) {
		gotDeps, gotUser = dd, userID
	}
	d.PushMgr = pm

	watchPersistedSessions(t.Context(), d)
	if onMessage == nil {
		t.Fatal("alice's session was not watched")
	}
	d.Cookie.tokens.Put("alice", "alice-live-token", time.Time{})
	onMessage("peer", "hello")

	ctx := turnContext(context.Background(), gotDeps, aliceSID, gotUser)
	env := fstools.ShellEnvFrom(ctx)
	if len(env) != 1 || env[0] != "PLAT_TOKEN=alice-live-token" {
		t.Fatalf("injected turn env = %v, want alice's token", env)
	}
}
