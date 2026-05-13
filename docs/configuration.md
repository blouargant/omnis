# Configuration reference

All runtime configuration lives in `config/`.

Precedence for overlapping values is:

1. CLI flags
2. Environment variables
3. YAML config
4. Built-in defaults

## `config/agent.yaml`

Unified runtime config for app settings, reusable model profiles, and
agent wiring.

```yaml
skills_dir: skills
softskills_dir: softskills
app_name: agent-toolkit
token_optimization: false
bash_output_filters_dir: config/filters
mcp_config_path: config/mcp_config.yaml
permissions_config_path: config/permissions.yaml

models:
  default:
    provider: openai_compat
    model: gpt-4o-mini
    base_url: http://localhost:11434/v1
    api_key: OPENAI_API_KEY
    context_length: 128000
    input_token_price_per_million: 0.15
    output_token_price_per_million: 0.6
  premium:
    provider: anthropic
    model: claude-sonnet-4-5
    api_key: ANTHROPIC_API_KEY
    context_length: 200000
    input_token_price_per_million: 3
    output_token_price_per_million: 15

agents:
  - name: leader
    model_ref: default
  - name: investigator
    model_ref: premium
    tools: [fs, mcp]
  - name: curator
    model_ref: default
    enabled: true
```

### Models and references

`models` is a reusable catalog of model profiles. Each profile supports:

- `provider`, `model`, `base_url`, `api_key`
- `context_length`
- `input_token_price_per_million`
- `output_token_price_per_million`

Agents select one profile using `model_ref`.

If an agent omits `model_ref`, it can still specify `provider` / `model`
inline for backward compatibility.

### Bash output filtering

The `bash` tool can optionally post-process command output using declarative
YAML pipelines imported from the snip filter format.

- `token_optimization` (bool): global opt-in toggle.
- `bash_output_filters_dir` (string): directory containing `.yaml`/`.yml`
  filter rules.

When disabled (default), `bash` output is unchanged. When enabled, matching
commands are filtered before the tool's normal truncation step.

If a non-leader agent omits model connection fields, they inherit from
the leader.

For `base_url` and `api_key`, the values can be either:

- the value itself (literal), or
- an environment variable name.

When loading YAML, if `base_url` or `api_key` matches an existing env var name, the
env var value is used.

### CLI and env overrides

- `--config` selects a runtime YAML file (default: `config/agent.yaml`).
- `--provider`, `--model`, `--base-url`, and `--api-key` override
  the leader agent model selection globally.
- `--curator-enabled` (`true` or `false`) overrides the `curator`
  agent's `enabled` value.
- `YOKE_PROVIDER`, `YOKE_MODEL`, `YOKE_BASE_URL`, and
  `YOKE_API_KEY` override the leader agent model selection.
- `YOKE_CURATOR_ENABLED` overrides the `curator` agent's `enabled`
  value.

## `config/permissions.yaml`

The harness's safety envelope. Patterns are Go [`regexp`] strings
matched against the **bash command string** that is about to run (and,
in the future, against tool names).

The file has three lists, evaluated **top to bottom**:

| List           | Meaning                                                       |
|----------------|---------------------------------------------------------------|
| `always_deny`  | Hard-deny. The tool call is never executed; the model sees an error. |
| `always_allow` | Auto-allow. No prompt to the user.                            |
| `ask_user`     | Prompt the user (`y/n`) before executing.                     |

Anything matched by **none** of the three falls through to **ask**
(safe default).

### Default rules shipped

```yaml
always_deny:
  - "rm -rf /"
  - "mkfs"
  - "dd if=.* of=/dev/"
  - ":(){.*};:"          # fork bomb

always_allow:
  - "^ls( |$)"
  - "^cat "
  - "^pwd$"
  - "^echo "
  - "^head "
  - "^tail "
  - "^grep "
  - "^find .* -name"
  - "^go (build|test|vet|fmt)"
  - "^npm (test|run build)"
  - "^kubectl (get|describe|logs|top|explain) "
  - "^kubectl config (current-context|get-contexts|view)"
  - "^docker (ps|images|logs|inspect) "

ask_user:
  - "^rm "
  - "^git push"
  - "^sudo "
  - "^kubectl (apply|delete|patch|edit|scale|rollout|drain|cordon)"
  - "^docker (run|rm|rmi|exec)"
  - "^terraform (apply|destroy)"
  - "^helm (install|upgrade|uninstall)"
```

### Adding a domain

When you specialise the agent, add a matching rule pair (read-only
auto-allow + mutating ask):

```yaml
always_allow:
  - "^psql -c \"select"             # read-only Postgres
  - "^aws s3 ls"
ask_user:
  - "^psql -c \"(insert|update|delete|alter|drop)"
  - "^aws s3 (rm|cp|mv|sync) "
```

### Asker

The root binary uses `permissions.StdinAsker{}` which prompts on the
terminal. Implement `permissions.Asker` to integrate with a different
UI (web modal, Slack DM, etc.).

---

