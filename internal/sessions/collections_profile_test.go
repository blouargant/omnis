package sessions

import (
	"sync"
	"testing"

	"github.com/blouargant/omnis/internal/collectionctx"
)

func TestCollectionProfileCRUD(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Client X"); err != nil {
		t.Fatal(err)
	}
	// No profile yet.
	if s, c := CollectionProfile("Client X"); s != "" || c != "" {
		t.Fatalf("empty profile expected, got %q %q", s, c)
	}
	if err := SetCollectionProfile("Client X", "Kubernetes", "/tmp"); err != nil {
		t.Fatal(err)
	}
	// Case-insensitive lookup returns the stored scalars.
	if s, c := CollectionProfile("client x"); s != "Kubernetes" || c != "/tmp" {
		t.Fatalf("got %q %q", s, c)
	}
	// Clearing both removes the profile.
	if err := SetCollectionProfile("Client X", "", ""); err != nil {
		t.Fatal(err)
	}
	if s, c := CollectionProfile("Client X"); s != "" || c != "" {
		t.Fatalf("profile not cleared: %q %q", s, c)
	}
	// Unknown collection is an error.
	if err := SetCollectionProfile("Ghost", "x", ""); err == nil {
		t.Fatal("expected error for unknown collection")
	}
}

func TestRenameCascadesProfileAndProse(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Old"); err != nil {
		t.Fatal(err)
	}
	if err := SetCollectionProfile("Old", "Coding", "/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := collectionctx.WriteInstructions("Old", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := RenameCollection("Old", "New"); err != nil || !ok {
		t.Fatalf("rename: ok=%v err=%v", ok, err)
	}
	if s, c := CollectionProfile("New"); s != "Coding" || c != "/tmp" {
		t.Fatalf("profile did not migrate: %q %q", s, c)
	}
	if s, _ := CollectionProfile("Old"); s != "" {
		t.Fatalf("old profile lingers: %q", s)
	}
	if got := collectionctx.ReadInstructions("New"); got != "hello" {
		t.Fatalf("prose did not migrate: %q", got)
	}
	if collectionctx.HasContext("Old") {
		t.Fatal("old prose dir lingers after rename")
	}
}

func TestRemoveCascadesProfileAndProse(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Doomed"); err != nil {
		t.Fatal(err)
	}
	if err := SetCollectionProfile("Doomed", "Coding", ""); err != nil {
		t.Fatal(err)
	}
	if err := collectionctx.WriteInstructions("Doomed", "bye"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := RemoveCollection("Doomed"); err != nil || !ok {
		t.Fatalf("remove: ok=%v err=%v", ok, err)
	}
	if s, _ := CollectionProfile("Doomed"); s != "" {
		t.Fatalf("profile lingers after remove: %q", s)
	}
	if collectionctx.HasContext("Doomed") {
		t.Fatal("prose lingers after remove")
	}
}

func TestUpdateCollectionProfilePreservesOtherField(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	if err := SetCollectionProfile("Acme", "Coding", "/tmp"); err != nil {
		t.Fatal(err)
	}
	// A squad-only update (the PATCH path) must NOT wipe the cwd.
	if err := UpdateCollectionProfile("Acme", func(p *CollectionProfileData) { p.Squad = "Kubernetes" }); err != nil {
		t.Fatal(err)
	}
	if p := CollectionProfileFull("Acme"); p.Squad != "Kubernetes" || p.Cwd != "/tmp" {
		t.Fatalf("profile = %+v, want Kubernetes /tmp", p)
	}
}

func TestCollectionProfileConcurrentFieldUpdates(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = UpdateCollectionProfile("Acme", func(p *CollectionProfileData) { p.Squad = "Coding" })
		}()
		go func() {
			defer wg.Done()
			_ = UpdateCollectionProfile("Acme", func(p *CollectionProfileData) { p.Cwd = "/tmp" })
		}()
	}
	wg.Wait()
	// Atomic per-field merges ⇒ the final state keeps BOTH fields.
	if p := CollectionProfileFull("Acme"); p.Squad != "Coding" || p.Cwd != "/tmp" {
		t.Fatalf("field lost under concurrency: %+v", p)
	}
}
