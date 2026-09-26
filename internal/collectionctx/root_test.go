package collectionctx

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreIsRootScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	a := In(filepath.Join(home, "users", "alice"))
	b := In(filepath.Join(home, "users", "bob"))
	if err := a.WriteInstructions("Infra", "alice rules"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.Resolve("Infra"), "alice rules") {
		t.Fatal("bob resolved alice's collection context")
	}
	if !strings.Contains(a.Resolve("Infra"), "alice rules") {
		t.Fatal("alice lost her context")
	}
	if strings.Contains(Resolve("Infra"), "alice rules") {
		t.Fatal("shared root must be untouched")
	}
}
