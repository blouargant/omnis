# Fleet Settings — Phase 4 (Slide-over shell) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Fleet-first **header shell** (Fleet · Models · Registry · Global peers + `⟳` reload) and three **slide-overs** (Models & Providers, Registries, Global config) that open over the Fleet pane, plus the agent editor's model-ref `↗` jump — so a user editing the fleet rarely has to leave it.

**Architecture:** Everything lands in the existing `renderFleetPane` (in `web/settings.js`) behind the existing `agent_toolkit_fleet_preview` flag. A single reusable slide-over host (backdrop + titled panel + close) mounts the **existing** renderers, per the design's "reuse as-is" non-goal. The **Global** slide-over edits the Fleet store's own draft (agent.json top-level keys the store already round-trips) and is persisted by the Fleet Save bar — one draft, one save. **Models** edits `models.json` (a separate file) with its own Save button reusing a newly-extracted `saveParsedFile(id)` helper. **Registries** mounts the existing hub; because installs can change agent.json server-side, it is dirty-guarded on open and re-syncs the store on close. Zero Go changes.

**Tech Stack:** Vanilla JS (no bundler; `window.*` globals), the existing `web/fleet/store.js` (untouched here), `web/css/settings/fleet.css`, `web/i18n/*.json` (+ generated `locales.js` via `make i18n`).

## Global Constraints

- **Behind the flag only.** Every change is inside `renderFleetPane` / its Fleet-only helpers / the Fleet CSS block. With `agent_toolkit_fleet_preview` unset, the classic sub-tabs (Agents/Squads/Remotes/Global) and every classic renderer must be **byte-identical** to before. `renderAgentForm` only mounts `renderFleetPane` when `sub === "fleet"`.
- **Preserve the server write contract.** No change to `PUT /api/config/parsed/:name`, `GET /api/squads`, `POST /api/config/reload`, or `prepareForSave`. Round-trip every key: agent.json top-level global keys, `models.json` providers/models, `subagents`, `hidden`, squad `members`, `max_instances`, `resumable_sessions`, `max_tool_calls`.
- **One draft per file.** agent.json is owned by the Fleet `store` (its `draft()`/`serialize()` is the whole agent value incl. top-level global keys). The Global slide-over MUST mutate `store.draft()` and route change → `store.touch()`, persisted by the Fleet Save bar — never a second agent.json writer. models.json is a separate file with its own Save.
- **Embedder-restart banner preserved.** A models.json save that changes the embedder identity must still raise the restart banner (`showBanner(true)` via `embedderChangedOnSave`), exactly as `saveActive` does today.
- **Reuse, don't reimplement.** The three slide-overs wrap the existing renderers: `renderAgentGlobals` (Global), `renderProvidersPanel`/`renderModelsPanel` (Models), `renderRegistriesHub` (Registries). Do not fork their internals.
- **Reuse i18n keys where they exist:** peer labels — `subtab.models`, `settings.menu.registries`, `subtab.globalEnv`, `subtab.fleet`; `common.close`; `fleet.editor.save`; `set.confirm.discard`. Only add the four new `fleet.shell.*` keys this plan specifies, in **all four** locales (en/fr/es/de), then run `make i18n`.
- **English-only** for docs (`web/docs/19-agents.md`) and CLAUDE.md.
- **Scope note (not a gap):** the header's `＋` "create squad/agent" affordance (design §5/§8) is a **composition interaction** and is deliberately deferred to **Phase 5** (interactions/DnD, design §9). Phase 4 builds the header shell + `⟳` + the three reference slide-overs + the model-ref `↗` only. Do not add `＋` here.

## Testing note (read before Task 1)

Phase 4 is DOM/settings.js glue over already-tested pure modules; the repo has **no jsdom harness**, so (as in Phases 2–3) the per-task **automated gate** each implementer runs is:

```bash
make test-web            # 35 pure-module tests still green (regression guard on store/tree/canvas)
node --check web/settings.js
node --check web/i18n/locales.js   # only after a task runs `make i18n`
```

