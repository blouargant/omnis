# Extending the harness with code

Most specialisation is configuration (see [specialising.md](specialising.md)).
You only need Go when you want a **brand-new tool**, **plugin** or
**sub-agent** that doesn't yet exist.

## Add a new tool

A tool is a Go function adapted to ADK's `tool.Tool` interface. The
simplest path is to mirror an existing one — `core/tools/grep.go` is a
small, complete reference.

Skeleton:

```go
package mytool

import (
    "context"

    "google.golang.org/adk/tool"
    "google.golang.org/genai"
)

type Args struct {
    Pattern string `json:"pattern"`
}

func New() tool.Tool {
    return tool.NewFuncTool(
        "my_tool",
        "One-sentence description visible to the model.",
        &genai.Schema{
            Type: genai.TypeObject,
            Properties: map[string]*genai.Schema{
                "pattern": {Type: genai.TypeString},
            },
            Required: []string{"pattern"},
        },
        func(ctx context.Context, a Args) (any, error) {
            // do the work
            return map[string]any{"matches": []string{}}, nil
        },
    )
}
```

Then register it in the root `main.go`:

```go
leadTools = append(leadTools, mytool.New())
```

If the tool is mutating, **also add a `permissions.json` rule** so the
gating plugin can prompt the user.

## React to the agent loop without code (hooks)

Before writing a plugin, consider whether a **lifecycle hook** does the job.
`hooks.json` lets you run a shell command at fixed points — before/after a
tool, on prompt submit, on stop, on session start/end, before compaction —
with no Go code and a hot-reload. A `PreToolUse` hook can block a tool
(exit 2), and a `UserPromptSubmit` hook can inject context. See
[Lifecycle hooks](../web/docs/22-hooks.md) and `internal/hooks` +
`agent/hooks_plugin.go`. Reach for a plugin (below) only when you need
in-process state or to mutate the request/response, not just react.

## Add a new plugin

Plugins observe and mutate the agent loop via the `plugin.Plugin`
interface. Look at `core/permissions/permissions.go` for a complete
example that intercepts `BeforeTool`.

Skeleton:

```go
func MyPlugin(name string) (*plugin.Plugin, error) {
    return plugin.New(plugin.Config{
        Name: name,
        BeforeTool: func(ctx context.Context, in *plugin.BeforeToolInput) (*plugin.BeforeToolOutput, error) {
            // inspect / mutate / short-circuit
            return nil, nil
        },
    })
}
```

Wire it into the root `main.go`:

```go
if p, err := mypkg.MyPlugin("mine"); err == nil {
    plugins = append(plugins, p)
}
```

### Filtering events by agent

Every payload emitted by `events.Bus.Plugin` carries an `agent` key
holding the running ADK agent's name (lead or sub-agent). Use it to
route events per agent:

```go
bus.On(events.EventAfterTool, func(_ string, p map[string]any) {
    name, _ := p["agent"].(string)
    if name == "investigator" {
        // ... only sub-agent tool calls
    }
})
```

`EventAfterModel` payloads also include `model`, `duration`, and a
`usage` map (`prompt_tokens`, `candidates_tokens`, `total_tokens`) so
you can attribute token spend to each agent.

## Add a new sub-agent

Most of the time you don't write any Go code. Agents live as files
under `registry/agents/<name>/` — one directory per agent, mirroring
the skills layout. Keep the per-agent instruction **domain-neutral** —
describe the *role* (e.g. "validate inputs", "estimate cost") not the
*domain*. Domain belongs in skills.

Three steps:

**1.** Create the agent directory with its definition:

```bash
mkdir -p registry/agents/critic
```

`registry/agents/critic/agent.json`:

```json
{
  "name": "critic",
  "description": "Pokes holes in a proposed plan.",
  "model_ref": "default",
  "tools": ["fs"]
}
```

