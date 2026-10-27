# Sidebar collection tree, account menu, settings modal — design

**Status:** draft, approved piecewise in conversation (2026-10-27) against browser mockups
**Scope:** web UI (`web/`) + one small addition to `GET /api/collections`
(an active-only count, §4.1). CLI/TUI untouched.
**Builder check:** done 2026-10-27; its findings are folded in below.

## 1. Intent

The web UI is a three-column shell: `#sidebar` (chrome + collections list +
Archived + Files + a Settings/Appearance/Documentation footer) | `#session-pane`
(a toolbar + the session list of the *selected* collection, which also hosts
the Settings category list while Settings is open) | `#chat-area`.

The user wants it to work like **Claude Desktop**:

1. **The middle column goes away.** Its session list moves into the left column
   as a **tree**: each collection is a foldable group with its own sessions
   listed directly under it (like Claude Desktop's `ai-forge · blo-cv ▾` groups).
2. **The footer becomes one account button** that opens a popover menu
   (Claude Desktop's `Bertrand · Chaps FR ⌄` menu): Settings, Appearance,
   Language, Documentation, version.
3. **Settings open in a large modal** over the chat (Claude Desktop's settings
   dialog: title + close, a left nav with a search box and grouped sections, the
   section content on the right), instead of replacing the chat area.

Why: the two left columns cost ~560px before the chat starts, and the
"select a collection to see its sessions" indirection hides every other
collection's sessions. A single tree shows everything at a glance and frees the
width for the chat.

## 2. Goals, non-goals, anti-goals

**Goals**
- One left column; every non-archived session reachable in the collection tree
  without first "selecting" a collection.
- Everything the middle-column toolbar did is still possible: search, sort,
  bulk select (move / archive / delete), import, new chat.
- Settings, Appearance, Documentation reached from the account menu, rendered in
  a modal; the chat behind stays mounted and untouched.
- Small screens keep working (the phone drawer becomes simpler: one column).

**Non-goals**
- No new route. The existing `/api/sessions` (collection filter, `q`,
  `offset/limit`, a blank `collection` = all collections) already serves the
  tree; `/api/collections` only gains an extra count field (§4.1).
- No change to what a collection *is* (single membership, General virtual,
  colours, context/profile, rename/delete cascade).
- No change to the new-tab landing page's session search (semantic + agent
  search) — it stays as is.
- No change to Settings section *content*; only where and how the sections are
  hosted and navigated.

**Anti-goals (would make this a failure even if it "works")**
- Losing a capability the middle column had (bulk actions, sort, search, import,
  drag-to-move, focus-follows-chat).
- A tree that fires a burst of requests or re-renders the whole list on every
  push event, making the sidebar janky while turns stream.
- A settings modal that silently loses unsaved edits, or that cannot hold the
  wide sections (Models cards, Agents tree, raw JSON editor, Documentation TOC).
- Regressing the collapsed rail or the phone drawer.

## 3. Layout of the new left column (top → bottom)

Approved mockup: [`2026-10-27-sidebar-tree-mockups/layout.html`](2026-10-27-sidebar-tree-mockups/layout.html) (frame 1).

1. **Header** — unchanged: logo, "Omnis", the Update button (when an update is
   available), the collapse toggle.
2. **Primary actions** (Claude Desktop's `Créer / Rechercher` block):
   - **New chat** — same button + squad chooser (`#new-chat`, `#squad-toggle`),
     but it now **opens a draft tab** in the focused pane (like Claude
     Desktop's "Créer") instead of creating the session at once. The
     collection is chosen on the draft (§3.1).
   - **Search** — a row that turns into an inline search field (§4.5).
   - **Import a conversation** — the former toolbar import button
     (`importSessionPrompt`).
3. **Collections section**
   - Header: label "Collections", a **Sort** button (⇅, opens the existing sort
     menu: recent / created / A–Z) and the **＋ New collection** button.
   - The **collection tree** (§4) — this is the scrolling region of the column.
4. **Archived** panel — stays a collapsible panel (§4.8).
5. **Files** panel — unchanged.
6. **Account button** (§5) — replaces `#sidebar-footer`'s three buttons and the
   `#sidebar-user` badge.

`#session-pane` (and its `#session-topbar`, `#session-list`, `#settings-menu`)
is removed from the markup.

### 3.1 Which collection a new chat goes into (decided by the user)

There is no "selected collection" any more, so a new session's collection is
always explicit:

- **A group's ＋** (§4.1) creates the session **directly in that collection**,
  immediately (as `newChat` does today, with the collection passed in).
- **Every other path opens a draft** whose landing page (the empty-pane
  `.pane-picker`) shows a **Collection selector** beside the existing Squad
  selector — the equivalent of Claude Desktop's "Choisir un environnement"
  popover. Those paths: the sidebar **New chat** button, a pane's **＋** tab,
  and Files → **"Open Chat here"** (which opens a draft with that folder pinned
  as its cwd instead of creating the session at once).
- The draft creates its session — in the **selected collection** — when the
  user clicks "Start a new chat" or sends the first message (the existing
  implicit-create paths, app.js ~1292, 9122, 10035, 11178, 11226).
- The selector lists every collection with its colour (General first). Its
  initial value is **the collection last chosen in a draft** (remembered per
  browser; General the first time; a deleted one falls back to General)
  *(my call — Claude Desktop pre-selects the last environment the same way)*.
- Picking a collection whose profile names a default squad shows that squad in
  the draft's Squad selector, unless the user picked a squad on this draft —
  the server's `resolveStartingSquad` precedence is unchanged.
- `newChat()` therefore takes the collection as an argument and stops reading
  `activeCollection`.

## 4. Collection tree

### 4.1 Group header
One row per collection, **General first** (as today's `/api/collections`
order), then user collections in their stored order:
- chevron (▾ open / ▸ closed), the collection glyph tinted with its colour
  (General keeps its star), the name, the **active (non-archived)** session
  count.
- **Server change:** today `collectionCounts` (`server/collections.go`) counts
  every non-hidden session *including archived ones*, so it would disagree with
  the group's paginated `total` (`archived=false`) — e.g. "14" above 4 rows and
  no "Show more". Add an `active` field to each `/api/collections` row (same
  folding rules, archived excluded) and keep `count` unchanged for any other
  consumer. The header shows `active`; "Show more (N)" uses the group's own
  `total − loaded`. Cover it with a server test.
- **Click** toggles open/closed. (There is no longer a "selected collection".)
- **On hover** the count is replaced by two buttons: **＋** (new chat filed in
  this collection — the collection profile then seeds squad/cwd exactly as a new
  chat in a selected collection does today) and **⋯** (menu).
- **⋯ / right-click menu**: the existing collection menu for user collections
  (rename, change colour, edit context…, delete) **plus "Select"** (enters bulk
  select, §4.6). General's menu only offers "Select".
- **Drop target**: dropping a dragged session on the header (or on the group's
  rows) moves it there (`moveSessionToCollection`), with the existing
  `drag-over` highlight.

### 4.2 Session rows
Indented under their group. **Single line** (approved mockup): busy dot +
title + hover actions (the existing ⋮ menu and delete button). The turn count,
last-used time and non-default squad move into the row's `data-tip` tooltip
(today they are a second line). The row keeps: click to open
(`selectSession`), `active` (open in a tab) and `active-focused` highlight with
the collection's `--col-accent` left rail, drag source, select-mode checkbox.

### 4.3 Open/closed state
- Remembered **per browser** in `localStorage` (collection name → open).
  Rename migrates the key; delete drops it. Default for a collection never seen:
  **open**. *(my call)*
- **Focus follows the chat**: `syncCollectionToSession` no longer "selects" a
  collection; it **opens the focused chat's group** (if closed, and remembers
  it open), makes sure that session's row is loaded, and scrolls it into view.
  The `fetchSessionMeta` + "only if still focused" guards stay.

### 4.4 Loading and pagination
- `GET /api/collections` gives the groups and counts.
- Each **open** group fetches its own page:
  `GET /api/sessions?collection=<name>&archived=false&sort=<sort>&offset=0&limit=10`.
  A closed group fetches nothing until opened.
- A group with more sessions than loaded ends with **"Show more (N)"**, which
  loads the next 20 *(page sizes: my call)*. No infinite scroll inside groups.
- Each group keeps its own view state (`loaded/total/exhausted/loading/seq`),
  generalising today's single `activeView`; the per-view monotonic `seq`
  stale-response guard is kept per group.
- **Live refresh must be targeted.** `loadSessions()` is called today after
  every finished turn, every routing chip, every pushed turn and every
  session/collection push (app.js ~8391–8412, 8861, 9479, 9662). Re-fetching
  every open group on each of those would be K+2 requests per event per browser
  — the burst the anti-goals rule out. Instead:
  - an event about **one session** (turn end, routing, pushed turn,
    `session_created/renamed/deleted`) re-fetches the loaded prefix of **that
    session's group only** (resolved through `sessionMeta` /
    `effectiveCollection`; unknown ⇒ General) + one `/api/collections` for the
    counts; the turn-end and routing callers pass the session id they already
    have;
  - `session_moved` refreshes the **old and new** groups;
  - `collections_changed` (create/rename/delete/recolour) re-renders the group
    list and re-fetches the open groups — rare, so acceptable;
  - the Archived panel is refreshed only by events that can change it
    (archive/unarchive/delete, and the existing archived callers).
  Re-fetches happen in place, preserving the column's scroll position and the
  per-group `seq` stale guard — the same contract `reloadActivePrefix` has
  today. Closed groups are never fetched.
- **A focused session beyond its group's loaded rows** (the API can't say where
  it sits in a sorted group) is fetched alone (`fetchSessionMeta`) and shown
  **pinned after the group's loaded rows**, slightly separated, until a "Show
  more" page naturally contains it (then it moves into place). No paging until
  found, no `limit=total`.

