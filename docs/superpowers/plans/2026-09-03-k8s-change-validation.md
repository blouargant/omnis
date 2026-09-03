# Kubernetes Change-Validation Layer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make it impossible for the Kubernetes squad's mutating agents to change a cluster without per-verb mechanical validation and an independent, unforgeable semantic review.

**Architecture:** Two generic extensions to the hooks engine (a `ask` decision + attempt counters + caller/attestation input + `fail_closed`) and one generic attestation tool group, then all Kubernetes knowledge expressed as configuration (`hooks.json` + a Python hook script) plus one new read-only agent. No kubernetes-specific logic enters the Go core.

**Tech Stack:** Go 1.x (`internal/hooks`, `agent/`, two new `internal/` packages), Python 3 stdlib (the hook script), JSON config, `kubectl` / `helm` CLIs.

**Spec:** [docs/superpowers/specs/2026-09-03-k8s-change-validation-design.md](../specs/2026-09-03-k8s-change-validation-design.md)

## Global Constraints

- **No kubernetes-specific logic in the Go core.** Per `CLAUDE.md`'s design contract: "No code changes required to retarget the agent." Every engine change must be domain-free; policy lives in `config/`.
- **Hook input/output stays Claude Code-compatible.** New fields are additive only; an existing Claude Code hook script must keep working unchanged.
- **`fail_closed` is opt-in**, never the default.
- **`N = 3`** attempts before escalating to the user, matching `agentCapGraceCalls` (`agent/budget_plugin.go:55`).
- **Escalation uses `askuser.Registry` with exactly two choices** (allow once / deny). Never the five permission scopes — a persisted "allow always" would permanently disable the guard.
- **No `ask_user` registry ⇒ deny** (fail safe), mirroring the budget gate.
- **A missing or non-`APPROVED` attestation is a terminal refusal** — it never escalates to `ask`, whatever the counters say.
- **No-op contract:** with no `hooks.json` in any layer, every change in this plan must be byte-identical to current behaviour.
- **Python 3 stdlib only** in the hook script (no pip dependencies).
- Docs in the repo are **English only**.

---

## File Structure

| File | Responsibility | Milestone |
|---|---|---|
| `internal/hooks/hooks.go` | Add `Command.FailClosed`. | M1 |
| `internal/hooks/run.go` | `fail_closed` blocking; `DecisionAsk`; `Input.Attempt` / `Consecutive` / `AgentName` / `Attestations`. | M1, M2 |
| `agent/tool_chain.go` (new) | The one place the before-tool callback order is written down. | M1 |
| `agent/build_subagents.go` | Use the chain helper. | M1 |
| `agent/build_plugins.go` | Root plugin order: hooks before permissions. | M1 |
| `internal/hookstate/hookstate.go` (new) | Per-session attempt counters + the canonical args hash. | M2 |
| `agent/hooks_plugin.go` | Wire registry + counters + caller + attestations; handle `DecisionAsk`. | M2, M3 |
| `agent/infrastructure.go` | Hold the two new process-wide stores. | M2, M3 |
| `internal/attest/attest.go` (new) | Attestation store. | M3 |
| `internal/attest/tools.go` (new) | The `record_validation` tool. | M3 |
| `agent/agent.go` | `case "attest":` tool group. | M3 |
| `config/hooks/k8s-validate.py` (new) | All Kubernetes policy. | M4 |
| `config/hooks.json` (new) | Hook declaration. | M4 |
| `.goreleaser.yaml`, `packaging/**`, `scripts/build_wheels.py` | Ship `config/hooks/`. | M4 |
| `packaging/k8s_validate_test.go` (new) | Behaviour of the shipped script. | M4 |
| `packaging/hooks_assets_test.go` (new) | The shipped hook exists and is packaged. | M4 |
| `registry/agents/k8s_validator/{agent.json,instruction.md}` (new) | The judgment agent. | M5 |
| `config/agents.json`, `registry/agents/k8s_{editor,cleaner}/agent.json` | Enable + nest the validator. | M5 |
| `CLAUDE.md`, `internal/features/FEATURES.md` | Documentation upkeep. | M6 |

## Milestones

| # | Deliverable | Ships alone? |
|---|---|---|
| **M1** | Two pre-existing defects fixed: hooks can no longer fail open, and a hook's verdict precedes the permission card. | **Yes** — pure bug fixes, no new feature. |
| **M2** | A hook can ask the user, and knows its attempt number and its caller. | Yes — inert without a `hooks.json`. |
| **M3** | A designated reviewer agent can record an unforgeable verdict a hook reads. | Yes — inert until mounted. |
| **M4** | Kubernetes validation actually enforced. | Yes — but useless without M5's agent, which it then refuses on (by design, §7.6). |
| **M5** | `k8s_validator` exists and is nested under both mutating agents. | Completes the feature. |
| **M6** | Docs, and `fail_closed` visible + editable in Settings → Hooks. | Yes. |

**Order matters:** M4 refuses every mutation until M5 lands (a missing attestation is terminal). Do not ship M4 to users without M5.

---

# Milestone 1 — Fix the latent fail-open and ordering defects

### Task 1: `fail_closed` — a hook that cannot run must block

**Files:**
- Modify: `internal/hooks/hooks.go:47-52` (the `Command` struct)
- Modify: `internal/hooks/run.go:131-160` (the three fail-open paths in `Run`)
- Test: `internal/hooks/run_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `hooks.Command.FailClosed bool` (JSON key `fail_closed`), honoured by `(*Config).Run`.

**Why:** `internal/hooks/run.go:99-106` documents that timeouts and non-2 exit codes "do not stop the turn". So a hook script that times out, crashes, or is **missing** (shell exit 127 — the packaging risk) lets the tool call proceed unvalidated. A guard whose absence is undetectable is not a guard.

- [ ] **Step 1: Write the failing tests**

Add to `internal/hooks/run_test.go`. Note the new `runOne` helper: the existing `run` helper cannot express `Timeout` or `FailClosed`.

```go
// runOne parses a single-command config from c and runs it, so a test can set
// fields (timeout, fail_closed) the string-based `run` helper cannot express.
func runOne(t *testing.T, event, subject string, c Command, in Input, defaultTimeout time.Duration) Outcome {
	t.Helper()
	f := File{Hooks: map[string][]Matcher{event: {{Matcher: subject, Hooks: []Command{c}}}}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg.Run(context.Background(), event, subject, in, t.TempDir(), defaultTimeout)
}

// A fail_closed hook that crashes must BLOCK. Without this, a validation hook
// whose script has a syntax error silently stops validating.
func TestFailClosedBlocksOnNonZeroExit(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "exit 1", FailClosed: true},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock", out.Decision)
	}
	if out.Reason == "" {
		t.Fatal("a fail-closed block must carry a reason naming the cause")
	}
}

// The packaging failure mode: hooks.json ships but the script does not, so the
// shell returns 127. This must block, not proceed.
func TestFailClosedBlocksOnMissingCommand(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "omnis-definitely-not-a-real-binary-xyz", FailClosed: true},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock", out.Decision)
	}
}

func TestFailClosedBlocksOnTimeout(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "sleep 5", Timeout: 1, FailClosed: true},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock", out.Decision)
	}
}

// The no-op contract: without the flag, Claude Code semantics are unchanged.
func TestWithoutFailClosedNonZeroExitStillProceeds(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "exit 1"},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionProceed {
		t.Fatalf("decision = %v, want DecisionProceed (unchanged default)", out.Decision)
	}
}
```

Add `"encoding/json"` to that file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/hooks/ -run 'FailClosed|WithoutFailClosed' -v`
Expected: FAIL — `Command` has no field `FailClosed`, so the package does not compile.

- [ ] **Step 3: Add the field**

In `internal/hooks/hooks.go`, replace the `Command` struct:

```go
// Command is one hook command entry. Only `type: "command"` is supported (the
// sole kind Claude Code defines).
type Command struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"` // seconds; 0 = engine default
	// FailClosed makes a command whose execution yields no usable verdict —
	// a timeout, a non-zero non-2 exit (a crashed or MISSING script: the shell
	// returns 127), or a safety-floor refusal — block the action instead of
	// proceeding. Claude Code has no such flag and its default (proceed) is
	// kept, so this is opt-in: a guard hook sets it, an advisory hook does not.
	// Without it a validation hook that fails to run stops validating silently,
	// which is the one failure mode a guard must not have.
	FailClosed bool `json:"fail_closed,omitempty"`
}
```

- [ ] **Step 4: Honour it in `Run`**

In `internal/hooks/run.go`, add above `addsContext`:

```go
// failClosedBlock turns a command that produced no usable verdict into a block,
// for commands that opted in via fail_closed. why names the cause so the model
// (and the user) can tell "the guard refused this" from "the guard is broken".
func failClosedBlock(out *Outcome, event, command, why string) {
	out.Decision = DecisionBlock
	if out.Reason == "" {
		out.Reason = fmt.Sprintf("hook did not complete (%s) and is declared fail_closed; refusing the action", why)
	}
	fmt.Fprintf(os.Stderr, "[hooks] %s: fail_closed block (%s): %s\n", event, why, command)
}
```

Then in the `for _, cmd := range cmds` loop, extend the three non-blocking branches (currently `internal/hooks/run.go:131-160`):

```go
		if res.Blocked {
			fmt.Fprintf(os.Stderr, "[hooks] %s: command refused by safety floor: %s\n", event, res.Stderr)
			if cmd.FailClosed {
				failClosedBlock(&out, event, cmd.Command, "refused by the safety floor")
			}
			continue
		}
		if res.TimedOut {
			fmt.Fprintf(os.Stderr, "[hooks] %s: command timed out: %s\n", event, cmd.Command)
			if cmd.FailClosed {
				failClosedBlock(&out, event, cmd.Command, "timed out")
			}
			continue
		}
```

and, in the `res.ExitCode != 0` branch, after the existing stderr log:

```go
			if cmd.FailClosed {
				failClosedBlock(&out, event, cmd.Command, fmt.Sprintf("exited %d", res.ExitCode))
			}
			continue
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/hooks/ -v`
Expected: PASS, including the pre-existing tests (`TestRunExitTwoBlocks` and friends must be untouched).

- [ ] **Step 6: Commit**

```bash
git add internal/hooks/hooks.go internal/hooks/run.go internal/hooks/run_test.go
git commit -m "fix(hooks): fail_closed so a hook that cannot run blocks instead of proceeding"
```

---

### Task 2: Run hooks before permissions

**Files:**
- Create: `agent/tool_chain.go`
- Create: `agent/tool_chain_test.go`
- Modify: `agent/build_subagents.go:196-215`
- Modify: `agent/build_plugins.go:71-88`

**Interfaces:**
- Consumes: nothing.
- Produces: `beforeToolChain(eventsCB, hooksCB, permCB, budgetCB llmagent.BeforeToolCallback) []llmagent.BeforeToolCallback` — the single source of truth for chain order.

**Why:** two defects, one cause. (a) With permissions first, a hook's refusal reaches the agent *after* the user approved the call — three permission cards for three failed validations, training reflexive clicking and degrading the permission layer. (b) **Corrected after review — do not restate the old claim.** An earlier draft held that the reorder also revives hooks' documented `permissionDecision: "allow"` bypass (`internal/hooks/run.go:76`). It does not: `DecisionAllow` is dead because **nothing in `agent/` consumes it** — `hookToolCallbacks` returns non-nil only on `out.Blocked()`, and a `nil` return means "proceed", not "skip the gate". Reordering is a *precondition* for honouring `"allow"`, never the feature. Defect (a) is on its own a sufficient reason for this task. Blast radius is nil: no `hooks.json` exists in any layer.

- [ ] **Step 1: Write the failing test**

Create `agent/tool_chain_test.go`:

```go
package agent

import (
	"testing"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/adk"
)

// mark returns a BeforeToolCallback that appends name to log when invoked.
func mark(log *[]string, name string) llmagent.BeforeToolCallback {
	return func(adk.ToolContext, tool.Tool, map[string]any) (map[string]any, error) {
		*log = append(*log, name)
		return nil, nil
	}
}

// Hooks must run BEFORE permissions: a PreToolUse hook that refuses a call must
// do so without the user having already approved it. Budget stays LAST so a call
// refused by a hook or the user is not charged.
func TestBeforeToolChainRunsHooksBeforePermissions(t *testing.T) {
	var log []string
	chain := beforeToolChain(
		mark(&log, "events"),
		mark(&log, "hooks"),
		mark(&log, "perms"),
		mark(&log, "budget"),
	)
	for _, cb := range chain {
		if _, err := cb(nil, nil, nil); err != nil {
			t.Fatalf("callback error: %v", err)
		}
	}
	want := []string{"events", "hooks", "perms", "budget"}
	if len(log) != len(want) {
		t.Fatalf("chain ran %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("chain ran %v, want %v", log, want)
		}
	}
}

