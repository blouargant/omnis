# Fleet Conductor + Dispatch — Implementation Plan (Plan 2a of the Fleet feature)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a routable **Fleet** squad whose **Conductor** lists your fleet projects, plans cross-project work, gets one approval, and **dispatches real coding tasks to a per-project Driver** — an omnis Coding-squad session rooted at that project's collection cwd and filed under its collection (so the collection's instructions inject) — with the Driver's result delivered back to the Conductor via the existing injected-turn rail.

**Architecture:** Reuse the existing spawn rail ([server/spawn.go](server/spawn.go) `materializeSession` + `runSpawnedTask`), which already mints a fresh session at an explicit cwd and delivers its task result back into the parent session. The one gap for a Driver is that generic `spawn_session` inherits the *parent's* cwd; a Driver must be rooted at its *project's* cwd + filed under its collection. So we add: (1) a `Collection` field on the spawn/materialize path; (2) a leader-only `fleet_dispatch(project, task)` tool that records a directive (mirroring `spawn_session` → `SpawnRegistry`); (3) a server drain that resolves the project via the Plan-1 fleet resolver and materializes+runs the Driver on the **Coding** squad; (4) the Conductor agent + Fleet squad + a plan-approve-execute instruction. Peer cross-project asks reuse the always-on teammate mailbox (Drivers register under their project name).

**Tech Stack:** Go; ADK (`tool`/`functiontool`) + `core/adk` façade (`adk.ToolContext`); the Plan-1 `internal/fleet` package; standard `go test`.

## Global Constraints

- **Builds on Plan 1** (already on this branch): `internal/fleet` has `Project{Name,Cwd,Engine,DependsOn}`, `Engine` (`EngineOmnis`/`EngineClaude`), `RoleProject`, `SetProjectsResolver`, `Validate`, `TopoOrder`, and the `fleet_projects` tool; the server installs the resolver from collections (`collectFleetProjects` in [server/fleet.go](server/fleet.go)).
- **Omnis engine only** in this plan. A project with `engine:"claude"` is dispatched with a clear "claude engine not yet available (Plan 3)" message — never silently run on the wrong engine.
- **Import-cycle rule:** `internal/fleet` still imports only stdlib + ADK + `core/adk`. The `fleet_dispatch` tool + its registry live in the **`agent`** package (on `Infrastructure`, like `SpawnRegistry`); the drain lives in **`server`** (like `drainSpawns`). `agent` may import `internal/fleet`.
- **ADK-v2 façade:** tool handlers take `adk.ToolContext`, not `tool.Context`.
- **No-op contract:** with no fleet projects declared, `fleet_dispatch` returns a clear error; a build that never mounts the Fleet squad is byte-identical. CLI/TUI never set the surface spawning flag, so the dispatch group is not mounted there.
- **Async, multi-turn dispatch (by design):** `fleet_dispatch` is fire-and-forget like `spawn_session` — the Driver runs after the Conductor's turn ends and its result is injected back as a *new* Conductor turn. The Conductor therefore coordinates across several turns (react to each delivered result, then dispatch the next per the DAG). This matches the spec's "Conductor waits on completion via the persisted injected-turn rail."
- **Engine→squad map:** `omnis` → the `Coding` squad (leader `coder`). Centralize this in one function.
- **English only** for user-facing strings/instructions.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `server/spawn.go` (modify) | `spawnOptions.Collection` + `materializeSession` files the session under it. | 1 |
| `server/newsession_collection_test.go` or a new `server/fleet_dispatch_test.go` | Assert a materialized session is filed under the given collection. | 1 |
| `internal/fleet/fleet.go` (modify) | Export `Projects() []Project` (wrapper over the resolver) + `EngineSquad(Engine) (string,bool)` engine→squad map. | 2 |
| `internal/fleet/fleet_test.go` (modify) | Test `EngineSquad`. | 2 |
| `agent/fleet_dispatch.go` (create) | `FleetDispatchDirective` + `FleetDispatchRegistry` (mirror `SpawnRegistry`) + the `fleet_dispatch` tool. | 2 |
| `agent/fleet_dispatch_test.go` (create) | Registry enqueue/drain + tool validates unknown project / claude-engine-not-ready. | 2 |
| `agent/infrastructure.go` (modify) | Add `FleetDispatches *FleetDispatchRegistry` to `Infrastructure`, built once. | 2 |
| `agent/squad.go` (modify) | Mount the `fleet_dispatch` tool group (leader-only, `opts.SessionSpawning`-gated, mirroring `spawn`). | 3 |
| `server/fleet_dispatch.go` (create) | `drainFleetDispatches` (resolve project → materialize Driver on Coding squad at project cwd/collection → run task, deliver back). | 3 |
| `server/sse.go` (modify) | Call `drainFleetDispatches` after `drainSpawns` in `handleMessages`. | 3 |
| `server/fleet_dispatch_test.go` (create/extend) | Drain mints a Driver with the right squad/cwd/collection. | 3 |
| `registry/agents/conductor/{agent.json,instruction.md}` (create) | The Conductor agent + its plan-approve-execute playbook. | 4 |
| `config/agents.json` (modify) | Add `conductor` to `agents`; add the `Fleet` squad. | 4 |

