# Architecture

This document maps the codebase and explains how the pieces interact.

## High-level picture

```
                      ┌────────────────────────────────────┐
                      │        yoke (root)        │
                      │  (launcher: REPL or web UI)        │
                      └─────────────────┬──────────────────┘
                                        │ wires
                                        ▼
        ┌──────────────────────────────────────────────────────────┐
        │                       Lead agent                         │
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
        │  skills/*    │  │ config/mcp_*    │  │ permissions   │
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
`YOKE_PROVIDER`. Adapters for Anthropic and OpenAI live in this
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
| `internal/mcp`         | (loads MCP toolsets from YAML)                                  | External tool servers         |

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
  `config/permissions.yaml` (allow / deny / ask).
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

Two generic sub-agents are wired by default in the root `main.go`:

- **investigator** — read-only evidence gatherer. Cites every finding
  with a source (file:line, command output, MCP resource id). Never
  modifies state.
- **summariser** — condenses long content into a one-line headline + ≤7
  bullets + suggested next actions.

Both are exposed to the lead via `agenttool.New(...)` — the lead calls
them as tools. Add your own specialist by following [extending.md](extending.md).

### 6. Specialisation surface

Three orthogonal mount points let you retarget the agent without
recompiling:

| Surface     | Where                          | How it's mounted                                  |
|-------------|--------------------------------|---------------------------------------------------|
| Skills      | `skills/<name>/SKILL.md`       | `internal/skills` walks the dir at startup        |
| MCP servers | `config/mcp_config.yaml`       | `internal/mcp` spawns processes & adapts toolsets |
| Permissions | `config/permissions.yaml`      | `core/permissions` plugin                         |

## Lifecycle of a turn

1. User prompt → `runner.Run(ctx, sessionID, message)`.
2. ADK calls `BeforeModel` plugins (events, cache stats).
3. Model produces a response. Each tool call is dispatched in turn:
   - `BeforeTool` — permissions plugin may auto-allow / ask / deny.
   - Tool function runs.
   - `AfterTool` — events plugin records I/O.
4. Model is re-invoked with tool results until it stops emitting calls.
5. `AfterModel` — compress plugin checks context size; if over
   threshold, writes a memory snapshot and rewrites session.
6. Session events are appended to the `session.Service`.

## Where to look next

- New tool? Read [extending.md](extending.md).
- New skill? Read [skills.md](skills.md).
- New MCP server? Read [configuration.md](configuration.md).
- Behaviour tweak? Edit `core/agentkit/agentkit.go` `SystemPrompt` —
  but stay domain-neutral.
