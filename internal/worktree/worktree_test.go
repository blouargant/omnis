package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightRejectsNonRepo(t *testing.T) {
	t.Parallel()

	err := Preflight(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "not a git repo") {
		t.Fatalf("Preflight() error = %v, want not a git repo", err)
	}
}

func TestCreateAndRemoveWorktree(t *testing.T) {
	repo := initGitRepo(t)
	worktreePath := filepath.Join(t.TempDir(), "wt")

	wt, err := Create(repo, worktreePath, "feature/test", "HEAD")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if wt.Branch != "feature/test" {
		t.Fatalf("Branch = %q, want feature/test", wt.Branch)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree path missing: %v", err)
	}

	if err := Remove(repo, wt.Path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after Remove(): %v", err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	mustGit(t, repo, "init", "-b", "main")
	mustGit(t, repo, "config", "user.email", "tests@example.com")
	mustGit(t, repo, "config", "user.name", "Tests")
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	mustGit(t, repo, "add", "README.md")
	mustGit(t, repo, "commit", "-m", "initial")
	return repo
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		t.Fatalf("git %v error = %v, output = %s", args, err, out)
	}
	return out
}
