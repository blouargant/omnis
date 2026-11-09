// Soft-skills, the curator and /learn were removed: the web UI must carry no
// trace of them (no tool, slash command, curation route or i18n string).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const read = (p) => readFileSync(new URL(p, import.meta.url), "utf8");
const webDir = fileURLToPath(new URL("..", import.meta.url));

// cssFiles lists every .css file under web/css, recursively.
function cssFiles(dir) {
  const out = [];
  for (const e of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, e.name);
    if (e.isDirectory()) out.push(...cssFiles(p));
    else if (e.name.endsWith(".css")) out.push(p);
  }
  return out;
}

test("no soft-skill, curator or /learn code in the web UI", () => {
  for (const file of ["../app.js", "../settings.js", "../tool-summary.js"]) {
    const src = read(file);
    for (const needle of ["softskill", "/learn-now", '"/learn"', "/curate"]) {
      assert.equal(src.includes(needle), false, `${file} still contains ${needle}`);
    }
    // Word-bounded and case-insensitive: "curated" is a legitimate word.
    assert.equal(/\bcurator\b/i.test(src), false, `${file} still mentions the curator`);
  }
});

test("no soft-skill in the page markup or stylesheets", () => {
  const files = [join(webDir, "index.html"), ...cssFiles(join(webDir, "css"))];
  for (const file of files) {
    const src = readFileSync(file, "utf8");
    assert.equal(/softskill/i.test(src), false, `${file} still contains softskill`);
  }
});

test("no soft-skill or curation i18n keys", () => {
  const keys = Object.keys(JSON.parse(read("../i18n/en.json")));
  const stale = keys.filter((k) => /softskill|curation/i.test(k));
  assert.deepEqual(stale, []);
});
