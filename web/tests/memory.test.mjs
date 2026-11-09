// Memory tool calls render as a compact chip (save / update / delete), never
// for a read, and as a normal error tool block when they fail.
import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { isMemoryTool, memoryChipLabel } = require("../memory-ui.js");

test("memory tools are recognised", () => {
  for (const n of ["memory_save", "memory_read", "memory_delete"]) assert.equal(isMemoryTool(n), true);
  for (const n of ["memory", "Write", "todo_write", ""]) assert.equal(isMemoryTool(n), false);
});

test("chip label follows the result", () => {
  assert.deepEqual(memoryChipLabel("memory_save", { ok: true, name: "tabs", status: "created" }), { key: "chat.memory.saved", vars: { name: "tabs" } });
  assert.deepEqual(memoryChipLabel("memory_save", { ok: true, name: "tabs", status: "updated" }), { key: "chat.memory.updated", vars: { name: "tabs" } });
  assert.deepEqual(memoryChipLabel("memory_delete", { ok: true, name: "tabs" }), { key: "chat.memory.deleted", vars: { name: "tabs" } });
  assert.equal(memoryChipLabel("memory_save", { ok: false, error: "x" }), null);  // a failure renders as a normal error tool block
  assert.equal(memoryChipLabel("memory_read", { found: true }), null);            // reads show nothing
});

// A replayed turn gets the recorded result back from the server; the live
// tool_result carries an object. Both must give the same chip.
test("a recorded result renders the same chip as the live one", () => {
  const { memoryResult } = require("../memory-ui.js");
  const live = { ok: true, name: "tabs", status: "updated" };
  assert.deepEqual(memoryResult(live), live);
  assert.deepEqual(memoryChipLabel("memory_save", memoryResult(JSON.stringify(live))), { key: "chat.memory.updated", vars: { name: "tabs" } });
  assert.equal(memoryResult("not json…"), "not json…");  // a truncated record stays as it was (and renders as a failure)
  assert.equal(memoryChipLabel("memory_save", memoryResult("not json…")), null);
});
