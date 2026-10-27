# Sidebar collection tree, account menu, settings modal — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the web UI's middle session column with a foldable per-collection tree in the left sidebar, replace the sidebar footer with an account menu, and host Settings in a modal — as specified.

**Architecture:** Vanilla JS/CSS, no build step. `web/app.js` owns the sidebar (tree, account menu, drafts); `web/settings.js` (an IIFE exposing `window.Settings`) owns the settings modal. One small Go change adds an active-only count to `GET /api/collections`. Every other data need is met by the existing paginated `GET /api/sessions`.

**Tech Stack:** Go (gin) server, vanilla JS, CSS partials under `web/css/`, i18n JSON catalogues + `make i18n`.

**Spec:** [docs/superpowers/specs/2026-10-27-sidebar-tree-settings-modal-design.md](../specs/2026-10-27-sidebar-tree-settings-modal-design.md) — read it before any task; mockups in `docs/superpowers/specs/2026-10-27-sidebar-tree-mockups/`.

## Global Constraints

- Web UI strings: every new user-visible string is an i18n key in `web/i18n/en.json` **and** `fr.json`, `es.json`, `de.json`, then `make i18n` (commit the regenerated `web/i18n/locales.js`). Never use a `title` attribute — tooltips are `data-tip` (CLAUDE.md "Web UI tooltips"). JS translation function is `tr`/`trN`; never shadow it.
- Do not translate product nouns/identifiers (`Omnis`, `MCP`, `A2A`, `Squad`, `Skills`, `Hooks`).
- When `app.js`, `settings.js`, `i18n/locales.js`, `i18n.js` or a CSS barrel's import list changes, bump its `?v=` in `web/index.html`.
- CSS: rules go in the partial that owns the feature; a new partial needs a barrel `@import`, a `?v=` bump, and a row in `web/css/CLAUDE.md`.
- Page sizes: tree groups load **10**, then **+20** per "Show more"; the Archived panel keeps **50** then **+5** (infinite scroll).
- localStorage keys introduced: `agent_collection_open` (JSON `{lowercased name: bool}`), `agent_draft_collection` (last collection chosen on a draft). Existing `agent_session_sort` is kept.
- Small-screen breakpoint stays **820px** (`MOBILE_MQ` in app.js and `responsive.css` must agree).
- No new HTTP route. The only server change is the `active` field (Task 1).
- `go test ./server/...` stays green after every task that touches Go.

## Smoke environment (used by every UI task's verification)

Isolated state so test data never touches the real `~/.omnis`:

```bash
make build-server
export SMOKE_HOME=$(mktemp -d)
OMNIS_HOME=$SMOKE_HOME OMNIS_SERVER_ADDR=127.0.0.1:8848 OMNIS_WEB_DIR="$(pwd)/web" bin/omnis-server --no-browser &   # no token ⇒ no auth
```

Seed (re-runnable; creates collections `Kubernetes` (blue), `IA Parc` (amber), 25 General sessions, 4 Kubernetes, 1 archived Kubernetes):

```bash
B=http://127.0.0.1:8848/api
curl -s -XPOST $B/collections -d '{"name":"Kubernetes","color":"blue"}'
curl -s -XPOST $B/collections -d '{"name":"IA Parc","color":"amber"}'
for i in $(seq 1 25); do id=$(curl -s -XPOST $B/sessions -d '{}' | jq -r .session_id); curl -s -XPATCH $B/sessions/$id -d "{\"title\":\"General chat $i\"}"; done
for i in 1 2 3 4 5; do id=$(curl -s -XPOST $B/sessions -d '{"collection":"Kubernetes"}' | jq -r .session_id); curl -s -XPATCH $B/sessions/$id -d "{\"title\":\"K8s chat $i\"}"; done
curl -s -XPOST $B/sessions/$id/archive
```

Drive the browser with the Playwright MCP at `http://127.0.0.1:8848/`. Delete `.playwright-mcp/` afterwards; `kill %1` and `rm -rf $SMOKE_HOME` when done.

## Review Focus

