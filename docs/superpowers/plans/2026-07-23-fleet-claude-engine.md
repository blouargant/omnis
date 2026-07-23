# Fleet Claude Worker Engine — Implementation Plan (Plan 3a of the Fleet feature)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a `claude`-engine fleet project dispatchable: its Driver drives a **real external `claude` CLI** subprocess (instead of the omnis Coding squad), rooted in the project's directory, resuming the same headless session across calls, launched with a conservative default `--allowedTools` allowlist that each project can override.

**Architecture:** A new `internal/claudecode` package modelled directly on `internal/astgrep` — a binary gated by `internal/deps` (`claude` on PATH), invoked with explicit argv (`exec.CommandContext`, `cmd.Dir = session cwd`), JSON-envelope parsing. It exposes a `claude_code` tool. A new leaderless **Claude Worker** squad's single agent owns that tool; `fleet.EngineSquad(EngineClaude)` now returns that squad, so the Plan-2a dispatch/drain machinery routes claude-engine projects to it with **no dispatch-side changes**. The per-driver-session claude `session_id` is stored process-wide and reused (`--resume`) within a driver session, fresh across dispatches. The allowlist is a conservative built-in default overridable per project via a new collection-profile field, resolved through a server-installed hook (respecting the agent↔sessions cycle).

**Tech Stack:** Go; `os/exec` (explicit argv, no shell); `internal/deps` gate; `core/tools` (`fstools.CwdForContext`); `core/adk`; the Plan-1/2a `internal/fleet` + collection profile; standard `go test` with a fake `claude` script on PATH.

## Global Constraints

