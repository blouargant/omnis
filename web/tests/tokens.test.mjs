// Guards the design tokens the Claude-style layout relies on: every structural
// token must have a :root default (so a theme that never defines it still
// renders), and the chat partials may only reference tokens that exist.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const web = join(dirname(fileURLToPath(import.meta.url)), "..");
const read = (p) => readFileSync(join(web, p), "utf8");
const common = read("css/features/common.css");

function rootBlock(css) {
  const m = css.match(/:root\s*\{([\s\S]*?)\n\}/);
  assert.ok(m, ":root block not found");
  return m[1];
}

const LAYOUT_TOKENS = [
  "--chat-max-w", "--radius-sm", "--radius-md", "--radius-composer",
  "--user-bubble-bg", "--user-bubble-fg", "--font-ui",
  "--answer-font-size", "--answer-line-height",
];

test("layout tokens have :root defaults", () => {
  const root = rootBlock(common);
  for (const name of LAYOUT_TOKENS) {
    assert.match(root, new RegExp(name + "\\s*:"), `${name} missing from :root`);
  }
});

// Tokens app.js sets at runtime via style.setProperty.
const RUNTIME_TOKENS = ["--ask-card-max-h", "--col-accent", "--composer-h",
  "--composer-overlay-h", "--sidebar-w", "--tip-arrow-x"];

test("chat partials only reference defined tokens", () => {
  const defined = new Set(RUNTIME_TOKENS);
  for (const m of common.matchAll(/(--[\w-]+)\s*:/g)) defined.add(m[1]);
  for (const file of ["css/features/messages.css", "css/features/composer.css"]) {
    const css = read(file);
    const local = new Set([...css.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]));
    for (const m of css.matchAll(/var\((--[\w-]+)\s*([,)])/g)) {
      if (m[2] === ",") continue; // has a fallback
      assert.ok(defined.has(m[1]) || local.has(m[1]), `${file} uses undefined ${m[1]}`);
    }
  }
});
