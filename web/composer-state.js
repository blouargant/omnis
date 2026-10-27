// composerButtonMode decides what the composer's single round button does:
//   "send"          — no turn in flight (or an archived session; the caller
//                     disables the button there);
//   "steer"         — a turn is running and the composer has text: the text is
//                     a mid-turn steering note;
//   "stop"          — a turn THIS browser streams is running, composer empty;
//   "stop-disabled" — a background/remote turn is running, composer empty: it
//                     has no client-side handle to cancel, so Stop is shown but
//                     inert.
// Loaded before app.js (window global) and by the node tests (module.exports).
function composerButtonMode({ hasText, localActive, busy, archived }) {
  if (archived || !busy) return "send";
  if (hasText) return "steer";
  return localActive ? "stop" : "stop-disabled";
}

if (typeof module !== "undefined" && module.exports) module.exports = { composerButtonMode };
else window.composerButtonMode = composerButtonMode;
