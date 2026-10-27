package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestGitStatusNotARepo(t *testing.T) {
	gitOrSkip(t)
	st, err := gitStatus(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if st.Repo || len(st.Files) != 0 {
		t.Fatalf("status = %+v, want not a repo", st)
	}
}

func TestGitStatusCountsChanges(t *testing.T) {
	gitOrSkip(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "init")
	// +2 −1 on a tracked file, plus an untracked file of 4 lines.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\nTWO\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.md"), []byte("1\n2\n3\n4\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := gitStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Repo || st.Branch != "main" {
		t.Fatalf("repo/branch = %v %q", st.Repo, st.Branch)
	}
	files := map[string]GitFile{}
	for _, f := range st.Files {
		files[f.Path] = f
	}
	if f := files["a.txt"]; f.Added != 2 || f.Removed != 1 || f.Status != "M" {
		t.Fatalf("a.txt = %+v", f)
	}
	if f := files["new.md"]; f.Added != 4 || f.Status != "?" {
		t.Fatalf("new.md = %+v", f)
	}
	if st.Added != 6 || st.Removed != 1 {
		t.Fatalf("totals = +%d −%d", st.Added, st.Removed)
	}
}

func TestGitStatusNoCommitYet(t *testing.T) {
	gitOrSkip(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := gitStatus(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Repo || len(st.Files) != 1 || st.Files[0].Status != "?" {
		t.Fatalf("status = %+v", st)
	}
}
