# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Self-Maintenance Rule

After every major change (new agent, new squad, new tool, new skill, new config file, new package under `core/` or `internal/`, new env var, new HTTP route, new SSE event, new MCP wiring, search-chain/precedence changes, hot-reload behavior changes, architectural shifts), update this CLAUDE.md file to reflect the current state. Specifically:

- Add new agents/squads/tools/skills/packages to the relevant tables and sections below (Agent topology, Key packages, Configuration files, Environment variables, Filesystem layout).
- Update the "Adding a new sub-agent", "Adding a new squad", "Adding a skill", and A2A sections when their procedures change.
- Add any new gotchas, precedence rules, or patterns where they belong (e.g. write-layer routing, MCP dedup, session pinning across hot-reload).
- Keep the configuration precedence chain and search chain accurate when either changes.
- Keep this file as the single source of truth for AI sessions working on this project.

## Commands

```bash
# Build (production binaries only)
make build              # bin/yoke + bin/yoke-server (host platform)
make build-root         # bin/yoke only
make build-server       # bin/yoke-server (HTTP API)
make examples          # opt-in: build all examples under bin/
make release            # cross-platform raw binaries → dist/
make package            # cross-platform + .deb + .rpm + .zip → dist/ (requires goreleaser)
make package-check      # validate .goreleaser.yaml without building

# Test
make test               # all unit tests
make env-tests          # LLM integration tests (requires .env with API keys)
go test ./core/tools -run TestRunBashSafetyFloorAndOutput   # single test
go test ./internal/a2a/...                                  # A2A unit tests
make a2a-smoke A2A_URL=http://127.0.0.1:8091/              # live A2A smoke test

# Code quality
make fmt                # go fmt ./...
make vet                # go vet ./...
make tidy               # go mod tidy

# Run — three usage modes
go run .                                # CLI: REPL when TTY, one-shot when piped
go run . "explain main.go"              # CLI one-shot with prompt argument
echo "summarize repo" | go run .        # CLI one-shot reading stdin
go run . tui                            # TUI: tview chat interface
make run-server                         # Server: HTTP API + web UI (needs YOKE_SERVER_TOKEN)

# Auxiliary subcommands
go run . -d                             # debug: log full payloads (any mode)
go run . curate --user u --session s    # manual soft-skill curation
go run . version                        # version info

# Examples (opt-in; not part of `make build`)
make build-example-s11_todo    # build a single example
go run ./examples/s21_skills   # run an example directly
```

## Architecture

**Design contract**: the same binary becomes a code reviewer, Kubernetes triage assistant, or DBA helper purely by mounting different tools, skills, and MCP servers. No code changes required to retarget the agent.

Built on [google.golang.org/adk](https://pkg.go.dev/google.golang.org/adk) for the agent loop, session, plugins, and runner.

### Agent topology

```
main.go / server/
    └── agent.NewAgent()            ← single wiring entry point
            ├── Squads              ← one wired tree per squad in agent.json
            │    ├── "default"      ← leader + full team (used when a session omits a squad)
            │    │    ├── leader              ← coordinator (fs tools + planning + mailbox)
            │    │    │    └── a2a_<name>…   ← one tool per peer in a2a_config.json
            │    │    ├── investigator        ← read-only evidence gatherer (tool-wrapped, not transfer_to_agent)
            │    │    ├── web_agent           ← web search + fetch
            │    │    └── summariser          ← condenses bulk output
            │    └── "research"     ← leader + smaller team, selectable per session
            │         ├── leader
            │         ├── web_agent
            │         └── summariser
            ├── reflector           ← post-session LLM analyst that tags loaded soft-skills (one hook per generation; optional — heuristic fallback when disabled)
            └── curator             ← process-wide post-session soft-skill distiller (one hook per generation)
```

A **squad** is a named group `{ leader, members[] }` composed from the
agents defined in `agents.json`. Each chat session selects one
squad at creation (default when none is chosen); the server resolves
`Instance.Squad(name).Runner` per session, so two sessions running on the
same generation can use different squads. Squads only *reference* agents
— skills, tools and MCP servers stay on the agent definitions, so two
squads that share a member also share that member's wiring (and the MCP
pool dedups any subprocess backing it).

