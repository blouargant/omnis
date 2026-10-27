// Tool runs render as one quiet summary line ("Ran 1 command, used 2 tools").
// toolKind classifies a call; toolSummary builds the sentence from the kinds.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { toolKind, toolSummary } = require("../tool-summary.js");

const fakeT = (k, n) => k.split(".").pop() + ":" + n;

test("toolKind classifies by name", () => {
  assert.equal(toolKind("Bash"), "command");
  assert.equal(toolKind("Read"), "read");
  assert.equal(toolKind("Grep"), "search");
  assert.equal(toolKind("Write"), "write");
  assert.equal(toolKind("Edit"), "edit");
  assert.equal(toolKind("WebFetch"), "web");
  assert.equal(toolKind("mcp__github__list_issues"), "mcp");
  assert.equal(toolKind("load_skill"), "skill");
  assert.equal(toolKind("frobnicate"), "other");
});

test("toolKind recognises sub-agents from the given set", () => {
  assert.equal(toolKind("web_agent", new Set(["web_agent"])), "delegate");
  assert.equal(toolKind("web_agent"), "other");
});

test("toolSummary counts kinds in first-appearance order", () => {
  const calls = [
    { kind: "command", status: "done" },
    { kind: "other", status: "done" },
    { kind: "other", status: "done" },
  ];
  assert.equal(toolSummary(calls, fakeT), "command:1, other:2");
});

test("toolSummary appends the failure count", () => {
  const calls = [{ kind: "command", status: "done" }, { kind: "read", status: "error" }];
  assert.equal(toolSummary(calls, fakeT), "command:1, read:1, failed:1");
});

test("toolSummary shows the action in progress while a call is pending", () => {
  const calls = [{ kind: "command", status: "done" }, { kind: "web", status: "pending" }];
  assert.equal(toolSummary(calls, fakeT), "web:1");
});