### 4.5 Search
- Clicking **Search** swaps the row for an input (Esc / ✕ closes, restoring the
  tree). Debounced (~150 ms like today).
- With a query, the tree shows **matches from all collections**, grouped under
  their collection header, all such groups open, empty groups hidden. Uses one
  collection-less query `GET /api/sessions?q=<q>&archived=false&limit=50` and
  groups client-side with `effectiveCollection`. The Archived panel is filtered
  by the same query (as today).
- Closing search restores the previous open/closed states.

### 4.6 Bulk select
- Entered from a group's **⋯ → Select**. A **selection bar** appears at the top
  of the Collections section (count · Select all · Actions · ✕), replacing the
  former toolbar select row.
- Every loaded, visible row (all groups) shows its checkbox; a row click toggles
  it. "Select all" ticks the rows of the group from which selection was entered
  *(my call)*. **Actions** = the existing batch menu (move to collection,
  archive, delete) via `runBatch`.
- Search and select stay mutually exclusive, as today.

### 4.7 Sort
The global sort (recent / created / A–Z, persisted in `localStorage`) applies
**within every group**. The Today/Yesterday/… timeframe headers are dropped
*(my call — they don't fit inside small groups; Claude Desktop has none)*.

### 4.8 Archived panel
Stays a collapsible panel above Files. Since there is no selected collection
anymore it lists **all archived sessions** (flat, recent order, existing
50 + 5 infinite scroll), filtered by the search query when one is active. Rows
are single-line like the tree.

### 4.9 Collapsed rail
When the sidebar is collapsed to its icon rail: New chat (+), a Search icon
(expands the sidebar and opens search), the collection pastilles (as today:
initials badge / General star) and the account avatar at the bottom (opens the
same menu). Clicking a pastille **expands the sidebar, opens that group and
scrolls to it**. Archived and Files stay hidden in the rail, as today.

## 5. Account button + menu

Approved mockup: [`2026-10-27-sidebar-tree-mockups/layout.html`](2026-10-27-sidebar-tree-mockups/layout.html) (frame 2).

- **Button** (bottom of the column): avatar disc with an initial + label + ⌄.
  Label = the login from `GET /api/whoami` when identity is meaningful (today's
  `loadWhoami` rule: not the single-user `web-user`, or cookie mode); otherwise
  **"Omnis"** *(my call)*.
- **Menu** (popover anchored above the button, closes on outside click / Esc /
  item click):
  - identity line (the login) — only when a login is shown;
  - **Settings** — `Ctrl+,` (`Cmd+,` on macOS) shown and bound globally;
  - **Appearance**;
  - **Language** — opens the modal on Appearance, scrolled to its Language
    section;
  - **Documentation**;
  - separator, then **version** (current version from `/api/update/status`)
    and, when an update is available, an **Update to vX** item that opens the
    existing update dialog. The header Update button stays.
- Styled as the app's existing context menus (themes apply).

## 6. Settings modal

Approved mockup: [`2026-10-27-sidebar-tree-mockups/settings-modal.html`](2026-10-27-sidebar-tree-mockups/settings-modal.html).

- A modal overlay (backdrop + dialog) appended to `body`; the chat panes stay
  mounted behind it. The `chat--settings` takeover of `#chat` is no longer used.
- **Size**: `min(1200px, 94vw) × min(860px, 90vh)` on desktop; **full-screen**
  below the 820px breakpoint *(my call)*.
- **Header**: "Settings" + ✕.
- **Left nav**: a search box filtering items by their (translated) label —
  empty groups hide — then grouped items:

  | Group | Items (existing section id) |
  |---|---|
  | Preferences | Appearance (`appearance`) |
  | Agents | Agents & squads (`agent`), Skills (`skills`), Commands (`user-commands`), Automation (`automation`) |
  | Connections | Models (`models`), MCP (`mcp`), A2A (`a2a`), Registries (`registries`) |
  | Security | Permissions (`permissions`), Hooks (`hooks`) |
  | Help | Documentation (`documentation`) |

  The "Raw JSON" nav entry is dropped and replaced by the **Form / Raw JSON
  toggle** already built in the section toolbar (`.settings-view-toggle` in
  `.settings-body-toolbar`). That toggle is **hidden today**
  (`web/css/settings/sidebar.css`: `#settings-panel .settings-view-toggle,
  .settings-body-toolbar { display: none }`) — un-hide it, and keep it hidden
  for client-only sections (Skills, Appearance, Documentation, Commands,
  Registries, Automation), as `isClientOnly` already decides.
- **Right side**: the existing settings panel content — section title (the
  breadcrumb becomes just the section title), Form/Raw toggle, Discard / Save,
  status, the post-save **Reload / Restart banner**, the section body, and the
  **Settings assistant** FAB + drawer (now inside the modal).
- **Opening**: account menu → Settings opens the last config section visited
  (default Skills, as `open()` does today); Appearance / Documentation / Language
  open their own section. `window.Settings` today exposes `{open, close, isOpen,
  prefsReady, saveNotifications, refreshSchedules}` — keep those working for
  every existing caller and **add `openSection(id[, anchor])`** (the internal
  `jumpToSection`, minus its toggle-to-close behaviour) for the account menu;
  `anchor` lets Language scroll Appearance to its Language section.
- **Closing**: ✕ or Esc. A click on the backdrop does **not** close *(my call —
  the sections are heavy forms)*. **Unsaved edits are kept, with no prompt** —
  exactly today's behaviour: `close()` asks nothing and the dirty
  `state.parsed`/`state.raw` cache survives, so reopening shows the edits and
  Save still saves them; `beforeunload` still guards a page reload. This keeps
  code-driven closes (`selectSession`, `newChat`, the Automation run-history
  link) synchronous. *(Correction: an earlier draft promised a discard
  confirmation on close "as today" — there is none today, and adding one would
  only guard edits that are not at risk.)* Optional polish: a dot on the
  account button / Settings item while unsaved edits exist.
- **Reload banner when closed**: a pending reload/restart must stay visible
  after the modal is closed — keep today's chat-area banner for that case.
- The FOOTER_ITEMS special-casing (Appearance/Documentation as "autonomous"
  sections that hide the category menu) disappears: every section is a normal
  nav item.

## 7. Small screens (< 820px)

The drawer becomes a single full-height column (no more sidebar-over-session-pane
stacking); `body.nav-open`, the scrim, closing the drawer on session open / new
chat stay. The settings modal is full-screen. The touch rules that reveal
hover-only controls also reveal the group header ＋ / ⋯ buttons. Update
`responsive.css` and its comments accordingly (§"Web UI small-screen layout" in
CLAUDE.md).

## 8. What is removed

`#session-pane`, `#session-topbar` (normal/search/select rows), `#session-list`,
`#settings-menu`, `#sidebar-footer`'s three buttons, `#sidebar-user`,
`activeCollection` as a *selection* (the rail highlight + middle-list filter),
timeframe headers, the two-line session row.

## 9. Housekeeping

- i18n: new keys (account menu, nav groups, search/select/show-more, Language)
  in `en/fr/es/de`, then `make i18n`; reuse existing `sessionbar.*`,
  `collections.*`, `settings.menu.*` keys where the wording still fits; drop
  dead keys.
- Bump the `?v=` of `app.js`, `settings.js`, `i18n/locales.js`, `i18n.js` in
  `index.html`.
- Docs: `web/docs/03-sessions.md`, `web/docs/04-settings-panel.md`,
  `web/docs/01-getting-started.md` (where they describe the columns/footer).
- `internal/features/FEATURES.md`: one bullet under the in-development minor.
- CLAUDE.md: rewrite the "Session collections (thematic folders, Web UI
  three-pane layout)", "Session list pagination", "Web UI small-screen layout"
  sections and the Settings-panel mentions to the new structure.

