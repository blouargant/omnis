# Squad graph view (Settings → Agents → Squads)

**Date:** 2026-07-19
**Status:** Design — approved for planning
**Surface:** Web UI only (`web/settings.js` + CSS + i18n). No Go / server changes.

## Problem

The Squads sub-tab (Settings → Agents → Squads) edits a squad as a flat form:
name, description, leader, and a grid of member toggles. That form does not
convey the *structure* a squad actually forms at runtime — a leader delegating to
members, members that themselves own nested `subagents` (the gatherer doctrine,
e.g. `research_critic → web_fetcher`), and, for the Omnis **router** squad, the
set of squads it can route traffic to. Users cannot see the command structure at
a glance.

## Goal

Add a **graph view** to the right-hand detail panel of the Squads sub-tab. The
right panel switches between the existing **Configuration** form and a new
**Graph** that visualises the selected squad's structure as a delegation tree.
For the Omnis router squad, the graph instead shows Omnis and every squad it can
route to.

## Non-goals

- No graph on the Agents sub-tab (Squads sub-tab only, this iteration).
- No editing from the graph (it is a read-only visualisation + navigation aid).
- No A2A-peer or handoff-back-to-router cross-links (delegation/routing tree only).
- No graph library / build step / CDN asset. Hand-rolled, consistent with the
  codebase's vendored-offline, no-build ethos.
- No server routes and no Go changes. All data is already client-side.

## Data model (already available client-side)

Everything renders from the in-memory parsed config `state.parsed["agent"].value`
(call it `d`), which the Squads sub-tab already holds:

- `d.squads[]` — `{ name, description, leader, members[], hidden? }`. The parsed
  list already includes the leaderless system squads (Omnis router, Helper, and
  the hidden Session Search).
- `d.agents[]` — `{ name, enabled, leader?, description?, model_ref?,
  max_instances?, subagents[]?, ... }`.
- `d.router_squad?` — the router squad name; **absent ⇒ default `"omnis"`**
  (mirrors the server's `ensureRouterSquad`).

Because the graph reads the same in-memory doc the form mutates, it reflects
**unsaved edits** (add a member in Configuration, flip to Graph, it appears).

## UI: the Configuration ↔ Graph toggle

- A small **segmented control** (`Configuration | Graph`) renders at the **top of
  `#squad-detail-panel`**, above the panel body, for **every** selected squad —
  including the read-only system squads (the graph is the whole point for Omnis).
- View state: `state.squadView ∈ {"config","graph"}`, default `"config"`,
  **persisted in `localStorage`** (e.g. `agent_toolkit_squad_view`) so it survives
  reloads and is remembered while clicking between squads.
- `renderSquadDetail(d, idx)` is refactored to:
  1. Render the toggle (wired to flip `state.squadView` + re-render).
  2. If `squadView === "config"` → render today's form **unchanged** (extracted,
     if convenient, into a `renderSquadConfig(d, idx)` helper; behaviour identical).
  3. If `squadView === "graph"` → call `renderSquadGraph(d, idx)`.
- The read-only rules for system squads apply to the **Configuration** form as
  today; the Graph is inherently read-only for all squads.

## The graph model — three root shapes, one renderer

`renderSquadGraph(d, idx)` builds a node tree, then hands it to a shared
layout/draw pass. Node kinds: **agent node** and **squad node**.

### 1. Router squad (Omnis)
- Detected as the leaderless squad whose name equals `d.router_squad || "omnis"`.
- Root = an **Omnis** node.
- Children = **every routable squad**: all `d.squads` that are **not** the router
  squad and **not** `hidden` (mirrors `routerSquadCatalogue` — this is exactly the
  set Omnis can route to; Session Search, being hidden, is excluded).
- Each child is a **squad node** showing **name + member count + leader name**.

### 2. Normal squad (has a leader)
- Root = **leader** agent node.
- Children = **members** (agent nodes).
- Grandchildren+ = each member's **`subagents`** (agent nodes), recursively.

### 3. Other leaderless squad (Helper, and Session Search if ever shown)
- Root = the **single member** agent node.
- Children = that agent's **`subagents`**, recursively (e.g. `helper →
  session_search`).

### Recursion safety
- Walk `subagents` with a **visited set** (per-render) and a **depth cap** (e.g.
  6) so a malformed config cannot infinite-loop the renderer, even though the
  server validates the subagent graph acyclic.
- A `subagents` entry that does **not** resolve to an enabled `d.agents` entry
  renders as a **greyed "unavailable"** node (kept visible, not silently dropped),
  so a broken reference is obvious.

## Node rendering & layout (approach A: HTML nodes + SVG connector overlay)

- **Nodes are HTML `<div>`s** styled like the existing agent cards:
  - **Agent node**: name; a **model-tier chip** from `model_ref` (omitted if
    absent); a `×N` chip when `max_instances > 1`; root nodes (leader / Omnis /
    single-agent root) carry an **accent border**. `data-tip` = the agent
    description → the app's themed `#tip-layer` tooltip.
  - **Squad node** (Omnis children): name; **member-count** meta; **leader** name
    sub-label. `data-tip` = the squad description.
