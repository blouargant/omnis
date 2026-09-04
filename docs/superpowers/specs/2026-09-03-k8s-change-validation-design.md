# Kubernetes change-validation layer — design

**Status:** design approved, not implemented. **Shape:** two generic Go extensions
to the hooks engine + one attestation tool group, then the whole Kubernetes policy
expressed as configuration and one new agent. No kubernetes-specific logic enters
the Go core.

## 1. Problem

The Kubernetes squad (`config/agents.json`, squad `Kubernetes`) has two agents that
mutate a live cluster: **`k8s_editor`** (apply / patch / scale / helm upgrade) and
**`k8s_cleaner`** (delete). Validation of those changes already exists — but only as
*instruction*:

- `registry/skills/k8s-modification/SKILL.md` phase 4 ("Preview before you change
  (always)") mandates `kubectl diff`, `helm diff`, then `--dry-run=server`.
- Phase 1 §3 forbids modifying production namespaces/contexts "without an explicit
  user override".
- `k8s_editor`'s own description promises it "previews every change with kubectl/helm
  diff and dry-run".
- `k8s_cleaner`'s description promises it "removes only ephemeral resources", keyed on
  the `omnis.dev/ephemeral=true` label.

What the **host** actually guarantees today is narrower: user confirmation only.
`config/permissions.json:293` puts every `kubectl apply|delete|patch|edit|scale|…` in
the `ask` tier, and `:298` does the same for `helm install|upgrade|uninstall|rollback`.
Nothing verifies that a dry-run ran, that it passed, who owns the resource, that the
production guard held, or that a deletion target carried the ephemeral label.

An instruction is advice. This design makes the validation a guarantee.

## 2. Goals and non-goals

**Goals**

1. No Kubernetes mutation — **delete included** — reaches a cluster without mechanical
   validation appropriate to its verb.
2. An independent agent reviews the *semantics* of the change, and its review is
   **structurally required and unforgeable**.
3. A failed validation returns a diagnostic the editor can learn from and correct.
   After **3** attempts on the same command, the user is asked what to do.
4. The mechanism stays **generic**: reusable for any domain, per the design contract
   in `CLAUDE.md` — "the same binary becomes a code reviewer, Kubernetes triage
   assistant, or DBA helper purely by mounting different tools, skills, and MCP
   servers. No code changes required to retarget the agent."

**Non-goals**

- Replacing the permission layer. It stays, and still asks — defence in depth.
- Bypassing the permission card on a successful validation (see §5.7).
- GitOps write-back. Detecting a Flux/Argo-owned resource and recommending a Git
  change stays advisory.
- Special-casing production with a harder policy (see §9).

## 3. Why the hooks engine and not a Go gate

The obvious implementation is a Go `BeforeToolCallback` (an `internal/k8sguard`),
mirroring `buildPermissionGate` and `budgetCallbacks`. It was rejected: it carves one
domain into the generic agent core, violating the design contract quoted above.

Evolving `internal/hooks` instead keeps the core domain-free and closes a **pre-existing
parity gap**: `internal/hooks/run.go:49` documents `permissionDecision` as
`allow|deny|ask`, but `applyJSONOutput` implements only `deny` and `allow` — `"ask"`
falls through to `DecisionProceed`. We are completing a protocol the file already
claims to implement, not inventing an extension.

Two costs are accepted deliberately:

- **~40 ms of interpreter startup on every `Bash` call in the whole fleet.** A
  `PreToolUse` matcher filters on the *tool name* only, never on arguments, so the
  validation hook fires for `coder`, `linux_admin`, `investigator` — everything. A Go
  gate would have cost zero for non-k8s commands. On a real `Bash` call this is
  absorbed, but it is a fleet-wide tax and it is real.
- **Duplicated shell parsing.** `core/permissions/match_bash.go` already implements
  compound-command splitting (`splitCompound:64`), wrapper stripping
  (`stripWrappers:156`) and sub-command extraction (`bashSubcommands:190`). The hook
  script must reimplement them in Python. Mitigated by a shared-corpus test (§10), not
  eliminated.

## 4. Architecture

| Piece | Where | Nature |
|---|---|---|
| **A. Engine** | `internal/hooks`, `agent/hooks_plugin.go`, `agent/build_subagents.go` | Generic Go. `ask` decision, attempt counters, attestation injection, `fail_closed`, chain order. Knows nothing about kubernetes. |
| **B. Attestation** | new tool group + a store on `Infrastructure` | Generic Go. Lets a designated reviewer agent record an unforgeable verdict a hook can read. |
| **C. Policy** | `config/hooks.json` + `config/hooks/k8s-validate.py` | Configuration. All per-verb Kubernetes knowledge lives here. Chooses `deny` vs `ask`. |
| **D. Judgment** | `registry/agents/k8s_validator/` | A new read-only agent, nested under the two mutating agents. |

Division of responsibility: **C guarantees** that nothing is applied without mechanical
validation; **D judges** whether the change is the right one; **B** is what makes D
impossible to skip. The engine decides nothing about policy — that stays in config.

## 5. Component A — hooks engine (generic)

### 5.1 `DecisionAsk`

`internal/hooks/run.go:69` defines `DecisionProceed / DecisionBlock / DecisionAllow`;
add `DecisionAsk`, and a `case "ask":` in `applyJSONOutput`.

**Aggregation order, explicit because several hooks may match one tool:
`Block > Ask > Allow > Proceed`.** A second hook must never soften another hook's
`deny` into a question.

`Outcome.Blocked()` stays `Decision == DecisionBlock`, so every other consumer
(`PostToolUse`, the `UserPromptSubmit` path, the fire-and-forget bus listeners) ignores
`Ask` with no change: it is meaningless outside `PreToolUse`.

### 5.2 Asking the user

`hookToolCallbacks` (`agent/hooks_plugin.go:156`) takes an `*askuser.Registry`. It is
already in scope at the only call site (`agent/squad.go:248`, immediately above
`budgetCallbacks` on line 257), so `agent/build_subagents.go` needs no signature change.

A helper `askHookPermission(tc, reg, sid, toolName, reason)` mirrors `askBudget`. It
passes `tc` as the context, which inherits the right contract for free: an unanswered
card is ended by a Stop/session-end/shutdown but **survives a mere client
disconnect**.

**Exactly two choices: "allow once" and "deny".** The five scopes of the permission
asker are deliberately not reused — an "allow always" there is persisted as an `allow`
rule, which would permanently disable this guard. A hook question is per-call by nature.

With **no registry** (a CLI one-shot, an example binary) the ask **fails safe and
denies**, exactly as the budget gate does.

### 5.3 Attempt counters

Two counters, both process-wide, escalation on **either**:

- **Exact:** `(session, tool, sha256(canonical args))`. This is "the same command". It
  has a useful property: a genuinely *corrected* manifest hashes differently and starts
  again at 1, so the counter only climbs when the agent retries the identical thing —
  precisely the "it is not learning" case.
- **Coarse:** `(session, tool)`, counting *consecutive* blocked calls, reset by any
  non-blocked call. This closes the hole the exact counter leaves open: an agent
  retrying with endlessly different but still-wrong variants would otherwise get a
  fresh budget every time and never escalate. The turn budget (2000 calls / 10M tokens)
  bounds that loop, but slowly.

`N = 3`, matching `agentCapGraceCalls` (`agent/budget_plugin.go:55`).

**Update timing differs, and it must.** The exact counter is incremented *before* the
script runs (it counts attempts on identical args, which is knowable up front), so the
first call sees `attempt: 1`. The coarse counter can only be updated *after* the outcome
is known — incremented on `Block`, reset on anything else — so the value handed to the
script is the count accumulated by *previous* calls. Both are dropped by
`Forget(session)` on session end.

**The script, not the engine, compares them to a threshold.** That is what keeps this
extension generic rather than a disguised Kubernetes feature.

### 5.4 Attestation and caller injection

`Input` (`internal/hooks/run.go:17`) gains an `attestations` map, populated by the
engine from the store (§6) for the current session. The hook reads its stdin and sees
the verdicts; no query channel is invented.

`Input` also gains **`agent_name`**, from `tc.AgentName()` (the same source
`budget_plugin.go` uses for its per-agent cap). Without it a hook cannot tell *which*
agent is calling, and §7.4's cleaner-specific rule — requiring `omnis.dev/ephemeral=true`
only for `k8s_cleaner`, since `k8s_editor` may legitimately delete a real resource —
would be unimplementable. It is additive and generally useful to any hook.

### 5.5 `fail_closed` — opt-in, per hook command

`internal/hooks/run.go:99-106` states the current contract: *"Non-blocking command
errors (exit codes other than 0/2, timeouts, safety-floor blocks) are logged to stderr
and do not stop the turn — matching Claude Code, only exit code 2 is treated as
blocking."*

So today: **hook timeout → the mutation proceeds unvalidated. Script error → proceeds.
Script missing → proceeds.** The last one is the packaging risk in §12: if the nfpms /
brew formula / WiX / wheel data omit the `hooks/` directory, the shell returns 127, the
engine logs to stderr, and the entire guarantee vanishes silently on that install
channel. A guard whose absence is undetectable is not a guard.

A hook command declared `"fail_closed": true` therefore turns any exit code other than
0/2, any timeout, and any command-not-found into `DecisionBlock`, with a reason naming
the cause.

**Opt-in, not default:** a legitimate hook script may exit non-zero without meaning to
block, and keeping the Claude Code default preserves portability of existing scripts.
Our validation hook sets it explicitly.

### 5.6 A shared store, and an invariant that breaks

The counters and attestations are mutable state, which contradicts the current doc
comment on `hookToolCallbacks`: *"there is no shared mutable state to thread: each
callback queries engine.Snapshot() live, so building an independent pair per sub-agent
is equivalent."*

The store therefore lives on `Infrastructure` (beside `SteerStore` / `GoalStore` /
`Budget`, so it survives hot-reload) and is threaded in, **and that comment must be
corrected**. Without this, `k8s_editor` and the leader would count attempts on the same
command separately, and a delegation bounce would silently reset the counter.

### 5.7 Chain order: `events → hooks → permissions → budget`

The sub-agent `BeforeToolCallback` chain (`agent/build_subagents.go:204-214`) is
currently `events → permissions → hooks → budget`.

**Defect it causes.** On `kubectl apply -f x.yaml` the permission card
(`config/permissions.json:293`) is shown *before* the hook has validated anything. The
user approves, then the hook refuses. Over three attempts they click three times for
nothing. The real damage is not annoyance: it **trains the user to click "allow"
reflexively**, degrading the permission layer — the only protection that existed before
this work.

**What it does NOT fix — claim withdrawn after Task 2's review.** An earlier draft of
this section held that the reorder also revives hooks' documented
`permissionDecision: "allow"` bypass (`internal/hooks/run.go:76`). That was wrong.
`DecisionAllow` *is* dead in the tool path, but the ordering is not why: **nothing in
`agent/` consumes `hooks.DecisionAllow` at all** — verified by grep, whose only other
hits are `internal/hooks`' own tests and the unrelated same-named constant in
`core/permissions`. `hookToolCallbacks` returns non-nil only on `out.Blocked()`, and a
`nil` return means "proceed", which is not a signal the permission gate can act on.
Honouring `"allow"` would additionally require the hook callback to tell the gate to
skip (e.g. by seeding the approval cache). This order is a **precondition** for that
feature, never the feature itself — and the double-ask defect above is on its own a
sufficient reason for the reorder.

**Blast radius of the reorder: nil.** No `hooks.json` exists in any layer (`config/`,
`/etc/omnis/`, `$HOME/.omnis/` — all verified absent), so nothing depends on the current
order.

**We do not use `allow` at all.** A green validation returns `Proceed`, so
the permission card still appears: removing the user's confirmation on a cluster
mutation is not acceptable. Instead the card gets *better* — the hook returns the diff
via `systemMessage`, which is surfaced to the user. Today they approve
`kubectl apply -f x.yaml` blind; after this they approve it with the diff in hand.

The budget stays **last**, unchanged, so a call refused by a hook or by the user is
still not charged.

## 6. Component B — attestation tool group (generic)

**Why the obvious design fails.** The natural approach — the validator writes its
verdict to a file, the hook reads it — is **forgeable**: `k8s_editor` has the `Write`
tool and can author its own approval. (`k8s_cleaner` has no `Write`, but one agent is
enough to void the guarantee.) An on-disk attestation is therefore dead on arrival, and
it is the kind of hole that looks like it works in testing.

**Design.** The attestation lives in the process-wide store from §5.6:
`(session, subject-hash) → {verdict, reasons, recorded_at}`. A tool group — mounted
**only** on `k8s_validator` — writes into it via
`record_validation(subject, verdict, reasons)`. The engine injects the map into the hook
input (§5.4). Process memory is unreachable from `Write`, and the tool is not mounted on
the mutating agents, so the verdict cannot be forged.

**Subject hash.** The same value both sides compute independently: canonicalised
manifest content plus normalised argv. Because it binds the *content*, having v1
validated and then applying v2 is refused automatically — one cannot get a verdict on
one manifest and apply another.

**Freshness.** Session-scoped with a TTL, dropped by `Forget(session)`. It survives a
hot-reload (the store is on `Infrastructure`) and is lost on a process restart, in which
case the change must be re-validated — the correct fail-closed direction.

**Generic reuse.** This is a "this action requires attestation from that reviewer"
mechanism. It would gate a `git push` on a code review without a line of change.

## 7. Component C — policy in configuration

### 7.1 Declaration

`config/hooks.json` declares one `PreToolUse` entry, matcher `Bash`:

```json
{ "hooks": { "PreToolUse": [ { "matcher": "Bash", "hooks": [ {
  "type": "command",
  "command": "python3 \"${OMNIS_SYSTEM_CONFIG_DIR:-/etc/omnis}/hooks/k8s-validate.py\"",
  "timeout": 60,
  "fail_closed": true
} ] } ] } }
```

Hooks run through the shell, so the env expansion works — and it is the correct form for
the non-FHS packaging wrappers (Homebrew, MSI, pip), which relocate exactly via
`OMNIS_SYSTEM_CONFIG_DIR`. The 60 s timeout accommodates a `kubectl diff` plus a
server-side dry-run against a slow cluster; the script sets a shorter internal deadline
so it can return an explained `deny` rather than being killed.

### 7.2 Input and fast path

The script reads `tool_input.command` (verified: `core/tools/bash.go:443`), `cwd`,
`attempt` and `consecutive`. Note `BashIn.Cwd` is `json:"-"`, so the working directory
comes from the hook input's own `cwd` field, not from the tool args.

First test is a trivial "does this text mention kubectl or helm", then exit 0. Everything
else in the fleet must pay only interpreter startup.

### 7.3 Parsing: prove read-only, or refuse

> **This section was rewritten during implementation.** The original rule was
> "recognise a mutation and validate it", and it did not converge: three fix rounds,
> and each round's fix left a *new* spelling of a mutation unrecognised. The ways to
> spell a mutation are not enumerable; the read-only commands are. So the rule is
> inverted, and the §7.4 table below is unchanged — it is what a segment reaches once
> it has failed to prove itself a read.

Strip wrappers, split compound commands (`&&`, `||`, `|&`, `;`, `|`, `&`, newline) and
classify **each segment independently** — `kubectl get x && kubectl delete y` must be
caught. Splitting mirrors `compoundOps` in `core/permissions/match_bash.go`, pinned by a
shared-corpus parity test.

Then, per segment: **a segment proceeds only if it is provably read-only. Everything
else is refused or validated.** There is no "unrecognised, therefore harmless" branch —
that branch is what every proven bypass went through.

Proving a read means clearing every one of these, each an enumerable set that **fails
closed** when it does not match:

| Must be established | Fails closed when |
|---|---|
| The segment hides no nested command | it contains `$(`, a backtick, `<(`, `>(`, or `${…$(}` |
| It can be tokenised | quotes are unbalanced |
| After stripping wrappers, a bare program name is at the head | the head is empty, a flag, or a quoted payload — i.e. a wrapper ate the binary or `flock -c` handed a string to `sh` |
| The head does not execute a string handed to it | it is a shell, interpreter, `ssh`, `awk`/`sed`, `make`, `watch`, `busybox`, … |
| The verb position is determinable | an unrecognised flag sits before the verb, so which token is the verb cannot be known |
| The verb is a known read | it is anything else — including a verb whose *sub*-verb decides (`auth`, `config`, `rollout`, `plugin`) |

A wrapper is stripped by an **explicit spec** — its own value-taking flags and its count
of bare operands — never by guessing, because guessing is what left `30` looking like the
binary in `timeout 30 kubectl get pods`. A wrapper missing from the spec is not a
loophole: whatever it leaves at the head fails the bare-program-name rule.

The cost of this direction is **friction**, and friction is the acceptable failure: it is
visible, it names its cause, and the agent can act on it. The cost of the old direction
was a **silent bypass**. A guard that refuses honest work does get disabled, though, so
the friction is bounded deliberately — ordinary control flow, env assignments, wrappers,
brace expansion, and naming the binary without running it all proceed.

**The script must never execute the mutation it is inspecting.** It runs only preview
forms (`diff`, `--dry-run=server`, `helm diff`/`template`), and a provably read-only
segment causes it to execute nothing at all.

**Verification is by execution, not by reading.** Every safety property asserted about
this parser in a comment or a review has to be run: three separate comments in review
claimed properties the code did not have, and each cost a round. The durable gate is
`TestProceedsAreInertUnderBash`, which runs every command the guard waves through under
a real shell whose only reachable binaries are stubs and fails if a kubectl/helm stub is
invoked with a mutating verb. A decision-only table cannot see the defect that matters
most — a command that proceeds and then mutates — and four families of exactly that
defect were invisible to one.

### 7.4 Per-verb semantics

| Verbs | Validation |
|---|---|
| `apply`, `create`, `replace` with `-f <file>` | `kubectl diff -f`, then `apply --server-side --dry-run=server -f`. **`kubectl diff` exits 1 when a diff exists** — that is the normal case, not an error; only `>1` fails. |
| `patch`, `set`, `scale`, `annotate`, `label`, `expose`, `autoscale` | Replay as argv with `--dry-run=server`. An "unknown flag" response is fail-closed: refuse and point at a manifest. |
| `helm install`, `helm upgrade` | `helm diff upgrade` when the plugin is present (`helm plugin list`), else `--dry-run=server`. |
| `delete`, `helm uninstall`, `helm rollback`, `drain` | **No dry-run** — it proves nothing (`delete --dry-run=client` merely lists names). Instead: resolve the target set with `-o json` to **count the blast radius**; refuse a `delete` with no name and a broad or absent selector (`--all`); report `ownerReferences` (deleting an owned pod is futile, it returns); and for `k8s_cleaner`, **require `omnis.dev/ephemeral=true`**. |
| any verb, context or namespace matching `prod|prd|production` | Standard failure path (§9). |

### 7.5 Verdicts

- **All green** → exit 0 (`Proceed`), with the diff returned via `systemMessage`.
- **Failure, counters below N** → `permissionDecision: deny` with
  `permissionDecisionReason` carrying **the raw server error and what to fix**. It
  reaches the agent as `[BLOCKED BY HOOK] Bash: …` (`agent/hooks_plugin.go:180`) — this
  is the pedagogical message.
- **Failure, either counter at N** → `permissionDecision: ask`, reason recounting the
  failures.
- **Unparsable** → `deny`, "use a manifest file".
- **No `APPROVED` attestation for this subject hash** → `deny`, naming the cause.
  **This refusal is terminal: it never escalates to `ask`, whatever the counters say**
  (see §7.6).

### 7.6 When the validator is disabled

If `k8s_validator` is disabled or absent, no attestation can exist, so **every
Kubernetes mutation is refused**, with a message naming the cause ("validation agent
disabled"). This is deliberate: a guarantee must not be removable by unticking an agent
in the Settings UI. The refusal is explicit rather than a silent degradation to
mechanical-only validation.

**It follows that a missing attestation must not participate in the escalation of §9.**
Were it to reach `ask` after three attempts, the guarantee would be removable by
unticking the agent *and then clicking "allow"* — reinstating exactly the hole this
section closes. A missing or non-`APPROVED` attestation is therefore a terminal refusal;
only *validation* failures escalate.

## 8. Component D — the `k8s_validator` agent

**Role.** Read-only, no mutating tools. It judges what a dry-run structurally cannot
see: a Helm-owned resource being patched by hand (silent drift at the next
`helm upgrade`); a GitOps-owned resource (reconciled away within minutes); ownership
theft from re-applying a whole fetched manifest; an over-broad selector; lost
`meta.helm.sh/*` / `app.kubernetes.io/managed-by` labels; deletion of a non-ephemeral
resource. And the underlying question: **does this change match the stated intent?** A
dry-run answers "the API server accepts it"; the validator answers "it is the right
change".

**Anti-rubber-stamp clause.** Its instruction must carry the discipline `k8s_auditor`
already states — *"does NOT trust a first audit: it independently re-enumerates"*. The
validator **re-derives** the intent from the manifest and cluster state and does not
trust the editor's narration. Without this clause explicitly written, a validator
becomes an expensive "looks good to me".

**Model: `high`.** The gatherer doctrine (cheap model retrieves, strong model judges)
applies inverted here — validation *is* judgment. `k8s_auditor` and `k8s_investigator`
are already `high`; `k8s_editor` is `hosted`.

**Wiring.** `subagents: ["k8s_validator"]` on both `k8s_editor` and `k8s_cleaner`; **not**
a squad member, so it is invisible to `k8s_leader` and the leader's tool list does not
grow. Four consequences:

1. It must be **enabled** in `agents.json` — `validateSubAgentGraph` is fatal on an
   unknown or disabled target, so a mistake here breaks the whole config at reload,
   not just this agent.
2. Shared by two callers, it is built once but gets a **fresh wrapper per mount point**,
   which is what stops an in-flight call from one caller reporting "already running" to
   the other.
3. `max_instances: 1` — sequential judgment, no fan-out — so its per-agent cap counter is
   its own.
4. **No `max_tool_calls` initially.** Its cost is quadratic in tool calls (it accumulates
   cluster JSON, re-sent on each step of its own flow loop); the sub-agent output shaper
   absorbs part of that. A cap that fires on honest work gets removed, so measure first
   with `squad-bench` (in the sibling `omnis-benches` repo) and set it from data.

## 9. Failure and escalation policy

One uniform path, including for production:

1. Validation fails → `deny` with a diagnostic → the editor corrects and retries.
2. Attempts 2 and 3 on the same command → same.
3. Either counter reaches 3 → `ask`: the user decides, seeing why it failed.

**No dedicated production policy.** At validation time nothing has been written to the
cluster, so a refusal costs nothing and is reversible, whereas a hard block would punish
a typo (`production-test` typed for `prod-sandbox`). The user is still guaranteed to be
in the loop: a production mutation is never applied silently — it is refused, or put to
them. The uniform path therefore *implements* the "explicit user override" the playbook
already requires rather than weakening it.

## 10. Testing

**Engine** (`internal/hooks/run_test.go` already exists and covers `exit 2`, `deny`,
`allow`): the `ask` decision; the `Block > Ask > Allow > Proceed` aggregation, in
particular that a second hook cannot soften a `deny`; `fail_closed` on all three leaking
paths — exit code 1, timeout, **command not found**; and injection of `attempt`,
`consecutive` and the attestation map.

**Chain order:** a test asserting `events → hooks → permissions → budget`, named after
the defect it prevents — otherwise a refactor silently reintroduces the double-ask. The
chain is assembled in two places, so **both** need a guard: the sub-agent call site
(where all four callbacks share one type, so a positional swap compiles and passes a
helper-only test) and the root plugin list.

**The script** — the genuinely safety-critical part. A table-driven Go test that pipes
JSON into the **real script as shipped** (`t.Skip` when `python3` is absent), so it stays
inside `make test` instead of rotting in a separate pytest suite. Required cases:
`kubectl get x && kubectl delete y` (the delete is caught); `sudo kubectl delete …`
(caught); heredoc `apply -f -` (fail-closed refusal); `kubectl diff` exiting 1 (**not** an
error); `delete --all` (refused); a non-ephemeral delete by the cleaner (refused); a
`prod` namespace (refused via the standard path); a non-k8s command (immediate
`Proceed`).

**Parsing divergence** (the cost accepted in §3): a **shared corpus** of command strings
run through both `splitCompound`/`stripWrappers` (`core/permissions/match_bash.go`) and
the Python splitter, asserting identical segmentation.

**Shipped assets.** The precedent is `packaging/profile_test.go`
(`TestProfileScriptDoesNotBypassConfigLayers`), which tests a packaging asset. Add: the
shipped `hooks.json` parses, references a script that **exists**, and that script is
listed in the nfpms, the brew formula, the WiX manifest and the wheel package data. This
is the test that makes a silently missing guarantee detectable.

**Attestation.** That the hash binds content: validate v1 of a manifest, apply v2 →
refused. And a tool-resolution test asserting the attestation group is **not** mounted on
`k8s_editor` — the property that makes the verdict unforgeable, and invisible on reading.

**Settings round-trip.** Verified good news: the Hooks form **mutates the object in
place** (`web/settings.js:4813-4821` — `cmd.command = …`, `cmd.timeout = …`) rather than
rebuilding a fresh object, so an unknown `fail_closed` key **survives** an edit. No hole
here. A round-trip test still locks it, because that is exactly how the `skills: []` bug
happened: a rendering refactor that started materialising objects. Since the form does
not *display* it, add a **fail-closed checkbox** — otherwise a user editing this hook
cannot see why it blocks.

## 11. No-op contract

With no `hooks.json`, `hookToolCallbacks` returns before doing any work
(`if len(cfg.Match(...)) == 0`), the counter store is never touched, and `DecisionAsk` /
`fail_closed` are unreachable.

**The reorder is an exact no-op too:** when the engine is absent (or this is the router
squad) `hooksBeforeTool` is `nil` and is not appended to the chain — so the chain is
`events → permissions → budget` either way, byte for byte.

With no `ask_user` registry the escalation **fails by refusing**, as the budget gate
already does. CLI and TUI inherit the whole mechanism (the hooks engine and the ask-user
registry both live on `Infrastructure` on every surface); they are not specially
engineered for it.

## 12. Packaging and documentation upkeep

**Packaging must ship `config/hooks/`** — nfpms (`→ /etc/omnis/hooks/`), the Homebrew
formula, `packaging/windows/omnis.wxs`, and the wheel's `sysconf/` package data in
`scripts/build_wheels.py`. §5.5 makes an omission fail closed; §10 makes it fail the
build.

**`CLAUDE.md`** per its own self-maintenance rule: a new agent, a new tool group, a new
config file (`hooks.json`), hooks-engine changes, and a before-tool chain order change all
require updates. **`internal/features/FEATURES.md`** gets a bullet — this is user-facing:
the permission card now carries the diff.

## 13. Known limitations and follow-ups

- **~40 ms per `Bash` call, fleet-wide** (§3). Accepted; revisit if it shows up in
  latency measurements.
- **Duplicated shell parsing** (§3). Guarded by a shared-corpus test, not eliminated.
  Follow-up worth considering: expose `splitCompound` through a small subcommand
  (`omnis bash-split`) so there is one implementation.
- **The hook's own `kubectl` subprocesses are neither permission-gated nor recorded as
  tool calls** in the event log. Consistent with the hooks trust model (hooks bypass the
  permission layer by design), but it means the audit log does not show the validation
  reads.
- **Validator cost is unmeasured.** Deliberately uncapped at first (§8); measure with
  `squad-bench` before setting `max_tool_calls`.
- **GitOps is detection only.** No Git write-back; recommending a source change stays
  advisory.
- **The attestation group is mounted by configuration**, so an operator who adds it to
  `k8s_editor`'s `tools` in Settings lets that agent sign its own changes. The test in
  §10 catches the shipped config, not a later hand-edit. Same class as disabling the
  validator (§7.6): the guarantee holds against the *model*, not against a determined
  operator — which is the correct boundary, but worth stating.
- **Attestations are lost on process restart** (not on hot-reload). A change validated
  before a restart must be re-validated — the correct direction, but worth knowing.
- **Parked parser residuals** (§7.3), each refusing safely rather than leaking:
  a heredoc body under a non-kubectl head is refused (structural, shared with
  `splitCompound`); `git -c alias.x='!cmd'` bypasses the guard, because listing `git`
  as a string-launcher would refuse `git commit -m "…kubectl…"` on every coder turn;
  and `watch -n 2 kubectl get pods`, `kubectl auth whoami` and `kubectl cp` are each
  refused pending one set entry. The open-ended case — an arbitrary unlisted wrapper
  that `exec`s its argv — is undecidable from argv alone (`setsid kubectl delete` is
  shape-identical to `echo kubectl delete`); the common members are listed and the
  bare-program-name rule catches what a wrapper leaves behind.
