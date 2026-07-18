# Fleet Settings — Phase 5 (Interactions) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Fleet tree directly composable — create squads/agents, and add/move/reorder/duplicate/enable/remove members and nested sub-agents — through node context menus, a header `＋` create control, and light drag-and-drop, all mutating the existing `FleetStore` draft and saving through the unchanged server contract.

**Architecture:** Phase 5 is the **interaction layer** on top of the Phase 1–4 Fleet surface. Pure topology mutations land in `web/fleet/store.js` (unit-tested, DOM-free); `web/fleet/tree.js` gains menu + drag affordances and emits new intents; `web/settings.js` (`renderFleetPane`) wires those intents to store mutations, a shared `fleetMenu` dropdown, and pickers. Every drag action has a menu equivalent (touch/accessibility fallback, per the design spec). One correctness fix is folded in: the **registry-install eject** — installing from the Registries slide-over currently tears down the whole settings body via `doReload → renderBody`, ejecting the user from the Fleet; a `reloadRebuild` hook makes the Fleet pane repaint in place instead.

**Tech Stack:** Vanilla JS (no bundler; `window.*` globals), `node --test` for the pure store/tree unit tests (`make test-web`), the existing `PUT /api/config/parsed/agent` save contract (`saveFleet`), i18n JSON catalogues + `make i18n`.

## Global Constraints

