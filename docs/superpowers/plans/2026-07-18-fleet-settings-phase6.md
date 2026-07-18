# Fleet Settings — Phase 6 (Cutover) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Retire the classic Agents/Squads/Remotes sub-tab editors and remove the `agent_toolkit_fleet_preview` flag so the **Fleet** view is the one and only Agents editor, then refresh i18n + docs. (Optional final task: extract the Fleet editor components into `web/fleet/editor.js`.)

**Architecture:** The `agent` settings section stops rendering a sub-tab strip and mounts the Fleet pane directly. `renderFleetPane` (the shell, tree, editor host, canvas, slide-overs, Save/Discard bar) and everything the Fleet reuses (Global/Models/Registry renderers + shared leaf widgets) are preserved verbatim. Only the *classic* agent/squad/remotes renderers — which the Fleet never calls — are deleted. No Go changes; the server config write contract is untouched.

**Tech Stack:** Vanilla ES (no bundler), `window.*` globals, `web/settings.js` (IIFE), `web/fleet/{store,tree,canvas}.js` modules, `node --test` unit tests, Playwright live smoke.

## Global Constraints

- **This is the first destructive phase.** After it, the Fleet is the *only* way to edit agents/squads — there is no classic fallback. Every task ends green (`node --check web/settings.js`, `make test-web` = the existing 57 tests) and the phase ends with a **live Playwright smoke of the full agent-edit → Save → hot-reload path** against a scratch `OMNIS_HOME` (never the real `~/.omnis`).
- **Round-trip fidelity is make-or-break and already covered by store tests** (lossless `serialize`, per-key preservation, `hidden`/`subagents`/empty-array round-trips). The 52 store + 5 canvas tests MUST stay green; do not weaken or delete them.
- **Deleting a classic renderer must leave NO live caller.** Before finishing any deletion task, `grep` the whole tree for the deleted name and confirm zero remaining references. A dangling call throws at runtime with no compile step to catch it.
- **Must-KEEP (the Fleet reuses these — deleting them breaks the Fleet):** `renderFleetPane` and all its nested helpers; `renderFleetSquadEditor`, `renderFleetAgentEditor`, `renderFleetRouterInfo`, `fleetInvalidMessage`, `saveFleet`, `fleetDefaultSquadName`; `renderAgentGlobals` (Fleet Global slide-over); `renderModelsPanel`, `renderProvidersPanel` (Fleet Models slide-over + top-level Models section); `renderRegistriesHub` and every per-kind remote renderer/card/dialog it reuses (`renderRemoteRegistriesSection`, `renderRemoteBrowseView`, `renderRemoteSkillDetailView`, `remoteAgentMetaRowsHtml`, `appAgentInstallDialog`, `buildAgentCard`/`buildSquadCard`/… inside the marketplace renderers); the shared leaf widgets `renderAgentTeamBlock`, `renderSkillBlockContent`, `renderAgentMCPBlockContent`, `renderAgentA2ABlockContent`, `subAgentDependsOn`, `populateAgentSkillBlock`/`MCPBlock`/`A2ABlock`, `modelComboField`, `prepareForSave`.
- **Preserve "Import agent from file" (user decision).** The classic Agents tab's `importAgentDialog` + `POST /agents/import` flow (`web/settings.js:3134`/`3148`/`8889`) must survive the cutover, re-homed into the Fleet `＋` create menu (Task 1). `importAgentDialog` is therefore **must-KEEP** (do NOT delete it in Task 3), and the reused `skillsPost("/agents/import", …)` wiring stays. Only the classic *button + its old click handler inside `renderAgentForm`* go away (replaced by the Fleet menu item).
- **Top-level sidebar sections are UNCHANGED.** `MENU_ITEMS` (Skills, Agents, Models, Permissions, MCP, A2A, Hooks, Commands, Automation, Registries, Appearance, Documentation) stays exactly as-is. The cutover only removes the *internal sub-tabs of the Agents section*, not any top-level section. Models and Registries remain reachable both as top-level sections and as Fleet header peers.
- **Flag removal is total.** `fleetPreviewOn()` is deleted; `localStorage["agent_toolkit_fleet_preview"]` becomes inert (present or absent, it changes nothing). The Agents section always shows the Fleet.
- **i18n:** remove only the *orphaned* `subtab.*` keys (verify each is unreferenced first). `subtab.fleet`, `subtab.models`, `subtab.globalEnv` are still used by the Fleet header peers / slide-overs — KEEP them. Run `make i18n` after any `web/i18n/*.json` edit and commit the regenerated `locales.js`.
- **Cache-busters:** bump `settings.js?v=` in `web/index.html` on every task that edits `web/settings.js`; bump `locales.js?v=` when i18n JSON changes; bump `settings.css?v=` only if a CSS file changes (this phase likely changes none). Do it in the same commit as the code change.
- **No Go changes.** `go vet ./...` must stay clean (it will — no Go is touched).
- **Extraction (Task 8) is OPTIONAL and independently revertible.** It moves the two editor components into a new module behind a deps bag. Its correctness is **not** gated by any unit test (the store tests exercise the pure store, not the editor DOM→store wiring), so it is validated by live smoke only. If lean-cutover is preferred, drop Task 8 entirely — Tasks 1–7 are a complete, shippable cutover.

