"use strict";
const { test } = require("node:test");
const assert = require("node:assert");
const FleetStore = require("./store.js");

test("module loads and exposes build/serialize", () => {
  assert.strictEqual(typeof FleetStore.build, "function");
  assert.strictEqual(typeof FleetStore.serialize, "function");
});

const SAMPLE = {
  router_squad: "omnis",
  agents: [
    { name: "omnis", leader: false },
    { name: "coder", leader: true, subagents: ["code_scout", "reviewer"] },
    { name: "code_scout" }, { name: "reviewer" },
    { name: "helper" }, { name: "summariser" },
  ],
  squads: [
    { name: "omnis", leader: "none", members: ["omnis"] },
    { name: "Coding", leader: "coder", members: ["summariser"] },
    { name: "Kubernetes", leader: "coder", members: ["summariser"] },
    { name: "Helper", leader: "none", members: ["helper"] },
  ],
};

test("build indexes agents case-insensitively without mutating input", () => {
  const cfg = JSON.parse(JSON.stringify(SAMPLE));
  const m = FleetStore.build(cfg);
  assert.strictEqual(m.agentByName.get("coder").name, "coder");
  assert.strictEqual(m.agents.length, 6);
  assert.deepStrictEqual(cfg, SAMPLE); // not mutated
});

test("build classifies squad kinds and marks the router", () => {
  const m = FleetStore.build(JSON.parse(JSON.stringify(SAMPLE)));
  const byName = Object.fromEntries(m.squads.map(s => [s.name, s]));
  assert.strictEqual(m.routerSquadName, "omnis");
  assert.strictEqual(byName["omnis"].kind, "router");
  assert.strictEqual(byName["Helper"].kind, "leaderless");
  assert.strictEqual(byName["Coding"].kind, "squad");
});

test("sharedCounts counts non-router squad memberships", () => {
  const m = FleetStore.build(JSON.parse(JSON.stringify(SAMPLE)));
  const sc = FleetStore.sharedCounts(m);
  assert.strictEqual(sc.get("summariser"), 2); // Coding + Kubernetes
  assert.strictEqual(sc.get("coder"), 2);       // leads Coding + Kubernetes
  assert.strictEqual(sc.get("helper") || 0, 1);
});

test("unusedAgents excludes leaders, members, and subagents", () => {
  const m = FleetStore.build(JSON.parse(JSON.stringify(SAMPLE)));
  const names = FleetStore.unusedAgents(m).map(a => a.name);
  // code_scout + reviewer are coder's subagents; omnis is router member;
  // coder/summariser/helper are in squads → nothing is unused here.
  assert.deepStrictEqual(names, []);
});

test("treeNodes nests subagents under a leader member", () => {
  const m = FleetStore.build(JSON.parse(JSON.stringify(SAMPLE)));
  const nodes = FleetStore.treeNodes(m);
  const kinds = nodes.map(n => n.kind);
  assert.ok(kinds.includes("router"));
  assert.ok(kinds.includes("leader"));
  assert.ok(kinds.includes("subagent")); // coder's code_scout/reviewer
  const scout = nodes.find(n => n.name === "code_scout" && n.kind === "subagent");
  assert.ok(scout && scout.depth >= 2); // leader=1, its sub-agents nest at depth 2
});

test("serialize(build(cfg)) round-trips losslessly incl. unknown keys", () => {
  const cfg = {
    router_squad: "omnis",
    agents: [
      { name: "coder", leader: true, model_ref: "premium", subagents: ["scout"],
        max_instances: 1, resumable_sessions: false, max_tool_calls: 40, weird_key: { a: 1 } },
      { name: "scout" },
    ],
    squads: [
      { name: "omnis", leader: "none", members: ["coder"] },
      { name: "Coding", leader: "coder", members: [], hidden: false, description: "x", extra: 7 },
      { name: "SS", leader: "none", members: ["scout"], hidden: true },
    ],
  };
  const round = FleetStore.serialize(FleetStore.build(JSON.parse(JSON.stringify(cfg))));
  assert.deepStrictEqual(round, cfg);
});

