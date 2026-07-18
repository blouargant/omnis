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

## Collections (thematic folders)

The web UI is a **three-column layout**, like an email client:

- **Left column** — the app chrome (the **Omnis** header, **New Chat**), your
  **Collections** (thematic folders), the **Archived** panel, the per-chat
  **Files** browser, and the **Settings / Appearance / Documentation** buttons.
- **Middle column** — a **toolbar** above the **session list** for the selected
  collection (grouped by time — Today / Yesterday / This week / …).
- **Right column** — the conversation panes (and the Settings body).

The middle column's **toolbar** carries: the current view's name and session
count; **Search** (filter the list by title); **Sort** (recent activity / date
created / A–Z); **Select** (bulk mode — tick multiple sessions, then **Move to** a
collection, **Archive**, or **Delete** them together, with a Select-all); and a
**New chat** button that starts a chat in the current collection.

A session lives in **exactly one** collection. **General** is the always-present
default: every new chat starts there unless another collection is selected, and
it holds any session you haven't filed elsewhere. General can't be renamed or
deleted.

- **Create** a collection with the **+** button at the top of the rail.
- **Select** a collection to filter the middle list to just its sessions. A new
  chat you start while a collection is selected is filed under it.
- **Move** a session by dragging its row onto a collection in the rail, or via
  the session's **⋯ → Move to** menu.
- **Rename / Delete** a collection from its right-click menu. Deleting a
  collection moves its sessions back to **General** (nothing is lost).

Collections and each session's filing are persisted (`collections.json` +
`conversation_<id>.json`) and survive a server restart. Changes sync live across
open browsers.

### Collection context (instructions, memory, defaults)

A collection can carry **persistent context** that applies to every chat filed
under it — useful for a *thematic, cross-repo* workstream (a client, a research
topic) that a per-directory `AGENT.md` can't cover. Open a collection's
right-click menu → **Edit context…**:

- **Instructions** — hand-authored, stable guidance prepended to every chat in
  the collection (tone, stack, do/don't). This is an `AGENT.md` scoped to the
  collection instead of a folder.
- **Memory** — facts about the workstream that persist across its chats (which
  repos are involved, decisions taken, conventions). Type it yourself, or click
  **Generate from recent chats** to have Omnis draft it from the collection's own
  recent conversations. The draft only fills the (editable) field — nothing is
  saved until you review it and click **Save**, so an out-of-date fact can never
  slip into your chats unnoticed.
- **Default squad** — new chats in the collection start on this squad instead of
  asking the router. It's a **hint, not a lock**: routing still runs, so a chat
  that drifts off-topic is handed back to the router automatically. Leave it on
  **Router** to let Omnis choose as usual.
- **Default folder** — the working directory new chats in the collection start
  in (optional).

Instructions and memory are injected into the assistant's context on every turn;
the squad/folder defaults apply when you start a new chat while the collection is
selected. Everything is stored under `$OMNIS_HOME/collections/<name>/` (prose)
and `collections.json` (defaults), and follows the collection through renames.
**General** has no context — it's the catch-all bucket.

If you're not sure what to write, click the **Assistant** button that floats in
the corner of the **Instructions** or **Memory** box to open a chat that helps
you draft that field. Describe your workstream and it proposes text with **Apply
to instructions** / **Apply to memory** buttons that drop the draft into the
field — you still review and **Save**, so nothing changes until you approve it.

## Session list affordances

- **Active session** is highlighted; a small dot indicates a busy (streaming)
  session — you can switch away and the work continues in the background.
- **Title** is auto-generated from the first model turn but can be renamed.
- **Pinned prompt** (the header above the transcript) shows the original
  request once the conversation has scrolled past it.
- **Squad badge** appears next to sessions running on a non-default squad,
  so you can tell at a glance which configuration each conversation uses.

## Searching past sessions

Open a new tab (or an empty pane) and use the **search box under "Start a new
chat"**. It searches what was actually said — your requests and the agent's
replies — across every past session, **including archived ones**. Tool calls are
not searched. Clicking a result opens that session and jumps to the matching
exchange.

There are two searches behind the one box, and the difference matters:

- **As you type**, you get an immediate ranked list. When an embedding model is
  configured it is a *semantic* search: "how much does the auditor cost" finds a
  conversation that said "premium is 140× for the same accuracy", even with no
  word in common. With no embedding model it falls back to a **direct scan** of
  every conversation — literal (every word must appear) and slower on a large
  history, which the box tells you.
- **Press Enter** (or click **Ask**) when that list is not good enough. The query
  goes to the **session_search agent**, which rewords it, searches again, *opens*
  the candidate sessions to check them, and reports back only the sessions it
  could actually confirm — each with the reason it matters. It is slower and costs
  a model call; it is the right tool when you half-remember a conversation and the
  obvious keywords are not in it.

The semantic index builds itself in the background (a session is indexed shortly
after it goes quiet, and when you archive it) and is dropped from memory when you
stop searching. To build it all at once — after a fresh install, or after changing
the embedding model — run `omnis reindex-sessions`.

In a **chat**, ask the same thing in words ("find the chat where we discussed the
k8s auditor") and the router hands it to the **Helper** squad, which delegates to
the same specialist.

## Export and import a conversation

A conversation can be moved between Omnis instances as a single JSON file.

- **Export** — open a session's **⋯ menu** and choose **Export chat**. The whole
  conversation (title, squad, collection, and every turn) downloads as
  `omnis-session-<id>.json`.
- **Import** — click the **Import** button in the session-list toolbar (next to
  **New chat**) and pick a previously exported file. Omnis creates a **new**
  chat seeded with the imported transcript and opens it.

Imports are made portable automatically: if the exported squad doesn't exist on
the target instance it falls back to the default (routing) squad, an unknown
collection lands the chat in **General**, and machine-specific details (the
working directory, an in-progress goal, archived/hidden flags) are dropped so the
import always arrives as a normal, active chat.

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
on the squad you pick (or `default`). To define new squads, use the
**Fleet** under Settings → Agents.

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
