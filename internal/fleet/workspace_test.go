package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsGitRepo(t *testing.T) {
	dir := t.TempDir()
	if IsGitRepo(dir) {
		t.Fatal("plain dir should not be a git repo")
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !IsGitRepo(dir) {
		t.Fatal("dir with .git should be a git repo")
	}
}

func TestValidateWorkspacesReportsMissingAndNonGit(t *testing.T) {
	repo := t.TempDir()
	_ = os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	projects := []Project{
		{Name: "ok", Cwd: repo, Engine: EngineOmnis},
		{Name: "missing", Cwd: filepath.Join(repo, "does-not-exist"), Engine: EngineOmnis},
		{Name: "nogit", Cwd: t.TempDir(), Engine: EngineOmnis},
	}
	err := ValidateWorkspaces(projects)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing") || !strings.Contains(msg, "nogit") {
		t.Fatalf("expected both bad projects reported, got %q", msg)
	}
	if strings.Contains(msg, `"ok"`) {
		t.Fatalf("valid project should not be reported: %q", msg)
	}
}