---

### Task 1: Make the Agents section Fleet-only (rewire `renderAgentForm`, remove the flag, re-home agent-import)

**Files:**
- Modify: `web/settings.js` — `renderAgentForm` (~3034-3174), `AGENT_SUBTABS` (259-264), `agentSubtabs` (271-275), `fleetPreviewOn` (267-270), the Fleet `＋` create menu inside `renderFleetPane` (530-547)
- Modify: `web/i18n/{en,fr,es,de}.json` — add `fleet.create.importAgent`; regenerate `web/i18n/locales.js` via `make i18n`
- Modify: `web/index.html` — bump `settings.js?v=` and `locales.js?v=`

**Interfaces:**
- Consumes: `renderFleetPane(host, cfg, defaultSquad)` (294), `fleetDefaultSquadName()` (282), `state.parsed["agent"].value`, `loadBuiltinAgents()`, `loadParsed("models")`, `updateFooter()`; and for the re-homed import: `importAgentDialog()` (8889), `skillsPost("/agents/import", …)`, `doReload()`, `setStatus`, `store.dirty()`, `appConfirm`.
- Produces: an Agents section that mounts the Fleet pane with no sub-tab strip and no flag check, and a Fleet `＋` menu with three items (New squad / New agent / **Import agent from file**).

- [ ] **Step 1: Rewrite `renderAgentForm` to mount the Fleet pane directly.** Keep the warm-up (`loadBuiltinAgents`, `state.parsed.agent` normalization, `loadParsed("models")`) and the `reloadRebuild = null` reset. Replace the entire sub-tab strip + `if (sub === "fleet") … else …` dispatch block (3055-3172) with a single Fleet mount:

```javascript
  async function renderAgentForm() {
    reloadRebuild = null; // renderFleetPane re-sets its own in-place reload hook on mount
    await loadBuiltinAgents();
    const id = "agent";
    const d = state.parsed[id].value;
    if (!Array.isArray(d.agents)) d.agents = [];
    if (!Array.isArray(d.squads)) d.squads = [];
    if (!state.parsed.models) {
      try { await loadParsed("models"); }
      catch { /* missing models.json is fine — dropdown shows (none) */ }
    }
    bodyEl.innerHTML = `<div class="settings-form"><div id="fleet-host"></div></div>`;
    // Learn the protected default squad name from the server (renamed "default"→"system").
    renderFleetPane(bodyEl.querySelector("#fleet-host"), d, await fleetDefaultSquadName());
    updateFooter();
  }
```

