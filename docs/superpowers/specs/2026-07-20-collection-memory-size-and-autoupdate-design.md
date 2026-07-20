# Collection memory: size control + auto-update — design

Date: 2026-07-20

Two additive features for the **Memory** section of the web-UI collection
**Context** editor (`collectionContextDialog` in `web/app.js`):

1. **Memory size** — a per-collection `small` / `medium` / `large` setting that
   bounds how much prose the memory holds (words), medium the default.
2. **Auto-update** — a per-collection toggle that lets the memory be
   re-distilled from recent chats and committed automatically after the
   collection goes idle, with a one-click revert safety net.

Both are per-collection, server-persisted, and degrade to a byte-identical no-op
when unset. CLI/TUI are untouched (the collection context editor + the
auto-update worker are server-only, like the curator / idle-indexer).

## Background (current state)

- A collection carries two prose blocks under
  `$OMNIS_HOME/collections/<name>/`: `instructions.md` (hand-authored, stable)
  and `memory.md` (durable facts). Owned by `internal/collectionctx` (imports
  only `internal/paths` + stdlib — no `agent`/`sessions` cycle).
- Both are injected into the answering root's system instruction **every turn**
  for any session in the collection (`collectionctx.Resolve` →
  `<collection-context>` block). So memory size is a **per-turn prompt cost**
  (mitigated but not eliminated by prompt caching of the stable prefix).
- The **distiller** (`agent.Manager.DistillCollectionMemory` +
  `buildDistillRequest` in `agent/collection_memory.go`) reconciles the current
  memory with material gathered from recent sessions, via one off-LLM call on
  the eval model (leader fallback). Output is hard-capped at
  `collectionMemoryOutputCap = 6000` chars today.
- Material gathering is `gatherCollectionMaterial` in
  `server/collection_memory.go`: the collection's recent, non-hidden, non-empty
  sessions, most-recent-first, user+assistant turn text only, capped.
- Today's distill route `POST /api/collections/:name/memory/distill` is
  **propose-then-commit**: it returns `{proposed, current}` and deliberately
  **does not write** `memory.md`. CLAUDE.md documents a fully-automatic
  idle-triggered distiller as "Phase 3 … unwired by design", precisely to avoid
  an evolving memory silently injecting a wrong fact into every chat. **Feature
  2 wires Phase 3 with an explicit safety net + enable warning** (decided below).
- Per-collection scalars live in `collections.json` under
  `paths.ConfigWriteDir()` as a `profiles` map keyed by canonical name:
  `collectionProfile{Squad, Cwd}` (`internal/sessions/collections.go`), with
  `CollectionProfile(name)` / `SetCollectionProfile(name, squad, cwd)` accessors
  and rename/delete cascade (`pruneProfilesLocked`, `RenameCollection`,
  `RemoveCollection`).
- The idle-indexer (`server/idle_indexer.go`) already fires
  **`EventSessionIndexNow`** for a session idle ≥5 min, and the archive handler
  fires it immediately. The precedent/session indexes subscribe to it.

Empirical calibration (existing data): only one collection has a memory today —
LangFuse, **270 words / 2149 chars / ~540 tokens**; two have instructions (184 /
312 words). The one organic memory sits inside the proposed *medium* budget.

## Feature 1 — Memory size

### Word limits (decided)

| Size | Limit | ≈ tokens/turn | Intent (shown in tooltip) |
|---|---|---|---|
| Small | **under 200 words** | ~260 | Essentials only — a few key facts |
| Medium *(default)* | **under 350 words** | ~460 | A solid working brief |
| Large | **under 700 words** | ~920 | A detailed dossier for complex workstreams |

`large` stays below today's 6000-char cap, so nothing regresses; `medium` is a
meaningfully tighter default. The tooltip on each radio reads
**"Under <N> words — <intent>"** via the existing `data-tip` hover system.

### Enforcement — soft target + live counter (decided)

- **Distiller** — `buildDistillRequest` / `DistillCollectionMemory` take a word
  limit derived from the size; the system prompt gains "keep the memory under ~N
  words", and `collectionMemoryOutputCap` becomes a per-size backstop
  (`wordLimit × ~8` chars). Governs both the **Generate** button and auto-update.