Sub-agents are wrapped via `agenttool.New()` and exposed as **tools** on
the leader (not via `transfer_to_agent`), so control always returns to
the leader after a sub-agent call. Only one sub-agent runs at a time
(enforced by `newNonConcurrentTool`). The curator stays a single
per-generation hook listening across every squad.

**Soft-skill reflection pipeline** — at `EventSessionEnd`, [agent/load_recorder.go](agent/load_recorder.go)
drains its in-memory bucket (leader-loaded skills, tool errors), runs
the deterministic `softskills.ReflectHeuristic`, applies the heuristic
tags to `softskills/_stats.json`, and emits `EventSessionReflected`
with the gathered payload. [agent/curator_hook.go](agent/curator_hook.go)
subscribes to that event: when a `reflector` agent is enabled it runs
the LLM Reflector ([internal/softskills/reflector.go](internal/softskills/reflector.go))
with a 60-second timeout, merges its Outcome over the heuristic (LLM
wins on overlap), `Retag`s the stats to reflect the override, then
gates and runs the curator. `EventCurateNow` (manual `/learn-now`)
bypasses the reflector and drives the curator directly.

**Sub-agent boundary events** — sub-agents run inside agenttool's private
runner, so neither `EventRunStart/End` nor `EventBeforeRun/AfterRun` fire
on the shared bus for their internal turns. To give reflection hooks a
clean "one sub-agent invocation finished" signal, [agent/subagent_event.go](agent/subagent_event.go)
subscribes to the leader's `EventBeforeTool / EventAfterTool / EventToolError`
and re-emits any payload whose `tool` name matches a sub-agent as
`EventSubAgentStart / EventSubAgentEnd`. Payload keys: `agent` (the
sub-agent), `caller_agent` (the leader), `user_id`, `session_id`, `input`,
`output` (end only), `duration` (end only), `error` (end only, on tool
error), `call_id`, `run_id`. Registered once per Instance from sub-agent
names spanning every squad; subscriptions detach on hot-reload.

**`run_id` on every event** — `EventRunStart / EventRunEnd / EventBeforeTool /
EventAfterTool / EventToolError / EventBeforeModel / EventAfterModel`
all carry a `run_id` field set to ADK's `InvocationContext.InvocationID()`.
It is stable across BeforeRun + AfterRun for a single `Runner.Run` call
and lets [agent/subagent_hook.go](agent/subagent_hook.go) buffer all
sub-agent invocations observed during one leader turn for the Phase 6
per-invocation tagger. Sub-agent internal runs get their own (different)
`run_id`s, so the leader-side `EventSubAgentStart/End` events keep the
leader's `run_id` (which is what we group on).

