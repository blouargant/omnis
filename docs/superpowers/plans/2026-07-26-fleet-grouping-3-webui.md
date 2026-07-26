# Fleet grouping — Plan 3: web UI (Fleets sidebar, CRUD, guarded behaviour)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface named fleets in the web UI: a dedicated **Fleets** sidebar section (out of the normal Collections list), fleet CRUD + project membership, the **Coordinate** action, and the guarded behaviour that makes a project unmistakably not a topic folder.

**Architecture:** Additive front-end over the Plan-2 `/api/fleets` routes, mirroring the existing collections-rail code (`loadCollections`/`renderCollections`/`buildCollectionRow`/`openCollectionCtxMenu`, `COLLECTION_COLORS`, `showFolderCtxMenu`, `uiPrompt`/`uiConfirm`). One tiny server addition: the collections list row carries `role` so the client can filter projects out of the Collections list. Verification is **manual / Playwright smoke** on a local server — the web UI has no Go unit tests.

**Tech Stack:** Vanilla JS (`web/app.js`), CSS (`web/css/features/collections.css`), i18n JSON (`web/i18n/*.json` + `make i18n`), gin (one server field). No framework/build step.

**Spec:** [docs/superpowers/specs/2026-07-26-fleet-grouping-design.md](../specs/2026-07-26-fleet-grouping-design.md) · **Mockup:** https://claude.ai/code/artifact/a0665d8e-1a5d-48e2-ab1e-7719a37073df
**Depends on:** Plan 2 (`/api/fleets` GET/POST/PATCH/DELETE + `…/projects` assign/unassign; `POST /sessions {fleet}`; `fleets_changed`/`collections_changed` push).

## Global Constraints

- **Guarded, not just visual:** a `role:"project"` collection must NOT appear in the Collections rail, NOT in any session "Move to" picker, and MUST refuse a chat drop. It appears ONLY under the Fleets section.
- **Distinct rendering (from the mockup):** Fleets is its own section below Collections; each fleet is a collapsible group (node-cluster icon in the fleet colour, name, project count, engine-mix dots, a **Coordinate ▸** header action); a project row uses a **hexagon** glyph (vs the folder), an engine badge (`omnis`/`claude`), and a `→dep` chip; an **Ungrouped** group (dashed/muted) renders only when it has members.
- **Reuse, don't reinvent:** context menus via `showFolderCtxMenu` (+ `SEP`); dialogs via `uiPrompt`/`uiConfirm`/the colour-swatch pattern in `collectionDialog`; colours from `COLLECTION_COLORS` / `collectionAccentVar`; the Coordinate chat via the existing session-create + open path (`newChat`/`selectSession`).
- **Glossary literal in i18n:** `Fleet`, `omnis`, `claude`, `Coordinate`, `Squad`, `Project` — do not translate; translate labels/tooltips/menus. New keys under `fleets.*` (en/fr/es/de), then `make i18n`, then bump the `?v=` on `app.js` **and** `i18n/locales.js` in `web/index.html`.
- **No-op / additive:** with no fleets and no project collections, the Fleets section is empty/hidden and the Collections rail + Move-to pickers are byte-identical to today.
- **Verification is smoke, not `go test`.** Each task ends with a Playwright (or manual) smoke check against a local server started with `OMNIS_WEB_DIR=$(pwd)/web` and no token (see the per-task steps). Seed fleets/projects via `/api/fleets` (curl) until Task 3 provides the UI.
- Commit after every task with the `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer. Bump `?v=` whenever `app.js` changes.

---

### Task 1: Fleets sidebar section (render + collections filter + sync)

**Files:**
- Modify: `server/collections.go` (`collectionInfo` gains `Role`; the builder sets it)
- Modify: `web/index.html` (add `#fleets-list` section; bump `?v=`)
- Modify: `web/app.js` (`fleetsData`, `loadFleets`, `renderFleets`, `buildFleetGroup`, `buildProjectRow`, `fleetProjectNames`; filter projects out of `buildCollectionRow`; boot + `subscribeGlobalEvents` wiring)
- Modify: `web/css/features/collections.css` (Fleets section + project-row styles)
- Modify: `web/i18n/en.json` + `fr.json` + `es.json` + `de.json` (`fleets.*` keys), then `web/i18n/locales.js` via `make i18n`

