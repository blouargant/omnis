// Memory age: last activity is the later of update and confirmation; only
// project facts and references go stale; the age sort puts the oldest first.
import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { memoryLastActivity, memoryIsStale, memoriesByAge } = require("../memory-ui.js");

test("last activity counts confirmations", () => {
  assert.equal(memoryLastActivity({ updated: "2026-01-01", last_confirmed: "2026-03-01" }), "2026-03-01");
  assert.equal(memoryLastActivity({ updated: "2026-04-01", last_confirmed: "2026-03-01" }), "2026-04-01");
  assert.equal(memoryLastActivity({ updated: "2026-04-01" }), "2026-04-01");
});

test("only old project facts and references are stale", () => {
  const today = new Date(2026, 10, 17);
  assert.equal(memoryIsStale({ type: "project", updated: "2026-07-01" }, today), true);
  assert.equal(memoryIsStale({ type: "reference", updated: "2026-07-01" }, today), true);
  assert.equal(memoryIsStale({ type: "project", updated: "2026-10-01" }, today), false);
  assert.equal(memoryIsStale({ type: "project", updated: "2026-07-01", last_confirmed: "2026-11-01" }, today), false);
  assert.equal(memoryIsStale({ type: "feedback", updated: "2025-01-01" }, today), false);
  assert.equal(memoryIsStale({ type: "user", updated: "2025-01-01" }, today), false);
});

test("age sort puts the oldest first", () => {
  const l = memoriesByAge([
    { name: "b", updated: "2026-05-01" },
    { name: "a", updated: "2026-02-01", last_confirmed: "2026-09-01" },
    { name: "c", updated: "2026-03-01" },
  ]);
  assert.deepEqual(l.map(m => m.name), ["c", "b", "a"]);
});
