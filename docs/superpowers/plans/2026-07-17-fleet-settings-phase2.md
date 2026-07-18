# Fleet Settings Phase 2 — Editors + Save Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to
> implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the read-only Fleet tree (Phase 1) into an editable surface — selecting a node
opens a **new**, store-driven agent or squad editor; edits mutate a store-owned draft; Save
round-trips through the existing `PUT /api/config/parsed/agent` route and hot-reloads.

**Architecture:** The Phase-1 pure model (`web/fleet/store.js`) grows a **stateful store** that
owns `base`/`draft` config copies, a typed mutation API (squad rules + cycle-safe team
candidates), `dirty()`, and `serialize()` — this is the "new save engine." Selecting a tree node
dispatches to **new** editor render functions (`renderFleetAgentEditor` / `renderFleetSquadEditor`,
co-located in `settings.js` because DOM editors need its private helpers `tr`/`escHtml`/tool
constants). The editors drive the store; the **already-modular** leaf widgets
(`renderAgentTeamBlock`, `populateAgentSkillBlock`/`MCPBlock`/`A2ABlock`) are **composed** (their
`onChange` routed to `store.touch`) so §11 must-preserve behaviors (cycle exclusion, MCP/A2A
pickers) can't regress. Save = `PUT prepareForSave("agent", store.serialize())` via the existing
route → reload → `store.commit()`.

**Tech Stack:** Vanilla JS (no bundler; `window.*` globals). Node `node --test` for the DOM-free
store. Live Playwright smoke for the DOM editors. i18n via `web/i18n/*.json` + `make i18n`.

## Global Constraints

