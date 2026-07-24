package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/worktree"
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

// TestFleetWorktreeCleanupRemovesCleanKeepsDirty locks the teardown contract:
// a clean experiment worktree is removed, a dirty one is kept (and logged) so
// the user never silently loses uncommitted experiment work.
func TestFleetWorktreeCleanupRemovesCleanKeepsDirty(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	repo := newGitRepo(t)

	// Clean worktree ⇒ removed.
	clean, err := fleetWorktreeDir("expClean", "Svc", repo)
	if err != nil {
		t.Fatal(err)
	}
	fleetWorktreeCleanup("expClean")
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Fatalf("clean worktree should be removed, still at %q (err=%v)", clean, err)
	}

	// Dirty worktree ⇒ kept.
	dirty, err := fleetWorktreeDir("expDirty", "Svc", repo)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dirty, "wip.txt"), []byte("z"), 0o644) // uncommitted
	fleetWorktreeCleanup("expDirty")
	if _, err := os.Stat(dirty); err != nil {
		t.Fatalf("dirty worktree should be KEPT, but it's gone: %v", err)
	}
	// manual cleanup so the test dir is removable
	_ = worktree.Remove(repo, dirty)
}

// TestForgetSessionStateCleansFleetWorktree verifies the teardown wiring
// end-to-end: forgetSessionState (the shared helper called by both
// deleteSession and the archive handler) must itself invoke
// fleetWorktreeCleanup, not just leave it available. A zero-value serverDeps
// is safe here — every field forgetSessionState dereferences is nil-checked
// except claudecode.ForgetSession, which is a plain id-keyed map op.
func TestForgetSessionStateCleansFleetWorktree(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	repo := newGitRepo(t)

	clean, err := fleetWorktreeDir("expTeardown", "Svc", repo)
	if err != nil {
		t.Fatal(err)
	}
	forgetSessionState(serverDeps{}, "expTeardown")
	if _, err := os.Stat(clean); !os.IsNotExist(err) {
		t.Fatalf("forgetSessionState should clean up the experiment worktree, still at %q (err=%v)", clean, err)
	}
}
