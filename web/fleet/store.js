"use strict";
// FleetStore — pure, DOM-free topology model for the Fleet settings view.
// Dual-export: CommonJS (node --test) + browser global (window.FleetStore).
(function (root, factory) {
  const mod = factory();
  if (typeof module !== "undefined" && module.exports) module.exports = mod;
  if (typeof window !== "undefined") window.FleetStore = mod;
})(this, function () {
  function lc(s) { return String(s || "").toLowerCase(); }

  function build(cfg) {
    const agents = Array.isArray(cfg.agents) ? cfg.agents.slice() : [];
    const rawSquads = Array.isArray(cfg.squads) ? cfg.squads : [];
    const agentByName = new Map();
    agents.forEach(a => { if (a && a.name) agentByName.set(lc(a.name), a); });

    const routerSquadName = cfg.router_squad && lc(cfg.router_squad) !== "none"
      ? cfg.router_squad
      : (cfg.router_squad === undefined ? "omnis" : null);

    const squads = rawSquads.map(sq => {
      const leader = sq.leader === undefined ? "" : sq.leader;
      const leaderless = !leader || lc(leader) === "none";
      const isRouter = routerSquadName && lc(sq.name) === lc(routerSquadName);
      return {
        name: sq.name || "",
        leader: leaderless ? "" : leader,
        members: Array.isArray(sq.members) ? sq.members.slice() : [],
        hidden: !!sq.hidden,
        description: sq.description || "",
        kind: isRouter ? "router" : (leaderless ? "leaderless" : "squad"),
      };
    });

    return { agents, agentByName, squads, routerSquadName, raw: cfg };
  }

  function sharedCounts(model) {
    const counts = new Map();
    const bump = n => { const k = lc(n); if (k) counts.set(k, (counts.get(k) || 0) + 1); };
    model.squads.forEach(sq => {
      if (sq.kind === "router") return;
      if (sq.leader) bump(sq.leader);
      sq.members.forEach(bump);
    });
    return counts;
  }

  function subagentNames(model, agent) {
    return Array.isArray(agent && agent.subagents) ? agent.subagents : [];
  }

  function collectUsed(model) {
    const used = new Set();
    model.squads.forEach(sq => {
      if (sq.leader) used.add(lc(sq.leader));
      sq.members.forEach(n => used.add(lc(n)));
    });
    // subagents (transitive) of any agent are "used"
    model.agents.forEach(a => subagentNames(model, a).forEach(n => used.add(lc(n))));
    return used;
  }

  function unusedAgents(model) {
    const used = collectUsed(model);
    return model.agents.filter(a =>
      a && a.name && a.enabled !== false && !used.has(lc(a.name)));
  }

  function pushSubtree(model, name, depth, seen, out, squadName) {
    const key = lc(name);
    if (seen.has(key)) return;           // cycle / repeat guard within one branch
    seen.add(key);
    const agent = model.agentByName.get(key);
    subagentNames(model, agent).forEach(sub => {
      out.push({ kind: "subagent", name: sub, agent: model.agentByName.get(lc(sub)), depth, parentAgent: name, squadName });
      pushSubtree(model, sub, depth + 1, seen, out, squadName);
    });
    seen.delete(key);
  }

  function treeNodes(model) {
    const out = [];
    const shared = sharedCounts(model);
    const orderedSquads = model.squads.slice().sort((a, b) => {
      if (a.kind === "router") return -1;
      if (b.kind === "router") return 1;
      return 0;
    });
    orderedSquads.forEach(sq => {
      out.push({ kind: sq.kind === "leaderless" ? "leaderless" : sq.kind === "router" ? "router" : "squad", name: sq.name, squad: sq, depth: 0 });
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
      sq.members.forEach((m, mi) => {
        out.push({ kind: "member", name: m, agent: model.agentByName.get(lc(m)), depth: 1, shared: shared.get(lc(m)) || 0, squadName: sq.name, memberIdx: mi });
        pushSubtree(model, m, 2, new Set(), out, sq.name);
      });
    });
    const unused = unusedAgents(model);
    if (unused.length) {
      out.push({ kind: "unused-header", name: "", depth: 0 });
      unused.forEach(a => out.push({ kind: "unused", name: a.name, agent: a, depth: 1 }));
    }
    return out;
  }

  function serialize(model) {
    // build() never mutates cfg and stores it as model.raw; the SquadView is a
    // derived read model. The canonical on-disk shape is exactly model.raw, so
    // round-trip is structural identity. (Phase 2 replaces this with a real
    // diff against edits; keeping identity here locks the contract.)
    return model.raw;
  }

  function subagentGraph(model) {
    const g = new Map();
    model.agents.forEach(a => g.set(lc(a.name), subagentNames(model, a).map(lc)));
    return g;
  }

  function wouldCreateCycle(model, fromName, toName) {
    const from = lc(fromName), to = lc(toName);
    if (from === to) return true;
    // Adding from→to creates a cycle iff `from` is reachable from `to`.
    const g = subagentGraph(model);
    const stack = [to], seen = new Set();
    while (stack.length) {
      const cur = stack.pop();
      if (cur === from) return true;
      if (seen.has(cur)) continue;
      seen.add(cur);
      (g.get(cur) || []).forEach(n => stack.push(n));
    }
    return false;
  }

  function validateSquad(sq) {
    const name = String(sq && sq.name || "").trim();
    if (!name) return { ok: false, error: "empty-name" };
    const leaderless = !sq.leader || lc(sq.leader) === "none";
    const members = Array.isArray(sq.members) ? sq.members : [];
    if (leaderless && members.length !== 1) return { ok: false, error: "leaderless-one-member" };
    if (!leaderless && members.length >= 2 && !sq.leader) return { ok: false, error: "needs-leader" };
    return { ok: true };
  }

  function clone(x) { return JSON.parse(JSON.stringify(x == null ? null : x)); }

  function uniqueName(existing, base) {
    const taken = new Set((existing || []).map(lc));
    if (!taken.has(lc(base))) return base;
    for (let i = 2; ; i++) { const cand = `${base}-${i}`; if (!taken.has(lc(cand))) return cand; }
  }

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

  // Render-time normalization for the dirty() comparison. Several agent
  // sub-editors inject an empty list (`skills`/`subagents`/`mcp_servers`/
  // `a2a_agents = []`) into an agent that OMITS the key — a pure normalization
  // done at render, WITHOUT marking a real edit (no store.touch()). The Fleet
  // store's structural dirty() (draft vs base) would otherwise over-report as
  // dirty after merely SELECTING an agent, and the dirty-guarded slide-overs
  // (Models / Registries) read dirty() directly, so they would block spuriously.
  // Treat an absent list key and an empty array as equal so dirty() reflects
  // only real user edits. serialize() is deliberately NOT normalized — the empty
  // arrays still round-trip on save, exactly as the classic editor writes them.
  const NORMALIZE_LIST_KEYS = ["skills", "subagents", "mcp_servers", "a2a_agents"];
  function normalizeForCompare(cfg) {
    const c = clone(cfg || {});
    if (Array.isArray(c.agents)) {
      for (const a of c.agents) {
        if (a && typeof a === "object") {
          for (const k of NORMALIZE_LIST_KEYS) {
            if (Array.isArray(a[k]) && a[k].length === 0) delete a[k];
          }
        }
      }
    }
    return c;
  }

  function create(cfg, opts) {
    let base = clone(cfg || {});
    let draft = clone(base);
    const listeners = [];
    // The protected default squad name is server-owned (agent.DefaultSquadName).
    // The surface passes it in via opts.defaultSquad (learned from
    // GET /api/squads `default_squad`); we fall back to the current const value
    // so the pure module + node tests work standalone. Do NOT hardcode "default"
    // here — the server renamed it to "system", and drift is the exact bug this fixes.
    const defaultSquadName = lc((opts && opts.defaultSquad) || "system");
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
      dirty() { return !deepEqual(normalizeForCompare(draft), normalizeForCompare(base)); },
      serialize() { return draft; },
      commit(saved) { base = clone(saved != null ? saved : draft); draft = clone(base); store.touch(); },
      discard() { draft = clone(base); store.touch(); },
    };

    function leaderlessSquad(sq) { return !sq.leader || lc(sq.leader) === "none"; }
    store.isDefaultSquad = (name) => lc(name) === defaultSquadName;
    store.defaultSquadName = () => defaultSquadName;
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
      // Scrub references so a delete never leaves a DANGLING member/subagent, which
      // the server rejects (→ bricked next boot). The leader case is deliberately
      // left for validate() to surface (a squad can't silently lose its leader).
      (draft.squads || []).forEach(sq => {
        if (Array.isArray(sq.members)) sq.members = sq.members.filter(m => lc(m) !== k);
      });
      (draft.agents || []).forEach(a => {
        if (Array.isArray(a.subagents)) {
          const kept = a.subagents.filter(s => lc(s) !== k);
          if (kept.length) a.subagents = kept; else delete a.subagents;
        }
      });
      store.touch();
    };

    // validate() mirrors the server's resolveSquadEntries + validateSubAgentGraph
    // (agent/runtime_config.go) so saveFleet never persists a config
    // ResolveRuntimeSettings would reject — which would brick the next boot (the
    // Fleet Save path PUTs + commits before the reload validates). Pure, no
    // mutation. An ABSENT default squad is NOT an error — the server synthesises
    // one — so there is deliberately no default-squad check here.
    store.validate = () => {
      const errors = [];
      const agents = draft.agents || [];
      const byName = new Map();
      agents.forEach(a => { if (a && a.name) byName.set(lc(a.name), a); });
      const enabled = a => a && a.enabled !== false;
      const squads = draft.squads || [];
      const seen = new Set();
      squads.forEach(sq => {
        const nm = lc(sq.name || "");
        if (!nm) return;
        if (seen.has(nm)) errors.push({ code: "dup-squad", squad: sq.name });
        seen.add(nm);
        const leaderless = !sq.leader || lc(sq.leader) === "none";
        const members = Array.isArray(sq.members) ? sq.members : [];
        if (leaderless) {
          if (members.length !== 1) errors.push({ code: "leaderless-count", squad: sq.name });
        } else {
          const la = byName.get(lc(sq.leader));
          if (!la) errors.push({ code: "leader-missing", squad: sq.name, agent: sq.leader });
          else if (!enabled(la)) errors.push({ code: "leader-disabled", squad: sq.name, agent: sq.leader });
        }
        members.forEach(m => {
          const ma = byName.get(lc(m));
          if (!ma) errors.push({ code: "member-missing", squad: sq.name, agent: m });
          else if (!enabled(ma)) errors.push({ code: "member-disabled", squad: sq.name, agent: m });
        });
      });
      agents.forEach(a => {
        (Array.isArray(a.subagents) ? a.subagents : []).forEach(s => {
          const sa = byName.get(lc(s));
          if (!sa) errors.push({ code: "subagent-missing", agent: a.name, sub: s });
          else if (!enabled(sa)) errors.push({ code: "subagent-disabled", agent: a.name, sub: s });
        });
      });
      return errors.length ? { ok: false, errors } : { ok: true };
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

    store.setEnabled = (agentName, v) => {
      const a = store.agent(agentName); if (!a) return;
      a.enabled = !!v; store.touch();
    };
    store.addMember = (squadName, agentName) => {
      const sq = store.squad(squadName); if (!sq) return;
      if (lc(agentName) === lc(sq.leader || "")) return;          // never list the squad's own leader
      const members = Array.isArray(sq.members) ? sq.members : [];
      if (leaderlessSquad(sq)) {
        if (members.length === 1 && lc(members[0]) === lc(agentName)) return; // already the sole member
        sq.members = [agentName];                                 // leaderless = exactly one (replace)
      } else {
        if (members.some(m => lc(m) === lc(agentName))) return;   // already a member → no-op
        sq.members = members.concat([agentName]);
      }
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
      if (!Number.isInteger(from) || !Number.isInteger(to) || from < 0 || from >= n || to < 0 || to >= n || from === to) return;
      const [x] = sq.members.splice(from, 1);
      sq.members.splice(to, 0, x);
      store.touch();
    };

    return store;
  }

  return { build, serialize, sharedCounts, unusedAgents, treeNodes, wouldCreateCycle, validateSquad, create };
});