- **Editor** — a live **`123 / 350 words`** counter under the Memory textarea,
  turning **amber** when over the limit. It **never truncates** manually typed
  text — it is a budget indicator; the user stays in control.
- **Injection is untouched** — `collectionctx.Resolve` keeps reading `memory.md`
  verbatim (no size awareness), preserving its `paths`-only layering. The size
  bounds what the *distiller* writes and *guides* manual editing; it does not
  clip an over-budget hand-written memory at inject time.

### Storage

Add `MemorySize string` to `collectionProfile` (`json:"memory_size,omitempty"`,
"" ⇒ medium). Extend the profile accessors (see "Shared: profile accessors").

## Feature 2 — Auto-update (auto-commit + revert net + enable warning)

### Toggle & enable warning (decided)

- Add `AutoUpdate bool` to `collectionProfile`
  (`json:"auto_update,omitempty"`), **default off**.
- Turning it **on** in the editor first raises a `uiConfirm` **warning**:
  *"Memory will be rewritten automatically from recent chats, without asking you
  to review each change. The previous version is always kept so you can revert.
  Enable auto-update?"* — the toggle only flips (and persists) on confirm.

### Trigger (server-only; reuses the idle rail)

A new server component subscribes to **`EventSessionIndexNow`** (the same event
the idle-indexer already fires at ≥5 min idle + on archive — the "quiet after
new activity" signal, no new timer machinery). On each event, for the session's
collection, it auto-distills **iff all hold**:

1. the collection's `auto_update` is on;
2. **content changed** — a hash of `gatherCollectionMaterial(collection)`
   differs from the last auto-distill's material hash (held in-memory,
   best-effort; the authoritative guards are #3 + commit-on-change below, so a
   restart risks at most one redundant LLM call per collection);
3. **min-interval** — ≥ `OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL` (default
   **30m**) since this collection's `last_memory_update`, so a busy collection
   cannot churn the model.

It then runs `DistillCollectionMemory` (respecting the collection's **size**
cap) and commits **only if** the result differs from the current memory.

### Commit + revert net (decided)

- Before overwriting `memory.md`, snapshot the current content to
  **`memory.prev.md`** (new `collectionctx` helpers `PrevMemoryPath`,
  `ReadPrevMemory`, `WritePrevMemory`, `HasPrevMemory`; the existing
  `RenameDir`/`RemoveDir` already move/drop the whole collection dir, so the
  snapshot rides along on rename/delete).
- Write the new memory; record `last_memory_update` (unix seconds) on the
  profile.
- The editor shows a **"⟲ Auto-updated 12m ago — Revert"** marker whenever a
  `memory.prev.md` snapshot exists. **Revert** restores `memory.prev.md` →
  `memory.md` via a new `POST /api/collections/:name/memory/revert`, then
  **consumes the snapshot** (deletes `memory.prev.md` + clears
  `last_memory_update`) so the marker disappears and a second revert is a no-op.
- **Snapshot lifecycle — the net covers exactly the last *unreviewed* auto-commit.**
  A manual Save of the memory (the PUT prose path) **also clears the snapshot +
  `last_memory_update`**: once the user has reviewed/edited the memory they have
  taken ownership, so the marker disappears and revert no longer offers to undo
  a change they've already superseded. A subsequent auto-commit starts a fresh
  snapshot from the (manually saved) baseline.

### Deliberately out of scope (v1)

- **No live refresh of an open editor modal** when an auto-commit lands — the
  user sees the new memory (and the revert marker) the next time they open the
  panel. (A `collections_changed`-style broadcast could add this later.)
- No per-collection override of the min-interval / idle threshold (env-global
  only in v1).

## Shared plumbing

### Profile accessors (`internal/sessions/collections.go`)

