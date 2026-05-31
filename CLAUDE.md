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
go run . reindex-precedents              # rebuild the cross-session precedent index (needs an embedder)
go run . reindex-docs                    # rebuild the documentation semantic index (needs an embedder)
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
the leader after a sub-agent call. By default each sub-agent runs one at a
time ([agent/non_concurrent_tool.go](agent/non_concurrent_tool.go)
`newNonConcurrentTool`, single-task schema): a mutex rejects a duplicate
concurrent call of the same sub-agent with an "already running" error. The
wrapper's `ProcessRequest` packs **itself** (via `packToolDecl`), not the
inner agenttool — ADK dispatches function calls by the object stored in
`req.Tools[name]`, so registering the inner there would call the inner's
`Run` directly and bypass the mutex (the declaration is identical either way,
so the model sees no difference). Setting an agent's
`max_instances > 1` in its `agent.json` swaps that wrapper for
[agent/parallel_agent_tool.go](agent/parallel_agent_tool.go)
`newParallelAgentTool`: the leader then sees a **batch/fan-out** tool whose
schema is `{ tasks: [ <inner-input>, … ] }` (the per-task shape mirrors the
sub-agent's own input schema), capped at `max_instances`. `Run` fans the
tasks out concurrently — each via the inner `agenttool.Run`, which builds
its own runner+session per call, so concurrent invocations of one
stateless sub-agent are safe — bounded by a semaphore of width
`max_instances`, preserves order, isolates per-task errors into their slot,
and returns `{ results: [ … ] }`. Because ADK dispatches function calls via
`req.Tools[name]`, the parallel tool's `ProcessRequest` packs **itself**
(not the inner) via the local `packToolDecl` (a copy of ADK's unexported
`toolutils.PackTool`) so the model gets the batch declaration and the runner
dispatches the fan-out. `max_instances` defaults to `1` and is per-agent.
The web UI Settings → Agent panel exposes it as a **Max parallel instances**
numeric field (a `Parallelism` section, hidden for the leader and curator
since both are excluded from fan-out); the value round-trips through the
editor save and the GET only surfaces it when `> 1` to keep agent.json clean.
The curator stays a single per-generation hook listening across every
squad.

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
| `agent/` | `NewAgent()` — wires all components; `ResolveRuntimeSettings()` — config precedence; `ResolveEmbedder()` — builds the semantic embedder from `embed_model_ref`/`YOKE_EMBED_*` |
| `core/agentkit/` | `New()` — thin ADK agent constructor |
| `core/llm/` | Multi-provider dispatcher: `anthropic`, `openai`, `gemini`, `openai_compat` |
| `core/embed/` | Text→vector embedder mirroring `core/llm`: `Embedder` iface, `Selection`, `NewWithSelection`; providers `openai`/`openai_compat`/`gemini` (anthropic ⇒ `ErrUnsupported`); L2-normalised output + content-hash on-disk cache. Powers all semantic recall |
| `core/tools/` | File-system tools: `Read`, `Write`, `Grep`, `Glob`, `revert`, `Bash` (with safety floor) |
| `core/permissions/` | JSON-based permission gating: always_deny → always_allow → ask_user |
| `core/events/` | Event bus + file logger; before/after model/tool callbacks + session lifecycle |
| `internal/tasks/` | Durable task graph; persisted to `logs/agent_tasks_<u>_<ts>.json` |
| `internal/todo/` | Lightweight scratch list; persisted to `logs/agent_todo_<u>_<ts>.json` |
| `internal/bg/` | Background command queue; `bash_background` + `bg_list` tools |
| `internal/worktree/` | Git worktree isolation tools |
| `internal/teammates/` | Inter-agent mailbox FSM: `teammate_ask/tell/check/list`. The leader's `teammate_check` is suppressed when the host drains the inbox in the background (see "Background mailbox delivery") |
| `internal/skills/` | Skill loader: `load_skill`, `list_skills` (reads `registry/skills/<name>/SKILL.md`) |
| `internal/softskills/` | Curator output: `load_softskill`, `list_softskills` (reads `softskills/`); `Stats` sidecar + `ReflectHeuristic` (deterministic per-skill helpful/harmful/neutral tagging); `recall.go` adds the embedder-gated `recall_softskills` semantic-rank tool |
| `internal/semindex/` | Reusable persistence + query layer over a go-turbovec `IdMapIndex` (`.tvim` + `.meta.json` sidecar + manifest); `Open`/`Upsert`/`Query`/`Remove`/`Save`. Backs all five recall features; nil-embedder handles degrade with `ErrNoEmbedder` |
| `internal/precedents/` | Cross-session precedent index over `semindex` at `index/precedents`; indexes each session's goal + decisions; `recall_precedents` tool |
| `internal/codeindex/` | Per-repo semantic code index over `semindex` (line-window chunks, `git ls-files`-aware, content-hash incremental); `search_code` + `reindex_code` tools |
| `internal/regindex/` | Semantic index over **remote registry** items of **all six kinds** (skills, agents, mcp, a2a, squads, commands) over `semindex` at `index/registries`; metadata-only (name+description+tags, no extra fetch beyond a browse); accurate `installed` flags via per-kind installed-name thunks on `Config` (shared with `buildRegistriesDeps`); `search_registries` + `reindex_registries` tools. Rebuilds on registry-set change (corpus-hash self-heal in `Search` + `registries.OnSave` background hook) |
| `internal/docindex/` | Semantic index over **yoke's own documentation** (user docs `web/docs` + developer docs `docs` → `/usr/share/doc/yoke/docs`; roots from `Roots()`, override `YOKE_DOCS_DIRS`) over `semindex` at `index/docs`; markdown line-window chunks, content-hash incremental, heading-aware, stores the quotable text in chunk meta; `search_docs` + `reindex_docs` tools plus always-on `list_docs`/`read_doc`/`grep_docs` glob fallback (`NewNavTools`). Mounted on the `helper` agent via the `docs` tool group; built/refreshed in the background at server startup |
| `internal/compress/` | Per-session context compression plugin + audit/statelog files |
| `internal/cache/` | Prompt cache hit-rate stats plugin |
| `internal/mcp/` | MCP config loader (path resolved from search chain) |
| `internal/a2a/` | A2A protocol client (`client.go`) + ADK tool wiring (`tools.go`); config types in `a2a.go` |
| `internal/tui/` | tview chat UI (trace pane + streaming chat) |
| `server/` | HTTP API server with Bearer token auth |
| `server/a2a_server.go` | Receives inbound A2A `tasks/send` / `tasks/sendSubscribe` calls; routes by squad + session |

### Semantic recall (embedder + vector indexes)

Five **additive, embedder-gated** recall features share `core/embed` +
`internal/semindex` (a wrapper over the `go-turbovec` pure-Go ANN index,
BitWidth 4 + UnitNorm cosine):

1. **`recall_softskills`** (leader) — semantically ranks curator-distilled
   soft-skills for the user's task; mounted beside the glob `list_softskills`
   ([internal/softskills/recall.go](internal/softskills/recall.go)). Index
   refreshes on call, content-hash gated.
2. **`recall_precedents`** (reflector + curator) — recalls past sessions' goals
   + decisions ([internal/precedents/](internal/precedents/)). Indexed on
   `EventSessionReflected` by [agent/precedents_hook.go](agent/precedents_hook.go).
   Web UI sessions never fire `EventSessionEnd` (so never `EventSessionReflected`),
   so the server also indexes them via the lightweight, indexing-only
   `EventSessionIndexNow` trigger (same hook): the idle indexer
   ([server/idle_indexer.go](server/idle_indexer.go)) fires it once a session
   has been idle ≥ 5 min (fixed threshold, independent of the curator's
   `YOKE_CURATOR_IDLE_TIMEOUT`), and the archive handler fires it immediately on
   `POST /api/sessions/:id/archive`. An in-memory `SessionMeta.Indexed` flag
   (set by `Registry.MarkIndexed`, cleared by `Touch`) stops re-indexing every
   scan tick. Backfill via `yoke reindex-precedents`.
3. **`search_code` / `reindex_code`** (investigator) —
   semantic code search over the repo ([internal/codeindex/](internal/codeindex/)),
   `git ls-files`-aware, content-hash incremental.
4. **`search_registries` / `reindex_registries`** (helper) —
   semantic search over **every kind** advertised by the configured remote
   registries — skills, agents, mcp, a2a, squads, commands
   ([internal/regindex/](internal/regindex/)). Mounted alongside the
   glob `browse_registry` whenever the `registries` tool group is present and an
   embedder resolves. The crawler's `browse_registry` / `get_remote_item` /
   `install_remote_item` tools likewise cover all six kinds (command install
   writes the per-user `user_commands.json` via the shared
   [internal/usercommands/](internal/usercommands/) package, which also backs the
   web-UI command editor). **Metadata-only**: embeds the name/description/tags a
   browse already returns, so no HTTP fetch beyond a normal browse. Indexing is
   lazy (first `search_registries` call) and self-healing (a corpus hash of the
   registry set — ids+urls+kinds — triggers a rebuild in `Search` when it
   changes); a `registries.OnSave` hook also rebuilds in the background after
   any web-UI/tool edit to `remote_registries.json`. Remote *content* drift
   (same URL, changed skills) is only caught by explicit `reindex_registries`.
5. **`search_docs` / `reindex_docs`** (helper) — semantic search over **yoke's
   own documentation** so the Helper can answer questions about yoke and quote
   the source ([internal/docindex/](internal/docindex/)). Indexes markdown across
   every doc root from `docindex.Roots()` — the web UI user docs (`web/docs` →
   `/usr/share/yoke/web/docs`) and the developer docs (`docs` →
   `/usr/share/doc/yoke/docs`), override with `YOKE_DOCS_DIRS`. Mounted via the
   `docs` tool group alongside the always-on glob `list_docs` / `read_doc` /
   `grep_docs` (`NewNavTools`), which are the no-embedder fallback. Chunking is
   line-window + heading-aware and content-hash incremental; each hit carries the
   source `path`, `heading`, line range and the quoted `text`. Built/refreshed in
   the background at server startup ([server/docs_indexer.go](server/docs_indexer.go)
   `startDocsIndexer`): the incremental `Reindex` builds on first boot and after
   docs/embedder change, no-op otherwise. Backfill via `yoke reindex-docs`.

The embedder and all index handles are process-wide on `Infrastructure`
(`Embedder()`, `Precedents()`, `CodeIndex()`, `RegistryIndex()`, `DocIndex()` in [agent/embedder.go](agent/embedder.go)),
built lazily and surviving hot-reload. **Contract: when no embedder resolves,
none of the recall tools are mounted and every path falls back to glob/grep —
behaviour is byte-identical to a build without these features.** See
[agent/embedder.go](agent/embedder.go) `ResolveEmbedder` for the
`embed_model_ref` → `YOKE_EMBED_*` precedence.

### Configuration files

Config files are resolved through a **3-layer search chain** (high → low precedence):
`.agents/` (or `agents/` as a dotless alias; both participate when both exist, `.agents/` first) → `$HOME/.yoke/` (per-user) → `/etc/yoke/` (system). Agent and skill registries live under `registry/agents/` and `registry/skills/` inside whichever layer you're targeting.

| File | Purpose |
|---|---|
| `agents.json` | List of enabled agent names, squad composition, global paths |
| `models.json` | Providers (credentials + endpoint) and reusable model profiles referenced by agents via `model_ref`. Per-model `"disable_streaming": true` forces agents using that model onto the non-streaming endpoint (for backends whose streamed output misbehaves). Also: embedding models (`"embedding": true` + `"dim"`) and `"embed_model_ref"` selecting the internal embedder for semantic recall |
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
  "agents": ["leader", "investigator", "web_agent", "skill_editor", "helper", "summariser", "curator"],
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

**Embedding model selection (semantic recall).** A model entry may be flagged
`"embedding": true` (with an optional `"dim"`); such entries are *not* picked by
agents via `model_ref` — they show up in the Web UI Models panel's "internal
embedding model" selector and in nothing else. The top-level `models.json`
field `"embed_model_ref"` names which embedding model is the active internal
embedder for all semantic recall (soft-skills, precedents, codebase). It can be
overridden by `embed_model_ref` in `agents.json` and then by the
`YOKE_EMBED_MODEL_REF` env var; when none resolves (and no `YOKE_EMBED_*` env is
set) the embedder is absent and every recall feature silently falls back to its
glob/grep path. The embedder is process-wide (built once on `Infrastructure`,
survives hot-reload like the MCP pool); changing `embed_model_ref` needs a
server restart to take effect.

**Models editor auto-fill (web UI).** The Settings → Models panel can prefill
model fields from the provider instead of asking the user to type them. Two
server helper routes back this (both resolve credentials via `provider_ref` —
no secrets cross the wire — or explicit `provider`/`api_key`/`base_url`
overrides; see [server/provider_models.go](server/provider_models.go)):
`GET /api/providers/models` lists the provider's models (the model combobox's
⟳ button) and `GET /api/providers/embedding-dim?model=…` probes the embeddings
endpoint with one tiny request and returns the vector length, filling the DIM
field via the ⟳ button beside it ([web/settings.js](web/settings.js) `dimField`).
Dimension detection requires both a provider and a model id and reports the
model's native dimension.

