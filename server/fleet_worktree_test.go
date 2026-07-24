package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newGitRepo makes a clean git repo with one commit (on a branch, not detached).
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	return dir
}

func TestFleetWorktreeDirCreatesAndReuses(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	repo := newGitRepo(t)
	t.Cleanup(func() { fleetWorktreeCleanup("expA") })

	p1, err := fleetWorktreeDir("expA", "Svc", repo)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p1 == repo || p1 == "" {
		t.Fatalf("worktree dir should differ from the repo, got %q", p1)
	}
	if fi, err := os.Stat(filepath.Join(p1, ".git")); err != nil || fi.IsDir() {
		// a worktree's .git is a FILE pointing at the gitdir
		t.Fatalf("expected a worktree .git file at %q", p1)
	}
	p2, err := fleetWorktreeDir("expA", "Svc", repo)
	if err != nil || p2 != p1 {
		t.Fatalf("second call must reuse the same worktree, got %q / %v", p2, err)
	}
}

func TestFleetWorktreeDirDirtyRepoErrors(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	repo := newGitRepo(t)
	_ = os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("y"), 0o644) // uncommitted
	t.Cleanup(func() { fleetWorktreeCleanup("expB") })
	if _, err := fleetWorktreeDir("expB", "Svc", repo); err == nil {
		t.Fatal("a dirty repo must fail worktree creation, not silently succeed")
	}
}