- **Confirmed CLI contract** (verified against the Claude Code headless/CLI-reference docs): non-interactive `claude -p "<task>"`; `--output-format json` returns `{result, session_id, usage, total_cost_usd, model}`; `--resume <session_id>` continues a prior session **from the same directory**; `--allowedTools "<rules>"` sets the permission allowlist; `--model <id>` selects the model. There is **no CLI flag to set the working directory** — we set the subprocess `cmd.Dir` in Go.
- **Permission posture (decided):** launch with a conservative default `--allowedTools` allowlist; **never** `--dangerously-skip-permissions`. Each project may override the allowlist via its collection. The Driver is headless (cannot prompt mid-task), so the allowlist is fixed at launch.
- **Explicit argv, never a shell string** — the task text is passed as a single `exec` arg, so no shell-injection surface (like `internal/astgrep`). Do NOT route the task through `fstools.RunShellCaptured`.
- **Session scoping:** the claude `session_id` is keyed by the omnis **driver session id** — reused (`--resume`) across `claude_code` calls within that driver session, and dropped on session end (task-scoped; a fresh dispatch = a fresh driver session = a fresh claude session). No long-lived per-project claude session.
- **Import-cycle rule:** `internal/claudecode` imports only stdlib + ADK + `core/adk` + `internal/deps` + `core/tools` (mirrors `internal/astgrep`) — NOT `agent`/`sessions`/`server`/`internal/fleet`. `agent` and `server` may import `internal/claudecode`. The allowlist resolver reaches collection data only through a server-installed hook.
- **No-op contract:** with `claude` absent, the deps gate reports it unavailable and the Driver reports it can't proceed (never a wrong-engine fallback). A build that declares no claude-engine projects never launches `claude`. `EngineOmnis` dispatch is byte-identical to Plan 2a.
- **English only** for user-facing strings.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/claudecode/claudecode.go` (create) | `Requirement()`/`DepGate`/`SetDepGate`/`ensureDep`; the session-id store (`rememberSession`/`resumeID`/`ForgetSession`); default allowlist + `SetAllowlistResolver`; the `claude_code` tool + `Tools()`; JSON-envelope parse. | 1 |
| `internal/claudecode/claudecode_test.go` (create) | Fake-`claude`-on-PATH tests: fresh run captures session_id; a 2nd call passes `--resume`; missing binary → notice; allowlist flag present. | 1 |
| `internal/sessions/collections.go` (modify) | Add `ClaudeAllowedTools []string` to the profile (Plan-1 field pattern). | 2 |
| `internal/sessions/collections_profile_test.go` (modify) | Round-trip the new field. | 2 |
| `internal/fleet/fleet.go` (modify) | `Project.AllowedTools []string`; `EngineSquad(EngineClaude)` → the Claude Worker squad. | 3 |
| `internal/fleet/fleet_test.go` (modify) | `EngineSquad(EngineClaude)` now returns the squad; assert it. | 3 |
| `server/fleet.go` (modify) | `collectFleetProjects` maps `ClaudeAllowedTools` → `Project.AllowedTools`. | 3 |
| `agent/agent.go` (modify) | Add the `claude_code` tool-group key. | 4 |
| `agent/infrastructure.go` (modify) | `claudecode.SetDepGate(...)` beside the astgrep gate. | 4 |
| `server/fleet_claude.go` (create) | Install `claudecode.SetAllowlistResolver` (session→collection→allowlist); call `claudecode.ForgetSession` from `forgetSessionState`. | 4 |
| `server/spawn.go` (modify) | `forgetSessionState` calls `claudecode.ForgetSession(id)`. | 4 |
| `registry/agents/claude_worker/{agent.json,instruction.md}` (create) | The Claude Worker driver agent. | 5 |
| `config/agents.json` (modify) | Enable `claude_worker`; add the leaderless `Claude Worker` squad. | 5 |

---

### Task 1: `internal/claudecode` — the `claude_code` tool

**Files:** Create `internal/claudecode/claudecode.go`, `internal/claudecode/claudecode_test.go`

**Interfaces produced:**
- `Requirement() deps.Requirement`; `type DepGate func(adk.ToolContext) string`; `SetDepGate(DepGate)`.
- `DefaultAllowedTools []string`; `SetAllowlistResolver(func(sessionID string) []string)`.
- `rememberSession(sessionID, claudeID string)`, `resumeID(sessionID) string`, `ForgetSession(sessionID string)`.
- `Tools() []tool.Tool` (the `claude_code` tool).

- [ ] **Step 1: Write the failing test (fake `claude` on PATH)**

Create `internal/claudecode/claudecode_test.go`:

```go
package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeClaude puts a fake `claude` executable first on PATH. It appends its
// argv to $CLAUDE_ARGS_LOG (one invocation per line) and prints a JSON envelope
// carrying a session id derived from whether --resume was passed, so tests can
// assert both the flags sent and the resume round-trip.
func writeFakeClaude(t *testing.T) (argsLog string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + argsLog + "\"\n" +
		"sid=fresh-123\n" +
		"case \"$*\" in *--resume*) sid=resumed-123;; esac\n" +
		"printf '{\"result\":\"ok\",\"session_id\":\"%s\",\"total_cost_usd\":0.001,\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}' \"$sid\"\n"
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsLog
}

func TestClaudeCodeFreshThenResume(t *testing.T) {
	argsLog := writeFakeClaude(t)
	SetDepGate(nil) // plain PATH check; fake claude is present
	SetAllowlistResolver(nil)
	t.Cleanup(func() { ForgetSession("sessA") })

	// First call: no prior session ⇒ no --resume; captures the fresh id.
	r1, err := runClaudeCode(tcStub("sessA"), claudeCodeIn{Task: "do a thing"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r1.Result, "ok") {
		t.Fatalf("expected result text, got %q", r1.Result)
	}
	if resumeID("sessA") != "fresh-123" {
		t.Fatalf("session id not captured, got %q", resumeID("sessA"))
	}

	// Second call in the same session: must pass --resume fresh-123.
	if _, err := runClaudeCode(tcStub("sessA"), claudeCodeIn{Task: "next step"}); err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(argsLog)
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 invocations, got %d: %q", len(lines), logged)
	}
	if strings.Contains(lines[0], "--resume") {
		t.Fatalf("first call must not resume: %q", lines[0])
	}
	if !strings.Contains(lines[1], "--resume fresh-123") {
		t.Fatalf("second call must resume fresh-123: %q", lines[1])
	}
	// Default allowlist + json format are always present.
	for _, want := range []string{"-p", "--output-format json", "--allowedTools"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("first call missing %q: %q", want, lines[0])
		}
	}
}

func TestClaudeCodeMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no claude
	SetDepGate(nil)
	out, err := runClaudeCode(tcStub("sessB"), claudeCodeIn{Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out.Result), "claude") || !strings.Contains(out.Result, "install") {
		t.Fatalf("expected a not-installed notice, got %q", out.Result)
	}
}
```

> **`tcStub`:** `runClaudeCode`'s first param is an `adk.ToolContext` used only for `.SessionID()` (and cwd via `fstools.CwdForContext`). `adk.ToolContext` is an **interface** (confirmed in Plan 2a). Provide a tiny test stub implementing just what's used — grep `core/adk` for the `ToolContext` interface method set, and mirror the minimal stub pattern already used in the repo's tool tests (e.g. how `agent`/`core/tools` tests supply a context). If a shared stub exists, reuse it; if the interface is large, prefer refactoring `runClaudeCode` to take `(sessionID, cwd string, in claudeCodeIn)` so the test needs no ToolContext at all (the tool closure then reads `ctx.SessionID()` + `fstools.CwdForContext(ctx)` and calls it) — this mirrors Plan 2a's `runFleetDispatch(reg, sessionID, in)` split and is the RECOMMENDED shape. Adjust the test calls accordingly.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/claudecode/ -v`
Expected: FAIL — package/symbols undefined.

- [ ] **Step 3: Implement the package**

Create `internal/claudecode/claudecode.go` (modelled on `internal/astgrep/astgrep.go`):

```go
// Package claudecode drives an external Claude Code CLI (`claude`) as a headless
// per-project worker: the `claude_code` tool runs `claude -p <task> --output-format
// json [--resume <id>] --allowedTools <allowlist> [--model <m>]` in the session's
// working directory, parses the JSON envelope, and remembers the returned
// session_id so later calls in the same driver session resume it. Modelled on
// internal/astgrep (a deps-gated, explicit-argv, JSON-output binary tool). Imports
// only stdlib + ADK + core/adk + internal/deps + core/tools — no agent/sessions/fleet.
package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/deps"
)

const binary = "claude"
const runTimeout = 30 * time.Minute // a coding task can be long; the fleet turn budget bounds cost elsewhere

// DefaultAllowedTools is the conservative launch allowlist for an unattended
// Driver: read/inspect + edit files + read-only git. Build/test/other commands
// are opt-in per project via the allowlist override. NEVER includes arbitrary
// Bash or --dangerously-skip-permissions. Rule syntax is Claude Code's
// --allowedTools grammar.
var DefaultAllowedTools = []string{
	"Read", "Edit", "Write", "Grep", "Glob",
	"Bash(git status:*)", "Bash(git diff:*)", "Bash(git log:*)", "Bash(ls:*)", "Bash(cat:*)",
}

// Requirement is the dependency descriptor: the `claude` binary on PATH.
func Requirement() deps.Requirement {
	return deps.Requirement{
		Command: binary,
		Label:   "Claude Code CLI (external fleet worker)",
		Install: deps.Install{Default: "npm install -g @anthropic-ai/claude-code"},
	}
}

type DepGate func(tc adk.ToolContext) string

var gate DepGate

func SetDepGate(g DepGate) { gate = g }

func ensureDep(tc adk.ToolContext) string {
	if gate != nil {
		return gate(tc)
	}
	if !deps.Present(binary) {
		return "the Claude Code CLI (`claude`) is not installed — install it (`" +
			Requirement().Install.Command() + "`) so this project's Driver can run."
	}
	return ""
}

// --- allowlist override hook -------------------------------------------------

var (
	allowMu       sync.RWMutex
	allowResolver func(sessionID string) []string
)

// SetAllowlistResolver installs a hook mapping a driver session to its project's
// allowlist override (nil/empty ⇒ DefaultAllowedTools). The server installs one
// backed by the session's collection; nil ⇒ always default.
func SetAllowlistResolver(f func(sessionID string) []string) {
	allowMu.Lock()
	allowResolver = f
	allowMu.Unlock()
}

func allowlistFor(sessionID string) []string {
	allowMu.RLock()
	f := allowResolver
	allowMu.RUnlock()
	if f != nil {
		if custom := f(sessionID); len(custom) > 0 {
			return custom
		}
	}
	return DefaultAllowedTools
}

// --- per-session claude session-id store -------------------------------------

