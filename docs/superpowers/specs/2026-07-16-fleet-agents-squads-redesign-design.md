# Fleet — Redesigned Agents & Squads Settings

**Date:** 2026-07-16
**Status:** Design approved — ready for implementation planning
**Scope:** Web UI. Full rebuild of the client-side Agents/Squads settings surface; the
server config write contract is preserved unchanged.

---

## 1. Problem

The current **Settings → Agents** panel is a five-sub-tab surface (Agents, Squads,
Remotes, Models, Global config) built across ~9,700 lines of
[`web/settings.js`](../../../web/settings.js). Agents and squads are edited in
*separate* tabs, each a list-on-left / detail-form-on-right split. The user
identified four problems, all of which this redesign must address:

1. **Composition is invisible.** Who leads a squad, who its members are, and which
   agent delegates to which nested sub-agent (`subagents`) is only discoverable
   through checkboxes and a Team picker buried inside forms. There is no view of the
   actual topology.
2. **Workflow is clunky.** Building a squad, moving an agent between squads, or
   wiring a sub-agent takes many clicks and tab switches.
3. **Hard to understand.** New users cannot grasp what squad / leader / member /
   router / sub-agent mean or how they relate.
4. **Too sprawling / dense.** The five-tab, giant-form layout is overwhelming.

## 2. Goals & non-goals

**Goals**
- Make the squad/agent topology visible and directly editable.
- Reduce clicks/mode-switches for common composition tasks.
- Make the concepts self-explanatory in-place.
- Consolidate the sprawl into one coherent surface.

**Non-goals**
- Redesigning the *internals* of the Models/Providers, Registry, or Global config
  forms. They are reused as-is, surfaced contextually (see §7).
- Touching the other Settings sections (Permissions, MCP, Hooks, Skills, Commands,
  Automation, Appearance, Documentation).
- Rewriting any server-side config engine. See the load-bearing principle below.

## 3. Guiding principle: rebuild the client, preserve the server contract

This is a **full rebuild of the client surface** — a new tree, new editor
components, and a new shell with a clean in-memory data model — replacing the
current agent/squad forms entirely.

It is **not** a rewrite of the server. The proven write contract stays exactly as
it is:

- `GET/PUT /api/config/parsed/:name` (+ the per-agent `agent.json` fan-out),
- `GET /api/squads`,
- `POST /api/config/reload`,
- the layered deep-merge / delta-write engine in `internal/configedit`.

