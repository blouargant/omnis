# Architecture

This document maps the codebase and explains how the pieces interact.

## High-level picture

```
                      ┌────────────────────────────────────┐
                      │        omnis (root)        │
                      │  (launcher: REPL or web UI)        │
                      └─────────────────┬──────────────────┘
                                        │ wires
                                        ▼
        ┌──────────────────────────────────────────────────────────┐
        │  Instance (one generation)                               │
        │   ├─ Squad "omnis"     ← Omnis ROUTER (default new chat) │
        │   ├─ Squad "default"   ← leader + full team              │
        │   ├─ Squad "research"  ← leader + web_agent + summariser │
        │   └─ Squad "…"         ← any number, defined in agent.json│
        │  New chats start at the router, which routes to a squad   │
        └─────────────────┬────────────────────────────────────────┘
                          │ (Omnis routes → per-session squad)
                          ▼
        ┌──────────────────────────────────────────────────────────┐
        │  Squad's lead agent                                      │
        │  (generic Claude-Code-style coordinator, no domain)      │
        └───────┬──────────┬───────────┬────────────┬──────────────┘
                │          │           │            │
                │ tool     │ tool      │ subagent   │ subagent
                ▼          ▼           ▼            ▼
        ┌────────────┐┌─────────┐┌───────────────┐┌────────────────┐
        │ core/tools ││internal ││ investigator  ││  summariser    │
        │ fs / bash  ││  todo   ││ (read-only)   ││ (compress out) │
        │ grep / glob││  tasks  │└───────────────┘└────────────────┘
        │ revert     ││  bg     │
        └────────────┘│worktree │
                      │teammate │
                      └─────────┘

        ┌──────────────┐  ┌─────────────────┐  ┌───────────────┐
        │  skills/*    │  │ mcp_config.json    │  │ permissions   │
        │ load_skill   │  │ MCP toolsets    │  │ ask / deny    │
        │ (lazy)       │  │ (filesystem,    │  │ (gating       │
        │              │  │  k8s, github…)  │  │  plugin)      │
        └──────────────┘  └─────────────────┘  └───────────────┘
                  ▲              ▲                    ▲
                  └──────────────┴────────────────────┘
                           Specialisation surface
                       (mount these to retarget the
                        agent at a new domain)
```

## Layers

### 1. Provider layer — `core/llm`

A tiny dispatcher that selects a `model.LLM` implementation based on
`OMNIS_PROVIDER`. Adapters for Anthropic and OpenAI live in this
package and speak HTTP+SSE directly — no third-party SDK is pulled in.
See [providers.md](providers.md).

### 2. Agent kit — `core/agentkit`

`agentkit.New()` is the single constructor used everywhere. It:
- Selects the model via `core/llm`
- Prepends `agentkit.SystemPrompt` (the Claude-Code-style universal
  operating contract) to the per-agent instruction
- Returns an ADK `agent.Agent` ready to run

The `SystemPrompt` is the heart of the framework: it describes a
*method* (restate → plan → investigate → act → report → respect →
escalate) and explicitly states that capability = mounted (tools ∪
skills ∪ MCP). Per-agent prompts add only the agent's *role*, never a
domain.

### 3. Tools — `core/tools`, `internal/*`

Each tool is a Go function exposed via ADK's `tool.Tool` interface.
Categorisation:

| Package                | Tools                                                           | Purpose                       |
|------------------------|-----------------------------------------------------------------|-------------------------------|
| `core/tools`           | `read`, `write`, `grep`, `glob`, `revert`, `bash`               | File system & shell           |
| `internal/todo`        | `todo_write`, `todo_read`                                       | Lightweight scratch list      |
| `internal/tasks`       | `task_create`, `task_update`, `task_list`                       | Durable plan graph            |
| `internal/bg`          | `bash_background`, `bg_list`                                    | Long-running commands         |
| `internal/worktree`    | `worktree_create`, `worktree_remove`, `worktree_merge`          | Isolated git scratch space    |
| `internal/teammates`   | `teammate_ask`, `teammate_tell`, `teammate_inbox`               | Inter-agent mailbox           |
| `internal/skills`      | `load_skill`, `list_skills`                                     | Lazy skill discovery          |
| `internal/mcp`         | (loads MCP toolsets from JSON)                                  | External tool servers         |
| `internal/a2a`         | `a2a_<name>` (one per configured peer)                          | Remote A2A agent delegation   |

