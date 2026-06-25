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

## Squads, and the Omnis router

A **squad** is a named group `{ leader, members[] }` defined in
`agents.json`. A session always runs on one squad at a time, and the
sidebar badge shows which.

**By default new chats are routed, not pinned.** Each new conversation
starts on the **Omnis router**, which picks the squad best able to handle
your request and hands over. If you change topic to something the active
squad can't handle, it hands control back to Omnis and the session
**switches squads** mid-conversation (a routing chip marks each switch).
Each squad keeps its **own history within the session**, so returning to an
earlier topic resumes that squad's earlier context. The current squad is
persisted in `conversation_<id>.json` and survives server restarts. See
[Architecture → Omnis router](10-architecture.md#omnis-router-default-chat-routing).

**Forcing a starting squad.** The compact picker next to the **New Chat**
button pins the next session to a specific squad, bypassing the router for
that chat. Single-squad setups stay tidy: the picker hides itself when only
the `default` squad is available, and your last choice is remembered in
`localStorage`. A pinned squad can still hand back to the router if you go
out of its scope.

If a server reload removes or renames the squad a session was running
on, the server falls back to the `default` squad on that session's
next turn (and logs a warning).

To **disable routing** entirely, set `router_squad` to `"none"` in
`agents.json` (or `OMNIS_ROUTER_SQUAD=none`); new chats then start directly
on the squad you pick (or `default`). To define new squads, see the
**Squads** sub-tab under Settings → Agents.

## Session lifecycle and the curator

When a session ends (closed, deleted, or **idle** for `OMNIS_CURATOR_IDLE_TIMEOUT`),
omnis runs a two-stage reflection pipeline followed by the curator:

1. **Heuristic reflector** tags every soft-skill the session loaded as
   `helpful` / `harmful` / `neutral` based on the StateLog, the last
   user messages, tool errors, and any explicit wrap-up feedback. Tag
   counts land in `$OMNIS_HOME/softskills/_stats.json`.
2. **LLM reflector** (the `reflector` agent, when enabled) refines the
   tags with reasons and extracts a `key_insight`.
3. The **curator** sub-agent reads the audit + StateLog + per-skill
   stats + the reflector's verdict, and creates / updates / deletes
   soft-skill files under `$OMNIS_HOME/softskills/`. The create / delete
   thresholds are concrete (see [11-skills.md](11-skills.md#post-session-reflection))
   so the curator skips by default.

Soft-skills show up in future sessions as additional knowledge the
leader can `load_softskill` on demand. The session's StateLog is also
indexed into the cross-session precedents store — see
[Learning & Recall](20-learning-and-recall.md) for how the StateLog is
built and recalled.

Idle-harvested sessions are marked **Harvested** in the sidebar and skipped on
re-runs until new activity occurs.

On interactive surfaces the leader is told to load the built-in
**wrap-session** soft-skill once per session, which asks one closing
question ("Anything off, or are we good to wrap?"). The answer is
persisted via `record_session_feedback` and becomes the dominant
verdict signal for both reflectors.

## Hot reload

Edits to `agent.json`, `permissions.json`, or `mcp_config.json` (made through
the Settings panel or by hand on disk) can be applied **without restarting**:
the banner above the chat exposes a **Reload** button. In-flight sessions stay
pinned to their existing agent generation; new sessions pick up the change.

The escape hatch — **Restart server** — is reserved for changes the hot-reload
path cannot apply (environment variables, binary updates).
