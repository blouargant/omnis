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
