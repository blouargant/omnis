# Configuration reference

Runtime configuration is resolved through a **3-layer search chain**:
`.agents/` (or `agents/` as a dotless alias; both participate when both
exist, `.agents/` first) → `$HOME/.yoke/` (per-user) →
`/etc/yoke/` (system). Agent and skill registries live one level deeper
inside each layer, at `registry/agents/` and `registry/skills/`.
Each layer can hold any config file; the first existing file wins (file-level
override, not merge). User-edited config is **layer-aware** on save: edits to
a file that lives in (or whose content references resources from) the local
layer are routed back to that local layer instead of forking into
`$HOME/.yoke/`.

Precedence for overlapping values is:

1. CLI flags
2. Environment variables
3. JSON config
4. Built-in defaults

## `agents.json`

Top-level runtime config: app settings, the list of enabled agent names,
and squad composition. Model profiles live in their own `models.json`
file (see [Models and references](#models-and-references) below).
Per-agent details live in `registry/agents/<name>/` — see
[Agent registry](#agent-registry).

```json
{
  "skills_dir": "skills",
  "softskills_dir": "softskills",
  "app_name": "yoke",
  "token_optimization": false,
  "bash_output_filters_dir": ".agents/filters",
  "mcp_config_path": ".agents/mcp_config.json",
  "permissions_config_path": ".agents/permissions.json",

  "agents": ["leader", "investigator", "web_agent", "summariser", "curator"],

  "squads": [
    {
      "name": "default",
      "description": "General-purpose squad with the full team.",
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

> **Migrating from older versions:** configurations that still declare a
> top-level `"models": { … }` block inside `agents.json` are rejected at
> startup with a clear error. Move the block to `models.json` as
> documented below.

The `agents` field is a list of agent *names*. Each name must
correspond to a directory under `registry/agents/<name>/` containing
the agent's definition. The resolver loads each agent from its own
file at startup.

### Agent registry

Agents live in `registry/agents/<name>/`:

```
registry/agents/
├── leader/
│   ├── agent.json
│   └── instruction.md
├── investigator/
│   ├── agent.json
│   └── instruction.md
├── skill_editor/
│   ├── agent.json
│   └── instruction.md
└── default.md          # fallback system instruction
```

Each `agent.json` is a single `AgentEntry`:

```json
{
  "name": "investigator",
  "description": "Read-only evidence gatherer.",
  "enabled": true,
  "builtin": false,
  "model_ref": "premium",
  "tools": ["fs", "mcp"]
}
```

Common fields:

| Field | Purpose |
|---|---|
| `name` | Agent identifier (must match directory name). |
| `description` | Short summary shown in the UI and exposed to the leader as the sub-agent tool description. |
| `enabled` | When `false`, the agent is excluded from squads and the leader's tool list. |
| `leader` | When `true`, this agent can be selected as a squad leader. |
| `builtin` | When `true`, marks the agent as shipped with yoke. The web UI groups built-in agents separately from user-added (custom) ones. Built-ins: `leader`, `skill_editor`, `helper`, `summariser`, `curator`. |
| `model_ref` | References a key in `models` for provider/model/base_url/api_key. |
| `tools` | List of tool group names mounted on this agent (`fs`, `mcp`, `web`, `skills`, `softskills`, `calc`, `registries`, ...). |
| `skills_dir` | Optional per-agent skills directory (overrides the global one). |
| `max_instances` | Max parallel invocations the leader may launch in one tool call. `1` (default) keeps the classic one-at-a-time tool. `> 1` exposes a batch/fan-out tool — the leader passes a `tasks` array (up to `max_instances` independent jobs) that run concurrently and return one result per task. Per-agent; JSON-only for now (no web-UI field). |

`instruction.md` is the agent's system prompt. If the file is missing,
the agent falls back to `registry/agents/default.md`.

The registry directory uses the same 3-layer lookup as config files:
`.agents/registry/agents` (and `agents/registry/agents` when that alias
directory exists), `$HOME/.yoke/registry/agents`, then
`/etc/yoke/registry/agents`. First existing directory wins.

The web UI Settings → Agent panel exposes both files: agent fields in
the form, instruction text in the **Instruction Set** block. Saving
writes `agent.json` and `instruction.md` separately.

### Squads — per-session agent groups

A **squad** is a named group `{ name, leader, members[] }` composed from
the `agents:` catalogue above. Each chat session selects one squad at
creation; the runtime resolves a separate leader + sub-agent tree per
squad and binds the session to that tree for the duration of the
conversation. Squads only *reference* agents — skills, tools and MCP
servers stay attached to the agent definitions, so two squads sharing a
member also share its wiring (and the MCP pool dedups any subprocess
backing it).

Rules enforced at resolution time:

- A squad named `default` is always present. When `squads:` is missing
  or contains only non-default entries, the resolver synthesises a
  `default` from the enabled agents (everything except `curator`).
- `leader` and every `members[i]` must reference an enabled agent.
- `curator` cannot be a squad member — it stays a single process-wide
  hook listening across all squads.
- Squad names are case-insensitive and must be unique within the file.

The web UI exposes a **Squads** sub-tab in Settings → Agent (leader
dropdown, member checkboxes, description, add/delete). A picker next to
the New Chat button selects which squad each new session uses; the
choice is persisted on the session and survives server restarts. Hot
reload picks up squad edits without a process restart.

### Models and references

Model profiles live in their own `models.json` file, resolved through the
same 3-layer search chain as the other config files (`.agents/`,
`$YOKE_HOME`, `/etc/yoke`). The file has two top-level sections:

```json
{
  "providers": {
    "openai-prod": {
      "kind": "openai_compat",
      "base_url": "OPENAI_BASE_URL",
      "api_key":  "OPENAI_API_KEY"
    },
    "anthropic-prod": {
      "kind": "anthropic",
      "api_key": "ANTHROPIC_API_KEY"
    }
  },
  "models": {
    "default": {
      "provider_ref": "openai-prod",
      "model": "gpt-4o-mini",
      "context_length": 128000,
      "input_token_price_per_million": 0.15,
      "output_token_price_per_million": 0.6
    },
    "premium": {
      "provider_ref": "anthropic-prod",
      "model": "claude-sonnet-4-6",
      "context_length": 200000,
      "input_token_price_per_million": 3,
      "output_token_price_per_million": 15
    }
  }
}
```

A **provider** groups credentials and an endpoint:

- `kind` — one of `anthropic`, `openai`, `openai_compat`, `gemini`.
- `base_url`, `api_key` — endpoint and credential, resolved as env-var
  names first.

A **model** profile supports:

- `provider_ref` — name of a provider whose `kind`, `base_url`, and
  `api_key` are inherited.
- Inline `provider`, `model`, `base_url`, `api_key` — override the
  inherited values when set.
- `context_length`, `input_token_price_per_million`,
  `cached_input_token_price_per_million`,
  `output_token_price_per_million`.

Agents reference a model by name via `model_ref` in their
`registry/agents/<name>/agent.json`. If an agent omits `model_ref`, it
can still specify `provider` / `model` inline.

If a non-leader agent omits model connection fields, they inherit from
the leader.

### Bash output filtering

The `bash` tool can optionally post-process command output using declarative
JSON pipelines imported from the snip filter format.

- `token_optimization` (bool): global opt-in toggle.
- `bash_output_filters_dir` (string): directory containing `.json`
  filter rules.

When disabled (default), `bash` output is unchanged. When enabled, matching
commands are filtered before the tool's normal truncation step.

For `base_url` and `api_key`, the values can be either:

- the value itself (literal), or
- an environment variable name.

When loading the config, if `base_url` or `api_key` matches an existing env
var name, the env var value is used.

### CLI and env overrides

- `--config` selects a runtime JSON file (default: resolved from the search chain — `.agents/agents.json`, then `$HOME/.yoke/agents.json`).
- `--provider`, `--model`, `--base-url`, and `--api-key` override
  the leader agent model selection globally.
- `--curator-enabled` (`true` or `false`) overrides the `curator`
  agent's `enabled` value.
- `YOKE_PROVIDER`, `YOKE_MODEL`, `YOKE_BASE_URL`, and
  `YOKE_API_KEY` override the leader agent model selection.
- `YOKE_CURATOR_ENABLED` overrides the `curator` agent's `enabled`
  value.
- `YOKE_CURATOR_MIN_TURNS` — minimum number of model responses before
  non-forced curation is considered (default: `3`). Sessions shorter
  than this are skipped automatically.
- `YOKE_CURATOR_MIN_SUB_AGENT_CALLS` — minimum total sub-agent
  invocations required when no explicit decision was recorded (default:
  `2`). Together with `MIN_TURNS`, this forms the pre-flight gate that
  avoids spinning up the curator LLM for trivial sessions.
- `YOKE_CURATOR_IDLE_TIMEOUT` — duration (e.g. `30m`, `2h`) after
  which an idle Web UI session automatically triggers curator evaluation.
  `0` or unset disables the idle trigger (default: disabled). The Web UI
  never fires `EventSessionEnd`, so this is the primary auto-curation
  path for server deployments. After firing, the session is marked
  **Harvested** and skipped by all subsequent scans until the user sends
  a new message — no repeated evaluations of long-idle sessions.

## `permissions.json`

The harness's safety envelope, using **Claude Code's permission nomenclature**.
The file holds a `permissions` object with three rule tiers plus a `defaultMode`,
evaluated **deny → ask → allow** (first match wins; deny always takes
precedence):

| Tier    | Meaning                                                       |
|---------|---------------------------------------------------------------|
| `deny`  | Hard-deny. The tool call is never executed; the model sees an error. |
| `ask`   | Prompt the user before executing.                            |
| `allow` | Auto-allow. No prompt to the user.                           |

Anything matched by **none** of the three falls through to the mode default
(in `default` mode that is **ask** — the safe default).

Each rule is a `Tool(specifier)` string — `Bash(npm run *)`, `Read(.env)`,
`Edit(/src/**)`, `mcp__server__tool`, `Agent(Name)`, or a bare tool name — with
Claude Code semantics (Bash glob + compound-command splitting + wrapper
stripping + a built-in read-only allowlist; gitignore path anchors). yoke adds
an object form `{"rule": "...", "reason": "...", "cwd": "..."}` and a `/regex/`
escape hatch (or `{"regex": "...", "tools": ["Bash"]}`) matched against
`toolName <json args>`.

Old-format files (top-level `always_deny`/`always_allow`/`ask_user`) are
**auto-converted on load**, with a `.bak` backup kept. Convert manually with
`yoke permissions convert -w permissions.json`, or import a Claude Code
`settings.json` with `yoke permissions import settings.json`.

### Default rules shipped (excerpt)

```json
{
  "permissions": {
    "defaultMode": "default",
    "deny": [
      {"regex": "\\bmkfs(\\.[a-z0-9]+)?\\b", "tools": ["Bash"], "reason": "creating a filesystem destroys data"},
      "Read(.ssh/id_*)"
    ],
    "allow": [
      "Read",
      "Bash(go build *)",
      "Bash(go test *)",
      "Bash(npm test *)",
      "Bash(kubectl get *)",
      "Bash(docker ps *)"
    ],
    "ask": [
      "Bash(rm *)",
      "Bash(git push *)",
      {"regex": "\\bsudo\\b", "tools": ["Bash"], "reason": "privilege escalation"}
    ]
  }
}
```

Common read-only commands (`ls`, `cat`, `grep`, `git status`, `which`,
`command -v`/`command -V`, …) are allowed by the built-in read-only allowlist,
so they need no explicit rule. (`command` counts as read-only only with a
`-v`/`-V` lookup flag; `command <prog> …` executes `<prog>` and is still gated.)

### Adding a domain

When you specialise the agent, add a matching rule pair (read-only
auto-allow + mutating ask):

```json
{
  "permissions": {
    "allow": [
      "Bash(psql -c \"select*)",
      "Bash(aws s3 ls *)"
    ],
    "ask": [
      {"regex": "psql -c \"(insert|update|delete|alter|drop)", "tools": ["Bash"], "reason": "mutating SQL"},
      "Bash(aws s3 rm *)",
      "Bash(aws s3 cp *)",
      "Bash(aws s3 mv *)",
      "Bash(aws s3 sync *)"
    ]
  }
}
```

### Asker

The root binary uses `permissions.StdinAsker{}` which prompts on the
terminal. Implement `permissions.Asker` to integrate with a different
UI (web modal, Slack DM, etc.).

---

## `mcp_config.json`

Wires external [Model Context Protocol] servers as ADK toolsets. Each
entry spawns a child process and exposes its tools to the agent.

Servers are a map keyed by name:

```json
{
  "servers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "."]
    },
    "kubernetes": {
      "command": "npx",
      "args": ["-y", "mcp-server-kubernetes"],
      "env": {"KUBECONFIG": "/home/you/.kube/config"}
    },
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_…"},
      "requires": [
        { "command": "npx", "label": "Node.js / npx",
          "install": {"darwin": "brew install node", "linux": "apt-get install -y nodejs npm"} }
      ]
    }
  }
}
```

### Fields

| Field      | Required | Notes                                                                 |
|------------|----------|-----------------------------------------------------------------------|
| (map key)  | yes      | The server name — used as the toolset prefix in the agent.            |
| `command`  | yes      | Executable to spawn (must be on `PATH`, or declared in `requires`).   |
| `args`     | no       | Arguments passed to the command.                                      |
| `env`      | no       | Environment variables added to the child process.                    |
| `requires` | no       | Runtime dependencies (binary + install command) installed with user consent at first connect — see below. |

### Lifecycle

- Servers without interactive `${input:id}` references or `requires` connect
  eagerly. If such a server fails to start it is logged and skipped — the agent
  continues with the rest.
- Servers with `${input:id}` references **or** `requires` connect **lazily at
  first tool use**, so the credential prompt / dependency install can be shown
  in a live session rather than at boot.
- Servers are killed when the root binary exits.
- Tool names are namespaced as `<server>/<tool>` to prevent collisions.

### Dependencies (`requires`)

A server may declare runtime dependencies — a binary that must be on `PATH` plus
how to install it — that the host enforces with user consent rather than letting
the server fail silently:

```json
"requires": [
  { "command": "uvx", "label": "uv", "install": "pip install uv" }
]
```

`install` is a string or a per-OS object keyed by `GOOS`
(`{linux, darwin, windows, default}`). At first tool use each missing binary
prompts the user to install; on approval the install runs through the Bash
safety floor and is re-checked. On decline or failure the server reports as
unavailable (a sticky connect error) instead of hanging. The same field — same
shape — backs skill dependencies via a `requires.json` sidecar (see
[skills.md](skills.md)). Implementation: `internal/deps` (`Ensure`) +
`internal/mcp` (`Server.Requires`).

### Security

Treat MCP servers as **untrusted code paths**: they receive arguments
from the LLM. Always pair an MCP server with `permissions.json` rules
gating its mutating verbs. The OOTB defaults already gate `kubectl
apply/delete`, `helm install`, `terraform apply`, etc.

[`regexp`]: https://pkg.go.dev/regexp
[Model Context Protocol]: https://modelcontextprotocol.io/

---

---

## `a2a_config.json`

Declares remote [A2A-protocol](https://google.github.io/A2A/) endpoints the
leader can delegate work to. Each entry in `agents` becomes an
`a2a_<name>` tool on the leader — the model calls it like any other tool and
receives the remote agent's text reply.

```json
{
  "agents": {
    "peer-yoke": {
      "url": "http://peer-host:8091/",
      "description": "Secondary yoke server specialised in database triage.",
      "headers": { "Authorization": "Bearer ${input:peer_token}" },
      "squad": "research",
      "session_name": "",
      "create": false
    }
  },
  "inputs": [
    {
      "id": "peer_token",
      "type": "promptString",
      "description": "Bearer token for the peer yoke server",
      "password": true
    }
  ]
}
```

### Agent fields

| Field          | Required | Purpose |
|----------------|----------|---------|
| `url`          | yes      | Base URL of the remote A2A endpoint. |
| `description`  | no       | Shown to the model as the tool description. Defaults to "Remote A2A agent." |
| `headers`      | no       | HTTP headers added to every request (e.g. `Authorization`). Values support `${input:id}` substitution. |
| `squad`        | no       | Default remote squad to address. Empty means the remote server's own default squad. Overridable per-call via the tool's `squad` argument. |
| `session_name` | no       | Default friendly name of the remote session to target (the name shown in the remote's web UI sidebar). Empty means the call is stateless. Overridable per-call via `session_name`. |
| `create`       | no       | When `true` and `session_name` is set, materialise the session on the remote if it does not yet exist. Idempotent. Overridable per-call via `create`. |

### `${input:id}` template syntax

Any string field in an agent entry may contain `${input:id}` placeholders.
These are resolved from the `inputs` array — interactively prompted (once per
session, then cached) using the same mechanism as MCP inputs.

### Tool arguments

When the leader invokes an `a2a_<name>` tool it can pass:

| Argument       | Type   | Purpose |
|----------------|--------|---------|
| `prompt`       | string | The task to delegate (required). |
| `squad`        | string | Override the remote squad for this single call. |
| `session_name` | string | Override the remote session for this single call. |
| `create`       | bool   | Materialise the named session if missing (only when `session_name` is set). |

Per-call values take precedence over the agent-level config defaults, which
in turn take precedence over the remote server's own defaults.

### Addressing named sessions

When `session_name` is set, the call is routed to the matching session in the
remote server's session registry (looked up by the friendly petname visible in
the web UI sidebar, e.g. `teaching-kite`). The turn is persisted into the
remote session's conversation file and any open web UI tab on that session
receives a live `mailbox_push` SSE event.

When `session_name` is empty the call is stateless: the remote server creates
a fresh ephemeral session for the duration of the request, runs the turn, and
discards it.

### Smoke test

```bash
# Start the remote and your local yoke-server, then:
make a2a-smoke A2A_URL=http://127.0.0.1:8091/
```

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
yoke [flags] [<launcher-command> [launcher-args]]
```

| Flag                | Default  | Effect                                                                                  |
|---------------------|----------|-----------------------------------------------------------------------------------------|
| `-s`, `--skills DIR`| `skills` | Directory scanned at startup for `<name>/SKILL.md` playbooks (see [skills.md](skills.md)). Pass an alternative folder to retarget the agent without touching the default `skills/` tree. |
| `--softskills DIR`  | `softskills` | Directory where curator-generated soft-skills are loaded and stored. |
| `--name NAME`       | `yoke` | Application name used by the runner/UI. |
| `--config FILE`     | resolved from `.agents/agents.json` or `$HOME/.yoke/agents.json` | Runtime JSON config file path. |
| `--provider NAME`   | from agents.json/env/defaults | Global model provider override. |
| `--model NAME`      | from agents.json/env/defaults | Global model id override. |
| `--base-url URL`    | from agents.json/env/defaults | Global model base URL override. |
| `--api-key VALUE`   | from agents.json/env/defaults | Global model API key override. |
| `--curator-enabled BOOL` | from agents.json/env/defaults | Enable/disable the auto-curator hook (`true`/`false`). |
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
| `YOKE_CURATOR_MIN_TURNS` | `agent`     | Minimum model-response count before non-forced curation (default: `3`) |
| `YOKE_CURATOR_MIN_SUB_AGENT_CALLS` | `agent` | Minimum sub-agent calls required when no decision recorded (default: `2`) |
| `YOKE_CURATOR_IDLE_TIMEOUT` | `server` | Idle period after which the Web UI auto-triggers curator (e.g. `30m`; `0` = disabled); session is marked Harvested after firing |
| `GOOGLE_API_KEY`     | gemini provider       | Auth                                             |
| `GEMINI_API_KEY`     | gemini provider       | Auth (alias for `GOOGLE_API_KEY`)                |
| `ANTHROPIC_API_KEY`  | anthropic provider    | Auth                                             |
| `OPENAI_API_KEY`     | openai / openai_compat| Auth                                             |
| `OPENAI_BASE_URL`    | openai_compat         | API endpoint                                     |
| `REDIS_URL`          | `internal/teammates`  | Switch the mailbox backend to Redis              |
