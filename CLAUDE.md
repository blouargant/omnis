# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Build
make build              # root binary + all examples (host platform)
make build-root         # bin/agent-toolkit only
make build-server       # bin/server (HTTP API)
make release            # cross-platform binaries → dist/

# Test
make test               # all unit tests
make env-tests          # LLM integration tests (requires .env with API keys)
go test ./core/tools -run TestRunBashSafetyFloorAndOutput   # single test

# Code quality
make fmt                # go fmt ./...
make vet                # go vet ./...
make tidy               # go mod tidy

# Run
go run . console                        # interactive REPL
go run . web webui                      # local ADK web UI
go run . --tui                          # tview chat UI
go run . -d console                     # debug: log full payloads
make run-server                         # HTTP API (requires GOAGENT_SERVER_TOKEN)
go run . curate <audit.md> <state.json> # manual soft-skill curation

# Examples
make build-example-s03_todo    # build a single example
go run ./examples/s05_skills   # run an example directly
```

## Architecture

**Design contract**: the same binary becomes a code reviewer, Kubernetes triage assistant, or DBA helper purely by mounting different tools, skills, and MCP servers. No code changes required to retarget the agent.

Built on [google.golang.org/adk](https://pkg.go.dev/google.golang.org/adk) for the agent loop, session, plugins, and runner.

### Agent topology

```
main.go / server/
    └── agent.NewAgent()            ← single wiring entry point
            ├── leader              ← coordinator (fs tools + planning + mailbox)
            │     ├── investigator  ← read-only evidence gatherer (tool-wrapped, not transfer_to_agent)
            │     └── summariser    ← condenses bulk output
            └── curator             ← post-session soft-skill distiller (background, EventSessionEnd)
```

Sub-agents are wrapped via `agenttool.New()` and exposed as **tools** on the leader (not via `transfer_to_agent`), so control always returns to the leader after a sub-agent call. Only one sub-agent runs at a time (enforced by `newNonConcurrentTool`).

### Key packages

| Path | Role |
|---|---|
| `agent/` | `NewAgent()` — wires all components; `ResolveRuntimeSettings()` — config precedence |
| `core/agentkit/` | `New()` — thin ADK agent constructor |
| `core/llm/` | Multi-provider dispatcher: `anthropic`, `openai`, `gemini`, `openai_compat` |
| `core/tools/` | File-system tools: `read`, `write`, `grep`, `glob`, `revert`, `bash` (with safety floor) |
| `core/permissions/` | YAML-based permission gating: always_deny → always_allow → ask_user |
| `core/events/` | Event bus + file logger; before/after model/tool callbacks + session lifecycle |
| `internal/tasks/` | Durable task graph; persisted to `logs/agent_tasks_<u>_<ts>.json` |
| `internal/todo/` | Lightweight scratch list; persisted to `logs/agent_todo_<u>_<ts>.json` |
| `internal/bg/` | Background command queue; `bash_background` + `bg_list` tools |
| `internal/worktree/` | Git worktree isolation tools |
| `internal/teammates/` | Inter-agent mailbox FSM: `teammate_ask/tell/inbox` |
| `internal/skills/` | Skill loader: `load_skill`, `list_skills` (reads `skills/<name>/SKILL.md`) |
| `internal/softskills/` | Curator output: `load_softskill`, `list_softskills` (reads `softskills/`) |
| `internal/compress/` | Per-session context compression plugin + audit/statelog files |
| `internal/cache/` | Prompt cache hit-rate stats plugin |
| `internal/mcp/` | MCP config loader from `config/mcp_config.yaml` |
| `internal/tui/` | tview chat UI (trace pane + streaming chat) |
| `server/` | HTTP API server with Bearer token auth |

### Configuration files

| File | Purpose |
|---|---|
| `config/agent.yaml` | Agent roles, model profiles, paths — main runtime config |
| `config/mcp_config.yaml` | MCP server definitions (name, command, args, env) |
| `config/permissions.yaml` | Tool permission rules (always_deny / always_allow / ask_user) |
| `config/filters/` | Bash output filter patterns (token optimization) |
| `skills/<name>/SKILL.md` | Authored skill playbooks (YAML front matter: name, description) |
| `softskills/` | Curator-distilled procedures from past sessions |

### Configuration precedence

`defaults → config/agent.yaml → ENV → Options (struct/flags)`

`api_key` and `base_url` values in YAML are resolved as environment variable names first (if an env var with that name exists and is non-empty, its value is used).

### Environment variables

| Variable | Purpose |
|---|---|
| `GOAGENT_PROVIDER` | `anthropic` / `openai` / `gemini` / `openai_compat` (default) |
| `GOAGENT_MODEL` | Provider-specific model ID |
| `GOAGENT_BASE_URL` | API endpoint (OpenAI/compat/Anthropic) |
| `GOAGENT_API_KEY` | Provider API key (also: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`) |
| `GOAGENT_CURATOR_ENABLED` | `true`/`false` — enable/disable post-session curator |
| `GOAGENT_SERVER_TOKEN` | Bearer token required to start the HTTP server |
| `GOAGENT_SERVER_ADDR` | HTTP server listen address (default `:8080`) |
| `GOAGENT_SERVER_GC_INTERVAL` | Period between sweeps that remove orphan files in `logs/` and `logs/uploads/` (default `1h`; `0` disables) |
| `GOAGENT_DEBUG` | Log full conversation/event payloads + per-stream SSE timing line |