test("wouldCreateCycle detects direct and transitive cycles", () => {
  const m = FleetStore.build({ agents: [
    { name: "a", subagents: ["b"] }, { name: "b", subagents: ["c"] }, { name: "c" },
  ], squads: [] });
  assert.strictEqual(FleetStore.wouldCreateCycle(m, "c", "a"), true);  // c→a closes a→b→c→a
  assert.strictEqual(FleetStore.wouldCreateCycle(m, "a", "a"), true);  // self
  assert.strictEqual(FleetStore.wouldCreateCycle(m, "c", "b"), true);  // c→b closes b→c→b
  assert.strictEqual(FleetStore.wouldCreateCycle(m, "a", "c"), false); // already there, no new cycle
});

test("validateSquad enforces leaderless/leader rules", () => {
  assert.strictEqual(FleetStore.validateSquad({ name: "x", leader: "", members: ["a"] }).ok, true);
  assert.strictEqual(FleetStore.validateSquad({ name: "x", leader: "", members: ["a","b"] }).ok, false);
  assert.strictEqual(FleetStore.validateSquad({ name: "x", leader: "L", members: ["a","b"] }).ok, true);
  assert.strictEqual(FleetStore.validateSquad({ name: "", leader: "L", members: [] }).ok, false);
});

test("unusedAgents returns enabled agents in no squad and not on any team", () => {
  const cfg = {
    agents: [
      { name: "leader", leader: true },
      { name: "orphan" },
      { name: "disabled_orphan", enabled: false },
    ],
    squads: [{ name: "S", leader: "leader", members: [] }],
  };
  const m = FleetStore.build(cfg);
  const names = FleetStore.unusedAgents(m).map(a => a.name);
  // leader is a squad leader (used); disabled_orphan is enabled:false (excluded);
  // only the enabled, un-squadded "orphan" is unused.
  assert.deepStrictEqual(names, ["orphan"]);
});

const S = FleetStore;

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
  // The default squad name is server-owned (DefaultSquadName = "system"); the
  // store must protect that name, NOT the literal "default".
  const st = S.create(cfg());
  const before = st.serialize().squads.length;
  st.removeSquad("system");                 // "system" IS the default squad
  assert.strictEqual(st.serialize().squads.length, before);
});

test("isDefaultSquad honours the server-provided default name; defaults to 'system'", () => {
  const st = S.create(cfg());
  assert.strictEqual(st.isDefaultSquad("system"), true);   // fallback const
  assert.strictEqual(st.isDefaultSquad("SYSTEM"), true);   // case-insensitive
  assert.strictEqual(st.isDefaultSquad("default"), false); // the old wrong name
  const st2 = S.create(cfg(), { defaultSquad: "core" });   // server override
  assert.strictEqual(st2.isDefaultSquad("core"), true);
  assert.strictEqual(st2.isDefaultSquad("system"), false);
});

test("removeAgent drops the agent entry", () => {
  const st = S.create(cfg());
  st.removeAgent("scout");
  assert.ok(!st.serialize().agents.some(a => a.name === "scout"));
});

test("dirty() ignores render-injected empty list keys (M7 guard-safety)", () => {
  const st = S.create(cfg());
  assert.strictEqual(st.dirty(), false);
  // The agent editors inject skills/subagents/mcp_servers/a2a_agents = [] into an
  // agent that omits the key, at render, WITHOUT a real edit. That must not make
  // the store dirty — the dirty-guarded slide-overs read dirty() directly.
  const scout = st.agent("scout"); // omits all four keys in cfg()
  scout.skills = []; scout.subagents = []; scout.mcp_servers = []; scout.a2a_agents = [];
  assert.strictEqual(st.dirty(), false, "injected empty list keys must not mark dirty");
  // serialize() is NOT normalized — the empty arrays still round-trip on save.
  const savedScout = st.serialize().agents.find(a => a.name === "scout");
  assert.deepStrictEqual(savedScout.skills, []);
});

