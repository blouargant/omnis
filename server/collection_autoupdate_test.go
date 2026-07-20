package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
)

var errFakeDistill = errors.New("distill failed")

func TestShouldAutoUpdate(t *testing.T) {
	if !shouldAutoUpdate(true, true, 40*time.Minute, 30*time.Minute) {
		t.Fatal("all conditions met ⇒ should fire")
	}
	if shouldAutoUpdate(false, true, time.Hour, time.Minute) {
		t.Fatal("auto_update off ⇒ no")
	}
	if shouldAutoUpdate(true, false, time.Hour, time.Minute) {
		t.Fatal("content unchanged ⇒ no")
	}
	if shouldAutoUpdate(true, true, time.Minute, 30*time.Minute) {
		t.Fatal("within min-interval ⇒ no")
	}
}

func TestAutoUpdaterCommitsThenSkipsUnchanged(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sessions.AddCollection("Acme")
	sessions.SetCollectionProfileData("Acme", sessions.CollectionProfileData{AutoUpdate: true, MemorySize: "small"})
	collectionctx.WriteMemory("Acme", "old")

	calls := 0
	au := &autoUpdater{
		minInterval: 0, // isolate the content-hash gate for the second run
		gather:      func(string) string { return "## Session: s\nUser: hi\nAssistant: yo\n" },
		distill: func(_ context.Context, cur, mat string, wl int) (string, error) {
			calls++
			if wl != 200 {
				t.Fatalf("expected small=200 word limit, got %d", wl)
			}
			return "new facts", nil
		},
		inflight:    map[string]bool{},
		lastHash:    map[string]string{},
		lastAttempt: map[string]time.Time{},
	}
	au.runCollection(context.Background(), "Acme")
	if got := collectionctx.ReadMemory("Acme"); got != "new facts" {
		t.Fatalf("memory not committed: %q", got)
	}
	if collectionctx.ReadPrevMemory("Acme") != "old" {
		t.Fatal("prev snapshot missing")
	}
	if sessions.CollectionProfileFull("Acme").LastMemoryUpdate == 0 {
		t.Fatal("last_memory_update not set")
	}
	au.runCollection(context.Background(), "Acme") // same material ⇒ hash gate skips
	if calls != 1 {
		t.Fatalf("expected 1 distill call, got %d", calls)
	}
}

func TestAutoUpdaterOffIsNoop(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sessions.AddCollection("Acme") // no profile ⇒ auto_update false
	collectionctx.WriteMemory("Acme", "old")
	au := &autoUpdater{
		minInterval: 0,
		gather:      func(string) string { return "material" },
		distill: func(_ context.Context, _, _ string, _ int) (string, error) {
			t.Fatal("distill must not run")
			return "", nil
		},
		inflight:    map[string]bool{},
		lastHash:    map[string]string{},
		lastAttempt: map[string]time.Time{},
	}
	au.runCollection(context.Background(), "Acme")
	if collectionctx.ReadMemory("Acme") != "old" {
		t.Fatal("memory changed while auto_update off")
	}
}

func TestAutoUpdaterThrottlesFailedDistill(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	sessions.AddCollection("Acme")
	sessions.SetCollectionProfileData("Acme", sessions.CollectionProfileData{AutoUpdate: true, MemorySize: "small"})
	collectionctx.WriteMemory("Acme", "old")
	calls := 0
	au := &autoUpdater{
		minInterval: time.Hour, // a failure must not re-fire within the interval
		gather:      func(string) string { return "## Session: s\nUser: hi\nAssistant: yo\n" },
		distill:     func(_ context.Context, _, _ string, _ int) (string, error) { calls++; return "", errFakeDistill },
		inflight:    map[string]bool{},
		lastHash:    map[string]string{},
		lastAttempt: map[string]time.Time{},
	}
	au.runCollection(context.Background(), "Acme")
	au.runCollection(context.Background(), "Acme") // within the interval → must be gated out
	if calls != 1 {
		t.Fatalf("expected the failed distill to be throttled to 1 call, got %d", calls)
	}
	if collectionctx.ReadMemory("Acme") != "old" {
		t.Fatal("memory must be unchanged after a failed distill")
	}
}
