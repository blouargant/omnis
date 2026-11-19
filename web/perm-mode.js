// Session permission mode helpers (pure). Loaded before app.js (window.PermMode)
// and by the node tests (module.exports).
const MODES = ["default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"];

// cycleMode is the Shift+Tab order: default → acceptEdits → plan → auto →
// (bypassPermissions when allowed) → default. dontAsk is menu-only; it and any
// unknown value fall back to default. An empty mode (follows config) is treated
// as default by the caller, who passes the effective mode.
function cycleMode(mode, bypassAllowed) {
  switch (mode) {
    case "default": return "acceptEdits";
    case "acceptEdits": return "plan";
    case "plan": return "auto";
    case "auto": return bypassAllowed ? "bypassPermissions" : "default";
    default: return "default";
  }
}

// modeLabelKey is the i18n key of a mode's one-line description.
function modeLabelKey(mode) { return "permmode.desc." + mode; }

const api = { cycleMode, modeLabelKey, MODES };
if (typeof module !== "undefined" && module.exports) module.exports = api;
else window.PermMode = api;