var (
	sessMu   sync.Mutex
	sessions = map[string]string{} // omnis driver session id -> claude session_id
)

func rememberSession(sessionID, claudeID string) {
	if sessionID == "" || claudeID == "" {
		return
	}
	sessMu.Lock()
	sessions[sessionID] = claudeID
	sessMu.Unlock()
}

func resumeID(sessionID string) string {
	sessMu.Lock()
	defer sessMu.Unlock()
	return sessions[sessionID]
}

// ForgetSession drops the stored claude session id for a driver session. Called
// on session delete/archive so a reused id can't leak across tasks.
func ForgetSession(sessionID string) {
	sessMu.Lock()
	delete(sessions, sessionID)
	sessMu.Unlock()
}

// --- the tool ----------------------------------------------------------------

type claudeCodeIn struct {
	Task string `json:"task" jsonschema:"the self-contained coding task for the external Claude Code worker to carry out in this project's directory"`
}
type claudeCodeOut struct {
	Result string `json:"result"`
}

type envelope struct {
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// runClaudeCode is the handler, extracted so tests call it without ADK plumbing.
func runClaudeCode(tc adk.ToolContext, in claudeCodeIn) (claudeCodeOut, error) {
	if notice := ensureDep(tc); notice != "" {
		return claudeCodeOut{Result: notice}, nil
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		return claudeCodeOut{Result: "claude_code: `task` is required."}, nil
	}
	sessionID := tc.SessionID()
	cwd := fstools.CwdForContext(tc)

	args := []string{"-p", task, "--output-format", "json",
		"--allowedTools", strings.Join(allowlistFor(sessionID), ",")}
	if rid := resumeID(sessionID); rid != "" {
		args = append(args, "--resume", rid)
	}

	cctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, binary, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.Output()
	if err != nil {
		return claudeCodeOut{Result: fmt.Sprintf("claude_code: the Claude Code worker failed: %v", err)}, nil
	}
	var env envelope
	if e := json.Unmarshal(stdout, &env); e != nil {
		// Not JSON (older/other output) — return raw so nothing is lost.
		return claudeCodeOut{Result: strings.TrimSpace(string(stdout))}, nil
	}
	if env.SessionID != "" {
		rememberSession(sessionID, env.SessionID)
	}
	if strings.TrimSpace(env.Result) == "" {
		return claudeCodeOut{Result: "(the Claude Code worker returned no text)"}, nil
	}
	return claudeCodeOut{Result: env.Result}, nil
}

// Tools returns the claude_code tool group.
func Tools() []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "claude_code",
		Description: "Carry out a coding task in THIS project's directory by driving the external Claude Code worker. " +
			"Pass a single self-contained `task`; the worker sees the repository on disk (not this conversation), so " +
			"restate everything it needs. Call it again to continue — the worker keeps its context across your calls " +
			"within this session. It runs with a fixed permission allowlist (it cannot ask for more mid-task).",
	}, runClaudeCode)
	if err != nil {
		panic(fmt.Errorf("claude_code tool: %w", err))
	}
	return []tool.Tool{t}
}
```

> If Step 1's recommended refactor was taken (`runClaudeCode(sessionID, cwd, in)`), adjust the signature + the `Tools()` closure to pass `ctx.SessionID()` + `fstools.CwdForContext(ctx)`, and keep `ensureDep` in the closure. Keep the chosen shape consistent with the test.

- [ ] **Step 4: Run to verify it passes**

Run: `go test ./internal/claudecode/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/claudecode/
git commit -m "feat(fleet): claude_code tool driving the external Claude Code CLI"
```

---

### Task 2: Per-project allowlist field on the collection profile

**Files:** `internal/sessions/collections.go`, `internal/sessions/collections_profile_test.go`

**Interfaces produced:** `CollectionProfileData.ClaudeAllowedTools []string`, round-tripped by the profile read/write (exactly the Plan-1 `DependsOn` field pattern).

- [ ] **Step 1: Write the failing test** — add `TestCollectionProfileClaudeAllowedTools` to `collections_profile_test.go` mirroring `TestCollectionProfileFleetFields`: set a profile with `ClaudeAllowedTools: []string{"Read","Bash(go test:*)"}`, read back via `CollectionProfileFull`, assert the slice round-trips; assert all-empty still drops the entry.

- [ ] **Step 2: Run** `go test ./internal/sessions/ -run TestCollectionProfileClaudeAllowedTools -v` → FAIL (no field).

- [ ] **Step 3: Add the field** exactly as Plan 1 added `DependsOn`:
  - `collectionProfile`: `ClaudeAllowedTools []string \`json:"claude_allowed_tools,omitempty"\``.
  - `CollectionProfileData`: `ClaudeAllowedTools []string`.
  - Extend `isEmpty()` with `&& len(p.ClaudeAllowedTools) == 0`.
  - Thread it through the three mapping sites (`CollectionProfileFull` uses `cloneStrings`; `UpdateCollectionProfile` snapshot + build; `SetCollectionProfileData` build) with `cleanStrings` on write / `cloneStrings` on read, identically to `DependsOn`.

