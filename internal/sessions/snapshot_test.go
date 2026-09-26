package sessions

import "testing"

func TestRegistrySnapshotIsACopy(t *testing.T) {
	r := NewEmptyRegistry()
	r.Add(&SessionMeta{ID: "s1", Turns: 2})
	snap, ok := r.Snapshot("s1")
	if !ok || snap.Turns != 2 {
		t.Fatalf("Snapshot = %+v, %v", snap, ok)
	}
	// Mutate the live item directly (same package, so this is legal) rather
	// than via SetArchived: that setter persists asynchronously in a
	// goroutine this test has no way to wait for, and this test sets no
	// OMNIS_HOME override at all, so a slow-to-run goroutine would write a
	// stray conversation_*.json.tmp straight into the real $HOME/.omnis. A
	// direct in-memory mutation exercises the exact same "does Snapshot
	// observe a later write to the live item" property with no such
	// side effect.
	r.mu.Lock()
	r.items["s1"].Archived = true
	r.mu.Unlock()
	if snap.Archived {
		t.Error("snapshot must not observe later writes")
	}
	if _, ok := r.Snapshot("nope"); ok {
		t.Error("unknown id must report ok=false")
	}
}
