# Fleet Settings Phase 3 — Read-only Canvas Minimap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the **read-only, collapsible delegation-graph minimap** ("canvas") beneath the Fleet
tree — the "+ canvas preview" half of the chosen tree-first shape (spec §5/§6). Rendered from the
same store model, no editing, collapsed by default.

**Architecture:** A new pure, DOM-free layout module `web/fleet/canvas.js` computes node positions
+ delegation edges from `FleetStore.treeNodes(model)` (already tested), and an SVG renderer draws
them into a collapsible strip mounted under the tree in `renderFleetPane`. Zero server changes.

**Tech Stack:** Vanilla JS (UMD dual-export like `store.js`), inline SVG (no deps), `node --test`
for the layout, live Playwright smoke for the render. i18n via `web/i18n/*.json` + `make i18n`.

## Global Constraints

- **No new dependencies.** Inline SVG only (no chart libs); `node --test` only.
- **Read-only in v1.** The minimap renders + tooltips; it has NO edit affordances and mutates
  nothing. Selecting/editing stays entirely in the tree + editor.
- **Rendered from the store, not a second source of truth.** The layout consumes
  `FleetStore.treeNodes(model)` (the exact node set the tree renders), so tree and minimap can
  never disagree. No new topology logic.
- **Collapsed by default, persisted.** The strip starts collapsed; its open/closed state persists
  in `localStorage["agent_toolkit_fleet_canvas_open"]` (mirroring the sidebar collapse pattern).
- **Feature flag preserved.** Everything stays behind `agent_toolkit_fleet_preview`; flag OFF ⇒
  byte-identical to before.
- **Glyphs verbatim** (Phase 1): `◇` router · `⬢` squad · `⬡` leaderless · `★` leader · `•` member
  · `◆` sole · `↳` subagent.
- **i18n:** new user-facing strings are `tr(...)` keys in all four `web/i18n/{en,fr,es,de}.json`,
  then `make i18n`. Product nouns not translated.

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/fleet/canvas.js` | **Create** | Pure `layout(model)` (node cells + edges + dimensions from `treeNodes`) + SVG `render(container, model, opts)`. UMD dual-export (`module.exports` + `window.FleetCanvas`). |
| `web/fleet/canvas.test.js` | **Create** | `node --test` for `layout` (cell/edge/dimension assertions on a fixture). |
| `web/settings.js` | **Modify** | Mount a collapsible minimap strip under the tree in `renderFleetPane`; repaint it on `store.onChange`; persist collapse state. |
| `web/css/settings/fleet.css` | **Modify** | Styles for the strip, toggle, SVG nodes/edges. |
| `web/index.html` | **Modify** | Add `<script src="assets/fleet/canvas.js?v=1" defer>` before `settings.js`; bump `settings.js`/`settings.css` cache-busters. |
| `web/i18n/{en,fr,es,de}.json` | **Modify** | `fleet.canvas.*` keys (toggle label, empty state). |
| `web/docs/19-agents.md` | **Modify** | Note the minimap. |

---

## Task 1: Pure canvas layout module + node tests

**Files:**
- Create: `web/fleet/canvas.js`, `web/fleet/canvas.test.js`

**Interfaces:**
- Consumes: `FleetStore.treeNodes(model)` (browser global `window.FleetStore`; under node, `require("./store.js")`).
- Produces: `window.FleetCanvas.layout(model, opts?) → { cells, edges, width, height, columns }` where
  - `cells: [{ name, kind, depth, col, x, y, model_ref, shared }]` — one per tree node except the
    `unused-header`/`unused` rows (the minimap shows the ACTIVE topology only).
  - `edges: [{ x1, y1, x2, y2 }]` — a line from each node (depth ≥ 1) to its parent (the nearest
    preceding node in the SAME column with `depth - 1`).
  - `columns: [{ name, kind, x }]` — one per squad (router first), in `treeNodes` order.
  - `width`/`height` — SVG viewport size.

- [ ] **Step 1: Write the failing test** — `web/fleet/canvas.test.js`:

```js
"use strict";
const test = require("node:test");
const assert = require("node:assert");
const S = require("./store.js");
const C = require("./canvas.js");

