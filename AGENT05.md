# AGENT.md — omnis

**omnis** is a generic, vendor-neutral AI agent harness written in Go. It turns any supported LLM into a specialist assistant by mounting tools, skills, and MCP servers — with no code changes required to retarget the agent at a new domain.

---

## Design contract

> The agent's effective capability on any run = union of mounted **tools** ∪ **skills** ∪ **MCP servers**.
> The same binary becomes a code reviewer, a Kubernetes triage assistant, a DBA helper, or a release engineer purely by changing what is mounted — **never by editing Go code or the system prompt**.

This rule is non-negotiable. If you feel the urge to add domain knowledge to a `.go` file or to `core/agentkit/agentkit.go`'s `SystemPrompt`, stop and put it in a `registry/skills/<name>/SKILL.md` instead.

---

## Commands / workflows

Go is installed at `$HOME/.local/go`. Prefix every `go` command accordingly, or export the PATH once:

```bash
export PATH=$HOME/.local/go/bin:$PATH
export GOPATH=$HOME/.local/gopath
```

### Build

```bash
make build              # bin/omnis + bin/omnis-server (host platform)
make build-root         # bin/omnis only
make build-server       # bin/omnis-server only
make examples           # opt-in: build all examples under bin/
make release            # cross-platform raw binaries → dist/ (plain Go, no goreleaser)
make package            # .deb / .rpm / .zip / .tar.gz via goreleaser (goreleaser must be installed)
make package-check      # validate .goreleaser.yaml; accepted non-zero exit if only the 'brews' deprecation warning fires
```

### Test

```bash
make test               # go test ./... (unit tests, no LLM calls)
make env-tests          # LLM integration tests — requires .env with API keys
make a2a-smoke          # A2A protocol smoke test against a live endpoint
```

### Quality

```bash
make fmt                # go fmt ./...
make vet                # go vet ./...
```

**Pre-commit canonical check** — run after every code change; do not declare a task done until the final line is `OK`:

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go vet ./... && echo OK
```

### Run

```bash
# Interactive REPL (stdin is a TTY)
go run .

# One-shot
go run . "summarize the architecture"

# TUI
go run . tui