### Session isolation

Every mutable component scopes its state by `(userID, buildTimestamp)`. Concurrent sessions never share task graphs, todo lists, memory, or mailbox namespaces. All session files land in `logs/`:

- `agent_tasks_<u>_<ts>.json` — task graph
- `agent_todo_<u>_<ts>.json` — todo plan  
- `agent_memory_<u>_<ts>.md` — compressed session memory
- `agent_statelog_<u>_<ts>.json` — full state log (consumed by curator)
- `agent_events_<ts>.log` — event audit log (global per build)

### Adding a new sub-agent

1. Add an `AgentEntry` in `config/agent.yaml` with a unique `name`, `tools` list, and optional `model_ref`.
2. `agent.NewAgent()` auto-discovers it via `runtime.Agents`; no Go code change needed unless you want a custom default instruction (`defaultAgentInstruction`) or tool wiring (`defaultToolKeys`).

### Adding a skill

Create `skills/<name>/SKILL.md` with YAML front matter:
```yaml
---
name: my-skill
description: One-line description shown in list_skills output
---
# Skill content as markdown instructions
```

The leader auto-discovers skills at startup; no config change required.

### Web UI debug mode

The web UI ships with a built-in debug overlay for inspecting streaming
performance and other client-side metrics. Enable it by either:

- Appending `?debug=1` to the URL, or
- Setting `localStorage.agent_toolkit_debug = "1"` (persists across reloads).

A small monospace badge appears in the top-right corner showing live per-turn
metrics:

```
[client] ttfb=120ms  chunks=84  42.3/s  bytes=1980
         render=18ms across 1 parse(s)
[server] ttfb=95ms  chunks=84  44.1/s  total=2010ms
```

- **client** metrics are measured in the browser (TTFB from `fetch()` start,
  cumulative `marked.parse` cost, chunks-per-second based on token-event
  arrival).
- **server** metrics are emitted by the backend as a `debug_timing` SSE event
  right before `done` (see [server/sse.go](server/sse.go) `emitDone`). They
  reflect the rate at which the agent is producing tokens on the wire,
  independent of any browser-side cost.

The instrumentation API is exposed on `window.AgentDebug` for ad-hoc probing
from the browser console. Extend it by adding new fields to the object in
[web/app.js](web/app.js) and calling `_paint()` after mutating state — keeping
the badge as the single surface for new client-side measurements.

Streaming itself always uses incremental Text-node appends; `marked.parse` runs
once per segment at finalize. Don't reintroduce per-chunk markdown rendering —
it makes the UI feel slow even when the wire is fast.
