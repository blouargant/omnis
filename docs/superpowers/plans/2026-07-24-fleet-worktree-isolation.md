# Fleet Worktree Isolation (fork = experiment) — Implementation Plan (Plan 4a, final Fleet plan)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a **forked** Conductor chat an **isolated experiment**: its per-project Drivers run in a dedicated git **worktree** per project (a fresh branch off the repo's HEAD) instead of the project's main checkout, so two competing forks never collide on disk. Worktrees are created on first dispatch and cleaned up on the experiment session's teardown — but only when clean, so uncommitted experiment work is never silently destroyed.

**Architecture:** Three pieces, all server-side (the fleet dispatch is server-only). (1) A persisted `FleetExperiment` flag on the session, set by `handleFork` when the forked source is a Fleet (Conductor) chat — mirroring the existing `Archived`/`Hidden` flags. (2) A process-wide per-(experiment-session, project) worktree store + resolver (`internal/worktree` `Preflight`/`Create`), which `drainFleetDispatches` consults: for an experiment Conductor, a Driver's cwd is the project's worktree (created on first touch) instead of the collection cwd; a dirty/detached project repo fails that dispatch with a clear message (never a silent fall-back to the main tree). (3) Cleanup on the experiment session's delete/archive: remove each worktree only if its tree is clean, else keep it and log a warning. A normal (non-fork) Conductor dispatches into the main checkout exactly as in Plan 2a — this plan only changes the forked path.

**Tech Stack:** Go; `internal/worktree` (`Preflight`/`Create`/`Remove`); `internal/paths` (`$OMNIS_HOME`); the Plan-2a dispatch drain; standard `go test` with real temp git repos.

## Global Constraints

