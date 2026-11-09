// Soft-skills, the curator and /learn were removed: the web UI must carry no
// trace of them (no tool, slash command, curation route or i18n string).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");

test("no soft-skill, curator or /learn code in the web UI", () => {
  for (const file of ["../app.js", "../settings.js", "../tool-summary.js"]) {
    const src = read(file);
    for (const needle of ["softskill", "/learn-now", '"/learn"', "/curate", "Curator"]) {
      assert.equal(src.includes(needle), false, `${file} still contains ${needle}`);
    }
  }
});

test("no soft-skill or curation i18n keys", () => {
  const keys = Object.keys(JSON.parse(read("../i18n/en.json")));
  const stale = keys.filter((k) => /softskill|curation/i.test(k));
  assert.deepEqual(stale, []);
});
