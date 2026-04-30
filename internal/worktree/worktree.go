// Package worktree implements the article's "git worktree isolation"
// (Phase 3 / s12) plus "conflict detection" (Phase 6 / s23). Each parallel
// agent gets a private working tree on its own branch; merges back via
// `git merge` with explicit conflict reporting.
package worktree

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Worktree is a created git worktree.
type Worktree struct {
	Path   string
	Branch string
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Preflight checks the repo is in a sane state to spawn worktrees.
func Preflight(repo string) error {
	if out, err := git(repo, "rev-parse", "--is-inside-work-tree"); err != nil || strings.TrimSpace(out) != "true" {
		return fmt.Errorf("not a git repo: %s", repo)
	}
	if out, _ := git(repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		return fmt.Errorf("dirty working tree: commit or stash before spawning worktrees")
	}
	if out, _ := git(repo, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(out) == "HEAD" {
		return fmt.Errorf("detached HEAD: checkout a branch first")
	}
	return nil
}

// Create makes a new worktree at `path` on a new branch `branch` from `base`.
func Create(repo, path, branch, base string) (*Worktree, error) {
	if err := Preflight(repo); err != nil {
		return nil, err
	}
	if base == "" {
		base = "HEAD"
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := git(repo, "worktree", "add", "-b", branch, abs, base); err != nil {
		return nil, err
	}
	return &Worktree{Path: abs, Branch: branch}, nil
}

// Remove tears down a worktree (does not delete its branch).
func Remove(repo, path string) error {
	_, err := git(repo, "worktree", "remove", "--force", path)
	return err
}

// Merge attempts a git merge of `branch` into the current branch of `repo`.
// On conflict it aborts and returns a structured error containing the
// conflicting files.
func Merge(repo, branch string) (string, error) {
	if out, err := git(repo, "merge", "--no-ff", branch); err != nil {
		_, _ = git(repo, "merge", "--abort")
		conf, _ := git(repo, "diff", "--name-only", "--diff-filter=U")
		return "", fmt.Errorf("merge conflict; aborted. files:\n%s\n--- merge output ---\n%s",
			strings.TrimSpace(conf), strings.TrimSpace(out))
	}
	return "merged " + branch, nil
}

// ----------------------------------------------------------------------
// ADK tool wrappers (operate on the repo at the process working dir)
// ----------------------------------------------------------------------

type createIn struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
	Base   string `json:"base,omitempty"`
}
type createOut struct {
	Result string `json:"result"`
}
type removeIn struct {
	Path string `json:"path"`
}
type removeOut struct {
	Result string `json:"result"`
}
type mergeIn struct {
	Branch string `json:"branch"`
}
type mergeOut struct {
	Result string `json:"result"`
}

// Tools returns three worktree management tools, scoped to `repo`.
func Tools(repo string) []tool.Tool {
	c, _ := functiontool.New(functiontool.Config{
		Name:        "worktree_create",
		Description: "Create an isolated git worktree at `path` on a new branch `branch` (optionally from `base`, default HEAD).",
	}, func(_ tool.Context, in createIn) (createOut, error) {
		w, err := Create(repo, in.Path, in.Branch, in.Base)
		if err != nil {
			return createOut{Result: "Error: " + err.Error()}, nil
		}
		return createOut{Result: fmt.Sprintf("worktree at %s on branch %s", w.Path, w.Branch)}, nil
	})
	r, _ := functiontool.New(functiontool.Config{
		Name:        "worktree_remove",
		Description: "Remove a worktree previously created with worktree_create.",
	}, func(_ tool.Context, in removeIn) (removeOut, error) {
		if err := Remove(repo, in.Path); err != nil {
			return removeOut{Result: "Error: " + err.Error()}, nil
		}
		return removeOut{Result: "removed " + in.Path}, nil
	})
	m, _ := functiontool.New(functiontool.Config{
		Name:        "worktree_merge",
		Description: "Merge a worktree branch back into the current branch. Aborts cleanly on conflict and reports the conflicting files.",
	}, func(_ tool.Context, in mergeIn) (mergeOut, error) {
		s, err := Merge(repo, in.Branch)
		if err != nil {
			return mergeOut{Result: "Error: " + err.Error()}, nil
		}
		return mergeOut{Result: s}, nil
	})
	return []tool.Tool{c, r, m}
}