# Web UI server (requires OMNIS_SERVER_TOKEN)
go run ./server
# or
make run-server
```

Flags must come **before** the subcommand or prompt:

```bash
go run . -d "what does main.go do?"
```

### Key CLI flags

The flags defined in the current `main.go` are:

| Flag | Default | Effect |
|---|---|---|
| `--softskills DIR` | _(empty)_ | Directory of curator-generated soft-skills. |
| `--config PATH` | _(empty)_ — falls back to `OMNIS_CONFIG_PATH` env var, then config search chain | Runtime JSON config path. |
| `--curator-enabled BOOL` | _(empty)_ — inherits from `OMNIS_CURATOR_ENABLED` env var | Enable/disable the auto-curator hook. |
| `--name NAME` | _(empty)_ | Application name. |
| `-d`, `--debug` | `false` | Write full conversation/event payloads to the event log. |

Provider, model, and API key are set via environment variables (see the env vars table below) or via `config/agents.json` / `registry/agents/<name>/agent.json`. Flags must come **before** the subcommand or prompt.

---

## Structure

```
omnis/
├── main.go                   # Root binary entry: CLI / TUI / curate dispatch. The only wiring file.
├── curate.go                 # `omnis curate` one-shot subcommand
├── server/                   # Separate binary: HTTP + SSE API + web UI server
├── web/                      # Vanilla-JS chat UI assets served by server/
│   └── docs/                 # In-app documentation (Markdown, served to the web UI)
├── agent/                    # NewAgent() — plugin and toolset assembly
├── core/
│   ├── agentkit/             # Central agent constructor + universal SystemPrompt
│   ├── llm/                  # Multi-provider model dispatcher (Anthropic, OpenAI, Gemini, compat)
│   ├── tools/                # file / bash / grep / glob / revert tools
│   ├── permissions/          # JSON-driven permission plugin
│   ├── events/               # Plugin-friendly event bus + file logger
│   └── stream/               # Streaming helpers
├── internal/
│   ├── cli/                  # stdio REPL + one-shot frontend
│   ├── tui/                  # tview chat frontend
│   ├── todo/                 # TodoWrite tools + store
│   ├── tasks/                # Durable task graph
│   ├── bg/                   # Background command queue
│   ├── worktree/             # Git worktree isolation tools
│   ├── teammates/            # Mailbox / FSM-based inter-agent comms (in-mem + Redis backends)
│   ├── compress/             # Context compression plugin
│   ├── cache/                # Prompt-cache stats plugin
│   ├── skills/               # Skill loader (skilltoolset wrapper)
│   ├── softskills/           # Curator-distilled procedures + reflectors
│   ├── mcp/                  # MCP config loader
│   ├── a2a/                  # A2A protocol client + tool wiring
│   └── paths/                # Centralised filesystem location resolution
├── registry/
│   ├── agents/<name>/        # Per-agent definitions (agent.json + instruction.md)
│   └── skills/<name>/        # Skill playbooks (SKILL.md + optional assets/)
├── softskills/               # Curator output (incl. _stats.json sidecar + wrap-session built-in)
├── config/                   # Project-local config: agents.json, permissions.json, mcp_config.json, server.yaml, models.json
├── .agents/                  # Development-agent bootstrap skills (project-overview, build-and-test, coding-conventions, …)
│   └── skills/               # Skills loaded by *the dev agent working on this repo*
├── examples/sNN_*/           # 30 single-component demos (opt-in via `make examples`)
├── docs/                     # Extended Markdown documentation
└── doc.go                    # Package-level overview for go doc
```

### Key config files (under `config/`)

| File | Purpose |
|---|---|
| `agents.json` | App name, enabled agents, squad composition, token optimization |
| `registry/agents/<name>/agent.json` | Per-agent definition: model_ref, tools, enabled flag, leader flag |
| `registry/agents/<name>/instruction.md` | Per-agent system prompt (falls back to `registry/agents/default.md`) |
| `models.json` | Reusable model profiles referenced by `model_ref` in agent.json |
| `permissions.json` | Safety envelope (Claude Code nomenclature): `permissions.{allow,ask,deny}` of `Tool(specifier)` rules + `defaultMode` |
| `mcp_config.json` | MCP server definitions (spawned as child processes) |
| `server.yaml` | Server listen address/port and bearer token |
| `a2a_config.json` | Remote A2A agent endpoints |
| `filters/` | Bash output filter patterns for token optimization |

---

## Configuration and filesystem layout

### Config search chain (read, high → low precedence)

1. `.agents/` (canonical) and/or `agents/` (dotless alias) — project-local (CWD-relative)
2. `$HOME/.omnis/` — per-user state root (overridable via `OMNIS_HOME`)
3. `/etc/omnis/` — system-wide install (overridable via `OMNIS_SYSTEM_CONFIG_DIR`)

Override the whole chain with `OMNIS_CONFIG_DIRS` (colon-separated). The first layer that has a given file wins for that entire file (file-level override, not merge).

Skill and agent registries live one level deeper inside each layer:
- Config layer root → `registry/agents/` and `registry/skills/`

### State root (write)

All mutable runtime state is written under `$HOME/.omnis` (default) or `$OMNIS_HOME`:

- `$OMNIS_HOME/softskills/` — curator-distilled soft-skills
- `$OMNIS_HOME/registry/skills/` — skills installed via the web UI
- `$OMNIS_HOME/registry/agents/` — agents installed via the web UI
- `$OMNIS_HOME/logs/` — session event logs and feedback sidecars

### Key environment variables

| Variable | Purpose |
|---|---|
| `OMNIS_PROVIDER` | `anthropic` / `openai` / `gemini` / `openai_compat` (default) |
| `OMNIS_MODEL` | Provider-specific model ID override |
| `OMNIS_API_KEY` | Provider API key (alternative to provider-specific vars) |
| `ANTHROPIC_API_KEY` | Claude key |
| `OPENAI_API_KEY` | OpenAI key |
| `GOOGLE_API_KEY` or `GEMINI_API_KEY` | Gemini key |
| `OPENAI_BASE_URL` | API endpoint (required for `openai_compat`) |
| `OMNIS_HOME` | Per-user state root (default: `$HOME/.omnis`) |
| `OMNIS_CONFIG_DIRS` | Replaces the entire config search chain |
| `OMNIS_SERVER_TOKEN` | Bearer token required by the HTTP server |
| `OMNIS_SERVER_ADDR` | Server listen address (default: `:8080`) |
| `OMNIS_DEBUG` | Log full conversation/event payloads |
| `OMNIS_CURATOR_ENABLED` | `true` / `false` — enable auto-curator hook |
| `REDIS_URL` | Redis backend for mailboxes (optional; in-memory used when unset) |

See `web/docs/16-env-vars.md` for the complete list.

---

## Specialising the agent (no Go required)

The three-step specialisation recipe — no Go code changes needed:

1. **Write a skill** at `registry/skills/<name>/SKILL.md`. Required frontmatter: `name` (matching the folder) and `description` (action-oriented, includes trigger keywords the model matches against).
2. **Mount tools** by adding MCP servers to `mcp_config.json` (or the project-local `.agents/mcp_config.json`).
3. **Gate mutations** by adding patterns to `permissions.json`.

The skill loader (`internal/skills`) searches both `<layer>/skills/` and `<layer>/registry/skills/` within each config layer. New playbooks may be placed in either location; `registry/skills/` is the preferred location for project-level skills. No code wiring is needed.

---

## Conventions

### Architectural rules (non-negotiable)

- **No domain knowledge in Go.** Domain rules belong in `registry/skills/<name>/SKILL.md`, MCP server configs, or `permissions.json`. Never write `if topic == "kubernetes"` or mention domain tools (`kubectl`, `psql`, `aws`) in `.go` files under `core/` or root.
- **The system prompt describes a method, not a domain.** `core/agentkit/agentkit.go`'s `SystemPrompt` must remain domain-agnostic. Domain rules go in skills.
- **Sub-agents are role-based, not domain-based.** Built-in sub-agents (`investigator`, `summariser`) are generic roles. New sub-agents get a *role* instruction, never a domain instruction.
- **Every mutating tool needs a permission rule.** Pair any tool that writes state with a rule in `permissions.json` under the `ask` or `deny` tier.
- **Use `agentkit.New`, never `llmagent.New` directly.** This guarantees every agent inherits the universal `SystemPrompt`.
- **No new LLM SDKs in `go.mod`.** Use the in-tree `core/llm/` HTTP+SSE adapters. For OpenAI-compatible providers, set `OPENAI_BASE_URL` — no code needed.

### Go style

- Standard `gofmt`. No custom formatter.
- Error wrapping: `fmt.Errorf("pkg.Func: %w", err)` — always include the caller's identity.
- Tool definitions go in their own `.go` file named after the tool (`grep.go`, `revert.go`, …).
- Constructors return `(T, error)`, never `T` with a panic.
- Tests live next to code as `*_test.go`.

### Skill authoring

- Folder name = `name` in frontmatter. Must match exactly.
- `description` must include the trigger keywords a user is likely to type — this is what the lead matches against.
- Body is a checklist: one verb per step. Cite tools by name.
- Include a "Hard rules" section naming destructive verbs that require confirmation.
- End with an output rule (`Result: ok | needs-attention | blocked`).
- Target ≤ 150 lines; push detail into `references/` subdirectory.

### Sub-agent wiring

Do **not** pass sub-agents via `SubAgents` in `agentkit.AgentConfig`. Use `agenttool.New` instead — it wraps the sub-agent as a regular tool so control always returns to the leader. Wrap with `newNonConcurrentTool` in `main.go` so duplicate calls to the same sub-agent in one turn fail fast.

### Adding MCP servers or skills

- **MCP server**: edit `mcp_config.json` (or `.agents/mcp_config.json`). No Go changes.
- **Skill**: drop `registry/skills/<name>/SKILL.md`. No Go changes.
- **New tool in Go**: add to `core/tools/` or a new `internal/<name>/` package, then wire into `main.go` via `leadTools = append(leadTools, mypkg.New())`.

---

## Gotchas

**`skills/` vs `registry/skills/`**
The README and older skill docs reference a top-level `skills/` directory, but there is no `skills/` directory at the repository root. The skill loader searches both `<layer>/skills/` and `<layer>/registry/skills/` within each config layer. Drop new playbooks under `registry/skills/`.

**Config is file-level override, not merge**
If `.agents/agents.json` exists, it completely replaces `$HOME/.omnis/agents.json` for every field. There is no deep merge across layers. A project-local file must be self-contained.

**Go path prefix required**
`go` is installed at `$HOME/.local/go/bin`, not on the default `PATH`. Every `go` invocation (build, vet, test, run) must be prefixed with `PATH=$HOME/.local/go/bin:$PATH` or the path must be exported once. Symptom of forgetting: `go: command not found`.

**Flags before the subcommand**
CLI flags (e.g. `-d`, `--skills`) must appear *before* the subcommand or prompt argument. `go run . tui -d` silently ignores `-d`; `go run . -d tui` is correct.

**Sub-agents must not use `SubAgents` field**
Passing agents via `AgentConfig.SubAgents` causes ADK to inject `transfer_to_agent`, which permanently hands off control — the leader never resumes. Always use `agenttool.New` and wrap with `newNonConcurrentTool`.

**Plugin failures are best-effort**
Plugin constructor failures inside `run()` should be logged, not returned as fatal errors — except for the permissions plugin, which is safety-critical and must succeed.

**Mailbox backend requires Redis when `REDIS_URL` is set**
If `REDIS_URL` is set but the server is unreachable, the agent will fail to start. Leave `REDIS_URL` unset to use the in-memory backend.

**`mcp_config.json` is resolved via the config search chain**
`mcp_config.json` is found by searching the config chain (`.agents/` → `$HOME/.omnis/` → `/etc/omnis/`) — not from a hardcoded path. The project includes a template at `config/mcp_config.json` used when the config path is explicitly set to `config/agents.json`. Add or edit MCP servers in whichever layer's `mcp_config.json` applies to your context.

**Runtime files — never commit**
The following are generated at runtime and are gitignored: `.agent_events.log`, `.agent_memory.md`, `.mailboxes/`, `bin/`, `dist/`, `logs/`.

**`.env` file**
Copy `.env.example` to `.env` and fill in your keys. `make env-tests` sources `.env` automatically; manual `source .env` is only needed when running `go test` or `go run` directly. `.env` is gitignored.

---

## Documentation index

| File | Topic |
|---|---|
| `docs/architecture.md` | Component map, data flow, plugin lifecycle |
| `docs/methodology.md` | The Claude Code 7-step operating method |
| `docs/configuration.md` | Full configuration reference |
| `docs/specialising.md` | How to retarget the agent at a new domain |
| `docs/skills.md` | Authoring `SKILL.md` files |
| `docs/providers.md` | Configuring Gemini / Anthropic / OpenAI / compat |
| `docs/extending.md` | Adding new tools, sub-agents, squads, and plugins |
| `docs/context-management.md` | Context compression + session decision log |
| `docs/semantic-recall.md` | Embedder, vector indexes, and cross-session precedents |
| `docs/examples-catalog.md` | The 30 single-component demos |
| `docs/notebooks.md` | Jupyter notebook examples and learning path |
| `web/docs/` | In-app documentation served by the web UI |

## Development-agent bootstrap skills

The `.agents/skills/` directory contains skills for the *development agent* working on this repository (not the shipped skills). Load these at the start of a development session:

| Skill | When to use |
|---|---|
| `project-overview` | Start of every dev session — repo context and design contract |
| `build-and-test` | Before building, running, or testing anything |
| `coding-conventions` | Before writing or reviewing Go code |
| `change-checklist` | Before declaring a task complete or committing |
| `add-tool` | When adding a new ADK tool in Go |
| `add-skill` | When authoring a new `registry/skills/<name>/SKILL.md` |
| `add-llm-provider` | When adding support for a new LLM backend |
| `root-wiring` | When modifying `main.go` assembly |
| `web-docs-update` | When web UI source changes need doc sync |