- [ ] **Step 2: Delete the flag + sub-tab plumbing.** Remove `fleetPreviewOn` (267-270), `agentSubtabs` (271-275), and the `AGENT_SUBTABS` const (259-264). Grep to confirm the only readers were `renderAgentForm` (now rewritten): `grep -n "AGENT_SUBTABS\|agentSubtabs\|fleetPreviewOn\|activeAgentSubtab" web/settings.js`.
- [ ] **Step 3: Remove `state.activeAgentSubtab` reads/writes** now that there are no sub-tabs (search the grep output from Step 2). Leave `state.activeAgentIdx`/`activeSquadIdx`/`agentRemotes`/`squadRemotes` for Tasks 2-4 (they are read by the classic renderers still present until then).
- [ ] **Step 4: Re-home "Import agent from file" into the Fleet `＋` menu.** In `renderFleetPane`'s create-menu (530-547), add a third `fleetMenu` item after "New agent". It mirrors the ⟳ reload's dirty-guard (import writes server-side, then a reload rebuilds the pane from disk, discarding an unsaved draft):

```javascript
        { label: tr("fleet.create.importAgent"), action: async () => {
          if (store.dirty() && !await appConfirm(tr("fleet.shell.reloadDirty"))) return;
          const result = await importAgentDialog();
          if (!result) return;
          try {
            const res = await skillsPost("/agents/import", { content: result.content, enable: result.enable });
            const names = (res.agents || []).map(a => a.name).join(", ");
            if ((res.agents || []).some(a => a.enabled)) {
              await doReload();   // rebuilds the Fleet from disk via reloadRebuild (resyncFromServer)
            } else {
              setStatus(tr("set.agent.imported", { names }), "success");
            }
          } catch (e) {
            setStatus(tr("set.status.importFailed", { error: e.message }), "error");
          }
        } },
```

  Add the i18n key `fleet.create.importAgent` to all four `web/i18n/*.json` files — `en`: "Import agent from file…"; translate `fr`/`es`/`de` (do not translate "agent"). Run `make i18n`. (`set.agent.imported` / `set.status.importFailed` already exist — reused.)
- [ ] **Step 5: Verify.** `node --check web/settings.js` (clean); `node --check web/i18n/locales.js`; `make test-web` (57 pass). Bump `settings.js?v=46` → `47` and `locales.js?v=52` → `53` in `web/index.html`.
- [ ] **Step 6: Commit.**

```bash
git add web/settings.js web/i18n/ web/index.html
git commit -m "feat(fleet): Agents section mounts Fleet directly; drop preview flag + sub-tabs; re-home agent-import into the ＋ menu"
```

---

### Task 2: Delete the classic Squads renderers (kills the `isDefault==="default"` bug)

**Files:**
- Modify: `web/settings.js` — delete `synthesizeDefaultSquad` (~3179-3192), `renderAgentSquads` (~3198-3221), `renderSquadDetail` (~3222-3386)
- Modify: `web/index.html` — bump `settings.js?v=`

**Interfaces:**
- Consumes: nothing new. These functions were only reached from the old `renderAgentForm` "squads" branch (deleted in Task 1).
- Produces: none.

- [ ] **Step 1: Confirm they are dead.** `grep -n "synthesizeDefaultSquad\|renderAgentSquads\|renderSquadDetail\|activeSquadIdx" web/settings.js` — after Task 1 the only hits must be their own definitions and internal self-calls (e.g. `renderAgentSquads` re-rendering itself, `renderSquadDetail` called from `renderAgentSquads`). If any *other* caller exists, STOP and report — a live caller means these are not dead.
- [ ] **Step 2: Delete the three functions in full** (whole function bodies, including their nested helpers and the classic `isDefault = (sq.name||"").toLowerCase() === "default"` logic — this hardcoded-"default" bug is fixed by deletion; the Fleet path already uses `store.isDefaultSquad`).
- [ ] **Step 3: Remove now-dead `state.activeSquadIdx`** reads/writes (only the classic squad list used it).
- [ ] **Step 4: Verify no dangling references.** Re-run the Step-1 grep — expect zero hits. `node --check web/settings.js`; `make test-web` (57 pass).
- [ ] **Step 5: Commit + cache-buster bump.**

```bash
git add web/settings.js web/index.html
git commit -m "refactor(fleet): delete classic Squads editor (fixes hardcoded default-squad name)"
```

