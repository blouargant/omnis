// The composer has ONE round button that sends, stops or steers depending on
// the turn state. composerButtonMode is the single decision point.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { composerButtonMode } = require("../composer-state.js");

const mode = (o) => composerButtonMode({ hasText: false, localActive: false, busy: false, archived: false, ...o });

test("idle composer sends", () => {
  assert.equal(mode({}), "send");
  assert.equal(mode({ hasText: true }), "send");
});

test("local turn: text steers, empty stops", () => {
  assert.equal(mode({ busy: true, localActive: true, hasText: true }), "steer");
  assert.equal(mode({ busy: true, localActive: true }), "stop");
});

test("remote turn: text steers, empty cannot stop", () => {
  assert.equal(mode({ busy: true, hasText: true }), "steer");
  assert.equal(mode({ busy: true }), "stop-disabled");
});

test("archived session short-circuits to send (caller disables)", () => {
  assert.equal(mode({ archived: true, busy: true, localActive: true }), "send");
});