**Interfaces:**
- Consumes: `GET /api/fleets` → `[{name, color, description, default_engine, project_count, engines, members:[{name,engine,depends_on}], ungrouped}]`; `GET /api/collections` rows now carry `role`.
- Produces: `fleetsData` (module-level), `fleetProjectNames()` (Set of project collection names), `loadFleets()`/`renderFleets()` — consumed by Tasks 2–4.

- [ ] **Step 1: Add `Role` to the collections list row (server)**

In `server/collections.go`, add to `collectionInfo` (after `HasContext`):

```go
	// Role is the collection's fleet role ("project" ⇒ it is a fleet project and
	// the web UI files it under Fleets, not the Collections rail; "" ⇒ a normal
	// collection).
	Role string `json:"role,omitempty"`
```

In `handleListCollections`, where each user-collection row is built (the `out = append(out, collectionInfo{...})` with `Squad`/`Cwd`/`HasContext`), add `Role: sessions.CollectionProfileFull(n).Role,` to the literal. (If `squad`/`cwd` are already read from a `CollectionProfileFull(n)` value there, reuse it rather than calling twice.)

Verify: `go build ./... && go test ./server/ -run TestListCollections` (expected PASS — additive field).

- [ ] **Step 2: Add the `#fleets-list` section to `web/index.html`**

Immediately after the `<ul id="collections-list" …></ul>` line (line ~83) and before `<div id="folders-panel" …>`, add:

```html
    <div id="fleets-section" hidden>
      <div class="fleets-head">
        <span class="fleets-head-label" data-i18n="fleets.sectionLabel">Fleets</span>
        <button id="new-fleet-btn" type="button" class="fleets-new-btn"
                data-i18n-tip="fleets.newTip" data-tip="New fleet" aria-label="New fleet">+</button>
      </div>
      <ul id="fleets-list" aria-label="fleets"></ul>
    </div>
```

Bump the `?v=` query on the `app.js` and `i18n/locales.js` `<script>` tags (find the current values and increment both).

- [ ] **Step 3: Render the Fleets section (app.js)**

First read the existing rail code you are mirroring: `COLLECTION_COLORS`/`collectionAccentVar`/`collectionInitials` (~line 6064), `loadCollections`/`renderCollections`/`buildCollectionRow` (~6115-6189), and the `els` cache + boot sequence (search `els.collectionsList`). Add an `els.fleetsSection`/`els.fleetsList`/`els.newFleetBtn` lookup beside `els.collectionsList`.

Add near `collectionsData` (module state, ~line 1429):

```js
let fleetsData = []; // [{name,color,description,default_engine,project_count,engines,members,ungrouped}] from GET /api/fleets
```

Add the loader + renderers (place them right after `renderCollections`):

