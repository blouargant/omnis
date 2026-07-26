# Fleet grouping — Plan 2: session scoping + `/api/fleets` routes

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a Conductor chat scopable to one fleet (so `fleet_projects`/`fleet_dispatch` only ever see that fleet's projects), and expose the fleet registry over HTTP (`/api/fleets` CRUD + project assign/unassign, plus a `fleet` field on session creation for the "Coordinate" action).

**Architecture:** A session carries a `fleet` name (mirroring its `squad`/`collection`). `internal/fleet` gains a session→fleet resolver hook (same pattern as `SetProjectsResolver`) and a `ProjectsForSession` filter; the two fleet tools filter the global project pool to the session's fleet. The server maps each project's `fleet` tag into `fleet.Project`, installs the resolver, and adds `/api/fleets` routes over the Plan-1 `internal/sessions` registry functions.

**Tech Stack:** Go, gin (HTTP), ADK tools. Builds on Plan 1 (`internal/sessions` fleets registry + `fleet` membership tag).

**Spec:** [docs/superpowers/specs/2026-07-26-fleet-grouping-design.md](../specs/2026-07-26-fleet-grouping-design.md)
**Depends on:** Plan 1 (commits `35e678a..541a270`) — `sessions.ListFleets/AddFleet/RenameFleet/RemoveFleet/UpdateFleetMeta/FleetMetaFor/FleetMembers/AssignProject/UnassignProject/FleetExists/ValidFleetName/ValidDefaultEngine/FleetMetaData/UngroupedFleet`, and `CollectionProfileData.Fleet`.

## Global Constraints

- **Scoping is by session fleet.** `fleet_projects` and `fleet_dispatch` must show/dispatch only the projects of the session's fleet. A session with **no** fleet (`""`) scopes to **Ungrouped** (projects whose `fleet` tag is empty) — this is today's behaviour, so existing dispatch is unchanged.
- **`internal/fleet` must NOT import `internal/sessions`** (import cycle). Session→fleet mapping reaches it only through a resolver hook the server installs (mirror `SetProjectsResolver`).
- **Fold-unknown → Ungrouped at the server boundary.** `collectFleetProjects` sets `Project.Fleet` to `""` when the collection's tag names a fleet not in `sessions.ListFleets()`, so `internal/fleet` only ever sees a clean `""`-or-real fleet name.
- **No-op / additive.** With no fleets defined, every project is Ungrouped and every path behaves byte-identically to Plan-1-HEAD. CLI/TUI never install the resolvers.
- **Route placement:** the new routes live under `/api/fleets/...` — a fresh top-level tree, registered next to `/api/collections` in `server/server.go` (no `:id` wildcard collision).
- **`fleets_changed`** (no session id) is broadcast on every fleet mutation; assign/unassign additionally broadcast **`collections_changed`** (a project's membership changes its role/visibility).
- **Atomic-on-rejection:** a route that validates several fields must validate ALL before writing any (mirror the Plan-4b collections PATCH fix).
- Tests redirect state with `t.Setenv("OMNIS_HOME", t.TempDir())`. Commit after every task with the `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer.

---

### Task 1: session `fleet` field (data plumbing)

**Files:**
- Modify: `internal/sessions/sessions.go` (`SessionMeta` struct + a `SetFleet` method)
- Modify: `internal/sessions/history.go` (`ConversationFile` struct + `SetConversationFleet` + the `LoadPersistedSessions` mapping)
- Test: `internal/sessions/fleet_session_test.go`

**Interfaces:**
- Consumes: existing `AppendConversationTurn`, `LoadPersistedSessions`, the `mutateConversation` helper.
- Produces: `SessionMeta.Fleet string`, `ConversationFile.Fleet string`, `(*Registry).SetFleet(id, fleet string) bool`, `SetConversationFleet(sessionID, fleet string) error` — consumed by Tasks 3 & 5 and the server resolver.

- [ ] **Step 1: Write the failing test**

Create `internal/sessions/fleet_session_test.go` (mirrors `TestFleetExperimentFlagRoundTrip` in `history_test.go`):

```go
package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionFleetFieldRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("OMNIS_HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	const sid = "fleet-field-test"
	if err := AppendConversationTurn(sid, "hi", "hello"); err != nil {
		t.Fatalf("AppendConversationTurn: %v", err)
	}
	reloaded := func() *SessionMeta {
		for _, m := range LoadPersistedSessions() {
			if m.ID == sid {
				return m
			}
		}
		return nil
	}

	if err := SetConversationFleet(sid, "Payments"); err != nil {
		t.Fatalf("SetConversationFleet: %v", err)
	}
	meta := reloaded()
	if meta == nil {
		t.Fatalf("session %q missing after setting fleet", sid)
	}
	if meta.Fleet != "Payments" {
		t.Fatalf("Fleet = %q after set, want Payments", meta.Fleet)
	}
	if meta.Turns != 1 {
		t.Fatalf("Turns = %d, want 1 (turns must be preserved)", meta.Turns)
	}

	if err := SetConversationFleet(sid, ""); err != nil {
		t.Fatalf("SetConversationFleet(clear): %v", err)
	}
	if meta := reloaded(); meta == nil || meta.Fleet != "" {
		t.Fatalf("Fleet still set after clearing: %+v", meta)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -run TestSessionFleetFieldRoundTrip -v`
Expected: FAIL — build error `undefined: SetConversationFleet` and `meta.Fleet undefined`.

- [ ] **Step 3: Add the fields + setters**

In `internal/sessions/sessions.go`, add to the `SessionMeta` struct immediately after the `Collection string` field (~line 43):

```go
	// Fleet names the fleet a Conductor chat coordinates. Empty ⇒ the session is
	// not fleet-scoped (it sees the Ungrouped project pool). Set by the Coordinate
	// action (POST /sessions {fleet}); scopes fleet_projects/fleet_dispatch.
	Fleet string `json:"fleet,omitempty"`
```

In `internal/sessions/sessions.go`, add a `SetFleet` method right after `SetCollection` (~line 357), mirroring it:

```go
// SetFleet scopes a session to a fleet (in-memory + persisted to the conversation
// file asynchronously, mirroring SetCollection). An empty name clears the scope
// (Ungrouped). Returns true when a session was found.
func (r *Registry) SetFleet(id, fleet string) bool {
	r.mu.Lock()
	m, ok := r.items[id]
	if ok {
		m.Fleet = fleet
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	go func() {
		if err := SetConversationFleet(id, fleet); err != nil {
			log.Printf("sessions: failed to persist fleet for session %s: %v", id, err)
		}
	}()
	return true
}
```

In `internal/sessions/history.go`, add to the `ConversationFile` struct immediately after the `Collection string` field (~line 145):

```go
	// Fleet is the fleet a Conductor chat coordinates (see SessionMeta.Fleet),
	// persisted so the scope survives a restart.
	Fleet string `json:"fleet,omitempty"`
```

In `internal/sessions/history.go`, add `SetConversationFleet` right after `SetConversationCollection` (~line 433):

```go
// SetConversationFleet persists the session's fleet scope to disk without
// touching the conversation turns.
func SetConversationFleet(sessionID, fleet string) error {
	return mutateConversation(sessionID, func(f *ConversationFile) { f.Fleet = fleet })
}
```

In `internal/sessions/history.go`, in `LoadPersistedSessions`, add `Fleet: f.Fleet,` to the `SessionMeta{...}` literal immediately after `Collection: f.Collection,` (~line 508):

```go
			Collection:      f.Collection,
			Fleet:           f.Fleet,
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/sessions/ -run TestSessionFleetFieldRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Whole package + build**

Run: `go test ./internal/sessions/... && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/sessions/sessions.go internal/sessions/history.go internal/sessions/fleet_session_test.go
git commit -m "$(printf 'feat(fleet): add session fleet scope field (SessionMeta + persistence)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 2: `internal/fleet` scoping primitive

**Files:**
- Modify: `internal/fleet/fleet.go` (`Project.Fleet` field)
- Modify: `internal/fleet/tool.go` (`SetSessionFleetResolver` / `sessionFleet` / `ProjectsForSession`; `runProjects` uses it)
- Test: `internal/fleet/scope_test.go`

**Interfaces:**
- Consumes: existing `currentProjects()`, `SetProjectsResolver`.
- Produces: `Project.Fleet string`; `func SetSessionFleetResolver(f func(sessionID string) string)`; `func ProjectsForSession(sessionID string) []Project` — consumed by Task 3 (`agent` + `server`).

- [ ] **Step 1: Write the failing test**

Create `internal/fleet/scope_test.go`:

```go
package fleet

import "testing"

func TestProjectsForSessionScopesByFleet(t *testing.T) {
	all := []Project{
		{Name: "api", Engine: EngineOmnis, Fleet: "Payments"},
		{Name: "gateway", Engine: EngineClaude, Fleet: "Payments"},
		{Name: "ledger", Engine: EngineOmnis, Fleet: "Billing"},
		{Name: "legacy", Engine: EngineOmnis, Fleet: ""}, // Ungrouped
	}
	SetProjectsResolver(func() []Project { return all })
	defer SetProjectsResolver(nil)
	SetSessionFleetResolver(func(sid string) string {
		switch sid {
		case "cond-pay":
			return "Payments"
		case "cond-bill":
			return "Billing"
		}
		return "" // unknown session ⇒ no scope ⇒ Ungrouped
	})
	defer SetSessionFleetResolver(nil)

	names := func(ps []Project) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.Name)
		}
		return out
	}

	if got := names(ProjectsForSession("cond-pay")); len(got) != 2 || got[0] != "api" || got[1] != "gateway" {
		t.Fatalf("Payments scope = %v, want [api gateway]", got)
	}
	if got := names(ProjectsForSession("cond-bill")); len(got) != 1 || got[0] != "ledger" {
		t.Fatalf("Billing scope = %v, want [ledger]", got)
	}
	// No scope ⇒ Ungrouped (empty-tag projects only).
	if got := names(ProjectsForSession("someone-else")); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("unscoped session = %v, want [legacy]", got)
	}
	// With no session-fleet resolver installed at all, everything is unscoped ⇒ Ungrouped.
	SetSessionFleetResolver(nil)
	if got := names(ProjectsForSession("cond-pay")); len(got) != 1 || got[0] != "legacy" {
		t.Fatalf("nil resolver = %v, want [legacy]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/fleet/ -run TestProjectsForSessionScopesByFleet -v`
Expected: FAIL — build errors `unknown field Fleet` and `undefined: SetSessionFleetResolver` / `ProjectsForSession`.

- [ ] **Step 3: Add the `Fleet` field and the scoping primitive**

In `internal/fleet/fleet.go`, add to the `Project` struct after `DependsOn`:

```go
	// Fleet is the fleet this project belongs to ("" ⇒ Ungrouped). The server sets
	// it from the collection's `fleet` tag, folding a tag that names an unknown
	// fleet to "" (so this is always empty-or-a-real-fleet). Used to scope a
	// Conductor to its fleet.
	Fleet string
```

In `internal/fleet/tool.go`, add the session→fleet resolver + the scoped enumerator. Put the resolver vars/funcs right below the existing `resolver` block (after `currentProjects`, ~line 36):

```go
var (
	sessionFleetMu sync.RWMutex
	sessionFleetFn func(sessionID string) string
)

// SetSessionFleetResolver installs the process-wide hook mapping a session id to
// the fleet it coordinates (""=unscoped ⇒ Ungrouped). The server installs one
// backed by the session registry; nil clears it (tests, CLI/TUI, no-fleet
// default). Mirrors SetProjectsResolver.
func SetSessionFleetResolver(f func(sessionID string) string) {
	sessionFleetMu.Lock()
	sessionFleetFn = f
	sessionFleetMu.Unlock()
}

func sessionFleet(sessionID string) string {
	sessionFleetMu.RLock()
	f := sessionFleetFn
	sessionFleetMu.RUnlock()
	if f == nil {
		return ""
	}
	return strings.TrimSpace(f(sessionID))
}

// ProjectsForSession returns the projects visible to a session: those whose Fleet
// matches the session's fleet scope. An unscoped session ("" fleet) sees the
// Ungrouped pool (projects with an empty Fleet). This is what confines a Conductor
// to its own fleet.
func ProjectsForSession(sessionID string) []Project {
	return projectsForFleet(sessionFleet(sessionID))
}

func projectsForFleet(fleetName string) []Project {
	fleetName = strings.TrimSpace(fleetName)
	var out []Project
	for _, p := range currentProjects() {
		if strings.EqualFold(strings.TrimSpace(p.Fleet), fleetName) {
			out = append(out, p)
		}
	}
	return out
}
```

Add `"strings"` to the `internal/fleet/tool.go` imports (the block currently imports `fmt`, `sync`, the adk tool packages, and `core/adk`):

```go
import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)
```

Change `runProjects` to scope by the session and to surface the fleet in each row. Replace the current body's first two statements and the `projectView` append:

```go
// runProjects is the handler, extracted so tests can call it without ADK plumbing.
func runProjects(ctx adk.ToolContext, _ projectsIn) (projectsOut, error) {
	projects := ProjectsForSession(ctx.SessionID())
	out := projectsOut{Valid: true, Projects: []projectView{}}
	for _, p := range projects {
		out.Projects = append(out.Projects, projectView{
			Name: p.Name, Cwd: p.Cwd, Engine: string(p.Engine), DependsOn: p.DependsOn,
		})
	}
	if err := Validate(projects); err != nil {
		out.Valid = false
		out.Problems = append(out.Problems, err.Error())
	}
	if err := ValidateWorkspaces(projects); err != nil {
		out.Valid = false
		out.Problems = append(out.Problems, err.Error())
	}
	if order, err := TopoOrder(projects); err == nil {
		out.Order = order
	}
	return out, nil
}
```

(The `_ adk.ToolContext` parameter becomes `ctx adk.ToolContext`. No other change to `Tools()`.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/fleet/ -run TestProjectsForSessionScopesByFleet -v`
Expected: PASS.

- [ ] **Step 5: Whole package + vet**

Run: `go vet ./internal/fleet/ && go test ./internal/fleet/...`
Expected: PASS (existing fleet tests — `TestFleetProjectsTool*` etc. — still green; they set no session-fleet resolver, so their projects, which have `Fleet==""`, resolve as Ungrouped and remain visible to an unscoped call).

- [ ] **Step 6: Commit**

```bash
git add internal/fleet/fleet.go internal/fleet/tool.go internal/fleet/scope_test.go
git commit -m "$(printf 'feat(fleet): scope fleet_projects to the session fleet (ProjectsForSession)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 3: wire scoping into the consumers (dispatch tool + server resolver + mapping)

**Files:**
- Modify: `agent/fleet_dispatch.go` (`runFleetDispatch` uses `fleet.ProjectsForSession`)
- Modify: `server/fleet.go` (`collectFleetProjects` maps `p.Fleet`, folding unknown→""; `installFleetSessionResolver`)
- Modify: `server/main.go` (call `installFleetSessionResolver(registry)`)
- Test: `agent/fleet_dispatch_test.go` (extend the existing tests for the scoped path); `server/fleet_test.go` (the mapping + fold)

**Interfaces:**
- Consumes: Task 1's `SessionMeta.Fleet`; Task 2's `fleet.ProjectsForSession`, `fleet.SetSessionFleetResolver`, `Project.Fleet`; existing `sessions.FleetExists`, `sessions.CollectionProfileFull`, `d.Registry`/`registry`.
- Produces: no new exported symbols — this task makes the scope actually bind end to end.

- [ ] **Step 1: Write the failing test (agent side)**

Add to `agent/fleet_dispatch_test.go` a test that a dispatch is scoped to the session's fleet. It installs both resolvers and asserts a dispatch to an out-of-fleet project is rejected as unknown:

```go
func TestRunFleetDispatchScopedToSessionFleet(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{
			{Name: "api", Engine: fleet.EngineOmnis, Fleet: "Payments"},
			{Name: "ledger", Engine: fleet.EngineOmnis, Fleet: "Billing"},
		}
	})
	defer fleet.SetProjectsResolver(nil)
	fleet.SetSessionFleetResolver(func(sid string) string {
		if sid == "cond-pay" {
			return "Payments"
		}
		return ""
	})
	defer fleet.SetSessionFleetResolver(nil)

	reg := NewFleetDispatchRegistry()
	// In-fleet dispatch succeeds and enqueues.
	if _, err := runFleetDispatch(reg, "cond-pay", fleetDispatchIn{Project: "api", Task: "do x"}); err != nil {
		t.Fatalf("in-fleet dispatch errored: %v", err)
	}
	if got := reg.Drain("cond-pay"); len(got) != 1 || got[0].Project != "api" {
		t.Fatalf("in-fleet dispatch enqueued %v, want [api]", got)
	}
	// Out-of-fleet project is invisible ⇒ rejected as unknown, nothing enqueued.
	if _, err := runFleetDispatch(reg, "cond-pay", fleetDispatchIn{Project: "ledger", Task: "do y"}); err == nil {
		t.Fatalf("out-of-fleet dispatch to ledger should be rejected")
	}
	if got := reg.Drain("cond-pay"); len(got) != 0 {
		t.Fatalf("out-of-fleet dispatch enqueued %v, want none", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./agent/ -run TestRunFleetDispatchScopedToSessionFleet -v`
Expected: FAIL — `runFleetDispatch` still calls `fleet.Projects()` (global), so a dispatch to `ledger` succeeds instead of being rejected.

- [ ] **Step 3: Scope `runFleetDispatch`**

In `agent/fleet_dispatch.go`, in `runFleetDispatch`, change the project enumeration from the global pool to the session-scoped pool. Replace:

```go
	projects := fleet.Projects()
```

with:

```go
	projects := fleet.ProjectsForSession(sessionID)
```

(Everything else in `runFleetDispatch` is unchanged — the "unknown fleet project" error message now naturally lists only in-fleet projects.)

- [ ] **Step 4: Run the agent test to verify it passes**

Run: `go test ./agent/ -run 'TestRunFleetDispatch' -v`
Expected: PASS (the new scoped test and the pre-existing `TestFleetDispatchTool*` tests — the latter install no session-fleet resolver, so their `Fleet:""` projects resolve as Ungrouped and stay visible).

- [ ] **Step 5: Write the failing test (server mapping)**

Add to `server/fleet_test.go` a test that `collectFleetProjects` maps the `fleet` tag and folds an unknown tag to `""`:

```go
func TestCollectFleetProjectsMapsFleetTagAndFolds(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddFleet("Payments", sessions.FleetMetaData{}); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	if _, _, err := sessions.AddCollection("api"); err != nil {
		t.Fatalf("AddCollection api: %v", err)
	}
	if _, _, err := sessions.AddCollection("orphan"); err != nil {
		t.Fatalf("AddCollection orphan: %v", err)
	}
	if err := sessions.AssignProject("Payments", "api"); err != nil {
		t.Fatalf("AssignProject: %v", err)
	}
	// A project tagged to a fleet that does not exist must fold to "" (Ungrouped).
	if err := sessions.UpdateCollectionProfile("orphan", func(p *sessions.CollectionProfileData) {
		p.Role = "project"
		p.Engine = "omnis"
		p.Fleet = "GhostFleet"
	}); err != nil {
		t.Fatalf("tag orphan: %v", err)
	}

	got := map[string]string{}
	for _, p := range collectFleetProjects() {
		got[p.Name] = p.Fleet
	}
	if got["api"] != "Payments" {
		t.Fatalf("api fleet = %q, want Payments", got["api"])
	}
	if got["orphan"] != "" {
		t.Fatalf("orphan fleet = %q, want \"\" (unknown fleet folds to Ungrouped)", got["orphan"])
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./server/ -run TestCollectFleetProjectsMapsFleetTagAndFolds -v`
Expected: FAIL — `collectFleetProjects` doesn't set `Fleet` yet, so `got["api"]` is `""`.

- [ ] **Step 7: Map the tag (with fold) + install the resolver**

In `server/fleet.go` `collectFleetProjects`, set `Fleet` on the mapped project, folding an unknown tag to `""`. Replace the append with:

```go
	for _, name := range names {
		p := sessions.CollectionProfileFull(name)
		if p.Role != fleet.RoleProject {
			continue
		}
		fleetTag := strings.TrimSpace(p.Fleet)
		if fleetTag != "" && !sessions.FleetExists(fleetTag) {
			fleetTag = "" // unknown fleet ⇒ Ungrouped (self-healing, mirrors FleetMembers)
		}
		out = append(out, fleet.Project{
			Name:      name,
			Cwd:       p.Cwd,
			Engine:    fleet.Engine(p.Engine),
			DependsOn: p.DependsOn,
			Fleet:     fleetTag,
		})
	}
```

Add `installFleetSessionResolver` to `server/fleet.go` (below `installFleetResolver`):

```go
// installFleetSessionResolver wires the process-wide session→fleet hook to the
// live session registry: a Conductor chat's fleet scope is its SessionMeta.Fleet.
// Called once at server startup beside installFleetResolver. CLI/TUI never call
// it, so every session is unscoped (Ungrouped) there — the no-op default.
func installFleetSessionResolver(reg *sessions.Registry) {
	fleet.SetSessionFleetResolver(func(sessionID string) string {
		if reg == nil {
			return ""
		}
		m, ok := reg.Get(sessionID)
		if !ok || m == nil {
			return ""
		}
		return strings.TrimSpace(m.Fleet)
	})
}
```

In `server/main.go`, right after the existing `installFleetResolver()` call, add:

```go
	installFleetSessionResolver(registry)
```

(`registry` is the same `*sessions.Registry` variable already passed to `installClaudeAllowlistResolver(registry)` a few lines below — confirm it is in scope at the `installFleetResolver()` call site; if `installClaudeAllowlistResolver(registry)` compiles there, so does this.)

- [ ] **Step 8: Run the server test + build + the agent tests**

Run: `go test ./server/ -run TestCollectFleetProjects -v && go test ./agent/ -run TestRunFleetDispatch && go build ./...`
Expected: PASS; build clean.

- [ ] **Step 9: Commit**

```bash
git add agent/fleet_dispatch.go agent/fleet_dispatch_test.go server/fleet.go server/main.go server/fleet_test.go
git commit -m "$(printf 'feat(fleet): bind fleet scope end-to-end (dispatch + server resolver + tag mapping)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 4: `/api/fleets` CRUD + assign/unassign routes

**Files:**
- Create: `server/fleets.go`
- Modify: `server/server.go` (register the routes next to `/api/collections`)
- Test: `server/fleets_route_test.go`

**Interfaces:**
- Consumes: Plan 1's `sessions.ListFleets/AddFleet/RenameFleet/RemoveFleet/UpdateFleetMeta/FleetMetaFor/FleetMembers/AssignProject/UnassignProject/FleetExists/UngroupedFleet/ValidFleetName/ValidDefaultEngine/FleetMetaData`, `sessions.CollectionProfileFull`, `sessions.ValidCollectionColor`; `serverDeps.PushEvents.broadcast`.
- Produces: the `/api/fleets` HTTP surface consumed by Plan 3's web UI.

- [ ] **Step 1: Write the failing test**

Create `server/fleets_route_test.go` (drives the real router; mirror `server/collections`/`session_list` route tests — use the package's existing `newTestRouter`/`newTestServerDeps` helper if present, else construct `serverDeps` the way `collections`-route tests do):

```go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

func TestFleetsRoutesCRUDAndAssign(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	r, d := newFleetsTestRouter(t) // helper below wires just the /api/fleets routes on a serverDeps
	_ = d

	do := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Create a fleet.
	if w := do(http.MethodPost, "/api/fleets", `{"name":"Payments","color":"blue","default_engine":"omnis"}`); w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create fleet: %d %s", w.Code, w.Body.String())
	}
	// A project collection assigned to it.
	if _, _, err := sessions.AddCollection("api"); err != nil {
		t.Fatalf("AddCollection: %v", err)
	}
	if w := do(http.MethodPost, "/api/fleets/Payments/projects", `{"collection":"api"}`); w.Code != http.StatusOK {
		t.Fatalf("assign: %d %s", w.Code, w.Body.String())
	}

	// GET lists it with a derived member.
	w := do(http.MethodGet, "/api/fleets", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var fleets []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &fleets); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, w.Body.String())
	}
	var pay map[string]any
	for _, f := range fleets {
		if f["name"] == "Payments" {
			pay = f
		}
	}
	if pay == nil {
		t.Fatalf("Payments not listed: %s", w.Body.String())
	}
	if members, _ := pay["members"].([]any); len(members) != 1 {
		t.Fatalf("Payments members = %v, want 1", pay["members"])
	}
	// assign promoted api to a project ⇒ it must carry role=project + engine.
	if p := sessions.CollectionProfileFull("api"); p.Role != "project" || p.Engine != "omnis" || p.Fleet != "Payments" {
		t.Fatalf("assigned api profile = %+v", p)
	}

	// Rename via PATCH.
	if w := do(http.MethodPatch, "/api/fleets/Payments", `{"name":"Billing"}`); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	if !sessions.FleetExists("Billing") || sessions.FleetExists("Payments") {
		t.Fatalf("rename didn't take")
	}
	if sessions.CollectionProfileFull("api").Fleet != "Billing" {
		t.Fatalf("rename didn't cascade member tag")
	}

	// Invalid default engine is rejected without mutating.
	if w := do(http.MethodPatch, "/api/fleets/Billing", `{"default_engine":"gpt"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad engine should 400, got %d", w.Code)
	}

	// Unassign returns api to Ungrouped (still a project).
	if w := do(http.MethodDelete, "/api/fleets/Billing/projects/api", ""); w.Code != http.StatusOK {
		t.Fatalf("unassign: %d %s", w.Code, w.Body.String())
	}
	if p := sessions.CollectionProfileFull("api"); p.Fleet != "" || p.Role != "project" {
		t.Fatalf("after unassign api = %+v (want fleet='' role=project)", p)
	}

	// Delete the fleet.
	if w := do(http.MethodDelete, "/api/fleets/Billing", ""); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if sessions.FleetExists("Billing") {
		t.Fatalf("fleet still present after delete")
	}
}
```

Add the test router helper at the bottom of `server/fleets_route_test.go`. **Before writing it, look at an existing `server/*_route_test.go` (e.g. `session_list_test.go` or `collections_ctx_test.go`) to see how that file builds a `serverDeps` + gin engine, and copy that construction** — the helper must register the fleet routes on an `auth`-style group with a no-op `PushEvents` (or the real `pushEvents` the other tests use):

```go
func newFleetsTestRouter(t *testing.T) (*gin.Engine, serverDeps) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	d := serverDeps{PushEvents: newTestPushEvents()} // reuse the same stub other route tests use
	g := r.Group("/api")
	registerFleetRoutes(g, d) // the exported-in-package registrar added in Step 3
	return r, d
}
```

> If the existing route tests use a different construction (a shared `setupTestServer`), use that instead and drop this helper — the point is only that the diff exercises the real handlers through gin. Match the file you copy from.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run TestFleetsRoutesCRUDAndAssign -v`
Expected: FAIL — build error `undefined: registerFleetRoutes` / handlers.

- [ ] **Step 3: Create `server/fleets.go`**

```go
// fleets.go — HTTP surface for named fleets: CRUD over the fleets.json registry
// (internal/sessions) plus assign/unassign a project (which writes the
// collection's `fleet` tag). Membership is derived (FleetMembers); this file adds
// no persistence of its own beyond the sessions registry functions. Mirrors
// server/collections.go.
package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

type fleetMemberView struct {
	Name      string   `json:"name"`
	Engine    string   `json:"engine,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type fleetView struct {
	Name          string            `json:"name"`
	Color         string            `json:"color,omitempty"`
	Description   string            `json:"description,omitempty"`
	DefaultEngine string            `json:"default_engine,omitempty"`
	ProjectCount  int               `json:"project_count"`
	Engines       []string          `json:"engines,omitempty"`
	Members       []fleetMemberView `json:"members"`
	Ungrouped     bool              `json:"ungrouped,omitempty"`
}

// buildFleetView assembles one fleet's row: metadata + derived members (each with
// its engine + depends_on read from the collection profile) + the distinct engine
// set. Ungrouped carries no metadata.
func buildFleetView(name string, ungrouped bool) fleetView {
	members := sessions.FleetMembers(name)
	v := fleetView{Name: name, ProjectCount: len(members), Members: []fleetMemberView{}, Ungrouped: ungrouped}
	if !ungrouped {
		m := sessions.FleetMetaFor(name)
		v.Color, v.Description, v.DefaultEngine = m.Color, m.Description, m.DefaultEngine
	}
	seenEngine := map[string]bool{}
	for _, mem := range members {
		p := sessions.CollectionProfileFull(mem)
		v.Members = append(v.Members, fleetMemberView{Name: mem, Engine: p.Engine, DependsOn: p.DependsOn})
		if p.Engine != "" && !seenEngine[p.Engine] {
			seenEngine[p.Engine] = true
			v.Engines = append(v.Engines, p.Engine)
		}
	}
	return v
}

func handleListFleets(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		names, err := sessions.ListFleets()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := []fleetView{}
		for _, n := range names {
			out = append(out, buildFleetView(n, false))
		}
		// Append Ungrouped only when it has members.
		if ung := buildFleetView(sessions.UngroupedFleet, true); ung.ProjectCount > 0 {
			out = append(out, ung)
		}
		c.JSON(http.StatusOK, out)
	}
}

func handleCreateFleet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Name          string `json:"name"`
			Color         string `json:"color"`
			Description   string `json:"description"`
			DefaultEngine string `json:"default_engine"`
		}
		_ = c.ShouldBindJSON(&body)
		name := strings.TrimSpace(body.Name)
		if !sessions.ValidFleetName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fleet name"})
			return
		}
		if !sessions.ValidCollectionColor(body.Color) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid colour"})
			return
		}
		if !sessions.ValidDefaultEngine(body.DefaultEngine) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid default engine"})
			return
		}
		if _, _, err := sessions.AddFleet(name, sessions.FleetMetaData{
			Color: body.Color, Description: body.Description, DefaultEngine: body.DefaultEngine,
		}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(name, false))
	}
}

func handleUpdateFleet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		var body struct {
			Name          *string `json:"name"`
			Color         *string `json:"color"`
			Description   *string `json:"description"`
			DefaultEngine *string `json:"default_engine"`
		}
		_ = c.ShouldBindJSON(&body)
		// Validate everything BEFORE writing anything (atomic-on-rejection).
		if body.Name != nil && !sessions.ValidFleetName(strings.TrimSpace(*body.Name)) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid fleet name"})
			return
		}
		if body.Color != nil && !sessions.ValidCollectionColor(*body.Color) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid colour"})
			return
		}
		if body.DefaultEngine != nil && !sessions.ValidDefaultEngine(*body.DefaultEngine) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid default engine"})
			return
		}
		final := name
		if body.Name != nil {
			nn := strings.TrimSpace(*body.Name)
			if !strings.EqualFold(nn, name) {
				if _, _, err := sessions.RenameFleet(name, nn); err != nil {
					c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
					return
				}
			}
			final = nn
		}
		if body.Color != nil || body.Description != nil || body.DefaultEngine != nil {
			if err := sessions.UpdateFleetMeta(final, func(m *sessions.FleetMetaData) {
				if body.Color != nil {
					m.Color = *body.Color
				}
				if body.Description != nil {
					m.Description = *body.Description
				}
				if body.DefaultEngine != nil {
					m.DefaultEngine = *body.DefaultEngine
				}
			}); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(final, false))
	}
}

func handleDeleteFleet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		if _, _, err := sessions.RemoveFleet(name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			// Members returned to Ungrouped changed a collection's role/visibility.
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

func handleAssignProject(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		var body struct {
			Collection string `json:"collection"`
		}
		_ = c.ShouldBindJSON(&body)
		if err := sessions.AssignProject(name, strings.TrimSpace(body.Collection)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(name, false))
	}
}

func handleUnassignProject(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		collection := strings.TrimSpace(c.Param("collection"))
		if err := sessions.UnassignProject(collection); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("fleets_changed", "")
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, buildFleetView(name, false))
	}
}

// registerFleetRoutes mounts the /api/fleets surface on the given (auth) group.
func registerFleetRoutes(g *gin.RouterGroup, d serverDeps) {
	g.GET("/fleets", handleListFleets(d))
	g.POST("/fleets", handleCreateFleet(d))
	g.PATCH("/fleets/:name", handleUpdateFleet(d))
	g.DELETE("/fleets/:name", handleDeleteFleet(d))
	g.POST("/fleets/:name/projects", handleAssignProject(d))
	g.DELETE("/fleets/:name/projects/:collection", handleUnassignProject(d))
}
```

- [ ] **Step 4: Register the routes in `server/server.go`**

Immediately after the `/api/collections` route block (after line 712, the `handleRevertCollectionMemory` line), add:

```go
	// Fleets — named groups of project-collections (see server/fleets.go).
	registerFleetRoutes(auth, d)
```

(`auth` is the `*gin.RouterGroup` the collection routes are registered on.)

- [ ] **Step 5: Run the route test + build**

Run: `go test ./server/ -run TestFleetsRoutesCRUDAndAssign -v && go build ./...`
Expected: PASS; build clean. If the test's router-construction helper doesn't match the package's existing pattern, fix the helper to match a working `server/*_route_test.go` (do NOT change the handlers to fit a broken helper).

- [ ] **Step 6: Commit**

```bash
git add server/fleets.go server/server.go server/fleets_route_test.go
git commit -m "$(printf 'feat(fleet): /api/fleets CRUD + project assign/unassign routes\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 5: Coordinate — scope a new chat to a fleet on creation

**Files:**
- Modify: `server/server.go` (the `POST /sessions` handler — accept + persist a `fleet`)
- Test: `server/newsession_fleet_test.go`

**Interfaces:**
- Consumes: Task 1's `d.Registry.SetFleet` + `sessions.SetConversationFleet`; `sessions.FleetExists`.
- Produces: the "Coordinate ▸" entry point — `POST /sessions {squad:"Fleet", fleet:"Payments"}` opens a fleet-scoped Conductor chat.

- [ ] **Step 1: Write the failing test**

Create `server/newsession_fleet_test.go`. **Model its server/router construction on `server/newsession_cwd_test.go`** (same package, same `POST /sessions` handler), changing only the assertion to the fleet field:

```go
package main

// See newsession_cwd_test.go for the router/serverDeps construction this mirrors.
// This test asserts POST /sessions {fleet} scopes the created session when the
// fleet exists, and ignores an unknown fleet.
```

Write the test to: create a fleet (`sessions.AddFleet("Payments", …)`), `POST /api/sessions` with `{"squad":"<a real squad>","fleet":"Payments"}`, then assert the created session's persisted `Fleet == "Payments"` (via `sessions.LoadPersistedSessions()` or `d.Registry.Get`), and a second POST with `{"fleet":"GhostFleet"}` leaves `Fleet == ""`. Copy the exact `serverDeps`/router/squad setup from `newsession_cwd_test.go` (it already stands up a `POST /sessions` route with a Manager whose `HasSquad` accepts the squad it uses) and add the fleet assertion.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./server/ -run 'NewSessionFleet' -v`
Expected: FAIL — the handler ignores `fleet`, so the created session's `Fleet` is `""`.

- [ ] **Step 3: Accept + persist the fleet in `POST /sessions`**

In `server/server.go`, add a `Fleet` field to the `POST /sessions` body struct (after `Collection string`):

```go
			Collection string `json:"collection"`
			Fleet      string `json:"fleet"`
```

Then, immediately after the collection filing block (after the `if collection != "" { d.Registry.SetCollection(...) }` block, ~line 383), add:

```go
			// Scope a Conductor chat to a fleet (the "Coordinate" action). Only an
			// existing fleet is honoured; an unknown name is ignored (⇒ Ungrouped),
			// mirroring the collection-fold behaviour.
			if fl := strings.TrimSpace(body.Fleet); fl != "" && sessions.FleetExists(fl) {
				d.Registry.SetFleet(meta.ID, fl)
				_ = sessions.SetConversationFleet(meta.ID, fl)
			}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./server/ -run 'NewSessionFleet' -v`
Expected: PASS.

- [ ] **Step 5: Full server package + vet + build**

Run: `go vet ./server/ && go test ./server/... && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/server.go server/newsession_fleet_test.go
git commit -m "$(printf 'feat(fleet): POST /sessions accepts a fleet scope (Coordinate action)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Self-Review

**Spec coverage (Plan 2 slice):**
- Session `fleet` field (SessionMeta + ConversationFile + persistence + LoadPersistedSessions) → Task 1. ✓
- `internal/fleet` scoping (Project.Fleet + resolver hook + `ProjectsForSession` + scoped `fleet_projects`) → Task 2. ✓
- Scoped `fleet_dispatch` + server tag-mapping-with-fold + resolver install → Task 3. ✓
- `/api/fleets` CRUD + assign/unassign + `fleets_changed`/`collections_changed` → Task 4. ✓
- Coordinate (`POST /sessions {fleet}`) → Task 5. ✓
- Degenerate/no-op (empty scope ⇒ Ungrouped ⇒ today's behaviour) → Task 2 test (nil resolver) + Task 3 (existing tests still green). ✓

Deferred to Plan 3 (correctly out of scope): all web UI — the Fleets sidebar section, fleet CRUD dialogs, the guarded project behaviour, `fleets_changed` client handling, i18n.

**Placeholder scan:** the only non-verbatim step is Task 4/Task 5's test *router construction*, which is explicitly delegated to "copy the existing `server/*_route_test.go` / `newsession_cwd_test.go` pattern" because that harness already exists in the package and must be matched, not reinvented — the handler code and assertions are complete. Everything else is complete code.

**Type consistency:** `Project.Fleet` (Task 2) is read by `collectFleetProjects` (Task 3) and set by `ProjectsForSession`'s filter; `SessionMeta.Fleet`/`SetFleet`/`SetConversationFleet` (Task 1) are used by the resolver (Task 3) and Coordinate (Task 5); `sessions.FleetMetaData{Color,Description,DefaultEngine}` and the fleet registry functions (Plan 1) are used verbatim by the routes (Task 4). `registerFleetRoutes` (Task 4) is the symbol Task 4's test and `server.go` both reference.

---

## Execution Handoff

After Plan 2 is green, Plan 3 (the web UI) is the last plan.
