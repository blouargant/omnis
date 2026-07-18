# Settings Panel

Click the gear icon at the bottom of the sidebar to open Settings. The panel
replaces the chat surface; the chat resumes when you close Settings.

## Sections

The sidebar lists the available sections. Each maps either to a JSON config
file on disk or to a client-only view:

| Section        | Backing file                  | Purpose |
|---|---|---|
| **Skills**     | `registry/skills/`             | Manage authored playbooks the agent can load on demand. |
| **Agents**     | `agents.json` + `registry/agents/` | Roles, model profiles, tool wiring, global env. Per-agent details live in `registry/agents/<name>/`. |
| **Permissions**| `permissions.json`     | What the agent may run without asking. |
| **Hooks**      | `hooks.json`           | Shell commands fired at lifecycle moments (before/after a tool, on prompt/session/compaction). |
| **MCP**        | `mcp_config.json`      | External tool servers (Model Context Protocol). |
| **A2A**        | `a2a_config.json`      | Remote A2A agent endpoints; each entry becomes an `a2a_<name>` tool on the leader. |
| **Commands**   | (client only)                 | Custom slash command templates that expand to a prompt. |
| **Appearance** | (client only)                 | Theme picker. |
| **Documentation** | (client only)              | This page. |

Below the section list a **Raw JSON** entry switches the active JSON section
into a textarea editor. The Form ↔ Raw toggle preserves unsaved edits across
visits within the same session.

## Save flow

JSON sections show a footer with **Discard** and **Save**. Saving runs a
server-side validation pass; on success a non-intrusive banner offers to
**Reload** the agent (no downtime) or **Restart server** (for env/binary
changes).

If you save the same file twice from different tabs, the second save will
fail with a stale-mtime error — reload the panel and retry.

## Editing Agents

The Agents section **is the Fleet**: a topology tree on the left — router →
squads → leaders → members → nested sub-agent teams, plus an **Unused
agents** pool for agents no squad references — and a right-hand editor for
whichever node you select. A header row above the tree carries four peers,
**Fleet · Models · Registry · Global**, plus a reload button (Models and
Registry are also their own top-level Settings sections — Registry being
the **Registries** section — while Global is reached only through this
header peer). See the dedicated [Agents Settings](19-agents.md) page for
the full field-by-field reference.

Key points:

- **`＋` create** — opens a menu: **New squad…**, **New agent…**, and
  **Import agent from file…** (paste or load a Claude Code-style `.md` or
  `.json` agent definition).
- **Node context menus** — right-click a node, or use its **`⋯`** button:
  squads offer add/set member, set/make leader, duplicate, and delete;
  agents offer add to team, add to squad, make leader, enable/disable,
  duplicate, remove from squad, and delete.
- **Drag-and-drop** — reorder a squad's members, or drop a member or an
  unused agent onto a different squad to add it there as a shared
  reference.
- Selecting an **agent** node opens its editor: tool set, skill/MCP/A2A
  wiring, system instruction, and model reference.
- Selecting a **squad** node opens its editor: leader, members,
  description, and the leaderless option (see "Squads" below).

The defaults are baked into the binary; the form highlights any field that
diverges from the built-in baseline.

### Squads

A **squad** is a named group `{ leader, members[] }` that a chat session
picks at creation time — selecting a different squad gives the user a
different set of delegable sub-agents while reusing the same shared agent
definitions. New chats actually start on the **Omnis router** squad rather
than `default` — it routes each request to the best squad and hands over
(see [Architecture](10-architecture.md#omnis-router-default-chat-routing));
choose the router squad, or disable routing entirely, with the top-level
`router_squad` key in `agents.json` (`"none"` disables; absent ⇒ `omnis`).

See [Agents Settings](19-agents.md) for the squad editor's full field
reference (name, description, leader, members, the leaderless option, and
deleting a squad).

## Editing Permissions

omnis uses **Claude Code's permission nomenclature**. The panel shows a
`defaultMode` selector and three rule tiers, evaluated **deny → ask → allow**
(first match wins):

1. **deny** — the action is rejected without prompting.
2. **ask** — a confirmation prompt appears in the chat.
3. **allow** — the action runs silently.

Each rule is a `Tool(specifier)` string — e.g. `Bash(npm run *)`, `Read(.env)`,
`mcp__github__*`, `Agent(Explore)` — or a `/regex/` escape hatch; an object rule
adds an optional reason and a project-scoping `cwd`. See the **Permissions**
concept page for the full syntax and modes. Skill-contributed permissions appear
in a read-only block — they are owned by the skill file and cannot be edited from
this panel.

## Editing Hooks

**Settings → Hooks** edits `hooks.json` — shell commands that fire at lifecycle
moments. The panel lists every event (PreToolUse, PostToolUse, UserPromptSubmit,
Stop, SubagentStop, SessionStart, SessionEnd, PreCompact, Notification); under
each you add **matcher cards** with a tool-name regexp (or blank for "all") and a
list of `command` + `timeout` rows. Edits hot-reload within a few seconds.

Hooks run **outside the permission layer** (you authored them) but still hit the
hard safety floor, and they receive the event as JSON on stdin — a `PreToolUse`
hook can block a tool by exiting `2`. See the **[Lifecycle Hooks](22-hooks.md)**
concept page for the input/output protocol and examples.

## Editing MCP

The MCP section lists every external server defined in `mcp_config.json` along
with its command, args, env vars, and resolved tool list (fetched live).

- **Inputs** — declare named secrets the server needs (e.g. an API key). At
  call time the agent emits the `ASK_USER` sentinel; the Web UI prompts you,
  caches the answer for the rest of the session, and coalesces concurrent
  requests for the same input.
- **Import / Export** — paste a snippet from another tool's MCP catalogue;
  duplicates are detected and you choose merge / replace / skip per server.

The MCP subprocess pool deduplicates by `(command, args, env)` hash: two
agent generations that mount the same server share a single child process.

## Editing A2A

The A2A section manages outbound connections to remote Agent-to-Agent peers.
It has two sub-tabs:

- **Agents** — list of remote endpoints. Each peer card has four sections:
  - **General Settings** — peer name (becomes an `a2a_<name>` tool on the leader).
  - **Connection** — URL and optional description.
  - **Routing** — default squad, session name, and "create session if missing"
    checkbox sent with every call (the agent can override per-invocation).
  - **Headers** — arbitrary HTTP headers; values support `${input:id}` to
    keep credentials out of the saved config.
- **Remotes** — browse and install A2A agent definitions from remote
  repositories (same infrastructure as skill/agent remotes).

**Inputs** in the Agents sub-tab declare named secrets that the Web UI
prompts for at first use and caches for the session. Reference them from
header values as `${input:id}`.

After adding a peer here, open the **Agents** section and enable the peer
under the target agent's **A2A Agents** block so the leader can delegate to it.

## Editing Commands

The Commands section is client-only (no backing config file is loaded from
the server-side config chain). It shows:

- **Built-in commands** — read-only table of commands shipped with the Web UI
  (`/help`, `/compress`, `/create-skill`, `/update-skill`, `/status`,
  `/learn`, `/learn-now`). These names are reserved and cannot be reused.
- **User commands** — CRUD table of custom slash commands. Each command
  has a name, optional description, optional args hint, and a prompt
  template body. Template placeholders: `$1`, `$2`, … for positional
  arguments; `$*` for all arguments joined together.

User commands are persisted to `$OMNIS_HOME/user_commands.json` and
take effect immediately — no reload required.