## 10. Verification

No JS test harness exists; verify in a real browser with the branch's web
assets (`OMNIS_WEB_DIR=$(pwd)/web`, see the web-UI smoke recipe): tree
open/close + persistence, show-more, focus-follows-chat for a session in a
closed group and beyond the loaded prefix, live push refresh while a turn
streams, search across collections, bulk select/move/archive/delete, drag to
move, sort, import, collapsed rail, account menu + `Ctrl+,`, settings modal
(each section, raw toggle, save + reload banner, unsaved-close confirm, assistant
drawer, Documentation TOC), dark/light themes, and a 390px phone viewport.
`go test ./server/...` must stay green, plus a new test pinning the
`/api/collections` `active` count (archived excluded, unknown collections folded
into General, cookie-mode scoping unchanged). Also check the request count in
the browser's network panel while a turn streams: one group + one counts
request per event, not one per open group.

## 11. Calls I made (please check)

- The draft's Collection selector starts on the collection last chosen in a
  draft (General the first time).
- Files → "Open Chat here" now opens a draft (folder pinned) rather than
  creating the session immediately, so the collection can be chosen.
- Groups open by default, open state remembered per browser.
- Page sizes: 10 per group, +20 per "Show more".
- Single-line rows; turns/time/squad moved to the tooltip.
- Timeframe headers dropped.
- "Select all" scopes to the group selection was entered from.
- Account button shows "Omnis" when there is no meaningful login.
- Settings modal: backdrop click doesn't close; full-screen on phones.
- Closing Settings keeps unsaved edits without a prompt (today's behaviour) —
  replaces the confirmation I wrongly described as existing.
- Archived panel lists all collections' archived sessions.
- Header count = active sessions only, via a new `active` field on
  `/api/collections` (the only server change).
- Live refresh targets the event's own group, not every open group.
- A focused session beyond its group's loaded rows is pinned after them.
- Mockups copied into the repo next to this spec.

## 12. Left to the builder

CSS details and exact sizes, sidebar default width (still resizable), the
markup/ids of the new elements, whether the account menu reuses
`showFolderCtxMenu` or a dedicated popover, how per-group view state is
structured, and the order of the migration steps.