- **Layout**: CSS flex — one **centred row per depth level**, siblings laid out
  horizontally, the row scrolls horizontally (`overflow-x:auto`) if it overflows.
- **Edges**: a single absolutely-positioned **SVG overlay** sized to the canvas
  draws parent→child connector lines. Line endpoints are computed from each
  node's `getBoundingClientRect` **after** layout (relative to the canvas box).
  - Redraw on:
    - a **`ResizeObserver`** on the graph canvas (panel width changes, sidebar
      resize, split-pane drag),
    - a theme change (colours from CSS vars — re-stroke on the existing
      `data-theme` `MutationObserver` signal already used elsewhere in the UI),
    - and once after the initial render (rAF, so layout has settled).
  - The observer is disconnected when the panel is re-rendered / the view flips,
    to avoid leaks.
- Empty squad (no members / no routable squads) → render the root node alone with
  a localised "no members" / "no routable squads" hint under it.

## Interactions (click-to-navigate + hover)

- **Hover** any node → the app's themed tooltip (`data-tip`) shows the
  model tier + description (agents) or description (squads).
- **Click an agent node** → switch to the **Agents** sub-tab with that agent
  selected: `state.activeAgentSubtab = "agents"`, set `state.activeAgentIdx` to
  that agent's index in `d.agents`, then `renderAgentForm()`. (A greyed
  unavailable node is inert.)
- **Click a squad node** (Omnis graph only) → select that squad
  (`state.activeSquadIdx`), **stay in Graph view**, re-render → natural drill-down
  into the chosen squad's own tree. The left-hand squad list (and re-selecting
  Omnis) is the way back; no separate back button.

## Styling, i18n, packaging

- **CSS**: new partial `web/css/settings/squad-graph.css`, imported by the
  settings CSS bundle. Theme-aware (light/dark) via existing CSS variables/tokens.
  Node cards reuse / extend the existing `.agent-tool-card`-family look so the
  graph reads as the same design system.
- **i18n**: new `set.squad.graph.*` keys (`configTab`, `graphTab`, `noMembers`,
  `noRoutableSquads`, `unavailableAgent`, and a leader sub-label prefix) added to
  **en/fr/es/de**; the existing `set.squad.memberCount` plural key is **reused**
  for the member-count meta (no new key). Run `make i18n` to regenerate
  `web/i18n/locales.js`. Agent/squad **names and `model_ref` ids stay
  untranslated** per the translation glossary.
- **Cache-bust**: bump the `settings.js` `?v=` query (and add/bump the new CSS
  partial's `?v=` if the bundle references it directly) in `web/index.html`.

## No-op contract

- Default `state.squadView` is `"config"`, so a freshly loaded Squads sub-tab is
  **byte-identical** to today until the user clicks **Graph**.
- The feature adds no server routes, no Go code, and no new config keys; it is a
  purely additive client-side view over data already loaded.

## Edge cases / decisions

- **Router detection** keys on `d.router_squad || "omnis"`; if that squad is
  absent (routing disabled / `"none"`), no squad matches the router shape and
  every squad renders as a normal/leaderless delegation tree — correct fallback.
- **Unsaved edits** are reflected (the graph reads the live `d`).
- **Deeply nested / wide trees** scroll inside the canvas (`overflow:auto`); the
  panel body never forces horizontal page scroll.
- **Disabled agents** referenced as members/subagents render greyed
  ("unavailable"), matching how the config form already excludes disabled agents
  from candidate lists.

## Affected files (anticipated)

- `web/settings.js` — refactor `renderSquadDetail`; add `renderSquadConfig`
  (extracted) + `renderSquadGraph` + a tree-build helper + the SVG connector
  draw/observer helpers; toggle wiring + `localStorage` view state.
- `web/css/settings/squad-graph.css` — new partial (nodes, rows, edges, toggle),
  wired into the settings CSS bundle.
- `web/i18n/en.json` (+ `fr`/`es`/`de`) — `set.squad.graph.*` keys; regenerate
  `web/i18n/locales.js` via `make i18n`.
- `web/index.html` — bump `settings.js` (+ CSS) `?v=`.

## Testing / verification

- Manual smoke via the branch web-assets recipe (`OMNIS_WEB_DIR=$(pwd)/web`,
  no token): select each squad and flip Configuration ↔ Graph.
  - **Omnis** → root Omnis + one node per routable squad (System, Coding,
    Kubernetes, Knowledge, Skill Editor, Helper), each with member count + leader;
    Session Search absent (hidden). Click a squad node → drills in, stays in Graph.
  - **Coding** → `coder` (leader, accent) → `code_scout` / `code_docs` /
    `reviewer` / `refactorer`; verify a member with `subagents` nests correctly.
  - **Knowledge** → `knowledge_leader` → members incl. `research_critic` →
    `web_fetcher` (nested grandchild).
  - **Helper** (leaderless) → `helper` → `session_search`.
  - Click an agent node → lands on the Agents sub-tab with that agent selected.
  - Hover → tooltip shows model + description.
  - Resize the panel / split pane → connector lines redraw correctly.
  - Toggle to Graph, reload → view persists (localStorage).
- Verify light + dark themes.
