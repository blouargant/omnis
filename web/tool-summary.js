// Tool runs render in the chat as one quiet summary line (Claude Desktop
// style): "Ran 1 command, read 2 files, 1 failed". toolKind classifies a call
// by tool name; toolSummary turns a run's calls into that sentence through a
// translator t(key, count) (trN in the browser). Loaded before app.js (window
// globals) and by the node tests (module.exports).

const TOOL_KIND_RULES = [
  [/^(bash|bash_background|monitor)$/, "command"],
  [/^(read|mime|read_doc|read_session|lsp_read_symbol|lsp_hover|lsp_document_symbols)$/, "read"],
  [/^(grep|glob|code_search|lsp_.*|ast_grep_search|search_.*|recall_.*|list_.*|browse_registry)$/, "search"],
  [/^write$/, "write"],
  [/^(edit|multiedit|revert|lsp_rename|ast_grep_rewrite|lsp_code_action)$/, "edit"],
  [/^(websearch|webfetch|html_to_markdown)$/, "web"],
  [/^mcp__/, "mcp"],
  [/^(load_skill|load_softskill)$/, "skill"],
];

function toolKind(name, subAgents) {
  const n = String(name || "");
  if (subAgents && subAgents.has(n)) return "delegate";
  const low = n.toLowerCase();
  for (const [re, kind] of TOOL_KIND_RULES) if (re.test(low)) return kind;
  return "other";
}

// toolSummary: calls = [{kind, status: "pending"|"done"|"error"}].
function toolSummary(calls, t) {
  const pending = calls.filter((c) => c.status === "pending");
  if (pending.length) return t("tool.run." + pending[pending.length - 1].kind, 1);
  const counts = new Map();
  let failed = 0;
  for (const c of calls) {
    counts.set(c.kind, (counts.get(c.kind) || 0) + 1);
    if (c.status === "error") failed++;
  }
  const parts = [...counts].map(([kind, n]) => t("tool.sum." + kind, n));
  if (failed) parts.push(t("tool.sum.failed", failed));
  return parts.join(", ");
}

if (typeof module !== "undefined" && module.exports) module.exports = { toolKind, toolSummary };
else { window.toolKind = toolKind; window.toolSummary = toolSummary; }
