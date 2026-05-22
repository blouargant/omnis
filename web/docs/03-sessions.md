# Sessions & Sidebar

Sessions are the unit of conversation isolation. Every mutable artifact the
agent produces is scoped by `(userID, buildTimestamp, sessionID)`.

## What lives in a session

- Transcript (`conversation_<id>.json` on disk).
- Task graph (`agent_tasks_*.json`).
- Todo list (`agent_todo_*.json`).
- Compressed memory (`agent_memory_*.md`).
- State log feeding the curator (`agent_statelog_*.json`).
- Mailbox namespace for inter-agent messages.
- Uploaded files under `logs/uploads/<session>/`.

Two concurrent sessions never share any of this. Deleting a session removes
the file group above (subject to garbage collection).

## Session list affordances

- **Active session** is highlighted; a small dot indicates a busy (streaming)
  session — you can switch away and the work continues in the background.
- **Title** is auto-generated from the first model turn but can be renamed.
- **Pinned prompt** (the header above the transcript) shows the original
  request once the conversation has scrolled past it.
- **Squad badge** appears next to sessions running on a non-default squad,
  so you can tell at a glance which configuration each conversation uses.

## Squads — picking a configuration per chat

A **squad** is a named group `{ leader, members[] }` defined in
`config/agents.json`. Each chat session uses exactly one squad — the one
selected when it was created.

The compact picker next to the **New Chat** button selects which squad
the next session will use. Single-squad setups stay tidy: the picker
hides itself entirely when only the `default` squad is available. Your
last choice is remembered in `localStorage` so it stays preselected
across browser reloads.

Once a session is created the squad is **fixed for the life of that
conversation** and persisted in `conversation_<id>.json` — switching
squads means starting a new chat. The recorded choice survives server
restarts.

If a server reload removes or renames the squad a session was running
on, the server falls back to the `default` squad on that session's
next turn (and logs a warning).

To define new squads, see the **Squads** sub-tab under
Settings → Agents.

## Session lifecycle and the curator

When a session ends (closed, deleted, or **idle** for `YOKE_CURATOR_IDLE_TIMEOUT`),
the **curator** sub-agent reads the state log and distils any reusable
procedures into soft-skill files under `$YOKE_HOME/softskills/`. These show up
in future sessions as additional knowledge the leader can `load_softskill` on
demand.

Idle-harvested sessions are marked **Harvested** in the sidebar and skipped on
re-runs until new activity occurs.

## Hot reload

Edits to `agent.json`, `permissions.json`, or `mcp_config.json` (made through
the Settings panel or by hand on disk) can be applied **without restarting**:
the banner above the chat exposes a **Reload** button. In-flight sessions stay
pinned to their existing agent generation; new sessions pick up the change.

The escape hatch — **Restart server** — is reserved for changes the hot-reload
path cannot apply (environment variables, binary updates).