---

### Task 1: `Collection` on the spawn/materialize rail

**Files:**
- Modify: `server/spawn.go` (`spawnOptions` at :30-37; `materializeSession` at :44-100)
- Test: `server/fleet_dispatch_test.go` (new)

**Interfaces:**
- Consumes: nothing new.
- Produces: `spawnOptions` gains `Collection string`; `materializeSession` files the session under it (via `d.Registry.SetCollection` + `sessions.SetConversationCollection`) when non-empty.

- [ ] **Step 1: Write the failing test**

Create `server/fleet_dispatch_test.go`:

```go
package main

import (
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

func TestMaterializeSessionFilesUnderCollection(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	d := newTestServerDeps(t) // see note below
	meta := materializeSession(d, spawnOptions{
		Squad:      "coding",
		Collection: "Service A",
		Title:      "driver: Service A",
	})
	if meta == nil {
		t.Fatal("expected a session")
	}
	if meta.Collection != "Service A" {
		t.Fatalf("session not filed under collection: %q", meta.Collection)
	}
	if got := sessions.CollectionOf(meta.ID); got != "Service A" { // persisted mirror
		t.Fatalf("collection not persisted: %q", got)
	}
}
```

> **Note on `newTestServerDeps` / `sessions.CollectionOf`:** the server package's existing tests already construct a minimal `serverDeps` — grep the test files (`server/*_test.go`) for how `serverDeps` is built (e.g. `TestListSessionsPaginated`, `TestInstallFleetResolver…`) and reuse that exact helper/pattern rather than inventing one; if there is no shared helper, build `serverDeps{Registry: sessions.NewRegistry(...)}` the same way those tests do. For the persisted-collection assertion, use whatever read the codebase exposes — `sessions.ConversationCollectionOf`/`CollectionOf` if present, else load the conversation file and read `.Collection` (grep `SetConversationCollection` for the matching reader). Adjust the two names to what actually exists; do not invent an API.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run TestMaterializeSessionFilesUnderCollection -v`
Expected: FAIL — `spawnOptions` has no field `Collection`.

- [ ] **Step 3: Add the field + filing**

In `server/spawn.go`, add to `spawnOptions` (after `Dir`):

```go
	Collection   string // file the new session under this collection (empty ⇒ none)
```

In `materializeSession`, after the title block (after the `_ = sessions.SetConversationSquad(meta.ID, squad)` line), add:

```go
	if col := strings.TrimSpace(o.Collection); col != "" {
		d.Registry.SetCollection(meta.ID, col)
		_ = sessions.SetConversationCollection(meta.ID, col)
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./server/ -run TestMaterializeSessionFilesUnderCollection -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/spawn.go server/fleet_dispatch_test.go
git commit -m "feat(fleet): file a materialized session under a collection"
```

---

### Task 2: `fleet_dispatch` tool + registry (+ fleet helpers)

**Files:**
- Modify: `internal/fleet/fleet.go`, `internal/fleet/fleet_test.go`
- Create: `agent/fleet_dispatch.go`, `agent/fleet_dispatch_test.go`
- Modify: `agent/infrastructure.go`

**Interfaces:**
- Consumes: `fleet.Project`, `fleet.Engine`, `fleet.EngineOmnis/EngineClaude` (Plan 1); the resolver installed via `fleet.SetProjectsResolver`.
- Produces:
  - `fleet.Projects() []Project` — exported wrapper over the resolver (nil when unset).
  - `fleet.EngineSquad(e Engine) (squad string, ok bool)` — `EngineOmnis` → `"coding"`, else `("", false)`.
  - `agent.FleetDispatchDirective{Project, Task string}`; `agent.FleetDispatchRegistry` (Enqueue/Drain/Forget, mirroring `SpawnRegistry`); `Infrastructure.FleetDispatches`.
  - the `fleet_dispatch` tool (built by `fleetDispatchTool(reg *FleetDispatchRegistry)`).

- [ ] **Step 1: Write the failing fleet-helper test**

Add to `internal/fleet/fleet_test.go`:

