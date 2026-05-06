# agent-toolkit

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
export GOAGENT_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-…

# Interactive REPL
go run . console

# Local web UI
go run . web webui
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
git clone https://github.com/blouargant/agent-toolkit
cd agent-toolkit
go build ./...
```

---

## Choosing an LLM provider

Set `GOAGENT_PROVIDER` (default: `openai_compat`):

| Provider        | Auth env                                         | Default model        |
|-----------------|--------------------------------------------------|----------------------|
| `gemini`        | `GOOGLE_API_KEY` *or* `GEMINI_API_KEY`           | `gemini-2.5-flash`   |
| `anthropic`     | `ANTHROPIC_API_KEY`                              | `claude-sonnet-4-5`  |
| `openai`        | `OPENAI_API_KEY`                                 | `gpt-4o-mini`        |
| `openai_compat` | `OPENAI_API_KEY` (optional) + `OPENAI_BASE_URL`  | `gpt-4o-mini`        |

Override the model with `GOAGENT_MODEL`. Examples:

```bash
# Local Ollama
export GOAGENT_PROVIDER=openai_compat
export OPENAI_BASE_URL=http://localhost:11434/v1
export GOAGENT_MODEL=llama3.1:70b

# Groq
export GOAGENT_PROVIDER=openai_compat
export OPENAI_BASE_URL=https://api.groq.com/openai/v1
export OPENAI_API_KEY=gsk_…
export GOAGENT_MODEL=llama-3.3-70b-versatile
```

See [docs/providers.md](docs/providers.md) for details.

---

## Running the all-in-one binary

[main.go](main.go) is the reference launcher; it wires
every component together and hands control to ADK's `full` launcher.

```bash
go run . console     # ADK interactive REPL (default if no command)
go run . web webui   # ADK web UI
go run . --tui       # built-in tview chat UI
```

### Command-line flags

| Flag                | Default    | Effect                                                                 |
|---------------------|------------|------------------------------------------------------------------------|
| `-s`, `--skills DIR`| `skills`   | Directory scanned for `<name>/SKILL.md` playbooks at startup.          |
| `-d`, `--debug`     | _off_      | Write full conversation/event payloads to the run's event log instead of partial event summaries. Debug logs can contain prompts, tool outputs, conversation history and secrets already present in context. |
| `--tui`             | _off_      | Launch the built-in [tview](https://github.com/rivo/tview) chat UI instead of an ADK launcher subcommand. Trace pane on the left, streaming chat + input box on the right. Keys: `Enter` send, `Ctrl-L` clear, `Ctrl-C` / `Esc` quit. |

Flags must come **before** any launcher subcommand:

```bash
go run . --skills ./my-skills console
go run . -s ./reviewer-skills --tui
```

See [docs/configuration.md](docs/configuration.md#command-line-flags) for the full reference.

There are also 23 single-component demos under `examples/sNN_*/` that mirror
the article's phases. See [docs/examples-catalog.md](docs/examples-catalog.md).

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
go run . console
> diagnose why pods in namespace payments are crash-looping
```

The lead agent will discover the new MCP tools, match the user's
question to the `k8s-triage` skill, and follow its procedure (confirm
context → snapshot state → classify failure → propose one dry-run fix).

See [docs/specialising.md](docs/specialising.md) for the full recipe.

---

## Project layout

```
agent-toolkit/
├── cmd/
│   ├── full/                    # all-in-one launcher (REPL + web)
│   └── sNN_*/                   # 23 single-component demos (one per article phase)
├── core/
│   ├── agentkit/                # central agent constructor + system prompt
│   ├── llm/                     # multi-provider model dispatcher
│   ├── tools/                   # file / bash / grep / glob / revert
│   ├── permissions/             # YAML-driven permission plugin
│   ├── events/                  # plugin-friendly event bus + file logger
│   └── stream/                  # streaming helpers
├── internal/
│   ├── todo/                    # TodoWrite tools + store
│   ├── tasks/                   # durable task graph
│   ├── bg/                      # background command queue
│   ├── worktree/                # git worktree isolation tools
│   ├── teammates/               # mailbox / FSM-based inter-agent comms
│   ├── compress/                # context compression plugin
│   ├── cache/                   # prompt-cache stats plugin
│   ├── skills/                  # skill loader (skilltoolset wrapper)
│   └── mcp/                     # MCP config loader
├── skills/                      # specialisation playbooks
├── config/                      # permissions.yaml, mcp_config.yaml
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
