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

if (typeof module !== "undefined" && module.exports) module.exports = { isMemoryTool, memoryResult, memoryToolFailed, memoryChipLabel };
else { window.isMemoryTool = isMemoryTool; window.memoryResult = memoryResult; window.memoryToolFailed = memoryToolFailed; window.memoryChipLabel = memoryChipLabel; }