## `config/mcp_config.yaml`

Wires external [Model Context Protocol] servers as ADK toolsets. Each
entry spawns a child process and exposes its tools to the agent.

```yaml
servers:
  - name: filesystem
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem", "."]
    env: {}

  - name: kubernetes
    command: npx
    args: ["-y", "mcp-server-kubernetes"]
    env:
      KUBECONFIG: /home/you/.kube/config

  - name: postgres
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-postgres"
      - "postgresql://reader:pw@localhost/app"

  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "ghp_…"
```

### Fields

| Field     | Required | Notes                                                |
|-----------|----------|------------------------------------------------------|
| `name`    | yes      | Used as the toolset prefix in the agent.             |
| `command` | yes      | Executable to spawn (must be on `PATH`).             |
| `args`    | no       | Arguments passed to the command.                     |
| `env`     | no       | Environment variables added to the child process.    |

### Lifecycle

- Servers spawn at startup. If a server fails to start, it is logged
  and skipped — the agent continues with the rest.
- Servers are killed when the root binary exits.
- Tool names are namespaced as `<server>/<tool>` to prevent collisions.

### Security

Treat MCP servers as **untrusted code paths**: they receive arguments
from the LLM. Always pair an MCP server with `permissions.yaml` rules
gating its mutating verbs. The OOTB defaults already gate `kubectl
apply/delete`, `helm install`, `terraform apply`, etc.

[`regexp`]: https://pkg.go.dev/regexp
[Model Context Protocol]: https://modelcontextprotocol.io/

---

## Other runtime files

All runtime files are created in the working directory of the root
binary. Every component that owns mutable state is **session-scoped**
by default, so two concurrent sessions never share a file, queue or
mailbox.

| File / resource                       | Owner                | Scope               | Purpose                                                          |
|---------------------------------------|----------------------|---------------------|------------------------------------------------------------------|
| `.agent_events.log`                   | `core/events`        | global              | Append-only JSONL of every Before/After event (audit log)        |
| `.agent_memory_<user>_<session>.md`   | `internal/compress`  | per (user, session) | Compressed-context snapshot                                      |
| `.agent_tasks_<user>_<session>.json`  | `internal/tasks`     | per (user, session) | Durable task graph                                               |
| `.agent_todo_<user>_<session>.json`   | `internal/todo`      | per (user, session) | TodoWrite plan                                                   |
| in-memory `bg.Queue`                  | `internal/bg`        | per (user, session) | Background-command notification stream                           |
| mailbox name `<user>_<session>:<name>`| `internal/teammates` | per (user, session) | Inter-agent inbox key (file path or Redis channel)               |
| `.mailboxes/*.jsonl`                  | `internal/teammates` | per mailbox name    | On-disk inbox files (one per resolved mailbox name)              |

## Session isolation

The root [main.go](../main.go) declares a single `sessionSuffix(userID,
sessionID) string` helper and feeds it to every session-scoped
component so all five line up on disk and on the wire:

```go
sessionSuffix := func(userID, sessionID string) string {
    u := sanitizeID(userID)
    s := sanitizeID(sessionID)
    if u == "" { u = "anon" }
    if s == "" { s = "default" }
    return u + "_" + s
}
```

IDs are sanitised (only `[A-Za-z0-9_.-]`) before being embedded in any
filename or channel name, preventing path traversal.

### How each component scopes itself

| Component              | Constructor                                       | Hook                                  |
|------------------------|---------------------------------------------------|---------------------------------------|
| `internal/compress`    | `compress.Plugin(name, compress.Config{...})`     | `MemoryPathFunc(userID, sessionID)`   |
| `internal/tasks`       | `tasks.NewSessionScoped(default, pathFor)`        | `pathFor(userID, sessionID)`          |
| `internal/todo`        | `todo.NewSessionScoped(default, pathFor)`         | `pathFor(userID, sessionID)`          |
| `internal/bg`          | `bg.NewSessionQueues(buf)`                        | per-session `*Queue` (in-memory)      |
| `internal/teammates`   | `teammates.NewAgent(name, backend)`               | `Agent.NameFunc(userID, sessionID, name)` |

Each session-scoped struct resolves the calling session's identity from
the `tool.Context` passed to every tool invocation (`ctx.UserID()` /
`ctx.SessionID()`). When both IDs are empty (e.g. very early callbacks
before a session is registered) the constructor's default path is used
as a safe fallback.

### Example: wiring all five components in `main.go`

```go
sessionSuffix := func(u, s string) string { /* sanitise + fall-back */ }

g := tasks.NewSessionScoped("", func(u, s string) string {
    return fmt.Sprintf(".agent_tasks_%s.json", sessionSuffix(u, s))
})
store := todo.NewSessionScoped("", func(u, s string) string {
    return fmt.Sprintf(".agent_todo_%s.json", sessionSuffix(u, s))
})
q := bg.NewSessionQueues(32)

leadMailbox := teammates.NewAgent("lead", be)
leadMailbox.NameFunc = func(u, s, name string) string {
    return sessionSuffix(u, s) + ":" + name
}

compress.Plugin("compress", compress.Config{
    MemoryPathFunc: func(u, s string) string {
        return fmt.Sprintf(".agent_memory_%s.md", sessionSuffix(u, s))
    },
    LLM: llm,
})
```

