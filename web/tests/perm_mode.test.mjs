// Session permission mode helpers: the Shift+Tab cycle and the label keys.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";

const require = createRequire(import.meta.url);
const { cycleMode, modeLabelKey, MODES } = require("../perm-mode.js");
const en = JSON.parse(readFileSync(new URL("../i18n/en.json", import.meta.url), "utf8"));

test("cycle with bypass allowed", () => {
  const seq = ["default", "acceptEdits", "plan", "auto", "bypassPermissions", "default"];
  for (let i = 0; i < seq.length - 1; i++) assert.equal(cycleMode(seq[i], true), seq[i + 1]);
});

test("cycle without bypass skips it", () => {
  assert.equal(cycleMode("auto", false), "default");
});

test("dontAsk and unknown modes return to default", () => {
  assert.equal(cycleMode("dontAsk", true), "default");
  assert.equal(cycleMode("bogus", true), "default");
});

test("every mode has a description key in en.json", () => {
  assert.equal(MODES.length, 6);
  for (const m of MODES) assert.ok(en[modeLabelKey(m)], `missing ${modeLabelKey(m)}`);
});