Functional verification (the tree → slide-over → save → reload loop) is a **controller-run Playwright smoke** performed *between* tasks and comprehensively at the end — NOT a subagent step (browser tooling is the controller's). Each task below ends with the exact automated gate; the controller owns the browser smoke.

## File Structure

- `web/settings.js` — all logic: `renderFleetPane` shell + slide-over host; three slide-over builders; `saveParsedFile(id)` (extracted from `saveActive`); `renderAgentGlobals` host/onChange parameterization; agent-editor model-ref `↗`. One file, localized edits.
- `web/css/settings/fleet.css` — append a "Phase 4 — header shell + slide-over" block.
- `web/i18n/{en,fr,es,de}.json` + `web/i18n/locales.js` (generated) — four new `fleet.shell.*` keys.
- `web/index.html` — bump `settings.js?v=42`→`43` and `settings.css?v=11`→`12` (Task 7).
- `web/docs/19-agents.md`, `CLAUDE.md` — document the shell/slide-overs (Task 7).

---

### Task 1: Slide-over host + header shell (chrome, `⟳` reload, stub builders)

**Files:**
- Modify: `web/settings.js` — `renderFleetPane` (≈ lines 294–369)
- Modify: `web/css/settings/fleet.css` (append)
- Modify: `web/i18n/{en,fr,es,de}.json` (+ regenerate `locales.js`)

**Interfaces:**
- Produces (closures inside `renderFleetPane`, consumed by Tasks 2/4/5/6):
  - `openFleetSlideover(title: string, buildBody: (bodyEl)=>void, onClose?: ()=>void)`
  - `closeFleetSlideover()`
  - `openPeer(id: "models"|"registry"|"global")`
  - `let store` (was `const`; Task 6 reassigns it)
  - stub builders `buildModelsSlideover(body)`, `buildRegistrySlideover(body)`, `buildGlobalSlideover(body)` — replaced by Tasks 4/6/2
  - a `PEER` map `{ models|registry|global: { title, build, onClose } }`
  - the existing `paintTree/paintEditor/paintActions/paintCanvas/select` closures (unchanged)

- [ ] **Step 1: Add the four new i18n keys (en), then all three other locales**

Add to `web/i18n/en.json` (place near the other `fleet.shell`/`fleet.editor` keys; JSON key order is irrelevant):

```json
"fleet.shell.reloadTip": "Reload configuration",
"fleet.shell.reloadDirty": "You have unsaved Fleet changes. Reload anyway? They will stay unsaved.",
"fleet.shell.editModelTip": "Edit models & providers",
"fleet.shell.registryDirty": "Save or discard your Fleet changes before browsing registries."
```

Add to `web/i18n/fr.json`:

```json
"fleet.shell.reloadTip": "Recharger la configuration",
"fleet.shell.reloadDirty": "Vous avez des modifications de la flotte non enregistrées. Recharger quand même ? Elles resteront non enregistrées.",
"fleet.shell.editModelTip": "Modifier les modèles et fournisseurs",
"fleet.shell.registryDirty": "Enregistrez ou annulez vos modifications de la flotte avant de parcourir les registres."
```

Add to `web/i18n/es.json`:

```json
"fleet.shell.reloadTip": "Recargar la configuración",
"fleet.shell.reloadDirty": "Tienes cambios de la flota sin guardar. ¿Recargar de todos modos? Seguirán sin guardarse.",
"fleet.shell.editModelTip": "Editar modelos y proveedores",
"fleet.shell.registryDirty": "Guarda o descarta tus cambios de la flota antes de explorar los registros."
```

Add to `web/i18n/de.json`:

```json
"fleet.shell.reloadTip": "Konfiguration neu laden",
"fleet.shell.reloadDirty": "Sie haben nicht gespeicherte Flotten-Änderungen. Trotzdem neu laden? Sie bleiben ungespeichert.",
"fleet.shell.editModelTip": "Modelle & Anbieter bearbeiten",
"fleet.shell.registryDirty": "Speichern oder verwerfen Sie Ihre Flotten-Änderungen, bevor Sie Registries durchsuchen."
```

- [ ] **Step 2: Regenerate the i18n bundle**

Run: `make i18n`
Expected: `web/i18n/locales.js` regenerated with no error (a warning about other missing keys, if any, is pre-existing and fine). `node --check web/i18n/locales.js` passes.

- [ ] **Step 3: Rewrite `renderFleetPane`'s template + add the shell/slide-over closures**

In `web/settings.js`, `renderFleetPane` currently opens with `const store = window.FleetStore.create(...)` and sets `host.innerHTML` to a template containing `.fleet-pane`, `.fleet-canvas-wrap`, and `.fleet-actionbar` (≈ lines 294–312). Apply these exact changes:

1. Change `const store =` to `let store =` (Task 6 reassigns it).
2. Replace the `host.innerHTML = \`...\`;` template with the version below (adds `.fleet-shell` wrapper, the `.fleet-shell-header`, and the `.fleet-slideover` host; the `.fleet-pane`/canvas/actionbar blocks are unchanged inside it):

```js
    host.innerHTML = `
      <div class="fleet-shell">
        <div class="fleet-shell-header">
          <div class="fleet-shell-peers">
            <span class="fleet-peer fleet-peer-home is-current">${escHtml(tr("subtab.fleet"))}</span>
            <button type="button" class="fleet-peer" data-peer="models">${escHtml(tr("subtab.models"))}</button>
            <button type="button" class="fleet-peer" data-peer="registry">${escHtml(tr("settings.menu.registries"))}</button>
            <button type="button" class="fleet-peer" data-peer="global">${escHtml(tr("subtab.globalEnv"))}</button>
          </div>
          <button type="button" class="fleet-shell-reload" id="fleet-reload" data-tip="${escHtml(tr("fleet.shell.reloadTip"))}" aria-label="${escHtml(tr("fleet.shell.reloadTip"))}">⟳</button>
        </div>
        <div class="fleet-pane">
          <div class="fleet-pane-tree" id="fleet-tree"></div>
          <div class="fleet-pane-editor" id="fleet-editor"></div>
        </div>
        <div class="fleet-canvas-wrap">
          <button type="button" class="fleet-canvas-toggle" id="fleet-canvas-toggle" aria-expanded="false">
            <span class="fleet-canvas-chevron">▸</span> <span>${escHtml(tr("fleet.canvas.title"))}</span>
          </button>
          <div class="fleet-canvas-body" id="fleet-canvas-body" hidden></div>
        </div>
        <div class="fleet-actionbar" id="fleet-actions" hidden>
          <span class="fleet-dirty-note">${escHtml(tr("fleet.editor.unsaved"))}</span>
          <button type="button" class="fleet-discard" id="fleet-discard">${escHtml(tr("fleet.editor.discard"))}</button>
          <button type="button" class="fleet-save" id="fleet-save">${escHtml(tr("fleet.editor.save"))}</button>
        </div>
        <div class="fleet-slideover" id="fleet-slideover" hidden>
          <div class="fleet-slideover-backdrop" id="fleet-slideover-backdrop"></div>
          <div class="fleet-slideover-panel" role="dialog" aria-modal="true">
            <div class="fleet-slideover-head">
              <span class="fleet-slideover-title" id="fleet-slideover-title"></span>
              <button type="button" class="fleet-slideover-close" id="fleet-slideover-close" aria-label="${escHtml(tr("common.close"))}">✕</button>
            </div>
            <div class="fleet-slideover-body" id="fleet-slideover-body"></div>
          </div>
        </div>
      </div>`;
```

3. The existing `const treeHost = host.querySelector("#fleet-tree")` … block still resolves (`#fleet-tree`, `#fleet-editor`, `#fleet-actions`, `#fleet-canvas-body`, `#fleet-canvas-toggle` all still exist inside `.fleet-shell`). Leave those lines and all of `paintCanvas/applyCanvasState/paintTree/paintActions/onEditorRename/paintEditor/select/store.onChange/discard/save/paintTree()...applyCanvasState()` **unchanged**.

4. Immediately **after** the `const canvasToggle = host.querySelector("#fleet-canvas-toggle");` line (before `const CANVAS_KEY`), add the slide-over + shell wiring:

```js
    // ── Header shell + slide-over host (Phase 4) ──
    const slideover  = host.querySelector("#fleet-slideover");
    const slideBody  = host.querySelector("#fleet-slideover-body");
    const slideTitle = host.querySelector("#fleet-slideover-title");
    let slideOnClose = null;
    function slideEsc(e) { if (e.key === "Escape") closeFleetSlideover(); }
    function openFleetSlideover(title, buildBody, onClose) {
      slideOnClose = onClose || null;
      slideTitle.textContent = title;
      slideBody.innerHTML = "";
      slideover.hidden = false;
      document.addEventListener("keydown", slideEsc);
      buildBody(slideBody);
    }
    function closeFleetSlideover() {
      if (slideover.hidden) return;
      slideover.hidden = true;
      slideBody.innerHTML = "";
      document.removeEventListener("keydown", slideEsc);
      const cb = slideOnClose; slideOnClose = null;
      if (typeof cb === "function") cb();
    }
    host.querySelector("#fleet-slideover-close").addEventListener("click", closeFleetSlideover);
    host.querySelector("#fleet-slideover-backdrop").addEventListener("click", closeFleetSlideover);

    // Slide-over content builders. Stubs here; replaced by later tasks:
    //   buildGlobalSlideover   → Task 2   buildModelsSlideover → Task 4
    //   buildRegistrySlideover → Task 6
    function buildGlobalSlideover(body)   { body.innerHTML = ""; }
    function buildModelsSlideover(body)   { body.innerHTML = ""; }
    function buildRegistrySlideover(body) { body.innerHTML = ""; }

    const PEER = {
      models:   { title: tr("subtab.models"),            build: buildModelsSlideover,   onClose: null },
      registry: { title: tr("settings.menu.registries"), build: buildRegistrySlideover, onClose: null },
      global:   { title: tr("subtab.globalEnv"),         build: buildGlobalSlideover,   onClose: null },
    };
    function openPeer(id) {
      const p = PEER[id];
      if (!p) return;
      openFleetSlideover(p.title, p.build, p.onClose);
    }
    host.querySelectorAll(".fleet-peer[data-peer]").forEach(btn => {
      btn.addEventListener("click", () => openPeer(btn.dataset.peer));
    });

    // ⟳ reload. If the Fleet draft is dirty, reloading applies only what is on
    // disk (the unsaved draft stays in the store), so confirm first.
    host.querySelector("#fleet-reload").addEventListener("click", async () => {
      if (store.dirty() && !await appConfirm(tr("fleet.shell.reloadDirty"))) return;
      await doReload();
    });
```

- [ ] **Step 4: Append the Phase-4 CSS block to `web/css/settings/fleet.css`**

```css

/* Fleet header shell + slide-over (Phase 4). */
.fleet-shell { position: relative; }
.fleet-shell-header { display: flex; align-items: center; gap: 8px; margin-bottom: 10px;
  padding-bottom: 8px; border-bottom: 1px solid var(--border, rgba(127,127,127,.2)); }
.fleet-shell-peers { display: flex; align-items: center; gap: 2px; flex: 1 1 auto; min-width: 0; flex-wrap: wrap; }
.fleet-peer { background: none; border: 0; cursor: pointer; color: var(--muted, #888);
  font-size: 13px; padding: 4px 10px; border-radius: 6px; }
.fleet-peer:hover { background: var(--hover-bg, rgba(127,127,127,.10)); color: var(--text, inherit); }
.fleet-peer-home { cursor: default; }
.fleet-peer.is-current { color: var(--text, inherit); font-weight: 600;
  background: var(--accent-soft, rgba(80,140,255,.16)); }
.fleet-shell-reload { flex: 0 0 auto; background: none; border: 1px solid var(--border, rgba(127,127,127,.3));
  cursor: pointer; color: var(--muted, #888); border-radius: 6px; width: 30px; height: 30px;
  font-size: 15px; line-height: 1; }
.fleet-shell-reload:hover { color: var(--text, inherit); background: var(--hover-bg, rgba(127,127,127,.10)); }

.fleet-slideover[hidden] { display: none; }
.fleet-slideover { position: absolute; inset: 0; z-index: 30; display: flex; justify-content: flex-end; }
.fleet-slideover-backdrop { position: absolute; inset: 0; background: rgba(0,0,0,.28); }
.fleet-slideover-panel { position: relative; width: min(720px, 92%); max-width: 92%;
  background: var(--bg, #fff); border-left: 1px solid var(--border, rgba(127,127,127,.3));
  box-shadow: -8px 0 24px rgba(0,0,0,.18); display: flex; flex-direction: column;
  height: 100%; overflow: hidden; }
.fleet-slideover-head { display: flex; align-items: center; gap: 10px; padding: 12px 14px;
  border-bottom: 1px solid var(--border, rgba(127,127,127,.2)); flex: 0 0 auto; }
.fleet-slideover-title { font-weight: 600; font-size: 14px; flex: 1 1 auto; }
.fleet-slideover-close { background: none; border: 0; cursor: pointer; color: var(--muted, #888);
  font-size: 16px; line-height: 1; padding: 4px 8px; border-radius: 6px; }
.fleet-slideover-close:hover { color: var(--text, inherit); background: var(--hover-bg, rgba(127,127,127,.10)); }
.fleet-slideover-body { flex: 1 1 auto; min-height: 0; overflow: auto; padding: 14px; }
.fleet-slideover-foot { display: flex; align-items: center; gap: 10px; justify-content: flex-end;
  padding: 10px 14px; border-top: 1px solid var(--border, rgba(127,127,127,.2)); flex: 0 0 auto; }
.fleet-slideover-foot .fleet-dirty-note { margin-right: auto; }
```

- [ ] **Step 5: Automated gate**

Run:
```bash
make test-web
node --check web/settings.js
node --check web/i18n/locales.js
```
Expected: `make test-web` → all pure-module tests pass (35). Both `node --check` → no output (valid). No functional browser check in this task (controller runs the smoke).

- [ ] **Step 6: Commit**

```bash
git add web/settings.js web/css/settings/fleet.css web/i18n/en.json web/i18n/fr.json web/i18n/es.json web/i18n/de.json web/i18n/locales.js
git commit -m "feat(fleet): header shell + reusable slide-over host + ⟳ reload"
```

---

### Task 2: Global config slide-over (store-draft-backed)

**Files:**
- Modify: `web/settings.js` — `renderAgentGlobals` (≈ line 2964) and `buildGlobalSlideover` (Task 1 stub)

**Interfaces:**
- Consumes: `store.draft()` (the whole agent.json value, incl. top-level global keys), `store.touch()`.
- Produces: nothing new; the Global slide-over persists via the existing Fleet Save bar (`saveFleet` → `store.serialize()`).

**Context:** `renderAgentGlobals(d)` today mutates `d` in place and hardcodes `const el = bodyEl.querySelector("#agent-globals-host")` and `const onChange = () => markFormDirty("agent")`. Parameterize both so the Fleet slide-over can pass the store's draft as `d`, its own host element, and `store.touch` as onChange — while the classic globals sub-tab keeps its exact current behavior via defaults.

- [ ] **Step 1: Parameterize `renderAgentGlobals`**

Change the signature and its first two lines from:

```js
  function renderAgentGlobals(d) {
    const el = bodyEl.querySelector("#agent-globals-host");
    const onChange = () => markFormDirty("agent");
```

to:

```js
  function renderAgentGlobals(d, opts) {
    const el = (opts && opts.host) || bodyEl.querySelector("#agent-globals-host");
    const onChange = (opts && opts.onChange) || (() => markFormDirty("agent"));
```

Everything else in `renderAgentGlobals` is unchanged (it already uses the local `el` and `onChange`). The classic call site in `renderAgentForm` (`renderAgentGlobals(d);`) still works — `opts` is undefined ⇒ identical behavior.

- [ ] **Step 2: Wire `buildGlobalSlideover` to the store draft**

Replace the Task-1 stub `function buildGlobalSlideover(body) { body.innerHTML = ""; }` with:

```js
    // Global config edits agent.json top-level keys, which the Fleet store's
    // draft already owns and round-trips. Render the existing globals editor
    // against store.draft() and route its onChange to store.touch(), so the
    // Fleet Save bar (saveFleet → store.serialize()) persists it — one draft,
    // one save. No separate save button here.
    function buildGlobalSlideover(body) {
      body.classList.add("env-sections");
      renderAgentGlobals(store.draft(), { host: body, onChange: () => store.touch() });
    }
```

- [ ] **Step 3: Automated gate**

Run:
```bash
make test-web
node --check web/settings.js
```
Expected: pass / valid.

- [ ] **Step 4: Commit**

```bash
git add web/settings.js
git commit -m "feat(fleet): Global config slide-over backed by the store draft"
```

**Controller smoke (between tasks):** flag on → Fleet tab → open **Global** peer → edit a key (e.g. add a number to a `turn_budget` field) → the Fleet Save/Discard bar appears (store dirty) → close slide-over → **Save** → persists + reload; re-open Global shows the saved value. Flag off → classic **Global configuration** sub-tab edits + footer Save still work unchanged.

---

### Task 3: Extract `saveParsedFile(id)` from `saveActive`

**Files:**
- Modify: `web/settings.js` — `saveActive` (≈ line 10164)

**Interfaces:**
- Produces: `async function saveParsedFile(id): Promise<{restartRequired: boolean}>` — PUTs `/api/config/parsed/${id}` with `prepareForSave(id, value)`, updates the parsed cache (`p.data`/`p.mtime`/`p.dirty=false`), invalidates `state.raw[id]`, and reports whether the embedder identity changed. Used by `saveActive` (this task) and the Models slide-over (Task 4).

**Context:** `saveActive`'s `else` (parsed) branch already contains exactly this logic keyed on `state.activeFile`. Extract it verbatim into a helper parameterized by `id`, so both the classic footer Save and the Models slide-over share one correct implementation (DRY; the embedder-fingerprint + banner logic must not be duplicated).

- [ ] **Step 1: Add the helper**

Add immediately **above** `async function saveActive()`:

```js
  // saveParsedFile persists one parsed config file (the "form" view path) and
  // reports whether the save changed the embedder identity (models.json only).
  // Extracted from saveActive so the Fleet Models slide-over can save
  // models.json without going through state.activeFile / the classic footer.
  async function saveParsedFile(id) {
    const p = state.parsed[id];
    const prevData = p.data;
    const r = await fetch(BASE_PATH + `/api/config/parsed/${id}`, {
      method: "PUT",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ data: prepareForSave(id, p.value), mtime: p.mtime }),
    });
    if (!r.ok) throw new Error(await errText(r));
    const j = await r.json();
    const restartRequired = embedderChangedOnSave(id, prevData, p.value);
    p.data = deepClone(p.value);
    p.mtime = j.mtime;
    p.dirty = false;
    delete state.raw[id]; // invalidate raw cache so the raw view re-fetches
    return { restartRequired };
  }
```

- [ ] **Step 2: Route `saveActive`'s parsed branch through it**

In `saveActive`, replace the `else { ... }` (parsed) branch body — from `const p = state.parsed[id];` through `delete state.raw[id];` (the lines that currently do the parsed PUT + cache update, ≈ 10184–10198) — with:

```js
      } else {
        restartRequired = (await saveParsedFile(id)).restartRequired;
      }
```

Leave the raw branch, the `setStatus(...)`, `showBanner(restartRequired)`, and `renderBody()` lines unchanged. (`restartRequired` is still declared `let restartRequired = false;` at the top of `saveActive`.)

- [ ] **Step 3: Automated gate**

Run:
```bash
make test-web
node --check web/settings.js
```
Expected: pass / valid.

- [ ] **Step 4: Commit**

```bash
git add web/settings.js
git commit -m "refactor(settings): extract saveParsedFile from saveActive (DRY)"
```

**Controller smoke:** flag off → classic **Models** sub-tab: edit a model price → footer **Save** → still saves + banner; classic **Permissions** (another parsed file) Save still works. No behavior change.

---

### Task 4: Models & Providers slide-over (own Save via `saveParsedFile`)

**Files:**
- Modify: `web/settings.js` — `buildModelsSlideover` (Task 1 stub)

**Interfaces:**
- Consumes: `renderProvidersPanel(d, host)` / `renderModelsPanel(d, host)` (both already host-parameterized), `MODELS_SUBTABS`, `state.parsed.models`, `loadParsed("models")`, `saveParsedFile("models")` (Task 3), `showBanner`, `doReload`.
- Produces: a self-contained Models editor in the slide-over with its own Save button.

**Context:** models.json is a **separate** file the Fleet store does not own, so the Models slide-over needs its own Save (not the Fleet bar). `renderModelsForm` (the classic wrapper) hardcodes `bodyEl` + the classic footer, so do NOT reuse it; instead mount the two host-parameterized panels directly under a small local sub-tab switcher + a Save footer. `renderProvidersPanel`/`renderModelsPanel` mark dirty via `markFormDirty("models")` (sets `state.parsed.models.dirty`), which the footer's Save-button enabled-state reads.

- [ ] **Step 1: Replace `buildModelsSlideover`**

Replace the Task-1 stub with:

```js
    // Models & Providers slide-over. models.json is a separate file the Fleet
    // store does not own, so this has its OWN Save (via saveParsedFile), which
    // preserves the embedder-restart banner. Reuses the host-parameterized
    // renderProvidersPanel / renderModelsPanel (NOT renderModelsForm, which is
    // bound to bodyEl + the classic footer).
    let modelsSlideSub = state.activeModelsSubtab || "models";
    async function buildModelsSlideover(body) {
      if (!state.parsed.models) {
        try { await loadParsed("models"); }
        catch { body.innerHTML = `<p class="settings-error">${escHtml(tr("set.none"))}</p>`; return; }
      }
      const d = state.parsed.models.value;
      if (!d.providers || typeof d.providers !== "object") d.providers = {};
      if (!d.models || typeof d.models !== "object") d.models = {};

      body.innerHTML = `
        <div class="settings-form">
          <div class="settings-subtabs" role="tablist">
            ${MODELS_SUBTABS.map(t => `<button type="button" data-subtab="${t.id}" class="${modelsSlideSub === t.id ? "active" : ""}">${escHtml(t.label)}</button>`).join("")}
          </div>
          <div class="settings-subtab-body" id="fleet-models-panel"></div>
        </div>
        <div class="fleet-slideover-foot">
          <span class="fleet-dirty-note" id="fleet-models-dirty" hidden>${escHtml(tr("fleet.editor.unsaved"))}</span>
          <button type="button" class="fleet-save" id="fleet-models-save">${escHtml(tr("fleet.editor.save"))}</button>
        </div>`;

      const panel = body.querySelector("#fleet-models-panel");
      const dirtyNote = body.querySelector("#fleet-models-dirty");
      function refreshDirty() { dirtyNote.hidden = !(state.parsed.models && state.parsed.models.dirty); }
      function paintPanel() {
        if (modelsSlideSub === "providers") renderProvidersPanel(d, panel);
        else renderModelsPanel(d, panel);
        refreshDirty();
      }
      body.querySelectorAll(".settings-subtabs button").forEach(b => {
        b.addEventListener("click", () => {
          if (modelsSlideSub === b.dataset.subtab) return;
          modelsSlideSub = b.dataset.subtab;
          state.activeModelsSubtab = modelsSlideSub;
          body.querySelectorAll(".settings-subtabs button").forEach(x => x.classList.toggle("active", x.dataset.subtab === modelsSlideSub));
          paintPanel();
        });
      });
      body.querySelector("#fleet-models-save").addEventListener("click", async () => {
        setStatus(tr("set.status.saving"));
        try {
          const { restartRequired } = await saveParsedFile("models");
          setStatus(restartRequired
            ? "Saved. Restart the server to apply the embedder change."
            : tr("set.status.savedReload"), "success");
          showBanner(restartRequired);
          if (!restartRequired) await doReload();
          refreshDirty();
        } catch (e) {
          setStatus(tr("set.status.saveFailed", { error: e.message }), "error");
        }
      });

      // The panels re-render themselves after add/edit and call
      // markFormDirty("models"); keep the footer's dirty note in step by
      // re-reading the flag on pointer interactions within the panel.
      panel.addEventListener("click", () => setTimeout(refreshDirty, 0));
      panel.addEventListener("input", () => setTimeout(refreshDirty, 0));

      paintPanel();
    }
```

- [ ] **Step 2: Automated gate**

Run:
```bash
make test-web
node --check web/settings.js
```
Expected: pass / valid.

- [ ] **Step 3: Commit**

```bash
git add web/settings.js
git commit -m "feat(fleet): Models & Providers slide-over with own Save + reload"
```

**Controller smoke:** Fleet → **Models** peer → the Providers/Models sub-tabs render inside the slide-over; edit a model price → the "unsaved" note shows → **Save** → persists + reload; making an embedder-identity change (e.g. switch the embed model) → **restart** banner (not a plain reload). Flag off → classic Models sub-tab unchanged.

---

### Task 5: Agent-editor model-ref `↗` + repaint editor on Models close

**Files:**
- Modify: `web/settings.js` — `renderFleetAgentEditor` (model-ref field, ≈ lines 630–654; signature ≈ 536) and `renderFleetPane`'s `paintEditor` (≈ line 351) + `PEER.models.onClose`

**Interfaces:**
- Consumes: `openPeer("models")` (Task 1/4).
- Produces: `renderFleetAgentEditor(host, store, name, onRename, openModels?)` gains a 5th param `openModels` (a `()=>void`).

**Context:** §7 of the design gives the agent's Model Reference field a `↗` that opens the Model editor as a slide-over. Thread the fleet pane's `openModels` into the agent editor and add the `↗`. After the Models slide-over closes, repaint the open agent editor so a newly-added model appears in its `model_ref` dropdown.

- [ ] **Step 1: Add `openModels` param to the agent editor signature**

Change `function renderFleetAgentEditor(host, store, name, onRename) {` to:

```js
  function renderFleetAgentEditor(host, store, name, onRename, openModels) {
```

- [ ] **Step 2: Add the `↗` to the Model Reference field**

In the Model Reference `genField` (the block that builds `sel` and appends it via `f.appendChild(sel)`), immediately **after** `f.appendChild(sel);` and before the `if (a.recommended_model)` block, insert:

```js
      if (typeof openModels === "function") {
        const jump = document.createElement("button");
        jump.type = "button";
        jump.className = "fleet-modelref-jump";
        jump.textContent = "↗";
        jump.setAttribute("data-tip", tr("fleet.shell.editModelTip"));
        jump.setAttribute("aria-label", tr("fleet.shell.editModelTip"));
        jump.addEventListener("click", () => openModels());
        f.appendChild(jump);
      }
```

- [ ] **Step 3: Pass `openModels` from `paintEditor` and repaint on Models close**

In `renderFleetPane`, change the agent branch of `paintEditor`:

```js
      if (selected.type === "agent")  return renderFleetAgentEditor(editorHost, store, selected.name, onEditorRename);
```

to:

```js
      if (selected.type === "agent")  return renderFleetAgentEditor(editorHost, store, selected.name, onEditorRename, () => openPeer("models"));
```

And set the Models peer's onClose to repaint the editor (so a model added in the slide-over shows up in the dropdown). Change `PEER.models`'s `onClose: null` to:

```js
      models:   { title: tr("subtab.models"),            build: buildModelsSlideover,   onClose: () => paintEditor() },
```

(`paintEditor` is declared later in the function; the arrow captures it lazily, so the forward reference is fine — it is only invoked on slide-over close.)

- [ ] **Step 4: Add the `↗` button CSS**

Append to `web/css/settings/fleet.css`:

```css
.fleet-modelref-jump { margin-left: 6px; background: none; border: 1px solid var(--border, rgba(127,127,127,.3));
  border-radius: 6px; cursor: pointer; color: var(--muted, #888); width: 26px; height: 26px;
  font-size: 13px; line-height: 1; vertical-align: middle; }
.fleet-modelref-jump:hover { color: var(--text, inherit); background: var(--hover-bg, rgba(127,127,127,.10)); }
```

- [ ] **Step 5: Automated gate**

Run:
```bash
make test-web
node --check web/settings.js
```
Expected: pass / valid.

- [ ] **Step 6: Commit**

```bash
git add web/settings.js web/css/settings/fleet.css
git commit -m "feat(fleet): model-ref ↗ jump to the Models slide-over"
```

**Controller smoke:** select an agent in the tree → the Model Reference row shows a `↗` → click it → the Models slide-over opens → add a new model → close → the agent editor's `model_ref` dropdown now lists the new model.

---

### Task 6: Registries slide-over + store re-sync on close

**Files:**
- Modify: `web/settings.js` — `buildRegistrySlideover` (Task 1 stub), `openPeer` (add the dirty guard), and `renderFleetPane` (add `resyncFromServer`)

**Interfaces:**
- Consumes: `renderRegistriesHub(host)` (already host-parameterized, default `bodyEl`), `loadParsed`, `deepClone`, `window.FleetStore.create`.
- Produces: `resyncFromServer()` — re-fetches agent.json, rebuilds the store, and repaints, preserving the current `selected` node.

**Context:** Registry installs can change **agent.json server-side** (enable/append an agent, install a squad), which would desync the Fleet store and risk a stale-base overwrite on the next Fleet Save. Contract: (a) require a **clean** store to open Registries (block + hint when dirty); (b) after the slide-over closes, **re-sync** the store from fresh agent.json (safe — the store was clean, so nothing is lost), preserving the selected node.

- [ ] **Step 1: Add `resyncFromServer` to `renderFleetPane`**

Add just above the `paintTree(); paintEditor(); paintActions(); applyCanvasState();` final line (so `store`, `paintTree`, `paintEditor`, `paintActions`, `paintCanvas`, `canvasOpen`, `defaultSquad`, `select` are all in scope):

```js
    // Rebuild the store from a fresh agent.json (e.g. after a registry install
    // changed it server-side). Safe only when the current store is clean — the
    // caller guarantees that. Preserves the selected node by name.
    async function resyncFromServer() {
      try {
        delete state.parsed["agent"];
        await loadParsed("agent");
      } catch { return; } // leave the current store in place on failure
      const fresh = state.parsed["agent"].value;
      store = window.FleetStore.create(fresh, { defaultSquad });
      store.onChange(() => { paintTree(); paintActions(); if (canvasOpen) paintCanvas(); });
      // Drop a selection that no longer resolves in the fresh config.
      if (selected && selected.type === "agent" && !store.agent(selected.name)) selected = null;
      if (selected && selected.type === "squad" && !store.squad(selected.name)) selected = null;
      paintTree(); paintEditor(); paintActions(); if (canvasOpen) paintCanvas();
    }
```

- [ ] **Step 2: Dirty-guard the Registries peer in `openPeer`**

In `openPeer`, immediately after `if (!p) return;` add:

```js
      if (id === "registry" && store.dirty()) { setStatus(tr("fleet.shell.registryDirty"), "error"); return; }
```

- [ ] **Step 3: Wire the Registries slide-over content + re-sync on close**

Replace the Task-1 stub `buildRegistrySlideover` with:

```js
    // Registries hub, mounted in the slide-over. Installs can change agent.json
    // server-side, so opening is dirty-guarded (openPeer) and closing re-syncs
    // the store from disk (resyncFromServer). renderRegistriesHub is async; it
    // paints into `body` itself.
    function buildRegistrySlideover(body) {
      renderRegistriesHub(body);
    }
```

And set the Registries peer's `onClose` to re-sync. Change `PEER.registry`'s `onClose: null` to:

```js
      registry: { title: tr("settings.menu.registries"), build: buildRegistrySlideover, onClose: () => resyncFromServer() },
```

- [ ] **Step 4: Automated gate**

Run:
```bash
make test-web
node --check web/settings.js
```
Expected: pass / valid.

- [ ] **Step 5: Commit**

```bash
git add web/settings.js
git commit -m "feat(fleet): Registries slide-over with dirty-guard + store re-sync"
```

**Controller smoke:** with unsaved Fleet edits, clicking **Registries** shows the "save or discard first" status and does NOT open (guard). With a clean store, **Registries** opens the hub inside the slide-over; installing/enabling an agent then closing re-syncs — the newly-enabled agent appears in the Fleet tree without a manual refresh.

---

### Task 7: Docs, CLAUDE.md, cache-busters, final verification

**Files:**
- Modify: `web/index.html` (cache-busters)
- Modify: `web/docs/19-agents.md`
- Modify: `CLAUDE.md`

**Interfaces:** none (documentation + versioning).

- [ ] **Step 1: Bump the cache-busters in `web/index.html`**

- `assets/css/settings.css?v=11` → `?v=12`
- `assets/settings.js?v=42` → `?v=43`

- [ ] **Step 2: Document the shell + slide-overs**

In `web/docs/19-agents.md`, under the Fleet section, add a short paragraph (English) describing: the header peers (Fleet · Models · Registry · Global) and `⟳`; that Models/Registry/Global open as slide-overs over the fleet so you rarely leave it; that Global edits are saved by the Fleet Save bar, Models has its own Save, and Registries re-syncs the tree after an install; and the agent editor's model-ref `↗` jump. Keep it consistent with the existing doc's tone; do not invent behavior beyond this plan.

- [ ] **Step 3: Update CLAUDE.md**

In the **Fleet (preview, Web UI)** paragraph (Agent-topology section), add one sentence recording Phase 4: the Fleet pane now has a header shell (Fleet/Models/Registry/Global peers + `⟳` reload) and slide-overs that wrap the existing Models/Registries/Global renderers over the fleet — Global edits ride the Fleet store draft (saved by the Fleet Save bar), Models saves models.json itself (via the extracted `saveParsedFile`, embedder-restart banner preserved), Registries is dirty-guarded and re-syncs the store on close, and the agent editor's model-ref `↗` opens the Models slide-over. Note it stays behind `agent_toolkit_fleet_preview` and adds zero Go.

- [ ] **Step 4: Final automated gate**

Run:
```bash
make test-web
node --check web/settings.js
node --check web/i18n/locales.js
go vet ./... 2>&1 | tail -5   # sanity: no Go touched, should be clean
```
Expected: web tests pass; both `node --check` valid; `go vet` clean.

- [ ] **Step 5: Commit**

```bash
git add web/index.html web/docs/19-agents.md CLAUDE.md
git commit -m "docs(fleet): document Phase 4 shell + slide-overs; bump cache-busters"
```

---

## Post-implementation (controller)

After Task 7, the controller runs a comprehensive Playwright smoke against a branch server (`OMNIS_WEB_DIR=$(pwd)/web`, scratch `OMNIS_HOME`, no token) covering: flag-on shell renders; each peer opens/closes (✕/backdrop/Escape); Global edit → Fleet Save; Models edit → slide-over Save (+ embedder restart banner); `↗` jump; Registries dirty-guard + re-sync; `⟳` reload; flag-off classic sub-tabs byte-identical; zero console errors. Then the final whole-branch review (most-capable model) and finishing-a-development-branch.
