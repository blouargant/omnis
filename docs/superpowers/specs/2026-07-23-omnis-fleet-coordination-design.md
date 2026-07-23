# Fleet: multi-project coordination — design

- **Date:** 2026-07-23
- **Status:** Approved (design); implementation not started
- **Author:** Bertrand Louargant (with Claude)

## 1. Summary

Add a general **multi-project coordination capability** to omnis — a *Fleet* —
whose real motivation is a **multi–Claude-Code-worker coordinator**: a way to run
several coding workers (primarily Claude Code instances, one per project) and
coordinate cross-project changes from a single conversation instead of
hand-carrying each change between repos.

> **Language-agnostic by design — this is not a Go feature.** The examples below
> use interdependent Go micro-services sharing gRPC contracts because that is the
> author's case, but the mechanism encodes **no** language, framework, build tool,
> or codegen knowledge. A project can be any language/toolchain (TypeScript, Rust,
> Python, mixed, …); anything project-specific lives in that project's **collection
> instructions**, never in the coordinator. Read every "Go / gRPC / `go.mod`"
> mention as one illustration of an arbitrary cross-project dependency, not a
> constraint.

The user talks to one coordinator (**Conductor**). The Conductor plans work that
spans several projects, the user approves the plan **once**, and the Conductor
then drives a dedicated agent per project (**Driver**), letting those agents ask
each other directly for the cross-project changes they need (update a proto,
regenerate stubs, bump a `go.mod`, implement). The Fleet is deliberately
*general*: the domain-specific "how" (the exact gRPC steps, `buf`/`protoc`
invocations, module paths) lives in each project's **collection instructions**,
never hard-coded into the mechanism.

### Motivating example (one illustration — NOT special-cased, NOT language-specific)

> Add a feature to service A that depends on a new field in service B's contract.
> Today the user manually: makes the contract change in B, regenerates B's
> client/server code, bumps the shared dependency in both A and B, then implements
> the feature in A. The Fleet lets A's Driver ask B's Driver to make the contract
> change, B does it and reports back, and the Conductor sequences the whole thing
> after a single plan approval.

The specific chore (here, Go + gRPC + `go.mod`) is **just an example**. The system
encodes no gRPC, Go, or build-tool knowledge; it encodes *coordination between
Claude-Code-style workers*. The same flow works for a TypeScript monorepo, a Rust
workspace, a Python service mesh, or any mix — the per-project specifics live in
collection instructions.

## 2. Goals / non-goals

**Goals**
- Coordinate changes across N interdependent local projects **of any language /
  toolchain** from one chat.
- Coordinate multiple Claude Code workers (the primary motivation) — one per
  project — with the omnis Coding squad as an interchangeable alternative engine.
- Per-project driving agents that communicate peer-to-peer.
- Plan-approve-execute control: one human approval per unit of work, then
  autonomous execution that only interrupts on failure/ambiguity.
- Hybrid worker: each project's coding is done either by omnis's own Coding squad
  or by an external `claude` CLI process, behind one interface — **both engines
  in v1**.
- Maximal reuse of existing omnis primitives; one net-new subsystem (external
  `claude` worker).
- Isolated experimentation via conversation forking (git-worktree per project).

**Non-goals (v1)**
- Multi-*server* fleets over A2A (all projects are local, single omnis server).
- Auto-merging a winning experiment branch back to the main tree.
- A rich web-UI "fleet dashboard" (minimal config fields only in v1).
- An Agent-SDK-based worker as an alternative to the CLI.
- A fully race-free durable peer request/reply ledger (see §12).

## 3. Vocabulary

| Term | Definition | Backed by |
|---|---|---|
| **Project** | One repo, declared as an omnis **collection** with extra profile fields (`role`, `engine`, `depends_on`). cwd + instructions come from the collection. | Existing collections + profile extension |
| **Task** | One unit of fleet work = **one omnis (Conductor) chat**. All fleet state (Drivers, worker sessions, workspaces) is scoped to the chat. | Existing session |
| **Driver** | The per-(project, task) agent: an omnis session pinned to the project's collection, registered under a task-scoped, collection-name address so peers reach it directly. | Existing session + mailbox registry |
| **Worker** | What does the coding *behind* a Driver. Swappable per project: **omnis engine** (Coding squad) or **claude engine** (external `claude` process). | Coding squad / new `claude_code` tool |
| **Conductor** | The agent the user converses with — leader of a new routable **Fleet** squad. Reads the registry, plans, gets approval, dispatches topologically, synchronizes completion. | New Fleet squad |

One-line flow: *You → Conductor → (approve plan) → Drivers (one per project) ⇄
each other for cross-project asks, Conductor sequencing the DAG.*

## 4. Decision log (locked)