Rationale: the server's config round-trip is correct and hard-won. The CLAUDE.md
records multiple "a key missing from the PUT whitelist is silently dropped" bugs
(`subagents`, `hidden`, …). Re-implementing that layer is pure regression risk with
no user-visible benefit. Confirmed during design: every datum the new UI needs is
already available client-side — `subagents` per agent (already read, with
cycle-detection, at [`web/settings.js:5751`](../../../web/settings.js#L5751)), squad
`members` + `hidden`, and the server already round-trips those keys
([`server/config.go:807`](../../../server/config.go#L807),
[`server/config.go:947`](../../../server/config.go#L947)). The redesign is therefore
**primarily frontend**, with little to no Go work.

## 4. Chosen shape (decisions)

Reached through brainstorming with the user:

| Decision | Choice |
|---|---|
| Primary metaphor | **Expandable topology tree** as the main view + a **read-only collapsible canvas minimap** |
| Scope | **Whole panel restructure** — fleet composition + per-agent/squad editing + Models/Providers + Registry/install + Global config |
| Shell / IA | **Fleet-first with slide-overs** — Models/Registry/Global are light header peers; deep references slide in over the editor |
| Interaction | **Click-driven + light drag-and-drop** |
| Shared agents | **Shown under every squad they belong to, badged `⌂N`** (N = squad count); edited once |
| Guidance | **Inline hints + legend + empty states** |
| Implementation | **Full client rebuild**, server write contract preserved |

## 5. Architecture (client)

A new `web/fleet/` module, kept out of the oversized `settings.js`, composed of
small single-purpose units:

- **`fleetStore`** — the single source of truth. Builds one clean in-memory model
  from the existing parsed config:
  `{ agents[], squads[], routerSquad, unusedAgents[], membershipIndex }`. Derives
  `⌂N` shared counts, computes cycle-safety for the Team picker, and owns
  dirty-tracking and validation. All edits mutate the store; Save diffs the store
  and issues the existing PUTs.
- **`fleetTree`** — renders the expandable tree from the store; emits
  `select(nodeRef)` and edit intents (add member, add to team, make leader, …).
- **`fleetCanvas`** — read-only, collapsible delegation-graph minimap rendered from
  the same store.
- **`fleetEditor`** — the right pane. **New** agent-editor and squad-editor
  components driven by the selected `nodeRef`.
- **`fleetShell`** — header (`Fleet · Models · Registry · Global`, `⟳`, `＋`), the
  pane layout, and the slide-over host.
- **slide-overs** — Model editor, Registry browse/install, Global config; reached
  contextually and rendered over the editor. These wrap the existing renderers.

**Data flow:**

```
config (existing GET) → fleetStore → { fleetTree, fleetCanvas, fleetEditor }
        edits mutate fleetStore
        Save → diff store → existing PUT /api/config/parsed/* (+ per-agent fan-out)
             → POST /api/config/reload
```

Each unit has one job and a narrow interface (`fleetStore` is the only thing that
knows the config wire shape; the tree/canvas/editor only see the derived model).

## 6. The Fleet tree

The heart of the redesign; the reading was validated against a wireframe with the
user.

- **Node kinds & glyphs:** `◇` router · `⬢` squad · `⬡` leaderless squad ·
  `★` leader · `•` member · `◆` sole agent (leaderless) · `↳` nested sub-agent
  (team) · `⌂N` shared across N squads · `·hidden·` marker on hidden squads.
- **Hierarchy:** router → squads → leader → members → each member's nested team
  (recursively) → an **Unused agents** pool at the bottom (enabled agents in no
  squad).
- **Shared agents** appear under **every** squad they belong to, each tagged `⌂N`.
  Editing one edits the single underlying definition; the badge signals the sharing.
- A **filter** box narrows the tree by name / model / tool.
- **Canvas minimap** is a collapsible strip under the tree (read-only in v1).

## 7. Editor pane (new components)

Selecting a node opens its editor in the right pane.

**Agent editor** — identity + active toggle; model reference (dropdown, with a `↗`
that opens the Model editor as a slide-over); tools grid; skills; MCP/A2A pickers;
**Team (`subagents`)** picker with **live cycle exclusion**; instruction editor with
token estimate; advanced path overrides; parallelism (`max_instances`) and
`resumable_sessions`. Built-in agents render their baked-in fields read-only, as
today.

**Squad editor** — name (default squad name read-only); description; leader dropdown
including `(none — run single agent directly)` which switches the member picker to
single-select (leaderless); member picker; `hidden` flag (round-tripped).

Save / Discard act on the current selection; dirty state comes from `fleetStore`.

## 8. Shell & navigation

Fleet is the home view. **Models / Registry / Global** are light header peers rather
than equal tabs. Deep references — an agent's `model_ref`, an "add from registry"
action, a global key — open as **slide-overs over the editor**, so the user rarely
leaves the fleet. `＋` creates a squad or an agent; `⟳` triggers a config reload.

## 9. Interactions

**Click-driven primary**, light DnD secondary; every DnD action has a menu
equivalent (touch + accessibility fallback).

- Menus / `＋` / pickers: *Add member…*, *Add to team…*, *Make leader*,
  *Add to squad…*, *Duplicate*, *Disable/Enable*, *Remove*.
- Light DnD: reorder members; move a member between squads (adds a reference, since
  agents are shared); drag an unused agent into a squad.

## 10. Guidance layer

- Always-visible **legend** for the glyphs.
- Rich tooltips on every concept (router / leader / member / leaderless / hidden /
  team / shared).
- Helpful **empty states** ("No squads yet — a squad is a leader + members. Create
  one").
- A one-line "what is this?" on the special nodes (router, leaderless, hidden).

## 11. Constraints / must-preserve (not optional)

These are correctness requirements the rebuild must honor:

- **Round-trip every key** the server whitelist cares about, especially the ones the
  CLAUDE.md flags as silently-dropped: `subagents`, `hidden`, squad `members`,
  `max_instances`, `resumable_sessions`, `max_tool_calls`.
- **Sub-agent cycle prevention** — mirror `validateSubAgentGraph`: the Team picker
  excludes any agent that already (transitively) depends on this one.
- **Squad rules** — the default squad is always present and its name is read-only; a
  leaderless squad has **exactly one** member; ≥2 members require a real leader
  (`leader: true`); the router squad is special (auto-injected, never a routing
  target, not offered in the picker).
- **Layered delta-write** — fork-on-first-edit and local-promotion happen through
  the existing routes; **hot-reload** fires after save; an **embedder-identity
  change** in models still raises the restart banner.
- **Built-in agents** show read-only fields where the binary bakes defaults;
  enable/disable and `agents_removed` tombstones keep working.

## 12. Phasing (implementation slices)

1. `fleetStore` + read-only `fleetTree` + legend/empty states, behind a flag, old
   panel still intact.
2. New agent editor + squad editor wired to the store, Save + reload.
3. `fleetCanvas` (read-only minimap).
4. Slide-overs for Models / Registry / Global + the header shell.
5. Interactions (context menus + light DnD).
6. Cut over: remove the old sub-tab renderers; i18n (en/fr/es/de) + docs
   ([`web/docs/19-agents.md`](../../../web/docs/19-agents.md)) update.

## 13. Testing

- **Round-trip fidelity is make-or-break.** Golden tests: load a representative
  config → mutate through `fleetStore` → assert the resulting PUT payload preserves
  every key (the delta-write contract), across agents, squads, `subagents`,
  `hidden`, and the numeric/bool agent fields.
- Unit tests for **cycle exclusion** in the Team picker and for the **squad rules**
  (leaderless single-member, leader eligibility, default-squad presence).
- Manual/e2e smoke of the tree → editor → save → hot-reload loop and the
  slide-overs.

## 14. Out of scope

- Models/Providers, Registry, and Global form internals (reused as slide-overs).
- Permissions, MCP, Hooks, Skills, Commands, Automation, Appearance, Documentation
  settings sections.
- Any server-side config-engine rewrite.