**Interactive shell-escape (`!`).** Separate from the agent's `bash` *tool*,
both UIs let a user run a shell command directly by prefixing a message with
`!` (e.g. `!ls -hal /tmp`). This path does **not** go through the model or the
permissions engine — the user authorised it by typing it — but the same hard
safety floor still applies. It is backed by `tools.RunBashInteractive` (which
reuses RunBash's safety floor, timeout, and filtering but carries a working
directory in and the resulting directory out, so an embedded `cd` persists
**per session**) and `internal/shellcomplete` (dependency-free bash-like Tab
completion: `$PATH` executables for the first token, filesystem paths
otherwise). The server exposes `POST /api/sessions/:id/bash` and
`GET /api/complete`; the TUI calls the same functions in-process. Output is
rendered live and is not added to the conversation history.

### 4. Plugins — `core/events`, `core/permissions`, `internal/cache`, `internal/compress`

ADK plugins observe and mutate the agent loop. The OOTB harness wires:

- **events** — file-logs every Before/AfterTool, Before/AfterModel,
  ToolError, SessionStart, SessionEnd to `.agent_events.log`. Every
  payload carries an `agent` field with the name of the ADK agent
  currently executing (lead or sub-agent), so subscribers can route
  events per agent — the TUI uses it to interleave sub-agent activity
  inside the Chat pane in real time and to indent rows in the Trace
  pane. `EventAfterModel` payloads also include `model`, `duration`,
  and a `usage` map (`prompt_tokens`, `candidates_tokens`,
  `total_tokens`) for per-call telemetry.
- **permissions** — gates bash and tool calls against
  `permissions.json` (allow / deny / ask).
- **hooks** — runs user-configured shell commands at lifecycle moments
  (Claude Code-style `hooks.json`). A per-squad runner plugin carries the
  blocking/injecting hooks (`PreToolUse` blocks a tool exactly like the
  permissions DENY path, `PostToolUse`, `UserPromptSubmit`, `Stop`); a
  process-wide engine on `Infrastructure` wires the fire-and-forget bus
  listeners (`SubagentStop`, `SessionStart`/`End`, `PreCompact`,
  `Notification`) once. See [Lifecycle hooks](../web/docs/22-hooks.md) and
  `internal/hooks` + `agent/hooks_plugin.go`.
- **cache** — surfaces prompt-cache stats per turn.
- **compress** — when a session's context approaches the model window,
  extracts durable facts to a per-session
  `.agent_memory_<user>_<session>.md` file and compresses turns. Token
  counters and transcript buffers are kept per `(userID, sessionID)` so
  concurrent sessions stay isolated.

### Session isolation

Every component that owns mutable state is **session-scoped** by
default. The root binary declares one `sessionSuffix(userID, sessionID)`
helper and feeds it to all of them, so a given session's task graph,
todo plan, compressed memory, background queue and mailbox namespace
all line up under the same suffix.

| Component              | What gets scoped                                       |
|------------------------|--------------------------------------------------------|
| `internal/compress`    | `.agent_memory_<u>_<s>.md` + token counters            |
| `internal/tasks`       | `.agent_tasks_<u>_<s>.json` + per-path mutex           |
| `internal/todo`        | `.agent_todo_<u>_<s>.json` + per-path mutex            |
| `internal/bg`          | one in-memory `*Queue` per `(user, session)`           |
| `internal/teammates`   | mailbox names prefixed with `<u>_<s>:`                 |