```go
func TestEngineSquad(t *testing.T) {
	if s, ok := EngineSquad(EngineOmnis); !ok || s != "coding" {
		t.Fatalf("omnis => coding, got %q ok=%v", s, ok)
	}
	if _, ok := EngineSquad(EngineClaude); ok {
		t.Fatal("claude engine has no squad yet (Plan 3)")
	}
	if _, ok := EngineSquad(Engine("bogus")); ok {
		t.Fatal("unknown engine must not map")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/fleet/ -run TestEngineSquad -v`
Expected: FAIL — `EngineSquad` undefined.

- [ ] **Step 3: Add the fleet helpers**

In `internal/fleet/fleet.go`, add:

```go
// Projects returns the currently-configured fleet projects via the installed
// resolver (nil when no resolver is installed — e.g. CLI/TUI, or no server).
func Projects() []Project { return currentProjects() }

// EngineSquad maps a project engine to the squad name that runs a Driver for it.
// omnis → the Coding squad. The claude engine has no squad yet (Plan 3) and
// returns ok=false so callers report it as not-yet-available rather than
// silently running it on the wrong engine.
func EngineSquad(e Engine) (string, bool) {
	if e == EngineOmnis {
		return "coding", true
	}
	return "", false
}
```

> `currentProjects()` already exists in `internal/fleet/tool.go` (Plan 1). If it is defined there and unexported, this `Projects()` wrapper in `fleet.go` sees it (same package).

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/fleet/ -run TestEngineSquad -v`
Expected: PASS.

- [ ] **Step 5: Write the failing dispatch-tool + registry test**

Create `agent/fleet_dispatch_test.go`:

```go
package agent

import (
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/fleet"
)

func TestFleetDispatchRegistryEnqueueDrain(t *testing.T) {
	r := NewFleetDispatchRegistry()
	if !r.Enqueue("sess1", &FleetDispatchDirective{Project: "Service A", Task: "add field"}) {
		t.Fatal("enqueue failed")
	}
	got := r.Drain("sess1")
	if len(got) != 1 || got[0].Project != "Service A" {
		t.Fatalf("drain mismatch: %+v", got)
	}
	if len(r.Drain("sess1")) != 0 {
		t.Fatal("second drain should be empty")
	}
}

func TestFleetDispatchToolUnknownProject(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "Service A", Cwd: "/x/a", Engine: fleet.EngineOmnis}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	_, err := runFleetDispatch(reg, "sess1", fleetDispatchIn{Project: "Ghost", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "Ghost") {
		t.Fatalf("expected unknown-project error, got %v", err)
	}
}

func TestFleetDispatchToolClaudeEngineNotReady(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "Service B", Cwd: "/x/b", Engine: fleet.EngineClaude}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	_, err := runFleetDispatch(reg, "sess1", fleetDispatchIn{Project: "Service B", Task: "x"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "claude") {
		t.Fatalf("expected claude-not-ready error, got %v", err)
	}
	// nothing enqueued on error
	if len(reg.Drain("sess1")) != 0 {
		t.Fatal("must not enqueue on a rejected dispatch")
	}
	_ = adk.ToolContext(nil) // ensure the adk import is used if the test file needs it
}
```

> If `adk` ends up unused in the test file, drop that import + the last line. The real handler (`runFleetDispatch`) takes the session id directly so the test needs no ToolContext.

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./agent/ -run TestFleetDispatch -v`
Expected: FAIL — undefined `NewFleetDispatchRegistry`, `FleetDispatchDirective`, `runFleetDispatch`, `fleetDispatchIn`.

- [ ] **Step 7: Implement the directive, registry, and tool**

Create `agent/fleet_dispatch.go`:

```go
// fleet_dispatch.go — the leader-only `fleet_dispatch` tool. The Conductor hands
// one project a coding task; the tool records a directive (mirroring
// spawn_session → SpawnRegistry), and the server drains it after the turn,
// materialising a Driver session (a Coding-squad session rooted at the project's
// collection cwd + filed under its collection) and delivering the result back to
// the Conductor. Host-side record-then-drain, because agent cannot import server.
package agent

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/fleet"
)

// maxDispatchesPerSession bounds how many project dispatches one Conductor turn
// may queue, so a runaway plan can't fan out without limit.
const maxDispatchesPerSession = 16

// FleetDispatchDirective is one queued "dispatch this task to this project's
// Driver" request. Only the project name + task are recorded; the server
// re-resolves the project's cwd/collection/engine at drain time (single source
// of truth).
type FleetDispatchDirective struct {
	Project string
	Task    string
}

// FleetDispatchRegistry holds pending dispatches per Conductor session
// (process-wide, on Infrastructure, survives hot-reload). Mirrors SpawnRegistry.
type FleetDispatchRegistry struct {
	mu sync.Mutex
	m  map[string][]*FleetDispatchDirective
}

func NewFleetDispatchRegistry() *FleetDispatchRegistry {
	return &FleetDispatchRegistry{m: map[string][]*FleetDispatchDirective{}}
}

func (r *FleetDispatchRegistry) Enqueue(sessionID string, d *FleetDispatchDirective) bool {
	if r == nil || sessionID == "" || d == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.m[sessionID]) >= maxDispatchesPerSession {
		return false
	}
	r.m[sessionID] = append(r.m[sessionID], d)
	return true
}

func (r *FleetDispatchRegistry) Drain(sessionID string) []*FleetDispatchDirective {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.m[sessionID]
	delete(r.m, sessionID)
	return ds
}

func (r *FleetDispatchRegistry) Forget(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, sessionID)
}

type fleetDispatchIn struct {
	Project string `json:"project" jsonschema:"the fleet project (collection name) to hand the task to; must be one listed by fleet_projects"`
	Task    string `json:"task" jsonschema:"the self-contained coding task for that project's Driver; restate everything it needs — it does not see this conversation"`
}
type fleetDispatchOut struct {
	Result string `json:"result"`
}

// runFleetDispatch validates the project against the live fleet registry and
// enqueues the directive. Extracted from the tool closure so it is unit-testable
// without ADK plumbing (takes the session id directly).
func runFleetDispatch(reg *FleetDispatchRegistry, sessionID string, in fleetDispatchIn) (fleetDispatchOut, error) {
	name := strings.TrimSpace(in.Project)
	task := strings.TrimSpace(in.Task)
	if name == "" || task == "" {
		return fleetDispatchOut{}, fmt.Errorf("both project and task are required")
	}
	projects := fleet.Projects()
	var match *fleet.Project
	var names []string
	for i := range projects {
		names = append(names, projects[i].Name)
		if strings.EqualFold(projects[i].Name, name) {
			match = &projects[i]
		}
	}
	if match == nil {
		return fleetDispatchOut{}, fmt.Errorf("unknown fleet project %q; available: %s", in.Project, strings.Join(names, ", "))
	}
	if _, ok := fleet.EngineSquad(match.Engine); !ok {
		return fleetDispatchOut{}, fmt.Errorf("project %q uses the %q engine, which is not available yet (the external Claude Code worker lands in a later phase); only omnis-engine projects can be dispatched today", match.Name, match.Engine)
	}
	if !reg.Enqueue(sessionID, &FleetDispatchDirective{Project: match.Name, Task: task}) {
		return fleetDispatchOut{}, fmt.Errorf("too many dispatches this turn (max %d) — let the running Drivers report back first", maxDispatchesPerSession)
	}
	return fleetDispatchOut{Result: fmt.Sprintf("Dispatched %q to project %q. Its Driver is working in the background; its result will come back to you as a follow-up message.", task, match.Name)}, nil
}

// fleetDispatchTool builds the leader-only fleet_dispatch tool.
func fleetDispatchTool(reg *FleetDispatchRegistry) tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "fleet_dispatch",
		Description: "Hand a coding task to one fleet project's Driver — a dedicated session rooted in that " +
			"project's directory with the project's own instructions. Use the exact project names from " +
			"fleet_projects. The Driver runs in the background and its result is delivered back to you as a " +
			"follow-up message; dispatch dependencies (see fleet_projects order) BEFORE the projects that depend " +
			"on them, and wait for a project's result before dispatching a project that needs its output. Restate " +
			"everything the task needs in `task` — the Driver does not see this conversation.",
	}, func(ctx adk.ToolContext, in fleetDispatchIn) (fleetDispatchOut, error) {
		return runFleetDispatch(reg, ctx.SessionID(), in)
	})
	if err != nil {
		panic(fmt.Errorf("fleet_dispatch tool: %w", err))
	}
	return t
}
```

- [ ] **Step 8: Add the registry to `Infrastructure`**

In `agent/infrastructure.go`, find the `Infrastructure` struct field where `SpawnDirectives *SpawnRegistry` is declared and add beside it:

```go
	FleetDispatches *FleetDispatchRegistry
```

