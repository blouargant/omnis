# yoke

A **generic, vendor-neutral AI agent harness** — turn any LLM into a
specialist assistant by mounting tools, skills, and MCP servers. No
code changes required to retarget the agent at a new domain.

> **Design contract** — the agent's effective capability equals the union
> of the **tools**, **skills**, and **MCP servers** currently mounted.
> The same binary becomes a code reviewer, a Kubernetes triage assistant,
> a DBA helper, or a release engineer purely by changing what is mounted.

After every session a two-stage **reflection pipeline** (a deterministic
heuristic + an optional LLM reflector) tags the soft-skills the session
loaded as helpful, harmful, or neutral, extracts a key insight, and
hands the verdict to a **curator** that distills a new soft-skill or
prunes a misleading one. The corpus improves itself over time without
manual authoring.

---

## Table of contents

1. [Screencasts](#screencasts)
2. [Quick start](#quick-start)
3. [Choosing an LLM provider](#choosing-an-llm-provider)
4. [Running the server](#running-the-server)
5. [Specialising the agent](#specialising-the-agent)
6. [Documentation](#documentation)
7. [For developers](#for-developers)

---

## Screencasts

### Chat

A simple chat session with the web UI — streaming responses, session
history, and file attachments.

https://github.com/user-attachments/assets/ba54d363-40ab-444d-ba8b-a550f03651fa


---

### Teammate — cross-session communication

Two sessions talking to each other via the built-in mailbox: one session
delegates a sub-task and the other picks it up, processes it, and sends
the result back.

https://github.com/user-attachments/assets/e6047770-878e-44ea-95f5-01ff4f2198db

---

### Helper — answering questions and discovering skills and agents

Using the dedicated **Helper** squad to answer questions about yoke from its
own documentation (quoting the source), and to browse remote GitHub or Gitea
registries for community skills and agents, then installing them directly from
the web UI.

https://github.com/user-attachments/assets/53b9e83d-54da-4bc5-bc40-b7a99db7cc6c

---

### Settings panels

A tour of the Settings area: agent configuration, model profiles, squad
composition, MCP server wiring, and permission rules — all editable
without touching a config file.

https://github.com/user-attachments/assets/6c5a4897-2f7b-436c-8ae4-5feacba2621d


---

### Themes

How to switch the web UI theme and persist the choice across sessions.


https://github.com/user-attachments/assets/aa4af480-5ae8-4b5b-9ea4-c5c84ddd24b7



---

## Quick start

### 1. Install

Download the package for your platform from the [Releases](https://github.com/blouargant/yoke/releases) page and install it:

```bash
# Debian / Ubuntu
sudo dpkg -i yoke_*_linux_amd64.deb

# Red Hat / Fedora / SUSE
sudo rpm -i yoke_*_linux_amd64.rpm

# Any Linux (tarball)
tar xzf yoke_*_linux_amd64.tar.gz -C /usr/local/bin yoke yoke-server
```

The package installs two binaries:
- **`yoke-server`** — HTTP API + web chat UI
- **`yoke`** — CLI / REPL / TUI

### 2. Configure your LLM provider

Edit `/etc/yoke/agents.json` (or set environment variables) to point at
your provider. The fastest path is a couple of env vars:

```bash
export YOKE_PROVIDER=anthropic          # or gemini, openai, openai_compat
export ANTHROPIC_API_KEY=sk-ant-…
```

See [Choosing an LLM provider](#choosing-an-llm-provider) for all options.

### 3. Start

**Web UI** — set a bearer token and launch the server:

```bash
# OPTIONAL: create a auth token to access the Web UI
# export YOKE_SERVER_TOKEN=$(openssl rand -hex 32)
yoke-server                             # → http://localhost:8080
```

Open <http://localhost:8080>, paste the token when prompted, and start chatting.

**Terminal UI** — no token required:

```bash
yoke tui
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

## Running the server

The web UI server exposes the agent over a JSON+SSE API and serves the
chat interface from `/usr/share/yoke/web/` (set automatically by the
package via `/etc/profile.d/yoke.sh`).

A bearer token is mandatory. Set it in `/etc/yoke/server.yaml`:

```yaml
# /etc/yoke/server.yaml
token: "<your-token-here>"
```

Or pass it as an environment variable:

```bash
export YOKE_SERVER_TOKEN=$(openssl rand -hex 32)
yoke-server
```

The server listens on `:8080` by default. Override with
`YOKE_SERVER_ADDR` or the `addr` key in `server.yaml`. See
[docs/configuration.md](docs/configuration.md) for the full reference.

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
2. **MCP servers** (`config/mcp_config.json`) — external tool surfaces
   (filesystem, Postgres, Kubernetes, GitHub, …).
3. **Permissions** (`config/permissions.json`) — auto-allow read-only
   verbs, gate mutations with `ask_user`, hard-deny destructive ones.

### Example: turn the harness into a Kubernetes diagnostician

```json
// config/mcp_config.json
{
  "servers": [
    {
      "name": "kubernetes",
      "command": "npx",
      "args": ["-y", "mcp-server-kubernetes"],
      "env": {"KUBECONFIG": "/home/you/.kube/config"}
    }
  ]
}
```

The `skills/k8s-triage/SKILL.md` is already shipped as an example. Ask
the agent:

```
> diagnose why pods in namespace payments are crash-looping
```

The agent will discover the new MCP tools, match the question to the
`k8s-triage` skill, and follow its procedure (confirm context → snapshot
state → classify failure → propose one dry-run fix).

See [docs/specialising.md](docs/specialising.md) for the full recipe.

---

## Documentation

| File                                          | Topic                                             |
|-----------------------------------------------|---------------------------------------------------|
| [docs/architecture.md](docs/architecture.md)  | Component map, data flow, plugin lifecycle        |
| [docs/methodology.md](docs/methodology.md)    | The Claude Code 7-step operating method           |
| [docs/context-management.md](docs/context-management.md) | How context compression works + session decision log |
| [docs/semantic-recall.md](docs/semantic-recall.md) | Embedder, vector indexes, and cross-session precedents |
| [docs/providers.md](docs/providers.md)        | Configuring Gemini / Anthropic / OpenAI / compat  |
| [docs/specialising.md](docs/specialising.md)  | How to retarget the agent at a new domain         |
| [docs/skills.md](docs/skills.md)              | Authoring `SKILL.md` files                        |
| [docs/configuration.md](docs/configuration.md)| Full configuration reference                      |
| [docs/extending.md](docs/extending.md)        | Adding new tools, sub-agents, squads and plugins  |

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

---

## For developers

This section covers building from source, the CLI/TUI modes, the
project layout, and the examples catalog.

### Installation

Requires Go ≥ 1.25.

```bash
git clone https://github.com/blouargant/yoke
cd yoke
go build ./...
```

### Usage modes

| Mode    | Invocation                            | When to use                                    |
|---------|---------------------------------------|------------------------------------------------|
| CLI     | `yoke [prompt…]`, `yoke run [prompt]` | REPL when stdin is a TTY; one-shot when piped or given a prompt arg. Best for scripting, CI, quick questions. |
| TUI     | `yoke tui`                            | Interactive tview interface with live trace pane and streaming markdown. Best for sustained terminal sessions. |
| Server  | `yoke-server` (separate binary)       | HTTP + SSE API plus the web chat UI. Best for multi-user or remote access. |

CLI quick start:

```bash
export YOKE_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-…

go run .                                   # interactive REPL
go run . "summarize the architecture"      # one-shot
echo "explain main.go" | go run .          # piped one-shot
go run . tui                               # TUI
```

### Command-line flags

| Flag                  | Default    | Effect                                                                  |
|-----------------------|------------|-------------------------------------------------------------------------|
| `-s`, `--skills DIR`  | `skills`   | Directory scanned for `<name>/SKILL.md` playbooks at startup.           |
| `--softskills DIR`    | `softskills` | Directory of curator-generated soft-skills.                           |
| `--config PATH`       | `config/agents.json` | Runtime JSON config path.                                     |
| `--provider NAME`     | _(config)_ | Global model provider override (e.g. `anthropic`).                      |
| `--model NAME`        | _(config)_ | Global model override.                                                  |
| `--base-url URL`      | _(config)_ | API endpoint override.                                                  |
| `--api-key KEY`       | _(config/env)_| API-key override.                                                    |
| `--curator-enabled BOOL` | _(env)_ | Enable/disable the auto-curator hook.                                  |
| `--name NAME`         | `yoke`     | Application name (used in runner + session metadata).                   |
| `-d`, `--debug`       | _off_      | Write full conversation/event payloads to the event log.               |

Flags must come **before** the subcommand or prompt:

```bash
go run . --skills ./my-skills tui
go run . -d "what does main.go do?"
```

### Build commands

```bash
make build              # bin/yoke + bin/yoke-server (host platform)
make examples           # opt-in: build all examples under bin/
make release            # cross-platform raw binaries → dist/
make fmt && make vet    # code quality
make test               # unit tests
make env-tests          # LLM integration tests (requires .env with API keys)
```

### Examples

There are 30 single-component demos under `examples/sNN_*/`, ordered
from the simplest (a bare loop) to the most complex (multi-agent and
distributed):

```bash
make examples                  # opt-in build
go run ./examples/s21_skills   # run one directly
```

See [docs/examples-catalog.md](docs/examples-catalog.md), or open
[examples/index.ipynb](examples/index.ipynb) for the GoNB-based
learning path (setup in [docs/notebooks.md](docs/notebooks.md)).

### Project layout

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
│   ├── permissions/             # JSON-driven permission plugin
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
│   ├── softskills/              # curator-distilled procedures + reflectors (heuristic + LLM)
│   ├── mcp/                     # MCP config loader
│   └── a2a/                     # A2A protocol client + tool wiring
├── examples/sNN_*/              # single-component demos (opt-in via `make examples`)
├── skills/                      # specialisation playbooks
├── softskills/                  # curator output (incl. _stats.json sidecar + wrap-session built-in)
├── config/                      # agents.json, permissions.json, mcp_config.json
├── doc.go                       # package-level overview
└── docs/                        # extended documentation
```