// A nil layer is skipped, not appended — this is what makes the reorder a
// byte-identical no-op for a build with no hooks engine.
func TestBeforeToolChainSkipsNilLayers(t *testing.T) {
	var log []string
	chain := beforeToolChain(mark(&log, "events"), nil, mark(&log, "perms"), nil)
	if len(chain) != 2 {
		t.Fatalf("chain length = %d, want 2", len(chain))
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./agent/ -run TestBeforeToolChain -v`
Expected: FAIL — `undefined: beforeToolChain`.

- [ ] **Step 3: Create the helper**

Create `agent/tool_chain.go`:

```go
package agent

import "google.golang.org/adk/agent/llmagent"

// beforeToolChain assembles a sub-agent's BeforeToolCallback chain, and is the
// single place that order is written down. The first non-nil return
// short-circuits the tool, so the order IS the policy.
//
// hooks BEFORE permissions. With permissions first, a PreToolUse hook that
// refuses a call was consulted only after the user had already approved it: on
// three failed validation attempts the user clicked "allow" three times for
// calls that were then rejected, which trains reflexive approval and degrades
// the permission layer — the only protection that existed before the validation
// work. That is what this order fixes, and it is reason enough on its own.
//
// It does NOT make hooks' documented permissionDecision:"allow" bypass
// (internal/hooks/run.go:76) work. That is still dead: nothing in agent/
// consumes hooks.DecisionAllow — hookToolCallbacks returns non-nil only on
// out.Blocked(), and returning nil merely means "proceed", which is not a
// signal the gate can act on. Honouring "allow" would additionally require the
// hook callback to tell the gate to skip (e.g. by seeding the approval cache).
// This order is a precondition for that, not the feature.
//
// budget LAST: a call already refused by a hook or by the user must not be
// charged to the turn's budget.
//
// eventsCB is appended unconditionally (it is the observability bridge and must
// see every call); the other three are skipped when nil, which is what makes a
// build with no hooks engine or no ceiling byte-identical to before.
func beforeToolChain(eventsCB, hooksCB, permCB, budgetCB llmagent.BeforeToolCallback) []llmagent.BeforeToolCallback {
	chain := []llmagent.BeforeToolCallback{eventsCB}
	for _, cb := range []llmagent.BeforeToolCallback{hooksCB, permCB, budgetCB} {
		if cb != nil {
			chain = append(chain, cb)
		}
	}
	return chain
}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./agent/ -run TestBeforeToolChain -v`
Expected: PASS.

- [ ] **Step 5: Use the helper in `build_subagents.go`**

Replace the block at `agent/build_subagents.go:196-215` (from the `// A sub-agent runs in agenttool's own plugin-less runner` comment through the `budgetBeforeTool` append) with:

```go
		// A sub-agent runs in agenttool's own plugin-less runner, so the
		// runner-level permissions AND hooks plugins never see its tool calls —
		// attach their tool-level callbacks here so a sub-agent's
		// Edit/Write/Bash/MCP calls are gated + hooked exactly like the leader's.
		// Order lives in one place: see beforeToolChain (agent/tool_chain.go).
		beforeTool := beforeToolChain(callbacks.BeforeTool, hooksBeforeTool, permGate, budgetBeforeTool)
```

Leave the `afterTool` assembly below it untouched.

- [ ] **Step 6: Reorder the root plugin list**

In `agent/build_plugins.go`, move the `buildHooksPlugin` block **above** the `permPlugin` block, and correct both comments:

```go
	// Claude Code-style lifecycle hooks. The per-squad runner plugin carries the
	// blocking/injecting hooks (PreToolUse/PostToolUse/UserPromptSubmit/Stop) and
	// reads the shared hot-reloading engine; the fire-and-forget lifecycle
	// listeners are wired once on the bus by Infrastructure.Hooks. The router
	// squad mounts none (hooks fire on the answering squad — see buildHooksPlugin).
	//
	// Mounted BEFORE the permission gate, for the reason in beforeToolChain
	// (agent/tool_chain.go): a hook's refusal must not arrive after the user has
	// already approved the call. Keep these two in this order. (It does not make
	// permissionDecision:"allow" work — see that comment.)
	if hp, herr := buildHooksPlugin(hooksEngine, isRouterSquad); herr == nil && hp != nil {
		plugins = append(plugins, hp)
	}
	// The permission gate is built once per squad by the caller (so the same
	// enforcement — one approval cache/asker — also attaches to sub-agents) and
	// passed in as a runner plugin here, mounted after the hooks so a hook that
	// already refused the call never reaches the user as a prompt.
	if permPlugin != nil {
		plugins = append(plugins, permPlugin)
	}
```

- [ ] **Step 7: Guard the root order at the source level**

`buildPlugins` needs a full `*Infrastructure` to call, so its order is guarded the way `internal/adkguard` guards ADK forms — by reading the source. Append to `agent/tool_chain_test.go`:

```go
// The root's plugin order is a list literal inside buildPlugins, which needs a
// whole Infrastructure to build — so it is guarded at the source level, like
// internal/adkguard guards raw ADK forms. This asserts the ordering only; the
// behavioural guarantee is TestBeforeToolChainRunsHooksBeforePermissions.
func TestRootPluginOrderMountsHooksBeforePermissions(t *testing.T) {
	src, err := os.ReadFile("build_plugins.go")
	if err != nil {
		t.Fatalf("read build_plugins.go: %v", err)
	}
	s := string(src)
	hooks := strings.Index(s, "plugins = append(plugins, hp)")
	perms := strings.Index(s, "plugins = append(plugins, permPlugin)")
	if hooks < 0 || perms < 0 {
		t.Fatal("could not find both plugin appends — update this guard with the code")
	}
	if hooks > perms {
		t.Fatal("the hooks plugin must be appended BEFORE the permission plugin; see agent/tool_chain.go for why")
	}
}
```

Add `"os"` and `"strings"` to that file's imports.

- [ ] **Step 8: Run the full suite**

Run: `go test ./agent/... ./internal/hooks/... && go vet ./agent/... ./internal/hooks/...`
Expected: PASS, no vet findings.

- [ ] **Step 9: Commit**

```bash
git add agent/tool_chain.go agent/tool_chain_test.go agent/build_subagents.go agent/build_plugins.go
git commit -m "fix(hooks): run PreToolUse before the permission gate

Stops a hook refusal from arriving after the user has already approved the
call, which on repeated validation failures would train reflexive approval
and degrade the permission layer itself.

This does NOT revive permissionDecision:\"allow\": nothing in agent/ consumes
hooks.DecisionAllow, so that stays dead code. The reorder is a precondition
for honouring it, not the feature."
```

---

# Milestone 2 — Engine: ask, counters, caller

### Task 3: The `ask` decision

**Files:**
- Modify: `internal/hooks/run.go:65-78` (the `Decision` consts), `:213-228` (`applyJSONOutput`)
- Test: `internal/hooks/run_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `hooks.DecisionAsk`; `Outcome.Decision == DecisionAsk` meaning "put this to the user"; aggregation `Block > Ask > Allow > Proceed`.

- [ ] **Step 1: Write the failing tests**

```go
func TestPermissionDecisionAskYieldsAsk(t *testing.T) {
	skipOnWindows(t)
	cmd := `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"validation failed 3x"}}'`
	out := run(t, PreToolUse, "Bash", cmd, Input{ToolName: "Bash"})
	if out.Decision != DecisionAsk {
		t.Fatalf("decision = %v, want DecisionAsk", out.Decision)
	}
	if out.Reason != "validation failed 3x" {
		t.Fatalf("reason = %q, want the ask reason carried through", out.Reason)
	}
}

// Aggregation is Block > Ask > Allow > Proceed. A second hook must never soften
// another hook's deny into a question.
func TestDenyBeatsAsk(t *testing.T) {
	skipOnWindows(t)
	cfg, err := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[
		{"command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\",\"permissionDecisionReason\":\"unsure\"}}'"},
		{"command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"deny\",\"permissionDecisionReason\":\"never\"}}'"}
	]}]}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := cfg.Run(context.Background(), PreToolUse, "Bash", Input{ToolName: "Bash"}, t.TempDir(), 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock (deny must win over ask)", out.Decision)
	}
}

// And Ask must win over Allow, or a permissive hook would silently cancel a
// guard hook's escalation.
//
// THE ORDER MATTERS AND MUST STAY [ask, allow]. With [allow, ask] this test is
// vacuous: `allow` runs first while Decision is still Proceed, so the amended
// `!= DecisionAsk` clause is never evaluated against Ask, and `ask` then wins via
// its own unrelated `!= DecisionBlock` guard — reverting the allow guard would not
// fail the test. Ask-then-allow is what actually exercises the new clause.
func TestAskBeatsAllow(t *testing.T) {
	skipOnWindows(t)
	cfg, err := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[
		{"command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\",\"permissionDecisionReason\":\"check\"}}'"},
		{"command":"echo '{\"hookSpecificOutput\":{\"permissionDecision\":\"allow\"}}'"}
	]}]}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out := cfg.Run(context.Background(), PreToolUse, "Bash", Input{ToolName: "Bash"}, t.TempDir(), 10*time.Second)
	if out.Decision != DecisionAsk {
		t.Fatalf("decision = %v, want DecisionAsk (ask must win over allow)", out.Decision)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/hooks/ -run 'Ask' -v`
Expected: FAIL — `undefined: DecisionAsk`.

- [ ] **Step 3: Add the const and the aggregation**

In `internal/hooks/run.go`, add to the `Decision` const block:

```go
	// DecisionAsk means a hook wants the user to decide (PreToolUse
	// permissionDecision="ask"). Only PreToolUse has a user to ask, so every
	// other consumer ignores it — Blocked() stays false for it deliberately.
	DecisionAsk
```

Add a helper beside `Blocked`:

```go
// Asks reports whether the outcome defers the decision to the user.
func (o Outcome) Asks() bool { return o.Decision == DecisionAsk }
```

In `applyJSONOutput`, replace the `switch strings.ToLower(hs.PermissionDecision)` block:

```go
		// Aggregation across several matched hooks: Block > Ask > Allow > Proceed.
		// A permissive hook must never cancel another hook's deny or escalation.
		switch strings.ToLower(hs.PermissionDecision) {
		case "deny":
			out.Decision = DecisionBlock
			if out.Reason == "" {
				out.Reason = firstNonEmpty(hs.PermissionDecisionReason, jo.Reason)
			}
		case "ask":
			if out.Decision != DecisionBlock {
				out.Decision = DecisionAsk
				if out.Reason == "" {
					out.Reason = firstNonEmpty(hs.PermissionDecisionReason, jo.Reason)
				}
			}
		case "allow":
			if out.Decision != DecisionBlock && out.Decision != DecisionAsk {
				out.Decision = DecisionAllow
			}
		}
```

**And the sibling legacy switch, a few lines below, needs the same guard.** `applyJSONOutput`
has a *second* path to the same `Outcome` — the top-level `decision` field (`approve`/`block`),
the older Claude Code protocol. Its `approve` case guards only against `DecisionBlock`, so a
hook using the legacy protocol can downgrade another hook's `ask` to `allow` — the exact
failure the aggregation rule exists to prevent, reached through the other protocol. Amend it:

```go
	case "approve":
		if out.Decision != DecisionBlock && out.Decision != DecisionAsk && event == PreToolUse {
			out.Decision = DecisionAllow
		}
```

and cover it with a test in the same shape as `TestAskBeatsAllow`, whose first hook emits
`hookSpecificOutput.permissionDecision: "ask"` and whose second emits the legacy
`{"decision":"approve"}`, asserting the outcome stays `DecisionAsk`.

**Also update `Run()`'s own doc comment** (`internal/hooks/run.go`, the "Aggregation:" paragraph).
It describes only deny/allow and still says non-zero exits and timeouts "do not stop the turn",
which Task 1 made conditional on `fail_closed`. One rewrite should now state both: the full
`Block > Ask > Allow > Proceed` order, and the `fail_closed` exception.

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/hooks/ -v`
Expected: PASS (all, including pre-existing).

- [ ] **Step 5: Commit**

```bash
git add internal/hooks/run.go internal/hooks/run_test.go
git commit -m "feat(hooks): implement permissionDecision \"ask\" (documented since day one, never wired)"
```

---

### Task 4: `internal/hookstate` — attempt counters

**Files:**
- Create: `internal/hookstate/hookstate.go`
- Create: `internal/hookstate/hookstate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `hookstate.New() *Store`
  - `(*Store).Attempt(sid, tool string, args map[string]any) (attempt, consecutive int)`
  - `(*Store).RecordOutcome(sid, tool string, blocked bool)`
  - `(*Store).Forget(sid string)`
  - `hookstate.HashArgs(args map[string]any) string`

- [ ] **Step 1: Write the failing tests**

Create `internal/hookstate/hookstate_test.go`:

```go
package hookstate

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The exact counter answers "how many times has this same command been tried",
// so a genuinely corrected command starts over at 1 — the counter only climbs
// when the agent retries the identical thing.
func TestAttemptCountsIdenticalArgsAndResetsOnChange(t *testing.T) {
	s := New()
	a1, _ := s.Attempt("sess", "Bash", map[string]any{"command": "kubectl apply -f a.yaml"})
	a2, _ := s.Attempt("sess", "Bash", map[string]any{"command": "kubectl apply -f a.yaml"})
	a3, _ := s.Attempt("sess", "Bash", map[string]any{"command": "kubectl apply -f b.yaml"})
	if a1 != 1 || a2 != 2 {
		t.Fatalf("attempts = %d,%d want 1,2", a1, a2)
	}
	if a3 != 1 {
		t.Fatalf("changed args attempt = %d, want 1 (a corrected command gets a fresh budget)", a3)
	}
}

// The coarse counter closes the hole the exact one leaves: an agent retrying
// endlessly DIFFERENT but still-wrong commands would otherwise never escalate.
func TestConsecutiveCountsBlocksAcrossDifferentArgs(t *testing.T) {
	s := New()
	for i, cmd := range []string{"a", "b", "c"} {
		_, cons := s.Attempt("sess", "Bash", map[string]any{"command": cmd})
		if cons != i {
			t.Fatalf("call %d saw consecutive = %d, want %d", i, cons, i)
		}
		s.RecordOutcome("sess", "Bash", true)
	}
}

func TestConsecutiveResetsOnASuccessfulCall(t *testing.T) {
	s := New()
	s.Attempt("sess", "Bash", map[string]any{"command": "a"})
	s.RecordOutcome("sess", "Bash", true)
	s.Attempt("sess", "Bash", map[string]any{"command": "b"})
	s.RecordOutcome("sess", "Bash", false)
	_, cons := s.Attempt("sess", "Bash", map[string]any{"command": "c"})
	if cons != 0 {
		t.Fatalf("consecutive = %d, want 0 after a non-blocked call", cons)
	}
}

// A nil args map must hash like an empty one: nil encodes as JSON `null`, while
// the Python hook script always sends an object, so without normalising, a
// no-argument tool call would never match its own attestation.
func TestHashArgsTreatsNilLikeEmpty(t *testing.T) {
	if HashArgs(nil) != HashArgs(map[string]any{}) {
		t.Fatal("HashArgs(nil) must equal HashArgs(empty) — the Python side always sends an object")
	}
	sum := sha256.Sum256([]byte(`{}`))
	if HashArgs(nil) != hex.EncodeToString(sum[:]) {
		t.Fatal("HashArgs(nil) must hash the canonical empty object")
	}
}

// Forget must clear BOTH counters. A simplification that dropped the second loop
// would pass every other test in this file, so it gets its own.
func TestForgetClearsTheConsecutiveCounterToo(t *testing.T) {
	s := New()
	s.Attempt("sess", "Bash", map[string]any{"command": "a"})
	s.RecordOutcome("sess", "Bash", true)
	s.RecordOutcome("sess", "Bash", true)
	s.Forget("sess")
	if _, cons := s.Attempt("sess", "Bash", map[string]any{"command": "b"}); cons != 0 {
		t.Fatalf("consecutive = %d after Forget, want 0", cons)
	}
}

func TestSessionsAreIsolatedAndForgettable(t *testing.T) {
	s := New()
	s.Attempt("a", "Bash", map[string]any{"command": "x"})
	if n, _ := s.Attempt("b", "Bash", map[string]any{"command": "x"}); n != 1 {
		t.Fatalf("other session attempt = %d, want 1", n)
	}
	s.Forget("a")
	if n, _ := s.Attempt("a", "Bash", map[string]any{"command": "x"}); n != 1 {
		t.Fatalf("after Forget attempt = %d, want 1", n)
	}
}

// The hash must not depend on map iteration order, or every call would look new.
// The Python hook script recomputes this hash with
// json.dumps(..., sort_keys=True, separators=(",",":")), which does not escape
// HTML. Go's encoding/json escapes &, < and > by default, and every compound
// shell command contains "&&" — so a regression back to json.Marshal would make
// the two sides disagree on exactly the commands that matter most, and every
// compound command would be refused as "not reviewed". Pin the exact bytes.
func TestHashArgsMatchesPlainJSONWithoutHTMLEscaping(t *testing.T) {
	args := map[string]any{"command": "kubectl get pods && kubectl delete pod x"}
	canonical := `{"command":"kubectl get pods && kubectl delete pod x"}`
	sum := sha256.Sum256([]byte(canonical))
	if got, want := HashArgs(args), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("HashArgs = %s, want %s — the hash of the UN-escaped canonical JSON %s", got, want, canonical)
	}
}

