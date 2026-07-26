# Fleet grouping — named fleets over project-collections

**Status:** design approved (2026-07-26), pre-implementation
**Builds on:** [2026-07-23-omnis-fleet-coordination-design.md](2026-07-23-omnis-fleet-coordination-design.md)
(the Fleet feature: Conductor/Driver/Project/Engine, shipped on `feature/fleet-coordination`)
**Mockup:** https://claude.ai/code/artifact/a0665d8e-1a5d-48e2-ab1e-7719a37073df

## Problem

In the shipped Fleet feature a **project is a collection** with `role:"project"` in
its profile, and a "fleet" is *implicit*: the resolver returns **every**
`role:"project"` collection as one global pool, so there is exactly **one** fleet
per omnis instance and its projects are interleaved with normal collections in the
sidebar, distinguished by nothing structural. Two problems follow:

1. **A project-collection is indistinguishable from a normal topic folder.** It
   sits in the same Collections list, is droppable, appears in the "Move to"
   picker, offers "New chat here" — yet it is not a topic folder, it is a repo a
   Driver edits.
2. **You cannot have more than one fleet.** Projects `{A,B}` and `{C,D}` all appear
   to any Conductor, so you can't run two independent multi-project efforts without
   them seeing (and mis-dispatching into) each other.

This design adds **named, first-class fleets** that group project-collections, make
a project **visually unmistakable and behaviourally guarded**, and **scope a
Conductor to a single fleet** so cross-fleet dispatch is impossible by construction.

## Approved decisions

| # | Decision | Chosen |
|---|---|---|
| 1 | How separated is a project-collection? | **Visual + guarded** — projects lifted out of the Collections list into a dedicated **Fleets** area; still hold their Driver sessions, but blocked from normal-collection actions (no drag-in, omitted from "Move to", no "New chat here"). |
| 2 | What is a fleet, as data? | **First-class object** — a new `fleets.json` metadata registry (name, description, colour, default engine, order). |
| 3 | Where does *membership* live? | **On the collection** — a new `fleet` tag on the project's profile. The fleet object holds metadata; members are the collections carrying its tag. Rename-safe, one-fleet-by-construction. |

## Vocabulary (recap + new)

- **Project** — a collection with `role:"project"` (unchanged): a repo + its cwd +
  its injected instructions/memory.
- **Fleet** *(new)* — a named group of projects, a first-class object in
  `fleets.json` carrying display + default metadata.
- **Ungrouped** *(new, virtual)* — the bucket for any `role:"project"` collection
  with no `fleet` tag. Not stored; it is the degenerate case that preserves today's
  single-pool behaviour.
- **Conductor / Driver / Engine** — unchanged from the base Fleet design.

## Data model

### `fleets.json` (new registry, sibling of `collections.json` under `$OMNIS_HOME`)

Managed by a new `internal/sessions/fleets.go` (mirrors `collections.go`: one
shared file, one mutex, atomic write). Shape:

```json
{
  "fleets": ["Payments", "Billing"],
  "meta": {
    "Payments": { "color": "blue",  "description": "...", "default_engine": "omnis" },
    "Billing":  { "color": "amber", "default_engine": "claude" }
  }
}
```

- `fleets` — ordered fleet names (like `collectionsFile.Collections`).
- `meta[name]` — `{color?, description?, default_engine?}`. Colour is a palette
  **token** resolved theme-side (same convention as collection colours); it is
  **not** a hex value. `default_engine ∈ {"", "omnis", "claude"}` seeds the engine
  when a project is added to the fleet.
- Fleet names: same validation as collection names (`[1..60]`, trimmed, unique;
  reserved: not `"Ungrouped"`, not `"General"`).

API surface (server-side only; never reaches `internal/fleet`):
`ListFleets`, `AddFleet`, `RenameFleet`, `RemoveFleet`, `SetFleetColor`,
`SetFleetMeta`, `FleetMembers(name)` (derived by scanning collection profiles).