```js
// fleetProjectNames returns the set (lowercased) of collection names that are
// fleet projects — the ones filtered OUT of the Collections rail and every
// "Move to" picker, and refused as chat-drop targets.
function fleetProjectNames() {
  const set = new Set();
  for (const f of fleetsData) for (const m of (f.members || [])) set.add(String(m.name).toLowerCase());
  return set;
}

// loadFleets fetches the fleet groups (named fleets + a virtual Ungrouped when it
// has members) and repaints the Fleets section. Idempotent; called at boot and on
// every fleets_changed / collections_changed push.
async function loadFleets() {
  try {
    const res = await apiFetch("/api/fleets", { cache: "no-store" });
    fleetsData = await res.json();
    if (!Array.isArray(fleetsData)) fleetsData = [];
  } catch (e) { console.error("loadFleets:", e); fleetsData = []; }
  renderFleets();
}

function renderFleets() {
  const list = els.fleetsList;
  if (!list) return;
  list.innerHTML = "";
  for (const f of fleetsData) list.appendChild(buildFleetGroup(f));
  // Hide the whole section when there are no fleets AND no ungrouped projects.
  if (els.fleetsSection) els.fleetsSection.hidden = fleetsData.length === 0;
}

// buildFleetGroup renders one collapsible fleet: a header (chevron, node-cluster
// icon in the fleet colour, name, engine-mix dots, project count, Coordinate) and
// its project rows. Ungrouped is a dashed/muted variant with no metadata/Coordinate.
function buildFleetGroup(f) {
  const wrap = document.createElement("li");
  wrap.className = "fleet-group" + (f.ungrouped ? " ungrouped" : "");
  wrap.dataset.fleet = f.name;
  const accent = collectionAccentVar(f.color);
  if (accent) wrap.style.setProperty("--fleet-accent", accent);

  const hd = document.createElement("div");
  hd.className = "fleet-hd";
  const dots = (f.engines || []).map((e) =>
    `<i class="fleet-dot ${e === "claude" ? "eng-claude" : "eng-omnis"}" title="${escHtml(e)}"></i>`).join("");
  hd.innerHTML =
    `<span class="fleet-chev">${ICON_CHEVRON}</span>` +
    `<span class="fleet-ficon">${f.ungrouped ? ICON_HEX : ICON_NODES}</span>` +
    `<span class="fleet-name"></span>` +
    `<span class="fleet-meta"><span class="fleet-dots">${dots}</span>` +
    `<span class="fleet-count">${f.project_count || 0}</span>` +
    (f.ungrouped ? "" : `<button type="button" class="fleet-coord" data-i18n="fleets.coordinate">Coordinate</button>`) +
    `</span>`;
  hd.querySelector(".fleet-name").textContent = f.ungrouped ? tr("fleets.ungrouped") : f.name;
  wrap.appendChild(hd);

  const body = document.createElement("ul");
  body.className = "fleet-body";
  for (const m of (f.members || [])) body.appendChild(buildProjectRow(f, m));
  wrap.appendChild(body);

  // Collapse toggle (skip clicks on the Coordinate button — Task 2 wires it).
  hd.addEventListener("click", (ev) => {
    if (ev.target.closest(".fleet-coord")) return;
    wrap.classList.toggle("collapsed");
  });
  // Fleet-header context menu (Task 3 fills it in). Ungrouped has none.
  if (!f.ungrouped) hd.addEventListener("contextmenu", (ev) => openFleetCtxMenu(ev, f));
  return wrap;
}

// buildProjectRow renders one project: hexagon glyph in the project's collection
// colour, name, engine badge, and a →dep chip when it depends on others.
function buildProjectRow(f, m) {
  const li = document.createElement("li");
  li.className = "project-row";
  li.dataset.name = m.name;
  li.dataset.fleet = f.name;
  const accent = collectionAccentVar(collectionColorByName(m.name));
  if (accent) li.style.setProperty("--col-accent", accent);
  const eng = m.engine === "claude" ? "claude" : "omnis";
  const dep = (m.depends_on && m.depends_on.length)
    ? `<span class="project-dep" title="${escHtml((m.depends_on || []).join(", "))}">${ICON_ARROW}${escHtml(m.depends_on[0])}${m.depends_on.length > 1 ? "…" : ""}</span>`
    : "";
  li.innerHTML =
    `<span class="project-hex">${ICON_HEX}</span>` +
    `<span class="project-name"></span>${dep}` +
    `<span class="project-eng ${eng}">${escHtml(eng)}</span>`;
  li.querySelector(".project-name").textContent = m.name;
  li.setAttribute("data-tip", m.name);
  // Project rows are NOT chat-drop targets (guarded — Task 4 also excludes them
  // from Move-to). A context menu (Task 4) offers Coordinate/Edit/Remove.
  li.addEventListener("contextmenu", (ev) => openProjectCtxMenu(ev, f, m));
  return li;
}
```

Add the three SVG icon constants beside the existing `ICON_*` consts (search `const ICON_FOLDER`): `ICON_CHEVRON`, `ICON_HEX` (hexagon), `ICON_NODES` (three connected circles), `ICON_ARROW` (a short →). Use the same inline-SVG style as the existing icons; copy the paths from the mockup (`fleet-grouping-mockup.html` — the chevron, hexagon `M12 2.6l7.5 4.33v9.14L12 20.4l-7.5-4.33V6.93z`, the node-cluster, and the `M3 8h8M8 4l4 4-4 4` arrow).

Add **stub** handlers so Tasks 2–4 can fill them (prevents a ReferenceError now):

```js
function openFleetCtxMenu(ev, f) { /* Task 3 */ }
function openProjectCtxMenu(ev, f, m) { /* Task 4 */ }
```

- [ ] **Step 4: Filter projects out of the Collections rail**