function cfg() {
  return {
    router_squad: "omnis",
    agents: [
      { name: "omnis" },
      { name: "leader", leader: true, subagents: ["scout"] },
      { name: "scout" },
      { name: "helper" },
    ],
    squads: [
      { name: "omnis", leader: "none", members: ["omnis"] },
      { name: "system", leader: "leader", members: ["helper"] },
    ],
  };
}

test("layout produces one cell per active tree node (no unused rows)", () => {
  const model = S.build(cfg());
  const lay = C.layout(model);
  // router(omnis squad)+sole(omnis) | system squad+leader+scout(subagent)+helper(member) = 6
  const names = lay.cells.map(c => c.name);
  assert.ok(names.includes("leader"));
  assert.ok(names.includes("scout"));   // nested subagent present
  assert.ok(names.includes("helper"));
  assert.ok(!lay.cells.some(c => c.kind === "unused-header" || c.kind === "unused"));
});

test("columns are one per squad, router first, with increasing x", () => {
  const lay = C.layout(S.build(cfg()));
  assert.strictEqual(lay.columns[0].kind, "router");
  assert.ok(lay.columns.length >= 2);
  for (let i = 1; i < lay.columns.length; i++) {
    assert.ok(lay.columns[i].x > lay.columns[i - 1].x);
  }
});

test("a subagent edge connects the subagent to its member parent in the same column", () => {
  const lay = C.layout(S.build(cfg()));
  const scout = lay.cells.find(c => c.name === "scout");
  const leader = lay.cells.find(c => c.name === "leader" && c.kind === "leader");
  assert.strictEqual(scout.depth, 2);
  assert.strictEqual(leader.depth, 1);
  assert.strictEqual(scout.col, leader.col);       // same squad column
  // an edge exists whose endpoints match leader→scout centers
  const hit = lay.edges.some(e =>
    Math.abs(e.x2 - scout.x) < 40 && Math.abs(e.y2 - scout.y) < 40 &&
    Math.abs(e.y1 - leader.y) < 40);
  assert.ok(hit, "expected a leader→scout edge");
});

test("width/height are positive and bound the cells", () => {
  const lay = C.layout(S.build(cfg()));
  assert.ok(lay.width > 0 && lay.height > 0);
  for (const c of lay.cells) { assert.ok(c.x >= 0 && c.x <= lay.width); assert.ok(c.y >= 0 && c.y <= lay.height); }
});

