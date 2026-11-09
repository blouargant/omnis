package collmem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMigrateLegacy(t *testing.T) {
	root := t.TempDir()
	s := In(root)
	dir := filepath.Join(root, "collections", "Proxy")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "memory.md"), []byte("Old notes."), 0o644)
	os.WriteFile(filepath.Join(dir, "memory.prev.md"), []byte("Older."), 0o644)
	os.WriteFile(filepath.Join(dir, "instructions.md"), []byte("Keep."), 0o644)
	done, err := s.MigrateLegacy("Proxy")
	if err != nil || !done {
		t.Fatal(done, err)
	}
	m, ok := s.Get("Proxy", "imported-notes")
	if !ok || m.Type != TypeProject || m.Body != "Old notes." || m.Description != "Notes carried over from the previous collection memory" {
		t.Fatalf("%+v", m)
	}
	for _, gone := range []string{"memory.md", "memory.prev.md"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s must be removed", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "instructions.md")); err != nil {
		t.Error("instructions must be untouched")
	}
	if again, _ := s.MigrateLegacy("Proxy"); again {
		t.Error("second call must be a no-op")
	}
}

func TestMigrateLegacyLongTextIsSplit(t *testing.T) {
	root := t.TempDir()
	s := In(root)
	dir := filepath.Join(root, "collections", "Proxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var paras []string
	for i := 0; i < 30; i++ {
		p := fmt.Sprintf("p%02d ", i)
		paras = append(paras, p+strings.Repeat("y", 300-len(p)))
	}
	text := strings.Join(paras, "\n\n")
	if len(text) < 9000 {
		t.Fatalf("fixture is %d chars", len(text))
	}
	if err := os.WriteFile(filepath.Join(dir, "memory.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	done, err := s.MigrateLegacy("Proxy")
	if err != nil || !done {
		t.Fatal(done, err)
	}
	var bodies []string
	for _, name := range []string{"imported-notes", "imported-notes-2", "imported-notes-3"} {
		m, ok := s.Get("Proxy", name)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if n := utf8.RuneCountInString(m.Body); n > MaxBodyChars {
			t.Errorf("%s body is %d chars", name, n)
		}
		bodies = append(bodies, m.Body)
	}
	if n := len(s.List("Proxy")); n != 3 {
		t.Errorf("got %d memories, want 3", n)
	}
	if strings.Join(bodies, "\n\n") != text {
		t.Error("joined bodies must equal the original text")
	}
	if _, err := os.Stat(filepath.Join(dir, "memory.md")); !os.IsNotExist(err) {
		t.Error("memory.md must be removed")
	}
}

func TestMigrateLegacyNoopForGeneralAndWhenMemoryDirExists(t *testing.T) {
	root := t.TempDir()
	s := In(root)

	if done, err := s.MigrateLegacy(""); done || err != nil {
		t.Errorf("General: got %v, %v", done, err)
	}

	dir := filepath.Join(root, "collections", "Proxy")
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "memory.md")
	if err := os.WriteFile(legacy, []byte("Old notes."), 0o644); err != nil {
		t.Fatal(err)
	}
	if done, err := s.MigrateLegacy("Proxy"); done || err != nil {
		t.Errorf("existing memory dir: got %v, %v", done, err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Error("memory.md must be left alone when the memory directory exists")
	}
	if len(s.List("Proxy")) != 0 {
		t.Error("nothing must be imported")
	}
}

// EnsureMigrated is what every reader and writer calls first: repeated calls
// import the legacy notes once, and a Save afterwards (which the first route
// write would be) keeps them.
func TestEnsureMigratedIsIdempotentAndSurvivesSave(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "collections", "Proxy")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "memory.md"), []byte("Old notes."), 0o644)
	s := In(root)
	s.EnsureMigrated("Proxy")
	s.EnsureMigrated("Proxy")
	// A second Store on the same root shares the lock and sees the import.
	In(root).EnsureMigrated("Proxy")
	if mems := s.List("Proxy"); len(mems) != 1 || mems[0].Name != "imported-notes" {
		t.Fatalf("after EnsureMigrated: %+v", mems)
	}
	if _, _, err := s.Save("Proxy", Memory{Name: "rate-limit", Description: "d", Type: TypeProject, Body: "b"}); err != nil {
		t.Fatal(err)
	}
	s.EnsureMigrated("Proxy")
	m, ok := s.Get("Proxy", "imported-notes")
	if !ok || m.Body != "Old notes." {
		t.Fatalf("imported notes lost after Save: %+v %v", m, ok)
	}
	if n := len(s.List("Proxy")); n != 2 {
		t.Fatalf("want 2 memories, got %d", n)
	}
	// General ("") never migrates and never panics.
	s.EnsureMigrated("")
}
