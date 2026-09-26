package sessions

import (
	"path/filepath"
	"testing"
)

func TestCollectionsAreRootScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	a := CollectionsIn(filepath.Join(home, "users", "alice"))
	b := CollectionsIn(filepath.Join(home, "users", "bob"))
	if _, _, err := a.AddCollection("Infra"); err != nil {
		t.Fatal(err)
	}
	if names, _ := b.ListCollections(); len(names) != 0 {
		t.Fatalf("bob sees alice's collections: %v", names)
	}
	if names, _ := ListCollections(); len(names) != 0 {
		t.Fatalf("shared root must be untouched: %v", names)
	}
	if names, _ := a.ListCollections(); len(names) != 1 || names[0] != "Infra" {
		t.Fatalf("alice: %v", names)
	}
}