### Components that are *not* session-scoped (by design)

| Component        | Why                                                                        |
|------------------|----------------------------------------------------------------------------|
| `core/events`    | Single audit log, cross-cutting observability.                             |
| `internal/cache` | Global rolling prompt-cache hit-rate stats (atomic counters).              |
| `internal/worktree` | Already isolated via the `path`/`branch` arguments the LLM supplies.    |
| `internal/skills`, `internal/mcp` | Read-only configuration loaded once at startup.           |

### Single-session demos

The `examples/sNN_*` binaries each demonstrate one component in
isolation. They use the back-compat constructors (`tasks.New("")`,
`todo.NewStore("")`, `bg.NewQueue(buf)`, `compress.Config.MemoryPath`,
`teammates.NewAgent(name, backend)` with no `NameFunc`) since they
only ever run one session at a time. Switch to the `*SessionScoped` /
`SessionQueues` / `NameFunc` variants when you embed the components in
a multi-session host (the root binary, a long-running server, etc.).

## Command-line flags

The root binary accepts a few flags, parsed **before** the launcher
subcommand (`console`, `web webui`, ...) is dispatched.

```bash
agent-toolkit [flags] [<launcher-command> [launcher-args]]
```

| Flag                | Default  | Effect                                                                                  |
|---------------------|----------|-----------------------------------------------------------------------------------------|
| `-s`, `--skills DIR`| `skills` | Directory scanned at startup for `<name>/SKILL.md` playbooks (see [skills.md](skills.md)). Pass an alternative folder to retarget the agent without touching the default `skills/` tree. |
| `--softskills DIR`  | `softskills` | Directory where curator-generated soft-skills are loaded and stored. |
| `--name NAME`       | `agent-toolkit` | Application name used by the runner/UI. |
| `--config FILE`     | `config/agent.yaml` | Runtime YAML config file path. |
| `--provider NAME`   | from config/env/defaults | Global model provider override. |
| `--model NAME`      | from config/env/defaults | Global model id override. |
| `--base-url URL`    | from config/env/defaults | Global model base URL override. |
| `--api-key VALUE`   | from config/env/defaults | Global model API key override. |
| `--curator-enabled BOOL` | from config/env/defaults | Enable/disable the auto-curator hook (`true`/`false`). |
| `-d`, `--debug`     | _off_ | Write full conversation/event payloads to the run's event log instead of partial event summaries. Debug logs can contain prompts, tool outputs, conversation history and secrets already present in context. |
| `--tui`             | _off_    | Launch the built-in [tview](https://github.com/rivo/tview) chat UI (`internal/tui`) instead of the ADK launcher. The launcher subcommand, if any, is ignored. |

The flag parser is Go's standard `flag` package, so both `-skills` and
`--skills` syntaxes work, and `=` is optional (`--skills=foo` and
`--skills foo` are equivalent).

### Examples

```bash
# Default ADK REPL with the default skills/ tree
go run . console

# ADK web UI with a custom skills directory
go run . --skills ./reviewer-skills web webui

# Built-in tview chat UI with the default skills tree
go run . --tui

# Built-in tview chat UI with a custom skills tree
go run . -s ./k8s-skills --tui
```

### `--tui` keys

| Key            | Action                              |
|----------------|-------------------------------------|
| `Enter`        | Send the current input              |
| `Ctrl-L`       | Clear the chat pane                 |
| `Ctrl-C`, `Esc`| Quit                                |

The trace pane on the left subscribes to the [event bus](../core/events/events.go)
so every model and tool invocation appears live.

## Environment variables (full list)

| Variable             | Used by               | Purpose                                          |
|----------------------|-----------------------|--------------------------------------------------|
| `YOKE_PROVIDER`   | `core/llm`            | Pick the LLM provider                            |
| `YOKE_MODEL`      | `core/llm`            | Override the per-provider default model id       |
| `YOKE_BASE_URL`   | `core/llm`            | Override the model API base URL                  |
| `YOKE_API_KEY`    | `core/llm`            | Override the model API key                       |
| `YOKE_CURATOR_ENABLED` | `agent`         | Override `features.curator_enabled` (`true`/`false`) |
| `GOOGLE_API_KEY`     | gemini provider       | Auth                                             |
| `GEMINI_API_KEY`     | gemini provider       | Auth (alias for `GOOGLE_API_KEY`)                |
| `ANTHROPIC_API_KEY`  | anthropic provider    | Auth                                             |
| `OPENAI_API_KEY`     | openai / openai_compat| Auth                                             |
| `OPENAI_BASE_URL`    | openai_compat         | API endpoint                                     |
| `REDIS_URL`          | `internal/teammates`  | Switch the mailbox backend to Redis              |
