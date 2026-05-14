package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestNewSymlinkDirFS_FollowsSymlinks(t *testing.T) {
	// target: a real directory with a SKILL.md
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// root: contains a symlink to the target directory
	root := t.TempDir()
	link := filepath.Join(root, "my-skill")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	fsys := NewSymlinkDirFS(root)

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].IsDir() {
		t.Error("symlink to directory should report IsDir()==true")
	}

	// Ensure the file inside is still readable.
	body, err := fs.ReadFile(fsys, "my-skill/SKILL.md")
	if err != nil {
		t.Fatal("cannot read file through symlink:", err)
	}
	if string(body) != "content" {
		t.Errorf("unexpected content: %q", body)
	}
}

func TestNewSymlinkDirFS_PlainDirStillWorks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plain-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	fsys := NewSymlinkDirFS(root)
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Error("plain directory should still report IsDir()==true")
	}
}
