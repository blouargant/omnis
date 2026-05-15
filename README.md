# yoke

A **generic, vendor-neutral agent harness** built in Go, inspired by the
methodology proven by Anthropic's **Claude Code**. The harness encodes a
*method*; specialisation comes from what you mount.

> **Design contract** — the agent's effective capability on any given run
> equals the union of the **tools**, **skills** and **MCP servers**
> currently mounted. The same binary becomes a code reviewer, a
> Kubernetes triage assistant, a DBA helper or a release engineer purely
> by changing what is mounted. **No code change is required to retarget
> the agent at a new domain.**

Built on top of [google.golang.org/adk](https://pkg.go.dev/google.golang.org/adk)
for the agent loop, session, plugins and runner, with first-class
support for Anthropic, OpenAI and any OpenAI-compatible endpoint via a
small in-tree adapter (no extra SDKs).

---

## Table of contents

1. [Quick start](#quick-start)
2. [Installation](#installation)
3. [Choosing an LLM provider](#choosing-an-llm-provider)
4. [Running the all-in-one binary](#running-the-all-in-one-binary)
5. [Specialising the agent](#specialising-the-agent)
6. [Project layout](#project-layout)
7. [Documentation](#documentation)

---

## Quick start

```bash
# Pick your provider + key (default provider is openai_compat)
export YOKE_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-…

# Interactive REPL (auto-detected when stdin is a TTY)
go run .

# One-shot prompt
go run . "summarize the architecture of this repo"
echo "explain main.go" | go run .

# Built-in TUI (tview chat interface)
go run . tui

# HTTP server + web chat UI (separate binary; see "Running the server")
export YOKE_SERVER_TOKEN=$(openssl rand -hex 32)
make run-server   # http://localhost:8080
```

Out of the box the agent has:

- File tools (`read`, `write`, `grep`, `glob`, `revert`, `bash`)
- Planning (`todo_write`, `task_create`/`task_update`/`task_list`)
- Background commands queue (`bash_background`)
- Git worktree isolation (`worktree_create`/`_remove`/`_merge`)
- Two generic sub-agents (`investigator`, `summariser`) reachable as tools
- Skills auto-loaded from `./skills/`
- MCP servers loaded from `config/mcp_config.yaml`
- Permission gating from `config/permissions.yaml`
- Event logging to `.agent_events.log`
- Per-session state isolation: each `(user, session)` pair gets its own
  task graph (`.agent_tasks_<u>_<s>.json`), todo plan
  (`.agent_todo_<u>_<s>.json`), compressed memory
  (`.agent_memory_<u>_<s>.md`), background-notification queue and
  mailbox namespace — concurrent sessions never share state. See
  [docs/configuration.md#session-isolation](docs/configuration.md#session-isolation).

---

## Installation

Requires Go ≥ 1.25.

```bash
git clone https://github.com/blouargant/yoke
cd yoke
go build ./...
```

---

## Choosing an LLM provider

Set `YOKE_PROVIDER` (default: `openai_compat`):

| Provider        | Auth env                                         | Default model        |
|-----------------|--------------------------------------------------|----------------------|
| `gemini`        | `GOOGLE_API_KEY` *or* `GEMINI_API_KEY`           | `gemini-2.5-flash`   |
| `anthropic`     | `ANTHROPIC_API_KEY`                              | `claude-sonnet-4-5`  |
| `openai`        | `OPENAI_API_KEY`                                 | `gpt-4o-mini`        |
| `openai_compat` | `OPENAI_API_KEY` (optional) + `OPENAI_BASE_URL`  | `gpt-4o-mini`        |

Override the model with `YOKE_MODEL`. Examples:

```bash
# Local Ollama
export YOKE_PROVIDER=openai_compat
export OPENAI_BASE_URL=http://localhost:11434/v1
export YOKE_MODEL=llama3.1:70b

# Groq
export YOKE_PROVIDER=openai_compat
export OPENAI_BASE_URL=https://api.groq.com/openai/v1
export OPENAI_API_KEY=gsk_…
export YOKE_MODEL=llama-3.3-70b-versatile
```

See [docs/providers.md](docs/providers.md) for details.

---

## Running the binary

`yoke` supports exactly three usage modes:

| Mode    | Invocation                            | When to use                                    |
|---------|---------------------------------------|------------------------------------------------|
| CLI     | `yoke [prompt…]`, `yoke run [prompt]` | REPL when stdin is a TTY; one-shot when a prompt arg or piped input is given. Best for scripting, quick questions, CI. |
| TUI     | `yoke tui`                            | Interactive tview interface with live trace pane, streaming markdown and slash-command shortcuts. Best for sustained terminal sessions. |
| Server  | `yoke-server` (separate binary)       | HTTP + SSE API plus the [web/](web/) chat UI. Best for multi-user, remote access, or integrations. See [Running the server](#running-the-server). |

Plus one auxiliary subcommand:

- `yoke curate …` — replay the soft-skills curator one-shot against an existing session's audit + statelog files.
- `yoke version` / `yoke help` — version info and usage reference.

### Command-line flags

| Flag                  | Default    | Effect                                                                  |
|-----------------------|------------|-------------------------------------------------------------------------|
| `-s`, `--skills DIR`  | `skills`   | Directory scanned for `<name>/SKILL.md` playbooks at startup.           |
| `--softskills DIR`    | `softskills` | Directory of curator-generated soft-skills.                           |
| `--config PATH`       | `config/agent.yaml` | Runtime YAML config path. Error out if the explicit path is missing. |
| `--provider NAME`     | _(yaml)_   | Global model provider override (e.g. `anthropic`).                      |
| `--model NAME`        | _(yaml)_   | Global model override.                                                  |
| `--base-url URL`      | _(yaml)_   | API endpoint override.                                                  |
| `--api-key KEY`       | _(yaml/env)_| API-key override.                                                      |
| `--curator-enabled BOOL` | _(env)_ | Enable/disable the auto-curator hook.                                  |
| `--name NAME`         | `yoke`     | Application name (used in runner + session metadata).                   |
| `-d`, `--debug`       | _off_      | Write full conversation/event payloads to the event log. Logs can contain prompts, tool outputs and secrets already present in context. |

Flags must come **before** the subcommand or prompt:

```bash
go run . --skills ./my-skills tui
go run . -d "what does main.go do?"
```

See [docs/configuration.md](docs/configuration.md#command-line-flags) for the full reference.

There are single-component demos under `examples/sNN_*/` that mirror the
article's phases — not part of the production build, but useful for
learning each component in isolation:

```bash
make examples            # opt-in: builds all demos
go run ./examples/s05_skills
```

See [docs/examples-catalog.md](docs/examples-catalog.md).

---

## Running the server

The repository ships a standalone HTTP server in [server/](server/) that
exposes the lead agent over a JSON+SSE API and serves the vanilla-JS chat
UI in [web/](web/). This is the **custom chat UI** with prompt-history,
mailbox push, file attachments and the debug overlay described above.

A bearer token is mandatory — the server refuses to start without one:

```bash
export YOKE_SERVER_TOKEN=$(openssl rand -hex 32)
export ANTHROPIC_API_KEY=sk-ant-…        # or any other provider key
```

Optional env vars: `YOKE_SERVER_ADDR` (default `:8080`),
`YOKE_WEB_DIR` (default `web`), `YOKE_CONFIG_PATH`,
`YOKE_SKILLS_DIR`, `YOKE_SOFTSKILLS_DIR`, `YOKE_DEBUG`. See
[server/main.go](server/main.go) for the full list.

### Dev mode (no build step)

Fast iteration — rebuilds on every invocation, picks up Go source changes
immediately, and serves `web/` from the working tree so front-end edits
are live-reload on browser refresh:

```bash
make run-server                          # equivalent to `go run ./server`
# or directly:
go run ./server
```

Then open <http://localhost:8080> and paste the token when prompted.

### Compiled binary

For production-ish use, build once and run the resulting binary:

```bash
make clean all                           # → bin/yoke, bin/yoke-server
./bin/yoke-server
```

`make clean all` removes `bin/` and `dist/` then rebuilds the root
binary (`yoke`) and the **`yoke-server`** binary. Examples are no
longer part of the default build — run `make examples` to build them
on demand. Run `bin/yoke-server` from the repository root so it can
find the default `web/` and `config/` directories — or set
`YOKE_WEB_DIR` / `YOKE_CONFIG_PATH` to point at absolute paths if you
copy the binary elsewhere.

> Enable the debug overlay in the browser by appending `?debug=1` to the
> URL (or `localStorage.agent_toolkit_debug = "1"`). The overlay reports
> live per-turn client + server streaming metrics — see the CLAUDE.md
> "Web UI debug mode" section for the field reference.

---

## Specialising the agent

You **never edit Go code** to change the agent's domain. Instead you
mount a different combination of:

1. **Skills** (`skills/<name>/SKILL.md`) — Markdown playbooks loaded
   lazily via the `load_skill` tool. The OOTB harness ships:
   - `review` — generic review/audit playbook (any artefact)
   - `agent-builder` — checklist for scaffolding a new specialist
   - `pdf` — PDF extraction
   - `k8s-triage` — example domain specialisation (Kubernetes triage)
2. **MCP servers** (`config/mcp_config.yaml`) — external tool surfaces
   (filesystem, Postgres, Kubernetes, GitHub, …).
3. **Permissions** (`config/permissions.yaml`) — auto-allow read-only
   verbs, gate mutations with `ask_user`, hard-deny destructive ones.

### Example: turn the harness into a Kubernetes diagnostician

```yaml
# config/mcp_config.yaml
servers:
  - name: kubernetes
    command: npx
    args: ["-y", "mcp-server-kubernetes"]
    env: { KUBECONFIG: /home/you/.kube/config }
```

The `skills/k8s-triage/SKILL.md` is already shipped as an example. Run:

```bash
go run .
> diagnose why pods in namespace payments are crash-looping
```

The lead agent will discover the new MCP tools, match the user's
question to the `k8s-triage` skill, and follow its procedure (confirm
context → snapshot state → classify failure → propose one dry-run fix).

See [docs/specialising.md](docs/specialising.md) for the full recipe.

---

## Project layout

```
yoke/
├── main.go                      # root binary entry point: CLI / TUI / curate dispatch
├── curate.go                    # `yoke curate` one-shot subcommand
├── server/                      # separate binary: HTTP + SSE API + web UI
├── web/                         # vanilla-JS chat UI assets served by server/
├── agent/                       # NewAgent() — single wiring entry point
├── core/
│   ├── agentkit/                # central agent constructor + system prompt
│   ├── llm/                     # multi-provider model dispatcher
│   ├── tools/                   # file / bash / grep / glob / revert
│   ├── permissions/             # YAML-driven permission plugin
│   ├── events/                  # plugin-friendly event bus + file logger
│   └── stream/                  # streaming helpers
├── internal/
│   ├── cli/                     # stdio REPL + one-shot frontend
│   ├── tui/                     # tview chat frontend
│   ├── todo/                    # TodoWrite tools + store
│   ├── tasks/                   # durable task graph
│   ├── bg/                      # background command queue
│   ├── worktree/                # git worktree isolation tools
│   ├── teammates/               # mailbox / FSM-based inter-agent comms
│   ├── compress/                # context compression plugin
│   ├── cache/                   # prompt-cache stats plugin
│   ├── skills/                  # skill loader (skilltoolset wrapper)
│   ├── softskills/              # curator-distilled procedures
│   └── mcp/                     # MCP config loader
├── examples/sNN_*/              # single-component demos (opt-in via `make examples`)
├── skills/                      # specialisation playbooks
├── softskills/                  # curator output
├── config/                      # agent.yaml, permissions.yaml, mcp_config.yaml
├── doc.go                       # package-level overview
└── docs/                        # extended documentation
```

---

## Documentation

| File                                          | Topic                                             |
|-----------------------------------------------|---------------------------------------------------|
| [docs/architecture.md](docs/architecture.md)  | Component map, data flow, plugin lifecycle        |
| [docs/methodology.md](docs/methodology.md)    | The Claude Code 7-step operating method           |
| [docs/context-management.md](docs/context-management.md) | How context compression works + session decision log |
| [docs/providers.md](docs/providers.md)        | Configuring Gemini / Anthropic / OpenAI / compat  |
| [docs/specialising.md](docs/specialising.md)  | How to retarget the agent at a new domain         |
| [docs/skills.md](docs/skills.md)              | Authoring `SKILL.md` files                        |
| [docs/configuration.md](docs/configuration.md)| `permissions.yaml` and `mcp_config.yaml` reference|
| [docs/examples-catalog.md](docs/examples-catalog.md)    | The 23 single-component demo binaries             |
| [docs/k8s-context-compression-e2e.md](docs/k8s-context-compression-e2e.md) | Real-world Kubernetes context-compression validation |
| [docs/extending.md](docs/extending.md)        | Adding new tools, sub-agents and plugins          |

---

## License

Released under the [MIT License](LICENSE).

## Acknowledgements

- The article *"Building Claude Code with Harness Engineering"* by
  [Level Up Coding](https://levelup.gitconnected.com/building-claude-code-with-harness-engineering-d2e8c0da85f0)
- [Anthropic Claude Code](https://www.anthropic.com/) for the
  methodology this harness encodes.
- [Google ADK for Go](https://pkg.go.dev/google.golang.org/adk) for the
  underlying agent loop.