The model list is metadata-aware for **LiteLLM** proxies (ChapsVision's gateways
are LiteLLM): `fetchOpenAIStyleModels` first tries `GET {base}/v1/model/info`
and, when present, maps each model's `model_info` — `max_input_tokens` →
`context_length`, `input/output/cache_read cost_per_token` → the per-million
prices, `output_vector_size` → `dim`, and `mode == "embedding"` → the embedding
flag. Selecting such a model in the combobox prefills all of these (without
overwriting fields the user already set) and re-renders the card. Plain OpenAI /
Ollama / vLLM endpoints (no `/model/info`) fall back to `GET /v1/models`, which
returns ids only.

Each `registry/agents/<name>/agent.json` is the full `AgentEntry`. A
`"builtin": true` flag marks agents shipped with yoke (leader,
skill_editor, helper, summariser, curator); custom agents added
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
  ├── index/            # semantic vector indexes (paths.IndexDir())
  │   ├── embed_cache/  #   content-hash embedding cache (sha256(model+text))
  │   ├── softskills.tvim + .meta.json   # recall_softskills index
  │   ├── precedents.tvim + .meta.json   # recall_precedents index
  │   ├── registries.tvim + .meta.json   # search_registries index (remote skills+agents)
  │   ├── docs.tvim + .meta.json         # search_docs index (yoke's own docs)
  │   │                 #   + docs.files.json (per-file hash→chunk-ids)
  │   └── <repo-hash>/  #   per-repo code index: codebase.tvim + .meta.json
  │                     #   + codebase.files.json (per-file hash→chunk-ids)
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
| `YOKE_EMBED_PROVIDER` | Embedder provider for semantic recall (default: `YOKE_PROVIDER`, else `openai_compat`). `anthropic` unsupported — use Voyage/OpenAI via `openai_compat` |
| `YOKE_EMBED_MODEL` | Embedding model id (default `text-embedding-3-small`) |
| `YOKE_EMBED_BASE_URL` | Embeddings endpoint (default `YOKE_BASE_URL`/`OPENAI_BASE_URL`) |
| `YOKE_EMBED_API_KEY` | Embedder API key (default `YOKE_API_KEY`/provider key) |
| `YOKE_EMBED_DIM` | Expected embedding dimension (default `1536`, or learned from the first response) |
| `YOKE_EMBED_MODEL_REF` | Overrides `embed_model_ref` from `models.json` — selects which catalogue model is the internal embedder |
| `YOKE_DOCS_DIRS` | Colon-separated documentation roots for `search_docs`/`list_docs`; replaces the auto-discovered set (`<webDir>/docs`, `/usr/share/yoke/web/docs`, `docs`, `/usr/share/doc/yoke/docs`) |
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
| `YOKE_LLM_STREAM_STALL_TIMEOUT` | Max idle gap between streamed chunks before the LLM read is aborted (Go duration, default `30s`; `0` disables). Guards against an upstream/gateway that streams partial text then goes silent without `[DONE]` or closing — otherwise the turn freezes "mid sentence" until the 5-minute client timeout. Applies to both the OpenAI/compat and Anthropic adapters ([core/llm/stall.go](core/llm/stall.go)). |