- [ ] **Step 4: Run** the focused test + `go test ./internal/sessions/...` → PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/sessions/collections.go internal/sessions/collections_profile_test.go
git commit -m "feat(fleet): per-project claude allowlist on the collection profile"
```

---

### Task 3: Route claude projects — `EngineSquad` + `Project.AllowedTools`

**Files:** `internal/fleet/fleet.go`, `internal/fleet/fleet_test.go`, `server/fleet.go`

**Interfaces produced:** `Project.AllowedTools []string`; `EngineSquad(EngineClaude)` → `("claude worker", true)`.

- [ ] **Step 1: Update the failing test** — change `TestEngineSquad` (Plan 2a asserted `EngineClaude` → `ok=false`): now assert `EngineSquad(EngineClaude)` returns `("claude worker", true)` and an unknown engine still returns `ok=false`. Add a case asserting `EngineOmnis` still → `("coding", true)`.

- [ ] **Step 2: Run** `go test ./internal/fleet/ -run TestEngineSquad -v` → FAIL.

- [ ] **Step 3: Implement**
  - In `internal/fleet/fleet.go` `EngineSquad`:
    ```go
    func EngineSquad(e Engine) (string, bool) {
    	switch e {
    	case EngineOmnis:
    		return "coding", true
    	case EngineClaude:
    		return "claude worker", true
    	}
    	return "", false
    }
    ```
  - Add `AllowedTools []string` to the `Project` struct (after `DependsOn`).
  - In `server/fleet.go` `collectFleetProjects`, map it: `AllowedTools: p.ClaudeAllowedTools` (from `sessions.CollectionProfileFull`).

- [ ] **Step 4: Run** `go test ./internal/fleet/... ./server/ -run 'EngineSquad|Fleet' -v` → PASS.

> `runFleetDispatch`'s claude-not-ready rejection (Plan 2a) keys on `EngineSquad` returning `ok=false`; now that claude returns `ok=true`, that branch simply stops firing for claude projects — no edit needed. Verify the Plan-2a test `TestFleetDispatchToolClaudeEngineNotReady` and UPDATE it: a claude project now enqueues (rename to `TestFleetDispatchToolClaudeEngineDispatches`, assert it enqueues one directive). This is a required test change, not optional.

- [ ] **Step 5: Commit**
```bash
git add internal/fleet/fleet.go internal/fleet/fleet_test.go server/fleet.go agent/fleet_dispatch_test.go
git commit -m "feat(fleet): route claude-engine projects to the Claude Worker squad"
```

---

### Task 4: Wire the tool group, deps gate, and allowlist resolver

**Files:** `agent/agent.go`, `agent/infrastructure.go`, `server/fleet_claude.go` (create), `server/spawn.go`

**Interfaces produced:** the `claude_code` tool-group key; the process-wide deps gate + allowlist resolver installed; `ForgetSession` on teardown.

- [ ] **Step 1: Tool-group key** — in `agent/agent.go`'s tool switch, add (beside `case "astgrep":`):
  ```go
  case "claude_code":
  	agentTools = append(agentTools, claudecode.Tools()...)
  ```
  and import `github.com/blouargant/omnis/internal/claudecode`.

- [ ] **Step 2: Deps gate** — in `agent/infrastructure.go`, beside `astgrep.SetDepGate(newAstgrepDepGate(askUserReg))`, add `claudecode.SetDepGate(newClaudeCodeDepGate(askUserReg))`. Create `newClaudeCodeDepGate` by copying `newAstgrepDepGate` (grep it — it's in `agent/`), swapping `astgrep.Requirement()` → `claudecode.Requirement()` and the tool name. (It runs `deps.Ensure` on first use: check PATH → ask user → install → recheck.)

- [ ] **Step 3: Allowlist resolver + ForgetSession** — create `server/fleet_claude.go`:
  ```go
  package main

  import (
  	"strings"

  	"github.com/blouargant/omnis/internal/claudecode"
  	"github.com/blouargant/omnis/internal/sessions"
  )

  // installClaudeAllowlistResolver maps a driver session to its project's claude
  // allowlist override: the session's collection profile's ClaudeAllowedTools
  // (empty ⇒ claudecode.DefaultAllowedTools). Called once at server startup.
  func installClaudeAllowlistResolver() {
  	claudecode.SetAllowlistResolver(func(sessionID string) []string {
  		meta, ok := registryLookupCollection(sessionID)
  		if !ok || strings.TrimSpace(meta) == "" {
  			return nil
  		}
  		return sessions.CollectionProfileFull(meta).ClaudeAllowedTools
  	})
  }
  ```
  > `registryLookupCollection(sessionID) (collectionName string, ok bool)`: the driver session is filed under the project collection (Plan 2a). Resolve its collection — grep the server for how a session's collection is read (`d.Registry.Get(id)` → `meta.Collection`, or `sessions.LoadConversationFile(id).Collection`). Implement this small helper inline using whichever the codebase exposes; it needs the process-wide registry, so this resolver must be installed where `d`/the registry is in scope (see Step 4). If the registry isn't reachable from a package-level func, make `installClaudeAllowlistResolver(d serverDeps)` take `d` and close over `d.Registry`.

  In `server/spawn.go` `forgetSessionState`, add `claudecode.ForgetSession(id)` (beside the existing `Forget` calls).

- [ ] **Step 4: Call the installer at startup** — in `server/main.go`, beside `installFleetResolver()` (Plan 1), add `installClaudeAllowlistResolver()` (or `installClaudeAllowlistResolver(d)` if it needs `d`). Match the surrounding call style.

- [ ] **Step 5: Verify** `go build ./... && go vet ./agent/... ./server/... ./internal/claudecode/... && go test ./agent/... ./server/... ./internal/claudecode/... ./internal/fleet/... ./internal/sessions/...` → all PASS.

- [ ] **Step 6: Commit**
```bash
git add agent/agent.go agent/infrastructure.go server/fleet_claude.go server/spawn.go server/main.go
git commit -m "feat(fleet): wire claude_code tool group + deps gate + allowlist resolver"
```

---

### Task 5: The Claude Worker agent + squad

**Files:** `registry/agents/claude_worker/{agent.json,instruction.md}`, `config/agents.json`

- [ ] **Step 1: Agent** — `registry/agents/claude_worker/agent.json`:
  ```json
  {
    "name": "claude_worker",
    "description": "Drives an external Claude Code CLI worker to carry out a coding task in one project's directory.",
    "model_ref": "hosted",
    "tools": ["claude_code"],
    "skills": []
  }
  ```
  > `model_ref`: this agent only relays a task to the external `claude` (the real coding reasoning happens in the subprocess), so a cheap tier is right — use the cheapest chat model ref the config defines (grep `config/models.json` for the ref used by other light agents like `session_search`/`k8s_editor`, commonly `"hosted"`; match an existing cheap ref, don't invent one). Not `leader: true` (it's a leaderless squad's single member).

- [ ] **Step 2: Instruction** — `registry/agents/claude_worker/instruction.md`:
  ```markdown
  # Claude Worker — external Claude Code driver

  You carry out ONE coding task in this project's working directory by driving an
  external Claude Code worker via the `claude_code` tool.

  - Pass the task you were given to `claude_code` as a single, self-contained
    `task`. The external worker sees the files on disk, not this conversation.
  - If the task needs several steps, call `claude_code` again — it keeps its
    context across your calls in this session.
  - The worker runs with a fixed permission allowlist and cannot ask for more
    mid-task. If it reports it was blocked from an action it needed (e.g. running
    the project's tests), say so plainly in your result so the user can widen this
    project's allowlist — do not try to work around it.
  - When the task is done, report what the worker changed. Keep it concise; the
    Conductor (or the user) reads your result.
  ```

- [ ] **Step 3: Register + squad** — in `config/agents.json`: add `"claude_worker"` to `agents`; add a leaderless squad:
  ```json
  {
    "name": "Claude Worker",
    "description": "Runs an external Claude Code worker on one project. Internal fleet engine — not a general chat squad.",
    "leader": "none",
    "members": ["claude_worker"]
  }
  ```
  > The squad name must lower-case to `"claude worker"` to match `EngineSquad(EngineClaude)` (Task 3). Squad names are lower-cased at resolution (Plan 2a confirmed), so `"Claude Worker"` → `"claude worker"` — matches. Leaderless single-member is the right shape (it just drives the external process; nothing to coordinate). Consider `"hidden": true` if it should not be offered in the chat squad picker (it's a machine-facing engine) — but it MUST stay resolvable by name for dispatch; hidden squads remain resolvable (Plan-1 Session Search precedent), so add `"hidden": true`.

- [ ] **Step 4: Verify** — add/extend a resolution test (like Plan 2a's `TestSquadsFleetConductorResolves`) asserting the `claude worker` squad resolves leaderless with the single `claude_worker` member, and that `claude_worker` resolves with the `claude_code` tool group. Run `go build ./... && go test ./agent/ -run 'Squad|Resolve' -v`. Confirm `config/agents.json` still parses (`python3 -c "import json,sys; json.load(open('config/agents.json'))"`).

- [ ] **Step 5: Commit**
```bash
git add registry/agents/claude_worker/ config/agents.json agent/squads_test.go
git commit -m "feat(fleet): add Claude Worker driver agent + leaderless squad"
```

---

## Self-Review

**Spec coverage (§8 claude engine, §4 decision 3, §14 phase 2):**
- External `claude` driven headless with `-p`/`--output-format json`/`--resume`/`--allowedTools`, cwd via `cmd.Dir` → Task 1. ✓
- Session-id captured + resumed within a driver session, task-scoped (dropped on teardown) → Task 1 store + Task 4 `ForgetSession`. ✓
- Conservative default allowlist + per-project override, never `--dangerously-skip-permissions` → Task 1 `DefaultAllowedTools` + Tasks 2-4 override chain. ✓ (the decided posture)
- claude projects dispatchable via the Plan-2a machinery with no dispatch-side change → Task 3 `EngineSquad` (the drain already routes via it). ✓
- Claude Worker as a thin driver agent/squad → Task 5. ✓
- deps gate on the `claude` binary (report-and-pause, no wrong-engine fallback) → Task 1 + Task 4. ✓
- Import-cycle + no-op contracts → `internal/claudecode` imports mirror `astgrep`; nil resolver ⇒ default allowlist; no claude projects ⇒ never launched. ✓
- **Deferred (out of scope, later plans):** worktree fork isolation (Plan 4); web-UI fields for engine/allowlist/depends_on (Plan 4); host-enforced topological parallelism; task-scoped mailbox addressing; the Agent-SDK worker alternative.

**Placeholder scan:** The grep-the-existing-pattern notes (`tcStub`/handler-split, `newAstgrepDepGate` copy, `registryLookupCollection`, the cheap `model_ref`) name the exact symbol to find + a concrete fallback (the recommended `runClaudeCode(sessionID,cwd,in)` split removes the ToolContext-stub question entirely). No `TBD`.

**Type consistency:** `EngineSquad` returns `(string,bool)` unchanged (Plan 2a call sites in `runFleetDispatch` + `fleetDriverOptions` still compile; they only branch on `ok`). `Project.AllowedTools` (Task 3) ← `CollectionProfileData.ClaudeAllowedTools` (Task 2) ← `collectFleetProjects` (Task 3). `claudecode.{SetDepGate,SetAllowlistResolver,ForgetSession,Tools}` used identically across the tool (Task 1), agent wiring (Task 4), and server wiring (Task 4). Squad name `"claude worker"` matches between `EngineSquad` (Task 3) and the lower-cased config squad (Task 5).