func TestHashArgsIsStableAcrossKeyOrder(t *testing.T) {
	h1 := HashArgs(map[string]any{"command": "x", "timeout": 5})
	h2 := HashArgs(map[string]any{"timeout": 5, "command": "x"})
	if h1 != h2 {
		t.Fatalf("hash is order-dependent: %q vs %q", h1, h2)
	}
	if h1 == HashArgs(map[string]any{"command": "y"}) {
		t.Fatal("different args must hash differently")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/hookstate/ -v`
Expected: FAIL — no such package / no non-test Go files.

- [ ] **Step 3: Implement the store**

Create `internal/hookstate/hookstate.go`:

```go
// Package hookstate holds the per-session state the hooks engine exposes to
// hook commands: how many times a tool call has been attempted with identical
// arguments, and how many calls of that tool were refused back-to-back.
//
// It is deliberately domain-free. The engine only *reports* these numbers to a
// hook script; the script decides what to do with them, which is what keeps the
// mechanism generic rather than a Kubernetes feature in disguise.
//
// The store is process-wide (held on agent.Infrastructure beside SteerStore /
// GoalStore / Budget, so it survives a hot-reload). That matters: the hook
// callbacks are built independently per sub-agent, so without a shared store
// k8s_editor and its leader would count attempts on the same command
// separately and a delegation bounce would silently reset the counter.
package hookstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// Store is a concurrency-safe counter set. The zero value is not usable; build
// one with New.
type Store struct {
	mu    sync.Mutex
	exact map[string]int // sid \x00 tool \x00 argsHash -> attempts
	cons  map[string]int // sid \x00 tool            -> consecutive blocked calls
}

// New returns an empty Store.
func New() *Store {
	return &Store{exact: map[string]int{}, cons: map[string]int{}}
}

// Attempt records one attempt of (sid, tool, args) and returns the attempt
// number — 1 on the first — together with the consecutive-blocked count
// accumulated by PREVIOUS calls of that tool.
//
// Degrade contract: with no store or no session id it reports (1, 0) on every
// call, so a hook always sees a brand-new attempt and never escalates. That
// degrades the ESCALATION, not the blocking: these numbers only ever choose
// between refusing and asking the user — never between refusing and allowing —
// so an unwired store means "refuse indefinitely", not "let it through". The
// opposite choice (reporting a huge count when unwired) was rejected: it would
// spam the user with a card on every tool call. In practice the store is built
// unconditionally on Infrastructure, so nil occurs only in tests and examples.
//
// The split in update timing is deliberate and cannot be collapsed: an attempt
// on identical arguments is knowable before the hook runs, whereas a block is
// only knowable after, so the consecutive count is advanced by RecordOutcome.
func (s *Store) Attempt(sid, tool string, args map[string]any) (attempt, consecutive int) {
	if s == nil || sid == "" {
		return 1, 0
	}
	k := key(sid, tool, HashArgs(args))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exact[k]++
	return s.exact[k], s.cons[key(sid, tool, "")]
}

// RecordOutcome advances the consecutive-blocked counter once the verdict is
// known: a block increments it, anything else resets it.
func (s *Store) RecordOutcome(sid, tool string, blocked bool) {
	if s == nil || sid == "" {
		return
	}
	k := key(sid, tool, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	if blocked {
		s.cons[k]++
		return
	}
	delete(s.cons, k)
}

// Forget drops every counter for a session. Called on session end.
func (s *Store) Forget(sid string) {
	if s == nil || sid == "" {
		return
	}
	prefix := sid + "\x00"
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.exact {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(s.exact, k)
		}
	}
	for k := range s.cons {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(s.cons, k)
		}
	}
}

// HashArgs is the canonical hash of a tool call's arguments. It is the attempt
// key and, in internal/attest, the attestation subject; the Python hook script
// computes the same value independently, so the two encodings must agree exactly.
// encoding/json sorts object keys, so the hash does not depend on map iteration
// order.
//
// SetEscapeHTML(false) is load-bearing, not a style choice. Go escapes &, < and >
// in strings by default, while the script's
// json.dumps(..., sort_keys=True, separators=(",",":")) does not — and EVERY
// compound shell command contains "&&". With the default escaping the two sides
// would disagree on precisely the commands that matter most, no attestation would
// ever match, and every compound command would be refused as "not reviewed" —
// a failure that looks intermittent rather than systematic.
func HashArgs(args map[string]any) string {
	if args == nil {
		// A nil map encodes as JSON `null`, but the Python hook script always
		// receives an object — `{}` for a call with no arguments — so the two
		// sides would hash differently for exactly that case. Normalise.
		args = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(args); err != nil {
		// Unencodable args cannot be identified at all, so they all share one
		// bucket. Practically unreachable: tool arguments arrive as decoded JSON.
		return ""
	}
	// Encode appends a newline; the Python side produces none.
	sum := sha256.Sum256(bytes.TrimRight(buf.Bytes(), "\n"))
	return hex.EncodeToString(sum[:])
}

func key(sid, tool, hash string) string {
	return sid + "\x00" + tool + "\x00" + hash
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/hookstate/ -v && go vet ./internal/hookstate/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/hookstate/
git commit -m "feat(hookstate): per-session hook attempt counters"
```

---

### Task 5: Wire ask, counters and caller into the hook callbacks

**Files:**
- Modify: `internal/hooks/run.go:17-44` (the `Input` struct)
- Modify: `agent/hooks_plugin.go:87-115` (`buildHooksPlugin`), `:156-200` (`hookToolCallbacks`)
- Modify: `agent/squad.go:246-249` (the call site)
- Modify: `agent/infrastructure.go` (new field + construction + session-end Forget)
- Create: `agent/hooks_ask.go`
- Test: `agent/hooks_ask_test.go`

**Interfaces:**
- Consumes: `hookstate.New`, `(*Store).Attempt`, `(*Store).RecordOutcome`, `hooks.DecisionAsk`.
- Produces:
  - `hooks.Input.Attempt int` (`attempt`), `.Consecutive int` (`consecutive`), `.AgentName string` (`agent_name`)
  - `askHookPermission(ctx context.Context, reg *askuser.Registry, sid, toolName, reason string) bool` — true = allow this once
  - `hookToolCallbacks(engine *hooks.Reloader, reg *askuser.Registry, state *hookstate.Store, isRouter bool) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback)`
  - `Infrastructure.HookState *hookstate.Store`

- [ ] **Step 1: Add the input fields**

In `internal/hooks/run.go`, extend the "Tool events" block of `Input`:

```go
	// Tool events (PreToolUse / PostToolUse).
	ToolName     string         `json:"tool_name,omitempty"`
	ToolInput    map[string]any `json:"tool_input,omitempty"`
	ToolResponse map[string]any `json:"tool_response,omitempty"`
	// AgentName is the agent making the call. Omnis extension (additive, so a
	// Claude Code script ignores it): without it a hook cannot apply a rule to
	// one agent — e.g. requiring an ephemeral-resource label for a cleanup
	// agent's deletes but not for a change agent's, which may legitimately
	// delete a real resource.
	AgentName string `json:"agent_name,omitempty"`
	// Attempt is how many times this tool has been called with these exact
	// arguments in this session, 1 on the first. Consecutive is how many calls
	// of this tool were blocked back-to-back before this one. Omnis extensions:
	// the engine reports them, the script decides what they mean, so a
	// retry-then-escalate policy stays in configuration.
	Attempt     int `json:"attempt,omitempty"`
	Consecutive int `json:"consecutive,omitempty"`
```

- [ ] **Step 2: Write the failing test for the ask helper**

Create `agent/hooks_ask_test.go`:

```go
package agent

import (
	"context"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

// With no registry there is nobody to authorise the call, so the escalation must
// deny — the same fail-safe the budget gate uses.
func TestAskHookPermissionDeniesWithoutRegistry(t *testing.T) {
	if askHookPermission(context.Background(), nil, "sess", "Bash", "why") {
		t.Fatal("no registry must deny, not allow")
	}
}

func TestAskHookPermissionAllowsOnlyOnTheAllowChoice(t *testing.T) {
	reg := askuser.NewRegistry()
	answer := func(sel string) bool {
		done := make(chan bool, 1)
		go func() {
			done <- askHookPermission(context.Background(), reg, "sess", "Bash", "validation failed 3x")
		}()
		q := awaitPending(t, reg, "sess")
		if err := reg.Resolve("sess", q.ID, askuser.Answer{Selected: []string{sel}}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return <-done
	}
	if !answer(choiceHookAllowOnce) {
		t.Fatal("the allow choice must allow")
	}
	if answer(choiceHookDeny) {
		t.Fatal("the deny choice must deny")
	}
}

// awaitPending waits for askHookPermission's card to be registered. Ask blocks
// in a goroutine, so the test must poll rather than assume it has landed.
func awaitPending(t *testing.T, reg *askuser.Registry, sid string) askuser.Question {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if qs := reg.Pending(sid); len(qs) > 0 {
			return qs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending question appeared within 2s")
	return askuser.Question{}
}
```

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./agent/ -run TestAskHookPermission -v`
Expected: FAIL — `undefined: askHookPermission`.

- [ ] **Step 4: Write the ask helper**

Create `agent/hooks_ask.go`:

```go
package agent

import (
	"context"
	"fmt"

	"github.com/blouargant/omnis/internal/askuser"
)

// The two choices offered when a hook escalates. Deliberately NOT the five
// scopes of the permission asker: an "allow always" there is persisted as an
// allow rule, which would permanently disable the guard that asked. A hook
// question is per-call by nature.
const (
	choiceHookAllowOnce = "Allow this once"
	choiceHookDeny      = "Deny"
)

// askHookPermission puts a hook's escalation to the user and reports whether to
// allow this one call.
//
// ctx is the tool's run context, which gives the right lifetime for free: an
// unanswered card is ended by a Stop / session end / shutdown but survives a
// mere client disconnect, so a backgrounded tab keeps the question pending.
//
// With no registry it denies: nobody is going to authorise the call, and
// proceeding unvalidated is the one outcome the guard exists to prevent. Note
// this is a caller-passes-nil case only — every shipped surface builds a registry
// (Infrastructure sets AskUserRegistry unconditionally), so in practice it is
// reached from tests and embedders, NOT from a CLI one-shot. A real CLI run has a
// registry and therefore waits on the question rather than auto-denying.
func askHookPermission(ctx context.Context, reg *askuser.Registry, sid, toolName, reason string) bool {
	if reg == nil {
		return false
	}
	prompt := fmt.Sprintf("**A validation hook is refusing `%s`.**\n\n%s\n\nAllow this call anyway?", toolName, reason)
	ans, err := reg.Ask(ctx, sid, askuser.Question{
		Kind:    askuser.KindSingle,
		Prompt:  prompt,
		Choices: []string{choiceHookAllowOnce, choiceHookDeny},
		Default: choiceHookDeny,
	})
	if err != nil || ans.Cancelled || len(ans.Selected) == 0 {
		return false
	}
	return ans.Selected[0] == choiceHookAllowOnce
}
```

- [ ] **Step 5: Run to verify it passes**

Run: `go test ./agent/ -run TestAskHookPermission -v`
Expected: PASS.

- [ ] **Step 6: Thread the registry and store through `hookToolCallbacks`**

In `agent/hooks_plugin.go`, change both signatures and the `beforeTool` body:

```go
func hookToolCallbacks(engine *hooks.Reloader, reg *askuser.Registry, state *hookstate.Store, isRouter bool) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	if engine == nil || isRouter {
		return nil, nil
	}

	beforeTool := func(tc adk.ToolContext, t tool.Tool, args map[string]any) (map[string]any, error) {
		cfg := engine.Snapshot()
		if len(cfg.Match(hooks.PreToolUse, t.Name())) == 0 {
			return nil, nil
		}
		cwd := fstools.CwdForContext(tc)
		sid := realSessionID(tc)
		attempt, consecutive := state.Attempt(sid, t.Name(), args)
		in := hooks.Input{
			SessionID:   sid,
			Cwd:         cwd,
			ToolName:    t.Name(),
			ToolInput:   args,
			AgentName:   tc.AgentName(),
			Attempt:     attempt,
			Consecutive: consecutive,
		}
		out := cfg.Run(tc, hooks.PreToolUse, t.Name(), in, cwd, hookDefaultTimeout)

		// An escalation is resolved here, not by the engine: the engine reports
		// a decision, the host owns the user interaction.
		if out.Asks() {
			reason := out.Reason
			if reason == "" {
				reason = "a PreToolUse hook asked for confirmation"
			}
			if askHookPermission(tc, reg, sid, t.Name(), reason) {
				state.RecordOutcome(sid, t.Name(), false)
				return nil, nil
			}
			state.RecordOutcome(sid, t.Name(), true)
			return map[string]any{
				"output": fmt.Sprintf("[BLOCKED BY HOOK] %s: %s (the user declined)", t.Name(), reason),
			}, nil
		}

		state.RecordOutcome(sid, t.Name(), out.Blocked())
		if out.Blocked() {
			reason := out.Reason
			if reason == "" {
				reason = "blocked by PreToolUse hook"
			}
			return map[string]any{
				"output": fmt.Sprintf("[BLOCKED BY HOOK] %s: %s", t.Name(), reason),
			}, nil
		}
		return nil, nil
	}
```

Leave `afterTool` unchanged. Update the doc comment above `hookToolCallbacks`: **delete** the sentence "Unlike the permission gate there is no shared mutable state to thread: each callback queries engine.Snapshot() live, so building an independent pair per sub-agent is equivalent." It is now false — the counters are shared mutable state — and leaving it would invite someone to drop the `state` parameter. Replace it with:

```go
// The counters in `state` ARE shared mutable state and must be threaded, unlike
// the config snapshot: the callbacks are built independently per sub-agent, so a
// per-pair counter would let a sub-agent and its leader count attempts on the
// same command separately and let a delegation bounce reset the count.
```

Also add `reg`/`state` to the `buildHooksPlugin` signature and pass them down to
its internal `hookToolCallbacks` call.

- [ ] **Step 7: Add the store to Infrastructure and fix the call sites**

In `agent/infrastructure.go`, beside `Budget *budget.Store`:

```go
	// HookState holds the per-session hook attempt counters exposed to hook
	// commands as `attempt` / `consecutive`. Process-wide (so it survives a
	// hot-reload) and shared across a squad's root and sub-agents — see
	// hookToolCallbacks for why a per-callback counter would be wrong.
	HookState *hookstate.Store
```

Construct it beside `Budget: budget.New(),`:

```go
		HookState:       hookstate.New(),
```

Then update the two call sites: `agent/squad.go:248` becomes

```go
	hooksBeforeTool, hooksAfterTool := hookToolCallbacks(hooksEngine, infra.AskUserRegistry, infra.HookState, isRouter)
```

and `buildPlugins`' `buildHooksPlugin(hooksEngine, isRouterSquad)` call gains
`infra.AskUserRegistry, infra.HookState`.

Finally, wherever `Infrastructure` already drops per-session state on session end
(grep `SteerStore.Forget` / `Budget.Forget` in `agent/` and `server/`), add
`HookState.Forget(sid)` alongside.

- [ ] **Step 7b: Pin the two invariants of the wiring**

Nothing currently constructs the callback pair with a real store, so the no-op
ordering and the outcome matrix are unasserted. Both are cheap to pin: `tool.Tool`
is a **three-method** interface (`Name`, `Description`, `IsLongRunning`), and this
very package already stubs an `adk.ToolContext` by embedding — see `cancelCtx` in
`agent/concurrent_agent_tool_test.go:187`. Follow that pattern.

Add to `agent/hooks_plugin_test.go`:

```go
// The no-op contract, pinned. With a PreToolUse matcher that does not match this
// tool, the callback must return BEFORE touching the counter store — otherwise
// every Bash call in the fleet mutates a map on a build with no relevant hooks.
func TestHookCallbacksLeaveTheStoreUntouchedWhenNoHookMatches(t *testing.T) {
	// engine with a PreToolUse matcher for some OTHER tool name
	// state := hookstate.New()
	// before, _ := ... assert Attempt on a probe key is 1
	// invoke beforeTool with a stub tool whose Name() does not match
	// assert the store's counters are unchanged
}

// The consecutive counter is what makes escalation possible: advanced on a block,
// reset on anything else. Drift here fires the escalation early or never.
func TestHookCallbacksRecordBlockedAndAllowedOutcomes(t *testing.T) {
	// matching hook that exits 2 -> expect consecutive to advance
	// matching hook that exits 0 -> expect consecutive to reset
}
```

Write the bodies out fully — the sketches above name the assertions, not the code.
**If the `adk.ToolContext` stub proves genuinely infeasible**, do not burn the round
on it: fall back to a source-text guard asserting that in `agent/hooks_plugin.go`
the `state.Attempt(` call appears after the `len(cfg.Match(` early return (the
technique `TestRootPluginOrderMountsHooksBeforePermissions` already uses), and say
in your report which route you took and why.

- [ ] **Step 8: Verify the whole build and suite**

Run: `go build ./... && go test ./agent/... ./internal/hooks/... ./internal/hookstate/... && go vet ./agent/...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/hooks/run.go agent/hooks_plugin.go agent/hooks_ask.go agent/hooks_ask_test.go agent/squad.go agent/build_plugins.go agent/infrastructure.go
git commit -m "feat(hooks): escalate an 'ask' decision to the user; report attempt/consecutive/agent_name"
```

---

# Milestone 3 — Attestation

### Task 6: `internal/attest` — the unforgeable verdict store and its tool

**Files:**
- Create: `internal/attest/attest.go`, `internal/attest/tools.go`
- Create: `internal/attest/attest_test.go`

**Interfaces:**
- Consumes: `hookstate.HashArgs` (the subject must be the same hash the hook computes).
- Produces:
  - `attest.New() *Store`
  - `(*Store).Record(sid, subject string, v Verdict, reasons string)`
  - `(*Store).For(sid string) map[string]any` — shaped for `hooks.Input.Attestations`
  - `(*Store).Forget(sid string)`
  - `attest.VerdictApproved` / `attest.VerdictRejected`
  - `attest.Tools(store *Store) []tool.Tool` — one tool, `record_validation`

**Why in memory:** the obvious design — the reviewer writes a verdict file, the
hook reads it — is **forgeable**: `k8s_editor` has the `Write` tool and can author
its own approval. Process memory is unreachable from `Write`, and the tool is
mounted only on the reviewer.

- [ ] **Step 1: Write the failing tests**

Create `internal/attest/attest_test.go`:

```go
package attest

import (
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/hookstate"
)

func TestRecordedVerdictIsVisibleForItsSession(t *testing.T) {
	s := New()
	subj := hookstate.HashArgs(map[string]any{"command": "kubectl apply -f a.yaml"})
	s.Record("sess", subj, VerdictApproved, "helm-owned check passed")
	got := s.For("sess")
	rec, ok := got[subj].(map[string]any)
	if !ok {
		t.Fatalf("subject %q missing from %v", subj, got)
	}
	if rec["verdict"] != string(VerdictApproved) {
		t.Fatalf("verdict = %v, want APPROVED", rec["verdict"])
	}
	if len(s.For("other")) != 0 {
		t.Fatal("verdicts must not leak across sessions")
	}
}

// The subject is a hash of the change, so approving one manifest cannot
// authorise applying a different one.
func TestVerdictDoesNotCoverDifferentArgs(t *testing.T) {
	s := New()
	v1 := hookstate.HashArgs(map[string]any{"command": "kubectl apply -f v1.yaml"})
	v2 := hookstate.HashArgs(map[string]any{"command": "kubectl apply -f v2.yaml"})
	s.Record("sess", v1, VerdictApproved, "ok")
	if _, found := s.For("sess")[v2]; found {
		t.Fatal("a verdict on one change must not cover another")
	}
}

func TestExpiredVerdictIsNotReported(t *testing.T) {
	s := New()
	subj := "abc"
	s.Record("sess", subj, VerdictApproved, "ok")
	s.records["sess"][subj] = record{Verdict: VerdictApproved, At: time.Now().Add(-2 * TTL)}
	if _, found := s.For("sess")[subj]; found {
		t.Fatal("a verdict older than the TTL must not be reported")
	}
}

func TestForgetDropsASession(t *testing.T) {
	s := New()
	s.Record("sess", "abc", VerdictApproved, "ok")
	s.Forget("sess")
	if len(s.For("sess")) != 0 {
		t.Fatal("Forget must drop the session's verdicts")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/attest/ -v`
Expected: FAIL — no such package.

- [ ] **Step 3: Implement the store**

Create `internal/attest/attest.go`:

```go
// Package attest holds short-lived, per-session review verdicts that a hook can
// read: "this exact change was reviewed by a designated reviewer, and here is
// the verdict".
//
// It exists because the obvious design does not work. Letting the reviewer write
// its verdict to a FILE and having the hook read it is forgeable: an agent that
// holds the Write tool — k8s_editor does — can author its own approval. So the
// verdict lives in process memory, unreachable from any file tool, and the tool
// that writes it is mounted on the reviewer alone.
//
// The package is domain-free: it is a "this action requires attestation from
// that reviewer" mechanism, equally able to gate a git push on a code review.
package attest

import (
	"sync"
	"time"
)

// TTL is how long a verdict stays valid. Short, because a cluster moves: a
// review of the world as it was an hour ago is not a review of the world now.
const TTL = 30 * time.Minute

// Verdict is a reviewer's conclusion about one subject.
type Verdict string

const (
	VerdictApproved Verdict = "APPROVED"
	VerdictRejected Verdict = "REJECTED"
)

type record struct {
	Verdict Verdict
	Reasons string
	At      time.Time
}

// Store is a concurrency-safe set of per-session verdicts. Build one with New.
type Store struct {
	mu      sync.Mutex
	records map[string]map[string]record // sessionID -> subject -> record
}

// New returns an empty Store.
func New() *Store {
	return &Store{records: map[string]map[string]record{}}
}

// Record stores a reviewer's verdict about subject, replacing any previous one.
func (s *Store) Record(sid, subject string, v Verdict, reasons string) {
	if s == nil || sid == "" || subject == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[sid] == nil {
		s.records[sid] = map[string]record{}
	}
	s.records[sid][subject] = record{Verdict: v, Reasons: reasons, At: time.Now()}
}

// For returns the session's unexpired verdicts, shaped for the hook input:
// subject -> {verdict, reasons, age_seconds}. A hook reads this from its stdin,
// so no query channel into the process is needed.
func (s *Store) For(sid string) map[string]any {
	out := map[string]any{}
	if s == nil || sid == "" {
		return out
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for subject, r := range s.records[sid] {
		if now.Sub(r.At) > TTL {
			continue
		}
		out[subject] = map[string]any{
			"verdict":     string(r.Verdict),
			"reasons":     r.Reasons,
			"age_seconds": int(now.Sub(r.At).Seconds()),
		}
	}
	return out
}

// Forget drops a session's verdicts. Called on session end.
func (s *Store) Forget(sid string) {
	if s == nil || sid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, sid)
}
```

- [ ] **Step 4: Implement the tool**

Create `internal/attest/tools.go`:

```go
package attest

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

type recordIn struct {
	Subject string `json:"subject" jsonschema:"required,the change identifier to attest — the exact subject hash given to you by the caller; never invent one"`
	Verdict string `json:"verdict" jsonschema:"required,APPROVED or REJECTED"`
	Reasons string `json:"reasons" jsonschema:"required,one line per finding: what you checked and what you concluded, with the field or resource you based it on"`
}

type recordOut struct {
	Result string `json:"result"`
}

// Tools returns the attestation tool set (one tool). Mount it ONLY on a reviewer
// agent: an agent that can attest its own work has no reviewer.
//
// sessionOf resolves the USER-FACING session from a tool context, and is injected
// rather than chosen here for a reason that is easy to get wrong. A reviewer runs
// as a sub-agent, so its own ctx.SessionID() is an ephemeral agenttool session;
// a verdict recorded under that id is invisible to the hook, which reads
// attestations by the real session. The codebase has exactly one correct resolver
// (agent.realSessionID: steer-session first, then ctx.SessionID()), and attest
// cannot import agent without a cycle — so the caller passes it in, and both
// sides of the attestation are keyed by literally the same function.
//
// Do NOT substitute events.RootSessionFromContext here: WithRootSession is planted
// only by the server (server/sse.go, server/mailbox_push.go, server/a2a_server.go)
// and NOT by the CLI or TUI, so on those surfaces it resolves empty and every
// Kubernetes mutation would be refused as "not reviewed" — on the one refusal path
// that deliberately never escalates to the user.
func Tools(store *Store, sessionOf func(adk.ToolContext) string) []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "record_validation",
		Description: "Record your review verdict for one specific change so the host can act on it. " +
			"Call this exactly once, as your final step, with the `subject` identifier you were given. " +
			"Arguments: `subject` (string, required) — the change identifier supplied by the caller, copied verbatim; " +
			"`verdict` (string, required) — APPROVED or REJECTED; " +
			"`reasons` (string, required) — what you checked and concluded, citing the resource fields you read. " +
			"An APPROVED verdict lets the change proceed, so do not approve anything you did not verify yourself.",
	}, func(ctx adk.ToolContext, in recordIn) (recordOut, error) {
		v := VerdictRejected
		if strings.EqualFold(strings.TrimSpace(in.Verdict), string(VerdictApproved)) {
			v = VerdictApproved
		}
		sid := sessionOf(ctx)
		if sid == "" {
			// Loud rather than silent: a verdict with no session to key it under
			// would be recorded where no hook will ever read it.
			return recordOut{Result: "Error: no session could be resolved, so the verdict cannot be recorded."}, nil
		}
		if strings.TrimSpace(in.Subject) == "" {
			return recordOut{Result: "Error: subject is required — use the change identifier you were given."}, nil
		}
		store.Record(sid, strings.TrimSpace(in.Subject), v, in.Reasons)
		return recordOut{Result: fmt.Sprintf("Recorded %s for %s.", v, in.Subject)}, nil
	})
	if err != nil {
		panic(fmt.Errorf("build record_validation tool: %w", err))
	}
	return []tool.Tool{t}
}
```

**Session resolution is settled — do not re-decide it.** The controller verified
which context keys each surface plants: `WithSteerSession` is planted by all three
surfaces (`server/sse.go:224`, `internal/cli/cli.go:473`,
`internal/tui/tui.go:1796`), while `WithRootSession` is planted **only by the
server** (`server/sse.go:229`, `server/mailbox_push.go:461`,
`server/a2a_server.go:572`). So `events.RootSessionFromContext` would resolve empty
on CLI and TUI. `agent.realSessionID` reads the steer key first and is the one
correct resolver; `attest` cannot import `agent` without a cycle, hence the
injected `sessionOf` parameter. Task 7 passes `realSessionID` in.

Add a test that a nil-ish/empty resolver yields the error result rather than
recording under an empty key.

- [ ] **Step 5: Run to verify they pass**

Run: `go test ./internal/attest/ -v && go vet ./internal/attest/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/attest/
git commit -m "feat(attest): in-memory, per-session review verdicts a hook can read"
```

---

### Task 7: Mount the group and inject verdicts into the hook input

**Files:**
- Modify: `internal/hooks/run.go` (`Input.Attestations`)
- Modify: `agent/agent.go` (the tool-group switch, near `case "settings":` at `:369`)
- Modify: `agent/hooks_plugin.go` (fill `Attestations`), `agent/squad.go`, `agent/infrastructure.go`
- Modify (forced by widening a shared signature — sweep them all with
  `grep -rn "toolsForAgentConfig(" agent/`): `agent/build_subagents.go`, plus every
  test that calls it (`agent/nested_build_test.go`,
  `agent/websearch_provider_test.go`, `agent/hooks_plugin_test.go`)
- Modify: `server/spawn.go` (`Attest.Forget` beside the other Infrastructure stores)
- Test: `agent/attest_mount_test.go`

**Interfaces:**
- Consumes: `attest.New`, `attest.Tools`, `(*Store).For`.
- Produces: `hooks.Input.Attestations map[string]any` (`attestations`); the `attest` tool-group key; `Infrastructure.Attest *attest.Store`.

- [ ] **Step 1: Add the input field**

In `internal/hooks/run.go`, after `Consecutive`:

```go
	// Attestations are the unexpired review verdicts recorded for this session,
	// keyed by subject hash. Omnis extension. This is how a hook can require a
	// reviewer's sign-off without a query channel into the process — and why the
	// verdict cannot be forged by an agent holding a file-writing tool.
	Attestations map[string]any `json:"attestations,omitempty"`
```

- [ ] **Step 2: Write the failing test**

Create `agent/attest_mount_test.go`:

```go
package agent

import (
	"context"
	"strings"
	"testing"
)

// The whole guarantee rests on this: an agent that can attest its own changes
// has no reviewer. This property is invisible when reading the config, so it is
// tested.
func TestAttestGroupMountsOnlyWhereDeclared(t *testing.T) {
	// A REAL store on both branches. A nil store would make the negative
	// assertion pass because of the `attestStore != nil` mount guard rather than
	// because the group was not declared — a test that passes either way.
	store := attest.New()
	has := func(keys []string) bool {
		tools, _, _, _ := toolsForAgentConfig(
			context.Background(),
			RuntimeAgentConfig{Name: "probe", Tools: keys},
			RuntimeSettings{},
			nil, nil, nil, nil, nil, nil, nil, nil, store, false, nil,
		)
		for _, tl := range tools {
			if tl.Name() == "record_validation" {
				return true
			}
		}
		return false
	}
	if !has([]string{"attest"}) {
		t.Fatal("an agent declaring the attest group must get record_validation")
	}
	if has([]string{"Bash", "Read", "Write"}) {
		t.Fatal("an agent that did not declare the attest group must NOT get record_validation")
	}
}

// The shipped fleet must not let either mutating agent sign its own work.
func TestShippedMutatingAgentsCannotAttest(t *testing.T) {
	for _, name := range []string{"k8s_editor", "k8s_cleaner"} {
		data := readShippedAgentJSON(t, name)
		if strings.Contains(data, `"attest"`) {
			t.Fatalf("%s declares the attest tool group — it could approve its own changes", name)
		}
	}
}
```

**Both helpers are settled — the controller checked.** `toolsForAgentConfig`'s
current signature (verified at `agent/agent.go:235`) is exactly the one above minus
`attestStore`, which this task inserts before `asLeader`; pass zero values for
every parameter you do not need. `readShippedAgentJSON(t, name)` is a small helper
reading `filepath.Join("..", "registry", "agents", name, "agent.json")` relative to
the package directory — no such helper exists yet, so write it. Precedents for
asserting against shipped config live in `agent/websearch_provider_test.go` (which
reasons about the shipped `web_agent`'s `agent.json`) and
`agent/runtime_config_test.go:735` (`setupAgentsRegistry`); read the former before
writing yours in case it already has what you need.

- [ ] **Step 3: Run to verify it fails**

Run: `go test ./agent/ -run 'Attest|CannotAttest' -v`
Expected: FAIL — the `attest` group is unknown, so no `record_validation` tool.

- [ ] **Step 4: Add the tool group**

In `agent/agent.go`'s tool-group switch, beside `case "settings":`. The exact code
is in the note below, because it depends on a signature change:

**Threading the store — decided, do not re-litigate.** `toolsForAgentConfig`
receives `runtime RuntimeSettings`, which holds only config-derived values (it has
no `json:"-"` field and no runtime handle — checked). Runtime handles reach it as
**parameters**, exactly like `codeIdx`, `regIdx`, `docIdx` and `sessIdx`. So add a
parameter, do **not** put the store on `RuntimeSettings`:

```go
func toolsForAgentConfig(ctx context.Context, cfg RuntimeAgentConfig, runtime RuntimeSettings, skillTS, softSkillTS tool.Toolset, leaderMCPHandles []*mcpcfg.Handle, pool *mcpcfg.Pool, codeIdx *codeindex.Index, regIdx *regindex.Index, docIdx *docindex.Index, sessIdx sessionIndexFn, attestStore *attest.Store, asLeader bool, emb embed.Embedder) ([]tool.Tool, []tool.Toolset, string, []*mcpcfg.Handle)
```

and mount it as:

```go
		case "attest":
			// Mount ONLY on a reviewer agent. An agent holding this can approve
			// its own changes, which is exactly the hole internal/attest exists
			// to close (a verdict file would be forgeable by any agent with
			// Write; this tool is the deliberate, narrow write path). A nil store
			// (CLI/examples) mounts nothing, so the group is inert there.
			if attestStore != nil {
				// realSessionID is the SAME resolver hookToolCallbacks uses to key
				// the attestations it reads, so both sides agree by construction.
				agentTools = append(agentTools, attest.Tools(attestStore, realSessionID)...)
			}
```

Update every call site (`grep -n "toolsForAgentConfig(" agent/`) to pass
`infra.Attest`, and `nil` in tests.

- [ ] **Step 5: Fill the input and hold the store**

In `agent/infrastructure.go`, beside `HookState`:

```go
	// Attest holds per-session reviewer verdicts (see internal/attest).
	// Process-wide so it survives a hot-reload; lost on restart, which forces a
	// re-review — the correct fail-closed direction.
	Attest *attest.Store
```

Construct with `Attest: attest.New(),` and add `Attest.Forget(sid)` beside the
other session-end `Forget` calls.

`hookToolCallbacks` gains a fifth parameter, so its signature (from Task 5)
becomes:

```go
func hookToolCallbacks(engine *hooks.Reloader, reg *askuser.Registry, state *hookstate.Store, attestStore *attest.Store, isRouter bool) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback)
```

Update both call sites (`agent/squad.go` and `buildHooksPlugin`) to pass
`infra.Attest`. Then, in the `beforeTool` body, add to the `hooks.Input` literal:

```go
			Attestations: attestStore.For(sid),
```

`(*Store).For` is nil-safe (it returns an empty map), so a build without the
store is unaffected.

- [ ] **Step 5b: Pin the record-to-read join end to end**

This is the join the whole design rests on: the tool records a verdict keyed by
session, and the hook callback reads it back by session. Both sides use
`realSessionID`, so they agree *by construction* — but nothing fails if a future
refactor renames or reorders that variable in `beforeTool`. Pin it.

The scaffolding already exists: `agent/hooks_plugin_test.go` has the
`hookTestCtx`/`hookTestTool` stubs from the previous task, and `hooks.Command`
runs through a shell, so a hook whose command is `cat > input.json` (with the
test's temp dir as the run cwd) captures the exact stdin the engine produced.

Add to `agent/hooks_plugin_test.go`:

```go
// The record-to-read join: a verdict recorded through the tool's store must reach
// the hook's stdin under the SAME session key. Both sides resolve the session with
// realSessionID; this fails if that ever stops being true.
func TestRecordedAttestationReachesTheHookInput(t *testing.T) {
	// - dir := t.TempDir(); build a hooks config whose PreToolUse command is
	//   `cat > input.json`, matching the stub tool's name
	// - store := attest.New(); store.Record(sid, subject, attest.VerdictApproved, "ok")
	//   using the same sid the stub context reports and a subject from hookstate.HashArgs
	// - invoke beforeTool with that context and those args
	// - read dir/input.json, json.Unmarshal it, and assert
	//   input["attestations"][subject]["verdict"] == "APPROVED"
}
```

Write the body out fully. Assert on the **subject key** as well as the verdict, so
a wrong session key (empty map) and a wrong subject key are both caught.

- [ ] **Step 6: Run to verify it passes**

Run: `go build ./... && go test ./agent/... ./internal/attest/... -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add agent/ internal/hooks/run.go
git commit -m "feat(attest): mount the attest tool group and inject verdicts into hook input"
```

---

# Milestone 4 — Kubernetes policy in configuration

### Task 8: The hook script — input, fast path, parsing

**Files:**
- Create: `config/hooks/k8s-validate.py`
- Create: `packaging/k8s_validate_test.go`

**Interfaces:**
- Consumes: the hook input JSON (`tool_input.command`, `cwd`, `agent_name`, `attempt`, `consecutive`, `attestations`).
- Produces: the script's stdout protocol — `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"|"ask","permissionDecisionReason":"..."}}`, or exit 0 with no output to proceed. Functions later tasks extend: `segments(command)`, `classify(argv)`, `emit(decision, reason)`, `proceed()`.

- [ ] **Step 1: Write the failing tests**

Create `packaging/k8s_validate_test.go`:

```go
package packaging

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// script returns the path to the shipped hook script.
func script(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the hook script assumes a POSIX environment")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	return filepath.Join("..", "config", "hooks", "k8s-validate.py")
}

// runHook pipes a hook input into the script and returns its stdout and exit code.
func runHook(t *testing.T, in map[string]any, extraPath string) (string, int) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cmd := exec.Command("python3", script(t))
	cmd.Stdin = bytes.NewReader(data)
	if extraPath != "" {
		// Prepend, so a stub shadows a real kubectl/helm on the machine.
		cmd.Env = append(os.Environ(), "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	// A traceback on stderr would otherwise read as "no opinion" and pass
	// silently, which is the worst possible failure for a guard.
	if errb.Len() > 0 {
		t.Fatalf("hook wrote to stderr: %s", errb.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

func decisionOf(t *testing.T, stdout string) string {
	t.Helper()
	if strings.TrimSpace(stdout) == "" {
		return ""
	}
	var jo struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &jo); err != nil {
		t.Fatalf("hook stdout is not the JSON protocol: %q", stdout)
	}
	return jo.HookSpecificOutput.PermissionDecision
}

func bashInput(command, agent string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
		"agent_name":      agent,
		"attempt":         1,
		"consecutive":     0,
	}
}

// Verb identification is where both failure directions live, and neither is
// observable from a bare "did it say something" assertion — which is why the
// first version of this file could not detect that `kubectl -A get pods` was
// denied while `helm --debug uninstall` sailed through. Table-drive the DECISION
// for every shape that has bitten, in both directions.
func TestDecisionForCommandShapes(t *testing.T) {
	cases := []struct {
		name, command, agent, want string // want: "" = proceed
	}{
		// Boolean global flags must not swallow the verb (false-positive axis).
		{"bare -A before a read verb", "kubectl -A get pods", "k8s_investigator", ""},
		{"long boolean global", "kubectl --all-namespaces get pods", "k8s_investigator", ""},
		{"another boolean global", "kubectl --insecure-skip-tls-verify get pods", "k8s_investigator", ""},
		{"value-taking global", "kubectl -n demo get pods", "k8s_investigator", ""},
		{"inline value global", "kubectl --context=prod get pods", "k8s_investigator", ""},
		// ... and must not hide a mutation either (false-negative axis).
		{"boolean global before apply", "kubectl --insecure-skip-tls-verify apply -f app.yaml", "k8s_editor", "deny"},
		{"helm boolean global before upgrade", "helm --debug upgrade r ./c -n demo", "k8s_editor", "deny"},
		{"helm boolean global before uninstall", "helm --debug uninstall r -n demo", "k8s_editor", "deny"},
		// Wrappers must not hide a mutation.
		{"sudo with its own flag", "sudo -n kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		{"absolute-path wrapper", "/usr/bin/sudo kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		{"wrapper flag with a value", "sudo -u root kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		// Separators the first regex missed entirely.
		{"newline-separated", "kubectl get pods\nkubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		{"ampersand-separated", "kubectl get pods & kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		// Not this guard's business.
		{"grep that merely mentions kubectl", "grep -rn kubectl deploy.sh > /tmp/out", "coder", ""},
		{"redirect on a READ", "kubectl get pods -o json > /tmp/pods.json", "k8s_investigator", ""},
		{"stderr redirect on a read", "kubectl get pods 2>&1", "k8s_investigator", ""},
		{"not kubernetes at all", "go test ./...", "coder", ""},
		// Shapes we refuse to reason about.
		{"heredoc apply", "kubectl apply -f - <<EOF\nkind: Pod\nEOF", "k8s_editor", "deny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := runHook(t, bashInput(c.command, c.agent), "")
			got := decisionOf(t, out)
			if got != c.want {
				t.Fatalf("decision = %q, want %q (exit=%d stdout=%q)", got, c.want, code, out)
			}
			if c.want == "" && strings.TrimSpace(out) != "" {
				t.Fatalf("a proceed must emit nothing, got %q", out)
			}
		})
	}
}

// The fast path: the hook fires on every Bash call in the whole fleet, so
// anything that is not kubectl/helm must proceed with no output.
func TestNonKubernetesCommandProceeds(t *testing.T) {
	out, code := runHook(t, bashInput("go test ./...", "coder"), "")
	if code != 0 || decisionOf(t, out) != "" {
		t.Fatalf("non-k8s command: exit=%d stdout=%q, want exit 0 and no opinion", code, out)
	}
}

// A read-only verb is not a mutation and must not be gated.
func TestReadOnlyKubectlProceeds(t *testing.T) {
	out, code := runHook(t, bashInput("kubectl get pods -n demo", "k8s_investigator"), "")
	if code != 0 || decisionOf(t, out) != "" {
		t.Fatalf("kubectl get: exit=%d stdout=%q, want no opinion", code, out)
	}
}

// The script must never risk executing the mutation itself, so a command shape
// it cannot fully re-tokenise and replay as argv is refused. A heredoc is the
// canonical case, and refusing it pushes toward the declarative manifest path
// the k8s-modification playbook already prefers.
func TestHeredocApplyIsRefusedFailClosed(t *testing.T) {
	out, _ := runHook(t, bashInput("kubectl apply -f - <<EOF\nkind: Pod\nEOF", "k8s_editor"), "")
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("heredoc apply decision = %q, want deny", got)
	}
	if !strings.Contains(out, "manifest file") {
		t.Fatalf("the refusal must tell the agent what to do instead: %q", out)
	}
}

// A mutation hidden in the second half of a compound command must still be seen.
func TestCompoundCommandMutationIsCaught(t *testing.T) {
	out, _ := runHook(t, bashInput("kubectl get pods && kubectl delete pod x -n demo", "k8s_cleaner"), "")
	if got := decisionOf(t, out); got == "" {
		t.Fatalf("compound command: got no opinion, want a decision on the delete: %q", out)
	}
}

// Wrappers must not hide a mutation either.
func TestWrapperStrippedMutationIsCaught(t *testing.T) {
	out, _ := runHook(t, bashInput("sudo kubectl delete pod x -n demo", "k8s_cleaner"), "")
	if got := decisionOf(t, out); got == "" {
		t.Fatalf("sudo-wrapped delete: got no opinion, want a decision: %q", out)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./packaging/ -run 'Kubernetes|Kubectl|Heredoc|Compound|Wrapper' -v`
Expected: FAIL — the script does not exist.

- [ ] **Step 3: Write the script's skeleton**

Create `config/hooks/k8s-validate.py`:

```python
#!/usr/bin/env python3
"""PreToolUse hook: refuse a Kubernetes mutation that has not been validated.

Reads the omnis hook input on stdin and writes the Claude Code hook output
protocol on stdout. All Kubernetes policy lives here, in configuration, so the
Go core stays domain-free (see the design contract in CLAUDE.md).

Three rules govern everything below:

1. FAIL CLOSED, and fail closed on IDENTIFICATION too. This script executes
   commands, so it must never risk executing the mutation itself: it validates a
   segment only when it can fully re-tokenise it and replay it as argv with no
   shell. But the subtler rule is that when a segment LOOKS like a kubectl/helm
   invocation and we cannot identify its verb, that is also a refusal — never a
   pass. Guessing "probably harmless" is how an unvalidated `helm uninstall`
   slips through.
2. Do not refuse what is not ours. A guard that fires on honest work gets
   disabled by its users, so the refusal gates key on "the first real word of
   this segment is kubectl/helm", never on "this text contains the word
   kubectl". `grep -rn kubectl deploy.sh > out` is not a cluster mutation.
3. The engine reports `attempt` / `consecutive`; this script decides what they
   mean. That is what keeps the engine generic.

Python 3 standard library only.
"""

import json
import re
import shlex
import subprocess
import sys

MAX_ATTEMPTS = 3          # matches agentCapGraceCalls in agent/budget_plugin.go
VALIDATE_TIMEOUT = 45     # seconds; below the hook's own timeout so we can explain

READ_ONLY_VERBS = {
    "get", "describe", "logs", "top", "explain", "events", "api-resources",
    "api-versions", "cluster-info", "version", "auth", "config", "diff", "wait",
}

APPLY_VERBS = {"apply", "create", "replace"}
IMPERATIVE_VERBS = {"patch", "set", "scale", "annotate", "label", "expose", "autoscale", "rollout"}
DESTRUCTIVE_VERBS = {"delete", "drain", "cordon", "uncordon", "taint"}

# helm uses an ALLOWLIST of read-only verbs, symmetric with kubectl above, so an
# unknown helm verb fails closed. A denylist of change verbs would let any future
# mutating verb through by default, and combined with a verb misparse that is how
# an unvalidated `helm uninstall` reaches the cluster.
HELM_READ_ONLY_VERBS = {
    "list", "status", "get", "history", "show", "search", "template", "lint",
    "version", "env", "repo", "dependency", "plugin", "verify", "diff",
}

WRAPPERS = {"sudo", "env", "time", "nice", "ionice", "nohup", "command", "exec",
            "timeout", "stdbuf"}

# Global flags that take a SEPARATE value, so the token after them is not the verb.
# Every other flag is treated as boolean. Inferring this instead from "the next
# token is not a flag" is what made `kubectl -A get pods` parse its verb as `pods`
# (denying an ordinary read-only command) and `helm --debug uninstall r` parse as
# `r` (letting a destructive command through unvalidated).
VALUE_FLAGS = {
    "-n", "--namespace", "--context", "--kube-context", "--kubeconfig",
    "--cluster", "--user", "--as", "--as-group", "--token", "--server", "-s",
    "--request-timeout", "--cache-dir", "--tls-server-name",
    "--client-certificate", "--client-key", "--certificate-authority",
    "-v", "--v", "--log-flush-frequency", "--profile", "--profile-output",
}

# Mirrors compoundOps in core/permissions/match_bash.go, longest-first so `&&` is
# not read as two `&`. The parity test in core/permissions pins the two lists.
COMPOUND_OPS = ("&&", "||", "|&", ";", "\n", "|", "&")

# Shapes we refuse to reason about rather than risk mis-parsing.
UNSAFE_SHELL = re.compile(r"<<|>>|[<>]|\$\(|`|\$\{")


def emit(decision, reason):
    """Write one PreToolUse decision and exit."""
    json.dump({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": decision,
            "permissionDecisionReason": reason,
        }
    }, sys.stdout)
    sys.stdout.write("\n")
    sys.exit(0)


def proceed(note=""):
    """No opinion: the tool call continues to the permission layer.

    A note is surfaced to the user through the hook protocol's systemMessage,
    which is how the validated diff reaches the permission card.
    """
    if note:
        json.dump({"systemMessage": note}, sys.stdout)
        sys.stdout.write("\n")
    sys.exit(0)


def refuse(reason, attempt, consecutive):
    """Deny with a diagnostic, or escalate once the agent stops learning.

    The diagnostic is the point: it reaches the agent as
    "[BLOCKED BY HOOK] Bash: <reason>" so it can correct and retry. After
    MAX_ATTEMPTS on the same command — or that many consecutive refusals of
    different-but-still-wrong commands — the user decides instead.
    """
    if attempt >= MAX_ATTEMPTS or consecutive >= MAX_ATTEMPTS:
        emit("ask", "Validation has failed %d times.\n\n%s" % (max(attempt, consecutive), reason))
    emit("deny", reason)


def _is_redirect_amp(command, i):
    """True when the `&` at i belongs to a redirect (2>&1, >&2, &>file) rather
    than backgrounding a command. Mirrors isRedirectAmp in match_bash.go: the
    nearest non-blank neighbour on either side being `>` makes it a redirect."""
    j = i - 1
    while j >= 0 and command[j] in " \t":
        j -= 1
    if j >= 0 and command[j] == ">":
        return True
    k = i + 1
    while k < len(command) and command[k] in " \t":
        k += 1
    return k < len(command) and command[k] == ">"


def segments(command):
    """Split a shell command line into independently-classified segments.

    Mirrors splitCompound in core/permissions/match_bash.go, including its quote
    awareness and its redirect carve-out for `&`. A regex cannot do this: it
    would split inside a quoted argument (turning `note="a && b"` into two
    unbalanced halves) and it would miss `\\n` and `&`, so a mutation on the
    second line of a multi-line command would never be examined. Like the Go
    side, backslash escapes are NOT interpreted.
    """
    out, buf = [], []
    quote = ""
    i, n = 0, len(command)
    while i < n:
        c = command[i]
        if quote:
            buf.append(c)
            if c == quote:
                quote = ""
            i += 1
            continue
        if c in ("'", '"'):
            quote = c
            buf.append(c)
            i += 1
            continue
        matched = ""
        for op in COMPOUND_OPS:
            if command.startswith(op, i):
                matched = op
                break
        if matched == "&" and _is_redirect_amp(command, i):
            buf.append(c)
            i += 1
            continue
        if matched:
            out.append("".join(buf))
            buf = []
            i += len(matched)
            continue
        buf.append(c)
        i += 1
    out.append("".join(buf))
    return [s.strip() for s in out if s.strip()]


def looks_like_k8s_invocation(segment):
    """True when this segment plausibly invokes kubectl or helm.

    Deliberately NOT a substring test on the text: `grep -rn kubectl deploy.sh`
    merely mentions the word and must not be refused. It walks leading wrappers
    and their flags to find the real binary; if a wrapper was stripped and the
    binary still cannot be identified (`sudo -u root kubectl …`), it returns True
    so the caller fails closed rather than guessing.
    """
    stripped_wrapper = False
    for word in segment.split():
        base = word.split("/")[-1]
        if base in ("kubectl", "helm"):
            return True
        if base in WRAPPERS:
            stripped_wrapper = True
            continue
        if word.startswith("-") or ("=" in word and not word.startswith("-")):
            continue
        return stripped_wrapper
    return stripped_wrapper


def _strip_wrappers(argv):
    """Drop leading process wrappers, their own flags, and env assignments.

    Basenames each token so /usr/bin/sudo is recognised (classify basenames too),
    and drops a stripped wrapper's flags so `sudo -n kubectl delete` does not
    leave `-n` as the apparent binary.
    """
    while argv:
        head = argv[0]
        base = head.split("/")[-1]
        if base in WRAPPERS:
            argv = argv[1:]
            while argv and argv[0].startswith("-"):
                argv = argv[1:]
            continue
        if "=" in head and not head.startswith("-"):
            argv = argv[1:]
            continue
        break
    return argv


def tokenise(segment):
    """Return the segment's argv with wrappers stripped, or None if unsafe.

    None means "this shape cannot be validated". For a segment that looks like a
    kubectl/helm MUTATION that is a refusal, never a pass — a shape we cannot read
    is a shape we cannot check.
    """
    if UNSAFE_SHELL.search(segment):
        return None
    try:
        argv = shlex.split(segment)
    except ValueError:
        return None
    return _strip_wrappers(argv) or None


def raw_classify(segment):
    """Best-effort (tool, verb) from the raw text, skipping shlex.

    Used for one purpose: to spare a READ-ONLY command whose shape we refuse to
    tokenise. A redirect on `kubectl get pods -o json > out` is harmless, and
    refusing it would be exactly the false positive that gets a guard disabled by
    its users. Never used to permit a mutation — an unrecognised or mutating verb
    still falls through to the refusal, and `segments` has already split the line,
    so an `apply` later in the command line is its own segment.
    """
    argv = _strip_wrappers(segment.split())
    return classify(argv) if argv else (None, None)


def classify(argv):
    """Return (tool, verb) for a kubectl/helm invocation, else (None, None).

    Global flags may precede the verb. Only flags known to take a SEPARATE value
    consume the following token; every other flag is boolean, so a bare global
    like -A or --debug cannot swallow the verb.
    """
    if not argv:
        return None, None
    binary = argv[0].split("/")[-1]
    if binary not in ("kubectl", "helm"):
        return None, None
    i = 1
    while i < len(argv):
        tok = argv[i]
        if not tok.startswith("-"):
            return binary, tok
        if "=" in tok:
            i += 1
            continue
        if tok in VALUE_FLAGS and i + 1 < len(argv):
            i += 2
            continue
        i += 1
    return binary, None


def is_read_only(tool, verb):
    return (tool == "kubectl" and verb in READ_ONLY_VERBS) or \
           (tool == "helm" and verb in HELM_READ_ONLY_VERBS)


def main():
    try:
        data = json.load(sys.stdin)
    except (ValueError, OSError):
        # We cannot even read the request; with fail_closed the engine turns a
        # non-zero exit into a block, which is the correct direction.
        sys.stderr.write("k8s-validate: unreadable hook input\n")
        sys.exit(1)

    command = (data.get("tool_input") or {}).get("command") or ""

    # Fast path. This hook fires on EVERY Bash call in the fleet, so anything
    # not mentioning kubectl or helm must cost only interpreter startup. This is
    # a substring test by design: an alias (`k delete …`) or an indirection
    # (`$KUBECTL delete …`) is a known, accepted blind spot of the cheap path.
    if "kubectl" not in command and "helm" not in command:
        sys.exit(0)

    agent = data.get("agent_name") or ""
    cwd = data.get("cwd") or None
    try:
        attempt = int(data.get("attempt") or 1)
        consecutive = int(data.get("consecutive") or 0)
    except (TypeError, ValueError):
        attempt, consecutive = 1, 0
    attestations = data.get("attestations") or {}

    for segment in segments(command):
        argv = tokenise(segment)
        if argv is None:
            if looks_like_k8s_invocation(segment):
                rtool, rverb = raw_classify(segment)
                if rtool and rverb and is_read_only(rtool, rverb):
                    # A redirect on a read is not this guard's business.
                    continue
                refuse(
                    "This command shape cannot be validated (it uses a redirection, heredoc "
                    "or substitution), so it is refused rather than applied unchecked. Write "
                    "the change to a manifest file and apply that file instead.",
                    attempt, consecutive,
                )
            continue
        tool, verb = classify(argv)
        if tool is None or verb is None:
            # Fail closed on identification: a segment that looks like a
            # kubectl/helm invocation whose verb we cannot read is refused, not
            # waved through.
            if looks_like_k8s_invocation(segment):
                refuse(
                    "This looks like a kubectl/helm command but its verb could not be "
                    "identified, so it cannot be validated. Re-run it without process "
                    "wrappers (sudo/env/time) and with the verb immediately after the binary.",
                    attempt, consecutive,
                )
            continue
        if is_read_only(tool, verb):
            continue
        validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations)

    proceed()


def validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations):
    """Validate one mutating segment. Filled in by Task 9."""
    refuse("Validation for `%s %s` is not implemented yet." % (tool, verb), attempt, consecutive)


if __name__ == "__main__":
    main()
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./packaging/ -run 'Kubernetes|Kubectl|Heredoc|Compound|Wrapper' -v`
Expected: PASS.

- [ ] **Step 5: Add the parsing-divergence guard**

The spec accepts duplicating `core/permissions/match_bash.go`'s splitting in
Python; this is what keeps the two from drifting apart silently. Append to
`packaging/k8s_validate_test.go`:

**Where this test lives — decided, do not re-litigate.** It goes in
**`core/permissions/bash_split_parity_test.go`**, package `permissions`, NOT in
`packaging/`. `splitCompound` is unexported, and exporting a `SplitCompoundForTest`
seam in a non-test file would widen a package's public API permanently for a test's
benefit. From inside `core/permissions` the function is already visible, and that
package's tests already assert against shipped config, so this is consistent. The
script path from there is `filepath.Join("..", "..", "config", "hooks", "k8s-validate.py")`.

```go
// The script reimplements the compound-splitting that match_bash.go already does
// in Go. That duplication is accepted (see the spec, §3), so it is pinned: both
// must segment the same corpus identically.
func TestPythonAndGoSegmentTheSameCorpus(t *testing.T) {
	// The first five pin the separators both sides always implemented — where
	// agreement was never in doubt. The rest are the cases that ACTUALLY
	// diverged before the Python side was rewritten to mirror splitCompound:
	// newline and bare `&` (absent from the original regex), `|&` (one operator
	// in Go, two characters to a regex), a separator inside quotes (Go is
	// quote-aware, a regex is not), and the two redirect shapes isRedirectAmp
	// exists for. Verified to agree on all twelve.
	corpus := []string{
		"kubectl get pods && kubectl delete pod x",
		"sudo kubectl apply -f a.yaml; echo done",
		"kubectl get pods | grep Running",
		"helm upgrade r c -n ns || kubectl rollout undo deploy/x",
		"echo hi",
		"kubectl get pods\nkubectl delete pod x -n demo",
		"kubectl get pods & kubectl delete pod x -n demo",
		"kubectl logs x |& tee log",
		"kubectl annotate pod x note=\"a && b\" -n demo",
		"kubectl get pods 2>&1",
		"kubectl get pods &> out",
		"kubectl get po; ; kubectl get svc",
	}
	for _, cmd := range corpus {
		got := pySegments(t, cmd)
		want := splitCompound(cmd)
		if len(got) != len(want) {
			t.Fatalf("%q: python segments %v, go segments %v", cmd, got, want)
		}
		for i := range want {
			if strings.TrimSpace(got[i]) != strings.TrimSpace(want[i]) {
				t.Fatalf("%q: segment %d python=%q go=%q", cmd, i, got[i], want[i])
			}
		}
	}
}
```

**`pySegments`** is the only helper you need to write: run `python3 -c` with a
snippet that imports the script by path (`importlib.util.spec_from_file_location`)
and prints `json.dumps(segments(cmd))`, then unmarshal that. The script's
`if __name__ == "__main__"` guard means importing it does not run `main`. Skip the
test when `python3` is not on PATH.

- [ ] **Step 6: Run and commit**

Run: `go test ./packaging/ ./core/permissions/ -v`
Expected: PASS.

```bash
git add config/hooks/k8s-validate.py packaging/k8s_validate_test.go core/permissions/
git commit -m "feat(hooks): k8s validation hook script — input, fast path, fail-closed parsing"
```

---

### Task 9: Per-verb validation

**Files:**
- Modify: `config/hooks/k8s-validate.py` (replace the `validate` stub)
- Test: `packaging/k8s_validate_test.go`

**Interfaces:**
- Consumes: `segments`, `tokenise`, `classify`, `refuse`, `proceed` from Task 8.
- Produces: `validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations)`; helpers `run_argv`, `flag_value`, `has_flag`, `validate_manifest`, `validate_imperative`, `validate_helm`, `validate_destructive`.

**Testing approach:** the tests must not need a cluster. Each test writes a **stub
`kubectl` / `helm`** shell script into a temp dir and prepends it to `PATH`, so the
script's real subprocess calls are observable and scriptable. This is the only way
to test the traps (notably `kubectl diff` exiting 1) honestly.

- [ ] **Step 1: Write the failing tests**

```go
// stubBin writes an executable stub named name into a fresh dir and returns the
// dir, for prepending to PATH. body is a /bin/sh script.
func stubBin(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return dir
}

// THE trap: `kubectl diff` exits 1 when a diff EXISTS. That is the normal case
// for any real change, so treating exit 1 as failure would refuse every correct
// apply. Only >1 is an error.
func TestKubectlDiffExitOneIsNotAFailure(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)      echo "~ spec.replicas: 1 -> 2"; exit 1 ;;
  *dry-run*)   echo "deployment.apps/x configured (server dry run)"; exit 0 ;;
  *)           exit 0 ;;
esac`)
	in := bashInput("kubectl apply -f change.yaml -n demo", "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("a normal diff (exit 1) must not be refused: %q", out)
	}
}

// A dry-run the API server rejects must be refused, with the server's own error
// carried back so the agent can correct it.
func TestServerDryRunFailureIsRefusedWithTheServerError(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)    exit 1 ;;
  *dry-run*) echo "error: unknown field spec.replica" >&2; exit 1 ;;
  *)         exit 0 ;;
esac`)
	out, _ := runHook(t, bashInput("kubectl apply -f change.yaml -n demo", "k8s_editor"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("failed dry-run decision = %q, want deny", decisionOf(t, out))
	}
	if !strings.Contains(out, "unknown field") {
		t.Fatalf("the refusal must carry the server error verbatim: %q", out)
	}
}

// A delete with no name and no selector has an unbounded blast radius.
func TestDeleteAllIsRefused(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0")
	out, _ := runHook(t, bashInput("kubectl delete pods --all -n demo", "k8s_cleaner"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("delete --all decision = %q, want deny", decisionOf(t, out))
	}
}

// The cleaner's documented contract, now enforced: it removes only resources
// labelled ephemeral. This needs agent_name — see Task 5.
func TestCleanerCannotDeleteAnUnlabelledResource(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *"-o json"*) echo '{"metadata":{"name":"real-app","labels":{}}}' ;;
  *)           exit 0 ;;
esac`)
	out, _ := runHook(t, bashInput("kubectl delete pod real-app -n demo", "k8s_cleaner"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("unlabelled delete by the cleaner = %q, want deny", decisionOf(t, out))
	}
	if !strings.Contains(out, "ephemeral") {
		t.Fatalf("the refusal must name the missing label: %q", out)
	}
}

// The same delete by the change agent is not gated on that label: k8s_editor may
// legitimately remove a real resource.
func TestEditorMayDeleteAnUnlabelledResource(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *"-o json"*) echo '{"metadata":{"name":"real-app","labels":{}}}' ;;
  *)           exit 0 ;;
esac`)
	in := bashInput("kubectl delete pod real-app -n demo", "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if strings.Contains(out, "ephemeral") {
		t.Fatalf("the ephemeral rule must apply to the cleaner only: %q", out)
	}
}