- **Feature-flag isolation:** all Phase-5 behaviour lives behind `localStorage["agent_toolkit_fleet_preview"] === "1"`, inside `renderFleetPane` / `web/fleet/*`. Flag-off must be **byte-identical** to before — the classic Agents/Squads/Remotes/Models/Global sub-tabs are untouched.
- **Round-trip fidelity is make-or-break.** Every mutation goes through the `FleetStore` draft; Save is the existing `saveFleet` (`PUT /api/config/parsed/agent` of `store.serialize()`). Never write a bespoke PUT. Preserve every whitelisted key: `subagents`, `hidden`, squad `members`, `max_instances`, `resumable_sessions`, `max_tool_calls`, `enabled`.
- **Never inject `agents`/`squads` keys** into a config that omits them — except a *create* mutation, which legitimately initialises the array it appends to. Match the existing store discipline (`removeSquad`/`removeAgent` return early when the array is absent).
- **Sub-agent cycle prevention:** any "add to team" path must reuse `wouldCreateCycle` (mirrors the server's `validateSubAgentGraph`). Never wire `self→self`, `→curator`, or a back-edge.
- **Squad rules:** the default squad (name from `store.defaultSquadName()`, server-owned, currently `"system"`) is never removed/renamed and always has a leader; a leaderless squad has **exactly one** member; the **router squad is auto-managed** — never a create target, drop target, or mutation target (read-only `renderFleetRouterInfo`).
- **`dirty()` normalization stays intact:** the M7 fix (`normalizeForCompare`, absent-list-key == empty-array) must keep working — new create/duplicate mutations that add non-empty data legitimately read dirty; a mutation that changes nothing must not.
- **i18n:** every new user-facing string is an `en.json` key + fr/es/de translations, regenerated with `make i18n`. Product nouns stay English per the glossary (Squad, Fleet, agent names).
- **Cache-busters:** any changed `web/*.js`/`web/css/*` bumps its `?v=` in `web/index.html`; `index.html` is served from an in-memory startup snapshot, so a smoke needs a **server restart** to pick up an index.html change (`/assets/*` is disk-fresh).

---

## File Structure

- `web/fleet/store.js` — **modify.** Add pure mutations: `addSquad`, `addAgent`, `duplicateAgent`, `duplicateSquad`, `setEnabled`, `addMember`, `addToTeam`, `reorderMember`, plus a `uniqueName` helper; enrich `treeNodes` with `squadName`/`parentAgent`. No new exports (mutations hang off the `create()` store; `treeNodes` shape is additive).
- `web/fleet/store.test.js` — **modify.** New unit tests for every mutation + the `treeNodes` enrichment.
- `web/fleet/tree.js` — **modify.** Per-row `⋯` kebab + `contextmenu` → `opts.onMenu(ref, ev)`; draggable member/unused rows + squad drop zones → `opts.onReorder`, `opts.onDropMember`. All new behaviour behind the existing `render(container, model, opts)` call — additive opts, so a caller that omits them is unchanged.
- `web/settings.js` — **modify (`renderFleetPane` region + `doReload`).** `fleetMenu` shared dropdown helper; header `＋` create menu; per-node menu builders (agent + squad) with candidate-picker submenus; DnD wiring; the `reloadRebuild` hook for the eject-fix.
- `web/css/settings/fleet.css` — **modify.** `.fleet-ctx-menu`/`.fleet-ctx-item`/`.fleet-ctx-sep`, the row `.fleet-kebab`, `.fleet-add-btn`, and drag affordances (`.fleet-row.dragging`, `.fleet-row.drop-target`, `.fleet-row.drop-before`/`.drop-after`).
- `web/i18n/{en,fr,es,de}.json` + `web/i18n/locales.js` — **modify.** New `fleet.menu.*` / `fleet.create.*` keys; regenerate `locales.js` via `make i18n`.
- `web/index.html` — **modify.** Bump `?v=` for `settings.js`, `fleet/store.js`, `fleet/tree.js`, `settings.css`, `i18n/locales.js`.
- `web/docs/19-agents.md` + `CLAUDE.md` — **modify.** Document the Phase-5 interactions + the eject-fix.

## Testing note (carried from Phase 4)

There is **no jsdom harness** in this repo. The per-task automated gate is:
- `make test-web` (runs `node --test web/fleet/*.test.js`) — the real gate for Tasks 1–3 (pure logic) and a regression guard thereafter.
- `node --check web/settings.js` and `node --check web/fleet/tree.js` — syntax gate for the DOM tasks.

Functional verification of the DOM tasks (menus, pickers, DnD, eject-fix) is a **controller-run Playwright smoke** at the end of the phase (against a branch server started with `OMNIS_WEB_DIR=$(pwd)/web`, a **scratch `OMNIS_HOME`** so Save writes a throwaway delta and never the user's `~/.omnis`, and no `OMNIS_SERVER_TOKEN`). Do not add a DOM test framework in this plan.

---

## Task 1: Store — creation & duplication mutations

**Files:**
- Modify: `web/fleet/store.js` (inside `create()`, alongside the existing `store.setLeader`/`removeSquad` block, ~line 230–290; `uniqueName` as a factory-scoped helper near `clone`, ~line 156)
- Test: `web/fleet/store.test.js`

**Interfaces:**
- Consumes: existing `lc`, `clone`, factory-scoped `draft`, `store.touch()`, `store.agent(name)`, `store.squad(name)`.
- Produces: `store.addSquad() → newName`, `store.addAgent() → newName`, `store.duplicateAgent(name) → newName|null`, `store.duplicateSquad(name) → newName|null`. A `uniqueName(existingNames[], base) → string`.

- [ ] **Step 1: Write the failing tests**

Append to `web/fleet/store.test.js`:

```js
test("addSquad appends a uniquely-named squad with a default leader", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  const n1 = s.addSquad();
  assert.strictEqual(n1, "new-squad");
  const n2 = s.addSquad();
  assert.strictEqual(n2, "new-squad-2"); // unique
  const sq = s.squad("new-squad");
  assert.strictEqual(sq.leader, "coder"); // first leader-eligible agent
  assert.deepStrictEqual(sq.members, []);
  assert.ok(s.dirty());
});

test("addSquad initialises an absent squads array without touching a clean config", () => {
  const s = FleetStore.create({ agents: [{ name: "leader", leader: true }] }, { defaultSquad: "system" });
  assert.ok(!s.dirty());
  s.addSquad();
  assert.ok(Array.isArray(s.draft().squads) && s.draft().squads.length === 1);
});

test("addAgent appends a uniquely-named enabled agent", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  const name = s.addAgent();
  assert.strictEqual(name, "new-agent");
  const a = s.agent("new-agent");
  assert.strictEqual(a.enabled, true);
  assert.deepStrictEqual(a.tools, []);
});

test("duplicateAgent clones fields, uniquifies the name, drops builtin, inserts after source", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  const name = s.duplicateAgent("coder");
  assert.strictEqual(name, "coder-copy");
  const copy = s.agent("coder-copy");
  assert.deepStrictEqual(copy.subagents, ["code_scout", "reviewer"]); // deep clone
  assert.strictEqual(copy.builtin, false);
  const names = s.draft().agents.map(a => a.name);
  assert.strictEqual(names[names.indexOf("coder") + 1], "coder-copy"); // inserted right after
});

test("duplicateSquad clones members, uniquifies, forces hidden:false, inserts after source", () => {
  const cfg = JSON.parse(JSON.stringify(SAMPLE));
  cfg.squads.push({ name: "Secret", leader: "coder", members: ["helper"], hidden: true });
  const s = FleetStore.create(cfg, { defaultSquad: "system" });
  const name = s.duplicateSquad("Secret");
  assert.strictEqual(name, "Secret-copy");
  const copy = s.squad("Secret-copy");
  assert.deepStrictEqual(copy.members, ["helper"]);
  assert.strictEqual(copy.hidden, false);
});

test("duplicateAgent returns null for an unknown source", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  assert.strictEqual(s.duplicateAgent("nope"), null);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test web/fleet/store.test.js`
Expected: FAIL — `s.addSquad is not a function`, etc.

- [ ] **Step 3: Implement the mutations**

Add a factory-scoped helper near `clone` (module-level, above `create`, alongside `deepEqual`):

```js
function uniqueName(existing, base) {
  const taken = new Set((existing || []).map(lc));
  if (!taken.has(lc(base))) return base;
  for (let i = 2; ; i++) { const cand = `${base}-${i}`; if (!taken.has(lc(cand))) return cand; }
}
```

Inside `create()`, after `store.teamCandidates = …` (before `return store;`):

```js
store.addSquad = () => {
  if (!Array.isArray(draft.squads)) draft.squads = [];
  const isLeaderAgent = a => !!a.leader || lc(a && a.name) === "leader";
  const leader = ((draft.agents || []).find(isLeaderAgent) || (draft.agents || [])[0] || { name: "leader" }).name;
  const name = uniqueName(draft.squads.map(s => s.name || ""), "new-squad");
  draft.squads.push({ name, description: "", leader, members: [] });
  store.touch();
  return name;
};
store.addAgent = () => {
  if (!Array.isArray(draft.agents)) draft.agents = [];
  const name = uniqueName(draft.agents.map(a => a.name || ""), "new-agent");
  draft.agents.push({ name, enabled: true, tools: [] });
  store.touch();
  return name;
};
store.duplicateAgent = (srcName) => {
  const src = store.agent(srcName); if (!src) return null;
  if (!Array.isArray(draft.agents)) draft.agents = [];
  const copy = clone(src);
  copy.name = uniqueName(draft.agents.map(a => a.name || ""), `${src.name}-copy`);
  copy.builtin = false; // a duplicate is always a user agent, never a shipped built-in
  const idx = draft.agents.findIndex(a => lc(a && a.name) === lc(srcName));
  draft.agents.splice(idx + 1, 0, copy);
  store.touch();
  return copy.name;
};
store.duplicateSquad = (srcName) => {
  const src = store.squad(srcName); if (!src) return null;
  if (!Array.isArray(draft.squads)) draft.squads = [];
  const copy = clone(src);
  copy.name = uniqueName(draft.squads.map(s => s.name || ""), `${src.name}-copy`);
  copy.hidden = false; // a user copy is meant to be used, never a machine-only hidden squad
  const idx = draft.squads.findIndex(s => lc(s && s.name) === lc(srcName));
  draft.squads.splice(idx + 1, 0, copy);
  store.touch();
  return copy.name;
};
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `node --test web/fleet/store.test.js`
Expected: PASS (all new + existing tests).

- [ ] **Step 5: Commit**

```bash
git add web/fleet/store.js web/fleet/store.test.js
git commit -m "feat(fleet): store create/duplicate mutations (Phase 5)"
```

---

## Task 2: Store — composition mutations

**Files:**
- Modify: `web/fleet/store.js` (inside `create()`, after the Task-1 block)
- Test: `web/fleet/store.test.js`

**Interfaces:**
- Consumes: `store.agent`, `store.squad`, `store.model()`, factory-scoped `leaderlessSquad`, module-level `wouldCreateCycle`, `lc`.
- Produces: `store.setEnabled(name, bool)`, `store.addMember(squad, agent)`, `store.addToTeam(agent, sub)`, `store.reorderMember(squad, fromIdx, toIdx)`. (These complement the existing `toggleMember`/`setLeader`/`setSubagents`.)

- [ ] **Step 1: Write the failing tests**

```js
test("setEnabled sets the enabled flag", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s.setEnabled("helper", false);
  assert.strictEqual(s.agent("helper").enabled, false);
});

test("addMember appends when absent, is idempotent, and never adds the leader", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s.addMember("Coding", "reviewer");
  assert.deepStrictEqual(s.squad("Coding").members, ["summariser", "reviewer"]);
  s.addMember("Coding", "reviewer"); // idempotent
  assert.deepStrictEqual(s.squad("Coding").members, ["summariser", "reviewer"]);
  s.addMember("Coding", "coder"); // coder leads Coding → refused
  assert.deepStrictEqual(s.squad("Coding").members, ["summariser", "reviewer"]);
});

test("addMember replaces the sole member of a leaderless squad", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s.addMember("Helper", "summariser");
  assert.deepStrictEqual(s.squad("Helper").members, ["summariser"]); // exactly one
});

test("addToTeam appends a sub-agent, is idempotent, and refuses a cycle / self / curator", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s.addToTeam("coder", "helper");
  assert.deepStrictEqual(s.agent("coder").subagents, ["code_scout", "reviewer", "helper"]);
  s.addToTeam("coder", "helper"); // idempotent
  assert.deepStrictEqual(s.agent("coder").subagents, ["code_scout", "reviewer", "helper"]);
  s.addToTeam("coder", "coder"); // self → refused
  s.addToTeam("code_scout", "coder"); // code_scout is coder's sub → back-edge cycle → refused
  assert.ok(!(s.agent("code_scout").subagents || []).includes("coder"));
});