Non-session-scoped (by design): `core/events` (single audit log),
`internal/cache` (global hit-rate counters), `internal/worktree` (already
isolated by the `path`/`branch` argument), and the read-only loaders
`internal/skills` / `internal/mcp`. See
[configuration.md#session-isolation](configuration.md#session-isolation)
for the full table and wiring snippet.

### 5. Sub-agents

Several generic sub-agents are wired by default in the root `main.go`:

- **investigator** — read-only evidence gatherer. Cites every finding
  with a source (file:line, command output, MCP resource id). Never
  modifies state.
- **summariser** — condenses long content into a one-line headline + ≤7
  bullets + suggested next actions.
- **web_agent** — web search + page fetch when MCP browsing is enabled.
- **image_generator** — image generation via the imagegen model profile.

Sub-agents are exposed to the leader via `agenttool.New(...)` wrapped in
a `newNonConcurrentTool` — the leader calls them as tools and at most
one sub-agent runs at a time per leader. Add your own specialist by
following [extending.md](extending.md).

### 6. Squads

A **squad** is a named group `{ name, leader, members[] }` defined under
`squads:` in `agents.json`. Each squad wires its own leader and
its own subset of the agent catalogue as that leader's sub-agents.

```
RuntimeSettings ──► resolveSquadEntries ──► RuntimeSquadConfig[]
                                                    │
                                                    ▼
   ┌─────────────────────────────────────────────────────────┐
   │ Instance (one generation)                               │
   │                                                         │
   │  Squads["default"] ─► SquadInstance{leader, members,    │
   │                                     runner, plugins}    │
   │  Squads["research"] ─► SquadInstance{…}                 │
   │                                                         │
   └─────────────────────────────────────────────────────────┘
```

Each chat session runs on one squad at a time and the server resolves
`Manager.LookupSquad(sessionID, squadName).Runner` on every turn. By
default a new session starts on the **Omnis router** squad (§7), which
routes it to the best squad; a session can also be pinned to a specific
squad at creation (the New Chat picker / `POST /api/sessions {"squad": …}`),
and squads can switch mid-session via routing hand-offs.

Key properties:

- **Squads compose, they don't redefine.** Skills, tools and MCP
  servers stay on agent definitions; two squads that share a member
  also share its wiring, and the MCP pool dedups any subprocess.
- **`default` is always present.** When the user's `squads:` list is
  empty or omits a `default`, the resolver synthesises one from the
  enabled agents (excluding the curator).
- **Leaderless squads.** A squad whose `leader` is `"none"` (or empty)
  and that has **exactly one member** runs that single agent directly as
  the runner root — no coordinator, no sub-agent delegation tools, tools
  limited to exactly what the agent declares (plus the always-on mailbox
  and `ask_user`). This is the shape used by the `helper` squad and by the
  Omnis router (below). Squads with ≥2 members require a real leader.
- **Curator stays process-wide.** It listens on the shared event bus
  with the union of every squad's member names — not per-squad — so
  session-end curation runs at most once per session regardless of
  which squad it used.
- **Reflector pipeline runs alongside the curator.** A `reflector`
  agent (optional, configured the same way as the curator) is built
  per generation. At every session end an in-process heuristic tags the
  session's loaded soft-skills (`helpful` / `harmful` / `neutral`),
  then — when enabled — the LLM Reflector layers its own tags on top
  with reasons + a `key_insight`. The merged outcome and per-skill
  stats (`softskills/_stats.json`) feed the curator's prompt so the
  create / update / delete decision is grounded in concrete thresholds
  rather than the curator's intuition. See [skills.md](skills.md#post-session-reflection-pipeline).

Add a squad by editing `agents.json` (or the Squads sub-tab in
the web UI's Agent settings); see [extending.md](extending.md#adding-a-squad).

### 7. Omnis router (default chat routing)

**Omnis** is a special **leaderless squad** (single member: the `omnis`
agent) that is the **default squad for every new chat**. Where a squad
leader orchestrates its own members and keeps control, the router
*transfers control of the conversation*: it reads the user's request,
picks the squad best able to handle it, and hands over; that squad's
leader then answers the user directly. When the user later switches to a
topic outside the active squad's scope, that squad hands control **back**
to the router, which re-routes. When intent is unclear and no squad fits,
Omnis talks to the user instead of routing.

The whole mechanism is host-side and config-driven (`agent/routing.go`):

- **Two control tools.** `route_to_squad(squad, reason)` is mounted only
  on the router root (and validates `squad` against the non-router squad
  catalogue, which is also injected into the router's instruction);
  `handoff_to_router(reason)` is mounted on every *non-router* squad root
  when routing is enabled. Both only record a per-session **directive** in
  a process-wide `RouteRegistry` on Infrastructure — they never run another
  runner. They carry **no prompt**: the answering squad always receives the
  user's verbatim turn, so the router cannot paraphrase, twist, or drop the
  request (or its attachments).
- **Capability probe (`ask_squad`).** When the router is *unsure* which
  squad fits, it privately checks a candidate before committing:
  `ask_squad(squad, request)` makes **one isolated, non-streamed
  `model.LLM.GenerateContent` call** using that candidate lead's own model
  + instruction (no runner, tools, sub-agents, or event bus — the one-off
  LLM pattern from `internal/compress`), returning the lead's `CAN_HANDLE`
  / `CANNOT_HANDLE` verdict. Because it touches neither the SSE stream nor
  the shared bus it is hidden by construction; it never runs the squad's
  tools. Confident routes skip the probe; when every plausible squad
  declines, the router asks the user instead of force-routing.
- **Dispatch loop** = `Manager.RunWithRouting(...)`. It runs the starting
  squad, `Take`s the recorded directive, switches squad, and re-dispatches
  the **same** user turn — up to a 4-hop bound (`routerMaxHops`). It feeds
  **two part-views**: every answering (non-router) hop gets the user's
  **original parts** (verbatim text + any attached files); the router hop
  gets a **clean text-only view** (the router has no file tools, so it must
  not be shown attachment blobs or the "use the read/mime tools" note).
  Per-squad in-session memory is preserved because each `SquadInstance`
  owns a private session service and is stable across turns — A → B → A
  returns to A's existing history; the loop only re-resolves runners, never
  rebuilds them.
- **Silent routing.** The routing tools are exempt from the permission
  layer (they record an internal directive with no side effects, so they
  must never prompt). Each surface additionally **suppresses the router
  hop's assistant text** — the model tends to narrate ("Routed to the
  default squad…") despite the instruction — deciding via
  `Manager.PendingRoute`: a route is pending ⇒ discard the chatter from
  both chat and the persisted turn; no route ⇒ the text is a genuine reply
  (a clarifying question) and is shown. The only visible routing signal is
  the routing chip / "── routed to X squad ──" line.
- **Default for everyone, opt-out.** `router_squad` in `agents.json` (or
  `OMNIS_ROUTER_SQUAD`) names the router squad — **absent ⇒ defaults to
  `omnis`**, `"none"` disables (new chats then start on `default`). When a
  config doesn't declare them, `ensureRouterSquad` injects the `omnis`
  agent (registry definition if present, else a built-in instruction,
  inheriting the leader's model) and a leaderless `omnis` squad. With
  routing disabled the path is byte-identical to a single-squad session.

### 8. Specialisation surface

Three orthogonal mount points let you retarget the agent without
recompiling:

| Surface     | Where                                     | How it's mounted                                  |
|-------------|-------------------------------------------|---------------------------------------------------|
| Agents      | `registry/agents/<name>/{agent.json,instruction.md}` | Loaded by `ResolveRuntimeSettings` for each name listed in `agents.json` |
| Skills      | `skills/<name>/SKILL.md`                  | `internal/skills` walks the dir at startup        |
| MCP servers | `mcp_config.json`                  | `internal/mcp` spawns processes & adapts toolsets |
| Permissions | `permissions.json`                 | `core/permissions` plugin                         |
| Hooks       | `hooks.json`                       | `internal/hooks` engine + `agent/hooks_plugin.go` (per-squad plugin + bus listeners) |
| A2A peers   | `a2a_config.json`                  | `internal/a2a.NewTools` adds one `a2a_<name>` tool per entry to the leader |

## Lifecycle of a turn

1. User prompt → `runner.Run(ctx, sessionID, message)`. ADK's
   `OnUserMessage` runs any **`UserPromptSubmit`** hook, which can inject
   context into the turn or abort it.
2. ADK calls `BeforeModel` plugins (events, cache stats).
3. Model produces a response. Each tool call is dispatched in turn:
   - `BeforeTool` — permissions plugin may auto-allow / ask / deny, and
     a **`PreToolUse`** hook may block the call (short-circuiting the tool
     with a reason, the same mechanism as a permissions DENY).
     When the tool is a sub-agent wrapper, the events bus synthesises
     `EventSubAgentStart` (carrying `caller_agent`, the sub-agent name,
     and `run_id`).
   - Tool function runs. Sub-agent runs use a private ADK runner; their
     internal tool/model events still flow through the shared bus
     because `AgentCallbacks` are attached on every sub-agent.
   - `AfterTool` — events plugin records I/O; a **`PostToolUse`** hook may
     feed a reason back to the model. Sub-agent wrappers fire
     `EventSubAgentEnd` with the same `run_id` (or `error` on tool
     errors).
4. Model is re-invoked with tool results until it stops emitting calls.
5. `AfterModel` — compress plugin checks context size; if over
   threshold, writes a memory snapshot and rewrites session.
6. Session events are appended to the `session.Service`.
7. `AfterRun` runs any **`Stop`** hook; `EventRunEnd` fires. The
   sub-agent reflection hook walks every `EventSubAgentStart/End` pair
   seen during this `run_id`, classifies the leader's reaction from its
   assistant text, and applies `helpful` / `harmful` / `neutral` tags to
   `softskills/_stats.json` for every soft-skill the sub-agents loaded.

When a real session ends (TUI quit / web UI close / idle timeout):

8. `EventSessionEnd` fires (CLI/TUI; the server fires the `SessionEnd`
   hook directly on session delete). The session reflection pipeline
   drains its buckets, computes the heuristic outcome, applies the
   heuristic tags to `_stats.json`, and emits `EventSessionReflected`.
9. The curator hook receives `EventSessionReflected`. When a reflector
   agent is configured, it runs the LLM reflector (60-second timeout)
   to refine the tags + extract a `key_insight`, applies the deltas
   via `Stats.Retag`, then runs the curator gate and curator agent.

## Where to look next

- New tool? Read [extending.md](extending.md).
- New skill? Read [skills.md](skills.md).
- New MCP server? Read [configuration.md](configuration.md).
- Behaviour tweak? Edit `core/agentkit/agentkit.go` `SystemPrompt` —
  but stay domain-neutral.
