// fleet_worktree.go — per-experiment git-worktree isolation. A forked Conductor
// chat (FleetExperiment) runs each project's Driver in its own worktree off the
// project repo's HEAD, so competing forks never touch a project's main checkout.
// Worktrees live under $OMNIS_HOME/fleet-worktrees/<session>/<project> and are
// created on first dispatch, reused across a project's re-dispatches within the
// experiment, and cleaned up (only when clean) on the session's teardown.
package main

import (
	"fmt"
	"log"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/worktree"
)

// fleetWorktreeEntry is one created worktree: its checkout path plus the main
// repo it was created from. Storing the repo alongside the path means cleanup
// never needs to reverse-engineer the main repo from the worktree's git
// metadata (`git rev-parse --git-common-dir` gymnastics) — it's just there.
type fleetWorktreeEntry struct {
	path string
	repo string
}

var (
	fleetWtMu sync.Mutex
	// sessionID -> (project name -> worktree entry)
	fleetWt = map[string]map[string]fleetWorktreeEntry{}
)

// fleetWorktreeRoot is the base dir for a session's experiment worktrees.
func fleetWorktreeRoot(sessionID string) string {
	return filepath.Join(paths.Home(), "fleet-worktrees", safeSeg(sessionID))
}

// safeSeg keeps a path segment safe (no separators / traversal).
func safeSeg(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// shortID is a stable short form of a session id for the branch name.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// fleetWorktreeDir returns the worktree checkout path for (sessionID, project),
// creating it off repoCwd's HEAD on first call and reusing it thereafter. It
// errors (and creates nothing) when repoCwd is not a clean, on-a-branch git repo
// (worktree.Preflight) — the caller must surface that rather than dispatch into
// the main checkout.
func fleetWorktreeDir(sessionID, project, repoCwd string) (string, error) {
	fleetWtMu.Lock()
	defer fleetWtMu.Unlock()
	if m := fleetWt[sessionID]; m != nil {
		if e, ok := m[project]; ok && e.path != "" {
			return e.path, nil
		}
	}
	path := filepath.Join(fleetWorktreeRoot(sessionID), safeSeg(project))
	branch := "omnis-fleet/" + safeSeg(shortID(sessionID)) + "/" + safeSeg(project)
	wt, err := worktree.Create(repoCwd, path, branch, "HEAD")
	if err != nil {
		return "", fmt.Errorf("could not isolate project %q for this experiment: %w", project, err)
	}
	if fleetWt[sessionID] == nil {
		fleetWt[sessionID] = map[string]fleetWorktreeEntry{}
	}
	fleetWt[sessionID][project] = fleetWorktreeEntry{path: wt.Path, repo: repoCwd}
	return wt.Path, nil
}

// fleetWorktreeCleanup removes a session's experiment worktrees, but ONLY those
// whose working tree is clean; a worktree with uncommitted experiment changes is
// kept (and logged) so the user doesn't lose it. Called on the experiment
// session's delete/archive. The stored map entry is always dropped.
func fleetWorktreeCleanup(sessionID string) {
	fleetWtMu.Lock()
	m := fleetWt[sessionID]
	delete(fleetWt, sessionID)
	fleetWtMu.Unlock()
	for project, e := range m {
		if worktreeDirty(e.path) {
			log.Printf("fleet worktree: kept experiment worktree with uncommitted changes: project=%q path=%q", project, e.path)
			continue
		}
		if err := worktree.Remove(e.repo, e.path); err != nil {
			log.Printf("fleet worktree: failed to remove experiment worktree project=%q path=%q: %v", project, e.path, err)
		}
	}
}

// worktreeDirty reports whether the worktree at path has uncommitted changes.
// A command error (path gone, git missing, ...) is treated as dirty so cleanup
// never discards something it can't actually verify is safe to remove.
func worktreeDirty(path string) bool {
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return true
	}
	return strings.TrimSpace(string(out)) != ""
}
