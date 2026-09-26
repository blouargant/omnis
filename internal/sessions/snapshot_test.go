package sessions

import "testing"

func TestRegistrySnapshotIsACopy(t *testing.T) {
	r := NewEmptyRegistry()
	r.Add(&SessionMeta{ID: "s1", Turns: 2})
	snap, ok := r.Snapshot("s1")
	if !ok || snap.Turns != 2 {
		t.Fatalf("Snapshot = %+v, %v", snap, ok)
	}
	r.SetArchived("s1", true)
	if snap.Archived {
		t.Error("snapshot must not observe later writes")
	}
	if _, ok := r.Snapshot("nope"); ok {
		t.Error("unknown id must report ok=false")
	}
}