// A production target is a validation failure like any other — refused with a
// diagnostic, escalated after MAX_ATTEMPTS, never a special harder policy
// (nothing has been written yet, and a typo must not hard-block).
func TestProductionTargetIsRefusedOnTheStandardPath(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0")
	out, _ := runHook(t, bashInput("kubectl apply -f change.yaml -n production", "k8s_editor"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("production apply decision = %q, want deny", decisionOf(t, out))
	}
}

func TestProductionTargetEscalatesAfterThreeAttempts(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0")
	in := bashInput("kubectl apply -f change.yaml -n production", "k8s_editor")
	in["attempt"] = 3
	out, _ := runHook(t, in, dir)
	if decisionOf(t, out) != "ask" {
		t.Fatalf("third attempt decision = %q, want ask", decisionOf(t, out))
	}
}
```

**Note for the implementer:** `approvedFor(t, in)` builds an attestations map whose
single key is the subject hash of `in["tool_input"]` with verdict `APPROVED`. The
hash must match `hookstate.HashArgs`, so compute it by calling that function:
`hookstate.HashArgs(in["tool_input"].(map[string]any))`. Import
`github.com/blouargant/omnis/internal/hookstate` in the test.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./packaging/ -run 'Diff|DryRun|Delete|Cleaner|Editor|Production' -v`
Expected: FAIL — `validate` is still the Task 8 stub.

- [ ] **Step 2b: Teach `classify` where the verb is, and stop slicing from a fixed index**

The validators below need the operands that FOLLOW the verb. A fixed slice like
`argv[2:]` is wrong the moment a global flag precedes the verb — for
`helm --debug uninstall myrel`, `argv[2]` is `uninstall`, so the release name would
be read as "uninstall" and the `helm history` pre-check would query a release that
does not exist. This is the same class of assumption whose sibling was a Critical
finding in the previous task (a bare `-A` swallowing the verb), so it is fixed the
same way: one walk, one source of truth for where the verb is.

Change `classify` in `config/hooks/k8s-validate.py` to return the index as well,
add a small accessor beside it, and update the call sites:

```python
def classify(argv):
    """Return (tool, verb, verb_index) for a kubectl/helm invocation.

    Returns (None, None, -1) when this is not one. Global flags may precede the
    verb. Only flags known to take a SEPARATE value consume the following token;
    every other flag is boolean, so a bare global like -A or --debug cannot
    swallow the verb.

    The INDEX is returned because the validators need the operands after the verb,
    and a fixed slice is wrong the moment a global flag precedes it.
    """
    if not argv:
        return None, None, -1
    binary = argv[0].split("/")[-1]
    if binary not in ("kubectl", "helm"):
        return None, None, -1
    i = 1
    while i < len(argv):
        tok = argv[i]
        if not tok.startswith("-"):
            return binary, tok, i
        if "=" in tok:
            i += 1
            continue
        if tok in VALUE_FLAGS and i + 1 < len(argv):
            i += 2
            continue
        i += 1
    return binary, None, -1


def operands(argv, verb_idx):
    """Everything after the verb: the resource or release operands and their flags."""
    return argv[verb_idx + 1:] if verb_idx >= 0 else []
```

Three call sites follow: `raw_classify`'s empty-argv return becomes
`(None, None, -1)`; `main` unpacks `tool, verb, verb_idx = classify(argv)` and its
`rtool, rverb, _ = raw_classify(segment)`; and `validate` gains `verb_idx` after
`argv`. Run the previous task's `TestDecisionForCommandShapes` after this change —
it must still pass unchanged, since none of the decisions move.

- [ ] **Step 3: Implement the validators**

Replace the `validate` stub in `config/hooks/k8s-validate.py`:

```python
PROD_PATTERN = re.compile(r"prod|prd|production", re.IGNORECASE)
EPHEMERAL_LABEL = "omnis.dev/ephemeral"
CLEANUP_AGENTS = {"k8s_cleaner"}


def run_argv(argv, cwd):
    """Run argv with no shell and return (exit_code, stdout, stderr)."""
    try:
        p = subprocess.run(argv, cwd=cwd, capture_output=True, text=True,
                           timeout=VALIDATE_TIMEOUT)
    except subprocess.TimeoutExpired:
        return 124, "", "timed out after %ds" % VALIDATE_TIMEOUT
    except OSError as e:
        return 127, "", str(e)
    return p.returncode, p.stdout, p.stderr


def has_flag(argv, *names):
    for tok in argv:
        for n in names:
            if tok == n or tok.startswith(n + "="):
                return True
    return False


def flag_value(argv, *names):
    for i, tok in enumerate(argv):
        for n in names:
            if tok == n and i + 1 < len(argv):
                return argv[i + 1]
            if tok.startswith(n + "="):
                return tok.split("=", 1)[1]
    return None


def target_scope(argv):
    """Return the namespace and context this command targets, if named."""
    return (flag_value(argv, "-n", "--namespace") or "",
            flag_value(argv, "--context") or "")


def check_production(argv, attempt, consecutive):
    ns, ctx = target_scope(argv)
    for label, value in (("namespace", ns), ("context", ctx)):
        if value and PROD_PATTERN.search(value):
            refuse(
                "The target %s is %r, which looks like production. Nothing has been "
                "changed. If this is genuinely the intended target, say so explicitly; "
                "if it is a typo, correct the %s and retry."
                % (label, value, label),
                attempt, consecutive,
            )


def check_attested(argv, attestations, attempt, consecutive, subject):
    """Require an APPROVED verdict from the reviewer for this exact change.

    A missing verdict is a TERMINAL refusal: it must never escalate to `ask`, or
    the guarantee would be removable by disabling the reviewer agent and then
    clicking "allow".
    """
    rec = attestations.get(subject) if isinstance(attestations, dict) else None
    if not rec:
        emit("deny",
             "This change has not been reviewed. Delegate it to the k8s_validator "
             "sub-agent with the change identifier `%s`, and retry once it has "
             "recorded an APPROVED verdict. (If no validation agent is configured, "
             "Kubernetes changes are refused by design.)" % subject)
    if rec.get("verdict") != "APPROVED":
        emit("deny",
             "The reviewer REJECTED this change:\n\n%s" % (rec.get("reasons") or "(no reasons given)"))


def validate_manifest(argv, cwd, attempt, consecutive):
    path = flag_value(argv, "-f", "--filename")
    if not path:
        return validate_imperative(argv, "apply", cwd, attempt, consecutive)
    code, out, err = run_argv([argv[0], "diff", "-f", path] + scope_flags(argv), cwd)
    # exit 1 means "a diff exists" — the normal case. Only >1 is an error.
    if code > 1:
        refuse("`kubectl diff` failed, so the change could not be previewed:\n\n%s"
               % (err.strip() or out.strip()), attempt, consecutive)
    code, out, err = run_argv(list(argv) + ["--server-side", "--dry-run=server"], cwd)
    if code != 0:
        refuse("The API server rejected this change in a server-side dry run:\n\n%s"
               % (err.strip() or out.strip()), attempt, consecutive)
    return out.strip()


def scope_flags(argv):
    flags = []
    ns, ctx = target_scope(argv)
    if ns:
        flags += ["-n", ns]
    if ctx:
        flags += ["--context", ctx]
    return flags


def validate_imperative(argv, verb, cwd, attempt, consecutive):
    code, out, err = run_argv(argv + ["--dry-run=server"], cwd)
    if code != 0:
        text = (err or out).strip()
        if "unknown flag" in text or "unknown shorthand" in text:
            refuse("`%s` does not support a server-side dry run, so this change cannot be "
                   "validated as written. Express it as a manifest and apply that file "
                   "instead." % ("%s %s" % (argv[0], verb)), attempt, consecutive)
        refuse("The API server rejected this change in a dry run:\n\n%s" % text,
               attempt, consecutive)
    return out.strip()


def validate_helm(verb, argv, verb_idx, cwd, attempt, consecutive):
    ops = operands(argv, verb_idx)
    if verb in HELM_DESTRUCTIVE_VERBS:
        code, out, err = run_argv(["helm", "history"] + ops[:1] + scope_flags(argv), cwd)
        if code != 0:
            refuse("No such Helm release, so `helm %s` cannot be validated:\n\n%s"
                   % (verb, (err or out).strip()), attempt, consecutive)
        return out.strip()
    code, _, _ = run_argv(["helm", "plugin", "list"], cwd)
    preview = ["helm", "diff", "upgrade"] + ops if code == 0 else argv + ["--dry-run=server"]
    code, out, err = run_argv(preview, cwd)
    if code != 0:
        refuse("The Helm change could not be previewed:\n\n%s" % ((err or out).strip()),
               attempt, consecutive)
    return out.strip()


def validate_destructive(argv, verb_idx, agent, cwd, attempt, consecutive):
    ops = operands(argv, verb_idx)
    if has_flag(argv, "--all") or not [a for a in ops if not a.startswith("-")]:
        refuse("This deletion names no specific resource, so its blast radius is "
               "unbounded. Name the resources to delete explicitly.", attempt, consecutive)
    # ops already carries -n/--context, so scope_flags would duplicate them.
    code, out, _ = run_argv([argv[0], "get"] + ops + ["-o", "json"], cwd)
    if code != 0:
        refuse("The deletion target could not be resolved, so it cannot be checked. "
               "Verify the resource exists before deleting it.", attempt, consecutive)
    try:
        doc = json.loads(out or "{}")
    except ValueError:
        doc = {}
    items = doc.get("items", [doc]) if doc else []
    for item in items:
        meta = (item or {}).get("metadata") or {}
        labels = meta.get("labels") or {}
        if agent in CLEANUP_AGENTS and labels.get(EPHEMERAL_LABEL) != "true":
            refuse("%r is not labelled %s=true, so it is not a leftover this agent may "
                   "remove. Only resources the squad created for diagnosis are in scope."
                   % (meta.get("name", "?"), EPHEMERAL_LABEL), attempt, consecutive)
        if meta.get("ownerReferences"):
            refuse("%r is owned by %s, so deleting it is futile — its controller will "
                   "recreate it. Change or remove the owner instead."
                   % (meta.get("name", "?"), meta["ownerReferences"][0].get("kind", "a controller")),
                   attempt, consecutive)
    return "%d resource(s) would be deleted." % max(len(items), 1)


def validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations):
    check_production(argv, attempt, consecutive)
    if tool == "helm":
        preview = validate_helm(verb, argv, verb_idx, cwd, attempt, consecutive)
    elif verb in DESTRUCTIVE_VERBS:
        preview = validate_destructive(argv, verb_idx, agent, cwd, attempt, consecutive)
    elif verb in APPLY_VERBS:
        preview = validate_manifest(argv, cwd, attempt, consecutive)
    elif verb in IMPERATIVE_VERBS:
        preview = validate_imperative(argv, verb, cwd, attempt, consecutive)
    else:
        refuse("`%s %s` changes the cluster but has no validation rule, so it is refused "
               "rather than applied unchecked." % (tool, verb), attempt, consecutive)
    return preview
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./packaging/ -v`
Expected: PASS. Fix any stub-script mismatches by adjusting the stub, not by
loosening an assertion.

- [ ] **Step 5: Commit**

```bash
git add config/hooks/k8s-validate.py packaging/k8s_validate_test.go
git commit -m "feat(hooks): per-verb Kubernetes validation (diff, server dry-run, helm, destructive pre-checks)"
```

---

### Task 10: Require the attestation, declare the hook, ship it

**Files:**
- Modify: `config/hooks/k8s-validate.py` (call `check_attested`, return the diff)
- Create: `config/hooks.json`
- Create: `packaging/hooks_assets_test.go`
- Modify: `.goreleaser.yaml` (nfpms `contents:`), `packaging/windows/omnis.wxs`, the Homebrew `brews:` block, `scripts/build_wheels.py`

**Interfaces:**
- Consumes: `check_attested`, `hookstate.HashArgs` (the subject).
- Produces: the shipped `hooks.json`; the subject the agent must pass to `record_validation`.

- [ ] **Step 1: Write the failing tests**

Create `packaging/hooks_assets_test.go`:

```go
package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped hook must parse, must be fail_closed, and must point at a script
// that exists. A guard whose script is missing stops guarding silently, which
// internal/hooks' fail_closed turns into a block — but only if the declaration
// is right in the first place.
func TestShippedHooksConfigIsWellFormed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "hooks.json"))
	if err != nil {
		t.Fatalf("read config/hooks.json: %v", err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command    string `json:"command"`
				Timeout    int    `json:"timeout"`
				FailClosed bool   `json:"fail_closed"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("config/hooks.json does not parse: %v", err)
	}
	pre := f.Hooks["PreToolUse"]
	if len(pre) == 0 || len(pre[0].Hooks) == 0 {
		t.Fatal("no PreToolUse hook declared")
	}
	h := pre[0].Hooks[0]
	if !h.FailClosed {
		t.Fatal("the validation hook must be fail_closed, or a broken script silently stops validating")
	}
	if h.Timeout <= 0 {
		t.Fatal("the validation hook must declare a timeout (a cluster dry-run takes seconds)")
	}
	if !strings.Contains(h.Command, "OMNIS_SYSTEM_CONFIG_DIR") {
		t.Fatal("the command must resolve through OMNIS_SYSTEM_CONFIG_DIR so the non-FHS packages (brew/MSI/pip) find it")
	}
	if _, err := os.Stat(filepath.Join("..", "config", "hooks", "k8s-validate.py")); err != nil {
		t.Fatalf("the declared script does not exist: %v", err)
	}
}