In `buildCollectionRow` (or its caller `renderCollections`'s loop), skip project collections. The cleanest place is the loop in `renderCollections`:

```js
  els.collectionsList.innerHTML = "";
  for (const c of collectionsData) {
    if ((c.role || "") === "project") continue; // projects live under Fleets, not here
    els.collectionsList.appendChild(buildCollectionRow(c));
  }
```

- [ ] **Step 5: Wire boot + push sync**

Find where `loadCollections()` is called at boot (search `loadCollections(`) and add `loadFleets();` beside it. In `subscribeGlobalEvents` (search `collections_changed`), make the `collections_changed` handler also `loadFleets()`, and add a `fleets_changed` case that runs both `loadFleets()` and `loadCollections()` (a membership change moves a collection between the two lists):

```js
        } else if (event === "collections_changed") {
          loadCollections();
          loadFleets();
        } else if (event === "fleets_changed") {
          loadFleets();
          loadCollections();
```

- [ ] **Step 6: Styles**

In `web/css/features/collections.css`, add the Fleets section styles adapted from the mockup (`fleet-grouping-mockup.html` `<style>` — the `.fleets`, `.fleet`, `.fleet-hd`, `.prow`/`.project-row`, `.eng`, `.dep`, `.fleet.ungrouped`, `.newfleet` rules), renamed to the class names used above (`.fleets-section`/`.fleets-head`/`.fleets-new-btn`, `.fleet-group`/`.fleet-hd`/`.fleet-body`, `.project-row`/`.project-hex`/`.project-name`/`.project-eng.omnis`/`.project-eng.claude`/`.project-dep`, `.fleet-group.ungrouped`, `.fleet-coord`, `.fleet-dot.eng-omnis`/`.eng-claude`, `.fleet-group.collapsed .fleet-body{display:none}`). Use the existing collection theme tokens (`--col-accent`, `--fleet-accent`, `var(--collection-*)`); keep it consistent with the sidebar's existing look rather than copying the mockup's standalone page frame.

- [ ] **Step 7: i18n**

Add to `web/i18n/en.json` (and translate into `fr`/`es`/`de` — keep `Fleet`/`omnis`/`claude`/`Coordinate`/`Project` literal):

```json
  "fleets.sectionLabel": "Fleets",
  "fleets.ungrouped": "Ungrouped",
  "fleets.coordinate": "Coordinate",
  "fleets.newTip": "New fleet",
  "fleets.projectsCount": "{count} projects"
```

(More keys are added by Tasks 2–4.) Then run `make i18n` to regenerate `web/i18n/locales.js`, and confirm the generator prints no missing-key errors for the four locales.

- [ ] **Step 8: Smoke test (Playwright / manual)**

Start a local server and seed a fleet + project via the API, then verify the render:

```bash
# In one shell — a tokenless local server serving the branch's web assets:
env -u OMNIS_CONFIG_PATH OMNIS_WEB_DIR="$(pwd)/web" OMNIS_SERVER_TOKEN="" OMNIS_HOME=/tmp/omnis-fleet-smoke \
  go run ./server &
# Seed data (no auth when token is empty):
curl -s -XPOST localhost:8080/api/fleets -d '{"name":"Payments","color":"blue","default_engine":"omnis"}'
curl -s -XPOST localhost:8080/api/collections -d '{"name":"api"}'
curl -s -XPOST localhost:8080/api/fleets/Payments/projects -d '{"collection":"api"}'
```

Then load `http://localhost:8080` (Playwright `browser_navigate`) and confirm: a **Fleets** section appears below Collections; a **Payments** group with a `1` count and an `api` project row (hexagon + `omnis` badge); `api` is **absent** from the Collections rail. Take a screenshot. Toggle collapse. (Commit even though Coordinate/CRUD aren't wired yet — this task is the render.)

- [ ] **Step 9: Commit**

```bash
git add server/collections.go web/index.html web/app.js web/css/features/collections.css web/i18n/
git commit -m "$(printf 'feat(fleet): Fleets sidebar section — render groups + projects, filter from Collections\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 2: Coordinate — open a fleet-scoped Conductor chat

**Files:**
- Modify: `web/app.js` (the `.fleet-coord` click → create + open a `squad:"Fleet", fleet:<name>` session)
- Modify: `web/i18n/*.json` (already has `fleets.coordinate`; add a toast key), `make i18n`

**Interfaces:**
- Consumes: `POST /api/sessions {squad, fleet}` (Plan 2 Task 5); the existing new-session-then-open path.
- Produces: the Coordinate entry point.

- [ ] **Step 1: Find the existing "create a session on squad X and open it" path**

Read `newChat` and how a squad is passed to `POST /api/sessions` (search `"/api/sessions"` `method: "POST"`), and how the created session is opened (`selectSession`/`activateTab`/`bindSessionToPanel`). The Coordinate flow reuses exactly that, adding `fleet` to the body and forcing `squad:"Fleet"`.

- [ ] **Step 2: Wire the Coordinate button**

In `buildFleetGroup`, replace the collapse-handler's early `return` on `.fleet-coord` with an actual action — add a dedicated listener on the button:

```js
  const coordBtn = hd.querySelector(".fleet-coord");
  if (coordBtn) coordBtn.addEventListener("click", (ev) => { ev.stopPropagation(); coordinateFleet(f.name); });
```

Add `coordinateFleet` next to `loadFleets`:

```js
// coordinateFleet opens a new Conductor chat scoped to a fleet: a Fleet-squad
// session pinned to that fleet (fleet_projects/fleet_dispatch then see only its
// projects). Mirrors newChat's create-then-open, adding {squad:"Fleet", fleet}.
async function coordinateFleet(name) {
  try {
    const res = await apiFetch("/api/sessions", {
      method: "POST",
      body: JSON.stringify({ squad: "Fleet", fleet: name, collection: fp() ? undefined : undefined }),
    });
    if (!res.ok) { const b = await res.json().catch(() => ({})); showToast(b.error || tr("fleets.coordinateFailed"), "err"); return; }
    const j = await res.json();
    await loadSessions();
    selectSession(j.session_id); // open it in the focused pane (match newChat's open call)
  } catch (e) { console.error(e); showToast(tr("fleets.coordinateFailed"), "err"); }
}
```

> Adapt the create/open calls to the EXACT helpers `newChat` uses in this codebase (the squad name string must match the shipped Fleet squad — confirm via `GET /api/squads` that it is `"Fleet"`; if the manager lowercases it, `POST /sessions` validates case-insensitively via `HasSquad`). If `newChat` opens via `bindSessionToPanel(panel, id)` rather than `selectSession(id)`, use that.

- [ ] **Step 3: i18n**

Add `"fleets.coordinateFailed": "Could not start the fleet coordinator."` (+ translations); `make i18n`; bump `?v=`.

- [ ] **Step 4: Smoke test**

With the Task-1 seeded fleet, click **Coordinate ▸** on Payments → a new chat opens; confirm via `GET /api/sessions/<id>` (or the network tab) that the created session has `squad:"Fleet"` and is fleet-scoped (send a message asking it to list its fleet projects — it should see only `api`). Screenshot the opened chat.

- [ ] **Step 5: Commit**

```bash
git add web/app.js web/i18n/ web/index.html
git commit -m "$(printf 'feat(fleet): Coordinate button opens a fleet-scoped Conductor chat\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 3: Fleet CRUD + project membership

**Files:**
- Modify: `web/app.js` (`+ New fleet`, `openFleetCtxMenu` rename/recolour/default-engine/delete, add/remove project)
- Modify: `web/i18n/*.json`, `make i18n`

**Interfaces:**
- Consumes: `POST /api/fleets`, `PATCH /api/fleets/:name`, `DELETE /api/fleets/:name`, `POST /api/fleets/:name/projects`, `DELETE /api/fleets/:name/projects/:collection`.
- Produces: the fleet management surface.

- [ ] **Step 1: `+ New fleet`**

Wire `els.newFleetBtn` (the `#new-fleet-btn`). Read the existing `collectionDialog` (name + colour swatch grid, ~line 6796) — build a small analogous `fleetDialog(existing)` returning `{name, color, default_engine}` (name input, the same swatch grid via `COLLECTION_COLORS`, and an engine `<select>` omnis/claude). On confirm `POST /api/fleets`; on `!ok` toast the error. `loadFleets()` on success (or rely on the `fleets_changed` push — but call `loadFleets()` directly too for immediacy, mirroring how `createCollection` calls `loadCollections`).

```js
els.newFleetBtn?.addEventListener("click", async () => {
  const chosen = await fleetDialog(null);
  if (!chosen) return;
  const res = await apiFetch("/api/fleets", { method: "POST", body: JSON.stringify(chosen) });
  if (!res.ok) { const b = await res.json().catch(() => ({})); showToast(b.error || tr("fleets.saveFailed"), "err"); return; }
  await loadFleets();
});
```

- [ ] **Step 2: Fleet-header context menu (`openFleetCtxMenu`)**

Replace the Task-1 stub. Mirror `openCollectionCtxMenu` (`showFolderCtxMenu` + `SEP`):

```js
function openFleetCtxMenu(ev, f) {
  showFolderCtxMenu(ev, [
    [tr("fleets.coordinate"), () => coordinateFleet(f.name)],
    [tr("fleets.addProject"), () => addProjectToFleet(f.name)],
    SEP,
    [tr("common.rename"), () => renameFleet(f.name)],
    [tr("fleets.editFleet"), () => editFleet(f)],
    SEP,
    [tr("common.delete"), () => deleteFleet(f.name)],
  ]);
}
```

Implement:
- `renameFleet(name)` — `uiPrompt` for a new name → `PATCH /api/fleets/:name {name}` → `loadFleets()` (rename cascades member tags server-side).
- `editFleet(f)` — `fleetDialog(f)` (prefilled) → `PATCH /api/fleets/:name {name?,color,default_engine,description?}` → `loadFleets()`.
- `deleteFleet(name)` — `uiConfirm` (warn its projects return to Ungrouped) → `DELETE /api/fleets/:name` → `loadFleets()` + `loadCollections()`.
- `addProjectToFleet(name)` — pick a collection to assign. Offer the collections that are NOT already projects (i.e. `collectionsData.filter(c => !c.general && (c.role||"") !== "project")`), plus, if the picker allows, promoting a plain collection. Build a small chooser via `showFolderCtxMenu` (one item per candidate) or a `uiPrompt`-style list; on choice `POST /api/fleets/:name/projects {collection}` → `loadFleets()` + `loadCollections()`. (Assigning promotes it to a project server-side, seeding engine from the fleet default.)

- [ ] **Step 3: i18n**

Add `fleets.addProject`, `fleets.editFleet`, `fleets.saveFailed`, `fleets.deleteConfirm`, `fleets.newTitle`, `fleets.engineLabel`, `fleets.colorLabel`, `fleets.nameLabel`, `fleets.noAssignable` (+ translations). `make i18n`; bump `?v=`.

- [ ] **Step 4: Smoke test**

`+ New fleet` → create "Billing" (amber, claude) → it appears. Right-click its header → **Add project…** → pick a plain collection → it becomes a project row under Billing and disappears from Collections. Rename Billing → Payments2 (its member tag follows). Delete it → its project returns to **Ungrouped**. Screenshot each state.

- [ ] **Step 5: Commit**

```bash
git add web/app.js web/i18n/ web/index.html
git commit -m "$(printf 'feat(fleet): fleet CRUD + project assign/unassign in the sidebar\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

### Task 4: Guarded project behaviour

**Files:**
- Modify: `web/app.js` (project-row context menu; exclude projects from Move-to pickers; refuse chat-drop onto a project)
- Modify: `web/i18n/*.json`, `make i18n`

**Interfaces:**
- Consumes: `DELETE /api/fleets/:name/projects/:collection` (Remove from fleet); Task 1's `fleetProjectNames()`.
- Produces: the "a project is not a topic folder" guarantee.

- [ ] **Step 1: Project-row context menu (`openProjectCtxMenu`)**

Replace the Task-1 stub — deliberately WITHOUT *New chat here / Move to / Change color*:

```js
function openProjectCtxMenu(ev, f, m) {
  showFolderCtxMenu(ev, [
    [tr("fleets.coordinate"), () => coordinateFleet(f.name)],
    [tr("fleets.editProject"), () => editCollectionContext({ name: m.name })], // reuse the context editor
    SEP,
    [tr("fleets.removeFromFleet"), () => removeProjectFromFleet(f.name, m.name)],
  ]);
}

function removeProjectFromFleet(fleet, collection) {
  return uiConfirm(tr("fleets.removeConfirm")).then(async (ok) => {
    if (!ok) return;
    const res = await apiFetch(`/api/fleets/${encodeURIComponent(fleet)}/projects/${encodeURIComponent(collection)}`, { method: "DELETE" });
    if (!res.ok) { const b = await res.json().catch(() => ({})); showToast(b.error || tr("fleets.saveFailed"), "err"); return; }
    await loadFleets(); await loadCollections();
  });
}
```

(Unassign returns the project to Ungrouped but keeps `role:project` — it stays under Fleets, in the Ungrouped group, not back in Collections. That is intended.)

- [ ] **Step 2: Exclude projects from every session "Move to" picker**

Find each "Move to collection" target list (search `collections.moveTo` / `menu.moveTo` — there are ~3 sites: the session-row context menu ~line 5679, another ~7041, and ~12069). In each, filter the target list to exclude project collections:

```js
  const projects = fleetProjectNames();
  const targets = [GENERAL_COLLECTION, ...collectionsData.filter(c => !c.general && !projects.has(c.name.toLowerCase())).map(c => c.name)];
```

(If a target list is built from `collectionsData` differently at one site, apply the same `!projects.has(...)` filter there.)

- [ ] **Step 3: Refuse a chat drop onto a project**

Project rows live in `#fleets-list`, not `#collections-list`, so the existing collection drop handler already never targets them. Add a defensive guard so dropping a dragged session over the Fleets section is a visible no-op (no `drop-over`, cursor `no-drop`): on `#fleets-list` (or each `.project-row`) add a `dragover` handler that, when `sessionDrag` is set, calls `ev.preventDefault()` + `ev.dataTransfer.dropEffect = "none"` and adds a `.drop-refused` class (a brief no-entry cue), and a `drop` handler that consumes the event without moving. Add the `.drop-refused` cue to CSS.

- [ ] **Step 4: i18n**

Add `fleets.editProject`, `fleets.removeFromFleet`, `fleets.removeConfirm` (+ translations). `make i18n`; bump `?v=`.

- [ ] **Step 5: Smoke test**

Right-click a project row → menu shows **Coordinate / Edit project… / Remove from fleet** and NO *New chat here / Move to / Change color*. Open a session's ⋯ → **Move to** → the project collection is absent from the target list. Drag a session over the Fleets section → it shows a no-entry cue and does not file. **Remove from fleet** a project → it moves to Ungrouped (still under Fleets, not back in Collections). Screenshot the menu + the Move-to list.

- [ ] **Step 6: Commit**

```bash
git add web/app.js web/css/features/collections.css web/i18n/ web/index.html
git commit -m "$(printf 'feat(fleet): guard projects from normal-collection actions (menu, move, drop)\n\nCo-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>')"
```

---

## Self-Review

**Spec coverage (Plan 3 slice):**
- Fleets sidebar section (groups, project rows, Ungrouped, engine badges, dep chip) → Task 1. ✓
- Projects lifted OUT of the Collections rail → Task 1 Step 4 (+ the `role` server field). ✓
- Coordinate ▸ opens a fleet-scoped Conductor → Task 2. ✓
- Fleet CRUD (+ New fleet, rename/recolour/default-engine/delete) + project assign/unassign → Task 3. ✓
- Guarded behaviour (project context menu without New-chat/Move/Colour; Move-to exclusion; drop refusal) → Task 4. ✓
- `fleets_changed`/`collections_changed` sync → Task 1 Step 5. ✓
- i18n en/fr/es/de + `make i18n` + `?v=` bump → every task's i18n step. ✓
- No-op/additive (no fleets ⇒ section hidden, rail unchanged) → Task 1 (`renderFleets` hides the empty section; the Collections filter is a no-op when nothing is a project). ✓

**Placeholder scan:** the new self-contained functions (`loadFleets`/`renderFleets`/`buildFleetGroup`/`buildProjectRow`/`coordinateFleet`/`openFleetCtxMenu`/`openProjectCtxMenu`/`removeProjectFromFleet`) carry complete code. The integration steps deliberately delegate to "the exact helper `newChat`/`createCollection` uses" and "each Move-to site" because those live in a 12k-line file and must be matched, not reinvented — each such step names the search anchor and the precise edit. The icon SVGs + CSS are sourced from the committed mockup. This is the appropriate altitude for web-glue over an existing large file; it is not a code placeholder.

**Type consistency:** `fleetsData` rows use the `/api/fleets` shape from Plan 2 Task 4 (`name/color/description/default_engine/project_count/engines/members[{name,engine,depends_on}]/ungrouped`); `fleetProjectNames()` (Task 1) is consumed by Task 4's Move-to filter; `openFleetCtxMenu`/`openProjectCtxMenu` are stubbed in Task 1 and filled in Tasks 3/4; `coordinateFleet` (Task 2) is reused by both context menus (Task 3/4).

---

## Execution Handoff

Plan 3 is the last plan. After it is smoke-verified, run the whole-feature final review across Plans 1–3, then use superpowers:finishing-a-development-branch.