1. **Collection renamed or deleted while its group is open** — the open-state key follows the rename, the group keeps its open state, and a deleted collection's sessions appear under General without a reload. (Task 4)
2. **Focused session the tree has not loaded** (boot-restored tab in a closed group, or past the group's first 10 rows) — its group opens, the row shows pinned after the loaded rows, highlighted, and moves into place once "Show more" reaches it. (Task 4)
3. **Event storm while a turn streams** — rename/turn-end/routing pushes for one session cost at most one group request + one `/api/collections` request per coalescing window, never one request per open group. (Task 6)
4. **Settings closed with unsaved edits by a code path** (`selectSession`, `newChat`, Esc) — edits survive and reappear on reopen; Esc never closes the modal while a confirm dialog or the assistant drawer is open. (Task 2)
5. **Search then close** — matches from collapsed groups and the Archived panel appear while searching; closing search restores every group's previous open/closed state and the loaded rows; select mode cannot be entered while searching. (Task 5)

---

### Task 0: Branch and commit the spec

**Files:** none modified; commits the existing spec + mockups.

- [ ] **Step 1:** `git switch -c feat/sidebar-tree-settings-modal develop`
- [ ] **Step 2:** Commit the design:

```bash
git add docs/superpowers/specs/2026-10-27-sidebar-tree-settings-modal-design.md docs/superpowers/specs/2026-10-27-sidebar-tree-mockups docs/superpowers/plans/2026-10-27-sidebar-tree-settings-modal.md
git commit -m "docs: design + plan for sidebar collection tree and settings modal"
```

---

### Task 1: Active-only count on `GET /api/collections`

**Files:**
- Modify: `server/collections.go:14-62` (`collectionInfo`, `collectionCounts`), `server/collections.go:66-97` (`handleListCollections`)
- Test: `server/collections_active_test.go` (new)

**Interfaces:**
- Produces: each `/api/collections` row gains `"active": <int>` — non-hidden, **non-archived** sessions folded exactly like `count` (blank/unknown collection ⇒ General, cookie-mode owner scoping unchanged). `count` keeps its current meaning. Go: `collectionInfo.Active int \`json:"active"\``; `collectionCounts(d serverDeps, login string, known []string) (all, active map[string]int)`.

- [ ] **Step 1: Write the failing test** `TestListCollectionsActiveExcludesArchived` in `server/collections_active_test.go`, harness copied from `server/collections_ctx_test.go` (`t.Setenv("OMNIS_HOME", t.TempDir())`, `sessions.AddCollection("Acme")`, `reg := sessions.NewEmptyRegistry()`, `newEngine(serverDeps{Registry: reg, rootCtx: context.Background()})`). Seed via `reg.New("")` + `reg.SetCollection` / `reg.SetArchived` / `reg.SetHidden`:
  - Acme: 2 sessions, 1 archived
  - General: 1 plain, 1 archived, 1 filed under unknown `"Gone"` (folds to General), 1 hidden

  Assert on `GET /api/collections`:
  ```go
  // General
  if g.Count != 3 || g.Active != 2 { t.Fatalf("General = %+v, want count 3 active 2", g) }
  // Acme
  if a.Count != 2 || a.Active != 1 { t.Fatalf("Acme = %+v, want count 2 active 1", a) }
  ```
- [ ] **Step 2:** `go test ./server -run TestListCollectionsActiveExcludesArchived -v` → FAIL (`Active` is 0 / field missing).
- [ ] **Step 3:** Implement: extend `collectionInfo`, make `collectionCounts` return `(all, active)` (skip `m.Archived` for `active`), fill `Active` for General and each user collection in `handleListCollections`.
- [ ] **Step 4:** `go test ./server/... ` → PASS (incl. `TestCollectionsArePerUser`).
- [ ] **Step 5:** Commit `feat(server): report active (non-archived) session count per collection`.

---

### Task 2: Settings in a modal

**Files:**
- Modify: `web/settings.js` — `ensurePanel` (~648), `buildSidebarMenu` (~717, delete), `syncActiveHighlight`/`updateBreadcrumb` (~749-787), `open`/`close`/`jumpToSection` (~9874-9921), `ensureBanner`/`refreshBannerVisibility` (~384-452), `renderAppearance` language section (~1201), public API (~10461), boot wiring (~10462-10479)
- Create: `web/css/settings/modal.css` (+ `@import` in `web/css/settings.css`, `?v=` bump, row in `web/css/CLAUDE.md`)
- Modify: `web/css/settings/sidebar.css` (delete every `#settings-menu*` rule and the `#session-pane` swap rules at ~150-175; delete the rule hiding `.settings-view-toggle, .settings-body-toolbar` at ~125-130), `web/css/settings/theme.css:81-82` (delete the `#chat.chat--settings` pane-hiding rules), `web/css/settings/core.css` (`#settings-panel` becomes a flex child of the modal, not of `#chat`)
- Modify: `web/index.html` (delete `#settings-menu` block), `web/app.js` (delete the `SIDEBAR_KEEPS_SETTINGS` capture handler ~11720-11732 — the modal backdrop now blocks the sidebar; drop `rebuildChatDOM`'s `#settings-panel` preservation ~395-401, the panel no longer lives in `#chat`)
- i18n: `settings.group.preferences` "Preferences", `settings.group.agents` "Agents", `settings.group.connections` "Connections", `settings.group.security` "Security", `settings.group.help` "Help", `settings.nav.agent` "Agents & squads", `settings.nav.search` "Search settings", `settings.modal.close` "Close settings", `settings.nav.empty` "No matching section"

**Interfaces:**
- Produces:
  - `MENU_GROUPS` (settings.js): `[{ key: "preferences", ids: ["appearance"] }, { key: "agents", ids: ["agent","skills","user-commands","automation"] }, { key: "connections", ids: ["models","mcp","a2a","registries"] }, { key: "security", ids: ["permissions","hooks"] }, { key: "help", ids: ["documentation"] }]` — the nav order. `MENU_ITEMS` stays the label/kind source. `FOOTER_ITEMS`, `RAW_VIEW_ID`'s nav entry and `buildSidebarMenu` are removed.
  - DOM: `#settings-modal` (backdrop, `hidden` when closed) > `.settings-modal-dialog` > `.settings-modal-head` (title + `.settings-modal-close`) + `.settings-modal-main` > `nav.settings-modal-nav` (`input#settings-nav-search`, `.settings-nav-group` headings, `button.settings-nav-item[data-file]`) + `#settings-panel` (existing panel, moved in).
  - `window.Settings = { open, close, isOpen, openSection, prefsReady, saveNotifications, refreshSchedules }` where `openSection(id: string, anchor?: string): Promise<void>` opens the modal on section `id` (never toggles closed) and, after render, scrolls `#<anchor>` into view.
  - Appearance's language `<section>` gets `id="appearance-language"`.
  - Global shortcut: `Ctrl+,` / `Cmd+,` → `open()` (preventDefault), bound in settings.js boot.

- [ ] **Step 1:** Build the modal scaffolding in `ensurePanel`: create `#settings-modal`, append to `document.body`, move the existing panel markup inside `.settings-modal-main`; the breadcrumb shows only `tr("settings.title." + id)`. Render the nav from `MENU_GROUPS` with `ICONS[id]` and `tr(id === "agent" ? "settings.nav.agent" : "settings.menu." + id)`.
- [ ] **Step 2:** Nav search: on `input`, show items whose translated label or id contains the lowercased query; hide a group with no visible item; show `settings.nav.empty` when nothing matches.
- [ ] **Step 3:** Rewrite `open()` / `close()` / add `openSection()`: toggle `#settings-modal[hidden]`; no `chat--settings` class; `syncActiveHighlight` only highlights `.settings-nav-item.active`. `close()` keeps `state.parsed`/`state.raw` untouched (no confirm — spec §6). Keep `jumpToSection` internal as an alias calling `openSection`.
- [ ] **Step 4:** Esc: one `keydown` listener (capture) active while open — if the assistant drawer is open → `assistantHide()`; else if any `.ui-modal-overlay`, `#user-cmd-modal-overlay:not([hidden])`, `#skill-name-modal-overlay:not([hidden])` or `.agent-info-*` modal is visible → ignore; else `close()`. Backdrop clicks do nothing.
- [ ] **Step 5:** Form / Raw toggle visible: remove the hiding rule; hide `.settings-body-toolbar` only under `.settings-panel--client-only` (in `core.css`).
- [ ] **Step 6:** Banner placement: add `placeBanner()` — while `state.open` the `#restart-banner` element is inserted at the top of `#settings-panel` (above `.settings-body`), otherwise at the top of `#chat-area`; call it from `open`, `close`, `refreshBannerVisibility`, `ensureBanner`.
- [ ] **Step 7:** `modal.css`: dialog `width: min(1200px, 94vw); height: min(860px, 90vh)`; nav 220px; backdrop uses existing overlay tokens; `@media (max-width: 820px)` → dialog `inset: 0; width: 100vw; height: 100dvh; border-radius: 0` and nav becomes a horizontal scroller above the content. Themes must apply (use tokens only).
- [ ] **Step 8:** Keep the footer buttons working for now: `#settings-btn` → `open()`, `#appearance-btn` → `openSection("appearance")`, `#documentation-btn` → `openSection("documentation")` (Task 3 replaces them).
- [ ] **Step 9:** i18n keys (4 locales) + `make i18n`; bump `?v=` for settings.js, settings.css, locales.js.
- [ ] **Step 10: Verify** (`node --check web/settings.js web/app.js`, then smoke environment + Playwright):
  - Gear → modal over the chat, last section (default Skills) active; chat pane still visible behind the backdrop.
  - Each nav item renders its section; Models shows the Form/Raw toggle, Skills/Appearance don't.
  - Typing "mod" in the nav search leaves only Models; "zzz" shows "No matching section".
  - Edit a field in Models, press Esc → closes; reopen → edit still there, Save enabled.
  - Edit a field, then `window.Settings.close()` via `selectSession` (click a session row) → reopen shows the edit (Review Focus 4).
  - Open the assistant drawer, press Esc → drawer closes, modal stays.
  - Trigger a `uiConfirm` (Discard with an edit) and press Esc → modal stays.
  - `localStorage.setItem("agent_toolkit_restart_required","1")` + reload → banner in `#chat-area`; open modal → banner inside the modal; close → back in chat area.
  - `window.Settings.openSection("appearance","appearance-language")` scrolls to Language.
  - `Ctrl+,` opens the modal.
  - 390×844 viewport → modal full-screen.
- [ ] **Step 11:** Commit `feat(web): host Settings in a modal with grouped, searchable nav`.

---

### Task 3: Account button and menu

**Files:**
- Modify: `web/index.html` — replace `#sidebar-footer`'s three buttons and `#sidebar-user` with the account button
- Modify: `web/app.js` — replace `loadWhoami` (~8680) with `loadAccountIdentity`; add account-menu code beside it; boot call site
- Modify: `web/settings.js` — delete Task 2's Step 8 footer-button wiring and the `#settings-btn/#appearance-btn/#documentation-btn` highlight code
- Modify: `web/css/settings/sidebar.css` (delete the footer-button rules) and `web/css/features/sidebar.css` (account button + menu styles; delete `#sidebar-user` rules ~650-671)
- i18n: `account.menu` "Account menu", `account.language` "Language", `account.version` "Omnis {version}", `account.updateTo` "Update to {version}"; reuse `settings.label`, `settings.menu.appearance`, `settings.menu.documentation`

**Interfaces:**
- Consumes: `window.Settings.open()`, `window.Settings.openSection(id, anchor)` (Task 2); `openUpdateDialog()` (app.js ~12236); `GET /api/whoami` → `{user_id, identity_mode}`; `GET /api/update/status` → `{current, latest, available}`.
- Produces: DOM `#sidebar-footer > button#account-btn` (`.account-avatar` initial disc, `.account-name`, chevron) and `#account-menu` (body-appended popover, `hidden` when closed); functions `loadAccountIdentity(): Promise<void>`, `openAccountMenu(): void`, `closeAccountMenu(): void`.

- [ ] **Step 1:** Markup + `loadAccountIdentity`: label = `user_id` when `!(user_id === "web-user" && identity_mode !== "cookie")` (today's `loadWhoami` rule), else `"Omnis"`; avatar = first letter uppercased.
- [ ] **Step 2:** Menu items, in order: identity line (only when a login is shown, non-clickable), Settings (shortcut hint `Ctrl+,`, or `⌘,` when `navigator.platform` is Mac), Appearance, Language (`openSection("appearance","appearance-language")`), Documentation, separator, version line `account.version` (from `/api/update/status` `current`; omitted when unavailable or `dev`), and `account.updateTo` → `openUpdateDialog()` only when `available`. Anchored above the button, aligned left; closes on outside click, Esc, item click, scroll, resize.
- [ ] **Step 3:** Collapsed rail: the button shows only the avatar; the menu opens anchored to it.
- [ ] **Step 4:** i18n (4 locales) + `make i18n`; bump `?v=` for app.js, settings.js, locales.js, styles.css/settings.css if imports changed.
- [ ] **Step 5: Verify** (`node --check`, smoke + Playwright):
  - Footer shows avatar "O" + "Omnis" (no token ⇒ single-user).
  - Menu shows Settings / Appearance / Language / Documentation / version; no identity line.
  - Each item opens the modal on the right section; Language lands scrolled to the language cards.
  - Esc and outside click close the menu.
  - Collapsed rail: avatar only, menu still opens.
  - Dark + light theme screenshots look themed.
- [ ] **Step 6:** Commit `feat(web): replace sidebar footer with an account menu`.

---

### Task 4: Collection tree replaces the middle column

The largest task: after it, the middle column is gone and every collection's sessions live under it in the sidebar. Search, select and sort controls arrive in Task 5; targeted refresh in Task 6.

**Files:**
- Modify: `web/index.html` — delete `<aside id="session-pane">` entirely; in `#sidebar` add the primary-actions rows (Search row `#sidebar-search-btn`, Import row `#sidebar-import-btn`) under `#new-chat-wrap`; Collections header gets `#collections-sort` (⇅) before `#collections-new`; rename `ul#collections-list` → `ul#collection-tree`
- Modify: `web/app.js` — state (~1465-1503), `buildSessionRow` (~6105), session-list functions (~6303-6561), collection rail functions (~6628-6762, 7323-7445), `wireSessionBar` (~7576), `refreshSidebarActive` (~359), `els` map (~141-160), boot (~13146)
- Modify: `web/css/features/collections.css` (drop `#session-pane`/`#session-topbar` rules; add tree rules), `web/css/features/sidebar.css` (session-row rules become single-line; drop `.session-group` header rules)
- i18n: `tree.showMore` "Show more ({count})", `tree.empty` "No chats yet", `collections.newChatIn` "New chat in {name}", `sidebar.search` "Search", `sidebar.import` "Import a conversation"

**Interfaces:**
- Consumes: `/api/collections` rows `{name, count, active, color, general, squad, cwd}` (Task 1); `GET /api/sessions?collection=&archived=&sort=&offset=&limit=&q=`.
- Produces (later tasks rely on these exact names):
  - Constants `GROUP_PAGE_INITIAL = 10`, `GROUP_PAGE_MORE = 20`; key `COLLECTION_OPEN_KEY = "agent_collection_open"`.
  - `groupViews: Map<string /*lowercased name*/, {loaded, total, loading, exhausted, seq, pinnedId: string|null}>`
  - `isGroupOpen(name): boolean` (default `true`), `setGroupOpen(name, open: boolean): void` (persists)
  - `renderCollectionTree(): void` — (re)builds `li.collection-group[data-name]` > `.collection-row` header + `ul.collection-sessions` + `li.group-more` from `collectionsData`, preserving existing groups' loaded rows when the collection set is unchanged
  - `groupQuery(name, offset, limit): string`, `loadGroupMore(name): Promise<void>`, `reloadGroupPrefix(name): Promise<void>` (in place, keeps `#sidebar` scroll), `resetGroup(name): void`
  - `loadSessions(): Promise<void>` — **full** refresh: `loadCollections()` + `reloadGroupPrefix` for each open group + `reloadArchivedPrefix()`
  - `revealSessionInTree(id): Promise<void>` — replaces `syncCollectionToSession`
  - `newChat(panel, squadOverride, dirOverride, collectionOverride)` — 4th param added; for now `collectionOverride ?? GENERAL_COLLECTION` (Task 7 adds the draft fallback). `activeCollection` is deleted.

- [ ] **Step 1:** Markup changes (index.html) and `els` map update; delete `els.list/sessionPane/sessionBar*/sessionSearch*/sessionSelect*`, add `els.tree`, `els.collectionsSort`, `els.sidebarSearchBtn`, `els.sidebarImportBtn`.
- [ ] **Step 2:** Group header (`buildCollectionRow` reworked): chevron, glyph/star, name, `active` count; hover actions `button.group-new` (＋, `data-tip` = `collections.newChatIn`) → `newChat(fp(), undefined, undefined, c.name)`, and `button.group-menu` (⋯) → `openCollectionCtxMenu` (General: menu with only the Task 5 "Select" entry — until then General shows no ⋯). Header click → `setGroupOpen(name, !isGroupOpen(name))` + load if opening. Right-click keeps today's menu. Drop target on header and on `ul.collection-sessions` → `moveSessionToCollection`.
- [ ] **Step 3:** Rows: `buildSessionRow` becomes single-line (busy dot, title, actions); the `.session-bottom-row` goes; `data-tip` on the row = `"<title> · <turns> turns · <time>"` plus `" · <squad>"` for a non-default squad. Keep `session-abbr`, `session-check`, drag source, `active`/`active-focused`, `--col-accent`.
- [ ] **Step 4:** Per-group paging: `loadGroupMore` fetches `GROUP_PAGE_INITIAL` first then `GROUP_PAGE_MORE`, `seq`-guarded like `loadMoreActive`; `li.group-more` shows `tree.showMore` with `total − loaded` and is hidden when exhausted. A closed group fetches nothing. Delete `activeView`, `appendActiveRows`, sentinel/observer code for the active list, timeframe header code (`sessionTimeframe`, `buildTimeframeHeader`).
- [ ] **Step 5:** Open state: `isGroupOpen`/`setGroupOpen` over `COLLECTION_OPEN_KEY`; `renameCollection` migrates the key (and the `groupViews` entry), `deleteCollection` drops it (its sessions refetch under General via `loadSessions()`).
- [ ] **Step 6:** Archived panel: `sessionsQuery(true, …)` no longer sends `collection`; rows use the single-line row.
- [ ] **Step 7:** `revealSessionInTree(id)` (called from `setFocusedPanel` where `syncCollectionToSession` was): resolve meta (`sessionMetaById` → `fetchSessionMeta`, keep the "still focused" guard); archived ⇒ no-op; open its group (`setGroupOpen(name, true)`, load if needed); if the row is not among loaded rows set `view.pinnedId = id` and render it after the loaded rows with class `pinned` (a pinned row is dropped as soon as a page contains it); scroll the row into view (`block: "nearest"`).
- [ ] **Step 8:** `refreshSidebarActive` iterates `els.tree.querySelectorAll("li[data-id]")` and `els.archivedList` rows.
- [ ] **Step 9:** Primary actions: `#sidebar-import-btn` → `importSessionPrompt()`; `#sidebar-search-btn` wired in Task 5 (leave it present, no-op). Sort button wired in Task 5.
- [ ] **Step 10:** CSS: tree indentation (rows indented under headers, header sticky not required), hover reveal of `.group-new/.group-menu` replacing the count, `.pinned` row with a top hairline, `.group-more` as a muted text button. Remove obsolete `#session-pane`/`#session-topbar`/`.session-group` rules.
- [ ] **Step 11:** i18n (4 locales) + `make i18n`; bump `?v=` (app.js, locales.js, styles.css if needed).
- [ ] **Step 12: Verify** (`node --check`, smoke + seed + Playwright):
  - No middle column; chat starts right after the sidebar.
  - General shows 10 rows + "Show more (15)"; clicking it shows 25 and the button disappears.
  - Kubernetes header shows **4** (not 5: one archived); Archived panel shows 1 row.
  - Collapse Kubernetes, reload → still collapsed; expand → rows load.
  - Hover Kubernetes → ＋ and ⋯ replace the count; ＋ creates a chat filed under Kubernetes (header becomes 5).
  - Drag a General row onto IA Parc → moves; counts update.
  - Rename IA Parc → "IA Parc 2" while open, reload → still open (Review Focus 1). Delete it → its session shows under General.
  - Open "General chat 1" (row 25 under sort recent → beyond the first 10) via `selectSession(id)` in the console after a reload → General opens, row pinned + highlighted; "Show more" moves it into place (Review Focus 2).
  - Row tooltip shows turns · time.
- [ ] **Step 13:** Commit `feat(web): collection tree replaces the middle session column`.

---

### Task 5: Search, sort and bulk select in the tree

**Files:**
- Modify: `web/app.js` — search/select/sort code (~7445-7598 reworked), collection ⋯ menu (`openCollectionCtxMenu` ~6764), `wireSessionBar` → `wireTreeControls`
- Modify: `web/index.html` — `#sidebar-search-row` (input `#sidebar-search-input` + close button, hidden) next to the Search button; `#tree-select-bar` (count `#tree-select-count`, `#tree-selectall-btn`, `#tree-batch-btn`, `#tree-select-cancel`, hidden) at the top of the Collections section
- Modify: `web/css/features/collections.css` (search row, select bar, `#sidebar.selecting` checkbox rules replacing `#session-pane.selecting`)
- i18n: reuse `sessionbar.*` keys (search placeholder/close, sort*, select, selectAll, actions, selected, batchDelete*); add `tree.noMatch` "No matching chats"

**Interfaces:**
- Consumes: Task 4's `renderCollectionTree`, `resetGroup`, `loadSessions`, `groupViews`, `isGroupOpen`.
- Produces: `setTreeSearch(query: string): void` (empty string = leave search mode), `enterSelectMode(scopeName: string): void`, `exitSelectMode(): void`, `setSessionSort(key: "recent"|"created"|"az"): void` (now resets every open group).

- [ ] **Step 1:** Search: Search row click → input shown and focused; debounced 150 ms, the committed query stored in the existing `sessionSearch` global (which `sessionsQuery` already sends as `q` for the Archived panel); non-empty query → one request `GET /api/sessions?archived=false&q=<q>&sort=<sort>&offset=0&limit=50` (no `collection`), rows grouped client-side by `effectiveCollection`, groups with hits shown open, others hidden, `tree.noMatch` when none; Archived panel reloaded with the same `q`. Esc / ✕ → `setTreeSearch("")` restores the regular tree (open states from storage, groups refetched) and the unfiltered Archived panel. "Show more" is hidden while searching.
- [ ] **Step 2:** Sort: `#collections-sort` → existing `openSortMenu`; `setSessionSort` persists and calls `resetGroup` on every open group.
- [ ] **Step 3:** Select: ⋯ menu gains `sessionbar.select` (General's ⋯ menu shows only this) → `enterSelectMode(name)`; adds `#sidebar.selecting`, shows `#tree-select-bar`, row click toggles. `#tree-selectall-btn` toggles the loaded rows of the scope group. `#tree-batch-btn` → existing `openBatchMenu`; `runBatch` ends with `exitSelectMode()` + `loadSessions()`. Search and select are mutually exclusive (entering one leaves the other).
- [ ] **Step 4:** Delete the old `#session-pane` toolbar functions (`setSearchMode`, `setSelectMode`, `updateSessionBar`, `wireSessionBar`) and `currentViewIds` if unused.
- [ ] **Step 5:** i18n + `make i18n`; bump `?v=`.
- [ ] **Step 6: Verify** (smoke + Playwright):
  - Collapse Kubernetes, search "K8s" → Kubernetes group shown open with 4 hits; Archived panel shows the archived "K8s chat 5" (Review Focus 5).
  - Search "zzz" → "No matching chats".
  - Esc → Kubernetes collapsed again, General back to its loaded rows.
  - While searching, the ⋯ "Select" item is absent/disabled; entering select closes nothing else.
  - Sort A–Z → General rows alphabetical; reload keeps A–Z.
  - Select from Kubernetes' ⋯, Select all → 4 ticked; Actions → Archive → 4 archived, select mode exits, header count 0.
- [ ] **Step 7:** Commit `feat(web): search, sort and bulk select in the collection tree`.

---

### Task 6: Targeted live refresh

**Files:**
- Modify: `web/app.js` — call sites of `loadSessions()` at ~4035 (rewind), ~7661 (delete), ~7718/7725 (rename), ~8391-8412 (push events), ~8861 (pushed turns), ~9479 (routing chip), ~9662 (turn end); `moveSessionToCollection` (~7423)

**Interfaces:**
- Consumes: Task 4's `reloadGroupPrefix`, `loadCollections`, `reloadArchivedPrefix`, `sessionMeta`, `effectiveCollection`.
- Produces: `groupOf(sessionId: string): string` — `effectiveCollection(sessionMeta.get(id))`, or `GENERAL_COLLECTION` when the session is not cached. `refreshSessionsFor(sessionId: string): void` — coalesced (one pass per **250 ms** window): collects the affected group names (the session's `effectiveCollection` from `sessionMeta`, General when unknown), then runs one `loadCollections()` and one `reloadGroupPrefix` per affected **open** group; reloads the Archived panel only if an affected session is archived. `refreshGroups(names: string[]): void` — same coalescing for explicit groups.

- [ ] **Step 1:** Implement `refreshSessionsFor` / `refreshGroups` with a shared pending-set + timer.
- [ ] **Step 2:** Rewire: rewind, rename, routing chip, pushed turns, turn end, `session_created`, `session_renamed` → `refreshSessionsFor(id)`; `session_deleted` → `refreshGroups([groupOf(id)])` before `forgetSession`, plus archived reload; `session_moved` → `refreshGroups([old, data.collection])` where `old` is read from `sessionMeta` before the update; `collections_changed`, import, fork, batch actions, archive/unarchive keep the full `loadSessions()`.
- [ ] **Step 3: Verify** (smoke + Playwright network log):
  - With General, Kubernetes, IA Parc all open, `curl -XPATCH $B/sessions/<K8s id> -d '{"title":"renamed"}'` → browser issues exactly one `/api/collections` and one `/api/sessions?collection=Kubernetes…` request (Review Focus 3).
  - Call `refreshSessionsFor(id)` 10× in one `browser_evaluate` → one request pair.
  - Move a session via curl (`POST /api/sessions/<id>/collection`) → both groups refresh, nothing else.
  - Create a session via curl → it appears in General.
- [ ] **Step 4:** Commit `perf(web): refresh only the affected collection group on session events`.

---

### Task 7: Collection selector on drafts; New chat opens a draft

**Files:**
- Modify: `web/index.html` — in `#chat-pane-tpl`'s `.pane-picker-inner`, add `.pane-picker-collection` (label, `.pane-picker-collection-toggle` with `.pane-picker-collection-name`, `.pane-picker-collection-menu`) beside `.pane-picker-squad`, and `.pane-picker-dir` (pinned folder line, hidden by default)
- Modify: `web/app.js` — `renderPanePicker`/`renderPickerSquad` (~902-1003), pane handlers (~1085-1091), `newDraftTab` (~2150), `newChat` (~8174), `#new-chat` click (~11021), Files "Open Chat here" (~12769, ~12840)
- Modify: `web/css/features/panes.css` (collection selector + dir line, mirroring the squad selector)
- i18n: `picker.collection` "Collection", `picker.collectionAria` "Choose the collection for the new chat", `picker.dir` "Folder: {dir}"

**Interfaces:**
- Consumes: `collectionsData`, `collectionAccentVar`, Task 4's `newChat(panel, squadOverride, dirOverride, collectionOverride)`.
- Produces:
  - `DRAFT_COLLECTION_KEY = "agent_draft_collection"`; `panel._pickerCollection: string` (per pane, initialised from the key, falling back to General when the stored name is no longer a collection); `panel._pickerSquadPicked: boolean`.
  - `draftDirs: Map<string /*draft key*/, string /*abs dir*/>`; `newDraftTab(panel, opts?: {dir?: string}): void`.
  - `newChat` resolution: `collection = collectionOverride ?? (isDraft(panel.activeTab) ? panel._pickerCollection : GENERAL_COLLECTION)`; `dir = dirOverride ?? draftDirs.get(panel.activeTab)`; `squad = squadOverride ?? (isDraft(panel.activeTab) && pickerSquadShown ? panel._pickerSquad : undefined) ?? currentSquadChoice()`. A draft key's `draftDirs` entry is deleted once its session is created.

- [ ] **Step 1:** Collection selector render (`renderPickerCollection(panel)`): items = General + user collections with colour glyphs; selecting sets `panel._pickerCollection`, stores `DRAFT_COLLECTION_KEY`, and when that collection has `squad` and `!panel._pickerSquadPicked` sets `panel._pickerSquad = c.squad` (if it is in `availableSquads`); a user click in the squad menu sets `_pickerSquadPicked = true`. Re-render on `collections_changed`.
- [ ] **Step 2:** `newChat` resolution per Interfaces; the implicit-create paths (`uploadPickedFiles` ~1292, send ~9122/10035/11178/11226) keep calling `newChat(panel)` and now inherit the draft's choices.
- [ ] **Step 3:** `#new-chat` click → `closeNav(); newDraftTab(fp())` (creating a pane if none, as `newChat` does). The sidebar squad chooser keeps setting the global squad pref used as the draft's default.
- [ ] **Step 4:** Files "Open Chat here" (both sites) → `newDraftTab(fp(), { dir })`; `.pane-picker-dir` shows `picker.dir` for a draft with a pinned dir.
- [ ] **Step 5:** i18n + `make i18n`; bump `?v=`.
- [ ] **Step 6: Verify** (smoke + Playwright):
  - Sidebar New chat → a draft tab with Squad + Collection selectors, collection = General (first time); no session created yet (`GET /api/session-ids` unchanged).
  - Pick Kubernetes, click "Start a new chat" → session filed under Kubernetes; next New chat preselects Kubernetes.
  - On a draft, pick Kubernetes and send a first message → session in Kubernetes (implicit path).
  - Give IA Parc a default squad (`PATCH /api/collections/IA%20Parc {"squad":"helper"}`) → choosing IA Parc on a fresh draft switches the Squad selector to helper; choosing a squad first then IA Parc keeps the chosen squad.
  - Files → "Open Chat here" on a folder → draft shows "Folder: …"; start → `GET /api/sessions/<id>` (or the Files panel) shows that cwd.
  - A group's ＋ still creates directly (no draft).
- [ ] **Step 7:** Commit `feat(web): choose the collection on the new-chat draft`.

---

### Task 8: Collapsed rail and small screens

**Files:**
- Modify: `web/css/features/sidebar.css` (collapsed-rail rules ~82-133), `web/css/features/collections.css`, `web/css/features/responsive.css` (drawer + touch block), `web/app.js` (rail pastille click, `MOBILE_MQ` drawer helpers ~11517-11535 if they reference `#session-pane`)

**Interfaces:**
- Consumes: Task 4's `setGroupOpen`, `loadGroupMore`; existing `expandSidebar()`, `setNavOpen`, `closeNav`.

- [ ] **Step 1:** Collapsed rail: hide `ul.collection-sessions`, `.group-more`, the search row label and the select bar; group headers render as today's pastilles (initials badge / General star); the Search button shows its icon only. Pastille click while collapsed → `expandSidebar()`, `setGroupOpen(name, true)`, load if needed, scroll the group into view. Search icon while collapsed → `expandSidebar()` then open search.
- [ ] **Step 2:** `responsive.css`: drop every `#session-pane` rule; the drawer is `#sidebar` alone at full height (`100dvh`); touch block reveals `.group-new`/`.group-menu` (and sizes them ≥34px) alongside the existing session-row reveals. Update the partial's header comment.
- [ ] **Step 3: Verify** (smoke + Playwright):
  - Collapse the sidebar → rail shows New chat, search icon, pastilles, avatar; clicking the Kubernetes pastille expands the sidebar with Kubernetes open.
  - 390×844: hamburger opens a single full-height drawer; tapping a session opens it and closes the drawer; ＋/⋯ visible on group headers without hover (emulate `pointer: coarse` or check the computed style under the media rule).
  - 1280×800 unchanged desktop (screenshot).
- [ ] **Step 4:** Commit `feat(web): collapsed rail and phone drawer for the collection tree`.

---

### Task 9: Docs, release notes, cleanup, final verification

**Files:**
- Modify: `web/docs/01-getting-started.md`, `web/docs/03-sessions.md`, `web/docs/04-settings-panel.md` (describe the tree, account menu, settings modal; English)
- Modify: `internal/features/FEATURES.md` — one bullet under the in-development minor section
- Modify: `CLAUDE.md` — rewrite "Session collections (thematic folders, Web UI three-pane layout)", "Session list pagination (server-side windowing)", "Web UI small-screen layout (phone drawer)", the Settings-panel mentions ("Settings Assistant", hot-reload banner, "Desktop notifications" Appearance references), the `GET /api/collections` row fields; `web/css/CLAUDE.md` file map (collections.css, sidebar.css, settings/sidebar.css, settings/modal.css, responsive.css rows)
- Modify: `web/i18n/*.json` — delete keys no longer referenced (`sessionbar.new`, `sessionbar.import`, `app.whoami.label`, … — grep each before deleting) and `make i18n`

- [ ] **Step 1:** Release notes. Compare the topmost `## A.B` section of `internal/features/FEATURES.md` (today `## 2.0 — Durable agent questions`) with `git describe --tags --abbrev=0`: if that section's version is above the latest tag (unreleased), add the bullet there; otherwise insert a new `## A.(B+1) (in development) — Collection tree and settings dialog` section above it. Bullet: `- **Collection tree & settings dialog** — sessions are listed under each collection in the sidebar, and settings open in a dialog from the new account menu.` Run `go test ./internal/features/...` → PASS.
- [ ] **Step 2:** Docs + CLAUDE.md updates as listed.
- [ ] **Step 3:** Dead i18n keys: for each candidate, `grep -rn "<key>" web/*.js web/index.html` returns nothing before deleting; `make i18n` prints no missing-key warning.
- [ ] **Step 4: Full verification:** `go test ./...` → PASS; `node --check web/app.js web/settings.js`; then the Review Focus 1–5 checks once more end-to-end on a fresh `$SMOKE_HOME`, plus dark/light theme screenshots of the sidebar, account menu and settings modal, and a 390×844 pass.
- [ ] **Step 5:** Commit `docs: collection tree, account menu and settings modal`.
