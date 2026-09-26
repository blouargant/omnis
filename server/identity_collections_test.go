package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/collectionctx"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestCollectionsArePerUser(t *testing.T) {
	r, _, _, _ := newCookieTestEngine(t)
	if w := doWithBody(r, "ta", "POST", "/api/collections", `{"name":"Infra"}`); w.Code/100 != 2 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if body := doAs(r, "tb", "GET", "/api/collections").Body.String(); strings.Contains(body, "Infra") {
		t.Fatalf("bob sees alice's collection: %s", body)
	}
	if w := doAs(r, "tb", "GET", "/api/collections/Infra/context"); w.Code == 200 && strings.Contains(w.Body.String(), "Infra") {
		// bob addressing "Infra" must hit HIS (non-existent) collection, not alice's
		t.Fatalf("bob reached alice's collection context: %s", w.Body)
	}
	if names, _ := sessions.ListCollections(); len(names) != 0 {
		t.Fatalf("cookie-mode create leaked into the shared root: %v", names)
	}
	if names, _ := sessions.CollectionsIn(userRoot("alice")).ListCollections(); len(names) != 1 {
		t.Fatalf("alice's store: %v", names)
	}
}

// A rename/delete by one user must only re-file THAT user's sessions, even when
// another user has a same-named collection with sessions in it.
func TestCollectionCascadeIsPerUser(t *testing.T) {
	r, d, aliceSID, bobSID := newCookieTestEngine(t)
	for _, tok := range []string{"ta", "tb"} {
		if w := doWithBody(r, tok, "POST", "/api/collections", `{"name":"Infra"}`); w.Code/100 != 2 {
			t.Fatalf("create as %s: %d %s", tok, w.Code, w.Body)
		}
	}
	// File directly on the live meta (avoids the async persist goroutine).
	a, _ := d.Registry.Get(aliceSID)
	b, _ := d.Registry.Get(bobSID)
	a.Collection, b.Collection = "Infra", "Infra"

	if w := doWithBody(r, "ta", "PATCH", "/api/collections/Infra", `{"name":"Ops"}`); w.Code != 200 {
		t.Fatalf("rename: %d %s", w.Code, w.Body)
	}
	if m, _ := d.Registry.Snapshot(bobSID); m.Collection != "Infra" {
		t.Fatalf("alice's rename re-filed bob's session to %q", m.Collection)
	}
	if m, _ := d.Registry.Snapshot(aliceSID); m.Collection != "Ops" {
		t.Fatalf("alice's session not re-filed: %q", m.Collection)
	}
	if w := doAs(r, "tb", "DELETE", "/api/collections/Infra"); w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if m, _ := d.Registry.Snapshot(aliceSID); m.Collection != "Ops" {
		t.Fatalf("bob's delete touched alice's session: %q", m.Collection)
	}
	if m, _ := d.Registry.Snapshot(bobSID); m.Collection != "" {
		t.Fatalf("bob's session not returned to General: %q", m.Collection)
	}
	time.Sleep(50 * time.Millisecond) // let async persist goroutines settle before TempDir cleanup
}

// The auto-update worker must read only the owner's sessions and write to the
// owner's collection store.
func TestAutoUpdaterIsPerUser(t *testing.T) {
	_, d, aliceSID, bobSID := newCookieTestEngine(t)
	alice := sessions.CollectionsIn(userRoot("alice"))
	bob := sessions.CollectionsIn(userRoot("bob"))
	for _, c := range []*sessions.Collections{alice, bob} {
		if _, _, err := c.AddCollection("Infra"); err != nil {
			t.Fatal(err)
		}
		if err := c.SetCollectionProfileData("Infra", sessions.CollectionProfileData{AutoUpdate: true}); err != nil {
			t.Fatal(err)
		}
	}
	for sid, text := range map[string]string{aliceSID: "alice-fact", bobSID: "bob-secret"} {
		m, _ := d.Registry.Get(sid)
		m.Collection, m.Turns = "Infra", 1
		if err := sessions.AppendConversationTurn(sid, "q", text); err != nil {
			t.Fatal(err)
		}
	}
	au := newAutoUpdater(d, 0)
	var seen string
	au.distill = func(_ context.Context, _, material string, _ int) (string, error) {
		seen = material
		return "distilled", nil
	}
	au.runCollection(context.Background(), "Infra", "alice")
	if !strings.Contains(seen, "alice-fact") || strings.Contains(seen, "bob-secret") {
		t.Fatalf("material not owner-scoped: %q", seen)
	}
	if got := ctxStoreFor("alice").ReadMemory("Infra"); got != "distilled" {
		t.Fatalf("alice's memory not written: %q", got)
	}
	if got := ctxStoreFor("bob").ReadMemory("Infra"); got != "" {
		t.Fatalf("bob's memory written: %q", got)
	}
	if got := collectionctx.ReadMemory("Infra"); got != "" {
		t.Fatalf("shared memory written: %q", got)
	}
	if alice.CollectionProfileFull("Infra").LastMemoryUpdate == 0 {
		t.Fatal("alice's last_memory_update not set")
	}
}