1. **Architecture = peer Drivers on the teammate mailbox** (chosen over hub-and-spoke
   spawn-only and multi-server A2A). Only design that fully delivers "each project
   has its own agent and they ask each other directly," and mostly composition of
   existing primitives.
2. **Persistence-robust refinement:** peer Drivers do durable *fire-and-forget*
   cross-project asks; the **Conductor is the execution synchronizer**, advancing
   the DAG off persisted injected-turn completions (spawn-result / mailbox-backstop
   rail), **not** racy in-memory `teammate_ask` replies. Rationale in §5.
3. **Hybrid worker, both engines in v1** (omnis Coding session + external `claude`).
4. **Control model = plan → approve once → execute, gate only on failure/ambiguity.**
5. **Projects = omnis collections**, extended with `engine` + `depends_on`. Domain
   "how-to" lives in collection instructions; the system stays general.
6. **Execution is topological** over the `depends_on` DAG: sequence dependent steps,
   parallelize independent branches.
7. **Task-scoped worker sessions, one task = one omnis chat.** Avoids context rot
   (no unrelated prior-task scrollback degrading the model). New chat → new task →
   fresh workers. Chosen for end-user simplicity (a task is a concept users already
   have) and model performance.
8. **Fork = isolated experiment branch.** A forked Conductor chat gets its own git
   worktree per touched project (reusing `internal/worktree`) + fresh workers, so
   competing approaches never collide on disk. Peer addressing is therefore
   **task-scoped** (a fork never cross-talks to the original chat's Drivers).

## 5. Why the Conductor synchronizes, not peer replies (persistence rationale)

Verified against `internal/teammates/mailbox.go` and `registry.go`:

- **Message durability (default JSONL backend):** each message is appended to
  `$OMNIS_HOME/mailboxes/<addr>.jsonl` on disk, so an *unread* cross-project ask
  survives a restart and the recipient Driver's background `WatchMailbox` drainer
  picks it up on boot. **But** it is a *consume-on-read* queue (read truncates the
  file) with a single reader, and the optional Redis backend (`REDIS_URL`) is
  fire-and-forget pub/sub with **no** durability. For the Fleet: keep the JSONL
  default; do not enable Redis.
- **Address durability:** the name→inbox `SessionRegistry` (`sessions.json`,
  file-backed + cached) survives restart but keys on session name. The Fleet
  registers each Driver under a **stable, collection-name address** (namespaced
  per task) and re-materializes + re-registers Drivers on boot, so a lookup always
  resolves to a live, watched inbox.
- **Known race:** synchronous `teammate_ask` reply reads can race the background
  drainer (the reply may be injected as a turn instead of returned to the blocking
  call). Fine for fire-and-forget requests; **not** to be relied on for tight
  request/response ordering.

Consequence: peer Drivers issue durable fire-and-forget requests, and the
Conductor awaits completion through the **persisted injected-turn rail**
(spawn-result / mailbox-backstop, which produces a real persisted turn), advancing
the DAG from there. This dodges both the consume-on-read race and the reply race
without any new durability subsystem.

## 6. Project registry (collection extension)

Three new fields on the existing collection **profile** (in `collections.json`,
beside `squad`/`cwd`/`color`/`memory_size`/`auto_update`), owned by
`internal/sessions/collections.go`:

- `role: "project"` — marks a collection as a fleet project. Absent/other ⇒ a
  plain collection, untouched (no-op).
- `engine: "omnis" | "claude"` — which Worker backs this project.
- `depends_on: ["<collection-name>", …]` — dependency edges (the gRPC/contract
  graph, or any other cross-project dependency).

The Conductor enumerates fleet projects = collections with `role:"project"` and
builds the DAG from `depends_on`. cwd + instructions + memory come from the
existing collection (`internal/collectionctx`).

**Validation** (mirrors the `subagents` graph rules in `ResolveRuntimeSettings`):
- every `depends_on` entry must reference an existing project-collection;
- the graph must be **acyclic** (reject cycles with a clear message);
- a project's `cwd` must exist and be a **git repository** (required for the
  worktree isolation in §9).

**Web UI (minimal, v1):** extend the existing collection context editor with an
`engine` dropdown and a `depends_on` multi-select. Round-trips through the
existing collections routes. No new dashboard.

## 7. Drivers & task-scoped addressing

- A Driver is a **per-(project, task)** omnis session, minted **lazily** when the
  task first touches a project.
- It is pinned to the project's collection, so its cwd + instructions inject
  automatically via the existing `collection_ctx` plugin.
- It is registered in the teammate `SessionRegistry` under a **task-scoped** key —
  presented to agents as just the collection name (so `teammate_ask "project-b"`
  reads naturally), namespaced internally by the task/chat id so a fork resolves to
  *its own* Drivers.
- The **engine picks the Driver's toolset**: `omnis` → the Coding squad;
  `claude` → a thin "Claude Worker" driver squad whose agent owns the `claude_code`
  tool and relays peer asks.