// Every packaging channel must ship the hook, or the guarantee is absent on that
// channel. This is the same class of defect as the profile script that bypassed
// the config layers (see profile_test.go).
func TestEveryPackagingChannelShipsTheHook(t *testing.T) {
	for _, f := range []struct{ path, needle string }{
		{filepath.Join("..", ".goreleaser.yaml"), "config/hooks/k8s-validate.py"},
		{filepath.Join("windows", "omnis.wxs"), "k8s-validate.py"},
		{filepath.Join("..", "scripts", "build_wheels.py"), "hooks"},
	} {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v", f.path, err)
		}
		if !strings.Contains(string(data), f.needle) {
			t.Fatalf("%s does not ship the validation hook (looking for %q)", f.path, f.needle)
		}
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./packaging/ -run 'Shipped|Channel' -v`
Expected: FAIL — `config/hooks.json` does not exist.

- [ ] **Step 3: Require the attestation in the script**

In `config/hooks/k8s-validate.py`, add the subject computation and wire
`check_attested` into `validate`. The subject must equal `hookstate.HashArgs`
(sha256 of the canonical JSON of `tool_input`):

```python
import hashlib


def subject_hash(tool_input):
    """The change identifier. Must match hookstate.HashArgs in Go: sha256 of the
    canonical JSON of the tool arguments. json.dumps with sort_keys and no
    spaces reproduces encoding/json's object output for these values."""
    canonical = json.dumps(tool_input, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()
```

Compute it once in `main` (`subject = subject_hash(data.get("tool_input") or {})`)
and pass it through to `validate`, which now ends with:

```python
def validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations, subject):
    check_production(argv, attempt, consecutive)
    if tool == "helm":
        preview = validate_helm(verb, argv, cwd, attempt, consecutive)
    elif verb in DESTRUCTIVE_VERBS:
        preview = validate_destructive(argv, agent, cwd, attempt, consecutive)
    elif verb in APPLY_VERBS:
        preview = validate_manifest(argv, cwd, attempt, consecutive)
    elif verb in IMPERATIVE_VERBS:
        preview = validate_imperative(argv, cwd, attempt, consecutive)
    else:
        refuse("`%s %s` changes the cluster but has no validation rule, so it is refused "
               "rather than applied unchecked." % (tool, verb), attempt, consecutive)
    # Mechanical validation passed; now require the reviewer's verdict.
    check_attested(argv, attestations, attempt, consecutive, subject)
    # Everything holds. Return Proceed WITHOUT `allow`: the permission card must
    # still appear — we only make it informative by carrying the diff.
    proceed("**Validated change** (`%s %s`):\n\n```\n%s\n```" % (tool, verb, preview[:4000]))
```

**Note for the implementer:** verify the hash agreement with a test rather than by
inspection — add a case to `packaging/k8s_validate_test.go` asserting
`subject_hash` (via `python3 -c`) equals `hookstate.HashArgs` for the same
`tool_input`. If they differ (Go escapes `<`, `>`, `&` in strings by default),
either set `json.Marshal`'s escaping off with an `Encoder` in `HashArgs` or mirror
the escaping in Python — **and pin whichever choice with that test**.

- [ ] **Step 4: Declare the hook**

Create `config/hooks.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "python3 \"${OMNIS_SYSTEM_CONFIG_DIR:-/etc/omnis}/hooks/k8s-validate.py\"",
            "timeout": 60,
            "fail_closed": true
          }
        ]
      }
    ]
  }
}
```

- [ ] **Step 5: Ship it on every channel**

In `.goreleaser.yaml`'s nfpms `contents:` (beside the other `config/*.json`
entries around line 127), add:

```yaml
      - src: config/hooks.json
        dst: /etc/omnis/hooks.json
        type: "config|noreplace"
        file_info:
          mode: 0644
      # The hook script is executable code, not user configuration: it must be
      # REPLACED on upgrade so a validation bug fix actually lands.
      - src: config/hooks/k8s-validate.py
        dst: /etc/omnis/hooks/k8s-validate.py
        file_info:
          mode: 0755
```

Add the equivalent entries to the Homebrew `brews:` block, to
`packaging/windows/omnis.wxs`, and to `scripts/build_wheels.py`'s `sysconf/`
package data (follow whatever pattern each file already uses for
`config/permissions.json`).

- [ ] **Step 6: Run the suite**

Run: `go test ./packaging/ -v && make package-check`
Expected: PASS, and goreleaser validates.

- [ ] **Step 7: Commit**

```bash
git add config/hooks.json config/hooks/k8s-validate.py packaging/ .goreleaser.yaml scripts/build_wheels.py
git commit -m "feat(k8s): require a reviewer verdict, declare the validation hook, ship it on every channel"
```

---

# Milestone 5 — The validator agent

### Task 11: `k8s_validator`

**Files:**
- Create: `registry/agents/k8s_validator/agent.json`, `registry/agents/k8s_validator/instruction.md`
- Modify: `config/agents.json` (the `agents` list), `registry/agents/k8s_editor/agent.json`, `registry/agents/k8s_cleaner/agent.json`
- Test: `agent/k8s_validator_wiring_test.go`

**Interfaces:**
- Consumes: the `attest` tool group (Task 7); `record_validation`.
- Produces: an enabled agent named `k8s_validator`, nested under both mutating agents.

- [ ] **Step 1: Write the failing test**

Create `agent/k8s_validator_wiring_test.go`:

```go
package agent

import (
	"strings"
	"testing"
)

// The validator must be enabled and nested under BOTH mutating agents. It is
// deliberately NOT a squad member: the leader's tool list must not grow, and the
// gatherer doctrine puts a specialist's helper on the specialist.
func TestValidatorIsNestedUnderBothMutatingAgents(t *testing.T) {
	for _, name := range []string{"k8s_editor", "k8s_cleaner"} {
		data := readShippedAgentJSON(t, name)
		if !strings.Contains(data, `"k8s_validator"`) {
			t.Fatalf("%s does not declare k8s_validator in its subagents", name)
		}
	}
}

// It must never hold a mutating tool: a reviewer that can change the cluster is
// not a reviewer.
func TestValidatorIsReadOnlyAndCanAttest(t *testing.T) {
	data := readShippedAgentJSON(t, "k8s_validator")
	if !strings.Contains(data, `"attest"`) {
		t.Fatal("k8s_validator must declare the attest tool group, or it cannot record a verdict")
	}
	for _, forbidden := range []string{`"Write"`, `"Edit"`, `"revert"`} {
		if strings.Contains(data, forbidden) {
			t.Fatalf("k8s_validator must not hold %s", forbidden)
		}
	}
}

// validateSubAgentGraph is fatal on an unknown or disabled target, so a
// mistake here breaks the whole config at reload, not just this agent.
func TestShippedFleetResolvesWithTheValidator(t *testing.T) {
	rs, err := loadShippedRuntimeSettings(t)
	if err != nil {
		t.Fatalf("the shipped config must resolve: %v", err)
	}
	found := false
	for _, a := range rs.Agents {
		if a.Name == "k8s_validator" {
			found = true
		}
	}
	if !found {
		t.Fatal("k8s_validator is not enabled in config/agents.json")
	}
}
```

**Note for the implementer:** `loadShippedRuntimeSettings` should call
`ResolveRuntimeSettings` against the repo's `config/` directory — grep
`agent/squads_test.go` and `agent/runtime_config_test.go` for the existing way
tests load a settings fixture and reuse it rather than inventing a second one.
`readShippedAgentJSON` is the helper introduced in Task 7.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./agent/ -run Validator -v`
Expected: FAIL — the agent does not exist.

- [ ] **Step 3: Create the agent definition**

Create `registry/agents/k8s_validator/agent.json`:

```json
{
  "allow_file_attachments": false,
  "builtin": false,
  "description": "Kubernetes change validator. Given a proposed change and its change identifier, performs an independent read-only review of what the change would actually do — resource ownership (Helm / GitOps / manual), field-ownership impact, selector breadth, namespace and context correctness, label preservation — and records an APPROVED or REJECTED verdict with cited field evidence. Does not trust the caller's description of the change: it re-derives the facts from the live cluster. Never mutates anything.",
  "enabled": true,
  "leader": false,
  "max_instances": 1,
  "model_ref": "high",
  "name": "k8s_validator",
  "skills": ["k8s-modification", "k8s-investigation"],
  "softskills_dir": "",
  "tools": ["Bash", "Read", "Grep", "Glob", "Skill", "mime", "calc", "attest"]
}
```

- [ ] **Step 4: Write the instruction**

Create `registry/agents/k8s_validator/instruction.md`:

```markdown
# Kubernetes Change Validator

You review ONE proposed change to a live Kubernetes cluster and record a verdict.
You never change anything: your tools are read-only, and your only write is
`record_validation`.

The host has already checked that the API server accepts the change (a
`kubectl diff` and a server-side dry run). That answers "is it well-formed". You
answer the different, harder question: **is it the right change?**

## Do not trust what you were told

The agent that proposed this change has described it to you. Treat that
description as a claim to verify, not as fact. Re-read the live resources
yourself and base every conclusion on a field you actually saw. If your
verdict's reasons could have been written without reading the cluster, you have
not done the review.

## What to check

1. **Who owns the resource.** Read its labels and annotations.
   - `app.kubernetes.io/managed-by=Helm` plus `meta.helm.sh/release-*` means
     Helm owns it. A hand-written `patch` or `edit` on a Helm-owned resource is
     drift: the next `helm upgrade` reverts it or hits a field-manager conflict.
     REJECT a persistent change made this way and say the change belongs in the
     chart or its values.
   - Flux/Argo labels (`kustomize.toolkit.fluxcd.io/*`, `helm.toolkit.fluxcd.io/*`,
     `argocd.argoproj.io/instance`) mean Git owns it and the cluster will be
     reconciled back, usually within minutes. REJECT unless the change is
     explicitly a temporary stop-gap, and say so in your reasons.
2. **Blast radius.** Does a selector or label match more than intended? Count
   what it actually matches.
3. **Field ownership.** Is a whole fetched manifest being re-applied? That steals
   ownership of fields other managers set and can wipe them. REJECT it and say to
   change only the intended fields.
4. **Target correctness.** Namespace and context: is this the cluster and
   namespace the change is meant for? A plausible-looking typo is the failure you
   are most likely to be the only one to catch.
5. **Label preservation.** Are `app.kubernetes.io/managed-by` and `meta.helm.sh/*`
   preserved?
6. **For a deletion:** does the target exist, is it owned by a controller that
   will recreate it, and — for cleanup work — is it labelled
   `omnis.dev/ephemeral=true`?

## Recording the verdict

Finish with exactly one `record_validation` call:

- `subject` — the change identifier you were given, **copied verbatim**. Never
  invent or reconstruct it; a wrong subject means your review applies to nothing.
- `verdict` — `APPROVED` or `REJECTED`.
- `reasons` — one line per check, naming the resource and field you read. On a
  rejection, say what to do instead.

An `APPROVED` verdict is what lets the change proceed. Approving something you
did not verify defeats the entire purpose of your existence. When you cannot get
the evidence you need, REJECT and say what was missing — that is a useful
answer, and a false approval is not.
```

- [ ] **Step 5: Enable and nest it**

In `config/agents.json`, add `"k8s_validator"` to the `agents` list. Do **not**
add it to the `Kubernetes` squad's `members`.

In `registry/agents/k8s_editor/agent.json` and
`registry/agents/k8s_cleaner/agent.json`, add:

```json
  "subagents": ["k8s_validator"],
```

- [ ] **Step 6: Run to verify it passes**

Run: `go test ./agent/... -v && go build ./...`
Expected: PASS.

- [ ] **Step 7: Smoke-test against the live cluster**

Per the memory note, the running server reads `/etc/omnis`, so test the repo's
config with a temporary system layer and without the `OMNIS_CONFIG_PATH` bypass:

```bash
env -u OMNIS_CONFIG_PATH OMNIS_SYSTEM_CONFIG_DIR="$(pwd)/config" OMNIS_WEB_DIR="$(pwd)/web" go run ./server
```

Then, in a throwaway namespace (never `test-production`), ask the Kubernetes squad
to scale a deployment and confirm: the editor delegates to `k8s_validator`, the
permission card carries the diff, and an intentionally malformed manifest is
refused with the server's error and retried.

- [ ] **Step 8: Commit**

```bash
git add registry/agents/k8s_validator/ registry/agents/k8s_editor/agent.json registry/agents/k8s_cleaner/agent.json config/agents.json agent/
git commit -m "feat(k8s): add the k8s_validator agent, nested under the editor and the cleaner"
```

---

# Milestone 6 — Documentation

### Task 12: Update `CLAUDE.md` and `FEATURES.md`

**Files:**
- Modify: `CLAUDE.md`
- Modify: `internal/features/FEATURES.md`

**Interfaces:** none.

- [ ] **Step 1: Update `CLAUDE.md`**

Its self-maintenance rule requires an entry for every one of these. Make the
edits in the sections named:

- **Agent topology** — add `k8s_validator` under the Kubernetes squad, marked as a
  nested `subagents` entry of the editor and cleaner, not a member.
- **Key packages** — add `internal/hookstate/` and `internal/attest/`.
- **Lifecycle hooks** — document `fail_closed`, `DecisionAsk`, the new `Input`
  fields (`agent_name`, `attempt`, `consecutive`, `attestations`), and that
  `config/hooks.json` now ships (so the "no `hooks.json` is shipped" statement is
  now false and must be corrected wherever it appears).
- **Permission nomenclature / the gate section** — record the chain order change
  (`events → hooks → permissions → budget`) and why. **Do not claim it revived
  `permissionDecision: "allow"`** — still dead code, since nothing in `agent/`
  consumes `hooks.DecisionAllow`; record that as a known remaining gap instead.
  Also fix the now-stale order at **`CLAUDE.md:3299`**, which still reads "Mounted
  last in the before-tool chain (events → perms → hooks → budget)".
- **Configuration files** table — add `hooks.json` and `hooks/k8s-validate.py`.
- **Distribution / packaging** — note that `config/hooks/` must ship on every
  channel and that `packaging/hooks_assets_test.go` guards it.
- A new subsection describing the validation layer itself, with the two traps
  worth remembering: `kubectl diff` exits 1 on a diff, and an on-disk attestation
  would be forgeable by any agent holding `Write`.

- [ ] **Step 2: Update `FEATURES.md`**

Add to the section for the current in-development minor version (create a
`## A.B (in development)` section above the latest release tag if none exists —
never a `## A.B.C` section):

```markdown
- **Validated Kubernetes changes** — every cluster change is now previewed and checked before it can be applied: manifests are diffed and dry-run against the API server, deletions are checked for blast radius and ownership, and an independent reviewer agent must approve the change. The confirmation prompt now shows you the diff of what would change.
```

- [ ] **Step 3: Verify and commit**

Run: `go test ./internal/features/ -v`
Expected: PASS (the embedded doc must still parse to ≥1 section).

```bash
git add CLAUDE.md internal/features/FEATURES.md
git commit -m "docs: record the Kubernetes change-validation layer"
```

---

### Task 13: Surface `fail_closed` in the Settings Hooks editor

**Files:**
- Modify: `web/settings.js:4801-4830` (the hook command row)
- Modify: `web/i18n/{en,fr,es,de}.json`, then regenerate `web/i18n/locales.js`
- Modify: `web/index.html` (bump the `?v=` on the `settings.js` and `i18n/locales.js` script tags)
- Create: `server/config_hooks_test.go`

**Interfaces:**
- Consumes: `hooks.Command.FailClosed` (Task 1).
- Produces: a checkbox bound to `cmd.fail_closed`; i18n key `set.hook.failClosed`.

**Why this is not cosmetic.** Two reasons. (1) The form does not display the flag,
so a user editing this hook cannot see why it blocks — the single most confusing
thing about it. (2) It converts `fail_closed` from an **unknown** key that merely
happens to survive (because `renderHooksForm` mutates `cmd` in place) into a
**known** one. That is what protects it from the failure mode the `skills: []` bug
came from: a rendering refactor that starts materialising fresh objects silently
drops every key the renderer does not know about.

- [ ] **Step 1: Write the failing server round-trip test**

Create `server/config_hooks_test.go`:

```go
package server

import (
	"strings"
	"testing"
)

// A hooks.json round trip through the parsed-config GET/PUT must preserve
// fail_closed. If a save drops it, the validation hook silently degrades to
// fail-open — the exact failure mode it exists to prevent, reintroduced by an
// unrelated edit in the Settings UI.
func TestHooksConfigRoundTripPreservesFailClosed(t *testing.T) {
	body := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[` +
		`{"type":"command","command":"python3 /etc/omnis/hooks/k8s-validate.py","timeout":60,"fail_closed":true}` +
		`]}]}}`
	got := roundTripParsedConfig(t, "hooks", body)
	if !strings.Contains(got, `"fail_closed":true`) {
		t.Fatalf("fail_closed did not survive the round trip:\n%s", got)
	}
	if !strings.Contains(got, `"timeout":60`) {
		t.Fatalf("timeout did not survive the round trip:\n%s", got)
	}
}
```

**Note for the implementer:** `roundTripParsedConfig(t, name, body)` should drive
the **real** router — `PUT /api/config/parsed/:name` then `GET` the same path and
return the response body. `server/config_test.go` and
`server/config_agent_squads_test.go` already stand up a router with a temp config
dir for exactly this; reuse their harness rather than writing a second one, and
match their setup (auth header, `OMNIS_HOME`/config-dir env) precisely.

- [ ] **Step 2: Run it to verify it fails or passes**

Run: `go test ./server/ -run TestHooksConfigRoundTripPreservesFailClosed -v`
Expected: **PASS is the likely outcome** — the generic parsed-config path writes a
JSON delta and does not whitelist per-key, unlike `cleanAgent`. That is fine: this
test is a *regression guard*, not a red-first test. If it FAILS, you have found a
real key-dropping bug in the generic PUT path — fix that before continuing, and
say so, because it would affect every config section, not just hooks.

- [ ] **Step 3: Add the i18n key**

In `web/i18n/en.json`, beside the other `set.hook.*` keys:

```json
  "set.hook.failClosed": "Block if the hook fails",
```

Translations:

- `fr.json`: `"set.hook.failClosed": "Bloquer si le hook échoue",`
- `es.json`: `"set.hook.failClosed": "Bloquear si el hook falla",`
- `de.json`: `"set.hook.failClosed": "Blockieren, wenn der Hook fehlschlägt",`

- [ ] **Step 4: Add the checkbox**

In `web/settings.js`, inside the `matcher.hooks.forEach((cmd, cIdx) => {` block,
extend `row.innerHTML` and wire the input. Keep the existing in-place mutation
style — do **not** rebuild `cmd`:

```javascript
          row.innerHTML = `
            <input type="text" class="hook-cmd" placeholder="${escHtml(tr("set.hook.cmdPlaceholder"))}" />
            <input type="number" class="hook-timeout" min="0" placeholder="${escHtml(tr("set.hook.timeoutPlaceholder"))}" />
            <label class="hook-failclosed-label" data-tip="${escHtml(tr("set.hook.failClosedTip"))}">
              <input type="checkbox" class="hook-failclosed" />
              <span>${escHtml(tr("set.hook.failClosed"))}</span>
            </label>
            <button type="button" class="del-btn">${escHtml(tr("common.remove"))}</button>
          `;
```

and, beside the existing `toIn` handler:

```javascript
          const fcIn = row.querySelector(".hook-failclosed");
          fcIn.checked = !!cmd.fail_closed;
          fcIn.addEventListener("change", () => {
            // Mutate in place and delete rather than store false, so an
            // unchecked box leaves hooks.json clean (the Go field is omitempty).
            if (fcIn.checked) cmd.fail_closed = true; else delete cmd.fail_closed;
            markFormDirty("hooks");
          });
```

Add the tooltip key too — `set.hook.failClosedTip`, en: `"Refuse the action when
this hook times out, crashes, or is missing. Use it for a guard hook: without it,
a hook that cannot run silently stops guarding."` (translate for fr/es/de in the
same pass). Per the repo convention, tooltips use `data-tip`, **never** the native
`title` attribute.

- [ ] **Step 5: Regenerate the locale bundle and bust the cache**

Run: `make i18n`

Then bump the `?v=` query on the `settings.js` and `i18n/locales.js` script tags
in `web/index.html`, as the repo requires whenever their contents change.

- [ ] **Step 6: Verify in the browser**

Run:

```bash
env -u OMNIS_CONFIG_PATH OMNIS_SYSTEM_CONFIG_DIR="$(pwd)/config" OMNIS_WEB_DIR="$(pwd)/web" go run ./server
```

Open Settings → Hooks. Confirm: the shipped `PreToolUse` hook shows its checkbox
**ticked**; unticking it and saving removes the key; re-ticking restores it; and
editing only the command's text leaves the checkbox state alone.

- [ ] **Step 7: Run the suite and commit**

Run: `go test ./server/... && go test ./internal/features/`
Expected: PASS.

```bash
git add web/settings.js web/i18n/ web/index.html server/config_hooks_test.go
git commit -m "feat(web): show and edit a hook's fail_closed flag in Settings"
```