### Permission prompts (ask_user) and grant scopes

When a tool call matches `ask_user` (or no rule), the permissions plugin
([core/permissions/permissions.go](core/permissions/permissions.go))
calls the configured `Asker`. In server/TUI mode the asker is
[agent/permission_asker.go](agent/permission_asker.go), which renders an
`ask_user` SSE widget; in CLI mode it's the `StdinAsker`. The user picks
one of five scopes, ordered by increasing blast radius (`AskOutcome`):

| Choice | Outcome | Effect |
|---|---|---|
| Deny | `OutcomeDeny` | Reject this call; next identical call asks again. |
| Allow once (this call) | `OutcomeAllowOnce` | Cache the **exact (tool, args)** probe for the session. |
| Allow all `<Tool>` this session | `OutcomeAllowToolSession` | Cache a **per-tool** grant for the session — every later call of that tool auto-allows regardless of args. In memory only; never persisted. |
| Allow in this project | `OutcomeAllowProject` | Persist an `always_allow` rule with `CWD` = project dir. |
| Allow always | `OutcomeAllowAlways` | Persist an `always_allow` rule with no `CWD`. |

The session-approval cache ([core/permissions/cache.go](core/permissions/cache.go))
holds two granularities: per-call (`m`) and per-tool (`tools`); a per-tool
grant short-circuits before per-call. Both are wiped by `Forget(sessionID)`
on `EventSessionEnd`.

