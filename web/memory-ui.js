// Collection memory in the transcript. The memory tools (memory_save,
// memory_read, memory_delete) never render as tool blocks: a successful save or
// delete shows as a compact chip, a read shows nothing, and a failure renders as
// the normal (error) tool block so it stays visible.
// Loaded before app.js (window globals) and by the node tests (module.exports).

function isMemoryTool(name) {
  return name === "memory_save" || name === "memory_read" || name === "memory_delete";
}

// memoryResult normalises a memory tool result: the live tool_result carries an
// object, and a persisted tool record carries the raw JSON of the response,
// which the server returns as an object too — unless it was stored as a string.
// A JSON string is parsed; anything else is returned unchanged.
function memoryResult(result) {
  if (typeof result !== "string") return result;
  try { return JSON.parse(result); } catch (_) { return result; }
}

// memoryToolFailed reports a memory call that must be shown as an error tool
// block: no result, an error, or a save/delete that did not succeed. A read that
// found nothing is not a failure.
function memoryToolFailed(name, result) {
  if (!result || typeof result !== "object" || result.error) return true;
  if (name === "memory_read") return false;
  return result.ok !== true;
}

// memoryChipLabel returns the chip's i18n key and vars, or null when the call
// shows no chip (a read, or a failure).
function memoryChipLabel(toolName, result) {
  if (toolName === "memory_read" || memoryToolFailed(toolName, result)) return null;
  const vars = { name: result.name || "" };
  if (toolName === "memory_delete") return { key: "chat.memory.deleted", vars };
  if (toolName === "memory_save") {
    return { key: result.status === "updated" ? "chat.memory.updated" : "chat.memory.saved", vars };
  }
  return null;
}

// memoryLastActivity is when the user last said or changed a memory: the later
// of its update date and its last confirmation (YYYY-MM-DD strings compare).
function memoryLastActivity(m) {
  const u = (m && m.updated) || "";
  const c = (m && m.last_confirmed) || "";
  return c > u ? c : u;
}

// MEMORY_STALE_DAYS: a project fact or a reference nobody said or changed for
// this long is flagged for review. Rules and facts about the user do not age.
const MEMORY_STALE_DAYS = 90;

// memoryIsStale reports a project/reference memory whose last activity is more
// than MEMORY_STALE_DAYS before today (a Date, default now).
function memoryIsStale(m, today) {
  if (!m || (m.type !== "project" && m.type !== "reference")) return false;
  const last = memoryLastActivity(m);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(last)) return false;
  const t = today instanceof Date ? today : new Date();
  const ms = Date.UTC(t.getFullYear(), t.getMonth(), t.getDate()) - Date.parse(last + "T00:00:00Z");
  return ms > MEMORY_STALE_DAYS * 86400000;
}

// memoriesByAge orders memories oldest last activity first, ties by name.
function memoriesByAge(list) {
  return [...(list || [])].sort((a, b) => {
    const x = memoryLastActivity(a), y = memoryLastActivity(b);
    if (x !== y) return x < y ? -1 : 1;
    return (a.name || "") < (b.name || "") ? -1 : 1;
  });
}

if (typeof module !== "undefined" && module.exports) module.exports = { isMemoryTool, memoryResult, memoryToolFailed, memoryChipLabel, memoryLastActivity, memoryIsStale, memoriesByAge, MEMORY_STALE_DAYS };
else { window.isMemoryTool = isMemoryTool; window.memoryResult = memoryResult; window.memoryToolFailed = memoryToolFailed; window.memoryChipLabel = memoryChipLabel; window.memoryLastActivity = memoryLastActivity; window.memoryIsStale = memoryIsStale; window.memoriesByAge = memoriesByAge; }
