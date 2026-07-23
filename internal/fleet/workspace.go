package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsGitRepo reports whether dir contains a .git entry (dir or file — the latter
// covers git worktrees, whose .git is a file pointing at the real gitdir).
func IsGitRepo(dir string) bool {
	if dir == "" {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	return false
}

// ValidateWorkspaces checks each project's Cwd exists, is a directory, and is a
// git repository (required for the fork/worktree isolation in later phases).
// Aggregates all problems; nil when every workspace is sound.
func ValidateWorkspaces(projects []Project) error {
	var problems []string
	for _, p := range projects {
		cwd := strings.TrimSpace(p.Cwd)
		if cwd == "" {
			problems = append(problems, fmt.Sprintf("project %q has no cwd", p.Name))
			continue
		}
		info, err := os.Stat(cwd)
		if err != nil {
			problems = append(problems, fmt.Sprintf("project %q cwd %q: %v", p.Name, cwd, err))
			continue
		}
		if !info.IsDir() {
			problems = append(problems, fmt.Sprintf("project %q cwd %q is not a directory", p.Name, cwd))
			continue
		}
		if !IsGitRepo(cwd) {
			problems = append(problems, fmt.Sprintf("project %q cwd %q is not a git repository", p.Name, cwd))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