- Retired on **task end** (chat archive/delete): address unregistered, worker
  session id dropped, worktree removed if forked (and unchanged).

## 8. Worker engines (the hybrid interface)

One Go interface, two implementations. Sketch:

```go
// Worker runs one task slice for one project and returns the result plus an
// opaque session reference to resume within the same fleet task.
type Worker interface {
    RunTask(ctx context.Context, in WorkerInput) (WorkerResult, error)
}

type WorkerInput struct {
    WorkspaceDir string // resolved per §9 (collection cwd, or a fork worktree)
    Task         string // the instruction for this project
    ResumeRef    string // prior session ref within this task ("" = fresh)
    // model, allowlist, etc. resolved from the project profile / config
}

type WorkerResult struct {
    Text       string
    SessionRef string // to resume within the task; discarded at task end
    Usage      WorkerUsage
}
```

- **omnis engine** — delegates the task to the Coding squad running in
  `WorkspaceDir`. `SessionRef` is an omnis sub-agent session handle (the existing
  resumable-agent mechanism). Pure reuse.
- **claude engine** — the `claude_code` tool group shells out (subprocess `cmd.Dir
  = WorkspaceDir`, since the CLI has no cwd flag):
  `claude -p "<task>" [--resume <SessionRef>] --output-format json
  --allowedTools "<allowlist>" --model <m> [--mcp-config <file>]`,
  then parses the JSON envelope for `result`, `session_id`, and `usage` /
  `total_cost_usd`. `SessionRef = session_id`. One prompt per call, `--resume`d
  **within** the task, **fresh across** tasks (session lookup is scoped to the
  workspace dir, so isolation is automatic).

**CLI contract (confirmed against the Claude Code headless/sessions/CLI-reference
docs):** `-p`/`--print` for non-interactive; `--output-format json` returns
`{result, session_id, usage, total_cost_usd, model}`; `--resume <id>` continues a
prior session **from the same directory**; multi-message streaming input is
Agent-SDK-only (not needed here). **Permissions:** default to a **configurable
allowlist** (`--allowedTools`), resolved from the project profile/config; never
`--dangerously-skip-permissions` by default. The `claude` binary is gated by
omnis's existing `internal/deps` requires-check (report-and-pause if absent, no
silent skip).

## 9. Workspace resolution & fork isolation

A single resolver feeds both engines: given (task, project) → a working directory.

- **Root chat (normal task):** the collection's `cwd` (the main working tree).
  Edits land there, exactly like using the tool in your repo today.
- **Forked chat (experiment):** a per-project **git worktree** created via
  `internal/worktree`, on a task-scoped branch, created on first touch. Two forks
  can explore competing approaches on the same projects without colliding, because
  each has its own worktree dir (and each claude worker's dir-scoped session is
  therefore isolated too).
- **Cleanup** mirrors omnis's worktree behavior: removed on task end **if
  unchanged**; **kept + warned** if it holds uncommitted experiment work.

## 10. Coordination flow (plan → approve → execute)

The Conductor (leader of a new routable **Fleet** squad) mostly *composes*
existing tools plus a small new `fleet` tool group.

**New `fleet` tools:**
- `fleet_projects` — list fleet projects + their engines + the resolved DAG.
- helpers to mint/resolve a Driver for a project within the current task.

**Reused:** planning (task graph), `ask_user` (the single approval),
`spawn`/materialize (mint Drivers), teammate (`teammate_ask`/`tell` for peer asks).

**Flow:**
1. Read the registry + DAG; build a cross-project task graph in topological order.
2. Present the whole plan → **`ask_user` approval (once)**.
3. Execute topologically — parallel where the DAG allows, sequenced where it does
   not. A project needing a change elsewhere emits a peer `teammate_ask`; the
   **Conductor waits on completion via the persisted injected-turn rail**,
   advancing the DAG from real persisted turns.
4. Gate back to the user only on **failure or ambiguity**.

A forked (experiment) task runs the identical flow, with workspaces resolved to
worktrees (§9).

**Two kinds of cross-project dependency — and who drives each:**
- **DAG-known** (declared via `depends_on`, visible at plan time): the **Conductor
  sequences** it — dispatch the upstream project's slice, await its persisted
  completion, then dispatch the downstream slice. This is the primary path; peer
  asks are the *content* channel (what the downstream needs), the Conductor is the
  *ordering* channel.
- **Discovered mid-execution** (a Driver realizes, while working, that it needs a
  change in another project that the approved plan did not include): the Driver
  emits a peer `teammate_ask`, and this is treated as an **ambiguity gate** — it
  surfaces to the user for re-approval / plan amendment rather than silently
  expanding the approved scope. This keeps "one approval per unit of work" honest.

## 11. What gets built vs reused

