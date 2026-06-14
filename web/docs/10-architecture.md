# Architecture

Yoke is built on top of [google.golang.org/adk](https://pkg.go.dev/google.golang.org/adk),
which provides the agent loop, session, plugin, and runner primitives. The
Yoke binary wires a fleet of agents on top of that runtime.

## Agent topology

```
agent.NewAgent()                ← single wiring entry point
    ├── Squads                  ← one wired tree per squad in agent.json
    │    ├── "omnis"            ← Omnis ROUTER squad (default for new chats)
    │    │    └── omnis         ← routes each request to the best squad, then steps out
    │    ├── "default"          ← leader + full team (used when routed to)
    │    │    ├── leader        ← coordinator (fs tools + planning + mailbox)
    │    │    │    └── a2a_*   ← one tool per peer in a2a_config.json
    │    │    ├── investigator  ← read-only evidence gatherer
    │    │    ├── web_agent     ← web search + page fetch
    │    │    └── summariser    ← condenses bulk output
    │    └── "research"         ← leader + smaller team, picked per session
    ├── reflector               ← process-wide post-session LLM analyst (optional)
    └── curator                 ← process-wide post-session soft-skill distiller
```

A **squad** is a named group `{ leader, members[] }` declared in
`agents.json`. Each squad becomes its own wired tree inside the
current generation; a chat session selects which squad to use when it
is created and the server resolves `Instance.Squad(name).Runner` per
session. Two sessions on the same generation can therefore use
different squads.

By default a new chat does **not** start on `default` — it starts on the
**Omnis router** (see below), which picks the best squad for the request
and hands over.

Squads compose existing agents — they don't redefine them. Skills,
tools and MCP servers stay on the agent definitions, so two squads
that share a member also share its wiring (and the MCP subprocess
pool dedups any backing process).

Within a squad, the **leader** is the only agent you talk to. Sub-agents
are wrapped via `agenttool.New()` and exposed as **tools** on the leader
(not via the ADK `transfer_to_agent` mechanism). Control therefore always
returns to the leader after a sub-agent call — there is no hand-off
semantics *inside* a squad. (Between squads, the Omnis router is the one
component that does transfer control — see below.)

## Omnis router (default chat routing)

**Omnis** is a special **leaderless squad** (a single agent run directly,
with no coordinator) that is the **default squad for every new chat**.
Unlike a squad leader — which orchestrates its own members and keeps
control — the router *transfers control of the conversation*: it reads
your request, picks the squad best able to handle it, and hands over;
that squad's leader then answers you directly.

- **Hand-back on topic change.** If you later ask about something outside
  the active squad's scope, that squad hands control **back** to Omnis,
  which re-routes to a better squad. Each squad keeps its own conversation
  history within the session, so going *kubernetes → car manual →
  kubernetes* returns you to the first squad with its earlier context
  intact.
- **Verify-before-committing.** When the router is unsure which squad fits,
  it privately asks a candidate squad's lead whether the request is in
  scope before handing over. This negotiation is **hidden** — you only see
  the final routing decision and the squad's answer.
- **Talks to you when nothing fits.** If the request is ambiguous and no
  squad fits, Omnis asks you a clarifying question instead of force-routing.
- **Silent and faithful.** Routing is invisible except for a small
  **routing chip** in the transcript; the router never prompts for
  permission, never narrates ("Routed to …"), and forwards your message —
  including any attached files — to the answering squad **verbatim** (it
  cannot paraphrase or drop it).
- **Opt-out.** Set `router_squad` to `"none"` in `agents.json` (or
  `YOKE_ROUTER_SQUAD=none`) to disable routing; new chats then start on the
  `default` squad and behave exactly as before. Absent ⇒ defaults to
  `omnis`, which is injected automatically if your config doesn't declare
  it.

Only one sub-agent runs at a time. Concurrency is enforced by
`newNonConcurrentTool`: a second sub-agent invocation queues until the first
one finishes. The curator stays a single per-generation hook listening
across every squad, so session-end curation runs at most once per
session.

The **reflector** is a peer process-wide hook that runs *before* the
curator at session end. It tags every soft-skill the session loaded as
`helpful` / `harmful` / `neutral`, extracts a single `key_insight`, and
feeds the merged outcome plus per-skill stats into the curator's prompt
so the create / update / delete decision is grounded in concrete
evidence (see [11-skills.md](11-skills.md#post-session-reflection)). A
deterministic in-process heuristic always runs; the LLM reflector layers
on top when the `reflector` agent is enabled.

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
3. Add the agent's name to the `agents:` list in `agents.json`.
4. Add the agent's name to the `members:` list of every squad that
   should expose it (omit to keep it reserved for a single squad).
5. `agent.NewAgent()` auto-discovers it via `runtime.Agents`. No Go
   code change is needed unless you want custom tool wiring.

## Connecting A2A peers

Remote [A2A-protocol](https://google.github.io/A2A/) agents are wired via
`a2a_config.json`. Each entry becomes an `a2a_<name>` tool on the
leader. The leader can:

- Delegate a task to any configured peer with a natural-language `prompt`.
- Address a specific **squad** on the remote server (via the `squad` arg or
  the `squad` config default).
- Route the turn into a **named session** visible in the remote web UI sidebar
  (via `session_name`); the turn is persisted and open tabs on that session
  receive a live SSE push.
- Materialise the named session on the remote if it does not yet exist (set
  `create: true`).

See [docs/configuration.md](../docs/configuration.md#configa2a_configjson) for
the full field reference.

## Adding a squad

Squads compose existing agents — they don't redefine them. Edit
`agents.json` (or use the **Squads** sub-tab under Settings →
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
- `curator` and `reflector` cannot be members — they are process-wide.
- A `leader` of `"none"` (or empty) makes the squad **leaderless** and
  requires **exactly one member**, which runs directly as the root with no
  coordinator (the shape used by the `helper` squad and the Omnis router).
  Squads with two or more members need a real leader.

The new squad becomes selectable in the New Chat picker on the next
hot-reload. (The router squad — `omnis` by default — is excluded from the
picker: it is the routing entry point, not a destination you pick.)
