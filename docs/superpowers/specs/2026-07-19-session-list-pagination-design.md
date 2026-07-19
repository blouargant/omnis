# Session list pagination (Web UI middle panel)

**Date:** 2026-07-19
**Status:** Design approved, pending spec review
**Area:** Web UI middle `#session-pane` session list + `GET /api/sessions`

## Problem

The middle-panel session list fetches **all** non-hidden sessions in one
`GET /api/sessions` payload ([server/server.go:430](../../../server/server.go)),
caches the full list client-side in `lastSessions`, and renders every matching
row after a client-side collection filter + live title search + sort. With many
sessions this means a large payload, large server response, and a large DOM.

## Goal

Load the active session list in pages: an initial **50**, then **5 more** each
time the user scrolls to within ~5 rows of the end, and so on. The chunking is
**server-side** (the server returns pages), and the interactions that depended
on holding the whole list client-side are reworked to fit.

## Decisions (locked)

1. **Server-side pagination**, not client-side windowing. The server returns
   pages; payload and server work shrink.
2. **Search moves server-side.** The middle-panel title filter becomes a `q`
   query param so search covers *all* sessions, paginated like the list.
3. **Archived list is paginated too**, with its own window + observer, fetched
   lazily when the collapsible Archived panel is expanded.
4. **Live pushes reload the loaded prefix**, preserving scroll position (not a
   reset-to-top).
5. **Select-all covers loaded rows only** (standard for a paginated list); the
   toolbar count shows the true `total` from the endpoint.

## Constants

- `PAGE_INITIAL = 50` — first page size.
- `PAGE_MORE = 5` — subsequent page size.
- Prefetch margin ≈ 5 rows (~300px `rootMargin` on the IntersectionObserver),
  i.e. "load 5 more when 5 rows from the bottom".

---

## 1. Server: extend `GET /api/sessions` (backward-compatible)

The existing handler ([server/server.go:430](../../../server/server.go)) gains
optional query params. **Pagination engages only when `limit` is present** — with
no `limit` the handler returns the full non-hidden list exactly as today, so
existing consumers/tests (`newsession_*_test.go`, etc.) are unaffected.

| Param | Meaning | Default |
|---|---|---|
| `limit` | page size; **absent ⇒ legacy full list** | — |
| `offset` | start index into the filtered+sorted list | `0` |
| `collection` | filter to this *effective* collection name | absent ⇒ all collections |
| `archived` | `true` / `false` — which list to page | `false` |
| `q` | case-insensitive substring over `title` (fallback `id`) | `""` |
| `sort` | `recent` \| `created` \| `az` | `recent` |

**Response:** `{ "sessions": [...], "total": N, "offset": O, "limit": L }`.
`total` is the count **after** filtering (collection + archived + q) but **before**
slicing — it drives the toolbar count and the exhaustion check
(`offset + len(sessions) >= total`).

**Handler pipeline:**
1. `d.Registry.List()` — already sorted `last_used desc`, tie-break `created desc`
   ([internal/sessions/sessions.go:395](../../../internal/sessions/sessions.go)).