`collectionProfile` gains `MemorySize`, `AutoUpdate`, `LastMemoryUpdate int64`.
Because `CollectionProfile` currently returns `(squad, cwd)`, add a
struct-returning accessor (e.g. `CollectionProfileFull(name) CollectionProfile`
exporting the fields) and a setter that updates individual fields without
clobbering the others, plus a small `TouchCollectionMemoryUpdate(name, ts)`
used by the auto-updater. The rename/delete cascade is unchanged (it
moves/deletes the whole profile entry, so new fields ride along).

### Distiller (`agent/collection_memory.go`)

- `sizeWordLimit(size) int` → 200 / 350 / 700 (default 350).
- `buildDistillRequest(current, material, wordLimit)` injects the word target
  into the prompt and derives the output cap.
- `DistillCollectionMemory(ctx, current, material, wordLimit)` threads it
  through. The existing distill route passes the collection's size.

### Routes (`server/collections.go`, `server/collection_memory.go`)

- **PATCH `/api/collections/:name`** body gains `memory_size *string`,
  `auto_update *bool` (merged with the stored profile like `squad`/`cwd`;
  `memory_size` validated ∈ {"","small","medium","large"}).
- **GET `/api/collections/:name/context`** response gains `memory_size`,
  `auto_update`, `last_memory_update`, `has_prev_memory`.
- **POST `/api/collections/:name/memory/revert`** — restores the snapshot;
  returns the restored memory; 404 when no snapshot exists.
- `handleDistillCollectionMemory` passes the collection's size to the distiller
  (Generate button now honours size too).
- **GET `/api/collections`** rows: optionally add `auto_update` for a future
  rail badge (nice-to-have, not required).

### Auto-update worker (`server/collection_autoupdate.go`, new)

Subscribes to `EventSessionIndexNow` on the process bus, resolves the session's
collection, applies the three gates, distills, snapshots + commits, and updates
`last_memory_update`. Wired once from `server/main.go` (like the idle-indexer /
docs-indexer). Env: `OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL` (Go duration,
default `30m`).

### Web UI (`web/app.js`, `web/css/features/dialogs.css`)

`collectionContextDialog` Memory section head gains:
- the **size radio group** (small/medium/large) with per-radio `data-tip`
  "Under N words — <intent>";
- the **auto-update toggle** (+ the `uiConfirm` enable warning);
- the **revert marker** ("⟲ Auto-updated N ago — Revert") when
  `has_prev_memory`;
- a **live word counter** under the textarea (amber over the limit).

Save path: PATCH the scalars (`memory_size`, `auto_update`) alongside
`squad`/`cwd`, then PUT the prose, as today. New i18n keys under `collections.*`
(en/fr/es/de) → `make i18n` → bump `app.js` + `locales.js` `?v=` in
`web/index.html`.

## Testing

- `agent/collection_memory_test.go`: `sizeWordLimit` mapping; `buildDistillRequest`
  injects the word target and caps output per size.
- Auto-update gate unit test: given (auto_update, content-hash-changed,
  since-last) combinations, the worker's `shouldAutoUpdate` predicate fires only
  when all three hold. Commit-on-change: identical distill ⇒ no write, no
  snapshot.
- `collectionctx` prev-snapshot round-trip (write memory → snapshot → revert
  restores; empty removes).
- Route test (drives the real router): PATCH round-trips `memory_size` +
  `auto_update`; context GET surfaces them + `has_prev_memory`; revert restores
  and consumes the snapshot; a manual memory PUT clears an existing snapshot.

## No-op contract

- Size unset ⇒ medium everywhere; the counter/radios are pure UI.
- `auto_update` off ⇒ the worker's per-event check is a cheap map lookup that
  returns immediately; nothing is distilled or written. Byte-identical to today.
- No embedder / no eval model ⇒ distillation already degrades (the existing
  route returns an error); the worker logs and skips.
- CLI/TUI: no collection editor, no worker — unchanged.

## Docs to update on implementation

- **CLAUDE.md** "Collection context" section: `memory_size`, the auto-update
  worker (Phase 3 now wired, with the safety net), the revert route/snapshot,
  and the new env var in the environment-variable table.
- **`internal/features/FEATURES.md`**: user-facing bullets (memory size control;
  automatic memory updates) under the in-development minor section.
