# Architecture

Yoke is built on top of [google.golang.org/adk](https://pkg.go.dev/google.golang.org/adk),
which provides the agent loop, session, plugin, and runner primitives. The
Yoke binary wires a fleet of agents on top of that runtime.

## Agent topology

```
agent.NewAgent()                ← single wiring entry point
    ├── Squads                  ← one wired tree per squad in agent.json
    │    ├── "default"          ← leader + full team
    │    │    ├── leader        ← coordinator (fs tools + planning + mailbox)
    │    │    ├── investigator  ← read-only evidence gatherer
    │    │    ├── web_agent     ← web search + page fetch
    │    │    └── summariser    ← condenses bulk output
    │    └── "research"         ← leader + smaller team, picked per session
    └── curator                 ← process-wide post-session soft-skill distiller
```

A **squad** is a named group `{ leader, members[] }` declared in
`config/agents.json`. Each squad becomes its own wired tree inside the
current generation; a chat session selects which squad to use when it
is created (the `default` squad when nothing is picked) and the server
resolves `Instance.Squad(name).Runner` per session. Two sessions on
the same generation can therefore use different squads.

Squads compose existing agents — they don't redefine them. Skills,
tools and MCP servers stay on the agent definitions, so two squads
that share a member also share its wiring (and the MCP subprocess
pool dedups any backing process).

The **leader** is the only agent you talk to. Sub-agents are wrapped via
`agenttool.New()` and exposed as **tools** on the leader (not via the ADK
`transfer_to_agent` mechanism). Control therefore always returns to the
leader after a sub-agent call — there is no hand-off semantics.

Only one sub-agent runs at a time. Concurrency is enforced by
`newNonConcurrentTool`: a second sub-agent invocation queues until the first
one finishes. The curator stays a single per-generation hook listening
across every squad, so session-end curation runs at most once per
session.

## Two-layer build

The server can swap out the live agent generation without restarting the
process. The model is split across `agent/infrastructure.go`,
`agent/instance.go`, and `agent/manager.go`:

- **Infrastructure** is process-wide and survives every reload: the mailbox
  backend, session registry, event bus, `ask_user` registry, MCP subprocess
  pool, and the session-scoped state holders (tasks, todo, background queues).
- **Instance** is one agent generation: a map of **SquadInstance** entries
  (each squad's leader + sub-agents + plugins + runner) derived from a
  snapshot of `RuntimeSettings`. Each reload bumps the generation number
  and builds a fresh Instance — with every squad rewired — on top of the
  unchanged Infrastructure.
- **Manager** owns the live generations. New sessions pin to the current
  generation and record their squad on the session; subsequent turns
  resolve `Manager.LookupSquad(sessionID, squadName).Runner`. In-flight
  sessions stay pinned to their existing generation across reloads, so a
  streaming turn never observes a swap.

An old generation is torn down once its pinned-session refcount drops to zero.
The `/api/config/status` endpoint exposes the current generation and
per-generation refcounts, which the **n sessions draining on previous version**
pill in the Web UI reads.

## Plugins

Plugins hook into the ADK event bus to add cross-cutting behavior without
modifying the agent itself. Three plugins ship by default:

- **Compression** — per-session context compression. Watches the token
  watermark and rewrites older turns into `agent_memory_*.md`.
- **Cache stats** — measures prompt cache hit rates and reports them in the
  context popup.
- **Event logger** — writes a structured audit log to `agent_events_*.log`.

## Adding a sub-agent

1. Create `registry/agents/<name>/agent.json` with the agent definition
   (`name`, `description`, `tools`, optional `model_ref`, etc.). Add
   `"builtin": true` only for agents shipped with yoke — leave it out
   for user-defined agents.
2. Optionally add `registry/agents/<name>/instruction.md` for the
   agent's system prompt. If missing, it falls back to
   `registry/agents/default.md`.
3. Add the agent's name to the `agents:` list in `config/agents.json`.
4. Add the agent's name to the `members:` list of every squad that
   should expose it (omit to keep it reserved for a single squad).
5. `agent.NewAgent()` auto-discovers it via `runtime.Agents`. No Go
   code change is needed unless you want custom tool wiring.

## Adding a squad

Squads compose existing agents — they don't redefine them. Edit
`config/agents.json` (or use the **Squads** sub-tab under Settings →
Agent) to declare a new squad:

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

Rules:

- A squad named `default` is always present. When the squads list is
  empty or omits one, the resolver synthesises a default from the
  enabled agents.
- `leader` and every member must reference an enabled agent.
- `curator` cannot be a member — it is process-wide.

The new squad becomes selectable in the New Chat picker on the next
hot-reload.