test("dirty() detects a real (non-empty) list edit", () => {
  const st = S.create(cfg());
  st.agent("scout").skills = ["review"]; // a genuine addition
  assert.strictEqual(st.dirty(), true);
});

test("dirty() detects emptying a populated list (real removal)", () => {
  const st = S.create(cfg());
  // base leader.subagents = ["scout"]; clearing it to [] is a real edit.
  st.agent("leader").subagents = [];
  assert.strictEqual(st.dirty(), true);
});

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

test("addMember refuses the leader without injecting a members key or dirtying", () => {
  const s = FleetStore.create({ agents: [{ name: "leader", leader: true }],
    squads: [{ name: "S", leader: "leader" }] }, { defaultSquad: "system" });
  assert.ok(!s.dirty());
  s.addMember("S", "leader");                 // leader can't be its own member → refused
  assert.ok(!s.dirty());                      // nothing changed
  assert.ok(!("members" in s.squad("S")));    // no spurious members:[] injected
});

test("addMember on an already-present member is a clean no-op (not dirty)", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s.addMember("Coding", "summariser");        // summariser already leads-under/is a member of Coding
  assert.ok(!s.dirty());
});

test("reorderMember ignores non-integer indices", () => {
  const cfg = JSON.parse(JSON.stringify(SAMPLE));
  cfg.squads.find(sq => sq.name === "Coding").members = ["a", "b", "c"];
  const s = FleetStore.create(cfg, { defaultSquad: "system" });
  s.reorderMember("Coding", NaN, 1);
  assert.deepStrictEqual(s.squad("Coding").members, ["a", "b", "c"]);
});

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

test("treeNodes exposes each member node's index within its squad's members", () => {
  const cfg = JSON.parse(JSON.stringify(SAMPLE));
  cfg.squads.find(sq => sq.name === "Coding").members = ["a", "b", "c"];
  const m = FleetStore.build(cfg);
  const nodes = FleetStore.treeNodes(m);
  const a = nodes.find(n => n.kind === "member" && n.name === "a" && n.squadName === "Coding");
  const b = nodes.find(n => n.kind === "member" && n.name === "b" && n.squadName === "Coding");
  const c = nodes.find(n => n.kind === "member" && n.name === "c" && n.squadName === "Coding");
  assert.strictEqual(a.memberIdx, 0);
  assert.strictEqual(b.memberIdx, 1);
  assert.strictEqual(c.memberIdx, 2);
});

test("removeAgent scrubs the deleted name from squad members and subagents", () => {
  const cfg = JSON.parse(JSON.stringify(SAMPLE));
  const s = FleetStore.create(cfg, { defaultSquad: "system" });
  s.removeAgent("summariser");                     // member of Coding + Kubernetes
  assert.ok(!s.squad("Coding").members.includes("summariser"));
  assert.ok(!s.squad("Kubernetes").members.includes("summariser"));
  s.removeAgent("code_scout");                     // a subagent of coder
  assert.ok(!(s.agent("coder").subagents || []).includes("code_scout"));
});

test("validate() passes a clean config and flags each brick condition", () => {
  const s = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  assert.strictEqual(s.validate().ok, true);

  // member references a removed agent
  const s2 = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s2.squad("Coding").members.push("ghost");
  let v = s2.validate();
  assert.strictEqual(v.ok, false);
  assert.ok(v.errors.some(e => e.code === "member-missing" && e.agent === "ghost"));

  // disabled member
  const s3 = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s3.setEnabled("summariser", false);
  assert.ok(s3.validate().errors.some(e => e.code === "member-disabled" && e.agent === "summariser"));

  // duplicate squad name
  const s4 = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s4.draft().squads.push({ name: "Coding", leader: "coder", members: [] });
  assert.ok(s4.validate().errors.some(e => e.code === "dup-squad"));

  // subagent references a removed agent
  const s5 = FleetStore.create(JSON.parse(JSON.stringify(SAMPLE)), { defaultSquad: "system" });
  s5.agent("coder").subagents = ["reviewer", "ghost"];
  assert.ok(s5.validate().errors.some(e => e.code === "subagent-missing" && e.sub === "ghost"));
});
