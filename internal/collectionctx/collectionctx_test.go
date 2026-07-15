package collectionctx

import (
	"strings"
	"testing"
)

func TestResolveEmptyWhenNoProse(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if got := Resolve("Client X"); got != "" {
		t.Fatalf("no prose should resolve empty, got %q", got)
	}
	if HasContext("Client X") {
		t.Fatal("HasContext should be false with no prose")
	}
}

func TestWriteReadResolve(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := WriteInstructions("Client X", "Use a formal tone."); err != nil {
		t.Fatal(err)
	}
	if err := WriteMemory("Client X", "Client is on GCP."); err != nil {
		t.Fatal(err)
	}
	if got := ReadInstructions("Client X"); got != "Use a formal tone." {
		t.Fatalf("instructions: %q", got)
	}
	if !HasContext("Client X") {
		t.Fatal("HasContext = false")
	}
	block := Resolve("Client X")
	for _, want := range []string{`name="Client X"`, "<instructions>", "Use a formal tone.", "<memory>", "Client is on GCP.", "</collection-context>"} {
		if !strings.Contains(block, want) {
			t.Fatalf("block missing %q:\n%s", want, block)
		}
	}
}

func TestResolveInstructionsOnly(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := WriteInstructions("Thesis", "Cite sources."); err != nil {
		t.Fatal(err)
	}
	block := Resolve("Thesis")
	if !strings.Contains(block, "<instructions>") || strings.Contains(block, "<memory>") {
		t.Fatalf("expected instructions only:\n%s", block)
	}
}

func TestEmptyWriteRemovesFile(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := WriteInstructions("C", "text"); err != nil {
		t.Fatal(err)
	}
	if !HasContext("C") {
		t.Fatal("expected context after write")
	}
	if err := WriteInstructions("C", "   "); err != nil {
		t.Fatal(err)
	}
	if HasContext("C") {
		t.Fatal("blank write should remove the file")
	}
	if Resolve("C") != "" {
		t.Fatal("resolve should be empty after removal (cache must invalidate)")
	}
}

func TestUnsafeNamesRejected(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	for _, bad := range []string{"", ".", "..", "a/b", `a\b`} {
		if Dir(bad) != "" {
			t.Errorf("Dir(%q) should be empty", bad)
		}
		if Resolve(bad) != "" {
			t.Errorf("Resolve(%q) should be empty", bad)
		}
		if err := WriteInstructions(bad, "x"); err == nil {
			t.Errorf("WriteInstructions(%q) should error", bad)
		}
	}
}

func TestRenameDirMigratesAndNeverClobbers(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := WriteInstructions("Old", "keep me"); err != nil {
		t.Fatal(err)
	}
	if err := RenameDir("Old", "New"); err != nil {
		t.Fatal(err)
	}
	if HasContext("Old") {
		t.Fatal("old dir should be gone after rename")
	}
	if got := ReadInstructions("New"); got != "keep me" {
		t.Fatalf("prose did not migrate: %q", got)
	}
	// Rename onto an existing destination must not clobber it.
	if err := WriteInstructions("Src", "src"); err != nil {
		t.Fatal(err)
	}
	if err := WriteInstructions("New", "dest"); err != nil {
		t.Fatal(err)
	}
	if err := RenameDir("Src", "New"); err != nil {
		t.Fatal(err)
	}
	if got := ReadInstructions("New"); got != "dest" {
		t.Fatalf("rename should not clobber existing dest: %q", got)
	}
}

func TestRemoveDir(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if err := WriteInstructions("Gone", "bye"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDir("Gone"); err != nil {
		t.Fatal(err)
	}
	if HasContext("Gone") {
		t.Fatal("prose should be gone after RemoveDir")
	}
	// Removing a non-existent collection is a no-op.
	if err := RemoveDir("NeverExisted"); err != nil {
		t.Fatalf("removing missing dir should be a no-op: %v", err)
	}
}