test("empty topology yields no cells and non-negative dimensions", () => {
  const lay = C.layout(S.build({ agents: [], squads: [] }));
  assert.strictEqual(lay.cells.length, 0);
  assert.ok(lay.width >= 0 && lay.height >= 0);
});
```

- [ ] **Step 2: Run to verify failure**

Run: `node --test web/fleet/canvas.test.js`
Expected: FAIL — `Cannot find module './canvas.js'`.

- [ ] **Step 3: Implement `web/fleet/canvas.js`** (layout only for this task; `render` is Task 2 —
  but define the whole UMD file now with both, filling `layout` and a stub `render`):

```js
"use strict";
// FleetCanvas — pure delegation-graph layout + SVG renderer for the Fleet minimap.
// Dual-export: CommonJS (node --test) + browser global (window.FleetCanvas).
(function (root, factory) {
  const store = (typeof module !== "undefined" && module.exports)
    ? require("./store.js")
    : (typeof window !== "undefined" ? window.FleetStore : null);
  const mod = factory(store);
  if (typeof module !== "undefined" && module.exports) module.exports = mod;
  if (typeof window !== "undefined") window.FleetCanvas = mod;
})(this, function (FleetStore) {
  // Geometry (compact "strip" — squads laid out left→right; nodes stack down a column).
  const COL_W = 150, ROW_H = 26, INDENT = 16, PAD = 12, NODE_R = 5;

  function layout(model, opts) {
    const o = opts || {};
    const colW = o.colW || COL_W, rowH = o.rowH || ROW_H, indent = o.indent || INDENT, pad = o.pad || PAD;
    const nodes = FleetStore.treeNodes(model)
      .filter(n => n.kind !== "unused-header" && n.kind !== "unused");
    const cells = [];
    const edges = [];
    const columns = [];
    let col = -1, colX = pad, row = 0;
    // parents[d] = the last cell seen at depth d in the current column (for edge wiring).
    let parents = [];
    for (const n of nodes) {
      if (n.depth === 0) {                 // a squad row starts a new column
        col++; colX = pad + col * colW; row = 0; parents = [];
        columns.push({ name: n.name, kind: n.kind, x: colX });
      }
      const x = colX + n.depth * indent;
      const y = pad + row * rowH;
      const cell = {
        name: n.name, kind: n.kind, depth: n.depth, col,
        x, y,
        model_ref: (n.agent && n.agent.model_ref) || "",
        shared: n.shared || 0,
      };
      cells.push(cell);
      parents[n.depth] = cell;
      if (n.depth >= 1 && parents[n.depth - 1]) {
        const p = parents[n.depth - 1];
        edges.push({ x1: p.x, y1: p.y, x2: x, y2: y });
      }
      row++;
    }
    // Dimensions: widest column extent + a row budget tall enough for the tallest column.
    const maxRowsByCol = {};
    cells.forEach(c => { maxRowsByCol[c.col] = Math.max(maxRowsByCol[c.col] || 0, (c.y - pad) / rowH + 1); });
    const tallest = Object.values(maxRowsByCol).reduce((a, b) => Math.max(a, b), 0);
    const width = columns.length ? pad * 2 + columns.length * colW : 0;
    const height = cells.length ? pad * 2 + tallest * rowH : 0;
    return { cells, edges, width, height, columns };
  }

  const GLYPH = { router: "◇", squad: "⬢", leaderless: "⬡", leader: "★",
    member: "•", sole: "◆", subagent: "↳" };

  function esc(s) { return (typeof window !== "undefined" && window.escHtml)
    ? window.escHtml(s)
    : String(s == null ? "" : s).replace(/[&<>"]/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;" }[c])); }
  function t(k, v) { return (typeof window !== "undefined" && window.tr) ? window.tr(k, v) : k; }

  // render — Task 2 fills this in (SVG). Stub kept so the UMD shape is stable.
  function render(container, model, opts) { /* [TASK 2] */ }

  return { layout, render, GLYPH };
});
```

- [ ] **Step 4: Run tests to verify pass**

Run: `node --test web/fleet/canvas.test.js` and `node --test web/fleet/store.test.js`
Expected: PASS (5 new canvas tests + the 30 store tests).

- [ ] **Step 5: Wire `make test-web` to include canvas** — in the [Makefile](../../../Makefile)
  `test-web` target, add `node --test web/fleet/canvas.test.js` (alongside the store test). Run
  `make test-web` → all pass.

- [ ] **Step 6: Commit**

```bash
git add web/fleet/canvas.js web/fleet/canvas.test.js Makefile
git commit -m "feat(fleet): pure delegation-graph layout for the canvas minimap"
```

---

## Task 2: SVG renderer + collapsible minimap strip + i18n/docs

**Files:**
- Modify: `web/fleet/canvas.js`, `web/settings.js`, `web/css/settings/fleet.css`, `web/index.html`,
  `web/i18n/{en,fr,es,de}.json`, `web/docs/19-agents.md`
- Verify: live smoke.

**Interfaces:**
- Consumes: `FleetCanvas.layout`, `FleetCanvas.GLYPH`, `store.model()`, `store.onChange`.
- Produces: `FleetCanvas.render(container, model, opts)` (SVG) + a collapsible minimap mounted in
  `renderFleetPane`.

- [ ] **Step 1: Implement `render` in `web/fleet/canvas.js`** — replace the Task-1 stub. Build an
  inline SVG from `layout(model)`: a `<line>` per edge, and per cell a `<g>` with a small `<circle>`
  (class `fleet-cv-node kind-<kind>`) + a `<text>` glyph + a `<text>` truncated label, each `<g>`
  carrying `data-tip` = `name` (+ ` · model_ref` when present) so the existing app tooltip layer
  shows detail on hover. Horizontal overflow scrolls (the container is `overflow-x:auto`). Empty
  topology → a `.fleet-cv-empty` note (`t("fleet.canvas.empty")`).

```js
  function render(container, model, opts) {
    const lay = layout(model, opts);
    if (!lay.cells.length) {
      container.innerHTML = `<div class="fleet-cv-empty">${esc(t("fleet.canvas.empty"))}</div>`;
      return;
    }
    const edges = lay.edges.map(e =>
      `<line class="fleet-cv-edge" x1="${e.x1}" y1="${e.y1}" x2="${e.x2}" y2="${e.y2}"/>`).join("");
    const nodes = lay.cells.map(c => {
      const label = c.name.length > 14 ? c.name.slice(0, 13) + "…" : c.name;
      const tip = c.model_ref ? `${c.name} · ${c.model_ref}` : c.name;
      return `<g class="fleet-cv-cell kind-${esc(c.kind)}" data-tip="${esc(tip)}" transform="translate(${c.x},${c.y})">
        <circle class="fleet-cv-node" r="5" cx="0" cy="0"/>
        <text class="fleet-cv-glyph" x="10" y="4">${esc(GLYPH[c.kind] || "")}</text>
        <text class="fleet-cv-label" x="22" y="4">${esc(label)}</text>
      </g>`;
    }).join("");
    container.innerHTML =
      `<svg class="fleet-cv-svg" width="${lay.width}" height="${lay.height}" viewBox="0 0 ${lay.width} ${lay.height}" role="img" aria-label="${esc(t("fleet.canvas.title"))}">${edges}${nodes}</svg>`;
  }