- **Only a FORK isolates.** A normal Conductor session's dispatch is byte-identical to Plan 2a (main checkout). This plan is additive on the fork path only.
- **Clean-repo requirement is real:** `worktree.Create` runs `Preflight`, which rejects a **dirty** working tree and a **detached HEAD**. An experiment dispatch to a project whose repo is dirty/detached must **fail that project's dispatch with an actionable message** back to the Conductor ("commit or stash project X first") — it must NOT dispatch into the project's main checkout (that would break isolation, the whole point).
- **Never destroy experiment work:** cleanup removes a worktree only when `git status --porcelain` in it is empty; a worktree with uncommitted changes is kept + logged. `worktree.Remove` uses `--force`, so cleanliness MUST be checked before calling it.
- **Merge-back is out of scope** (spec §2 non-goal). `worktree.Merge` exists but is not called here; the user merges a winning experiment branch themselves.
- **No-op contract:** no forks ⇒ no worktrees ever created; a fork of a non-Fleet chat sets no flag and behaves as today; CLI/TUI unaffected (server-only). A build with the flag never set is byte-identical.
- **English only** for user-facing strings.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/sessions/sessions.go` + `history.go` (modify) | `FleetExperiment bool` on `SessionMeta` + `ConversationFile`; `Registry.SetFleetExperiment`; `sessions.SetConversationFleetExperiment`; map it in `LoadPersistedSessions`. | 1 |
| `internal/sessions/*_test.go` (extend) | Flag persists + round-trips. | 1 |
| `server/fork_rewind.go` (modify) | `handleFork` sets `FleetExperiment` on the fork when the source is a Fleet-squad session. | 1 |
| `server/fork_experiment_test.go` (create) | Fork of a Fleet session → flag set; fork of a non-Fleet session → not set. | 1 |
| `server/fleet_worktree.go` (create) | The per-(session,project) worktree store + `fleetWorktreeDir` resolver (Preflight+Create on first touch) + `fleetWorktreeCleanup`. | 2 |
| `server/fleet_dispatch.go` (modify) | `drainFleetDispatches`: for an experiment Conductor, override the Driver's cwd with the project's worktree (skip-with-message on Preflight failure). | 2 |
| `server/fleet_worktree_test.go` (create) | Resolver creates a worktree in a temp git repo; reuse on 2nd call; dirty repo → error. | 2 |
| `server/spawn.go` (modify) | `forgetSessionState` (or `deleteSession`) calls `fleetWorktreeCleanup(id)`. | 3 |
| `server/fleet_worktree_test.go` (extend) | Clean worktree removed on cleanup; dirty worktree kept. | 3 |

---

### Task 1: `FleetExperiment` session flag + set it on fork

**Files:** `internal/sessions/sessions.go`, `internal/sessions/history.go` (+ tests); `server/fork_rewind.go`, `server/fork_experiment_test.go`

**Interfaces produced:** `SessionMeta.FleetExperiment bool`; `Registry.SetFleetExperiment(id string, v bool) bool`; `sessions.SetConversationFleetExperiment(id string, v bool) error`; `ConversationFile.FleetExperiment bool`.

- [ ] **Step 1: Write the failing test (flag round-trip + fork sets it)**

Add to `internal/sessions/…_test.go` a round-trip test (grep for how `Hidden`/`Archived` are tested — mirror it): set `FleetExperiment` via `Registry.SetFleetExperiment` + `SetConversationFleetExperiment`, reload, assert it persists.
Create `server/fork_experiment_test.go`: fork a session on squad `"fleet"` → the new session's `FleetExperiment` is true; fork a session on squad `"system"` → false. (Reuse the httptest router harness the other server tests use; grep for how `handleFork` is driven or how a fork test builds `serverDeps`.)

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/sessions/ ./server/ -run 'FleetExperiment|Fork' -v` → FAIL.

- [ ] **Step 3: Add the flag (mirror `Hidden` EXACTLY)**

`Hidden` was added as a session flag in an earlier feature — grep `internal/sessions/` for **every** place `Hidden` appears and add `FleetExperiment` beside each:
- `SessionMeta` struct: `FleetExperiment bool` (+ any json tag `Hidden` carries).
- `ConversationFile` struct: `FleetExperiment bool` (json `fleet_experiment,omitempty`).
- `Registry.SetHidden` → add `Registry.SetFleetExperiment(id, v)` (in-memory + async-persist via `SetConversationFleetExperiment`, same shape).
- `sessions.SetConversationHidden` → add `SetConversationFleetExperiment` (persist via the conversation lock, same shape).
- `LoadPersistedSessions` where it maps `Hidden` → also map `FleetExperiment`.

- [ ] **Step 4: Set it on fork**

In `server/fork_rewind.go` `handleFork`, after the fork is registered (after the `bashCwd.set(newMeta.ID, …)` / collection-mirror block, before the reseed is fine), add:

```go
		// A fork of a Fleet (Conductor) chat is an isolated experiment: its
		// per-project Drivers run in git worktrees (see server/fleet_worktree.go),
		// so competing forks never collide on the projects' main checkouts.
		if isFleetSquad(srcSquad) {
			d.Registry.SetFleetExperiment(newMeta.ID, true)
			_ = sessions.SetConversationFleetExperiment(newMeta.ID, true)
		}
```

Add the helper (in `server/fleet.go` or `server/fork_rewind.go`):

```go
// isFleetSquad reports whether a squad name is the Fleet coordinator squad (the
// one whose Conductor dispatches per-project Drivers). Keyed on the shipped
// squad name; squad names are lower-cased at resolution.
func isFleetSquad(name string) bool { return strings.EqualFold(strings.TrimSpace(name), "fleet") }
```

> If you'd rather not hard-code `"fleet"`, resolve it via `d.Manager`: a squad is the fleet squad iff its leader agent is `conductor`. Grep the Manager/runtime API for a way to get a squad's leader (`RuntimeSettings.Squad(name).Leader`); if that's cheaply reachable at fork time, prefer `leader == "conductor"`. Otherwise the name check is acceptable for v1 — note which you used.

- [ ] **Step 5: Run to verify it passes** — the tests + `go test ./internal/sessions/... ./server/...` + `go build ./...` → PASS.

- [ ] **Step 6: Commit**
```bash
git add internal/sessions/ server/fork_rewind.go server/fleet.go server/fork_experiment_test.go
git commit -m "feat(fleet): mark a fork of a Fleet chat as an isolated experiment"
```

---

### Task 2: worktree store + resolver + dispatch integration

**Files:** `server/fleet_worktree.go` (create); `server/fleet_dispatch.go` (modify); `server/fleet_worktree_test.go` (create)

**Interfaces produced:** `fleetWorktreeDir(sessionID, project, repoCwd string) (path string, err error)` (create-on-first-touch, reused thereafter); `fleetWorktreeCleanup(sessionID string)` (Task 3 uses it); an internal `sessionID → {project → worktree}` store.

- [ ] **Step 1: Write the failing resolver test**

Create `server/fleet_worktree_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails** — `go test ./server/ -run TestFleetWorktree -v` → FAIL (undefined).

- [ ] **Step 3: Implement the store + resolver**

Create `server/fleet_worktree.go`:

```go
// fleet_worktree.go — per-experiment git-worktree isolation. A forked Conductor
// chat (FleetExperiment) runs each project's Driver in its own worktree off the
// project repo's HEAD, so competing forks never touch a project's main checkout.
// Worktrees live under $OMNIS_HOME/fleet-worktrees/<session>/<project> and are
// created on first dispatch, reused across a project's re-dispatches within the
// experiment, and cleaned up (only when clean) on the session's teardown.
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/worktree"
)

var (
	fleetWtMu sync.Mutex
	// sessionID -> (project name -> worktree checkout path)
	fleetWt = map[string]map[string]string{}
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

// fleetWorktreeDir returns the worktree checkout path for (sessionID, project),
// creating it off repoCwd's HEAD on first call and reusing it thereafter. It
// errors (and creates nothing) when repoCwd is not a clean, on-a-branch git repo
// (worktree.Preflight) — the caller must surface that rather than dispatch into
// the main checkout.
func fleetWorktreeDir(sessionID, project, repoCwd string) (string, error) {
	fleetWtMu.Lock()
	defer fleetWtMu.Unlock()
	if m := fleetWt[sessionID]; m != nil {
		if p := m[project]; p != "" {
			return p, nil
		}
	}
	path := filepath.Join(fleetWorktreeRoot(sessionID), safeSeg(project))
	branch := "omnis-fleet/" + safeSeg(shortID(sessionID)) + "/" + safeSeg(project)
	wt, err := worktree.Create(repoCwd, path, branch, "HEAD")
	if err != nil {
		return "", fmt.Errorf("could not isolate project %q for this experiment: %w", project, err)
	}
	if fleetWt[sessionID] == nil {
		fleetWt[sessionID] = map[string]string{}
	}
	fleetWt[sessionID][project] = wt.Path
	return wt.Path, nil
}

// shortID is a stable short form of a session id for the branch name.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
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
	for project, path := range m {
		// repo = the worktree's own dir; `git -C <wt> worktree remove` is run
		// against the main repo, but Remove takes the repo path — resolve it from
		// the worktree's main-worktree. Simplest: run status in the worktree; if
		// clean, remove via the worktree's own toplevel.
		if worktreeDirty(path) {
			logFleet("kept experiment worktree with uncommitted changes: project=%q path=%q", project, path)
			continue
		}
		if err := worktree.Remove(mainRepoOf(path), path); err != nil {
			logFleet("failed to remove experiment worktree project=%q path=%q: %v", project, path, err)
		}
	}
}

// worktreeDirty reports whether the worktree at path has uncommitted changes.
func worktreeDirty(path string) bool {
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").CombinedOutput()
	if err != nil {
		return true // be safe: if we can't tell, keep it
	}
	return strings.TrimSpace(string(out)) != ""
}

// mainRepoOf returns the main repo path for a worktree (its common dir's parent).
func mainRepoOf(worktreePath string) string {
	out, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--path-format=absolute", "--git-common-dir").CombinedOutput()
	if err != nil {
		return worktreePath
	}
	commonDir := strings.TrimSpace(string(out)) // <mainrepo>/.git
	return filepath.Dir(commonDir)
}

func logFleet(format string, args ...any) { /* use the server's logging idiom */ }
```

> **Two things to resolve against the codebase while implementing:**
> 1. `logFleet` — replace with the server package's actual logging idiom (grep `server/*.go` for `log.Printf`/a logger; match it). It's fine for `logFleet` to just wrap `log.Printf`.
> 2. `mainRepoOf` — verify the `--path-format=absolute --git-common-dir` invocation returns `<mainrepo>/.git` on the installed git; if the flag combo isn't supported, use `git -C <wt> rev-parse --git-common-dir` (may be relative) and resolve it to abs, then `filepath.Dir`. The goal: `worktree.Remove` needs the MAIN repo path, not the worktree path. Alternatively, store the `repoCwd` alongside the path in the map (change the map value to a small struct `{path, repo}`) so cleanup has the repo directly — **prefer this**, it avoids the git gymnastics entirely. If you take the struct route, adjust `fleetWorktreeDir` to store `{path, repo: repoCwd}` and `fleetWorktreeCleanup` to use the stored repo.

- [ ] **Step 4: Run the resolver tests** — `go test ./server/ -run TestFleetWorktree -v` → PASS.

- [ ] **Step 5: Integrate into the dispatch drain**

In `server/fleet_dispatch.go` `drainFleetDispatches`, after computing `opts` from `fleetDriverOptions` and before `materializeSession`, add the experiment override:

```go
		// If the Conductor session is a forked experiment, isolate this project's
		// Driver in a git worktree instead of the project's main checkout.
		if meta, ok := d.Registry.Get(parentID); ok && meta.FleetExperiment {
			wt, err := fleetWorktreeDir(parentID, dd.Project, opts.Dir)
			if err != nil {
				// Don't dispatch into the main tree — that would break isolation.
				// Report the failure back to the Conductor so it can tell the user.
				runSpawnedTaskNotice(d, parentID, parentUserID, fmt.Sprintf(
					"Could not run project %q in this experiment: %v. Commit or stash that project's repo, then retry.", dd.Project, err))
				continue
			}
			opts.Dir = wt
		}
```

> `runSpawnedTaskNotice` — a tiny helper that injects a one-way notice turn back into the Conductor (reuse the `formatSpawnResultNotice`/`injectTurn(... "mailbox_push")` pattern already in `server/spawn.go`; grep it). If a suitable one-liner already exists, use it; otherwise add a 3-line helper next to `runSpawnedTask`. Needs `fmt` imported in fleet_dispatch.go.

- [ ] **Step 6: Run** `go test ./server/... -run 'Fleet|Collection' && go build ./...` → PASS.

- [ ] **Step 7: Commit**
```bash
git add server/fleet_worktree.go server/fleet_dispatch.go server/fleet_worktree_test.go
git commit -m "feat(fleet): run a forked experiment's Drivers in per-project git worktrees"
```

---

### Task 3: Cleanup on teardown

**Files:** `server/spawn.go` (`forgetSessionState`); `server/fleet_worktree_test.go` (extend)

- [ ] **Step 1: Write the failing cleanup test**

Add to `server/fleet_worktree_test.go`:

```go
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
```

(add the `worktree` import to the test file.)

- [ ] **Step 2: Run to verify it fails** — `go test ./server/ -run TestFleetWorktreeCleanup -v` → FAIL (cleanup not wired to remove/keep, or already passes if Task 2 implemented cleanup fully — if it passes, that's fine; this test locks the behavior).

- [ ] **Step 3: Wire cleanup into teardown**

In `server/spawn.go` `forgetSessionState`, add (beside the existing `Forget` calls):

```go
	fleetWorktreeCleanup(id)
```

`forgetSessionState` is called from both `deleteSession` and the archive handler, so an experiment's worktrees are cleaned on delete AND archive.

- [ ] **Step 4: Run** `go test ./server/ -run 'TestFleetWorktree' -v && go build ./... && go vet ./server/...` → PASS.

- [ ] **Step 5: Commit**
```bash
git add server/spawn.go server/fleet_worktree_test.go
git commit -m "feat(fleet): clean up experiment worktrees on teardown (keep if dirty)"
```

---

## Self-Review

**Spec coverage (§9 workspace resolution & fork isolation; §4 decision 8):**
- Forked chat → per-project git worktree, created on first touch → Tasks 1-2. ✓
- Root (normal) chat → collection cwd, unchanged → untouched Plan-2a path (the override is gated on `meta.FleetExperiment`). ✓
- Cleanup if unchanged; keep + warn if dirty → Task 3 + `fleetWorktreeCleanup`. ✓
- Fork = experiment marked automatically → Task 1 `handleFork` + `isFleetSquad`. ✓
- **Honest limitation stated:** a dirty/detached project repo can't be isolated (Preflight), so the experiment dispatch fails that project with an actionable message rather than breaking isolation. ✓
- **Deferred (spec non-goal):** merge-back of a winning experiment branch (the user does it; `worktree.Merge` is available but uncalled). Also still deferred fleet-wide: host-enforced topological parallelism, task-scoped mailbox addressing, the unattended-driver permission-mode question.

**Placeholder scan:** the resolve-against-codebase notes (mirror `Hidden`; the `isFleetSquad` name-vs-leader choice; `logFleet` logging idiom; `mainRepoOf` vs storing the repo in the map — with a stated PREFERENCE to store `{path, repo}`; `runSpawnedTaskNotice` reuse) each name the exact symbol + a concrete fallback. No `TBD`.

**Type consistency:** `FleetExperiment` is used identically on `SessionMeta`/`ConversationFile`/the setters (Task 1) and read via `d.Registry.Get(parentID).FleetExperiment` in the drain (Task 2). `fleetWorktreeDir(sessionID, project, repoCwd) (string, error)` and `fleetWorktreeCleanup(sessionID)` signatures match their call sites (drain + `forgetSessionState`) and tests. `opts.Dir` (the Plan-2a `spawnOptions.Dir`) is the single override point.
