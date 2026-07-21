# ADK v2 Paving Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Refactor omnis so the eventual ADK v1→v2 migration is a one-file change plus a handful of edits, and add guardrails (a CI test + a status dashboard + CLAUDE.md rules) that stop the v1/v2 gap from silently widening in the meantime.

**Architecture:** Introduce a tiny **anti-corruption layer** — a new leaf package `core/adk` — that owns the *only* three ADK surfaces v2 breaks: the context types (v2 merges `ToolContext`/`CallbackContext`/`ReadonlyContext`/`InvocationContext` into one `agent.Context`), the `session.NewEvent` constructor (v2 makes it take a `context.Context`), and the `Actions().SkipSummarization` turn-termination poke (v2's node runtime may change how a run ends). Sweep the whole codebase to name those seams through `core/adk` aliases/helpers, then fence them with a boundary test so nothing re-scatters them. **This plan does NOT bump ADK to v2** — it only paves. It deliberately does **not** wrap the *stable* ADK types (`model.LLM`, `agent.Agent`, `tool.Tool`, `session.Event`, `functiontool`) — wrapping stable types is cost with no payoff.

**Tech Stack:** Go 1.25.5, `google.golang.org/adk v1.5.0`, `google.golang.org/genai v1.57.0`. No new third-party dependencies — the guardrail is a plain Go test, not golangci-lint.

## Global Constraints

- **Module path:** `github.com/blouargant/omnis`. ADK stays pinned at `google.golang.org/adk v1.5.0` for the entire plan — **do not** add `google.golang.org/adk/v2`.
- **Behaviour-neutral:** every task is a runtime no-op. Type aliases are transparent (identical types), the helpers set the exact same field the inline code did, and `session.NewEventWithContext` already exists in v1.5. `make test` MUST stay green after every task.
- **The façade fences only the churny seams:** the context types, `session.NewEvent(WithContext)`, and `Actions().SkipSummarization`. Everything else in ADK is imported directly, unchanged.
- **New façade package name:** `core/adk`, package identifier `adk`. It imports ONLY `google.golang.org/adk/*` + stdlib (a pure leaf — no omnis imports — so nothing can create an import cycle with it).
- **Go version:** omnis already declares `go 1.25.5`, which satisfies ADK v2's Go 1.25+ floor — no change needed.
- **Base branch:** create `chore/adk-v2-paving`. Ideally branch it off the state where the verified `chore/adk-v1.5-upgrade` has landed, because the alias right-hand sides must reflect the ADK version actually in `go.mod`.
- **Commit trailer** on every commit:
  ```
  Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>
  ```

---

## Investigation Findings (why this plan exists)

ADK Go **2.0 GA'd 2026-06-30** as a *new module path* `google.golang.org/adk/v2` (Go 1.25+). It is a graph-based rewrite (Workflow Runtime, agent Chat/Task/SingleTurn modes, built-in durable HITL, Task API) with **breaking changes to the agent API, event model, and session schema**. Sources: [Announcing ADK Go 2.0](https://developers.googleblog.com/announcing-adk-go-20/), [migration guide README-v2.md](https://github.com/google/adk-go/blob/main/README-v2.md), [pkg.go.dev adk/v2](https://pkg.go.dev/google.golang.org/adk/v2).

The v2 breaking changes that actually hit omnis, with the measured blast radius (census run 2026-07-21):

| v2 breaking change | omnis exposure today |
|---|---|
| `ToolContext`/`CallbackContext`/`ReadonlyContext` merged into `agent.Context`; node/handler params take `agent.Context` | **`tool.Context` ×163**, `agent.CallbackContext` ×6 + `adkagent.CallbackContext` ×5, `agent.ReadonlyContext` ×4, `agent.InvocationContext` ×3 + `adkagent.InvocationContext` ×3 — ~55 files |
| `session.NewEvent` now takes `ctx` | **1 site**: [`agent/session_reseed.go:139`](../../agent/session_reseed.go#L139) `session.NewEvent("")` |
| Unified node runtime may change run termination (`SkipSummarization → IsFinalResponse → loop stops`) | **3 load-bearing sites**: [`agent/routing.go:194,230`](../../agent/routing.go#L194), [`agent/budget_plugin.go:132`](../../agent/budget_plugin.go#L132), [`internal/sessindex/tools.go:320`](../../internal/sessindex/tools.go#L320) — the router, the budget cap, and session-search all rely on it |

Confirmed non-issues: omnis does **not** implement a custom `InvocationContext` (it only consumes it), so v2's "custom InvocationContext must add `IsolationScope()`/`ResumedInput()`" does not apply. All packages omnis imports survive under `/v2` with identical names. The termination mechanism today is `session/session.go:126` `IsFinalResponse()` returning early when `e.Actions.SkipSummarization` is set.

---

## File Structure

- **Create** `core/adk/adk.go` — the anti-corruption façade: the four context type aliases + `EndTurnAfterToolCall` + `NewEvent`. The ONE file that names the churny ADK seams.
- **Create** `core/adk/adk_test.go` — unit tests: the v2 **termination canary** (`SkipSummarization ⇒ IsFinalResponse`) + a `NewEvent` smoke test.
- **Create** `internal/adkguard/guard_test.go` — the anti-divergence guardrail: walks the repo and fails if a raw churny seam appears outside `core/adk`. Runs in `make test`.
- **Modify** ~55 files (context-type sweep) — replace raw context selectors with `adk.*` aliases.
- **Modify** `agent/routing.go`, `agent/budget_plugin.go`, `internal/sessindex/tools.go` — route the `SkipSummarization` pokes through `adk.EndTurnAfterToolCall`.
- **Modify** `agent/session_reseed.go` — route `session.NewEvent` through `adk.NewEvent`.
- **Create** `scripts/adk-v2-status.sh` + **Modify** `Makefile` — the `adk-v2-status` migration dashboard (version drift + surface size).
- **Create** `docs/adk-v2-readiness.md` — the living readiness tracker + the manual v2-spike procedure + the migration checklist.
- **Modify** `CLAUDE.md` — add a clearly-delimited, **removable** "ADK v2 transition" section.

---

### Task 1: The `core/adk` anti-corruption façade + v2 termination canary

**Files:**
- Create: `core/adk/adk.go`
- Test: `core/adk/adk_test.go`

**Interfaces:**
- Produces: package `adk` with type aliases `adk.ToolContext`, `adk.CallbackContext`, `adk.ReadonlyContext`, `adk.InvocationContext`; `func adk.EndTurnAfterToolCall(ctx adk.ToolContext)`; `func adk.NewEvent(ctx context.Context, invocationID string) *session.Event`.
- Consumes: nothing from omnis (pure leaf over `google.golang.org/adk/*`).

- [ ] **Step 1: Write the failing canary test**

Create `core/adk/adk_test.go`:

```go
package adk

import (
	"context"
	"testing"
)

// TestSkipSummarizationImpliesFinalResponse pins the exact host-side guarantee
// omnis's turn-termination depends on: an event whose SkipSummarization is set
// reports IsFinalResponse(), which is what stops the ADK flow loop. It is the
// v2 CANARY — ADK v2 drives even a plain LlmAgent through the workflow node
// runtime, and if that changes how a run ends, EndTurnAfterToolCall silently
// stops terminating runs and the router / budget cap / session-search would
// spin forever. If this test breaks at the v2 bump, the termination design
// (not just this package) needs rework.
func TestSkipSummarizationImpliesFinalResponse(t *testing.T) {
	ev := NewEvent(context.Background(), "canary")
	ev.Actions.SkipSummarization = true
	if !ev.IsFinalResponse() {
		t.Fatal("SkipSummarization must imply IsFinalResponse(): omnis relies on it to end a turn after route_to_squad / handoff_to_router / the budget cap / report_sessions")
	}
}

// TestNewEventThreadsContext smoke-tests the ctx-taking constructor wrapper
// (the v2-shaped API, already available in v1.5 as NewEventWithContext).
func TestNewEventThreadsContext(t *testing.T) {
	if ev := NewEvent(context.Background(), "inv-1"); ev == nil {
		t.Fatal("NewEvent returned nil")
	}
}
```

- [ ] **Step 2: Run it to verify it fails to build**

Run: `go test ./core/adk/ -run TestSkipSummarization -v`
Expected: build failure — `undefined: NewEvent` (the package doesn't exist yet).

- [ ] **Step 3: Write the façade**

Create `core/adk/adk.go`:

```go
// Package adk is omnis's anti-corruption layer over the churny surface of
// google.golang.org/adk. It exists so the ADK v1->v2 migration is a one-file
// change instead of a ~55-file sweep: v2 merges the three context types into
// agent.Context, changes session.NewEvent to take a context, and reworks the
// run/flow runtime. Every omnis package names those seams through the aliases
// and helpers here, so migrating is: repoint the imports below to
// ".../adk/v2/..." and adjust these few lines.
//
// It deliberately does NOT wrap the STABLE ADK types (model.LLM, agent.Agent,
// tool.Tool, session.Event, functiontool) — those are imported directly across
// the codebase and v2 does not break them, so wrapping them would be cost with
// no payoff.
//
// This package imports ONLY google.golang.org/adk/* + stdlib, so it is a pure
// leaf and can never form an import cycle with the rest of omnis.
package adk

import (
	"context"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// Context aliases. In ADK v1 these are three distinct interfaces; ADK v2 merges
// them into a single agent.Context. TO MIGRATE: change every right-hand side
// below to agent.Context (they all become the same type). Call sites, which use
// only the alias names on the left, do not change.
type (
	// ToolContext is the context a tool handler receives.
	ToolContext = tool.Context // v2: = agent.Context
	// CallbackContext is the context a before/after model-or-tool callback receives.
	CallbackContext = agent.CallbackContext // v2: = agent.Context
	// ReadonlyContext is the read-only context some callbacks receive.
	ReadonlyContext = agent.ReadonlyContext // v2: = agent.Context
	// InvocationContext is the context a run-level (before/after-run, user-message) callback receives.
	InvocationContext = agent.InvocationContext // v2: = agent.Context
)

// EndTurnAfterToolCall marks the current function-response event as final, so
// the ADK flow loop stops immediately after this tool call instead of handing
// the model another (possibly looping) turn. It is the host-side turn-
// termination guarantee behind route_to_squad / handoff_to_router, the per-
// agent budget cap, and session-search's report_sessions.
//
// This is THE single place that pokes SkipSummarization. ADK v2 drives even a
// plain LlmAgent through the workflow node runtime, which may change how a run
// terminates; if it does, re-implement termination here (e.g. via a v2 route or
// HITL primitive) and every call site is fixed at once. TestSkipSummarization*
// in this package is the canary that fails the moment the mechanism changes.
func EndTurnAfterToolCall(ctx ToolContext) {
	ctx.Actions().SkipSummarization = true
}

// NewEvent builds a session.Event, threading ctx as ADK v2 requires. It wraps
// session.NewEventWithContext, which already exists in v1.5 and is the v2-shaped
// constructor, so call sites are v2-ready today. TO MIGRATE: swap the body to
// session.NewEvent(ctx, invocationID).
func NewEvent(ctx context.Context, invocationID string) *session.Event {
	return session.NewEventWithContext(ctx, invocationID)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./core/adk/ -v`
Expected: PASS — `TestSkipSummarizationImpliesFinalResponse`, `TestNewEventThreadsContext`.

- [ ] **Step 5: Confirm the whole tree still builds**

Run: `go build ./...`
Expected: no output (success). The new package is not yet imported anywhere, so nothing else changes.

- [ ] **Step 6: Commit**

```bash
git add core/adk/adk.go core/adk/adk_test.go
git commit -m "feat(adk): add core/adk anti-corruption façade + v2 termination canary

Localises the three ADK surfaces v2 breaks (context types, session.NewEvent,
SkipSummarization) behind one leaf package so the eventual v1->v2 migration is
a one-file change. Behaviour-neutral: aliases are identical types; NewEvent
wraps the v1.5 NewEventWithContext constructor.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Sweep the context types to the `core/adk` aliases

**Files:**
- Modify: ~55 files across `core/`, `internal/`, `agent/` that name a raw ADK context type. Do **not** touch `core/adk/adk.go`.

**Interfaces:**
- Consumes: `adk.ToolContext`, `adk.CallbackContext`, `adk.ReadonlyContext`, `adk.InvocationContext` from Task 1.
- Produces: a codebase where the only raw context selectors left are inside `core/adk`.

The qualifier map (measured): `tool.Context` (163, always qualifier `tool`), `agent.CallbackContext` (6) + `adkagent.CallbackContext` (5), `agent.ReadonlyContext` (4), `agent.InvocationContext` (3) + `adkagent.InvocationContext` (3). `fileref.Context` (2) is omnis's OWN type — never rewrite it (the rules below don't match it).

- [ ] **Step 1: Apply the six rewrite rules with `gofmt -r` (excluding the façade)**

Run each of these from the repo root. `gofmt -r` rewrites the selector expressions in place; it does not manage imports (next step does).

```bash
# The façade must keep its raw right-hand sides — exclude it from the sweep.
FILES=$(grep -rlE '\btool\.Context\b|\b(agent|adkagent)\.(CallbackContext|ReadonlyContext|InvocationContext)\b' --include=*.go . | grep -v '/core/adk/')

for f in $FILES; do
  gofmt -w \
    -r 'tool.Context -> adk.ToolContext' \
    "$f"
  gofmt -w \
    -r 'agent.CallbackContext -> adk.CallbackContext' \
    "$f"
  gofmt -w \
    -r 'adkagent.CallbackContext -> adk.CallbackContext' \
    "$f"
  gofmt -w \
    -r 'agent.ReadonlyContext -> adk.ReadonlyContext' \
    "$f"
  gofmt -w \
    -r 'agent.InvocationContext -> adk.InvocationContext' \
    "$f"
  gofmt -w \
    -r 'adkagent.InvocationContext -> adk.InvocationContext' \
    "$f"
done
```

- [ ] **Step 2: Fix imports (add `core/adk`, drop now-unused ADK imports)**

If `goimports` is available, it does both automatically:

```bash
command -v goimports >/dev/null || go install golang.org/x/tools/cmd/goimports@latest
goimports -w -local github.com/blouargant/omnis $FILES
```

goimports adds `adk "github.com/blouargant/omnis/core/adk"` to files that now reference `adk.*`, and removes an ADK import (`.../adk/tool` or `.../adk/agent`) that became unused (e.g. a file that used `agent` ONLY for `ReadonlyContext`). If `goimports` cannot be installed, rely on the compiler in Step 3: for each error, add `adk "github.com/blouargant/omnis/core/adk"` to the import block or delete the now-unused ADK import by hand.

- [ ] **Step 3: Build and fix any stragglers (aliased imports the rules missed)**

Run: `go build ./...`
If it fails with an undefined `agent.*`/`tool.*`/`adkagent.*` context selector, an import used a local alias the six rules didn't cover. Find and fix:

```bash
# Any raw context selector still present outside the façade is a straggler:
grep -rnE '\btool\.Context\b|\b[a-zA-Z_]+\.(CallbackContext|ReadonlyContext|InvocationContext)\b' --include=*.go . \
  | grep -v '/core/adk/' | grep -vE '\badk\.'
```
Rewrite each straggler to the matching `adk.*` alias, re-run `goimports`/build. Repeat until the build is clean.

- [ ] **Step 4: Run the full test suite (proves behaviour is unchanged)**

Run: `make test`
Expected: all packages pass — identical to the pre-sweep run (aliases are transparent, so no behaviour changed).

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(adk): route all ADK context types through core/adk aliases

Mechanical, behaviour-neutral sweep (~184 occurrences across ~55 files):
tool.Context/agent.*Context/adkagent.*Context -> adk.*. The v2 context
unification is now a one-file change in core/adk.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Route the termination poke + event constructor through the helpers

**Files:**
- Modify: `agent/routing.go:194`, `agent/routing.go:230`
- Modify: `agent/budget_plugin.go:132`
- Modify: `internal/sessindex/tools.go:320`
- Modify: `agent/session_reseed.go:139`

**Interfaces:**
- Consumes: `adk.EndTurnAfterToolCall`, `adk.NewEvent` from Task 1.
- Produces: a codebase where `Actions().SkipSummarization` and `session.NewEvent(WithContext)` appear only inside `core/adk`.

- [ ] **Step 1: Replace the two routing pokes**

In `agent/routing.go`, both `route_to_squad` and `handoff_to_router` currently do `ctx.Actions().SkipSummarization = true`. Replace each with the helper (keep the surrounding comment):

```go
// route_to_squad handler (~line 194):
adk.EndTurnAfterToolCall(ctx)
```
```go
// handoff_to_router handler (~line 230):
adk.EndTurnAfterToolCall(ctx)
```

- [ ] **Step 2: Replace the budget-cap poke**

In `agent/budget_plugin.go` (~line 132), replace `tc.Actions().SkipSummarization = true` with:

```go
adk.EndTurnAfterToolCall(tc)
```

- [ ] **Step 3: Replace the session-search poke**

In `internal/sessindex/tools.go` (~line 320), replace `ctx.Actions().SkipSummarization = true` with:

```go
adk.EndTurnAfterToolCall(ctx)
```

- [ ] **Step 4: Replace the event constructor**

In `agent/session_reseed.go`, the `appendEvent` closure (~line 138) captures the enclosing `ctx`. Replace `ev := session.NewEvent("")` with:

```go
ev := adk.NewEvent(ctx, "")
```

- [ ] **Step 5: Fix imports and build**

Add `adk "github.com/blouargant/omnis/core/adk"` to any of the four files that don't already import it (Task 2 added it to `routing.go`/`budget_plugin.go`/`sessindex/tools.go` if they used a context type; `session_reseed.go` likely needs it added). Remove the now-unused `session` import from `session_reseed.go` only if nothing else there uses `session.` (it still uses `session.Service`/`AppendEvent`, so keep it).

```bash
command -v goimports >/dev/null && goimports -w -local github.com/blouargant/omnis \
  agent/routing.go agent/budget_plugin.go internal/sessindex/tools.go agent/session_reseed.go
go build ./...
```
Expected: success.

- [ ] **Step 6: Run the affected tests**

Run: `go test ./agent/... ./internal/sessindex/... ./core/adk/...`
Expected: PASS. Behaviour is identical — the helper sets the same field, and `NewEventWithContext` produces the same event as `NewEvent("")` did (threading a context the reseed path already had).

- [ ] **Step 7: Commit**

```bash
git add agent/routing.go agent/budget_plugin.go internal/sessindex/tools.go agent/session_reseed.go
git commit -m "refactor(adk): route SkipSummarization + NewEvent through core/adk helpers

route_to_squad/handoff_to_router, the budget cap, and report_sessions now call
adk.EndTurnAfterToolCall; session reseed calls adk.NewEvent(ctx, ...). The v2
run-termination + event-constructor changes are now one-file changes, and the
termination guarantee has a single re-implementation point (with its canary).

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: The anti-divergence boundary guard test

**Files:**
- Create: `internal/adkguard/guard_test.go`

**Interfaces:**
- Consumes: nothing (reads source files off disk).
- Produces: a `go test` that fails if any raw churny ADK seam appears outside `core/adk`.

- [ ] **Step 1: Write the guard test**

Create `internal/adkguard/guard_test.go`:

```go
// Package adkguard hosts a source-level guardrail (no production code): it
// fails if a raw ADK seam that ADK v2 breaks is used outside core/adk. All
// such seams must go through core/adk (adk.ToolContext, adk.EndTurnAfterToolCall,
// adk.NewEvent) so the v1->v2 migration stays a one-file change. If this test
// fails, route the offending call site through core/adk — do NOT weaken the
// guard.
package adkguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// rawADK matches a raw use of an ADK symbol v2 breaks. The alternatives that
// capture a qualifier let us skip our own façade (adk.ToolContext, adk.NewEvent).
// Limitation: an import of google.golang.org/adk/tool under a NON-default alias
// would evade the `tool.Context` branch; omnis imports it as `tool`, and the
// suffix branches below catch aliased agent-context imports regardless of name.
var rawADK = regexp.MustCompile(
	`\btool\.Context\b` +
		`|\b(\w+)\.(ToolContext|CallbackContext|ReadonlyContext|InvocationContext)\b` +
		`|\.SkipSummarization\b` +
		`|\b(\w+)\.NewEvent(WithContext)?\(`,
)

func TestNoRawADKSeamsOutsideFacade(t *testing.T) {
	root := repoRoot(t)
	allow := map[string]bool{
		filepath.FromSlash("core/adk/adk.go"):      true,
		filepath.FromSlash("core/adk/adk_test.go"): true,
	}
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "vendor" || d.Name() == ".git" || d.Name() == "web" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if allow[rel] {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, m := range rawADK.FindAllString(line, -1) {
				if strings.HasPrefix(m, "adk.") { // our own façade — allowed
					continue
				}
				offenders = append(offenders, rel+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("raw ADK seams found outside core/adk — route them through core/adk (see docs/adk-v2-readiness.md):\n%s",
			strings.Join(offenders, "\n"))
	}
}

// repoRoot walks up from the test's working directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}
```

- [ ] **Step 2: Run the guard to verify it passes (sweep is complete)**

Run: `go test ./internal/adkguard/ -run TestNoRawADKSeamsOutsideFacade -v`
Expected: PASS. If it lists offenders, Tasks 2/3 missed them — route each through `core/adk` and re-run.

- [ ] **Step 3: Prove the guard actually catches reintroduction**

Temporarily add a raw seam to a scratch file and confirm the guard fails, then remove it:

```bash
printf 'package agent\nimport "google.golang.org/adk/tool"\nvar _ = func(c tool.Context) {}\n' > agent/zz_guard_probe.go
go test ./internal/adkguard/ -run TestNoRawADKSeamsOutsideFacade 2>&1 | grep -q 'zz_guard_probe.go' && echo "GUARD CATCHES IT" || echo "GUARD FAILED TO CATCH"
rm agent/zz_guard_probe.go
```
Expected: prints `GUARD CATCHES IT`.

- [ ] **Step 4: Confirm it runs as part of `make test`**

Run: `make test 2>&1 | tail -5`
Expected: the suite (including `internal/adkguard`) passes.

- [ ] **Step 5: Commit**

```bash
git add internal/adkguard/guard_test.go
git commit -m "test(adk): guard against raw ADK v2-breaking seams outside core/adk

Fails make test if tool.Context / agent.*Context / .SkipSummarization /
session.NewEvent reappear outside the façade, keeping the v1->v2 gap from
silently widening.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Migration dashboard (`adk-v2-status`) + living readiness doc

**Files:**
- Create: `scripts/adk-v2-status.sh`
- Modify: `Makefile`
- Create: `docs/adk-v2-readiness.md`

**Interfaces:**
- Consumes: nothing.
- Produces: `make adk-v2-status` (a dashboard) and the readiness tracker doc.

- [ ] **Step 1: Write the status script**

Create `scripts/adk-v2-status.sh`:

```bash
#!/usr/bin/env bash
# ADK v2 migration dashboard: current ADK pin, latest published adk/v2, and the
# size of the surface still naming ADK's churny seams outside core/adk (i.e. the
# migration cost + a divergence tripwire). See docs/adk-v2-readiness.md.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "ADK pinned (go.mod):   $(grep -E 'google.golang.org/adk ' go.mod | awk '{print $2}')"
echo "Latest adk/v2:         $(go list -m -versions google.golang.org/adk/v2 2>/dev/null | tr ' ' '\n' | tail -1 || echo '(offline)')"
echo
echo "Surface outside core/adk (aliased seams should read 0):"
printf '  raw context types:   %s\n' "$(grep -rE '\btool\.Context\b|\b(agent|adkagent)\.(ToolContext|CallbackContext|ReadonlyContext|InvocationContext)\b' --include='*.go' . | grep -v '/core/adk/' | wc -l | tr -d ' ')"
printf '  SkipSummarization:   %s\n' "$(grep -rE '\.SkipSummarization\b' --include='*.go' . | grep -v '/core/adk/' | wc -l | tr -d ' ')"
printf '  session.NewEvent:    %s\n' "$(grep -rE '\bsession\.NewEvent' --include='*.go' . | grep -v '/core/adk/' | wc -l | tr -d ' ')"
printf '  direct ADK imports:  %s file(s)\n' "$(grep -rlE '\"google\.golang\.org/adk' --include='*.go' . | grep -v '/core/adk/' | wc -l | tr -d ' ')"
```

Make it executable:
```bash
chmod +x scripts/adk-v2-status.sh
```

- [ ] **Step 2: Add the Makefile target**

Add near the other quality targets in `Makefile` (after `vet:`):

```make
adk-v2-status: ## Show ADK v2 migration surface + version drift
	@bash scripts/adk-v2-status.sh
```

- [ ] **Step 3: Run it and capture the baseline numbers**

Run: `make adk-v2-status`
Expected: the three aliased-seam counts (`raw context types`, `SkipSummarization`, `session.NewEvent`) read **0**; `direct ADK imports` shows the count of files still importing ADK directly for the STABLE types (expected non-zero — that's fine). Record these numbers for Step 4.

- [ ] **Step 4: Write the readiness doc**

Create `docs/adk-v2-readiness.md`:

```markdown
# ADK v2 migration readiness

**Status:** paving (still on ADK v1.5; NOT migrated). **Do not delete this doc
or the CLAUDE.md "ADK v2 transition" section until `go.mod` is on
`google.golang.org/adk/v2` and this file says "migrated".**

## Why

ADK Go 2.0 (GA 2026-06-30, module `google.golang.org/adk/v2`, Go 1.25+) is a
graph-based rewrite with breaking changes to the agent API, event model, and
session schema. We are deliberately waiting for it to mature, but paving the way
so the eventual upgrade is small and low-risk. Primary sources: the ADK Go 2.0
announcement and the README-v2.md migration guide.

## The three seams v2 breaks (all fenced behind `core/adk`)

| v2 change | omnis lives behind |
|---|---|
| context types merged into `agent.Context` | `adk.ToolContext` / `adk.CallbackContext` / `adk.ReadonlyContext` / `adk.InvocationContext` |
| `session.NewEvent` takes a `ctx` | `adk.NewEvent(ctx, id)` |
| run termination reworked (node runtime) | `adk.EndTurnAfterToolCall(ctx)` + the canary `core/adk.TestSkipSummarizationImpliesFinalResponse` |

## Surface tracker

Run `make adk-v2-status`. Definition of ready = the three aliased-seam counts
are 0 AND `core/adk` compiles/tests green against the target ADK version.

- Baseline (2026-07-21, on v1.5): raw context types **0**, SkipSummarization **0**, session.NewEvent **0**.

## Manual v2 spike procedure (run occasionally; do NOT commit the result)

```bash
git worktree add /tmp/omnis-v2-spike HEAD
cd /tmp/omnis-v2-spike
go get google.golang.org/adk/v2@latest
grep -rl '"google.golang.org/adk/' --include='*.go' core/adk | \
  xargs sed -i 's#"google.golang.org/adk/#"google.golang.org/adk/v2/#g'   # façade only
# In core/adk/adk.go: change the four alias RHS to agent.Context and
# NewEvent's body to session.NewEvent(ctx, invocationID).
go build ./... 2>&1 | tee /tmp/v2-build.log | grep -c '^'   # count of remaining errors
cd - && git worktree remove /tmp/omnis-v2-spike --force
```
The remaining errors after fixing `core/adk` are the true migration cost. Record
the count + a one-line summary here each time you spike.

## When we migrate (checklist)

- [ ] Bump `go.mod` to `google.golang.org/adk/v2` (+ `go mod tidy`).
- [ ] In `core/adk/adk.go`: alias RHS -> `agent.Context`; `NewEvent` body -> `session.NewEvent(ctx, id)`.
- [ ] `go build ./...`; fix any stable-type API drift the spike surfaced.
- [ ] `go test ./core/adk/` — the termination canary MUST pass (if not, redesign termination before proceeding).
- [ ] `make test` green; live-gateway smoke (see the v1.5 upgrade plan for the recipe).
- [ ] Delete this doc, the `adk-v2-status` target + script, `internal/adkguard`, and the CLAUDE.md "ADK v2 transition" section — the scaffolding has done its job.
```

- [ ] **Step 5: Commit**

```bash
git add scripts/adk-v2-status.sh Makefile docs/adk-v2-readiness.md
git commit -m "chore(adk): add adk-v2-status dashboard + readiness tracker doc

make adk-v2-status reports version drift + the churny-seam surface; the doc
tracks the baseline, the manual v2 spike procedure, and the migration checklist
(which includes deleting all of this scaffolding once migrated).

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: CLAUDE.md transition instructions (removable)

**Files:**
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: the `core/adk` façade, the `adkguard` test, `make adk-v2-status`, and `docs/adk-v2-readiness.md` from earlier tasks.
- Produces: a clearly-delimited, self-describing-as-temporary section that binds future sessions to the façade + guardrails.

- [ ] **Step 1: Insert the transition section**

In `CLAUDE.md`, insert the following block immediately **after** the `## Self-Maintenance Rule` section and **before** `## What's-new feature tracking (FEATURES.md)`:

```markdown
## ⚠️ ADK v2 transition (TEMPORARY — delete this whole section once migrated)

> This section is scaffolding for the pending **ADK v1→v2 migration**. **Delete
> it (heading included) once `go.mod` is on `google.golang.org/adk/v2` and
> [docs/adk-v2-readiness.md](docs/adk-v2-readiness.md) reports "migrated".** It
> exists so the migration stays a one-file change instead of a ~55-file sweep.

While omnis is on ADK v1 but preparing for v2, **never name a churny ADK symbol
directly** — reach it through the `core/adk` façade so the v2 change lands in one
place:

- Context types → `adk.ToolContext` / `adk.CallbackContext` / `adk.ReadonlyContext`
  / `adk.InvocationContext` — **not** `tool.Context` / `agent.*Context`.
- Turn termination → `adk.EndTurnAfterToolCall(ctx)` — **not**
  `ctx.Actions().SkipSummarization = true`.
- Event construction → `adk.NewEvent(ctx, id)` — **not** `session.NewEvent(...)`.

The guard test `internal/adkguard` fails `make test` if a raw form reappears —
**fix the call site, never weaken the guard.** The **stable** ADK types
(`model.LLM`, `agent.Agent`, `tool.Tool`, `session.Event`, `functiontool.New`)
are **not** fenced — import them directly as before.

**Do not add new bespoke orchestration that ADK v2 already provides natively**
(graph workflow routing/fan-out/fan-in/loops, agent Chat/Task/SingleTurn modes,
durable HITL pause/resume) without recording the decision in
[docs/adk-v2-readiness.md](docs/adk-v2-readiness.md). The v1-only mechanisms —
the `RunWithRouting` dispatch loop, the concurrent/resumable agenttool wrappers,
and `ask_user`'s in-memory HITL — are **frozen (bug-fix only)** to keep the v2
gap from widening.

Run `make adk-v2-status` to see the migration surface; keep
`docs/adk-v2-readiness.md` current (same self-maintenance discipline as
FEATURES.md).
```

- [ ] **Step 2: Verify the insertion is well-formed**

Run: `grep -n "ADK v2 transition" CLAUDE.md`
Expected: one match, positioned between the Self-Maintenance Rule and the What's-new sections.

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(adk): add removable ADK v2 transition rules to CLAUDE.md

Binds future sessions to the core/adk façade + the adkguard guardrail and
freezes v1-only orchestration, so the v2 gap can't silently widen. The section
is explicitly marked for deletion once the migration lands.

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Self-Review

**1. Spec coverage** (against the last two turns' agreed strategy):
- *Anti-corruption façade for the churny seams* → Task 1 (`core/adk`).
- *Sweep the codebase behind it* → Task 2 (context types) + Task 3 (termination + NewEvent).
- *The termination trick centralised + pinned with a canary* → Task 1 (`EndTurnAfterToolCall` + `TestSkipSummarization*`), consumed in Task 3.
- *CI guardrail that fails on divergence* → Task 4 (`internal/adkguard`, runs in `make test`).
- *Version/surface drift monitor* → Task 5 (`make adk-v2-status`) + the readiness doc's manual v2-spike procedure (the "compile-canary" idea, kept manual to avoid a brittle CI job — noted deliberately).
- *CLAUDE.md transition instructions that are removable* → Task 6, explicitly self-describing as delete-on-migration. The user's explicit ask is covered.
- *Discipline / freeze v1-only orchestration* → encoded in Task 6 + the readiness doc, not a code change (YAGNI: no premature interface abstraction of existing orchestration).

**2. Placeholder scan:** No TBD/TODO/"handle edge cases". Every code step shows complete code; every command shows expected output. The sweep (Task 2) is scripted with the exact six rewrite rules derived from the measured qualifier map, plus a straggler-catch step for aliased imports.

**3. Type consistency:** `adk.ToolContext`/`adk.CallbackContext`/`adk.ReadonlyContext`/`adk.InvocationContext`, `adk.EndTurnAfterToolCall(ctx adk.ToolContext)`, and `adk.NewEvent(ctx context.Context, invocationID string) *session.Event` are defined in Task 1 and used with those exact names/signatures in Tasks 2–4. The guard regex in Task 4 skips the `adk.` qualifier, so it does not flag the aliases it is meant to enforce. `session.NewEventWithContext` (used by `adk.NewEvent`) is confirmed present in ADK v1.5.0.

**Deferred (out of scope, by design):** the actual `/v2` bump; automated CI compile-canary (manual spike instead); interface-abstraction of existing orchestration to v2 shapes (freeze-only for now).