**Persisted-rule breadth** ([core/permissions/persist.go](core/permissions/persist.go)
`buildApprovalRule`) differs by tool: file tools (`Read`/`Write`/`Edit`/`revert`)
broaden to "this tool on **any** path" (`^Write\b`), so approving the first
of N file writes covers the rest — the `CWD` field still scopes "Allow in
this project" to the project tree. `Bash` keeps an **exact-command** match
(a blanket persisted shell allow is a footgun; use the ephemeral
"Allow all Bash this session" grant for command bursts instead).

### Session isolation

Every mutable component scopes its state by `(userID, buildTimestamp)`. Concurrent sessions never share task graphs, todo lists, memory, or mailbox namespaces. All session files land in `$YOKE_HOME/logs/`:

- `agent_tasks_<u>_<ts>.json` — task graph
- `agent_todo_<u>_<ts>.json` — todo plan
- `agent_memory_<u>_<ts>.md` — compressed session memory
- `agent_statelog_<u>_<ts>.json` — full state log (consumed by curator)
- `agent_events_<ts>.log` — event audit log (global per build)
- `conversation_<id>.json` — Web UI turn history + title + `squad` name + `Harvested` flag + `Archived` flag (server only)

### Session states (active / archived / deleted)

A session is in one of three states:

- **active** — present in the registry, listed in the sidebar, chattable.
- **archived** — present and **viewable read-only**, but detached from its agent
  generation. Set via `POST /api/sessions/:id/archive` (and reversed by
  `…/unarchive`). The `Archived` flag lives on both `SessionMeta`
  ([internal/sessions/sessions.go](internal/sessions/sessions.go)) and
  `ConversationFile` ([internal/sessions/history.go](internal/sessions/history.go),
  the durable source of truth); `Registry.SetArchived` mirrors the in-memory
  flag and persists it asynchronously via `SetConversationArchived`. Archiving
  calls `PushMgr.Stop` + `Manager.Release`; unarchiving re-`Pin`s and re-`Watch`es.
  The turn handler (`handleMessages` in [server/sse.go](server/sse.go)) rejects
  new turns on an archived session with `409 Conflict` (read-only guard); the TUI
  `send` path blocks them similarly.