2. Drop `Hidden` sessions (as today).
3. If `collection` present: keep sessions whose **effective collection** matches
   (case-insensitive). Effective collection folds blank/unknown → General, via a
   new server helper `effectiveCollection(meta, known)` using
   `sessions.ListCollections()` + `sessions.GeneralCollection` (mirrors the
   client's `effectiveCollection`).
4. If `archived` present: keep matching `Archived` flag. When `limit` present and
   `archived` absent, default to `false` (active).
5. If `q` present: keep `strings.Contains(lower(title|id), lower(q))`.
6. Apply `sort`:
   - `recent` — keep `List()` order (no-op).
   - `created` — `CreatedAt` desc.
   - `az` — case-insensitive title/id ascending. **Must be server-side** so
     ordering is stable across page boundaries. (Minor: Go's comparison is not
     locale-aware like JS `localeCompare`; consistency across pages matters more
     than exact collation.)
7. Capture `total = len(filtered)`, then slice `[offset : min(offset+limit, total)]`.

**Test:** add `TestListSessionsPaginated` driving the real router — asserts
page/offset/total, collection+archived+q filtering, and that omitting `limit`
returns the full legacy list.

**Optional (not in v1):** `GET /api/sessions/ids?collection=&archived=&q=`
returning just the id list, to power a true "select all N in collection". Left
out as scope creep; revisit if loaded-rows select-all proves insufficient.

---

## 2. Client: windowed view state + IntersectionObserver

Two independent view-states, one per list:

- **active** → `#session-list` (middle column).
- **archived** → `#archived-list` (left sidebar, collapsible).

Each view:

```
{
  collection,          // active collection name for this view
  q, sort,             // current search + sort
  offset, loaded,      // paging cursor + rows rendered
  total,               // from the last response
  loading, exhausted,  // guards
  seq,                 // monotonic; drops stale responses
  lastGroupKey,        // for timeframe headers (recent sort only)
}
```

**Trigger — sentinel + IntersectionObserver (not element counting).**
A sentinel `<li class="session-sentinel">` is appended at the end of the list.
An `IntersectionObserver` rooted on the list with `rootMargin` ≈ `0px 0px 300px 0px`
(~5 rows) fires **~5 rows before the end** → `loadMore(view, PAGE_MORE)`. This
implements the "5 from the bottom" trigger robustly despite variable-height
timeframe headers. (Element counting on "the 45th row" is brittle with headers.)

**Functions:**
- `resetView(view)` — on collection/sort/search change: clear the list DOM, reset
  `offset/loaded/total/exhausted/lastGroupKey`, bump `seq`, then
  `loadMore(view, PAGE_INITIAL)`.
- `loadMore(view, n)` — return if `loading || exhausted`; set `loading`; fetch
  `?collection&archived&q&sort&offset=loaded&limit=n`; if `seq` is stale, drop;
  else `appendSessionRows(view, sessions)`, `loaded += len`,
  `total = resp.total`, `exhausted = loaded >= total`; keep the sentinel last;
  clear `loading`.
- `reloadPrefix(view)` — see §5.

**Archived view is lazy:** its first `resetView` runs only when the Archived
panel is expanded; collapsing does not tear down state, re-expanding does not
refetch unless a push invalidated it.

---

## 3. Timeframe grouping across pages

For `recent` sort, `appendSessionRows(view, rows)` emits a Today/Yesterday/…
header only when an incoming row's timeframe key differs from `view.lastGroupKey`
(the group of the last row already rendered), then updates `lastGroupKey`. Groups
therefore stay correct as pages stream in. `created`/`az` render flat (as today).

---

## 4. Search & sort become server-driven

- The `#session-search-input` handler (debounced ~150ms) sets `view.q` and calls
  `resetView(activeView)`. Search now covers all sessions (server-side), paginated.
- The sort menu sets `view.sort` and calls `resetView(activeView)`.
- A collection rail click sets `activeCollection`, updates **both** views'
  `collection` (active + archived), calls `resetView` on the active view (one
  paginated fetch, replacing the old re-filter-from-cache), and marks the
  archived view stale so it refetches on next expand.

---

## 5. Live pushes → reload the loaded prefix

In `subscribeGlobalEvents` ([web/app.js](../../../web/app.js)), the
`session_created` / `session_deleted` / `session_renamed` / `session_moved`
handlers, plus local new-chat / delete / archive / move paths, stop calling the
full `loadSessions()` and instead call `reloadPrefix(view)`:

1. Fetch `offset=0 & limit=max(PAGE_INITIAL, loaded)`.
2. `appendSessionRows(view, sessions, {reset:true})` (clears list first, resets
   `lastGroupKey`).
3. Restore the pre-reload `scrollTop`.
4. Update `loaded = len`, `total`, `exhausted`.

`collections_changed` / `session_moved` still also call `loadCollections()` for
the rail counts (server-computed, unaffected). The monotonic `seq` guard prevents
an in-flight `loadMore` response from clobbering a prefix reload.

---

## 6. Ripple: retiring the full-list cache (`lastSessions`)

`lastSessions` (the whole payload) has three dependents; each is reworked:

- **Row/tab colour** (`sessionCollectionColor`, and any `lastSessions.find`):
  replace with a lazily-grown `sessionMeta` map (id → `{title, collection,
  archived, squad}`) populated on every fetch (page / prefix / single-session
  open). This extends the existing `sessionTitles` map.
- **Collection rail click:** now `resetView()` (one paginated fetch) instead of
  re-filtering the cached list.
- **Pane picker** (open-existing session in an empty pane): backed by the
  paginated endpoint — shows recents (first page) + the server `q` search box,
  instead of iterating `lastSessions`.
- **Boot layout-restore:** stop pre-filtering persisted tabs against the full
  list (a tab for a session not in page 1 would be wrongly dropped). Instead
  restore each persisted tab and let session-mount drop it on a 404.

`renderSessions(sessions)` is refactored into `appendSessionRows` +
`resetView`; its old single-pass body is removed. Callers that passed
`lastSessions` (`renderCollections`, search/sort handlers) call `resetView`
instead.

---

## 7. Toolbar count & select-all

- `updateSessionBar` shows `view.total` (real filtered total), not the loaded-row
  count.
- `currentViewIds` becomes the **loaded** active ids. Select-all ticks loaded
  rows; batch move/archive/delete act on the selection (mechanically as today).
  Documented behavior change: select-all = "all loaded", not "all in collection".

---

## 8. Edge cases

- **`q` change** resets the window; server returns paged matches; the sentinel
  keeps loading matches.
- **Collection with < 50 sessions:** single page, `exhausted` immediately, the
  sentinel never triggers another load.
- **Empty result:** existing empty state; sentinel present but inert.
- **Fast collection/sort switching:** per-view `seq` guard drops stale responses.
- **Scroll position** preserved across prefix reloads.
- **Archived panel collapsed:** no archived fetch until expanded.

---

## Non-goals

- No change to the semantic session-search feature (`/api/search/sessions` + the
  `session_search` agent) — that is a separate surface.
- No change to `/api/collections` counts (already server-computed).
- No true "select all N in collection" in v1 (see optional ids endpoint in §1).
- CLI/TUI untouched (server-only UI).

## Files touched (anticipated)

- `server/server.go` — extend the `GET /sessions` handler + effective-collection
  helper.
- `server/*_test.go` — new pagination test.
- `web/app.js` — view-state, `loadMore`/`resetView`/`reloadPrefix`/
  `appendSessionRows`, IntersectionObserver, `sessionMeta` map, pane-picker +
  boot-restore rework, toolbar count, sync handlers.
- `web/css/features/sidebar.css` and/or `collections.css` — `.session-sentinel`
  (+ optional "loading more…" affordance).
- `CLAUDE.md` — document the paginated endpoint + client windowing under the
  session-collections / three-pane section.
