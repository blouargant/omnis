# Fleet Project Registry — Implementation Plan (Phase 1 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the foundational *fleet project registry* — mark omnis collections as fleet projects (`role`/`engine`/`depends_on`), compute + validate their dependency DAG, and expose it to agents via a read-only `fleet_projects` tool — without any coordination/execution behavior yet.

**Architecture:** Extend the existing per-collection profile (`internal/sessions/collections.go`) with three fields. Add a new dependency-free `internal/fleet` package holding the `Project` type, pure DAG/topological-order/validation logic, a process-wide **resolver hook** (`SetProjectsResolver`), and the `fleet_projects` tool. Because `agent → internal/sessions` is an import cycle (`internal/sessions/history.go` imports `agent`), the tool must **not** read collections directly; instead the **server** installs the resolver (backed by `sessions`), exactly like the existing `agent.SetCollectionResolver` / `fstools.SetCwdResolver` idiom. Agents opt in via a new `"fleet"` tool-group key.

**Tech Stack:** Go; ADK (`google.golang.org/adk/tool`, `.../tool/functiontool`); the `core/adk` façade (ADK-v2 guard — use `adk.ToolContext`, never `tool.Context`); standard `go test`.

## Global Constraints

- **Language-agnostic.** This phase encodes no Go/gRPC/build-tool knowledge — only a project registry + dependency graph. (Copied from spec §1.)
- **No-op contract.** A collection **without** `role:"project"` must behave byte-identically to today; a build with no fleet projects declared adds no behavior. (Spec §15.)
- **Import-cycle rule.** `internal/fleet` may import only stdlib + ADK + `core/adk`. It must **not** import `internal/sessions` or `agent`. Collection data reaches it only through the installed resolver. (Spec §11; verified: `internal/sessions/history.go:13` imports `agent`.)
- **ADK-v2 façade.** Tool handlers take `adk.ToolContext` (from `github.com/blouargant/omnis/core/adk`), not `tool.Context`. The `internal/adkguard` test fails the build otherwise. (CLAUDE.md ADK-v2 section.)
- **English only** for any user-facing strings/docs.
- **Engine values** are exactly `"omnis"` and `"claude"`. (Spec §6.)

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/sessions/collections.go` (modify) | Add `Role`/`Engine`/`DependsOn` to the profile structs; fix struct-comparability; thread fields through the 3 mapping sites. | 1 |
| `internal/sessions/collections_profile_test.go` (modify) | Round-trip test for the new fields + drop-when-empty. | 1 |
| `internal/fleet/fleet.go` (create) | `Project` type, `Engine` consts, pure `TopoOrder` + `Validate` DAG logic. | 2 |
| `internal/fleet/fleet_test.go` (create) | Unit tests: topo chain, parallel branches, cycle, unknown edge, bad engine. | 2 |
| `internal/fleet/workspace.go` (create) | `IsGitRepo` + `ValidateWorkspaces` (filesystem checks, split from pure logic). | 3 |
| `internal/fleet/tool.go` (create) | Resolver hook (`SetProjectsResolver`) + the `fleet_projects` tool + `Tools()`. | 3 |
| `internal/fleet/tool_test.go` (create) | Tool handler test with a fake resolver (valid + cyclic). | 3 |
| `internal/fleet/workspace_test.go` (create) | `IsGitRepo` / `ValidateWorkspaces` on temp dirs. | 3 |
| `agent/agent.go` (modify) | Add `case "fleet":` to the tool-group switch + import. | 4 |
| `server/fleet.go` (create) | `installFleetResolver()` mapping `sessions` project-collections → `fleet.Project`. | 4 |
| `server/fleet_test.go` (create) | Seed collections, assert the resolver returns the right projects. | 4 |

---

### Task 1: Extend the collection profile with fleet fields

**Files:**
- Modify: `internal/sessions/collections.go` (structs at lines 51-74; mapping sites at ~90-94, ~116-137, ~156-172)
- Test: `internal/sessions/collections_profile_test.go`

**Interfaces:**
- Consumes: nothing (leaf change).
- Produces: `sessions.CollectionProfileData` gains exported fields `Role string`, `Engine string`, `DependsOn []string`; `sessions.CollectionProfileFull(name) CollectionProfileData` and `sessions.SetCollectionProfileData(name, CollectionProfileData) error` round-trip them.

**Background:** `collectionProfile` (persisted) is currently compared with `p == (collectionProfile{})` to decide whether to drop an empty entry. Adding a **slice** field (`DependsOn`) makes the struct **non-comparable** — that `==` will no longer compile. This task replaces those two comparisons with an `isEmpty()` method.

- [ ] **Step 1: Write the failing test**

Add to `internal/sessions/collections_profile_test.go`:

```go
func TestCollectionProfileFleetFields(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := AddCollection("Service B"); err != nil {
		t.Fatal(err)
	}
	in := CollectionProfileData{
		Cwd:       "/repos/service-b",
		Engine:    "claude",
		Role:      "project",
		DependsOn: []string{"Service A"},
	}
	if err := SetCollectionProfileData("Service B", in); err != nil {
		t.Fatal(err)
	}
	got := CollectionProfileFull("service b") // case-insensitive
	if got.Role != "project" || got.Engine != "claude" || got.Cwd != "/repos/service-b" {
		t.Fatalf("scalars not round-tripped: %+v", got)
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != "Service A" {
		t.Fatalf("depends_on not round-tripped: %+v", got.DependsOn)
	}
	// All-empty drops the entry.
	if err := SetCollectionProfileData("Service B", CollectionProfileData{}); err != nil {
		t.Fatal(err)
	}
	if got := CollectionProfileFull("Service B"); got.Role != "" || len(got.DependsOn) != 0 {
		t.Fatalf("profile not cleared: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sessions/ -run TestCollectionProfileFleetFields -v`
Expected: FAIL to **compile** — `CollectionProfileData` has no field `Engine`/`Role`/`DependsOn`.

- [ ] **Step 3: Add the fields to both structs**

In `internal/sessions/collections.go`, extend `collectionProfile` (add after `LastMemoryUpdate`, before the closing brace at line 65):

```go
	// Role marks this collection as a fleet project ("project") vs a plain
	// collection (""). Fleet fields are inert unless Role == "project".
	Role string `json:"role,omitempty"`
	// Engine selects the fleet worker backing this project: "omnis" | "claude".
	Engine string `json:"engine,omitempty"`
	// DependsOn lists the collection names this project depends on (the
	// cross-project dependency edges used to order fleet work).
	DependsOn []string `json:"depends_on,omitempty"`
```

Extend `CollectionProfileData` (add after `LastMemoryUpdate int64` at line 73):

```go
	Role       string
	Engine     string
	DependsOn  []string
```

- [ ] **Step 4: Add an `isEmpty` method and replace the two `==` comparisons**

Add this method just after the `collectionProfile` struct (after line 65):

```go
// isEmpty reports whether the profile carries no data. Used instead of `==`
// because DependsOn ([]string) makes collectionProfile non-comparable.
func (p collectionProfile) isEmpty() bool {
	return p.Squad == "" && p.Cwd == "" && p.MemorySize == "" &&
		!p.AutoUpdate && p.LastMemoryUpdate == 0 &&
		p.Role == "" && p.Engine == "" && len(p.DependsOn) == 0
}
```

Replace **both** occurrences of `if p == (collectionProfile{}) {` (in `UpdateCollectionProfile` ~line 128 and `SetCollectionProfileData` ~line 163) with:

```go
	if p.isEmpty() {
```

- [ ] **Step 5: Thread the fields through the three mapping sites**

(a) `CollectionProfileFull` (~line 90) — add the new fields to the returned struct:

```go
	return CollectionProfileData{
		Squad: p.Squad, Cwd: p.Cwd, MemorySize: p.MemorySize,
		AutoUpdate: p.AutoUpdate, LastMemoryUpdate: p.LastMemoryUpdate,
		Role: p.Role, Engine: p.Engine, DependsOn: cloneStrings(p.DependsOn),
	}
```

(b) `UpdateCollectionProfile` — extend the `d := CollectionProfileData{...}` snapshot (~line 116) with `Role: cur.Role, Engine: cur.Engine, DependsOn: cloneStrings(cur.DependsOn),` and extend the `p := collectionProfile{...}` build (~line 121) with:

```go
		Role:      strings.TrimSpace(d.Role),
		Engine:    strings.TrimSpace(d.Engine),
		DependsOn: cleanStrings(d.DependsOn),
```

(c) `SetCollectionProfileData` — extend its `p := collectionProfile{...}` build (~line 156) with the same three lines as (b).

Add these helpers at the end of the file:

```go
// cloneStrings returns a copy so callers can't mutate stored slices.
func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// cleanStrings trims each entry and drops blanks (keeps order, dedups).
func cleanStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/sessions/ -run TestCollectionProfileFleetFields -v`
Expected: PASS.

- [ ] **Step 7: Run the full sessions package tests (guard against regressions in the `==`→`isEmpty` change)**

Run: `go test ./internal/sessions/...`
Expected: PASS (including the pre-existing `TestCollectionProfilePreservesFields` / `...ConcurrentFieldUpdates`).

- [ ] **Step 8: Commit**

```bash
git add internal/sessions/collections.go internal/sessions/collections_profile_test.go
git commit -m "feat(fleet): add role/engine/depends_on to collection profile"
```

---

### Task 2: `internal/fleet` — Project type + pure DAG logic

**Files:**
- Create: `internal/fleet/fleet.go`
- Test: `internal/fleet/fleet_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Engine string`; consts `EngineOmnis Engine = "omnis"`, `EngineClaude Engine = "claude"`.
  - `type Project struct { Name string; Cwd string; Engine Engine; DependsOn []string }`
  - `func TopoOrder(projects []Project) ([]string, error)` — dependency-first order; error on cycle.
  - `func Validate(projects []Project) error` — aggregated: unknown edges, bad engine, cycle.

- [ ] **Step 1: Write the failing tests**

Create `internal/fleet/fleet_test.go`:

```go
package fleet

import (
	"strings"
	"testing"
)

func p(name string, deps ...string) Project {
	return Project{Name: name, Cwd: "/x/" + name, Engine: EngineOmnis, DependsOn: deps}
}

func TestTopoOrderChain(t *testing.T) {
	// c depends on b depends on a  =>  a, b, c
	order, err := TopoOrder([]Project{p("c", "b"), p("a"), p("b", "a")})
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, n := range order {
		idx[n] = i
	}
	if !(idx["a"] < idx["b"] && idx["b"] < idx["c"]) {
		t.Fatalf("bad order: %v", order)
	}
}

func TestTopoOrderCycle(t *testing.T) {
	_, err := TopoOrder([]Project{p("a", "b"), p("b", "a")})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidateUnknownEdge(t *testing.T) {
	err := Validate([]Project{p("a", "ghost")})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected unknown-edge error, got %v", err)
	}
}

func TestValidateBadEngine(t *testing.T) {
	bad := Project{Name: "a", Cwd: "/x/a", Engine: "python"}
	err := Validate([]Project{bad})
	if err == nil || !strings.Contains(err.Error(), "engine") {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate([]Project{p("a"), p("b", "a")}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateAggregatesMultipleProblems(t *testing.T) {
	projects := []Project{
		{Name: "", Cwd: "/x", Engine: EngineOmnis},
		{Name: "b", Cwd: "/x/b", Engine: "python", DependsOn: []string{"ghost"}},
	}
	err := Validate(projects)
	if err == nil {
		t.Fatal("expected aggregated errors")
	}
	msg := err.Error()
	for _, want := range []string{"blank name", "engine", "ghost"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("want %q in aggregated error, got %q", want, msg)
		}
	}
}

func TestTopoOrderDanglingEdgeIsNotCycle(t *testing.T) {
	order, err := TopoOrder([]Project{p("a", "ghost")})
	if err != nil {
		t.Fatalf("dangling edge must not be reported as a cycle: %v", err)
	}
	if len(order) != 1 || order[0] != "a" {
		t.Fatalf("expected [a], got %v", order)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/fleet/ -v`
Expected: FAIL — package `fleet` has no source (undefined `Project`, `TopoOrder`, …).

- [ ] **Step 3: Write the implementation**

Create `internal/fleet/fleet.go`:

```go
// Package fleet holds the multi-project coordination registry: the Project
// type, the dependency-graph logic, and the read-only fleet_projects tool.
// It imports only stdlib + ADK + core/adk so both `agent` and `server` can
// depend on it without the agent<->sessions import cycle; collection data
// reaches it through the resolver installed via SetProjectsResolver.
package fleet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Engine selects the worker backing a project.
type Engine string

const (
	EngineOmnis  Engine = "omnis"
	EngineClaude Engine = "claude"
)

// Project is one fleet project, derived from a collection with role:"project".
type Project struct {
	Name      string
	Cwd       string
	Engine    Engine
	DependsOn []string
}

// TopoOrder returns project names in dependency-first order (a project appears
// after everything it depends on). It errors if the graph has a cycle. Edges to
// names that are not themselves projects are ignored here (Validate reports them
// as unknown dependencies), so a dangling edge is never mistaken for a cycle.
// Order is deterministic (ties broken alphabetically) so plans are reproducible.
func TopoOrder(projects []Project) ([]string, error) {
	known := make(map[string]bool, len(projects))
	for _, p := range projects {
		known[p.Name] = true
	}
	indeg := make(map[string]int, len(projects))
	adj := make(map[string][]string, len(projects))
	for _, p := range projects {
		if _, ok := indeg[p.Name]; !ok {
			indeg[p.Name] = 0
		}
	}
	for _, p := range projects {
		for _, dep := range p.DependsOn {
			if !known[dep] {
				continue // unknown dep: a Validate concern, not an ordering/cycle one
			}
			adj[dep] = append(adj[dep], p.Name) // dep must precede p
			indeg[p.Name]++
		}
	}
	var ready []string
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		var freed []string
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				freed = append(freed, m)
			}
		}
		sort.Strings(freed)
		ready = append(ready, freed...)
		sort.Strings(ready)
	}
	if len(order) != len(indeg) {
		return nil, fmt.Errorf("dependency cycle among fleet projects (ordered %d of %d)", len(order), len(indeg))
	}
	return order, nil
}

// Validate aggregates all structural problems: blank names, unknown/self edges,
// unknown engine, and cycles. Returns nil when the graph is sound. It never
// fail-fasts — a config with several mistakes reports them all at once.
func Validate(projects []Project) error {
	var problems []string
	names := make(map[string]bool, len(projects))
	for i, p := range projects {
		if strings.TrimSpace(p.Name) == "" {
			problems = append(problems, fmt.Sprintf("project #%d has a blank name", i))
			continue
		}
		names[p.Name] = true
	}
	for _, p := range projects {
		if strings.TrimSpace(p.Name) == "" {
			continue // already reported above
		}
		if p.Engine != EngineOmnis && p.Engine != EngineClaude {
			problems = append(problems, fmt.Sprintf("project %q: unknown engine %q (want omnis|claude)", p.Name, p.Engine))
		}
		for _, dep := range p.DependsOn {
			switch {
			case dep == p.Name:
				problems = append(problems, fmt.Sprintf("project %q depends on itself", p.Name))
			case !names[dep]:
				problems = append(problems, fmt.Sprintf("project %q depends on unknown project %q", p.Name, dep))
			}
		}
	}
	if _, err := TopoOrder(projects); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/fleet/ -v`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add internal/fleet/fleet.go internal/fleet/fleet_test.go
git commit -m "feat(fleet): project type + dependency DAG (topo order, validation)"
```

---

### Task 3: Workspace checks + resolver hook + `fleet_projects` tool

**Files:**
- Create: `internal/fleet/workspace.go`, `internal/fleet/tool.go`
- Test: `internal/fleet/workspace_test.go`, `internal/fleet/tool_test.go`

**Interfaces:**
- Consumes: `Project`, `Engine`, `TopoOrder`, `Validate` (Task 2).
- Produces:
  - `func IsGitRepo(dir string) bool`
  - `func ValidateWorkspaces(projects []Project) error`
  - `func SetProjectsResolver(f func() []Project)` — process-wide hook (mirrors `agent.SetCollectionResolver`).
  - `func Tools() []tool.Tool` — returns the `fleet_projects` tool.

- [ ] **Step 1: Write the failing workspace test**

Create `internal/fleet/workspace_test.go`:

```go
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
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/fleet/ -run TestIsGitRepo -v`
Expected: FAIL — `IsGitRepo` undefined.

- [ ] **Step 3: Implement the workspace checks**

Create `internal/fleet/workspace.go`:

```go
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
```

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/fleet/ -run 'TestIsGitRepo|TestValidateWorkspaces' -v`
Expected: PASS.

- [ ] **Step 5: Write the failing tool test**

Create `internal/fleet/tool_test.go`:

```go
package fleet

import (
	"context"
	"testing"

	"github.com/blouargant/omnis/core/adk"
)

func TestFleetProjectsToolValid(t *testing.T) {
	SetProjectsResolver(func() []Project {
		return []Project{
			{Name: "a", Cwd: "/x/a", Engine: EngineOmnis},
			{Name: "b", Cwd: "/x/b", Engine: EngineClaude, DependsOn: []string{"a"}},
		}
	})
	t.Cleanup(func() { SetProjectsResolver(nil) })

	out, err := runProjects(adk.ToolContext{}, projectsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(out.Projects))
	}
	if len(out.Order) != 2 || out.Order[0] != "a" || out.Order[1] != "b" {
		t.Fatalf("bad topo order: %v", out.Order)
	}
	if !out.Valid {
		t.Fatalf("graph should be valid, problems=%v", out.Problems)
	}
}

func TestFleetProjectsToolReportsCycle(t *testing.T) {
	SetProjectsResolver(func() []Project {
		return []Project{
			{Name: "a", Cwd: "/x/a", Engine: EngineOmnis, DependsOn: []string{"b"}},
			{Name: "b", Cwd: "/x/b", Engine: EngineOmnis, DependsOn: []string{"a"}},
		}
	})
	t.Cleanup(func() { SetProjectsResolver(nil) })

	out, err := runProjects(adk.ToolContext{}, projectsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Valid {
		t.Fatal("cyclic graph must report invalid")
	}
	if len(out.Problems) == 0 {
		t.Fatal("expected a problem describing the cycle")
	}
}
```

> Note: `runProjects` is the extracted handler (tested directly). `adk.ToolContext{}`
> zero value is accepted because the handler ignores the context. If `adk.ToolContext`
> is an interface rather than a struct in the pinned ADK, pass `nil` instead and change
> the handler signature test accordingly — confirm by reading `core/adk`.

- [ ] **Step 6: Run to verify it fails**

Run: `go test ./internal/fleet/ -run TestFleetProjectsTool -v`
Expected: FAIL — `SetProjectsResolver`, `runProjects`, `projectsIn` undefined.

- [ ] **Step 7: Implement the resolver hook + tool**

Create `internal/fleet/tool.go`:

```go
package fleet

import (
	"fmt"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

var (
	resolverMu sync.RWMutex
	resolver   func() []Project
)

// SetProjectsResolver installs the process-wide hook that enumerates fleet
// projects. The server installs one backed by internal/sessions; passing nil
// clears it (used by tests and the no-fleet default). Mirrors
// agent.SetCollectionResolver / fstools.SetCwdResolver.
func SetProjectsResolver(f func() []Project) {
	resolverMu.Lock()
	resolver = f
	resolverMu.Unlock()
}

func currentProjects() []Project {
	resolverMu.RLock()
	f := resolver
	resolverMu.RUnlock()
	if f == nil {
		return nil
	}
	return f()
}

type projectsIn struct{}

type projectView struct {
	Name      string   `json:"name"`
	Cwd       string   `json:"cwd"`
	Engine    string   `json:"engine"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type projectsOut struct {
	Projects []projectView `json:"projects"`
	Order    []string      `json:"order"`    // dependency-first; empty on cycle
	Valid    bool          `json:"valid"`    // graph + workspaces sound
	Problems []string      `json:"problems"` // human-readable issues, empty when valid
}

// runProjects is the handler, extracted so tests can call it without ADK plumbing.
func runProjects(_ adk.ToolContext, _ projectsIn) (projectsOut, error) {
	projects := currentProjects()
	out := projectsOut{Valid: true}
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

// Tools returns the read-only fleet tool group.
func Tools() []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "fleet_projects",
		Description: "List the configured fleet projects (collections marked role:project), " +
			"their engine (omnis|claude) and dependency edges, the dependency-first " +
			"execution order, and any validation problems. Read-only; takes no arguments.",
	}, runProjects)
	if err != nil {
		panic(fmt.Errorf("build fleet_projects tool: %w", err))
	}
	return []tool.Tool{t}
}
```

- [ ] **Step 8: Run the full fleet package**

Run: `go test ./internal/fleet/...`
Expected: PASS (Task 2 + Task 3 tests).

> If Step 8 fails to compile on `adk.ToolContext{}` in the test, read `core/adk`
> to confirm the type and adjust the test's zero value (struct `{}` vs interface
> `nil`) — the handler body never touches the context, so only the test literal changes.

- [ ] **Step 9: Commit**

```bash
git add internal/fleet/workspace.go internal/fleet/tool.go internal/fleet/workspace_test.go internal/fleet/tool_test.go
git commit -m "feat(fleet): workspace validation + resolver hook + fleet_projects tool"
```

---

### Task 4: Wire the `fleet` tool-group key + server resolver

**Files:**
- Modify: `agent/agent.go` (import block; switch at ~line 290-333)
- Create: `server/fleet.go`
- Test: `server/fleet_test.go`

**Interfaces:**
- Consumes: `fleet.Tools()`, `fleet.SetProjectsResolver`, `fleet.Project`, `fleet.Engine` (Task 3); `sessions.ListCollections()`, `sessions.CollectionProfileFull(name)` (Task 1).
- Produces: agents can list `"fleet"` in their `tools`; the server populates the resolver from project-collections at startup.

- [ ] **Step 1: Add the `fleet` case to the tool-group switch**

In `agent/agent.go`, add to the import block (with the other `internal/...` imports):

```go
	"github.com/blouargant/omnis/internal/fleet"
```

In the `switch key {` block (after the `case "astgrep":` block, ~line 331), add:

```go
		case "fleet":
			// Read-only fleet registry tool (fleet_projects). Enumeration is
			// backed by the process-wide resolver the server installs; with no
			// resolver (CLI/TUI) it returns an empty list — no behavior.
			agentTools = append(agentTools, fleet.Tools()...)
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./agent/...`
Expected: builds (no unused import — `fleet` is used by the new case).

- [ ] **Step 3: Write the failing server resolver test**

Create `server/fleet_test.go`:

```go
package server

import (
	"testing"

	"github.com/blouargant/omnis/internal/fleet"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestInstallFleetResolverEnumeratesProjectCollections(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Two fleet projects + one plain collection.
	mustAdd := func(name string) {
		if _, _, err := sessions.AddCollection(name); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd("Service A")
	mustAdd("Service B")
	mustAdd("Notes") // plain collection, not a project

	if err := sessions.SetCollectionProfileData("Service A", sessions.CollectionProfileData{
		Role: "project", Engine: "omnis", Cwd: "/repos/a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfileData("Service B", sessions.CollectionProfileData{
		Role: "project", Engine: "claude", Cwd: "/repos/b", DependsOn: []string{"Service A"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfileData("Notes", sessions.CollectionProfileData{
		Cwd: "/notes",
	}); err != nil {
		t.Fatal(err)
	}

	installFleetResolver()
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })

	got := fleetProjectsForTest()
	if len(got) != 2 {
		t.Fatalf("want 2 fleet projects, got %d: %+v", len(got), got)
	}
	byName := map[string]fleet.Project{}
	for _, p := range got {
		byName[p.Name] = p
	}
	if byName["Service B"].Engine != fleet.EngineClaude {
		t.Fatalf("Service B engine = %q", byName["Service B"].Engine)
	}
	if len(byName["Service B"].DependsOn) != 1 || byName["Service B"].DependsOn[0] != "Service A" {
		t.Fatalf("Service B deps = %v", byName["Service B"].DependsOn)
	}
	if _, ok := byName["Notes"]; ok {
		t.Fatal("plain collection must not appear as a fleet project")
	}
}
```

- [ ] **Step 4: Run to verify it fails**

Run: `go test ./server/ -run TestInstallFleetResolver -v`
Expected: FAIL — `installFleetResolver` / `fleetProjectsForTest` undefined.

- [ ] **Step 5: Implement the server resolver**

Create `server/fleet.go`:

```go
package server

import (
	"github.com/blouargant/omnis/internal/fleet"
	"github.com/blouargant/omnis/internal/sessions"
)

// installFleetResolver wires the process-wide fleet project resolver to the
// collection registry: a fleet project is a collection whose profile has
// role:"project". Called once at server startup (see run() in main.go, beside
// agent.SetCollectionResolver / fstools.SetCwdResolver). CLI/TUI never call it,
// so fleet_projects returns an empty list there — the no-op contract.
func installFleetResolver() {
	fleet.SetProjectsResolver(collectFleetProjects)
}

func collectFleetProjects() []fleet.Project {
	names, err := sessions.ListCollections()
	if err != nil {
		return nil
	}
	var out []fleet.Project
	for _, name := range names {
		p := sessions.CollectionProfileFull(name)
		if p.Role != "project" {
			continue
		}
		out = append(out, fleet.Project{
			Name:      name,
			Cwd:       p.Cwd,
			Engine:    fleet.Engine(p.Engine),
			DependsOn: p.DependsOn,
		})
	}
	return out
}

// fleetProjectsForTest exposes the collected projects to the package test
// without depending on resolver-call timing.
func fleetProjectsForTest() []fleet.Project { return collectFleetProjects() }
```

- [ ] **Step 6: Call `installFleetResolver()` at startup**

In `server/main.go`, find the existing `agent.SetCollectionResolver(` call (installed in `run()`), and add on the line **after** it:

```go
	installFleetResolver()
```

- [ ] **Step 7: Run to verify it passes**

Run: `go test ./server/ -run TestInstallFleetResolver -v`
Expected: PASS.

- [ ] **Step 8: Full build + vet + the touched packages' tests**

Run:
```bash
go build ./... && go vet ./agent/... ./internal/fleet/... ./server/... && \
go test ./internal/fleet/... ./internal/sessions/... ./server/ -run 'Fleet|Collection'
```
Expected: builds, vets clean, tests PASS.

- [ ] **Step 9: Commit**

```bash
git add agent/agent.go server/fleet.go server/fleet_test.go server/main.go
git commit -m "feat(fleet): mount fleet tool-group + wire server project resolver"
```

---

## Self-Review

**Spec coverage (Phase-1 slice of spec §6, §11, §14 phase 1 prerequisites):**
- §6 collection profile fields `role`/`engine`/`depends_on` → Task 1. ✓
- §6 DAG build + cycle detection + edge validation → Task 2. ✓
- §6 cwd exists + is a git repo → Task 3 (`ValidateWorkspaces`). ✓
- §11 net-new `internal/fleet` importing only stdlib+ADK; resolver-hook idiom → Tasks 2-4. ✓
- §10 `fleet_projects` tool (list projects + DAG + engines) → Task 3. ✓
- §15 no-op contract (no resolver ⇒ empty; `role!="project"` ignored) → Task 3 (`currentProjects` nil-safe) + Task 4 (filter). ✓
- **Deferred to later phases (correctly out of this plan):** Conductor/Fleet squad + `config/agents.json` (Phase-1 remainder / plan 2), plan-approve-execute, Drivers, `claude_code` worker, worktree isolation, web-UI fields. Noted in the handoff below.

**Placeholder scan:** No TBD/TODO. Two conditional notes (adk.ToolContext zero value; the main.go anchor line) give an exact fallback action, not a vague instruction. The main.go step anchors on an existing, greppable call (`agent.SetCollectionResolver(`).

**Type consistency:** `CollectionProfileData.{Role,Engine,DependsOn}` (Task 1) match the reads in `collectFleetProjects` (Task 4). `fleet.Project` / `Engine` / `EngineClaude` used identically in Tasks 2-4. `runProjects(adk.ToolContext, projectsIn) (projectsOut, error)` matches `functiontool.New`'s `functiontool.Func[A,R]` shape (confirmed against `core/tools/tools.go:106`). `SetProjectsResolver(func() []Project)` signature identical across tool.go, tests, and server.