- **deleted** — registry entry removed, conversation + agent log files
  hard-deleted (unchanged behaviour).

**GC retention invariant**: archived sessions stay in `Registry.List()`, so the
GC ([server/gc.go](server/gc.go) `activeFromRegistry`) treats them as live and
retains their files — keeping them available for semantic-recall indexing.

Both UI surfaces render archived sessions in a **collapsible panel above the
Settings button**: the Web UI `#archived-panel` ([web/index.html](web/index.html),
[web/app.js](web/app.js) `renderSessions`/`archiveSession`/`unarchiveSession`,
collapse state in `localStorage`), and the TUI `archivedPane` in the left column
([internal/tui/tui.go](internal/tui/tui.go), toggled with **Ctrl-A**; `a` archives
the highlighted session, `u` unarchives, `d` deletes). Viewing an archived session
disables the composer in both surfaces.

### Background mailbox delivery

In **server mode** the leader's mailbox is drained in the background, not
polled by the model. [server/mailbox_push.go](server/mailbox_push.go)
`pushManager` runs one goroutine per session (via
[agent/infrastructure.go](agent/infrastructure.go) `WatchMailbox`); when a
cross-session message arrives it `inject`s a synthetic `"[mailbox] …"` turn
(serialised against user turns by `sessionRunGuard`) and fires the
`sessionPushBroadcaster` so open web UI tabs refresh.