- **No new runtime or dev dependencies.** Node built-in `node --test` only (as Phase 1).
- **Round-trip fidelity is sacred.** For `id === "agent"`, `prepareForSave` returns the value
  **unchanged** ([settings.js:947](../../../web/settings.js#L947)); the server does the
  layered delta-write + per-agent fan-out. Therefore the client must **preserve every key** of
  the loaded config. `store.serialize()` MUST equal the loaded config plus exactly the edits
  made — never drop or reorder-lose a key, especially the CLAUDE.md silently-dropped ones:
  `subagents`, `hidden`, squad `members`, `max_instances`, `resumable_sessions`,
  `max_tool_calls`, plus every top-level key (`router_squad`, `embed_model_ref`, `serpapi_key`,
  `serper_key`, `turn_budget`, …).
- **Persist-clean parity with the current editor** (do not change on-disk shape):
  `max_instances` persisted only when `> 1` (else key deleted); `resumable_sessions` persisted
  only when `false` (else key deleted); `subagents`/`mcp_servers` absent when empty; a leaderless
  squad stores `leader: "none"` (dropdown value) exactly as today.
- **Squad rules** (mirror server `resolveSquadEntries`): default squad name is read-only and
  cannot be leaderless; a leaderless squad (`leader` == `""`/`"none"`) has **exactly one** member;
  choosing a leader drops that leader from the members list; the router squad is special — **not
  editable** (read-only info panel), never a member/leader candidate.
- **Cycle prevention** mirrors `validateSubAgentGraph`: the Team picker excludes any agent that
  transitively depends on this one (`store.teamCandidates` reuses `wouldCreateCycle`).
- **Built-in agents** (`isBuiltinAgent(name)`) render description + instruction **read-only** from
  `state.builtinAgents[name]`; enable/disable and per-field edits behave exactly as the current
  `renderAgentDetail`.
- **Feature flag preserved:** everything stays behind `agent_toolkit_fleet_preview === "1"`; with
  the flag OFF, behavior is byte-identical to before Phase 2.
- **Legend glyphs verbatim** (Phase 1): `◇` router · `⬢` squad · `⬡` leaderless · `★` leader ·
  `•` member · `◆` sole · `↳` subagent · `⌂` shared.
- **i18n:** every new user-facing string is a `tr(...)` key added to all four
  `web/i18n/{en,fr,es,de}.json`, then `make i18n`. Glossary nouns (`Squad`, `Omnis`, tool/model
  ids, paths) are not translated.

---

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `web/fleet/store.js` | **Modify** | Add stateful `create(cfg)` store: base/draft, `model()`, `agent()`, `squad()`, `dirty()`, `serialize()`, `commit()`, `discard()`, `touch()`/`onChange()`, and rule mutations (`setLeader`, `toggleMember`, `setHidden`, `setSubagents`, `removeSquad`, `removeAgent`, `teamCandidates`). Keep all Phase-1 pure exports. |
| `web/fleet/store.test.js` | **Modify** | Add node tests: golden round-trip after edits, dirty transitions, commit/discard, and every rule mutation + cycle exclusion. |
| `web/fleet/tree.js` | **Modify** | `render(container, model, opts)` gains `onSelect(ref)` + `selectedRef` highlight; rows carry `data-ref-*` and a delegated click handler. Read-only content otherwise unchanged. |
| `web/settings.js` | **Modify** | New: `renderFleetPane` (split shell + node dispatch + Fleet Save/Discard bar), `renderFleetAgentEditor`, `renderFleetSquadEditor`, `renderFleetRouterInfo`, `saveFleet`. Replace the `sub === "fleet"` branch body to mount `renderFleetPane`. |
| `web/css/settings/fleet.css` | **Modify** | Styles for the split pane, editor host, selected row, Fleet action bar, hidden toggle. |
| `web/index.html` | **Modify** | Bump `settings.js?v=`, `fleet/tree.js?v=`, `fleet/store.js?v=`, `settings.css?v=` cache-busters. |
| `web/i18n/{en,fr,es,de}.json` | **Modify** | New `fleet.editor.*` keys (router info, hidden toggle, save/discard, select prompt). |
| `web/docs/19-agents.md`, `CLAUDE.md` | **Modify** | Note the editable Fleet preview. |

---

## Task 1: Stateful store — draft/serialize/dirty save engine

**Files:**
- Modify: `web/fleet/store.js`
- Test: `web/fleet/store.test.js`

**Interfaces:**
- Consumes: Phase-1 exports `build`, `treeNodes`, `wouldCreateCycle` (kept).
- Produces: `create(cfg) → store` with `store.model()`, `store.agent(name)`, `store.squad(name)`,
  `store.squadAt(i)`, `store.touch()`, `store.onChange(fn)`, `store.dirty()`, `store.serialize()`,
  `store.commit(saved?)`, `store.discard()`, `store.baseSnapshot()`, `store.draft()`. All Phase-1
  exports stay. Consumed by Tasks 2–7.

- [ ] **Step 1: Write failing tests** — append to `web/fleet/store.test.js`:

```js
const S = require("./store.js");

function cfg() {
  return {
    router_squad: "omnis",
    embed_model_ref: "qwen",
    agents: [
      { name: "leader", leader: true, tools: ["fs"], subagents: ["scout"] },
      { name: "scout", model_ref: "simple", max_instances: 5 },
      { name: "curator", builtin: true },
    ],
    squads: [
      { name: "omnis", leader: "none", members: ["omnis"], hidden: false },
      { name: "system", leader: "leader", members: ["scout"] },
      { name: "hiddenone", leader: "none", members: ["scout"], hidden: true },
    ],
  };
}

test("create: serialize of an untouched store equals the input config", () => {
  const st = S.create(cfg());
  assert.deepStrictEqual(st.serialize(), cfg());
  assert.strictEqual(st.dirty(), false);
});

test("create: editing a draft agent does not mutate the input config", () => {
  const original = cfg();
  const st = S.create(original);
  st.agent("scout").model_ref = "balanced";
  st.touch();
  assert.strictEqual(original.agents[1].model_ref, "simple"); // input untouched
  assert.strictEqual(st.serialize().agents[1].model_ref, "balanced");
  assert.strictEqual(st.dirty(), true);
});

test("create: serialize preserves every unrelated key after a scalar edit", () => {
  const st = S.create(cfg());
  st.agent("scout").model_ref = "balanced";
  st.touch();
  const out = st.serialize();
  assert.strictEqual(out.router_squad, "omnis");
  assert.strictEqual(out.embed_model_ref, "qwen");
  assert.deepStrictEqual(out.agents[0], cfg().agents[0]); // leader entry incl. subagents intact
  assert.strictEqual(out.squads[2].hidden, true);          // hidden squad preserved
  assert.strictEqual(out.agents[1].max_instances, 5);      // sibling key preserved
});

test("discard reverts the draft to base; dirty clears", () => {
  const st = S.create(cfg());
  st.agent("scout").model_ref = "balanced"; st.touch();
  assert.strictEqual(st.dirty(), true);
  st.discard();
  assert.strictEqual(st.dirty(), false);
  assert.deepStrictEqual(st.serialize(), cfg());
});

test("commit reseeds base so the same edit is no longer dirty", () => {
  const st = S.create(cfg());
  st.agent("scout").model_ref = "balanced"; st.touch();
  st.commit();                       // adopt current draft as the new base
  assert.strictEqual(st.dirty(), false);
  assert.strictEqual(st.serialize().agents[1].model_ref, "balanced");
});

test("commit(saved) adopts the server-returned value verbatim", () => {
  const st = S.create(cfg());
  st.agent("scout").model_ref = "x"; st.touch();
  const saved = cfg(); saved.agents[1].model_ref = "server-canon";
  st.commit(saved);
  assert.strictEqual(st.dirty(), false);
  assert.strictEqual(st.serialize().agents[1].model_ref, "server-canon");
});

test("onChange listeners fire on touch", () => {
  const st = S.create(cfg());
  let n = 0; st.onChange(() => n++);
  st.touch(); st.touch();
  assert.strictEqual(n, 2);
});

test("model() reflects draft edits for the tree", () => {
  const st = S.create(cfg());
  st.agent("scout").model_ref = "balanced"; st.touch();
  const m = st.model();
  assert.strictEqual(m.agentByName.get("scout").model_ref, "balanced");
});

test("create does NOT inject empty keys — a squad-less config stays clean and undirtied", () => {
  const bare = { agents: [{ name: "leader", leader: true }] }; // no squads key
  const st = S.create(bare);
  assert.strictEqual(st.dirty(), false);            // untouched ⇒ not dirty
  assert.ok(!("squads" in st.serialize()));         // no spurious squads:[] added
  assert.deepStrictEqual(st.serialize(), bare);     // exact round-trip
});
```

- [ ] **Step 2: Run to verify failure**

Run: `node --test web/fleet/store.test.js`
Expected: FAIL — `S.create is not a function`.

- [ ] **Step 3: Implement `create` in `web/fleet/store.js`**

Add (inside the factory, before `return`) a deterministic deep-equal + clone and the store
factory, then export `create`:

```js
  function clone(x) { return JSON.parse(JSON.stringify(x == null ? null : x)); }

  function deepEqual(a, b) {
    if (a === b) return true;
    if (typeof a !== typeof b) return false;
    if (a === null || b === null) return a === b;
    if (typeof a !== "object") return a === b;
    const arrA = Array.isArray(a), arrB = Array.isArray(b);
    if (arrA !== arrB) return false;
    if (arrA) {
      if (a.length !== b.length) return false;
      for (let i = 0; i < a.length; i++) if (!deepEqual(a[i], b[i])) return false;
      return true;
    }
    const ka = Object.keys(a), kb = Object.keys(b);
    if (ka.length !== kb.length) return false;
    for (const k of ka) { if (!Object.prototype.hasOwnProperty.call(b, k)) return false;
      if (!deepEqual(a[k], b[k])) return false; }
    return true;
  }

  function create(cfg) {
    let base = clone(cfg || {});
    let draft = clone(base);
    const listeners = [];
    // NEVER eagerly add `agents`/`squads` keys to the draft: a config that omits
    // one must round-trip without one (and must not read as dirty). Accessors +
    // build() already tolerate the absent key; mutations lazily initialize.
    const store = {
      model() { return build(draft); },
      draft() { return draft; },
      baseSnapshot() { return base; },
      agent(name) { const k = lc(name); return (draft.agents || []).find(a => a && lc(a.name) === k) || null; },
      squad(name) { const k = lc(name); return (draft.squads || []).find(s => s && lc(s.name) === k) || null; },
      squadAt(i) { return (draft.squads || [])[i] || null; },
      touch() { listeners.forEach(fn => { try { fn(); } catch (e) {} }); },
      onChange(fn) { if (typeof fn === "function") listeners.push(fn); },
      dirty() { return !deepEqual(draft, base); },
      serialize() { return draft; },
      commit(saved) { base = clone(saved != null ? saved : draft); draft = clone(base); store.touch(); },
      discard() { draft = clone(base); store.touch(); },
    };
    return store;
  }
```

Note: `serialize()` returns the live `draft`. The golden tests assert it deep-equals the input
for an untouched store — including a config that omits `squads` — because `draft = clone(cfg)`
preserves key order and shape and edits mutate in place. **`create` must NOT normalize, strip, or
inject anything** — the server owns cleanup; the client's job is fidelity.

Add `create` to the returned exports object:

```js
  return { build, serialize, sharedCounts, unusedAgents, treeNodes, wouldCreateCycle, validateSquad, create };
```

- [ ] **Step 4: Run tests to verify pass**

Run: `node --test web/fleet/store.test.js`
Expected: PASS (all Phase-1 tests + the 8 new tests).

- [ ] **Step 5: Commit**

```bash
git add web/fleet/store.js web/fleet/store.test.js
git commit -m "feat(fleet): stateful store — draft/serialize/dirty save engine"
```

---

## Task 2: Store — squad rules, cycle-safe team, structural mutations

**Files:**
- Modify: `web/fleet/store.js`
- Test: `web/fleet/store.test.js`

**Interfaces:**
- Consumes: `create` (Task 1), `wouldCreateCycle`, `lc`.
- Produces, on the store returned by `create`: `setLeader(squadName, leader)`,
  `toggleMember(squadName, name)`, `setHidden(squadName, bool)`, `setSubagents(agentName, list)`,
  `removeSquad(name)`, `removeAgent(name)`, `teamCandidates(agentName) → string[]`,
  `isDefaultSquad(name) → bool`. All mutate the draft and call `touch()`. Consumed by Tasks 4–6.

- [ ] **Step 1: Write failing tests** — append to `web/fleet/store.test.js`:

```js
test("setLeader on a real leader drops that leader from members", () => {
  const st = S.create(cfg());
  st.squad("system").members = ["scout", "leader"];
  st.setLeader("system", "leader");
  assert.deepStrictEqual(st.squad("system").members, ["scout"]);
});

test("setLeader('none') trims members to one (leaderless rule)", () => {
  const st = S.create(cfg());
  st.squad("system").members = ["scout", "leader"];
  st.setLeader("system", "none");
  assert.strictEqual(st.squad("system").members.length, 1);
});

test("toggleMember on a leaderless squad is single-select", () => {
  const st = S.create(cfg());
  st.toggleMember("hiddenone", "leader");   // hiddenone is leaderless (leader:'none')
  assert.deepStrictEqual(st.squad("hiddenone").members, ["leader"]);
  st.toggleMember("hiddenone", "leader");   // clicking the selected one clears it
  assert.deepStrictEqual(st.squad("hiddenone").members, []);
});

test("toggleMember on a led squad toggles membership", () => {
  const st = S.create(cfg());
  st.toggleMember("system", "curator");
  assert.ok(st.squad("system").members.includes("curator"));
  st.toggleMember("system", "curator");
  assert.ok(!st.squad("system").members.includes("curator"));
});

test("setHidden round-trips the hidden flag", () => {
  const st = S.create(cfg());
  st.setHidden("system", true);
  assert.strictEqual(st.serialize().squads[1].hidden, true);
  st.setHidden("system", false);
  assert.strictEqual(st.serialize().squads[1].hidden, false);
});

test("setSubagents keeps the key absent when empty (persist-clean)", () => {
  const st = S.create(cfg());
  st.setSubagents("leader", []);
  assert.ok(!("subagents" in st.serialize().agents[0]));
  st.setSubagents("leader", ["scout"]);
  assert.deepStrictEqual(st.serialize().agents[0].subagents, ["scout"]);
});

test("teamCandidates excludes self, curator, and cycle-creating agents", () => {
  const st = S.create(cfg());
  // leader already delegates to scout. scout's candidates must exclude leader
  // (leader depends on scout ⇒ scout→leader would cycle) and curator and self.
  const cands = st.teamCandidates("scout");
  assert.ok(!cands.includes("scout"));
  assert.ok(!cands.includes("curator"));
  assert.ok(!cands.includes("leader"));
});

test("removeSquad drops a non-default squad", () => {
  const st = S.create(cfg());
  st.removeSquad("hiddenone");
  assert.ok(!st.serialize().squads.some(s => s.name === "hiddenone"));
});

test("removeSquad refuses the default squad", () => {
  const st = S.create(cfg());
  st.squad("system").name = "default"; // make one default for the test
  const before = st.serialize().squads.length;
  st.removeSquad("default");
  assert.strictEqual(st.serialize().squads.length, before);
});

test("removeAgent drops the agent entry", () => {
  const st = S.create(cfg());
  st.removeAgent("scout");
  assert.ok(!st.serialize().agents.some(a => a.name === "scout"));
});
```

- [ ] **Step 2: Run to verify failure**

Run: `node --test web/fleet/store.test.js`
Expected: FAIL — `st.setLeader is not a function`.

- [ ] **Step 3: Implement the mutations** — inside `create`, before `return store;`, add these
methods onto `store` (they mirror the exact rules in the current editors:
[settings.js:2069–2117](../../../web/settings.js#L2069) squad rules,
[settings.js:5794–5806](../../../web/settings.js#L5794) team candidates):

```js
    function leaderlessSquad(sq) { return !sq.leader || lc(sq.leader) === "none"; }
    store.isDefaultSquad = (name) => lc(name) === "default";
    store.setLeader = (squadName, leader) => {
      const sq = store.squad(squadName); if (!sq) return;
      sq.leader = leader;
      if (!leader || lc(leader) === "none") {
        if (Array.isArray(sq.members) && sq.members.length > 1) sq.members = [sq.members[0]];
      } else if (Array.isArray(sq.members)) {
        sq.members = sq.members.filter(m => lc(m) !== lc(leader));
      }
      store.touch();
    };
    store.toggleMember = (squadName, name) => {
      const sq = store.squad(squadName); if (!sq) return;
      if (!Array.isArray(sq.members)) sq.members = [];
      if (leaderlessSquad(sq)) {
        sq.members = sq.members.includes(name) ? [] : [name];
      } else if (sq.members.includes(name)) {
        sq.members = sq.members.filter(m => m !== name);
      } else {
        sq.members.push(name);
      }
      store.touch();
    };
    store.setHidden = (squadName, v) => {
      const sq = store.squad(squadName); if (!sq) return;
      sq.hidden = !!v; store.touch();
    };
    store.setSubagents = (agentName, list) => {
      const a = store.agent(agentName); if (!a) return;
      if (Array.isArray(list) && list.length) a.subagents = list.slice();
      else delete a.subagents;
      store.touch();
    };
    store.removeSquad = (name) => {
      if (store.isDefaultSquad(name)) return;
      if (!Array.isArray(draft.squads)) return; // never inject the key
      const k = lc(name);
      draft.squads = draft.squads.filter(s => lc(s.name) !== k);
      store.touch();
    };
    store.removeAgent = (name) => {
      if (!Array.isArray(draft.agents)) return; // never inject the key
      const k = lc(name);
      draft.agents = draft.agents.filter(a => lc(a.name) !== k);
      store.touch();
    };
    store.teamCandidates = (agentName) => {
      const self = lc(agentName);
      const m = store.model();
      return (draft.agents || [])
        .filter(x => x && x.name)
        .filter(x => { const n = lc(x.name);
          if (n === self || n === "curator") return false;
          if (x.enabled === false) return false;
          // adding self→x cycles iff x already (transitively) depends on self
          if (wouldCreateCycle(m, agentName, x.name)) return false;
          return true; })
        .map(x => x.name);
    };
```

- [ ] **Step 4: Run tests to verify pass**

Run: `node --test web/fleet/store.test.js`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/fleet/store.js web/fleet/store.test.js
git commit -m "feat(fleet): store squad rules + cycle-safe team + structural mutations"
```

---

## Task 3: Fleet pane shell — selectable tree + editor host + Save/Discard

**Files:**
- Modify: `web/fleet/tree.js`, `web/settings.js`, `web/css/settings/fleet.css`, `web/index.html`
- Verify: live smoke (no node test — DOM).

**Interfaces:**
- Consumes: `FleetStore.create`, `FleetStore.treeNodes`, `FleetTree.render`.
- Produces: `renderFleetPane(host, cfg)` in settings.js (owns one store instance for the pane),
  and `FleetTree.render(container, model, opts)` where `opts = { onSelect(ref), selectedRef }`
  and `ref = { type: "agent"|"squad"|"router"|"none", name }`. Editor dispatch calls
  `renderFleetAgentEditor` / `renderFleetSquadEditor` / `renderFleetRouterInfo` (stubs here,
  filled in Tasks 4–6). Consumed by the `sub === "fleet"` branch of `renderAgentForm`.

- [ ] **Step 1: Make the tree emit selection** — in `web/fleet/tree.js`:
  1. In `rowHTML(n)`, compute a ref and stamp it on clickable rows. Add before the `return`:
     ```js
     const ref = n.kind === "router" ? { type: "router", name: n.name }
       : (n.kind === "squad" || n.kind === "leaderless") ? { type: "squad", name: n.name }
       : (n.kind === "leader" || n.kind === "member" || n.kind === "sole"
          || n.kind === "subagent" || n.kind === "unused") ? { type: "agent", name: n.name }
       : { type: "none", name: "" };
     ```
     Then change the `.fleet-row` open tag to include the ref + a `selected` class:
     ```js
     const sel = _selRef && _selRef.type === ref.type && lc2(_selRef.name) === lc2(ref.name)
       && ref.type !== "none" ? " selected" : "";
     return `<div class="fleet-row kind-${esc(n.kind)}${sel}" data-ref-type="${esc(ref.type)}" data-ref-name="${esc(ref.name)}" style="padding-left:${indent}px"> …unchanged inner… </div>`;
     ```
     Add a module-scope `let _selRef = null;` and `const lc2 = s => String(s||"").toLowerCase();`.
  2. In `render(container, model, opts)`: set `_selRef = (opts && opts.selectedRef) || null;`
     before building `nodes`. After `container.innerHTML = …`, wire one delegated click listener:
     ```js
     if (opts && typeof opts.onSelect === "function") {
       container.querySelectorAll(".fleet-row[data-ref-type]").forEach(row => {
         const type = row.dataset.refType;
         if (type === "none") return;
         row.classList.add("fleet-row-click");
         row.addEventListener("click", () => opts.onSelect({ type, name: row.dataset.refName }));
       });
     }
     ```
  Read-only content (legend, glyphs, badges, tags) is otherwise unchanged.

- [ ] **Step 2: Add the pane shell + dispatch + stubs** — in `web/settings.js`, add near the
  other Fleet code (after `agentSubtabs()`):

```js
  // Fleet pane (Phase 2): selectable tree (left) + editor host (right) + a
  // Fleet-local Save/Discard bar driven by the store's dirty state. One store
  // instance per mount; edits mutate its draft, Save PUTs store.serialize().
  function renderFleetPane(host, cfg) {
    const store = window.FleetStore.create(cfg);
    let selected = null; // { type, name }
    host.innerHTML = `
      <div class="fleet-pane">
        <div class="fleet-pane-tree" id="fleet-tree"></div>
        <div class="fleet-pane-editor" id="fleet-editor"></div>
      </div>
      <div class="fleet-actionbar" id="fleet-actions" hidden>
        <span class="fleet-dirty-note">${escHtml(tr("fleet.editor.unsaved"))}</span>
        <button type="button" class="fleet-discard" id="fleet-discard">${escHtml(tr("fleet.editor.discard"))}</button>
        <button type="button" class="fleet-save" id="fleet-save">${escHtml(tr("fleet.editor.save"))}</button>
      </div>`;
    const treeHost = host.querySelector("#fleet-tree");
    const editorHost = host.querySelector("#fleet-editor");
    const actions = host.querySelector("#fleet-actions");

    function paintTree() {
      window.FleetTree.render(treeHost, store.model(), { selectedRef: selected, onSelect: select });
    }
    function paintActions() { actions.hidden = !store.dirty(); }
    function paintEditor() {
      if (!selected) { editorHost.innerHTML = `<div class="fleet-editor-empty">${escHtml(tr("fleet.editor.selectPrompt"))}</div>`; return; }
      if (selected.type === "router") return renderFleetRouterInfo(editorHost, store, selected.name);
      if (selected.type === "squad")  return renderFleetSquadEditor(editorHost, store, selected.name);
      if (selected.type === "agent")  return renderFleetAgentEditor(editorHost, store, selected.name);
      editorHost.innerHTML = "";
    }
    function select(ref) { selected = ref; paintTree(); paintEditor(); }

    // Any store mutation repaints the tree (labels/badges) + the action bar.
    // Editors repaint themselves; tree/actions repaint here.
    store.onChange(() => { paintTree(); paintActions(); });

    host.querySelector("#fleet-discard").addEventListener("click", async () => {
      if (!store.dirty()) return;
      if (!await appConfirm(tr("set.confirm.discard"))) return;
      store.discard(); paintEditor();
    });
    host.querySelector("#fleet-save").addEventListener("click", () => saveFleet(store, () => { paintTree(); paintActions(); paintEditor(); }));

    paintTree(); paintEditor(); paintActions();
  }

  // Stubs — filled in Tasks 4–6. Each renders into `host`, drives `store`.
  function renderFleetRouterInfo(host, store, name) {
    host.innerHTML = `<div class="fleet-editor-info"><h2>${escHtml(name)}</h2><p>${escHtml(tr("fleet.editor.routerInfo"))}</p></div>`;
  }
  function renderFleetSquadEditor(host, store, name) { host.innerHTML = `squad: ${escHtml(name)}`; }
  function renderFleetAgentEditor(host, store, name) { host.innerHTML = `agent: ${escHtml(name)}`; }

  // saveFleet — filled in Task 7 (temporary body so the button is inert-safe).
  async function saveFleet(store, after) { setStatus(tr("set.status.saving")); }
```

- [ ] **Step 3: Mount the pane from `renderAgentForm`** — replace the current `sub === "fleet"`
  branch body ([settings.js:1831-1834](../../../web/settings.js#L1831)):

```js
    if (sub === "fleet") {
      host.innerHTML = `<div id="fleet-host"></div>`;
      renderFleetPane(host.querySelector("#fleet-host"), d);
    } else if (sub === "globals") {
```

- [ ] **Step 4: Styles** — append to `web/css/settings/fleet.css`:

```css
.fleet-pane { display: flex; gap: 12px; align-items: flex-start; }
.fleet-pane-tree { flex: 0 0 300px; max-width: 340px; overflow: auto; }
.fleet-pane-editor { flex: 1 1 auto; min-width: 0; }
.fleet-row-click { cursor: pointer; border-radius: 6px; }
.fleet-row-click:hover { background: var(--hover-bg, rgba(127,127,127,.10)); }
.fleet-row.selected { background: var(--accent-soft, rgba(80,140,255,.16)); }
.fleet-editor-empty, .fleet-editor-info { padding: 1rem; color: var(--muted, #888); }
.fleet-actionbar { display: flex; align-items: center; gap: 10px; justify-content: flex-end;
  padding: 8px 4px; border-top: 1px solid var(--border, rgba(127,127,127,.2)); margin-top: 10px; }
.fleet-dirty-note { margin-right: auto; color: var(--muted, #888); font-size: .85em; }
.fleet-save, .fleet-discard { padding: 5px 14px; border-radius: 6px; cursor: pointer; }
```

- [ ] **Step 5: Cache-busters** — in `web/index.html` bump the query strings on
  `assets/fleet/store.js?v=`, `assets/fleet/tree.js?v=`, `settings.js?v=`, and
  `css/settings.css?v=` (increment each by 1).

- [ ] **Step 6: Node tests still green + smoke**

Run: `node --test web/fleet/store.test.js` → PASS.
Smoke (see [web-ui-branch-smoke-recipe](../../../)): `make build-server`, run with
`OMNIS_WEB_DIR="$(pwd)/web"`, set `localStorage.agent_toolkit_fleet_preview="1"`, open
Settings → Agents → Fleet. Click a squad row → editor host shows `squad: <name>`; click a leader
row → `agent: <name>`; the row highlights; router row shows the info panel. No JS console errors.

- [ ] **Step 7: Commit**

```bash
git add web/fleet/tree.js web/settings.js web/css/settings/fleet.css web/index.html
git commit -m "feat(fleet): selectable tree + editor host + Save/Discard bar"
```

---

## Task 4: Squad editor (fresh, store-driven) + router info

**Files:**
- Modify: `web/settings.js`, `web/i18n/{en,fr,es,de}.json`
- Verify: live smoke.

**Interfaces:**
- Consumes: `store.squad(name)`, `store.model()`, `store.setLeader`, `store.toggleMember`,
  `store.setHidden`, `store.removeSquad`, `store.isDefaultSquad`, `store.touch`.
- Produces: full `renderFleetSquadEditor(host, store, name)` (replaces the Task-3 stub). It renders
  name (default read-only) · description · leader dropdown (with `none` for non-default) · member
  grid (leaderless single-select) · **hidden toggle (new)** · remove (non-default).

- [ ] **Step 1: Implement `renderFleetSquadEditor`** — replace the Task-3 stub. Port the field
  markup + rules from `renderSquadDetail`
  ([settings.js:1965-2128](../../../web/settings.js#L1965)) with these transforms:
  - Source the squad from `const sq = store.squad(name)` (not `d.squads[idx]`); guard null → empty
    prompt.
  - `const model = store.model();` build `leaderCandidates` from `model.agents` (agents with
    `leader === true || name === "leader"`, enabled, not curator) and `memberCandidates`
    (enabled, not curator) — same predicates as lines 1980-1985.
  - `const isDefault = store.isDefaultSquad(sq.name); const leaderless = !isDefault && (!sq.leader || lc(sq.leader)==="none");`
  - **Leader change** → `store.setLeader(sq.name, value)` then re-render this editor
    (`renderFleetSquadEditor(host, store, sq.name)`), instead of the in-place `sq.leader=…` block.
  - **Member click** → `store.toggleMember(sq.name, name)` then re-render (a store-driven repaint
    is fine here; the "don't re-sort while toggling" nicety from lines 2098-2115 is optional —
    prefer correctness; a full re-render is acceptable).
  - **Name/description** → mutate `sq.name` / `sq.description` on the draft object + `store.touch()`
    (name input disabled when `isDefault`). On name edit, re-render + restore caret as lines
    2056-2063 do.
  - **New — Hidden toggle:** after the members field, add a toggle bound to `store.setHidden`:
    ```js
    // Hidden squads (e.g. Session Search) exist but are never offered in the picker
    // or as a routing target. The classic editor never surfaced this — the Fleet
    // editor does, so the flag is editable without dropping to raw JSON.
    ```
    Render an `.agent-toggle-switch` (reuse the resumable toggle markup pattern,
    [settings.js:3870-3889](../../../web/settings.js#L3870)) with `checked = !!sq.hidden`, label
    `tr("fleet.editor.hidden")`, hint `tr("fleet.editor.hiddenHint")`, `change` →
    `store.setHidden(sq.name, cb.checked)`.
  - **Remove** (non-default) → `await appConfirm(tr("set.confirm.deleteSquad",{name:sq.name}))`
    then `store.removeSquad(sq.name)`; after removal the caller's `onChange` repaints the tree —
    also clear the editor: `host.innerHTML = ""` and reset selection is handled by the pane
    (acceptable to leave the editor showing empty until the next selection).
  - Use the existing CSS classes verbatim (`agent-detail-section`, `agent-detail-field`,
    `agent-detail-input`, `agent-tools-grid`, `agent-tool-card`, `agent-detail-hint`,
    `agent-detail-remove`) so it inherits styling.

  **Coverage checklist (reviewer verifies each is present & store-driven):** name (default
  read-only) · description · leader dropdown incl. `none` for non-default · member grid with
  leaderless single-select + led-squad toggle + leader row disabled · hidden toggle · remove
  (non-default only) · null-squad guard.

- [ ] **Step 2: Router info** — flesh out `renderFleetRouterInfo` to explain the router (spec §10
  one-liner): title = the router squad name, body = `tr("fleet.editor.routerInfo")`, plus a
  read-only note that the router is auto-managed and not editable here. No store mutation.

- [ ] **Step 3: i18n** — add to `web/i18n/en.json` (then fr/es/de translations; `Squad`/`Omnis`
  untranslated):
  - `fleet.editor.selectPrompt` = "Select a squad or agent to edit."
  - `fleet.editor.unsaved` = "Unsaved changes"
  - `fleet.editor.save` = "Save"
  - `fleet.editor.discard` = "Discard"
  - `fleet.editor.hidden` = "Hidden squad"
  - `fleet.editor.hiddenHint` = "Hidden squads are never offered in the picker or as a routing target."
  - `fleet.editor.routerInfo` = "The router reads each request and hands control to the best squad. It is managed automatically and is not edited here."
  Run: `make i18n`.

- [ ] **Step 4: Smoke** — select a squad: change description/leader/members, toggle hidden →
  action bar reveals (dirty); toggling leaderless enforces single-member; default squad's name is
  read-only; `node --test web/fleet/store.test.js` still PASS.

- [ ] **Step 5: Commit**

```bash
git add web/settings.js web/i18n/*.json
git commit -m "feat(fleet): fresh squad editor (with hidden toggle) + router info"
```

---

## Task 5: Agent editor (fresh) — identity, model, tools, parallelism, sessions

**Files:**
- Modify: `web/settings.js`
- Verify: live smoke.

**Interfaces:**
- Consumes: `store.agent(name)`, `store.model()`, `store.removeAgent`, `store.touch`; existing
  constants `TOOL_GROUPS`, `TOOL_MUTEX`, `TOOL_ICONS`, `TOOL_DISPLAY`; `isBuiltinAgent`,
  `state.builtinAgents`, `state.parsed.models`. Reads `modelOptions` from
  `Object.keys(state.parsed.models?.value?.models || {})`.
- Produces: `renderFleetAgentEditor(host, store, name)` scaffold + the first field groups. It
  appends sections into a shared `body` element (Task 6 appends the rest, so structure the
  function to build `body` once and return/hold it for Task 6 continuation — implement the whole
  function in this task with Task-6 sections stubbed as clearly-marked TODO comments the Task-6
  implementer fills, OR build the complete section list now and let Task 6 replace the trailing
  stubs; either is acceptable as long as Task 5 leaves the editor mountable).

- [ ] **Step 1: Implement the scaffold + these sections** — replace the Task-3 stub. Port from
  `renderAgentDetail` ([settings.js:3527-3898](../../../web/settings.js#L3527)) with the transform
  **`a.<field> = v; markFormDirty("agent")` → `a.<field> = v; store.touch()`** (mutate the draft
  agent object from `store.agent(name)`; `store.touch()` triggers the pane's tree/action repaint):
  - `const a = store.agent(name);` guard null → empty prompt.
  - `const model = store.model();` `const isLeader = a.name === "leader";` `const isBuiltin = isBuiltinAgent(a.name);`
    `const builtinDefaults = (state.builtinAgents && state.builtinAgents[a.name]) || null;`
    `const onChange = () => store.touch();`
    `const modelOptions = Object.keys(state.parsed.models?.value?.models || {});`
  - **Title bar** (port lines 3540-3578): name + source badge + LIVE badge; **enabled toggle**
    (`a.enabled`, leader locked on) → set `a.enabled` + `onChange()`; **remove link** (non-builtin)
    → `appConfirm(set.confirm.removeAgent)` then `store.removeAgent(a.name)` (the pane's onChange
    repaints the tree; clear the editor host). Drop the fleet-list dot-update side effects (the
    tree repaints via onChange).
  - **General Settings** (port 3583-3650): display name (`a.name`, leader disabled; on input update
    the title `<h2>` + `onChange`), **model_ref** dropdown from `modelOptions` (+ `recommended_model`
    hint if present). Drop the `updateFleetModelLine`/fleet-item DOM pokes — the tree repaints.
  - **Available Tools** (port 3652-3772): the `TOOL_GROUPS` grid with `TOOL_MUTEX` peer-clearing,
    the disabled gating (`serpapi`↔`d.serpapi_key`, `serper`↔`d.serper_key`, `code_search`↔
    embedder configured via `d.embed_model_ref || state.parsed.models?.value?.embed_model_ref`),
    the `Skill`/`mcp` section-reveal (toggling `.section-inactive` on the skills/mcp sections built
    in Task 6 — reference those section elements), and the two **feature cards** (`leader` flag,
    locked for the "leader" agent; `allow_file_attachments`). Assign `a.tools = Array.from(cur)`
    + `onChange()`. Where `d` was the parsed root, use `store.draft()` (same object shape:
    `serpapi_key`/`serper_key`/`embed_model_ref` are top-level keys on the draft).
  - **Parallelism** (`max_instances`, port 3774-3847) — hidden for leader/curator; stepper; persist
    only when `> 1` else `delete a.max_instances`; `onChange()`.
  - **Sessions** (`resumable_sessions`, port 3849-3897) — hidden for leader/curator; toggle;
    persist only `false` else `delete a.resumable_sessions`; `onChange()`.
  - Append all sections to `body`; `host.innerHTML = ""; host.appendChild(titleBar); host.appendChild(body);`.
  - Leave a clearly-marked insertion point comment `// [TASK 6] team · skills · mcp · a2a · instruction · advanced` where Task 6 appends the remaining sections.

  **Coverage checklist (reviewer):** title/name/source/LIVE · enabled toggle (leader locked) ·
  remove (non-builtin, via `store.removeAgent`) · display name (leader disabled) · model_ref +
  recommended hint · tools grid with MUTEX + serpapi/serper/code_search gating + Skill/mcp reveal ·
  leader & allow_file_attachments feature cards · max_instances (hidden leader/curator, persist >1)
  · resumable_sessions (hidden leader/curator, persist false). Every write goes through
  `store.touch()` (no `markFormDirty`).

- [ ] **Step 2: Smoke** — select a leader and a sub-agent: toggling a tool, changing the model,
  flipping enabled marks the pane dirty (action bar shows); `max_instances`/`resumable_sessions`
  are hidden for the leader and curator; no console errors; `node --test` still PASS.

- [ ] **Step 3: Commit**

```bash
git add web/settings.js
git commit -m "feat(fleet): fresh agent editor — identity, model, tools, parallelism, sessions"
```

---

## Task 6: Agent editor — team, skills, MCP, A2A, instruction, advanced

**Files:**
- Modify: `web/settings.js`
- Verify: live smoke + full round-trip.

**Interfaces:**
- Consumes: `store.agent`, `store.draft`, `store.touch`; the already-modular leaf widgets
  `renderAgentTeamBlock(container, agent, allAgents, onChange)`,
  `populateAgentSkillBlock(container, agent, hasSkillTool, onChange)`,
  `populateAgentMCPBlock(container, agent, hasMcpTool, onChange)`,
  `populateAgentA2ABlock(container, agent, onChange)`.
- Produces: the remaining sections appended at the Task-5 insertion point, completing
  `renderFleetAgentEditor`.

- [ ] **Step 1: Append the remaining sections** at the Task-5 insertion point. **Compose** the
  existing leaf widgets (do NOT reimplement them — this preserves the cycle-safe Team picker and
  the MCP/A2A pickers exactly). Pass `store.agent(name)` as the agent and `() => store.touch()` as
  `onChange`; the widgets mutate the draft object in place, and `store.touch()` repaints the pane:
  - **Team (subagents)** — offered for every agent except curator (port the guard from
    [settings.js:3909](../../../web/settings.js#L3909)). Build a `section` + header
    (`tr("set.hdr.team")`) and call
    `renderAgentTeamBlock(teamBody, a, store.draft().agents, () => store.touch())`. Cycle
    exclusion + persist-clean `subagents` come from the widget.
  - **Skills** — section (`section-inactive` when `Skill` not in `a.tools`); call
    `populateAgentSkillBlock(skillsBody, a, (a.tools||[]).includes("Skill"), () => store.touch())`.
    Hold a reference to this section so the Task-5 tools grid's `Skill` toggle can
    `classList.toggle("section-inactive", …)` it (wire the reference through a closure or a
    `host`-scoped variable shared with Task 5's grid — implement Task 5's Skill/mcp reveal to
    query `host.querySelector(".fleet-skills-section")` / `.fleet-mcp-section` classes added here).
  - **MCP** — section (`section-inactive` when `mcp` not in `a.tools`, class `fleet-mcp-section`);
    call `populateAgentMCPBlock(mcpBody, a, (a.tools||[]).includes("mcp"), () => store.touch())`.
  - **A2A** — section; call `populateAgentA2ABlock(a2aBody, a, () => store.touch())`.
  - **Instruction Set** (port 3963-4038): **Public Description** (builtin → read-only from
    `builtinDefaults.description`; else input → `a.description` + `onChange`) and **System
    Instructions** (builtin → read-only from `builtinDefaults.instruction`; else textarea →
    `a.instruction` + `onChange`) with the **token estimate** `Math.round(len/4)`
    (`tr("set.agent.tokensUsed",{count})`) updating on input.
  - **Advanced paths** (collapsible `<details>`, port 4040-4061): exactly the three keys
    `softskills_dir`, `mcp_config_path`, `permissions_config_path` — text input each → `a[key]` +
    `onChange` (leader placeholder `tr("set.default")` when empty).
  - **Do NOT port** the up/down reorder block (lines 4063-4086) — reorder is a Phase-5 composition
    action; omit it in Phase 2.

  **Coverage checklist (reviewer):** Team via `renderAgentTeamBlock` (curator excluded, cycle
  exclusion intact) · Skills via `populateAgentSkillBlock` (reveal wired to the tools grid) · MCP
  via `populateAgentMCPBlock` (reveal wired) · A2A via `populateAgentA2ABlock` · description +
  instruction (builtin read-only) + token estimate · advanced paths (3 keys) · **no reorder
  block**. All `onChange` route to `store.touch()`.

- [ ] **Step 2: Smoke — full field coverage + round-trip** — open a delegating agent (e.g.
  `research_critic` or `leader`): the Team grid excludes self/curator/cycle-creating agents;
  toggling a team member / skill / mcp server marks dirty; a builtin agent shows description +
  instruction read-only. In the browser console assert the store round-trips:
  ```js
  // before any edit, on the live pane's store (exposed via a temporary debug hook if needed):
  // JSON of serialize() deep-equals the loaded /api/config/parsed/agent data.
  ```
  `node --test web/fleet/store.test.js` still PASS.

- [ ] **Step 3: Commit**

```bash
git add web/settings.js
git commit -m "feat(fleet): agent editor — team, skills, mcp, a2a, instruction, advanced"
```

---

## Task 7: Save engine glue + reload + i18n/docs + full smoke

**Files:**
- Modify: `web/settings.js`, `web/i18n/{en,fr,es,de}.json`, `web/docs/19-agents.md`, `CLAUDE.md`,
  `web/index.html`
- Verify: full select → edit → save → hot-reload smoke.

**Interfaces:**
- Consumes: `store.serialize`, `store.commit`, `store.dirty`; existing `prepareForSave`,
  `authHeaders`, `errText`, `deepClone`, `doReload`, `showBanner`, `setStatus`, `state.parsed`.
- Produces: the real `saveFleet(store, after)`.

- [ ] **Step 1: Implement `saveFleet`** — replace the Task-3 temporary body:

```js
  // Save the Fleet draft through the SAME route + prepare step the classic editor
  // uses (server does the per-agent fan-out + layered delta-write), then adopt the
  // saved value as the new base and hot-reload the fleet.
  async function saveFleet(store, after) {
    if (!store.dirty()) return;
    setStatus(tr("set.status.saving"));
    try {
      const p = state.parsed["agent"];
      const payload = prepareForSave("agent", store.serialize()); // agent case: identity clone
      const r = await fetch(BASE_PATH + `/api/config/parsed/agent`, {
        method: "PUT",
        headers: authHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify({ data: payload, mtime: p ? p.mtime : 0 }),
      });
      if (!r.ok) throw new Error(await errText(r));
      const j = await r.json();
      // Keep the classic editor's parsed cache in sync with what we saved.
      if (p) { p.value = deepClone(store.serialize()); p.data = deepClone(p.value); p.mtime = j.mtime; p.dirty = false; }
      store.commit();                 // adopt draft as the new base (dirty → false)
      delete state.raw["agent"];      // invalidate raw view cache
      setStatus(tr("set.status.savedReload"), "success");
      showBanner(false);              // agent.json never carries the embedder identity → no restart
      await doReload();               // hot-reload so the edit takes effect
      if (typeof after === "function") after();
    } catch (e) {
      setStatus(tr("set.status.saveFailed", { error: e.message }), "error");
    }
  }
```

  Notes: (1) `prepareForSave("agent", …)` is the existing no-op-for-agent path — **reuse it, do
  not reimplement** stripping. (2) The mtime comes from the shared `state.parsed["agent"]` (the
  pane was seeded from `d = state.parsed["agent"].value`), so optimistic-concurrency still works.
  (3) `set.status.savedReload` may already exist; if not, add it below.

- [ ] **Step 2: i18n** — ensure `set.status.savedReload` exists (else add
  = "Saved. Reloading to apply."); the Task-4 `fleet.editor.*` keys already cover the rest. Run
  `make i18n`.

- [ ] **Step 3: Docs** — in `web/docs/19-agents.md` extend the Fleet-preview note: the preview is
  now **editable** (select a node → edit → Save reloads the fleet). In `CLAUDE.md`, update the
  Fleet-preview bullet (Agent topology / Fleet section) to "read + edit (agent & squad editors,
  store-driven save) behind `agent_toolkit_fleet_preview`."

- [ ] **Step 4: Cache-busters** — bump `settings.js?v=` (and any file touched) in
  `web/index.html`.

- [ ] **Step 5: Full smoke** — with `OMNIS_WEB_DIR="$(pwd)/web"` and the flag on:
  1. Select an agent, change its `model_ref`, Save → toast "Saved…", action bar hides, tree label
     updates, and a fresh `GET /api/config/parsed/agent` shows the change (round-trip).
  2. Select a squad, toggle `hidden` / change members, Save → persists; re-open confirms.
  3. Discard reverts a pending edit and hides the action bar.
  4. Confirm the classic Agents/Squads sub-tabs still open and save (shared `state.parsed.agent`
     stays consistent — no double-dirty, no lost keys).
  5. Flag OFF (`localStorage.removeItem("agent_toolkit_fleet_preview")`, reload) → the Fleet
     sub-tab is gone; classic tabs byte-identical.
  6. `make test` (Go + `node --test`) green; `make fmt`/`make vet` clean (no Go changes expected).

- [ ] **Step 6: Commit**

```bash
git add web/settings.js web/i18n/*.json web/docs/19-agents.md CLAUDE.md web/index.html
git commit -m "feat(fleet): store-driven save (PUT+reload) + docs; Phase 2 complete"
```

---

## Self-Review (author checklist, run before dispatching Task 1)

- **Spec coverage:** §7 agent editor (identity/model/tools/skills/mcp/a2a/team/instruction/
  parallelism/resumable/advanced) → Tasks 5–6; §7 squad editor (name/desc/leader/members/hidden)
  → Task 4; §5 store diff/serialize save engine → Tasks 1–2; §8 Save+reload → Task 7; §11
  round-trip + cycle + squad rules + built-in read-only + hot-reload → Tasks 1,2,4,5,6,7; §13
  golden round-trip + cycle/squad-rule unit tests → Tasks 1–2 (node) + Task 6/7 smoke.
- **Deferred to later phases (documented):** model-editor `↗` slide-over (Phase 4), canvas
  minimap (Phase 3), create/add-member/DnD composition actions (Phase 5), agent reorder,
  extracting editors into `web/fleet/editor.js` (Phase 6 cutover). The agent editor's model field
  is a plain dropdown in Phase 2.
- **Reuse decision (explicit):** the new editors **compose** the already-modular leaf widgets
  (`renderAgentTeamBlock`, `populateAgentSkillBlock`/`MCPBlock`/`A2ABlock`) rather than
  reimplement them — this is the DRY/§11-regression-safe engineering call within the chosen
  "fresh components + new save engine" approach: the shells, node dispatch, store save engine, and
  tree selection are new; the leaf field-widgets are reused so cycle exclusion / MCP / A2A can't
  regress. Editors are co-located in `settings.js` (not `web/fleet/editor.js`) because they need
  its private helpers/state; extraction is a Phase-6 follow-up.
- **Placeholder scan:** the two Task-3 stubs (`renderFleetSquadEditor`/`renderFleetAgentEditor`
  and `saveFleet`) are explicitly replaced in Tasks 4/5/6 and 7 — not left as placeholders.
- **Type consistency:** node ref `{type,name}` shape is identical in tree.js, `renderFleetPane`,
  and dispatch; store method names match between Tasks 1–2 and their call sites in Tasks 3–7.