Then find where `SpawnDirectives` is initialised (grep `NewSpawnRegistry()` in that file — it's in the infrastructure builder) and add on the next line:

```go
	infra.FleetDispatches = NewFleetDispatchRegistry()
```

> Match the exact construction style at that site (field assignment on the `infra`/`inf` receiver, whatever the surrounding code uses). Also add `infra.FleetDispatches.Forget(id)` next to the existing `SpawnDirectives.Forget(id)` call if one exists in this file; the server also calls Forget via `forgetSessionState` (Task 3 note).

- [ ] **Step 9: Run to verify tests pass**

Run: `go test ./internal/fleet/... ./agent/ -run 'EngineSquad|FleetDispatch' -v`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/fleet/fleet.go internal/fleet/fleet_test.go agent/fleet_dispatch.go agent/fleet_dispatch_test.go agent/infrastructure.go
git commit -m "feat(fleet): fleet_dispatch tool + registry + engine→squad helper"
```

---

### Task 3: Mount the dispatch tool + server drain

**Files:**
- Modify: `agent/squad.go` (root tool-group mounting, near the `spawn` block at ~:190)
- Create: `server/fleet_dispatch.go`
- Modify: `server/sse.go` (call the drain after `drainSpawns`)
- Modify: `server/spawn.go` `forgetSessionState` (Forget fleet dispatches too)
- Test: extend `server/fleet_dispatch_test.go`

**Interfaces:**
- Consumes: `Infrastructure.FleetDispatches`, `fleet.Projects()`, `fleet.EngineSquad`, `materializeSession` (+ `Collection` from Task 1), `runSpawnedTask`.
- Produces: the `"fleet_dispatch"` tool group key; `drainFleetDispatches(d, parentID, parentUserID)`.

- [ ] **Step 1: Mount the tool group (mirror `spawn`)**

In `agent/squad.go`, right after the `spawn` mounting block:

```go
	if keySet["spawn"] && opts.SessionSpawning && !leaderless {
		leadTools = append(leadTools, spawnSessionTool(infra.SpawnDirectives, routerSquadCatalogue(runtime)))
	}
```

add:

```go
	if keySet["fleet_dispatch"] && opts.SessionSpawning && !leaderless {
		leadTools = append(leadTools, fleetDispatchTool(infra.FleetDispatches))
	}
```

- [ ] **Step 2: Write the failing drain test**

Add to `server/fleet_dispatch_test.go`:

```go
func TestDrainFleetDispatchesMintsDriver(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	d := newTestServerDeps(t)

	// A fleet project "Service A" (omnis engine) rooted at a real git dir.
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sessions.AddCollection("Service A"); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfileData("Service A", sessions.CollectionProfileData{
		Role: "project", Engine: "omnis", Cwd: repo,
	}); err != nil {
		t.Fatal(err)
	}
	installFleetResolver()
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })

	// A Conductor session enqueues a dispatch.
	conductor := d.Registry.New("fleet")
	d.Manager.Infra().FleetDispatches.Enqueue(conductor.ID,
		&toolkitagent.FleetDispatchDirective{Project: "Service A", Task: "add a field"})

	before := len(d.Registry.List())
	drainFleetDispatches(d, conductor.ID, sessions.DefaultUserID)
	after := d.Registry.List()
	if len(after) != before+1 {
		t.Fatalf("expected exactly one Driver session minted, got %d new", len(after)-before)
	}
	// Find the new Driver and assert its squad/cwd/collection.
	var driver *sessions.SessionMeta
	for _, m := range after {
		if m.ID != conductor.ID {
			driver = m
		}
	}
	if driver == nil {
		t.Fatal("no Driver session found")
	}
	if driver.Squad != "coding" {
		t.Fatalf("Driver squad = %q, want coding", driver.Squad)
	}
	if driver.Collection != "Service A" {
		t.Fatalf("Driver collection = %q, want Service A", driver.Collection)
	}
	if bashCwd.get(driver.ID) != repo {
		t.Fatalf("Driver cwd = %q, want %q", bashCwd.get(driver.ID), repo)
	}
}
```

> Imports for this file now include `os`, `path/filepath`, `github.com/blouargant/omnis/internal/fleet`, and `toolkitagent "github.com/blouargant/omnis/agent"`. `newTestServerDeps` must build `serverDeps` with a real `Manager` whose `Infra().FleetDispatches` is non-nil and `HasSquad("coding")` is true — grep existing server tests (e.g. the ones that call `d.Manager`) for the established construction; if the server tests use a fake/minimal Manager, extend that fake to expose `Infra().FleetDispatches` and `HasSquad`. If building a real Manager in a unit test is too heavy, split the assertion: unit-test the pure mapping (project→squad/cwd/collection) in a helper `fleetDriverOptions(project) spawnOptions` and test THAT directly, leaving the `materializeSession` call thin. Prefer the helper split if the Manager is hard to construct.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./server/ -run TestDrainFleetDispatches -v`
Expected: FAIL — `drainFleetDispatches` undefined.

- [ ] **Step 4: Implement the drain**

Create `server/fleet_dispatch.go`:

```go
// fleet_dispatch.go — server side of fleet_dispatch. After a Conductor turn,
// drainFleetDispatches materialises one Driver per queued dispatch: a Coding-squad
// session rooted at the project's collection cwd and filed under its collection,
// running the task in the background with the result delivered back to the
// Conductor (reusing the spawn rail). Mirrors drainSpawns.
package main

import (
	"strings"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/fleet"
)

// fleetDriverOptions maps a fleet project to the spawnOptions for its Driver.
// Returns ok=false when the project can't be dispatched (unknown, or an engine
// with no squad yet). Pure + unit-testable.
func fleetDriverOptions(projectName, userID string) (spawnOptions, bool) {
	for _, p := range fleet.Projects() {
		if !strings.EqualFold(p.Name, projectName) {
			continue
		}
		squad, ok := fleet.EngineSquad(p.Engine)
		if !ok {
			return spawnOptions{}, false
		}
		return spawnOptions{
			Squad:      squad,
			Title:      "driver: " + p.Name,
			Dir:        p.Cwd,
			Collection: p.Name,
			UserID:     userID,
		}, true
	}
	return spawnOptions{}, false
}

// drainFleetDispatches materialises every Driver the Conductor requested via
// fleet_dispatch during the just-finished turn on parentID, and delivers each
// Driver's result back to the Conductor. Uses the server root context so a
// client disconnect / Stop never cancels a dispatch.
func drainFleetDispatches(d serverDeps, parentID, parentUserID string) {
	if d.Manager == nil {
		return
	}
	infra := d.Manager.Infra()
	if infra == nil || infra.FleetDispatches == nil {
		return
	}
	dirs := infra.FleetDispatches.Drain(parentID)
	for _, dd := range dirs {
		if dd == nil {
			continue
		}
		opts, ok := fleetDriverOptions(dd.Project, parentUserID)
		if !ok {
			continue // unknown/unsupported — the tool already reported it to the model
		}
		meta := materializeSession(d, opts)
		if meta == nil {
			continue
		}
		// Deliver the Driver's result back into the Conductor session.
		runSpawnedTask(d, meta.ID, "driver: "+dd.Project, parentID, parentUserID, dd.Task)
	}
	// Reference the directive type so the import is used even if the loop is empty
	// in some build configuration.
	_ = toolkitagent.FleetDispatchDirective{}
}
```

> Drop the trailing `_ = toolkitagent.FleetDispatchDirective{}` line if `toolkitagent` is otherwise referenced (it is not, in this file — the drained slice is `[]*toolkitagent.FleetDispatchDirective` but comes from `infra.FleetDispatches.Drain`, whose type is inferred; keep the blank reference only if the build complains about an unused import, otherwise remove both the import and the line). Prefer: remove the import if unused.

- [ ] **Step 5: Call the drain after `drainSpawns` in `handleMessages`**

In `server/sse.go`, grep for the existing `drainSpawns(` call in `handleMessages` and add immediately after it:

```go
			drainFleetDispatches(d, meta.ID, userID)
```

> Match the exact variable names in scope at that call site (the session meta id and the user id used by the adjacent `drainSpawns` call) — copy them from the `drainSpawns` line.

- [ ] **Step 6: Forget fleet dispatches on session teardown**

In `server/spawn.go` `forgetSessionState`, beside `infra.SpawnDirectives.Forget(id)` add:

```go
			infra.FleetDispatches.Forget(id)
```

- [ ] **Step 7: Run the drain test + build**

Run: `go test ./server/ -run 'TestDrainFleetDispatches|TestMaterializeSessionFilesUnderCollection' -v && go build ./...`
Expected: PASS + build clean.

- [ ] **Step 8: Commit**

```bash
git add agent/squad.go server/fleet_dispatch.go server/sse.go server/spawn.go server/fleet_dispatch_test.go
git commit -m "feat(fleet): mount fleet_dispatch + server drain (mint per-project Drivers)"
```

---

### Task 4: The Conductor agent + Fleet squad

**Files:**
- Create: `registry/agents/conductor/agent.json`, `registry/agents/conductor/instruction.md`
- Modify: `config/agents.json` (`agents` list + a new `Fleet` squad)

**Interfaces:**
- Consumes: the `fleet`, `fleet_dispatch`, and `planning` tool groups (Tasks 2-3 + Plan 1); the always-on mailbox + `ask_user` a squad root gets automatically.
- Produces: a routable **Fleet** squad the Omnis router can hand "coordinate across my projects" requests to.

- [ ] **Step 1: Create the Conductor agent definition**

Create `registry/agents/conductor/agent.json`:

```json
{
  "name": "conductor",
  "description": "Multi-project fleet coordinator. Reads the fleet project registry, plans cross-project work, gets one approval, and dispatches coding tasks to each project's Driver.",
  "leader": true,
  "model_ref": "premium",
  "tools": ["fleet", "fleet_dispatch", "planning"],
  "skills": []
}
```

> `model_ref`: use the same tier the other coordinating leaders use. Grep `registry/agents/leader/agent.json` and `registry/agents/coder/agent.json` for the exact `model_ref` value they ship with and match it (likely `"premium"` or `"hosted"`); do not guess — copy the value one of those leaders uses.

- [ ] **Step 2: Create the Conductor instruction (the plan-approve-execute playbook)**

Create `registry/agents/conductor/instruction.md`:

```markdown
# Conductor — multi-project fleet coordinator

You coordinate work that spans several of the user's projects. Each project is a
collection with its own directory and its own instructions; a project's work is
carried out by its **Driver** (a dedicated session you start with `fleet_dispatch`).
You never edit project files yourself — you plan, get approval, and delegate.

## Your loop

1. **Survey.** Call `fleet_projects` to get the projects, their engines, their
   dependency edges, and the dependency-first `order`. If it reports problems
   (cycles, unknown edges, bad engines, missing directories), tell the user and
   stop — the fleet config must be fixed first.

2. **Plan.** Work out which projects must change and in what sequence. Respect the
   dependency `order`: a project that others depend on must be changed and finish
   **before** the projects that depend on it. Use your planning tools to write the
   plan as concrete per-project steps.

3. **Get one approval.** Present the whole plan to the user with `ask_user` — the
   projects touched, what each will do, and the order. Do not dispatch anything
   until the user approves. If they change it, re-plan and re-confirm.

4. **Execute in order.** For each step, call `fleet_dispatch(project, task)` with a
   **self-contained** task (the Driver does not see this conversation — restate the
   files, the contract, the exact change). Dispatch a dependency **before** the
   projects that need it. The Driver runs in the background; its result comes back
   to you as a follow-up message. **Wait for that result before dispatching a
   project that depends on it.** Independent projects may be dispatched together.

5. **React to each returned result.** When a Driver's result arrives, check it did
   what the plan needed. If a project needs a change in another project that the
   plan didn't foresee, do NOT quietly expand scope — surface it to the user with
   `ask_user` and get approval before dispatching the extra work.

6. **Report.** When every step is done, summarise what each project changed.

## Cross-project requests between Drivers

A Driver can ask another project's Driver directly over the mailbox
(`teammate_ask`/`teammate_tell`), addressing it by the project name. When your plan
needs project B to produce something for project A, prefer dispatching B first and
feeding its result into A's task; use direct Driver-to-Driver asks only for tight,
in-flight coordination you called out in the approved plan.

## Rules

- Only omnis-engine projects can be dispatched today; if the user asks to dispatch a
  `claude`-engine project, explain that the external Claude Code worker arrives in a
  later phase and offer to proceed with the omnis-engine projects.
- One approval per unit of work. New cross-project needs discovered mid-execution
  are a fresh approval, not a silent expansion.
- Keep each `fleet_dispatch` task self-contained and specific.
```

- [ ] **Step 3: Register the agent + add the Fleet squad**

In `config/agents.json`, add `"conductor"` to the `agents` array (after `"coder"` group is fine), and add this squad to the `squads` array (after the `Coding` squad):

```json
    {
      "name": "Fleet",
      "description": "Multi-project coordinator. Route here when the user wants to coordinate a change across SEVERAL of their projects at once — e.g. 'add this feature across the services', 'update the shared contract and every consumer', 'plan the work spanning projects A, B and C'. The Conductor reads the fleet project registry (collections marked as projects), plans the cross-project work, gets one approval, and dispatches each project's task to its own Driver. NOT for a change to a single project (that's the Coding squad).",
      "leader": "conductor",
      "members": []
    }
```

- [ ] **Step 4: Verify the config resolves + the squad builds**