```

- [ ] **Step 2: Mount the collapsible strip in `renderFleetPane`** ([web/settings.js](../../../web/settings.js)).
  After the `.fleet-pane` markup (tree + editor), add a minimap section and repaint it whenever the
  store changes:
  - Add to the `host.innerHTML` template, **between** `.fleet-pane` and `.fleet-actionbar`:
    ```html
    <div class="fleet-canvas-wrap">
      <button type="button" class="fleet-canvas-toggle" id="fleet-canvas-toggle" aria-expanded="false">
        <span class="fleet-canvas-chevron">▸</span> <span>${escHtml(tr("fleet.canvas.title"))}</span>
      </button>
      <div class="fleet-canvas-body" id="fleet-canvas-body" hidden></div>
    </div>
    ```
  - After grabbing `treeHost`/`editorHost`, add:
    ```js
    const canvasBody = host.querySelector("#fleet-canvas-body");
    const canvasToggle = host.querySelector("#fleet-canvas-toggle");
    const CANVAS_KEY = "agent_toolkit_fleet_canvas_open";
    let canvasOpen = localStorage.getItem(CANVAS_KEY) === "1";
    function paintCanvas() { if (canvasOpen) window.FleetCanvas.render(canvasBody, store.model()); }
    function applyCanvasState() {
      canvasBody.hidden = !canvasOpen;
      canvasToggle.setAttribute("aria-expanded", canvasOpen ? "true" : "false");
      canvasToggle.querySelector(".fleet-canvas-chevron").textContent = canvasOpen ? "▾" : "▸";
      if (canvasOpen) paintCanvas();
    }
    canvasToggle.addEventListener("click", () => {
      canvasOpen = !canvasOpen; localStorage.setItem(CANVAS_KEY, canvasOpen ? "1" : "0"); applyCanvasState();
    });
    ```
  - Fold `paintCanvas()` into the existing `store.onChange` handler (so an edit repaints the minimap
    too, when open): change `store.onChange(() => { paintTree(); paintActions(); })` to also call
    `if (canvasOpen) paintCanvas();`.
  - Call `applyCanvasState()` once at the end of `renderFleetPane` (beside `paintTree(); paintEditor(); paintActions();`).

- [ ] **Step 3: Styles** — append to [web/css/settings/fleet.css](../../../web/css/settings/fleet.css):

```css
.fleet-canvas-wrap { margin-top: 10px; border-top: 1px solid var(--border, rgba(127,127,127,.2)); }
.fleet-canvas-toggle { display: inline-flex; align-items: center; gap: 6px; background: none; border: 0;
  cursor: pointer; color: var(--muted, #888); font-size: .85em; padding: 8px 2px; }
.fleet-canvas-chevron { display: inline-block; width: 1em; }
.fleet-canvas-body { overflow-x: auto; max-height: 240px; padding: 4px 2px 10px; }
.fleet-cv-empty { color: var(--muted, #888); padding: 8px; font-size: .85em; }
.fleet-cv-edge { stroke: var(--border, rgba(127,127,127,.45)); stroke-width: 1; }
.fleet-cv-node { fill: var(--accent, #6aa0ff); }
.fleet-cv-cell.kind-router .fleet-cv-node { fill: #b07cff; }
.fleet-cv-cell.kind-leader .fleet-cv-node { fill: #ffb454; }
.fleet-cv-cell.kind-subagent .fleet-cv-node { fill: #57c7a3; }
.fleet-cv-glyph { font-size: 11px; fill: var(--text, currentColor); }
.fleet-cv-label { font-size: 11px; fill: var(--text, currentColor); }
```

- [ ] **Step 4: index.html** — add `<script src="assets/fleet/canvas.js?v=1" defer></script>`
  immediately AFTER the `assets/fleet/tree.js` script (canvas needs `FleetStore`, already earlier)
  and BEFORE `settings.js`; bump the `settings.js?v=` and `css/settings.css?v=` cache-busters by 1.

- [ ] **Step 5: i18n** — add to `web/i18n/en.json` (+ fr/es/de):
  - `fleet.canvas.title` = "Delegation map"
  - `fleet.canvas.empty` = "No squads to map yet."
  Run `make i18n`; commit the regenerated `web/i18n/locales.js`.

- [ ] **Step 6: Docs** — in `web/docs/19-agents.md`, add one line under the Fleet-preview note: the
  Fleet view now includes a collapsible read-only **delegation map** beneath the tree.

- [ ] **Step 7: Verify + smoke**
  - `node --test web/fleet/canvas.test.js web/fleet/store.test.js` → all pass; `node --check web/settings.js web/fleet/canvas.js` → clean.
  - Live smoke (scratch `OMNIS_HOME`, real config, flag on): open Fleet → the "Delegation map"
    toggle appears under the tree, collapsed; click it → an SVG renders with nodes + edges for the
    squads (router/leader/subagent colored), horizontally scrollable; hovering a node shows the
    app tooltip with name (+ model); collapse state persists across a reload; editing a node (e.g.
    changing a model_ref) with the map open repaints it; **zero console errors**; flag OFF ⇒ no
    Fleet subtab (and thus no map).

- [ ] **Step 8: Commit**

```bash
git add web/fleet/canvas.js web/settings.js web/css/settings/fleet.css web/index.html web/i18n/*.json web/docs/19-agents.md
git commit -m "feat(fleet): collapsible read-only delegation-map minimap under the tree"
```

---

## Self-Review (author checklist, run before dispatching Task 1)

- **Spec coverage:** §5 `fleetCanvas` (read-only, from the store) → Tasks 1–2; §6 "collapsible strip
  under the tree, read-only in v1" → Task 2 collapsible mount. §10 guidance (tooltips) → per-node
  `data-tip`.
- **Read-only contract:** the minimap has no click-to-edit, no store mutation — it only reads
  `store.model()` and repaints on `onChange`. Selection/editing stays in the tree/editor.
- **No second source of truth:** layout consumes `FleetStore.treeNodes` (the tree's own node set),
  so tree and map cannot diverge.
- **No placeholders:** the Task-1 file ships `layout` complete + a clearly-marked `render` stub that
  Task 2 replaces (not a silent gap).
- **Deferred (later phases):** interactive canvas (pan/zoom/click-to-select), slide-overs (Phase 4),
  DnD (Phase 5), cutover (Phase 6). v1 is a static, collapsible, read-only strip.