### `collectionProfile.Fleet` (new membership tag)

One field added to `collectionProfile` in
[internal/sessions/collections.go](../../internal/sessions/collections.go), beside
the existing `Role`/`Engine`/`DependsOn`/`ClaudeAllowedTools`:

```go
// Fleet names the fleet this project belongs to (membership). Empty ⇒ the
// project is Ungrouped. Inert unless Role == "project".
Fleet string `json:"fleet,omitempty"`
```

Threaded through the same sites as `DependsOn` (transfer struct, `isEmpty`,
`CollectionProfileFull`, `UpdateCollectionProfile`, `SetCollectionProfileData`,
`cloneStrings` N/A — it is a scalar). `isEmpty()` gains the `Fleet` check.

**Cross-file invariants** (both files under their existing locks):
- `RemoveFleet(name)` clears `fleet` on every member collection (→ Ungrouped),
  then removes the fleet from `fleets.json`.
- `RenameFleet(old,new)` migrates `meta` key and rewrites `fleet` on every member.
- Assigning a project to a fleet that isn't in `fleets.json` is rejected.

### `internal/fleet.Project.Fleet` (new, for scoping)

`Project` gains `Fleet string`; `collectFleetProjects`
([server/fleet.go](../../server/fleet.go)) maps `p.Fleet` from the profile. This is
the **only** fleet datum that reaches `internal/fleet` — metadata stays server-side.

## Agent scoping (the reason a first-class object earns its keep)

### Session `fleet` field

A Conductor chat carries its fleet the way it carries its squad. Add `Fleet string`
to `SessionMeta` + `ConversationFile` (mirror `Squad`), with
`Registry.SetFleet` + `sessions.SetConversationFleet` + `LoadPersistedSessions`
mapping. `POST /api/sessions` accepts an optional `fleet`.

### Scoped tools

`fleet_projects` and `fleet_dispatch` must see only the session's fleet. Two hooks
(same pattern as `SetProjectsResolver`, keeping `internal/fleet` free of `sessions`):

- `fleet.SetSessionFleetResolver(func(sessionID string) string)` — installed by the
  server, maps a session id → its `fleet` (via `Registry.Get`).
- The tools resolve `fleetName := sessionFleet(tc.SessionID())`, then filter the
  resolver's project list to `p.Fleet == fleetName`. `TopoOrder`/`Validate` run on
  that filtered set, so a `depends_on` edge pointing outside the fleet surfaces as
  an "unknown dependency" (existing behaviour) rather than silently reaching across
  fleets.

### Degenerate case = today's behaviour

An **empty** session fleet scopes to **Ungrouped** (`p.Fleet == ""`). So a Conductor
started with no fleet (or any legacy/CLI path) behaves exactly as the shipped
single-pool feature does. **No migration needed** — the base feature is unreleased
(1.9 in development), and any existing `role:"project"` collection simply reads as
Ungrouped until tagged.

## Server routes

New (`auth` group, in a new `server/fleets.go`):

- `GET /api/fleets` — `[{name, color, description, default_engine, project_count,
  engines:[...], members:[{name, engine, depends_on}]}]`, Ungrouped folded in last
  (virtual, only when non-empty).
- `POST /api/fleets` `{name, color?, description?, default_engine?}` — create.
- `PATCH /api/fleets/:name` `{name?, color?, description?, default_engine?}` —
  rename + recolour + metadata (rename cascades per the invariants above).
- `DELETE /api/fleets/:name` — delete (members → Ungrouped).
- `POST /api/fleets/:name/projects` `{collection}` — assign a project to the fleet
  (writes the collection's `fleet` tag; promotes a plain collection to
  `role:"project"` if needed, seeding `engine` from `default_engine`).
- `DELETE /api/fleets/:name/projects/:collection` — unassign (→ Ungrouped).