**2.** Optionally add a system prompt at
`registry/agents/critic/instruction.md`. If omitted, the agent falls
back to `registry/agents/default.md`.

```markdown
You are an adversarial reviewer. For each step in the proposed plan,
list the most likely failure mode in one sentence. Cite evidence when
possible.
```

**3.** Register the agent by adding its name to the `agents` list in
`agents.json`:

```json
{
  "agents": ["leader", "investigator", "critic"]
}
```

Then add it to the `members:` list of every squad that should be able
to delegate to it (see "Add a squad" below). If no squad includes the
new agent, no session will see it — that's how you keep an agent
reserved for a specific squad.

Built-in vs custom: the agents shipped with omnis (`omnis`, `leader`,
`skill_editor`, `helper`, `summariser`, `curator`,
`reflector`) carry `"builtin": true` in their `agent.json`. The web UI
displays them under a **Built-in Agents** section, separated from
user-added **Custom Agents**. Leave the flag out for your own agents.
(`omnis` is the Omnis router and is auto-injected when your config doesn't
declare it — see [Add a squad](#add-a-squad).)

For programmatic embedders (CLI / examples / TUI) the same registry is
consumed when `agent.NewAgent()` runs; the lower-level constructor
`agentkit.New(...)` is still available if you want to wire a sub-agent
outside the file pipeline.

## Add a squad

A **squad** is the named group of agents `{ leader, members[] }` a chat
session uses. Declare squads under the top-level `squads:` array in
`agents.json`:

```json
{
  "agents": ["leader", "investigator", "critic", "summariser"],
  "squads": [
    {
      "name": "default",
      "description": "General-purpose squad.",
      "leader": "leader",
      "members": ["investigator", "summariser"]
    },
    {
      "name": "review",
      "description": "Plan review with an adversarial reviewer.",
      "leader": "leader",
      "members": ["critic", "summariser"]
    }
  ]
}
```

Rules:

- `default` is always required and is auto-synthesised when omitted.
- `leader` and every member must reference an enabled agent in `agents:`.
- The squad's `leader` must point to an agent marked `"leader": true`. The
  agent literally named `leader` is auto-flagged; mark any other coordinator
  with `"leader": true` to make it eligible as a squad lead.
- A `leader` of `"none"` (or empty) makes the squad **leaderless** — it must
  then declare **exactly one member**, which runs directly as the runner root
  with no coordinator and no sub-agent delegation tools (the shape used by the
  `helper` squad and the Omnis router). Squads with two or more members need a
  real leader.
- `curator` and `reflector` cannot be members — they stay process-wide
  (one hook per generation, fired at `EventSessionEnd` /
  `EventSessionReflected`).

Each squad becomes a separate wired tree inside the current generation
(leader + sub-agents + runner + plugins). New chat sessions pick a
squad through the picker next to the **New Chat** button in the web UI
(or via `POST /api/sessions` with `{"squad": "review"}` on the API). In
the web UI's Settings → Agent panel, the **Squads** sub-tab provides a
structured editor with leader dropdown, member checkboxes, and
add/delete; saving triggers a hot reload.

> **New chats are routed by Omnis.** Unless you pin a squad (the New Chat
> picker / `POST /api/sessions {"squad": …}`), new chats start on the
> **Omnis router** squad, which picks the best squad for each request and
> hands over. Give every squad a clear `description` — that's what the
> router matches against. Disable routing with `router_squad: "none"` in
> `agents.json`. See
> [configuration.md#omnis-router-default-chat-routing](configuration.md#omnis-router-default-chat-routing).

## Connect a remote A2A agent

Omnis implements the [A2A protocol](https://google.github.io/A2A/) on both
sides — it can receive tasks as a server (`server/a2a_server.go`) and
delegate tasks to remote A2A endpoints as a client (`internal/a2a/`).

You **don't write Go code** to wire a peer. Add an entry to
`a2a_config.json` and list the peer's name in the leader's
`a2a_agents` field.

### 1. Create or edit `a2a_config.json`

```json
{
  "agents": {
    "peer-omnis": {
      "url": "http://peer-host:8091/",
      "description": "Secondary omnis server specialised in database triage.",
      "headers": { "Authorization": "Bearer ${input:peer_token}" },
      "squad": "",
      "session_name": "",
      "create": false
    }
  },
  "inputs": [
    {
      "id": "peer_token",
      "type": "promptString",
      "description": "Bearer token for the peer omnis server",
      "password": true
    }
  ]
}
```

The map key (`peer-omnis`) becomes the tool name suffix: the leader sees a
tool called `a2a_peer-omnis`. Use only `[a-zA-Z0-9_-]` characters.

### 2. Add the peer name to `registry/agents/leader/agent.json`

```json
{
  "a2a_agents": ["peer-omnis"]
}
```

Only agents listed here are exposed on the leader; entries in
`a2a_config.json` that are not listed in `a2a_agents` are silently ignored.

### 3. Reload or restart

Hot-reload (`POST /api/config/reload` or the Reload button in the web UI)
picks up both files without a process restart.

### Optional: target a specific squad or session

The tool accepts `squad` and `session_name` arguments the model can fill in
at call time. You can also bake defaults into the config:

```json
{
  "agents": {
    "peer-omnis": {
      "url": "http://peer-host:8091/",
      "squad": "research",
      "session_name": "teaching-kite",
      "create": true
    }
  }
}
```

- `squad` — selects a named squad on the remote (`tasks/send` metadata).
- `session_name` — routes into a named session visible in the remote web UI
  sidebar; the turn is persisted and any open tab on that session receives a
  live SSE push.
- `create: true` — materialise the named session on the remote if it does
  not yet exist (idempotent).

Per-call overrides (the tool's `squad` / `session_name` / `create`
arguments) take precedence over these defaults.

See [docs/configuration.md#configa2a_configjson](configuration.md#configa2a_configjson)
for the full field reference and smoke-test command.

---

## Add a new LLM provider

If the provider exposes an OpenAI-compatible API, **don't** add code —
just point `OPENAI_BASE_URL` at it. Otherwise:

1. Implement `model.LLM` (`Name() string` + `GenerateContent(ctx, *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error]`).
2. Register it in [`core/llm/llm.go`](../core/llm/llm.go)'s `New()` switch.
3. Add a default model id to `defaultModel`.
4. Document its env vars at the top of `llm.go`.

Look at [`core/llm/anthropic.go`](../core/llm/anthropic.go) and
[`core/llm/openai.go`](../core/llm/openai.go) — both are ~200 lines
each, no third-party SDK.

## Conventions

- **Stay domain-neutral** in the harness. Domains live in `skills/`,
  `mcp_config.json` and `permissions.json`.
- **One package per component.** Mirror the existing `core/` and
  `internal/` layout.
- **Expose tools through `tool.Tool`**, never as bare Go functions
  called by the launcher.
- **Use `agentkit.New`** so every agent inherits the universal system
  prompt.
- **Pair every mutating tool with a permission rule.**
- **Make stateful components session-scoped.** If your component holds
  mutable state (a file, a queue, an inbox, a counter), expose a
  `NewSessionScoped(default, pathFor)` (or equivalent `*Func` hook on
  the existing constructor) that resolves the on-disk / on-the-wire
  identifier from `tool.Context.UserID()` + `tool.Context.SessionID()`.
  Mirror the pattern used by `internal/tasks`, `internal/todo`,
  `internal/bg.SessionQueues`, `internal/teammates.Agent.NameFunc` and
  `internal/compress.Config.MemoryPathFunc`. Keep a back-compat
  single-path constructor for the `examples/sNN_*` demos. See
  [configuration.md#session-isolation](configuration.md#session-isolation).
- **Build & vet** before committing: `go build ./... && go vet ./...`.