Because the JSONL backend's `Receive` **consumes** the message (single
reader), the model must not also poll the same inbox. The server therefore
sets `Options.BackgroundMailboxDelivery = true`, which sets
`teammates.Agent.SuppressInboxPolling` on the leader and **omits the
`teammate_check` tool** from the leader's toolset. The leader instruction no
longer mandates a per-turn mailbox poll — incoming messages arrive as
injected turns instead. CLI/TUI leave the flag false (no background drainer),
so `teammate_check` stays as the leader's only delivery path there.
`teammate_ask/tell/list` are unaffected in both modes. (Note: `teammate_ask`
still reads replies from the leader's own inbox, so under background delivery
its reply can race the drainer — a known limitation, separate from the
per-turn `teammate_check` tax this removed.)

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
version" pill.

The Settings editor's post-save banner ([web/settings.js](web/settings.js))
picks **one** action by mode, decided at save time:
- **reload** (default) — offers only **Reload** (hot-reload, no downtime).
- **restart** — offers only **Restart server**. Entered when a save changes
  the **embedder identity** in `models.json` (the `embed_model_ref`, or the
  referenced model's id/dim/provider connection — see `embedderFingerprint`),
  because the embedder is process-wide on `Infrastructure` and survives
  hot-reload, so only a full restart applies it. The mode is a sticky
  `localStorage` flag (`agent_toolkit_restart_required`) so a later
  hot-reloadable save can't downgrade a still-pending embedder restart back to
  Reload; it clears only on an actual Reload/Restart. The "Restart server"
  option is therefore **proposed only when an embedder change is pending** —
  every other edit shows Reload. (Restart remains the conceptual escape hatch
  for env/binary updates, but those are applied out-of-band, not via this
  banner.)

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

There is also a consolidated **Settings → Registries** section (top-level
sidebar entry, between Commands and Appearance) that concentrates every remote
registry grouped by kind in a left nav (Skills / Agents / Squads / MCP / A2A /
Commands), with the same Add / Edit / Remove / Browse / Install flows as the
per-kind Remotes tabs — it *reuses* those per-kind renderers
([web/settings.js](web/settings.js) `renderRegistriesHub`), it does not
duplicate them. Two nav-context indirections let the reused renderers re-render
into the hub's right panel: `registriesHubRefresh` for the form-based kinds
(skills/mcp/a2a/commands) and the pre-existing `refreshRemotesRightFn` for
agents/squads; both are cleared at the top of each per-kind form renderer so the
standalone tabs are unchanged. A single **Reindex** button rebuilds the semantic
registry index via `POST /api/registries/reindex`
([server/server.go](server/server.go)), which calls
`Infrastructure.RegistryIndex(...).Reindex(ctx)` and returns the indexed-item
count (or `400` with a clear message when no embedding model is configured, in
which case the index is absent and recall falls back to glob/browse).

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

Streaming renders in two tiers ([web/app.js](web/app.js)
`streamMdAdvance`/`streamMdFinalize`). **Completed blocks** — anything before a
blank line outside a fence, or a closed code fence — are promoted to HTML by
the full `marked.parse` exactly once and never re-parsed. The **in-progress
trailing block** (the "tail") is a single live node: a raw `<pre><code>` Text
node for code fences (appended via `appendData` at wire speed, verbatim), and a
`<span class="md-stream-tail">` for prose. The prose tail is re-rendered every
token by `lightStreamMd` — a tiny regex renderer that handles the common block
constructs (ATX headings, ordered/unordered tight lists, `<hr>`, blockquotes)
plus inline emphasis/strike/code via `lightInline`, collapses accumulated blank
lines, and emits `<br>` for single newlines to mirror `breaks:true`. Its HTML is
shaped to match `marked`'s tight-list/heading output so the preview doesn't
reflow when the real parser flushes the block. `lightInline` protects inline
code with private-use sentinels (`<n>`) before escaping/emphasis so
`**\`x\`**` renders as bold-wrapping-code (and code-internal `*`/digits stay
literal). This is cheap because it only ever touches the current block
(everything before `s.blockStart` is already flushed), so cost stays O(block),
not O(message²). The bubble carries **no `white-space: pre-wrap`** — the tail
emits its own `<br>`s and code lives in `<pre>`, so dropping pre-wrap stops the
literal newlines `marked` puts between block tags (`</ul>\n<ul>`) from rendering
as blank gaps mid-stream. **Do not run the full `marked.parse` per chunk on the
whole message** — that quadratic re-parse is what makes the UI feel slow even
when the wire is fast; `lightStreamMd` is the bounded exception, and only the
heavy parser produces the authoritative final HTML at block flush / finalize.

### Web UI todo plan widget

The `todo_write` / `todo_update` / `todo_read` tools do **not** render as the
generic collapsed tool block. Instead [web/app.js](web/app.js) keeps a
per-session plan view in `sessionTodos` (sessionId → `[{task, status}]`) and
renders an "Update Todos" checklist (`.todo-block`, styled in
[web/css/styles.css](web/css/styles.css)) on every todo tool call so users can
follow plan execution: pending = empty box, `in_progress` = spinning marker,
`done`/`failed` = filled box + struck-through text. `todo_write` rebuilds the
list from `args.tasks`; `todo_update` mutates the item at `args.index`. These
calls are routed in the `tool_call` SSE case (via `isTodoTool`) and are **not**
pushed to `pendingTools`, so their `tool_result` is ignored.

Only the latest snapshot per session stays expanded: `sessionTodoBlock`
(sessionId → latest `.todo-block`) lets `appendTodoBlock` add the `collapsed`
class to the prior block when a new one arrives. Any block's header is a
click-toggle, and its `done/total` progress count stays visible while
collapsed. State is live-only (history replay renders text turns, not tool
calls) and both maps are cleared on session delete.

### Web UI split panels (VS Code-style)

`#chat` is a horizontal flex **row** of one-or-more independent `.chat-pane`
columns separated by draggable `.pane-divider` handles ([web/index.html](web/index.html)
`<template id="chat-pane-tpl">` is cloned per pane; [web/css/styles.css](web/css/styles.css)
`.chat-pane`/`.pane-divider`/`.pane-toolbar`/`.pane-picker`). Each pane owns its
own copy of the chat UI (transcript, composer, prompt, send/cancel, status,
context ring + popup, ask-user slot, attachments) and is bound to **at most one
session** — `getContainer(sessionId)` returns a single DOM node per session, so a
session can be mounted in only one pane at a time. Selecting a session already
shown elsewhere focuses that pane instead of duplicating.

Per-session state stays in the existing `sessionId`-keyed Maps; the refactor is
purely in the view layer ([web/app.js](web/app.js)): a `panels` array of
`{id, sessionId, root, els, width, _stick}` objects, `focusedPanelId`, and
helpers `panelsForSession(id)`, `focusedPanel()`/`fp()`, `setFocusedPanel`,
`bindSessionToPanel`, `createPanel`/`splitPanel`/`closePanel`,
`rebuildChatDOM`/`layoutWidths`, `paneOfNode` (resolve a node's pane for
scroll/media). `activeSessionId` is a **compatibility shim** = the focused pane's
session, so global-action sites (sidebar, modals, ctx browser) keep working.
Display-write functions (`applySessionUI`, `renderCtxRing/Popup`, `setStatus`,
`scrollBottom`, pinned-prompt, `renderAttachmentsUI`, `renderAskUserWidget`,
streaming gates) take/loop a `panel` via `panelsForSession` so **background panes
update too** and each pane scrolls independently. Composer/prompt/attach/slash/
ctx-ring/cancel/resize listeners are wired **per-pane** in `attachPaneHandlers`.

A pane's toolbar has split (clones a new empty pane to the right) and close
(hidden when `#chat.solo`) buttons; an empty pane shows `.pane-picker` (start a
new chat → `newChat(panel)`, or open an existing session). Push subscriptions
follow `releaseSessionIfUnviewed` (subscribe while mounted, drop when no pane
shows it). Layout (sessions + widths + focus) persists to
`localStorage["agent_toolkit_layout"]` (`saveLayout`/`restoreLayout`, restored
on boot after `loadSessions`, dropping dead session ids to empty panes). The
Settings panel still appends to `#chat`; `#chat.chat--settings > .chat-pane`
hides panes while it's open, and `rebuildChatDOM` preserves `#settings-panel`.