---

### Task 3: Delete the classic Agents renderers

**Files:**
- Modify: `web/settings.js` — delete `updateFleetModelLine` (~4674-4693), `renderAgentAgents` (~4694-4783), `renderAgentDetail` (~4784-5355)
- Modify: `web/index.html` — bump `settings.js?v=`

**Interfaces:**
- Consumes: nothing new (reached only from the old "agents" branch, deleted in Task 1).
- Produces: none.

- [ ] **Step 0: Do NOT delete `importAgentDialog` (8889).** After Task 1 re-homed agent-import into the Fleet `＋` menu, `importAgentDialog` and the `skillsPost("/agents/import", …)` wiring are LIVE (their caller is now the Fleet menu, not the classic tab). `grep -n "importAgentDialog\|/agents/import" web/settings.js` must show the Fleet `＋` menu as the live caller. Leave both intact.
- [ ] **Step 1: Confirm dead.** `grep -n "renderAgentAgents\|renderAgentDetail\|updateFleetModelLine\|activeAgentIdx" web/settings.js` — only their own defs + internal self-calls should remain. Any external caller ⇒ STOP and report.
- [ ] **Step 2: Confirm the shared leaf widgets survive.** `renderAgentDetail` calls `renderAgentTeamBlock`, `renderSkillBlockContent`/`populateAgentSkillBlock`, `renderAgentMCPBlockContent`, `renderAgentA2ABlockContent`, `modelComboField`. These are ALSO called by `renderFleetAgentEditor`. Grep each after planning the deletion — confirm each still has a live caller (the Fleet editor). Do NOT delete any of them.
- [ ] **Step 3: Delete the three functions in full.**
- [ ] **Step 4: Remove now-dead `state.activeAgentIdx`** reads/writes.
- [ ] **Step 5: Verify.** Re-run Step-1 grep (zero hits) + Step-2 grep (each leaf still called by the Fleet editor). `node --check web/settings.js`; `make test-web` (57 pass).
- [ ] **Step 6: Commit + cache-buster bump.**

```bash
git add web/settings.js web/index.html
git commit -m "refactor(fleet): delete classic Agents editor (list + detail form)"
```

---

### Task 4: Delete ONLY the classic Agents-section Remotes tab wrapper (`renderAgentRemotesTab`)

> **CORRECTED after code inspection.** The original plan said "remove `state.agentRemotes`/`state.squadRemotes`" and treated the remote browse/detail views as classic-only. That is WRONG and would break the top-level Registries section: `renderRegistriesHub`'s `refreshRegistriesRight` (web/settings.js:2278+, ~line 2350-2355) reads `state.agentRemotes`/`state.squadRemotes` and calls `renderAgentRemoteDetailView`/`renderAgentRemoteBrowseView`/`renderSquadRemoteBrowseView`. Those views + `doInstallAgent` + that state are **shared with the must-keep Registries hub**. Only the `renderAgentRemotesTab` *wrapper* itself is dead. This is a surgical single-function deletion.

**Files:**
- Modify: `web/settings.js` — delete **only** `renderAgentRemotesTab` (7134 → just before `renderAgentRemoteBrowseView` at 7319; its nested `renderKindNav` dies with it)
- Modify: `web/index.html` — bump `settings.js?v=`

**Interfaces:**
- Consumes: nothing new (`renderAgentRemotesTab` is unreachable since Task 1 removed the "remotes" sub-tab dispatch from `renderAgentForm`).
- Produces: none.