Run:
```bash
go build ./... && go test ./agent/ -run 'Squad|Resolve' 2>&1 | tail -20
```
Then a resolution smoke check (the fleet config is read from the running install, so use a repo-local override to point at the repo's `config/`):
```bash
OMNIS_SYSTEM_CONFIG_DIR="$(pwd)/config" env -u OMNIS_CONFIG_PATH go run . -h >/dev/null 2>&1; echo "config load exit=$?"
```
Expected: build clean; agent squad/resolve tests pass. If there is a dedicated config-resolution test (grep `ResolveRuntimeSettings` in `agent/*_test.go`), prefer adding a focused assertion there that a squad named `Fleet` resolves with leader `conductor`.

> **Config-resolution test (preferred over the smoke run):** add to the existing agent config-resolution test file a case that loads `config/agents.json` (or a fixture) and asserts `ResolveRuntimeSettings` yields a squad `Fleet` with `Leader == "conductor"` and that `conductor` resolves as a leader with tools including `fleet`/`fleet_dispatch`/`planning`. Grep for how existing squad-resolution tests load config and mirror it.

- [ ] **Step 5: Commit**

```bash
git add registry/agents/conductor/agent.json registry/agents/conductor/instruction.md config/agents.json
git commit -m "feat(fleet): add Conductor agent + routable Fleet squad"
```

---

## Self-Review

**Spec coverage (spec §7 Drivers, §10 coordination flow, phase 1 remainder):**
- Routable Fleet squad + Conductor → Task 4. ✓
- Conductor composes fleet_projects + planning + ask_user (always-on) + dispatch → Task 4 tools + instruction. ✓
- Per-project Driver = omnis Coding session rooted at the project cwd + filed under its collection → Tasks 1-3 (`fleetDriverOptions` + `materializeSession` Collection). ✓
- Plan → approve once → execute topologically, gate on new scope → Conductor instruction (§10). ✓ (Ordering is Conductor-followed from `fleet_projects.order`; host-enforced parallelism is a later plan — stated in Global Constraints.)
- Conductor waits on completion via the persisted injected-turn rail → `runSpawnedTask` deliver-back. ✓
- Peer cross-project asks via the mailbox → Drivers are squad roots (always-on mailbox), registered under the project title; documented in the instruction. ✓
- omnis engine only; claude engine reported not-ready → `EngineSquad` + tool + drain guards. ✓
- No-op contract → nil resolver ⇒ empty projects ⇒ dispatch errors cleanly; group unmounted without `SessionSpawning`. ✓
- **Deferred (correctly out of scope, noted):** task-scoped mailbox addressing, host-enforced topological parallelism/sync, the `claude` worker engine (Plan 3), git-worktree fork isolation (Plan 4), web-UI fleet fields.

**Placeholder scan:** The test-helper notes (`newTestServerDeps`, `sessions.CollectionOf`, the Manager construction, the `model_ref` value, the `drainSpawns`/`agent.SetCollectionResolver` anchors) instruct the implementer to grep for and reuse the EXISTING pattern/value rather than invent one — because those are pre-existing conventions in files outside this plan's creation set. Each names the exact symbol to grep and a concrete fallback. No `TBD`/`TODO`.

**Type consistency:** `FleetDispatchDirective{Project,Task}`, `FleetDispatchRegistry` (Enqueue/Drain/Forget), `runFleetDispatch(reg,sessionID,in)`, `fleetDispatchIn{Project,Task}` are used identically across Tasks 2-3 and both test files. `fleet.EngineSquad`/`fleet.Projects` signatures match their call sites. `spawnOptions.Collection` (Task 1) matches `fleetDriverOptions` (Task 3). Squad name `"coding"` is lower-cased consistently (matches `materializeSession`'s `strings.ToLower`).

## Post-review fixes (applied after the whole-branch review)

The opus whole-branch review found one **Critical** the four tasks missed, fixed in a follow-up commit:

- **Critical — the injected-turn rail must drain too.** `drainSpawns`/`drainFleetDispatches` were only called in `handleMessages` (the interactive `RunWithRouting` path). But when a Driver's result is delivered back, the Conductor turn runs through `injectTurnRouted` (in `server/mailbox_push.go`), which did **not** drain — so a follow-up `fleet_dispatch` issued *in a deliver-back turn* was enqueued but never materialized, silently stalling multi-wave dependency execution after wave 1 (and the same latent gap existed for `spawn_session` from any injected turn). **Fix:** call both drains at the end of `injectTurnRouted` (after the turn runs + persists, before `return reply`), using its `sessionID`/`userID`. `handleMessages` is untouched (no double-drain); recursion is bounded (only the Conductor holds `fleet_dispatch`, capped at 16/turn, driven by the finite DAG).
- **Important — `runFleetDispatch` happy-path coverage:** added tests for a valid omnis enqueue (one canonical directive), blank-input and over-cap rejection, and a no-enqueue assertion on the unknown-project path.
- **Minor — wrong-squad safety:** `drainFleetDispatches` now skips (with a log) when the resolved squad is absent, instead of letting `materializeSession` silently fall back to the System squad.

Deferred-minor triage: the double persisted-write (T1), the duplicated `"driver: "` label (T3), and the model_ref-not-pinned fixture (T4) were judged acceptable-as-is; the coverage gap (T2) was fixed above.