**Net-new**
- `fleet` tool group + Conductor agent (`registry/agents/…`) + **Fleet** squad in
  `config/agents.json`.
- `claude_code` tool group (external worker) + its `internal/deps` requires-gate.
- Collection-profile extension (`role`/`engine`/`depends_on`) in
  `internal/sessions/collections.go` (accessors + validation).
- Task-workspace resolver (worktree integration).
- Thin task-scoped driver registry over the existing mailbox `SessionRegistry` +
  materialize.
- Minimal web-UI fields (engine + depends_on) in the collection editor.
- `internal/features/FEATURES.md` line + `CLAUDE.md` update (self-maintenance
  rule).

**Reused unchanged:** collections + `collectionctx`; teammate mailbox + background
delivery + reply-backstop; `spawn`/materialize; planning/task-graph; `ask_user`;
`internal/worktree`; `internal/deps`; per-session cwd; the Coding squad.

## 12. Error handling / edge cases

- **Cycle in `depends_on`** → rejected at plan time with a clear message.
- **Missing `claude` binary** → deps gate asks to install / reports unavailable;
  the project **pauses** (no silent skip, no fallback engine swap).
- **Worker failure** (non-zero exit / error result) → Conductor pauses and surfaces
  it to the user (the "gate on failure" branch of the approved model).
- **Concurrent hits on one project's worker** → a **per-project lock** queues them
  (a `--resume`d claude conversation is linear; the omnis engine is likewise
  single-threaded per session). Within one plan the Conductor sequences steps, so
  contention is rare; the lock is the safety net for concurrent peer asks.
- **Restart mid-task** → task state (driver map, worker `session_id`s, workspaces)
  persisted on the conversation; worktrees persist on disk; claude sessions resume
  by id; Drivers re-materialize + re-register on boot.
- **Fork cleanup** → see §9.

## 13. Testing strategy

- **Unit:** DAG build + cycle detection + topological order; workspace resolver
  (worktree create/cleanup); `claude_code` JSON-envelope parsing (`session_id` /
  `usage` capture) driven by a **fake `claude` script on PATH**; `fleet_projects`
  enumeration from a collections fixture; profile validation (bad edge, missing
  cwd, non-git cwd).
- **Integration:** a two-project fixture — one `omnis` engine, one `claude` engine
  (fake binary) — driven through a full plan; assert cross-project ask delivery,
  topological ordering, and that a fork spins isolated worktrees.
- Standard omnis conventions (`make test`, table-driven tests). The fake `claude`
  binary is placed on PATH via a temp dir (+ `binpath`) so no network/model is
  needed.

## 14. v1 scope & phased implementation

**In scope (v1):** both engines; plan-approve-execute; collections-as-projects;
peer asks + Conductor sync; topological execution; fork = worktree-isolated
experiment.

**Recommended implementation phases** (all v1 scope; phased for working
checkpoints — to be detailed in the implementation plan). Note the ordering
**de-risks the coordination layer first, it does not deprioritize the motivating
Claude-Code engine** — phase 1 uses the zero-subprocess omnis engine purely to
prove the plan/approve/execute + peer-messaging machinery cheaply, then phase 2
delivers the Claude Code worker that is the point of the feature:
1. Registry + Conductor/Fleet squad + `fleet_projects` + plan-approve-execute with
   the **omnis engine only** (normal tasks, no fork) — validates coordination.
2. **claude engine** (`claude_code` tool + deps gate + envelope parsing) — the
   primary motivating worker.
3. **Fork = worktree-isolated experiments** + workspace resolver.
4. Web-UI fields + FEATURES.md/CLAUDE.md + polish.

**Out of scope (future extensions):**
- Non-consuming, correlation-keyed **fleet request ledger** (fully race-free
  durable peer request/reply) — only if the Conductor-synchronized model proves
  insufficient.
- Multi-server A2A fleet.
- Agent-SDK worker as an alternative to the CLI.
- Auto-merging a winning experiment branch.
- Rich web-UI fleet dashboard.

## 15. No-op contract

A collection without `role:"project"` is byte-identical to today. A build with no
fleet projects declared never mints a Driver, never touches the mailbox, and adds
no behavior. CLI/TUI surfaces are unaffected (the Fleet is a server-mode UI
capability; the `fleet`/`claude_code` tools work anywhere they are mounted, but the
squad is opt-in via `config/agents.json`). With no `claude` binary the claude
engine reports unavailable and pauses — it never degrades another project's path.

## 16. Open questions for implementation planning

- Exact storage of per-task fleet state on the conversation (new fields vs a
  side-file) — resolve in the plan.
- Whether the "Claude Worker" driver is a dedicated squad or a leaderless
  single-agent squad per the leaderless-squad rules.
- Allowlist defaults for the claude engine (per-project override vs a global
  fleet default).
