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

func TestCollectionProfilePreservesFields(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	if err := SetCollectionProfileData("Acme", CollectionProfileData{
		Squad: "Coding", MemorySize: "large", AutoUpdate: true, LastMemoryUpdate: 123,
	}); err != nil {
		t.Fatal(err)
	}
	// A squad/cwd-only edit (the PATCH path) must NOT wipe memory_size/auto_update.
	if err := SetCollectionProfile("Acme", "Kubernetes", "/tmp"); err != nil {
		t.Fatal(err)
	}
	p := CollectionProfileFull("Acme")
	if p.Squad != "Kubernetes" || p.Cwd != "/tmp" {
		t.Fatalf("squad/cwd not updated: %+v", p)
	}
	if p.MemorySize != "large" || !p.AutoUpdate || p.LastMemoryUpdate != 123 {
		t.Fatalf("size/auto_update/last clobbered: %+v", p)
	}
	// The legacy two-value accessor still works.
	if s, c := CollectionProfile("Acme"); s != "Kubernetes" || c != "/tmp" {
		t.Fatalf("CollectionProfile = %q,%q", s, c)
	}
	// SetCollectionMemoryUpdate updates only the timestamp.
	if err := SetCollectionMemoryUpdate("Acme", 0); err != nil {
		t.Fatal(err)
	}
	if CollectionProfileFull("Acme").LastMemoryUpdate != 0 {
		t.Fatal("last_memory_update not cleared")
	}
	if !ValidMemorySize("") || !ValidMemorySize("small") || ValidMemorySize("huge") {
		t.Fatal("ValidMemorySize wrong")
	}
}

func TestCollectionProfileConcurrentFieldUpdates(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Acme"); err != nil {
		t.Fatal(err)
	}
	if err := SetCollectionProfileData("Acme", CollectionProfileData{Squad: "seed"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = SetCollectionProfile("Acme", "Coding", "") }()
		go func() { defer wg.Done(); _ = SetCollectionMemoryUpdate("Acme", 777) }()
	}
	wg.Wait()
	// Atomic per-field merges ⇒ the final state keeps BOTH fields; neither the
	// squad write nor the timestamp write can clobber the other.
	p := CollectionProfileFull("Acme")
	if p.Squad != "Coding" {
		t.Fatalf("squad lost under concurrency: %+v", p)
	}
	if p.LastMemoryUpdate != 777 {
		t.Fatalf("last_memory_update lost under concurrency: %+v", p)
	}
}