- [ ] **Step 1: Confirm `renderAgentRemotesTab` is dead.** `grep -n "renderAgentRemotesTab" web/settings.js` — only its own def (~7134) should remain (Task 1 removed the caller). Any other caller ⇒ STOP/report.
- [ ] **Step 2: Fix the deletion boundary.** Read from `async function renderAgentRemotesTab(d, host)` (7134) to its matching closing brace, which lands **just before** `async function renderAgentRemoteBrowseView(host)` (7319). Delete exactly that wrapper (incl. its nested `renderKindNav`). Do NOT cross into 7319+.
- [ ] **Step 3: DO NOT delete — prove these stay live (reused by `renderRegistriesHub`, NOT by the dead wrapper).** For EACH, `grep -n` and confirm a live caller inside `renderRegistriesHub`/`refreshRegistriesRight` (~2337-2356), distinct from `renderAgentRemotesTab`: `state.agentRemotes`, `state.squadRemotes`, `renderAgentRemoteBrowseView` (7319), `renderAgentRemoteDetailView` (7460), `doInstallAgent` (7575), `renderSquadRemoteBrowseView` (7713), plus the marketplace leaf renderers (`renderRemoteRegistriesSection`, `renderRemoteBrowseView`, `remoteAgentMetaRowsHtml`, `appAgentInstallDialog`, `importAgentDialog`, `buildAgentCard`/`buildSquadCard`). **Keep every one of them.** If any turns out reachable *only* through `renderAgentRemotesTab`, STOP and report it (do not delete it unilaterally — surface it, since the plan's premise would be wrong again).
- [ ] **Step 4: Delete the wrapper only** (Step 2 boundary). Leave `state.agentRemotes`/`state.squadRemotes` and every Step-3 name intact.
- [ ] **Step 5: Verify.** `grep -n "renderAgentRemotesTab" web/settings.js` → zero hits. `grep -n "agentRemotes\|squadRemotes\|renderAgentRemoteBrowseView\|renderAgentRemoteDetailView\|renderSquadRemoteBrowseView\|doInstallAgent" web/settings.js` → still present, with `renderRegistriesHub`/`refreshRegistriesRight` as a live caller. `node --check web/settings.js`; `make test-web` (57 pass).
- [ ] **Step 6: Commit + cache-buster bump.**

```bash
git add web/settings.js web/index.html
git commit -m "refactor(fleet): delete dead classic Agents-section Remotes tab wrapper (shared browse/detail views + state retained for Registries hub)"
```

---

### Task 5: i18n cleanup — remove orphaned sub-tab keys, regenerate `locales.js`

**Files:**
- Modify: `web/i18n/en.json`, `web/i18n/fr.json`, `web/i18n/es.json`, `web/i18n/de.json` — remove only the orphaned `subtab.*` keys
- Regenerate: `web/i18n/locales.js` (via `make i18n`)
- Modify: `web/index.html` — bump `locales.js?v=`

**Interfaces:**
- Consumes: the grep evidence of which keys are now unreferenced.
- Produces: catalogues with no orphaned keys; `en` remains the base + fallback.

- [ ] **Step 1: Identify orphans — grep-driven, remove ONLY zero-reference keys.** Three families are affected (for each candidate, `grep -rn "<key>" web/*.js web/fleet/*.js web/*.html` and remove ONLY when zero hits):
  - **`subtab.*`:** `grep -rn "subtab\." web/*.js`. Expected orphans after Tasks 1-4: `subtab.agents`, `subtab.remotes` (classic Agents/Remotes labels). Expected KEEP: `subtab.fleet`, `subtab.models`, `subtab.globalEnv` (Fleet peers/slide-overs) and any `subtab.*` still used by other sections' sub-tabs (Models `providers`/`models`; Skills `installed`/`remotes`; MCP `servers`/`remotes`; A2A `agents`/`remotes`; Commands `user`/`remotes`; Registries `squads`). **`subtab.remotes`/`subtab.squads`/`subtab.agents` are shared across several sections — only remove one when the grep proves it unused anywhere.**
  - **`set.fleet.*`:** the classic Agents/Squads list chrome keys are orphaned once the classic renderers are gone. `grep -rn "set\.fleet\." web/*.js web/fleet/*.js` and remove each with zero references. Known orphans found during review: `set.fleet.activeFleet`, `set.fleet.addAgent`, `set.fleet.importAgent`, `set.fleet.addSquad`, `set.fleet.squadsTitle`, `set.fleet.noAgents`, `set.fleet.agentsLabel`, `set.fleet.builtinAgentsLabel` — plus any sibling the grep proves unused. **`set.fleet.importAgent` (classic tooltip) is superseded by `fleet.create.importAgent` (added in Task 1) — remove the old key, keep the new one.** Do NOT remove any `fleet.*` key the Fleet uses.
  - **`set.agent.*` / `set.squad.*`:** the classic agent/squad detail forms owned some of these. Known orphans found during review: `set.agent.moveUp`, `set.agent.moveDown`. `grep -rn "set\.agent\.\|set\.squad\." web/*.js web/fleet/*.js` and remove each with zero references. **Many `set.agent.*`/`set.squad.*` keys are STILL used by the surviving Fleet editors (`renderFleetAgentEditor`/`renderFleetSquadEditor`) and the shared leaf widgets — only remove a key the grep proves unused anywhere.**
- [ ] **Step 2: Fix the `fleet.create.importAgent` translations (Task 1 follow-up).** Task 1 left "agent" untranslated per an over-strict instruction; the i18n glossary only protects product nouns (Omnis/Squad/MCP/…), and "agent" is a common noun sibling keys translate. Correct the non-`en` values in `web/i18n/{fr,es,de}.json` (leave `en` as "Import agent from file…"): `fr` → "Importer un agent depuis un fichier…"; `es` → "Importar un agente desde un archivo…"; `de` → "Agent aus Datei importieren…".
- [ ] **Step 3: Remove the confirmed-orphan keys** from all four `web/i18n/*.json` files (keep key parity across locales — remove the same keys everywhere).
- [ ] **Step 4: Regenerate + verify.** `make i18n` (regenerates `web/i18n/locales.js`; heed any missing-key warnings — they indicate over-removal). `node --check web/i18n/locales.js`; `make test-web` (57 pass).
- [ ] **Step 5: Commit + cache-buster bump.** `locales.js` is already at `?v=53` (bumped in Task 1) — bump `?v=53` → `54` in `web/index.html`.

```bash
git add web/i18n/ web/index.html
git commit -m "i18n(fleet): drop orphaned classic keys after cutover; fix importAgent translations; regenerate locales"
```

---

### Task 6: Docs — retire the preview-flag instructions

**Files:**
- Modify: `web/docs/19-agents.md` — remove the `agent_toolkit_fleet_preview` opt-in paragraph; describe the Fleet as the agent/squad editor
- Modify: `CLAUDE.md` — update the Fleet paragraph (drop "behind `localStorage["agent_toolkit_fleet_preview"]`" / "preview" wording; note the cutover shipped)

**Interfaces:** docs only.

- [ ] **Step 1: `web/docs/19-agents.md`** — replace the "Enable it by setting `localStorage["agent_toolkit_fleet_preview"] = …`" paragraph (~line 17) with a description of the Fleet as the standard Agents editor (tree → editor → Save → hot-reload; Models/Registry/Global header peers; `＋` create; context menus; drag-and-drop). English only (per `web/docs` policy).
- [ ] **Step 2: `CLAUDE.md`** — in the Fleet bullet under "Agent topology", change "behind `localStorage["agent_toolkit_fleet_preview"] = "1"` (reload to pick it up)" and the trailing "Still behind `agent_toolkit_fleet_preview`, zero Go." to reflect that Phase 6 removed the flag and the Fleet is now the sole Agents editor. Keep the round-trip/store description accurate.
- [ ] **Step 3: Verify** no other doc references the flag: `grep -rn "agent_toolkit_fleet_preview" web/docs CLAUDE.md` → zero hits.
- [ ] **Step 4: Commit.**

```bash
git add web/docs/19-agents.md CLAUDE.md
git commit -m "docs(fleet): Fleet is now the sole Agents editor; remove preview-flag instructions"
```

---

### Task 7: Full live smoke of the cutover (verification gate)

**Files:** none (verification only; any fix found here is a follow-up commit on the relevant task's files).

**Interfaces:** validates the whole cutover against a running branch server.

- [ ] **Step 1: Start a scratch branch server.** `OMNIS_WEB_DIR=$(pwd)/web OMNIS_HOME=$(mktemp -d) OMNIS_SYSTEM_CONFIG_DIR=/etc/omnis` with no `OMNIS_SERVER_TOKEN`, on a free port (pick via `ss -ltnp`; never `pkill -f omnis-server`). Confirm Save writes only to the throwaway `OMNIS_HOME`.
- [ ] **Step 2: Drive Playwright** (via `browser_evaluate` JS clicks to bypass overlays; remove the "What's new in Omnis" `.ui-modal-overlay` first). Verify, with the flag **unset** (prove it is inert):
  - Settings → **Agents** shows the **Fleet** shell (header peers Fleet · Models · Registry · Global, `＋`, `⟳`) with **no sub-tab strip** and no flag needed.
  - The tree renders router → squads → leaders → members → Unused; legend + context menus (kebab/right-click) work; `＋` create works; light DnD reorder + cross-squad drop work.
  - **Agent edit → Save → hot-reload round-trips** (change a model_ref; Save; confirm the PUT fired, the throwaway delta wrote, the reload succeeded, the value persisted). This is the load-bearing path — there is no classic fallback.
  - **Squad edit → Save** round-trips (leader change / member toggle / hidden flag).
  - Slide-overs open and save: **Models** (edit + save, embedder-restart banner preserved), **Registry** (dirty-guard blocks when dirty, opens when clean, resyncs on close), **Global** (edits ride the Fleet Save bar), model-ref `↗` opens Models.
  - The store **validation gate** still blocks a config-bricking save (e.g. disable a squad's leader) with the invalid-config message and issues no PUT.
  - Top-level **Models** and **Registries** sidebar sections still render (unchanged).
  - **Zero console errors/warnings** across the run.
- [ ] **Step 3: Record the smoke outcome** in the phase ledger. Any failure → fix on the owning task's files, re-commit, re-smoke.

---

### Task 8 (OPTIONAL — highest risk, no unit-test gate, independently revertible): Extract Fleet editors into `web/fleet/editor.js`

> Skip this task for a lean cutover. Tasks 1-7 already deliver a complete, shippable Fleet-only Agents surface. This task only relocates code for module hygiene (spec §5); it changes no behaviour and its correctness is validated by **live smoke only** (the store tests exercise the pure store, not the editor DOM→store wiring).

**Files:**
- Create: `web/fleet/editor.js` — new `window.FleetEditor` module
- Modify: `web/settings.js` — remove the moved functions; build a deps bag; call `window.FleetEditor.*`
- Modify: `web/index.html` — add `<script src="assets/fleet/editor.js?v=1" defer>` after `fleet/canvas.js` and before `settings.js`; bump `settings.js?v=`

**Interfaces:**
- Move (verbatim): `renderFleetRouterInfo` (759-771), `renderFleetSquadEditor` (772-919), `renderFleetAgentEditor` (920-1475), and each editor's nested `genField`.
- **Keep in `settings.js`** (shell-level): `renderFleetPane`, `saveFleet`, `fleetInvalidMessage`, `fleetDefaultSquadName`.
- Deps bag passed into the module (the settings.js-scope identifiers the editors reference): `{ tr, escHtml, setStatus, showBanner, renderBody, doReload, appConfirm, deepClone, prepareForSave, markFormDirty, renderAgentTeamBlock, populateAgentSkillBlock, populateAgentMCPBlock, populateAgentA2ABlock, isLeaderAgent, isBuiltinAgent, errText, authHeaders, BASE_PATH, state }`.

- [ ] **Step 1: Define the module surface.** Create `web/fleet/editor.js` exposing `window.FleetEditor = { init(deps), renderSquadEditor(host, store, name, onRename), renderAgentEditor(host, store, name, onRename, openModels), renderRouterInfo(host, store, name) }`. `init(deps)` stores the deps bag in a module-local `D`; the render functions use `D.tr`, `D.escHtml`, … instead of the settings.js closures.
- [ ] **Step 2: Move the three functions verbatim** into the module, mechanically rewriting each external identifier from the deps list to `D.<name>` (e.g. `tr(...)` → `D.tr(...)`, `renderAgentTeamBlock(...)` → `D.renderAgentTeamBlock(...)`, `state.` → `D.state.`). Store methods (`store.*`), DOM APIs, and `Number`/`parseInt`/etc. are untouched. Keep each nested `genField` inside its parent.
- [ ] **Step 3: Wire settings.js to the module.** In the settings IIFE, after all deps exist, call `window.FleetEditor.init({ tr, escHtml, setStatus, showBanner, renderBody, doReload, appConfirm, deepClone, prepareForSave, markFormDirty, renderAgentTeamBlock, populateAgentSkillBlock, populateAgentMCPBlock, populateAgentA2ABlock, isLeaderAgent, isBuiltinAgent, errText, authHeaders, BASE_PATH, state })` once. Replace the calls in `renderFleetPane`/`paintEditor` (`renderFleetSquadEditor(...)` → `window.FleetEditor.renderSquadEditor(...)`, etc.). Delete the three moved functions from settings.js.
- [ ] **Step 4: Load order.** Add the script tag in `web/index.html` after `fleet/canvas.js` and before `settings.js` (the module attaches to `window` synchronously; `defer` preserves document order). Bump `settings.js?v=`.
- [ ] **Step 5: Verify.** `node --check web/fleet/editor.js`; `node --check web/settings.js`; `grep -n "renderFleetSquadEditor\|renderFleetAgentEditor\|renderFleetRouterInfo" web/settings.js` → only the `window.FleetEditor.*` call sites remain (no stale defs). `make test-web` (57 pass — unchanged, they test the store).
- [ ] **Step 6: LIVE SMOKE (mandatory — the only gate for this task).** Re-run the Task 7 smoke focused on the editor save paths: select an agent, edit model_ref + toggle a tool + add a Team member + toggle resumable_sessions/max_instances → Save → reload → assert every edit round-trips; select a squad, change leader + toggle a member + hidden → Save → reload → assert. Any missing/mis-threaded dep surfaces here as a thrown error or a lost edit. Zero console errors.
- [ ] **Step 7: Commit.**

```bash
git add web/fleet/editor.js web/settings.js web/index.html
git commit -m "refactor(fleet): extract Fleet agent/squad editors into web/fleet/editor.js"
```

---

## Testing

- **Per task:** `node --check` on every edited JS file + `make test-web` (57 tests) must pass.
- **Deletion safety:** each deletion task greps for the deleted symbol and proves zero live callers, and (Tasks 3-4) proves every shared leaf/marketplace renderer still has a live caller through the Fleet editor / Registries hub.
- **Round-trip fidelity:** the existing store golden tests (lossless `serialize`, per-key preservation, `hidden`/`subagents`/empty-array) are the fidelity gate and must stay green — the cutover changes no store code.
- **Live smoke (Task 7, and Task 8 if run):** against a scratch `OMNIS_HOME` branch server — Agents = Fleet with no flag; agent + squad edit → Save → hot-reload round-trips; slide-overs save; validation gate blocks a bricking save; top-level Models/Registries intact; zero console errors. This is the only end-to-end gate for the editor save path, which has no classic fallback after cutover.

## Self-Review (author checklist)

- **Spec coverage (§12.6):** remove old sub-tab renderers (Tasks 1-4) ✅; i18n en/fr/es/de (Task 5) ✅; docs `web/docs/19-agents.md` (Task 6) ✅. Extraction (added scope from the user's chosen option) = Task 8, marked optional.
- **Must-keep list** is enumerated in Global Constraints and re-checked in Tasks 3-4 — the Fleet's reused renderers and shared leaf widgets are never deleted.
- **No hardcoded "default":** the buggy classic squad code (`(sq.name||"").toLowerCase() === "default"`) is deleted with `renderSquadDetail`/`renderAgentSquads` (Task 2); the surviving Fleet path uses `store.isDefaultSquad`.
- **Flag inert, not merely default-on:** `fleetPreviewOn` is deleted (Task 1), so the flag key has no effect either way.
- **No Go changes;** server config contract untouched. Top-level Models/Registries sections untouched.