test("reorderMember moves within bounds and no-ops out of bounds", () => {
  const cfg = JSON.parse(JSON.stringify(SAMPLE));
  cfg.squads.find(sq => sq.name === "Coding").members = ["a", "b", "c"];
  const s = FleetStore.create(cfg, { defaultSquad: "system" });
  s.reorderMember("Coding", 0, 2);
  assert.deepStrictEqual(s.squad("Coding").members, ["b", "c", "a"]);
  s.reorderMember("Coding", 5, 0); // out of bounds → no-op
  assert.deepStrictEqual(s.squad("Coding").members, ["b", "c", "a"]);
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test web/fleet/store.test.js`
Expected: FAIL — `s.setEnabled is not a function`, etc.

- [ ] **Step 3: Implement the mutations**

Inside `create()`, after the Task-1 block:

```js
store.setEnabled = (agentName, v) => {
  const a = store.agent(agentName); if (!a) return;
  a.enabled = !!v; store.touch();
};
store.addMember = (squadName, agentName) => {
  const sq = store.squad(squadName); if (!sq) return;
  if (!Array.isArray(sq.members)) sq.members = [];
  if (lc(agentName) === lc(sq.leader || "")) return;      // a squad never lists its own leader
  if (leaderlessSquad(sq)) sq.members = [agentName];       // leaderless = exactly one (replace)
  else if (!sq.members.some(m => lc(m) === lc(agentName))) sq.members.push(agentName);
  store.touch();
};
store.addToTeam = (agentName, subName) => {
  const a = store.agent(agentName); if (!a) return;
  if (lc(subName) === lc(agentName) || lc(subName) === "curator") return;
  if (wouldCreateCycle(store.model(), agentName, subName)) return; // validateSubAgentGraph mirror
  const cur = Array.isArray(a.subagents) ? a.subagents.slice() : [];
  if (cur.some(n => lc(n) === lc(subName))) return;        // already on the team
  cur.push(subName);
  a.subagents = cur;
  store.touch();
};
store.reorderMember = (squadName, from, to) => {
  const sq = store.squad(squadName); if (!sq || !Array.isArray(sq.members)) return;
  const n = sq.members.length;
  if (from < 0 || from >= n || to < 0 || to >= n || from === to) return;
  const [x] = sq.members.splice(from, 1);
  sq.members.splice(to, 0, x);
  store.touch();
};
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `node --test web/fleet/store.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/fleet/store.js web/fleet/store.test.js
git commit -m "feat(fleet): store composition mutations — enable/addMember/addToTeam/reorder (Phase 5)"
```

---

## Task 3: Store — `treeNodes` context enrichment

**Files:**
- Modify: `web/fleet/store.js` (`pushSubtree` ~line 70, `treeNodes` ~line 82)
- Test: `web/fleet/store.test.js`

**Interfaces:**
- Produces: every `leader`/`member`/`sole` tree node carries `squadName` (the squad it appears under); every `subagent` node carries `parentAgent` (the agent whose `subagents` list it came from) **and** `squadName` (the branch's squad). Additive — existing node fields unchanged. Menus (Tasks 6–8) and DnD (Task 8) need this to know which squad a node belongs to.

- [ ] **Step 1: Write the failing tests**

```js
test("treeNodes tags member/leader/sole nodes with their squadName", () => {
  const m = FleetStore.build(JSON.parse(JSON.stringify(SAMPLE)));
  const nodes = FleetStore.treeNodes(m);
  const leader = nodes.find(n => n.kind === "leader" && n.name === "coder"); // first appears under Coding
  assert.strictEqual(leader.squadName, "Coding");
  const member = nodes.find(n => n.kind === "member" && n.name === "summariser");
  assert.strictEqual(member.squadName, "Coding");
  const sole = nodes.find(n => n.kind === "sole" && n.name === "helper");
  assert.strictEqual(sole.squadName, "Helper");
});

test("treeNodes tags subagent nodes with parentAgent and squadName", () => {
  const m = FleetStore.build(JSON.parse(JSON.stringify(SAMPLE)));
  const nodes = FleetStore.treeNodes(m);
  const scout = nodes.find(n => n.kind === "subagent" && n.name === "code_scout");
  assert.strictEqual(scout.parentAgent, "coder");
  assert.strictEqual(scout.squadName, "Coding");
});
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `node --test web/fleet/store.test.js`
Expected: FAIL — `squadName` / `parentAgent` are `undefined`.

- [ ] **Step 3: Implement the enrichment**

Change `pushSubtree` to thread `squadName`:

```js
function pushSubtree(model, name, depth, seen, out, squadName) {
  const key = lc(name);
  if (seen.has(key)) return;
  seen.add(key);
  const agent = model.agentByName.get(key);
  subagentNames(model, agent).forEach(sub => {
    out.push({ kind: "subagent", name: sub, agent: model.agentByName.get(lc(sub)), depth, parentAgent: name, squadName });
    pushSubtree(model, sub, depth + 1, seen, out, squadName);
  });
  seen.delete(key);
}
```

In `treeNodes`, pass `sq.name` and stamp `squadName` on the leader/member/sole nodes:

```js
if (sq.kind === "leaderless" || sq.kind === "router") {
  (sq.members[0] ? [sq.members[0]] : []).forEach(n => {
    out.push({ kind: "sole", name: n, agent: model.agentByName.get(lc(n)), depth: 1, squadName: sq.name });
    pushSubtree(model, n, 2, new Set(), out, sq.name);
  });
  return;
}
if (sq.leader) {
  out.push({ kind: "leader", name: sq.leader, agent: model.agentByName.get(lc(sq.leader)), depth: 1, shared: shared.get(lc(sq.leader)) || 0, squadName: sq.name });
  pushSubtree(model, sq.leader, 2, new Set(), out, sq.name);
}
sq.members.forEach(m => {
  out.push({ kind: "member", name: m, agent: model.agentByName.get(lc(m)), depth: 1, shared: shared.get(lc(m)) || 0, squadName: sq.name });
  pushSubtree(model, m, 2, new Set(), out, sq.name);
});
```

(The router-branch `sole` node also gets `squadName`; menus never act on router nodes, but keeping the field consistent is harmless.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `make test-web`
Expected: PASS — new + all existing store/tree tests green (enrichment is additive; `treeNodes nests subagents…` still passes).

- [ ] **Step 5: Commit**

```bash
git add web/fleet/store.js web/fleet/store.test.js
git commit -m "feat(fleet): enrich treeNodes with squadName/parentAgent context (Phase 5)"
```

---

## Task 4: Shared `fleetMenu` dropdown + header `＋` create control

**Files:**
- Modify: `web/settings.js` (`renderFleetPane`, header template ~line 297–333; new helpers after the slide-over host block ~line 365)
- Modify: `web/css/settings/fleet.css`
- Modify: `web/i18n/en.json` (+ fr/es/de)

**Interfaces:**
- Consumes: `store.addSquad`/`store.addAgent` (Task 1), `appPrompt` (settings.js:6332), `escHtml`, `tr`, `select(ref)`, `paintTree`/`paintEditor`/`paintActions`.
- Produces: `fleetMenu(ev, items)` — a body-appended `position:fixed` dropdown; `items` = array of `"SEP"` | `{ label, action?, submenu?(), disabled?, danger? }`. Reused by Tasks 5–8. A `＋` button in the shell header opening a create menu.

- [ ] **Step 1: Add the `fleetMenu` helper**

Inside `renderFleetPane`, after `host.querySelector("#fleet-slideover-backdrop").addEventListener(...)` (~line 365), add:

```js
    // ── Shared context-menu dropdown (Phase 5) ──
    // Body-appended, position:fixed so it escapes the pane's overflow; dismissed
    // on any outside click / scroll / Escape / resize. `items` entries are
    // "SEP" | { label, action?, submenu?, disabled?, danger? }. A submenu opens a
    // nested fleetMenu anchored to the item (used by the candidate pickers).
    let openMenuEl = null;
    function closeFleetMenu() {
      if (openMenuEl) { openMenuEl.remove(); openMenuEl = null; }
      document.removeEventListener("click", closeFleetMenu, true);
      document.removeEventListener("scroll", closeFleetMenu, true);
      window.removeEventListener("resize", closeFleetMenu);
      document.removeEventListener("keydown", menuEsc);
    }
    function menuEsc(e) { if (e.key === "Escape") closeFleetMenu(); }
    function fleetMenu(ev, items) {
      closeFleetMenu();
      const menu = document.createElement("div");
      menu.className = "fleet-ctx-menu";
      items.forEach(it => {
        if (it === "SEP") { const d = document.createElement("div"); d.className = "fleet-ctx-sep"; menu.appendChild(d); return; }
        const b = document.createElement("button");
        b.type = "button";
        b.className = "fleet-ctx-item" + (it.danger ? " danger" : "");
        b.textContent = it.submenu ? `${it.label} ▸` : it.label;
        if (it.disabled) b.disabled = true;
        else b.addEventListener("click", (e) => {
          e.stopPropagation();
          if (it.submenu) {
            const r = b.getBoundingClientRect();
            fleetMenu({ clientX: r.right, clientY: r.top }, it.submenu());
          } else { closeFleetMenu(); it.action && it.action(); }
        });
        menu.appendChild(b);
      });
      document.body.appendChild(menu);
      // Position within the viewport (flip left/up if it would overflow).
      const vw = window.innerWidth, vh = window.innerHeight, r = menu.getBoundingClientRect();
      let x = ev.clientX, y = ev.clientY;
      if (x + r.width > vw) x = Math.max(4, vw - r.width - 4);
      if (y + r.height > vh) y = Math.max(4, vh - r.height - 4);
      menu.style.left = x + "px"; menu.style.top = y + "px";
      openMenuEl = menu;
      // Defer listener attach so the opening click doesn't immediately close it.
      setTimeout(() => {
        document.addEventListener("click", closeFleetMenu, true);
        document.addEventListener("scroll", closeFleetMenu, true);
        window.addEventListener("resize", closeFleetMenu);
        document.addEventListener("keydown", menuEsc);
      }, 0);
    }
```

- [ ] **Step 2: Add the `＋` button to the shell header**

In the header template, change the peers row so a `＋` create button sits after the reload button. Replace the `<button ... id="fleet-reload" ...>⟳</button>` line region with:

```js
          <div class="fleet-shell-actions">
            <button type="button" class="fleet-shell-add" id="fleet-add" data-tip="${escHtml(tr("fleet.create.tip"))}" aria-label="${escHtml(tr("fleet.create.tip"))}">＋</button>
            <button type="button" class="fleet-shell-reload" id="fleet-reload" data-tip="${escHtml(tr("fleet.shell.reloadTip"))}" aria-label="${escHtml(tr("fleet.shell.reloadTip"))}">⟳</button>
          </div>
```

- [ ] **Step 3: Wire the create menu**

After the `#fleet-reload` click handler (~line 474), add:

```js
    host.querySelector("#fleet-add").addEventListener("click", (ev) => {
      fleetMenu(ev, [
        { label: tr("fleet.create.squad"), action: async () => {
          const name = store.addSquad();
          const chosen = await appPrompt(tr("fleet.create.squadName"), name);
          const sq = store.squad(name);
          if (chosen && sq) { sq.name = chosen; store.touch(); }
          select({ type: "squad", name: (chosen || name) });
        } },
        { label: tr("fleet.create.agent"), action: async () => {
          const name = store.addAgent();
          const chosen = await appPrompt(tr("fleet.create.agentName"), name);
          const a = store.agent(name);
          if (chosen && a) { a.name = chosen; store.touch(); }
          select({ type: "agent", name: (chosen || name) });
        } },
      ]);
    });
```

(`select` repaints the tree + opens the new node's editor; the store mutation's `store.touch()` reveals the Save bar.)

- [ ] **Step 4: Add i18n keys + CSS**

`web/i18n/en.json` (add near the `fleet.shell.*` block):

```json
  "fleet.create.tip": "Create a squad or agent",
  "fleet.create.squad": "New squad…",
  "fleet.create.agent": "New agent…",
  "fleet.create.squadName": "Name the new squad",
  "fleet.create.agentName": "Name the new agent",
```

Translate the same five keys in `fr.json`/`es.json`/`de.json` (Squad/agent stay untranslated per the glossary; translate the surrounding words). `web/css/settings/fleet.css` — append:

```css
.fleet-shell-actions { display: flex; align-items: center; gap: 6px; }
.fleet-shell-add {
  background: none; border: 1px solid var(--border); color: var(--text);
  border-radius: 6px; width: 28px; height: 28px; cursor: pointer; font-size: 16px; line-height: 1;
}
.fleet-shell-add:hover { background: var(--surface-2); }
.fleet-ctx-menu {
  position: fixed; z-index: 10000; min-width: 180px; padding: 4px;
  background: var(--surface); border: 1px solid var(--border); border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0,0,0,.28);
}
.fleet-ctx-item {
  display: block; width: 100%; text-align: left; padding: 6px 10px; border: 0;
  background: none; color: var(--text); border-radius: 5px; cursor: pointer; font-size: 13px;
}
.fleet-ctx-item:hover:not(:disabled) { background: var(--surface-2); }
.fleet-ctx-item:disabled { opacity: .45; cursor: default; }
.fleet-ctx-item.danger { color: var(--danger, #d9534f); }
.fleet-ctx-sep { height: 1px; margin: 4px 6px; background: var(--border); }
```

- [ ] **Step 5: Regenerate locales + syntax check**

```bash
make i18n
node --check web/settings.js
```
Expected: no output (clean).

- [ ] **Step 6: Commit**

```bash
git add web/settings.js web/css/settings/fleet.css web/i18n/*.json web/i18n/locales.js
git commit -m "feat(fleet): shared fleetMenu dropdown + header ＋ create control (Phase 5)"
```

---

## Task 5: Tree row menu affordance (kebab + right-click) → `onMenu`

**Files:**
- Modify: `web/fleet/tree.js` (`rowHTML`, `render`)
- Modify: `web/settings.js` (`paintTree` — pass `onMenu`; stub handler)
- Modify: `web/css/settings/fleet.css`

**Interfaces:**
- Produces: `render(container, model, { …, onMenu })` — when `onMenu` is a function, each actionable row (any row with a resolvable `ref.type !== "none"`) gets a hover `⋯` kebab button and a `contextmenu` listener, both calling `onMenu(ref, ev, node)` where `node` is the full tree node (carrying `squadName`/`parentAgent`/`kind`). Additive: a caller omitting `onMenu` renders exactly as today.

- [ ] **Step 1: Emit the node from rows**

In `tree.js`, the row needs its full node available at click time. Change `render` to build a `nodeByKey` map keyed by `refType:refName:squadName` and stamp `data-node-idx` on each row, OR (simpler) attach the node via a closure. Implement by iterating with index:

Replace the `nodes.map(rowHTML).join("")` + the click-wiring block in `render` with:

```js
  function render(container, model, opts) {
    _selRef = (opts && opts.selectedRef) || null;
    const nodes = window.FleetStore.treeNodes(model);
    const hasSquads = model.squads.some(s => s.kind !== "router");
    const body = hasSquads
      ? `<div class="fleet-tree">${nodes.map((n, i) => rowHTML(n, i)).join("")}</div>`
      : `<div class="fleet-empty">${esc(t("fleet.empty"))}</div>`;
    container.innerHTML = `<div class="fleet-view">${legendHTML()}${body}</div>`;
    const onSelect = opts && opts.onSelect;
    const onMenu = opts && opts.onMenu;
    container.querySelectorAll(".fleet-row[data-ref-type]").forEach(row => {
      const type = row.dataset.refType;
      if (type === "none") return;
      const node = nodes[Number(row.dataset.nodeIdx)];
      const ref = { type, name: row.dataset.refName };
      if (typeof onSelect === "function") {
        row.classList.add("fleet-row-click");
        row.addEventListener("click", (e) => {
          if (e.target.closest(".fleet-kebab")) return; // kebab handles itself
          onSelect(ref);
        });
      }
      if (typeof onMenu === "function") {
        row.addEventListener("contextmenu", (e) => { e.preventDefault(); onMenu(ref, e, node); });
        const kebab = row.querySelector(".fleet-kebab");
        if (kebab) kebab.addEventListener("click", (e) => { e.stopPropagation(); onMenu(ref, e, node); });
      }
    });
  }
```

In `rowHTML(n, i)`, add the `data-node-idx` attribute and the kebab button (kebab only for actionable rows — not `unused-header`, not `router`):

```js
  function rowHTML(n, i) {
    if (n.kind === "unused-header") {
      return `<div class="fleet-unused-header">${esc(t("fleet.unusedAgents"))}</div>`;
    }
    const glyph = GLYPH[n.kind] || "";
    const indent = 8 + n.depth * 16;
    const model = n.agent && n.agent.model_ref ? `<span class="fleet-sub">${esc(n.agent.model_ref)}</span>` : "";
    const badge = n.shared && n.shared > 1 ? `<span class="fleet-badge" data-tip="${esc(t("fleet.sharedTip", { n: n.shared }))}">⌂${n.shared}</span>` : "";
    let tag = "";
    if (n.kind === "router") tag = `<span class="fleet-tag">${esc(t("fleet.tag.router"))}</span>`;
    else if (n.kind === "leaderless") tag = `<span class="fleet-tag">${esc(t("fleet.tag.leaderless"))}</span>`;
    if (n.squad && n.squad.hidden) tag += `<span class="fleet-tag">${esc(t("fleet.tag.hidden"))}</span>`;
    const ref = n.kind === "router" ? { type: "router", name: n.name }
      : (n.kind === "squad" || n.kind === "leaderless") ? { type: "squad", name: n.name }
      : (n.kind === "leader" || n.kind === "member" || n.kind === "sole"
         || n.kind === "subagent" || n.kind === "unused") ? { type: "agent", name: n.name }
      : { type: "none", name: "" };
    const sel = _selRef && _selRef.type === ref.type && lc2(_selRef.name) === lc2(ref.name)
      && ref.type !== "none" ? " selected" : "";
    // Kebab for everything except the router (auto-managed, read-only).
    const kebab = (ref.type !== "none" && n.kind !== "router")
      ? `<button type="button" class="fleet-kebab" aria-label="${esc(t("fleet.menu.open"))}" data-tip="${esc(t("fleet.menu.open"))}">⋯</button>` : "";
    return `<div class="fleet-row kind-${esc(n.kind)}${sel}" data-node-idx="${i}" data-ref-type="${esc(ref.type)}" data-ref-name="${esc(ref.name)}" style="padding-left:${indent}px">
      <span class="fleet-glyph">${esc(glyph)}</span>
      <span class="fleet-name">${esc(n.name)}</span>${badge}${model}${tag}${kebab}
    </div>`;
  }
```

- [ ] **Step 2: Pass a stub `onMenu` from settings.js**

In `paintTree`:

```js
    function paintTree() {
      window.FleetTree.render(treeHost, store.model(), {
        selectedRef: selected,
        onSelect: select,
        onMenu: openNodeMenu,
      });
    }
```

Add a stub `openNodeMenu` (filled by Tasks 6–7) just above `paintTree`:

```js
    // Per-node context menu. Agent vs squad menus land in Tasks 6/7.
    function openNodeMenu(ref, ev, node) {
      if (ref.type === "squad") return openSquadNodeMenu(ref, ev, node);
      if (ref.type === "agent") return openAgentNodeMenu(ref, ev, node);
    }
    function openSquadNodeMenu() {}   // Task 7
    function openAgentNodeMenu() {}   // Task 6
```

- [ ] **Step 3: CSS for the kebab**

Append to `fleet.css`:

```css
.fleet-row { position: relative; }
.fleet-kebab {
  margin-left: auto; opacity: 0; background: none; border: 0; color: var(--muted, #888);
  cursor: pointer; font-size: 15px; line-height: 1; padding: 0 4px; border-radius: 4px;
}
.fleet-row:hover .fleet-kebab, .fleet-row.selected .fleet-kebab { opacity: 1; }
.fleet-kebab:hover { background: var(--hover-bg, rgba(127,127,127,.10)); color: var(--text, inherit); }
@media (pointer: coarse) { .fleet-kebab { opacity: 1; } }
```

Add i18n key `"fleet.menu.open": "Actions"` (+ fr/es/de).

- [ ] **Step 4: Syntax + test gate**

```bash
node --check web/fleet/tree.js
node --check web/settings.js
make test-web
make i18n
```
Expected: clean; store/tree tests still pass.

- [ ] **Step 5: Commit**

```bash
git add web/fleet/tree.js web/settings.js web/css/settings/fleet.css web/i18n/*.json web/i18n/locales.js
git commit -m "feat(fleet): tree row kebab + right-click menu affordance (Phase 5)"
```

---

## Task 6: Agent node context-menu actions

**Files:**
- Modify: `web/settings.js` (replace the `openAgentNodeMenu` stub)
- Modify: `web/i18n/en.json` (+ fr/es/de)

**Interfaces:**
- Consumes: `store.addToTeam`, `store.teamCandidates`, `store.addMember`, `store.setLeader`, `store.setEnabled`, `store.duplicateAgent`, `store.removeAgent`, `store.toggleMember`, `store.model()`, `store.squad`, `store.agent`, `fleetMenu`, `select`, `appConfirm`, the enriched `node.squadName`/`node.kind`.

- [ ] **Step 1: Implement `openAgentNodeMenu`**

Replace the `function openAgentNodeMenu() {}` stub with:

```js
    // Agent node menu. `node.kind` is leader|member|sole|subagent|unused;
    // `node.squadName` (when present) is the squad this instance appears under.
    function openAgentNodeMenu(ref, ev, node) {
      const name = ref.name;
      const a = store.agent(name);
      if (!a) return;
      const m = store.model();
      const squadName = node && node.squadName;
      const isBuiltin = isBuiltinAgent(name);
      const isEnabled = name === "leader" ? true : a.enabled !== false;
      const items = [];

      // Add to team… — sub-agents this agent may delegate to (cycle-safe).
      const teamCands = store.teamCandidates(name).filter(c =>
        !((a.subagents || []).some(s => s.toLowerCase() === c.toLowerCase())));
      items.push({ label: tr("fleet.menu.addTeam"), disabled: teamCands.length === 0,
        submenu: () => teamCands.map(c => ({ label: c, action: () => { store.addToTeam(name, c); } })) });

      // Add to squad… — squads (non-router) that don't already include this agent.
      const squadCands = m.squads.filter(sq => sq.kind !== "router")
        .filter(sq => sq.leader.toLowerCase() !== name.toLowerCase()
          && !(sq.members || []).some(mm => mm.toLowerCase() === name.toLowerCase()))
        .map(sq => sq.name);
      items.push({ label: tr("fleet.menu.addSquad"), disabled: squadCands.length === 0,
        submenu: () => squadCands.map(sn => ({ label: sn, action: () => { store.addMember(sn, name); } })) });

      // Make leader (of the squad this node appears under) — only for a member
      // node in a real (non-leaderless, non-router) squad, and only if the agent
      // is leader-eligible.
      const sq = squadName ? store.squad(squadName) : null;
      const sqKind = sq ? m.squads.find(s => s.name === sq.name).kind : null;
      const isLeaderAgent = !!a.leader || name.toLowerCase() === "leader";
      if (node && node.kind === "member" && sqKind === "squad" && isLeaderAgent) {
        items.push({ label: tr("fleet.menu.makeLeader"), action: () => { store.setLeader(squadName, name); } });
      }

      items.push("SEP");
      items.push({ label: isEnabled ? tr("fleet.menu.disable") : tr("fleet.menu.enable"),
        disabled: name === "leader", action: () => { store.setEnabled(name, !isEnabled); } });
      items.push({ label: tr("fleet.menu.duplicate"), action: () => {
        const nn = store.duplicateAgent(name); if (nn) select({ type: "agent", name: nn }); } });

      items.push("SEP");
      // Remove-from-squad for a member/leader appearance; delete for the agent
      // itself (unused pool) — built-ins can be disabled but never deleted.
      if (node && node.kind === "member" && squadName) {
        items.push({ label: tr("fleet.menu.removeFromSquad"), danger: true,
          action: () => { store.toggleMember(squadName, name); } });
      } else if (node && node.kind === "leader" && squadName) {
        items.push({ label: tr("fleet.menu.removeFromSquad"), danger: true,
          disabled: true, action: () => {} }); // leader removal is via the squad editor's leader dropdown
      }
      if (!isBuiltin) {
        items.push({ label: tr("fleet.menu.deleteAgent"), danger: true, action: async () => {
          if (!await appConfirm(tr("set.confirm.removeAgent", { name }))) return;
          store.removeAgent(name);
          if (selected && selected.type === "agent" && selected.name.toLowerCase() === name.toLowerCase()) { selected = null; paintEditor(); }
        } });
      }

      fleetMenu(ev, items);
    }
```

- [ ] **Step 2: Add i18n keys**

`en.json`:

```json
  "fleet.menu.addTeam": "Add to team…",
  "fleet.menu.addSquad": "Add to squad…",
  "fleet.menu.makeLeader": "Make leader",
  "fleet.menu.enable": "Enable",
  "fleet.menu.disable": "Disable",
  "fleet.menu.duplicate": "Duplicate",
  "fleet.menu.removeFromSquad": "Remove from squad",
  "fleet.menu.deleteAgent": "Delete agent",
```

Translate in fr/es/de (agent names stay English).

- [ ] **Step 3: Syntax + test gate**

```bash
node --check web/settings.js
make i18n
make test-web
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/settings.js web/i18n/*.json web/i18n/locales.js
git commit -m "feat(fleet): agent node context-menu actions (Phase 5)"
```

---

## Task 7: Squad node context-menu actions

**Files:**
- Modify: `web/settings.js` (replace the `openSquadNodeMenu` stub)
- Modify: `web/i18n/en.json` (+ fr/es/de)

**Interfaces:**
- Consumes: `store.addMember`, `store.setLeader`, `store.duplicateSquad`, `store.removeSquad`, `store.isDefaultSquad`, `store.model()`, `store.squad`, `fleetMenu`, `select`, `appConfirm`.

- [ ] **Step 1: Implement `openSquadNodeMenu`**

Replace the `function openSquadNodeMenu() {}` stub with:

```js
    // Squad node menu. Router squads never reach here (no kebab). The default
    // squad is protected: no remove, no leaderless.
    function openSquadNodeMenu(ref, ev, node) {
      const name = ref.name;
      const sq = store.squad(name);
      if (!sq) return;
      const m = store.model();
      const kind = (m.squads.find(s => s.name === name) || {}).kind;
      if (kind === "router") return; // defensive — router has no kebab
      const isDefault = store.isDefaultSquad(name);
      const leaderless = kind === "leaderless";
      const items = [];

      // Add member… — enabled agents not already the leader/a member.
      const memberCands = (m.agents || [])
        .filter(x => x && x.name && (x.enabled === undefined || x.enabled) && x.name.toLowerCase() !== "curator")
        .filter(x => x.name.toLowerCase() !== (sq.leader || "").toLowerCase()
          && !(sq.members || []).some(mm => mm.toLowerCase() === x.name.toLowerCase()))
        .map(x => x.name);
      items.push({ label: leaderless ? tr("fleet.menu.setMember") : tr("fleet.menu.addMember"),
        disabled: memberCands.length === 0,
        submenu: () => memberCands.map(cn => ({ label: cn, action: () => { store.addMember(name, cn); } })) });

      // Make leader… — leader-eligible agents (never for the leaderless kind,
      // whose "leader" is the leader dropdown's (none) option; use the editor).
      if (!leaderless) {
        const isLeaderAgent = x => !!x.leader || (x.name || "").toLowerCase() === "leader";
        const leaderCands = (m.agents || [])
          .filter(x => x && x.name && (x.enabled === undefined || x.enabled) && x.name.toLowerCase() !== "curator" && isLeaderAgent(x))
          .filter(x => x.name.toLowerCase() !== (sq.leader || "").toLowerCase())
          .map(x => x.name);
        items.push({ label: tr("fleet.menu.setLeader"), disabled: leaderCands.length === 0,
          submenu: () => leaderCands.map(cn => ({ label: cn, action: () => { store.setLeader(name, cn); } })) });
      }

      items.push("SEP");
      items.push({ label: tr("fleet.menu.duplicate"), action: () => {
        const nn = store.duplicateSquad(name); if (nn) select({ type: "squad", name: nn }); } });

      if (!isDefault) {
        items.push("SEP");
        items.push({ label: tr("fleet.menu.deleteSquad"), danger: true, action: async () => {
          if (!await appConfirm(tr("set.confirm.deleteSquad", { name }))) return;
          store.removeSquad(name);
          if (selected && selected.type === "squad" && selected.name.toLowerCase() === name.toLowerCase()) { selected = null; paintEditor(); }
        } });
      }

      fleetMenu(ev, items);
    }
```

- [ ] **Step 2: Add i18n keys**

`en.json`:

```json
  "fleet.menu.addMember": "Add member…",
  "fleet.menu.setMember": "Set member…",
  "fleet.menu.setLeader": "Set leader…",
  "fleet.menu.deleteSquad": "Delete squad",
```

Translate in fr/es/de.

- [ ] **Step 3: Syntax + test gate**

```bash
node --check web/settings.js
make i18n
make test-web
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/settings.js web/i18n/*.json web/i18n/locales.js
git commit -m "feat(fleet): squad node context-menu actions (Phase 5)"
```

---

## Task 8: Light drag-and-drop (reorder members, drop into a squad)

**Files:**
- Modify: `web/fleet/tree.js` (`render` — DnD wiring; `rowHTML` — `draggable`)
- Modify: `web/settings.js` (`paintTree` — pass `onReorder`/`onDropMember`)
- Modify: `web/css/settings/fleet.css`

**Interfaces:**
- Produces: `render(container, model, { …, onReorder, onDropMember })`. HTML5 DnD:
  - **member** rows and **unused** rows are `draggable`;
  - dropping a dragged agent on a **member row of the same squad** calls `onReorder(squadName, fromIdx, toIdx)` (reorder);
  - dropping a dragged agent on a **squad row** (or a member row of a *different* squad) calls `onDropMember(targetSquadName, agentName)` (add a membership reference — per the spec, agents are shared, so this **adds**, it does not move-out of the source).
  - Additive: omitting the callbacks disables DnD entirely.

The drag payload is carried in a module-level `_drag = { name, squadName, kind, idx }` (dataTransfer text is set too, for the browser, but the object is the source of truth). Router rows are never drag sources and never drop targets.

- [ ] **Step 1: Make rows draggable + carry an index within their squad**

`rowHTML` already stamps `data-node-idx="${i}"` (global index, Task 5). For reorder we need the member's index **within its squad's `members`**. Add `data-member-idx` for member nodes. In `treeNodes` this is derivable, but simplest is to compute it in `rowHTML` from the node — add a `memberIdx` field in Task 3's member push? To avoid re-touching the store, compute in `render` instead: track a per-squad counter is fragile. **Decision:** add `memberIdx` to member nodes in `treeNodes` (extend Task 3's member loop). Since Task 3 is already merged, add it here as a one-line store change:

In `treeNodes`, the member loop:

```js
sq.members.forEach((m, mi) => {
  out.push({ kind: "member", name: m, agent: model.agentByName.get(lc(m)), depth: 1, shared: shared.get(lc(m)) || 0, squadName: sq.name, memberIdx: mi });
  pushSubtree(model, m, 2, new Set(), out, sq.name);
});
```

(Add one store test: a member node exposes `memberIdx`. Keep it in `store.test.js`.)

In `rowHTML`, set `draggable` + the drag data attributes for member/unused rows:

```js
    const dragAttrs = (n.kind === "member" || n.kind === "unused")
      ? ` draggable="true" data-drag-name="${esc(n.name)}"${n.kind === "member" ? ` data-drag-squad="${esc(n.squadName || "")}" data-member-idx="${n.memberIdx}"` : ""}` : "";
```

and add `${dragAttrs}` into the row's opening `<div class="fleet-row …"` tag.

- [ ] **Step 2: Wire DnD in `render`**

Inside `render`, after the existing per-row `onSelect`/`onMenu` wiring, add (only when `onReorder`/`onDropMember` are provided):

```js
    const onReorder = opts && opts.onReorder;
    const onDropMember = opts && opts.onDropMember;
    if (typeof onReorder === "function" || typeof onDropMember === "function") {
      container.querySelectorAll(".fleet-row[draggable=true]").forEach(row => {
        row.addEventListener("dragstart", (e) => {
          _drag = { name: row.dataset.dragName, squadName: row.dataset.dragSquad || "",
                    kind: row.classList.contains("kind-member") ? "member" : "unused",
                    idx: row.dataset.memberIdx != null ? Number(row.dataset.memberIdx) : -1 };
          row.classList.add("dragging");
          e.dataTransfer.effectAllowed = "move";
          try { e.dataTransfer.setData("text/plain", _drag.name); } catch (_) {}
        });
        row.addEventListener("dragend", () => { row.classList.remove("dragging"); clearDropCues(container); _drag = null; });
      });
      // Drop targets: squad rows (add member) and member rows (reorder within
      // the same squad, else add to that member's squad). Router rows excluded.
      container.querySelectorAll(".fleet-row.kind-squad, .fleet-row.kind-leaderless, .fleet-row.kind-member, .fleet-row.kind-leader, .fleet-row.kind-sole").forEach(row => {
        row.addEventListener("dragover", (e) => {
          if (!_drag) return;
          e.preventDefault(); e.dataTransfer.dropEffect = "move";
          clearDropCues(container); row.classList.add("drop-target");
        });
        row.addEventListener("dragleave", () => row.classList.remove("drop-target"));
        row.addEventListener("drop", (e) => {
          e.preventDefault();
          if (!_drag) return;
          // Resolve the target squad name: a squad row's ref-name IS the squad;
          // a leader/member/sole row carries squadName in the node — read from
          // the nodes array by data-node-idx.
          const node = nodes[Number(row.dataset.nodeIdx)];
          const sqName = (row.classList.contains("kind-squad") || row.classList.contains("kind-leaderless"))
            ? row.dataset.refName : (node && node.squadName);
          if (!sqName) return;
          // Never mutate the auto-managed router squad (its member renders as a
          // .kind-sole row, so it is otherwise a valid drop target).
          const tsq = model.squads.find(s => s.name.toLowerCase() === sqName.toLowerCase());
          if (!tsq || tsq.kind === "router") { clearDropCues(container); _drag = null; return; }
          const sameSquadReorder = _drag.kind === "member" && _drag.squadName
            && sqName.toLowerCase() === _drag.squadName.toLowerCase()
            && node && node.kind === "member" && typeof onReorder === "function";
          if (sameSquadReorder) onReorder(sqName, _drag.idx, node.memberIdx);
          else if (typeof onDropMember === "function") onDropMember(sqName, _drag.name);
          clearDropCues(container); _drag = null;
        });
      });
    }
```

Add module-level state + helper near the top of the IIFE (beside `_selRef`):

```js
  let _drag = null;
  function clearDropCues(container) {
    container.querySelectorAll(".drop-target").forEach(el => el.classList.remove("drop-target"));
  }
```

- [ ] **Step 3: Pass the callbacks from settings.js**

`paintTree`:

```js
    function paintTree() {
      window.FleetTree.render(treeHost, store.model(), {
        selectedRef: selected,
        onSelect: select,
        onMenu: openNodeMenu,
        onReorder: (sq, from, to) => store.reorderMember(sq, from, to),
        onDropMember: (sq, agent) => { store.addMember(sq, agent); },
      });
    }
```

- [ ] **Step 4: CSS drag affordances**

Append to `fleet.css`:

```css
.fleet-row[draggable=true] { cursor: grab; }
.fleet-row.dragging { opacity: .45; }
.fleet-row.drop-target { outline: 2px dashed var(--accent, #4a90d9); outline-offset: -2px; border-radius: 4px; }
```

- [ ] **Step 5: Syntax + test gate**

```bash
node --check web/fleet/tree.js
node --check web/settings.js
make test-web
```
Expected: clean; the `memberIdx` store test passes.

- [ ] **Step 6: Commit**

```bash
git add web/fleet/tree.js web/fleet/store.js web/fleet/store.test.js web/settings.js web/css/settings/fleet.css
git commit -m "feat(fleet): light drag-and-drop — reorder members + drop into squad (Phase 5)"
```

---

## Task 9: Registry-install eject-fix (`reloadRebuild` hook)

**Files:**
- Modify: `web/settings.js` (`doReload` ~line 1564–1566; `renderBody` top ~line 2003; `renderFleetPane`)

**Interfaces:**
- Produces: a module-level `let reloadRebuild = null;` override. When set, `doReload` calls it **instead of** `renderBody()` after invalidating caches — so a config reload triggered from *inside* the Fleet pane (a registry install in the slide-over, the Fleet `⟳`, Fleet Save, a Models-slide-over save) repaints the Fleet pane in place rather than tearing down the whole settings body (which ejected the user to the classic Agents view). `renderBody` resets the hook to `null` on every full rebuild, so a stale Fleet closure never fires after the user navigates away.

Root cause (verified): the classic `doInstallAgent`/`doInstallSquad` call `await doReload()`; `doReload` wipes `state.parsed`/`state.raw` and calls `renderBody()` (settings.js:1564–1566), which rebuilds `bodyEl` → re-runs `renderAgentForm` → replaces the whole Fleet DOM, discarding the open Registries slide-over.

- [ ] **Step 1: Add the module-level hook**

Near the other module-level Fleet/nav-context state (beside `let registriesHubRefresh = null;`, ~line 1362), add:

```js
  // When the Fleet pane is mounted, it registers a `reloadRebuild` so that a
  // config reload repaints the Fleet in place instead of rebuilding the whole
  // settings body (which would eject the user from an open slide-over — e.g. a
  // registry install). Cleared by renderBody() on any full-body rebuild so a
  // stale Fleet closure never fires after navigating away.
  let reloadRebuild = null;
```

- [ ] **Step 2: Route `doReload` through the hook**

Replace `if (state.activeFile) renderBody();` (settings.js:1566) with:

```js
      if (reloadRebuild) { try { await reloadRebuild(); } catch (_) { if (state.activeFile) renderBody(); } }
      else if (state.activeFile) renderBody();
```

(`doReload` is already `async`; awaiting the hook is safe. On a hook error, fall back to the full rebuild so the user is never left on stale DOM.)

- [ ] **Step 3: Reset the hook on full rebuild**

At the very top of `renderBody` (settings.js:2003, first statement inside the function body):

```js
    reloadRebuild = null; // a full-body rebuild supersedes any Fleet in-place hook
```

- [ ] **Step 4: Register the hook from `renderFleetPane`**

In `renderFleetPane`, after `resyncFromServer` is defined and before the final `paintTree(); paintEditor(); …` line, register:

```js
    // Repaint the Fleet in place after a config reload (instead of renderBody
    // tearing down the whole settings body). resyncFromServer re-reads agent.json
    // and rebuilds the store + tree/editor, preserving the selection by name; any
    // open slide-over (e.g. the Registries hub during an install) is left mounted.
    reloadRebuild = () => resyncFromServer();
```

Note: `resyncFromServer` currently early-returns on a `loadParsed` throw and rebuilds the store from the fresh config. Since `doReload` already deleted `state.parsed["agent"]`, `resyncFromServer`'s own `delete state.parsed["agent"]` is a harmless no-op and its `await loadParsed("agent")` re-fetches — exactly what is needed post-reload.

- [ ] **Step 5: Syntax + test gate**

```bash
node --check web/settings.js
make test-web
```
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add web/settings.js
git commit -m "fix(fleet): repaint in place on reload so registry install no longer ejects the Fleet (Phase 5)"
```

---

## Task 10: Docs, i18n coverage, cache-busters, final checks

**Files:**
- Modify: `web/docs/19-agents.md`
- Modify: `CLAUDE.md` (the Fleet bullet under "Agent topology")
- Modify: `web/index.html` (cache-busters)
- Modify: `web/i18n/locales.js` (regenerated)

- [ ] **Step 1: Regenerate locales + verify full key parity**

```bash
make i18n
```
Expected: no "missing key" warnings for the new `fleet.menu.*` / `fleet.create.*` keys across fr/es/de.

- [ ] **Step 2: Bump cache-busters in `web/index.html`**

- `assets/css/settings.css?v=12` → `?v=13`
- `assets/i18n/locales.js?v=50` → `?v=51`
- `assets/fleet/store.js?v=4` → `?v=5`
- `assets/fleet/tree.js?v=2` → `?v=3`
- `assets/settings.js?v=44` → `?v=45`

- [ ] **Step 3: Document Phase 5**

In `web/docs/19-agents.md`, add a short "Editing the Fleet" note: the `＋` create control, node right-click / `⋯` menus (add member, add to team, make leader, add to squad, duplicate, enable/disable, remove), and drag-and-drop (reorder members, drop an agent into a squad — a shared reference, not a move). In `CLAUDE.md`, extend the Fleet bullet: Phase 5 adds the interaction layer (context menus + `＋` create + light DnD) and the reload-in-place fix so a registry install from the slide-over no longer ejects the Fleet; still behind `agent_toolkit_fleet_preview`, zero Go.

- [ ] **Step 4: Full gate**

```bash
make test-web
node --check web/settings.js && node --check web/fleet/tree.js && node --check web/fleet/store.js
go vet ./...   # expect no Go changes at all this phase; sanity only
```
Expected: web tests pass; syntax clean; `go vet` clean.

- [ ] **Step 5: Commit**

```bash
git add web/docs/19-agents.md CLAUDE.md web/index.html web/i18n/locales.js
git commit -m "docs(fleet): Phase 5 interactions + cache-busters + i18n regen"
```

---

## Post-implementation: controller Playwright smoke (final review step)

After all tasks pass their reviews, the controller runs a live smoke (branch server, `OMNIS_WEB_DIR=$(pwd)/web`, **scratch `OMNIS_HOME`**, no token, restart the server so the bumped `index.html` is served). Verify, flag ON:

- `＋` → New squad / New agent create + name-prompt; the new node is selected and the Save bar shows dirty.
- Right-click **and** `⋯` on a squad → Add member… (picker), Set/Make leader… (picker), Duplicate, Delete squad (non-default); default squad shows no Delete.
- Right-click **and** `⋯` on an agent → Add to team… (cycle-excluded picker), Add to squad… (picker), Make leader (member-in-real-squad only), Enable/Disable (leader disabled), Duplicate, Remove from squad / Delete agent (built-in shows no Delete).
- DnD: drag a member within its squad → reorder; drag a member / unused agent onto another squad → added there (source unchanged); router is never a drag source or drop target.
- Save → hot-reload → the tree reflects every change; round-trip verified (reopen shows the same topology). Discard clears the draft.
- Registry slide-over → install an agent → the slide-over stays open, the Fleet tree behind it gains the agent, and the user is **not** ejected to the classic Agents view.
- Zero console errors. **Flag OFF:** the classic Agents/Squads/Remotes/Models/Global sub-tabs are byte-identical (no kebabs, no `＋`, no DnD).

Then proceed to **superpowers:finishing-a-development-branch**.