**Coordinate** reuses `POST /api/sessions {squad:"Fleet", fleet:"<name>"}` — no new
route; it opens a scoped Conductor chat.

Cross-browser: a `fleets_changed` event on `/api/events` (no sid) on any
create/rename/delete/assign, handled like `collections_changed`.

**Route placement:** `/api/fleets/...` is a fresh top-level tree — no collision with
the `/api/sessions/:id/...` wildcard (the recurring gin route-tree trap).

## Web UI

Entirely additive over the shipped collections sidebar
([web/app.js](../../web/app.js), [web/css/features/collections.css](../../web/css/features/collections.css)).

- **Fleets section** below Collections in the left sidebar, rendered from
  `GET /api/fleets`. Each fleet = a collapsible group: node-cluster icon (fleet
  colour), name, project count, engine-mix dots, and a **Coordinate ▸** header
  action. Members render as project rows: **hexagon** glyph (vs the folder glyph),
  engine badge (`omnis`/`claude`, pill style — dot is a considered alternative in
  the mockup), and a `→dep` chip. **Ungrouped** renders as a dashed, muted group,
  only when it has members.
- **Fleet CRUD:** `+ New fleet` in the section header; a fleet-header context menu
  (rename, recolour, set default engine, add/remove project, delete) reusing the
  themed `uiPrompt`/`uiConfirm` + `showFolderCtxMenu` machinery collections already
  use.
- **Guarded project behaviour** (the "not a topic folder" contract):
  - Projects are **not** rendered in the Collections list and **not** in the
    chat "Move to collection" picker.
  - A project row's context menu offers *Coordinate this fleet · View driver
    sessions · Edit project… · Remove from fleet* — **no** *New chat here / Move to
    / Change color*.
  - Dragging a chat onto a project row is refused (no-entry cue, no drop).
- **Coordinate ▸** → `POST /api/sessions {squad:"Fleet", fleet}` → opens the scoped
  Conductor chat (appears in the normal session list, pinned to the Fleet squad).
- **i18n:** new `fleets.*` keys in en/fr/es/de (`Fleet`, engine names, `omnis`,
  `claude` stay literal per the glossary); `make i18n`; `?v=` bump.

## Out of scope (unchanged deferrals + new)

Carried from the base design and untouched here: host-enforced topological
parallelism, task-scoped mailbox addressing, unattended-driver permission mode,
experiment branch merge-back, multi-server A2A, Agent-SDK worker. **New non-goals:**
a fleet is flat (no nested fleets); a project is in exactly one fleet (no sharing);
cross-fleet dependencies are not modelled (a dep must be same-fleet or it reads as
unknown).

## Testing strategy

- `internal/sessions`: `fleets.go` CRUD round-trip + the two cross-file invariants
  (delete clears member tags; rename migrates meta + member tags); `Fleet` field
  round-trips through `CollectionProfileFull`/`UpdateCollectionProfile`.
- `internal/fleet`: `Project.Fleet` populated; scoped filtering (a Payments scope
  excludes a Billing project); empty scope ⇒ Ungrouped set.
- `server`: real-router tests for the `/api/fleets` CRUD + assign/unassign, the
  `fleets_changed` broadcast, and the session `fleet` field flowing into
  `fleet_projects`/`fleet_dispatch` scope (a cross-fleet dispatch is rejected).
- No-op: no `fleets.json` + no `fleet` tags ⇒ Fleets section empty, all projects
  Ungrouped, dispatch byte-identical to the shipped feature.

## No-op contract (summary)

With no fleets defined and no `fleet` tags, `fleets.json` is absent and `GET
/api/fleets` returns an empty list (no named fleets, and Ungrouped is folded in
only when it actually has members). Every project reads as Ungrouped, and a
Conductor started with no fleet scope sees exactly the shipped feature's pool. So a
Fleets section is simply never drawn, and dispatch is byte-identical to today.
CLI/TUI never call the resolvers, so they are unaffected. The feature is purely
additive.