**Sub-agent reflection pipeline (Phase 6)** —
[agent/subagent_hook.go](agent/subagent_hook.go) opens a per-`run_id`
buffer at each `EventSubAgentStart`, attributes `load_softskill` events
and `tool_error`s to the open invocation, captures the leader's
`AfterModel` text for the lexical reaction scan, and at `EventRunEnd`
walks the buffer to call `softskills.TagInvocation` per invocation
(retry detection via "same sub-agent appears later in the same run",
`Error:` / empty output detection, leader reaction via
`ClassifyLeaderReaction`'s approval / retry / unknown classifier).
Resulting tags are applied to `_stats.json` via `Stats.RecordTag`.

### Key packages

| Path | Role |
|---|---|
| `agent/` | `NewAgent()` — wires all components; `ResolveRuntimeSettings()` — config precedence |
| `core/agentkit/` | `New()` — thin ADK agent constructor |
| `core/llm/` | Multi-provider dispatcher: `anthropic`, `openai`, `gemini`, `openai_compat` |
| `core/tools/` | File-system tools: `Read`, `Write`, `Grep`, `Glob`, `revert`, `Bash` (with safety floor) |
| `core/permissions/` | JSON-based permission gating: always_deny → always_allow → ask_user |
| `core/events/` | Event bus + file logger; before/after model/tool callbacks + session lifecycle |
| `internal/tasks/` | Durable task graph; persisted to `logs/agent_tasks_<u>_<ts>.json` |
| `internal/todo/` | Lightweight scratch list; persisted to `logs/agent_todo_<u>_<ts>.json` |
| `internal/bg/` | Background command queue; `bash_background` + `bg_list` tools |
| `internal/worktree/` | Git worktree isolation tools |
| `internal/teammates/` | Inter-agent mailbox FSM: `teammate_ask/tell/inbox` |
| `internal/skills/` | Skill loader: `load_skill`, `list_skills` (reads `registry/skills/<name>/SKILL.md`) |
| `internal/softskills/` | Curator output: `load_softskill`, `list_softskills` (reads `softskills/`); `Stats` sidecar + `ReflectHeuristic` (deterministic per-skill helpful/harmful/neutral tagging) |
| `internal/compress/` | Per-session context compression plugin + audit/statelog files |
| `internal/cache/` | Prompt cache hit-rate stats plugin |
| `internal/mcp/` | MCP config loader (path resolved from search chain) |
| `internal/a2a/` | A2A protocol client (`client.go`) + ADK tool wiring (`tools.go`); config types in `a2a.go` |
| `internal/tui/` | tview chat UI (trace pane + streaming chat) |
| `server/` | HTTP API server with Bearer token auth |
| `server/a2a_server.go` | Receives inbound A2A `tasks/send` / `tasks/sendSubscribe` calls; routes by squad + session |

### Configuration files

Config files are resolved through a **3-layer search chain** (high → low precedence):
`.agents/` (or `agents/` as a dotless alias; both participate when both exist, `.agents/` first) → `$HOME/.yoke/` (per-user) → `/etc/yoke/` (system). Agent and skill registries live under `registry/agents/` and `registry/skills/` inside whichever layer you're targeting.

| File | Purpose |
|---|---|
| `agents.json` | List of enabled agent names, squad composition, global paths |
| `models.json` | Providers (credentials + endpoint) and reusable model profiles referenced by agents via `model_ref` |
| `registry/agents/<name>/agent.json` | Per-agent definition (model_ref, tools, skills, builtin flag, etc.) |
| `registry/agents/<name>/instruction.md` | Per-agent system instruction (markdown) |
| `registry/agents/default.md` | Fallback system instruction for agents without their own |
| `registry/skills/<name>/SKILL.md` | Authored skill playbooks (YAML front matter: name, description) |
| `mcp_config.json` | MCP server definitions (name, command, args, env) |
| `a2a_config.json` | Remote A2A agent endpoints; each entry becomes an `a2a_<name>` tool on the leader |
| `permissions.json` | Tool permission rules (always_deny / always_allow / ask_user) |
| `filters/` | Bash output filter patterns (token optimization, JSON files) |
| `softskills/` | Curator-distilled procedures from past sessions |

Agent definitions live in `registry/agents/<name>/` directories — mirroring
the skills layout. `agents.json` no longer contains inline agent
objects; its `agents` field is a list of names that reference the registry:

```json
{
  "agents": ["leader", "investigator", "web_agent", "skill_editor", "registries_crawler", "summariser", "curator"],
  "squads": [ ... ]
}
```

The `models` block lives in its own `models.json` file alongside `agents.json`.
A startup-time check rejects configs that still declare `models` inline in
`agents.json` — move the block to `models.json` (the loader points at the
expected path in the error message). The file holds two top-level sections:

```json
{
  "providers": {
    "openai-prod": {
      "kind": "openai_compat",
      "base_url": "OPENAI_BASE_URL",
      "api_key":  "OPENAI_API_KEY"
    }
  },
  "models": {
    "premium": {
      "provider_ref": "openai-prod",
      "model": "claude-sonnet-4-6",
      "context_length": 200000,
      "input_token_price_per_million": 5,
      "output_token_price_per_million": 26
    }
  }
}
```

A model's `provider_ref` inherits `kind` (as `provider`), `base_url`, and
`api_key` from the referenced provider; inline `provider`/`base_url`/`api_key`
on a model still override the inherited values when set.

Each `registry/agents/<name>/agent.json` is the full `AgentEntry`. A
`"builtin": true` flag marks agents shipped with yoke (leader,
skill_editor, registries_crawler, summariser, curator); custom agents added
by the user omit the flag. The web UI groups them under separate
**Built-in** and **Custom** sections in the agents list.

The registry directory uses the same 3-layer lookup as config files:
`.agents/registry/agents` (and `agents/registry/agents` when that alias dir
exists), `$HOME/.yoke/registry/agents`, then `/etc/yoke/registry/agents` —
first existing directory wins. (The registry subdirs sit one level below
their layer's config files: e.g. system has `/etc/yoke/agents.json` next
to `/etc/yoke/registry/agents/`.)

### Filesystem layout

Two roots, resolved by [internal/paths/paths.go](internal/paths/paths.go):

- **Read root for config**: a 3-layer search chain, high → low precedence.
  Whichever layer has a given file wins for that whole file (file-level
  override, not deep merge):

  1. `.agents/` (canonical) and/or `agents/` (dotless alias) — project-local
     directories (CWD-relative, highest priority). Both are accepted; when
     both exist, `.agents/` wins and `agents/` is searched right after.
  2. `$HOME/.yoke/` — per-user state root
  3. `/etc/yoke/` — system-wide install (lowest priority). Agent/skill
     registries live at `/etc/yoke/registry/agents` and
     `/etc/yoke/registry/skills`; every other config file is directly
     under `/etc/yoke/`.

  Override the chain via `YOKE_CONFIG_DIRS` (colon-separated; replaces
  the chain wholesale).

- **Write root for state**: `$HOME/.yoke/` by default (override via `YOKE_HOME`).
  Agent runtime state (logs, mailboxes, softskills, registry installs) always
  lands here. For user-edited config (the web UI editor + the auto-install
  helpers), yoke is **layer-aware**: when the edited file or any of its
  references already lives in the project-local `.agents/` (or `agents/`)
  layer, the save is routed back to that layer so a local-only project
  never grows orphaned references under `$HOME/.yoke/`. Files originally
  in `/etc/yoke` still fork into `$HOME/.yoke/` on first edit (the system
  layer is read-only). Other state files (logs, mailboxes, softskills)
  remain anchored under `$HOME/.yoke/` regardless of layer:

  ```
  $HOME/.yoke/
  ├── agents.json       # editor writes — user config overrides
  ├── permissions.json  # editor writes — user permission overrides
  ├── logs/             # agent_tasks_*, agent_todo_*, agent_memory_*,
  │   │                 #   agent_statelog_*, agent_events_*, conversation_*
  │   └── uploads/      # web UI file uploads (per-session)
  ├── mailboxes/        # JSONL inter-agent mailboxes
  ├── softskills/       # curator-distilled procedures (read AND write)
  │   ├── _stats.json   # per-skill load/helpful/harmful/neutral counters
  │   │                 #   sidecar; keyed by <agent>/<name> or bare <name>
  │   │                 #   for leader. Maintained by agent/load_recorder.go.
  │   └── wrap-session/ # built-in soft-skill (deletable) that asks one
  │                     #   wrap-up question on interactive surfaces and
  │                     #   persists the answer via record_session_feedback.
  ├── logs/
  │   └── agent_feedback_<key>.json  # Phase 5 wrap-session sidecar; one
  │                                  #   record per session: {question,
  │                                  #   answer, timestamp}. Consumed by
  │                                  #   the heuristic + LLM reflectors.
  └── registry/
      ├── skills/       # web UI installed skills (override via YOKE_SKILLS_REGISTRY_DIR)
      └── agents/       # web UI installed agents (override via YOKE_AGENTS_REGISTRY_DIR)
  ```

  The web UI editor reads from the search chain and writes to the same
  layer the source file lives in — local files stay local, user files
  stay user, and system files fork to user. For `agents.json` specifically,
  saves are promoted to the **local** layer when the file references any
  agent or skill that only resolves in `.agents/` (or `agents/`), so
  every reference remains satisfied after the write.

  The skill registry (`registry/skills/`) follows the same lookup as
  agent definitions: `.agents/registry/skills` (and `agents/registry/skills`
  when present), `$HOME/.yoke/registry/skills`, `/etc/yoke/registry/skills`
  — first existing directory wins. (The `registry/` sub-tree is the only
  thing that lives one level deeper than its layer's config files.)

### Configuration precedence

`defaults → agents.json → ENV → Options (struct/flags)`

`api_key` and `base_url` values in the config file are resolved as environment variable names first (if an env var with that name exists and is non-empty, its value is used).

### Environment variables

| Variable | Purpose |
|---|---|
| `YOKE_PROVIDER` | `anthropic` / `openai` / `gemini` / `openai_compat` (default) |
| `YOKE_MODEL` | Provider-specific model ID |
| `YOKE_BASE_URL` | API endpoint (OpenAI/compat/Anthropic) |
| `YOKE_API_KEY` | Provider API key (also: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GOOGLE_API_KEY`) |
| `YOKE_CURATOR_ENABLED` | `true`/`false` — enable/disable post-session curator |
| `YOKE_CURATOR_IDLE_TIMEOUT` | Duration (e.g. `30m`) after which the idle harvester triggers automatic curation for a Web UI session; session is then marked **Harvested** and skipped until new activity; `0` disables (default: disabled) |
| `YOKE_CURATOR_MIN_TURNS` | Minimum model-response count before non-forced curation is considered (default: `3`) |
| `YOKE_CURATOR_MIN_SUB_AGENT_CALLS` | Minimum sub-agent invocations required when no decision is recorded (default: `2`) |
| `YOKE_SERVER_TOKEN` | Bearer token required to start the HTTP server |
| `YOKE_SERVER_ADDR` | HTTP server listen address (default `:8080`) |
| `YOKE_SERVER_GC_INTERVAL` | Period between sweeps that remove orphan files in `$YOKE_HOME/logs` and `$YOKE_HOME/logs/uploads` (default `1h`; `0` disables) |
| `YOKE_HOME` | Per-user state root for all mutable files (default `$HOME/.yoke`) |
| `YOKE_CONFIG_DIRS` | Colon-separated config search chain, high→low precedence. Replaces the default `.agents:$YOKE_HOME:/etc/yoke` |
| `YOKE_CONFIG_PATH` | Explicit `agents.json` path; bypasses the chain |
| `YOKE_SKILLS_REGISTRY_DIR` | Where the web UI installs imported skills (default `$YOKE_HOME/registry/skills`) |
| `YOKE_AGENTS_REGISTRY_DIR` | Where the web UI installs imported agents (default `$YOKE_HOME/registry/agents`) |
| `YOKE_DEBUG` | Log full conversation/event payloads + per-stream SSE timing line |

### Session isolation

Every mutable component scopes its state by `(userID, buildTimestamp)`. Concurrent sessions never share task graphs, todo lists, memory, or mailbox namespaces. All session files land in `$YOKE_HOME/logs/`:

- `agent_tasks_<u>_<ts>.json` — task graph
- `agent_todo_<u>_<ts>.json` — todo plan
- `agent_memory_<u>_<ts>.md` — compressed session memory
- `agent_statelog_<u>_<ts>.json` — full state log (consumed by curator)
- `agent_events_<ts>.log` — event audit log (global per build)
- `conversation_<id>.json` — Web UI turn history + title + `squad` name + `Harvested` flag (server only)

### Hot reload (server mode)

The HTTP server supports rebuilding the agent generation without
restarting the process. Edits to `agents.json`, `models.json`,
`permissions.json`, and `mcp_config.json` (from any layer of the search
chain) are picked up by `POST /api/config/reload` (or the "Reload" button
in the web UI).

The model is a two-layer build split across [agent/infrastructure.go](agent/infrastructure.go),
[agent/instance.go](agent/instance.go), and [agent/manager.go](agent/manager.go):

- **Infrastructure** is process-wide and survives every reload: mailbox
  backend, session registry, event bus, ask_user registry, MCP subprocess
  pool, and the session-scoped state holders (tasks, todo, bg queues).
- **Instance** is one agent generation: a map of **SquadInstance** entries
  (leader + sub-agents + plugins + runner per squad) derived from a
  snapshot of RuntimeSettings. Each reload bumps the generation number
  and builds a fresh Instance — with every squad rewired — on top of the
  unchanged Infrastructure. The default squad's leader/runner/plugins
  are mirrored at the top of Instance so legacy callers (CLI, TUI,
  examples) keep working unchanged.
- **Manager** owns the live generations. New sessions pin to the current
  generation and record their squad on the session; the server resolves
  `Manager.LookupSquad(sessionID, squadName).Runner` per turn. In-flight
  sessions stay pinned to their existing generation across reloads, so a
  streaming turn never observes a swap. An old generation is torn down
  once its pinned-session refcount drops to zero.

MCP subprocesses are deduplicated by `(command, args, env)` hash via
[internal/mcp/pool.go](internal/mcp/pool.go): two generations that mount
the same server share one child process. A reload that only changes one
server restarts just that server.

`GET /api/config/status` exposes the current generation and per-generation
refcounts so the web UI can render a "n sessions draining on previous
version" pill. The "Restart server" button stays available as the escape
hatch for changes that hot-reload cannot apply (env vars, binary updates).

### Adding a new sub-agent

1. Create `.agents/registry/agents/<name>/agent.json` with the `AgentEntry` fields
   (`name`, `description`, `tools`, optional `model_ref`, etc.). Omit the
   `builtin` flag for user-added agents. (Use `$HOME/.yoke/registry/agents/<name>/`
   for user-global agents that don't belong to a specific project.)
2. Optionally create `registry/agents/<name>/instruction.md` to provide a
   custom system instruction. If omitted, the agent falls back to
   `registry/agents/default.md`.
3. Add the agent's name to the `agents` list in `agents.json` (from the active search-chain layer).
4. Add the new agent's name to the `members` list of every squad that
   should expose it (omit the entry to keep an agent reserved for one
   squad). If a squad omits the agent, the squad's leader won't see it
   as a delegable tool.
5. `agent.NewAgent()` auto-discovers the agent via `runtime.Agents`; no
   Go code change needed unless you want custom tool wiring
   (`defaultToolKeys`).

### Adding a new squad

Squads compose existing agents. Add a `SquadEntry` to the top-level
`squads:` array in `agents.json`:

```json
{
  "squads": [
    {
      "name": "default",
      "leader": "leader",
      "members": ["investigator", "web_agent", "summariser"]
    },
    {
      "name": "research",
      "description": "Web research focus.",
      "leader": "leader",
      "members": ["web_agent", "summariser"]
    }
  ]
}
```

Rules enforced at resolution time:

- A squad named `default` is always present; the resolver synthesises
  one (from enabled agents) when missing or when the user adds only a
  non-default squad in the editor.
- `leader` and every `members[i]` must reference an enabled agent;
  `curator` cannot be a member (it is process-wide).
- Duplicate squad names are rejected.

The web UI exposes a Squads sub-tab under Settings → Agent with leader
dropdown, member checkboxes, and add/delete. Hot-reload picks up squad
edits without a process restart.

### Adding a skill

1. Create `registry/skills/<name>/SKILL.md` with YAML front matter:
   ```yaml
   ---
   name: my-skill
   description: One-line description shown in list_skills output
   ---
   # Skill content as markdown instructions
   ```
   The directory name must equal the frontmatter `name` field.

2. Add the skill name to the `"skills"` list in each agent's
   `registry/agents/<name>/agent.json` that should have access to it:
   ```json
   { "skills": ["my-skill", "other-skill"] }
   ```
   An empty list means no skills; the field is absent for agents that
   don't expose the `"Skill"` tool at all.

Hot-reload picks up changes to `agent.json` without a process restart.
The skill files themselves are read on demand at `load_skill` call time.

### Connecting remote A2A agents (client side)

A2A peers are wired via `a2a_config.json` (resolved from the config search chain) — no Go code required.

1. Add an entry for each remote endpoint:
   ```json
   {
     "agents": {
       "peer-yoke": {
         "url": "http://peer-host:8091/",
         "description": "Secondary yoke server.",
         "headers": { "Authorization": "Bearer ${input:peer_token}" },
         "squad": "",
         "session_name": "",
         "create": false
       }
     },
     "inputs": [
       { "id": "peer_token", "type": "promptString", "description": "Peer token", "password": true }
     ]
   }
   ```

2. Add the peer name to `registry/agents/leader/agent.json`:
   ```json
   { "a2a_agents": ["peer-yoke"] }
   ```

3. Hot-reload picks up both files (`POST /api/config/reload`). The leader
   then sees an `a2a_peer-yoke` tool it can invoke with a `prompt`,
   optional `squad`, `session_name`, and `create` arguments.

**Session routing** — when `session_name` is set the remote server looks up
the session by its friendly petname (e.g. `teaching-kite`), runs the turn,
persists it to the session's conversation file, and fires a `mailbox_push`
SSE event to any open web UI tab on that session. When `create: true` the
session is materialised if it does not yet exist (uses the same
`NewWithName` path as `POST /api/sessions` with a name).

**Tool argument precedence**: per-call `squad`/`session_name`/`create` >
`a2a_config.json` defaults > remote server's own defaults.

### A2A server (inbound calls)

`server/a2a_server.go` handles inbound `tasks/send` and `tasks/sendSubscribe`
calls from other A2A agents. Key behaviours:

- **Squad routing**: `metadata.squad` selects which squad the task runs on
  (falls back to `default`).
- **Session routing**: `metadata.session_name` routes into an existing named
  session. `metadata.create: true` auto-creates it if missing.
- **Ephemeral sessions**: omitting `session_name` creates a throwaway session
  per task and discards it after the response.
- **SSE push**: after persisting a turn, `sessionPushBroadcaster.notify`
  fires a `mailbox_push` event so open web UI tabs refresh live.
- **RunGuard**: `sessionRunGuard` serialises concurrent turns on the same
  session (shared with the web UI path — no double-processing).
- **Session name validation**: names must match `[a-z0-9-]{1,80}` (`validSessionName`).

Enable the A2A server via `server.yaml`:
```yaml
a2a_enabled: true
a2a_port: 8091
```

### Remote registries (skills, agents, mcp, a2a, squads, commands)

The web UI can browse and install skills, agents, MCP servers, A2A peers,
squads, and slash commands from any GitHub, GitLab, or Gitea repository.
All share the same `remote_registries.json` file (resolved from the config
search chain; with the same fork-on-first-edit semantics as other config),
and the same set of provider adapters in
[internal/registries/](internal/registries/).

Each entry has a `kind` field: `skills` (default when missing — legacy),
`agents`, `both` (skills + agents), `mcp`, `a2a`, `squads`, or `commands`.
The Settings → Skills/Agents/MCP/A2A/Commands → Remotes tabs each list
only the registries whose `kind` matches; a `both` entry shows up in
both the skills and agents tabs. The "Hosts" selector on the add/edit
dialog sets the kind.

Remote layout — agents:

```
repo/path/to/agents/
├── leader/
│   ├── agent.json        ← required; same shape as registry/agents/<name>/agent.json
│   └── instruction.md    ← optional
└── investigator/
    └── agent.json
```

Remote layout — skills: one `SKILL.md` per subdirectory.

```
repo/path/to/skills/
├── my-skill/
│   └── SKILL.md
└── other-skill/
    └── SKILL.md
```

Remote layout — commands: one Claude Code-style markdown file per command
(Anthropic's `~/.claude/commands/<name>.md` formalism). The filename
without `.md` is the command name; YAML frontmatter (optional) supplies
`description` and `argument-hint`; the body is the prompt template,
supporting `$1..$N` positional and `$*` rest placeholders.

```
repo/path/to/commands/
├── review.md             ← frontmatter + body
└── triage/
    └── repro.md
```

The browse view discovers `agent.json`, `SKILL.md`, or command `.md`
files recursively under the registry URL's `tree` path. The install
button downloads every file in the matched directory into
`$YOKE_HOME/registry/agents/<name>/` (agents) or
`$YOKE_HOME/registry/skills/<name>/` (skills). Commands install into
the single per-user `$YOKE_HOME/user_commands.json` file (same store
the local Slash Commands editor writes to). After installing a skill,
add its name to the target agent's `"skills"` list in `agent.json` —
either via the web UI Skills tab or by editing the file directly.

The agent install dialog also exposes an "Enable in agents.json"
checkbox — when checked the installed agent's name is appended to the
runtime config's `agents` list so the next hot-reload wires it in.

Use `YOKE_AGENTS_REGISTRY_DIR` or `YOKE_SKILLS_REGISTRY_DIR` to redirect
either install location independently of `YOKE_HOME`.

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
