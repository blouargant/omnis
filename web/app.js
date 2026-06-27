// Vanilla-JS client for the omnis HTTP API.
// Uses fetch + ReadableStream to consume SSE (EventSource doesn't allow
// custom headers, so we use fetch with Authorization).

// i18n safety shim: i18n.js loads (deferred) before this file and defines tr/
// trN/I18N, but if it ever fails to load we degrade to English keys instead of
// crashing the whole UI on a module-load ReferenceError.
if (typeof window.tr !== "function") {
  window.tr = (k) => k;
  window.trN = (k) => k;
  window.I18N = { locale: "en", localeStored: true, detectedLocale: null, LOCALES: [], trIn: (l, k) => k, labelFor: (id) => id, setLocale() {}, persistLocale() {}, translateDom() {}, reconcileServerLocale() { return false; } };
}

const TOKEN_KEY = "agent_toolkit_token";

// ─── Debug instrumentation ───────────────────────────────────────────────────
// General-purpose debugging tool for the web UI. Surfaces a small fixed badge
// in the top-right corner of the page with live per-turn metrics:
//
//   [client] ttfb / chunks / chunks-per-sec / bytes / cumulative markdown parse
//   [server] same dimensions reported by the backend (sent as a `debug_timing`
//            SSE event right before `done`)
//
// Enable by appending `?debug=1` to the URL, or by setting
// `localStorage.agent_toolkit_debug = "1"` (persistent across reloads). Disable
// by removing the param / clearing the storage key.
//
// Exposed on `window.AgentDebug` so additional probes can hook in from the
// browser console without touching this file — e.g. `AgentDebug.token(42)` to
// account for an out-of-band chunk. Add new measurements by extending the
// object below and calling `_paint()` after mutating state.
// Per-session per-agent token accumulation. Declared here (before AgentDebug)
// so _paint() can reference it without a temporal dead zone, and so the
// context popup can read it independently of debug mode.
const sessionAgentTokens = new Map(); // sessionId → Map(agentName → {prompt, output, cost})

// Default per-million token prices, used only as a fallback when a turn carries
// no frozen price (legacy turns persisted before per-agent prices were saved).
const DEFAULT_IN_PRICE_PER_M  = 3.0;  // $3  / M input
const DEFAULT_OUT_PRICE_PER_M = 15.0; // $15 / M output
// usageCostUSD prices one usage delta in dollars. `prices` carries the per-million
// rates frozen on the turn (the agent's model rates at turn time): {in, out,
// cacheRead, cacheCreate}; absent (≤0) input/output fall back to the defaults and
// absent cache rates fall back to the input rate. `prompt` is the TOTAL prompt and
// includes the cache tokens, so the fresh (full-rate) input is prompt−read−create.
// Pricing each delta as it arrives — rather than summing tokens and applying one
// rate — keeps the budget correct even across a mid-session model/price change.
// Mirrors the server's TokenUsage.CostUSD (the single source of truth).
function usageCostUSD(prompt, output, cacheRead, cacheCreate, prices) {
  const p = prices || {};
  const inP  = p.in  > 0 ? p.in  : DEFAULT_IN_PRICE_PER_M;
  const outP = p.out > 0 ? p.out : DEFAULT_OUT_PRICE_PER_M;
  const crP  = p.cacheRead   > 0 ? p.cacheRead   : inP;
  const ccP  = p.cacheCreate > 0 ? p.cacheCreate : inP;
  let fresh = (prompt | 0) - (cacheRead | 0) - (cacheCreate | 0);
  if (fresh < 0) fresh = 0;
  return (fresh * inP + (cacheRead | 0) * crP + (cacheCreate | 0) * ccP + (output | 0) * outP) / 1_000_000;
}
const AgentDebug = {
  enabled: new URLSearchParams(location.search).get("debug") === "1"
        || localStorage.getItem("agent_toolkit_debug") === "1",
  badge: null,
  tStart: 0, tFirstToken: 0, tEnd: 0,
  tokens: 0, bytes: 0,
  renderMs: 0, renderCount: 0,
  server: null,
  activeSession: null,
  reset() {
    this.tStart = this.tFirstToken = this.tEnd = 0;
    this.tokens = this.bytes = 0;
    this.renderMs = 0; this.renderCount = 0;
    this.server = null;
  },
  start(sessionId) { this.reset(); this.activeSession = sessionId || null; this.tStart = performance.now(); this._paint(); },
  firstToken() { if (!this.tFirstToken) this.tFirstToken = performance.now(); },
  token(bytes) { this.tokens++; this.bytes += bytes | 0; this._paint(); },
  render(ms) { this.renderMs += ms; this.renderCount++; this._paint(); },
  serverTiming(d) { this.server = d; this._paint(); },
  end() { this.tEnd = performance.now(); this._paint(); },
  addAgentUsage(sessionId, agentName, prompt, output, cacheRead, cacheCreate, prices) {
    if (!sessionId || !agentName) return;
    let agents = sessionAgentTokens.get(sessionId);
    if (!agents) { agents = new Map(); sessionAgentTokens.set(sessionId, agents); }
    const ag = agents.get(agentName) || { prompt: 0, output: 0, cost: 0 };
    ag.prompt += prompt | 0;
    ag.output += output | 0;
    // Freeze the cost at the rate that was in effect for this delta.
    ag.cost += usageCostUSD(prompt, output, cacheRead, cacheCreate, prices);
    agents.set(agentName, ag);
  },
  _paint() {
    if (!this.enabled) return;
    if (!this.badge) {
      const b = document.createElement("div");
      b.id = "debug-badge";
      b.style.cssText = "position:fixed;top:6px;right:8px;z-index:9999;background:#111c;color:#cdf;font:11px/1.35 ui-monospace,SFMono-Regular,Menlo,monospace;padding:6px 8px;border:1px solid #345;border-radius:6px;max-width:420px;white-space:pre;pointer-events:none";
      document.body.appendChild(b);
      this.badge = b;
    }
    const now = this.tEnd || performance.now();
    const ttfb = this.tFirstToken ? (this.tFirstToken - this.tStart) : 0;
    const streamElapsed = now - (this.tFirstToken || this.tStart);
    const tps = (this.tokens && streamElapsed > 0) ? (this.tokens * 1000 / streamElapsed) : 0;
    const lines = [
      `[client] ttfb=${ttfb.toFixed(0)}ms  chunks=${this.tokens}  ${tps.toFixed(1)}/s  bytes=${this.bytes}`,
      `         render=${this.renderMs.toFixed(0)}ms across ${this.renderCount} parse(s)`,
    ];
    if (this.server) {
      lines.push(`[server] ttfb=${this.server.ttfb_ms}ms  chunks=${this.server.tokens}  ${(this.server.tok_per_sec||0).toFixed(1)}/s  total=${this.server.total_ms}ms`);
    }
    const agentMap = sessionAgentTokens.get(this.activeSession);
    if (agentMap && agentMap.size > 0) {
      const fmtTok = n => n >= 1e6 ? (n/1e6).toFixed(2)+"M" : n >= 1000 ? (n/1000).toFixed(1)+"K" : String(n);
      const fmtCost = c => "~$" + (c || 0).toFixed(4);
      const entries = [...agentMap.entries()].sort((a, b) =>
        a[0] === "leader" ? -1 : b[0] === "leader" ? 1 : (b[1].prompt + b[1].output) - (a[1].prompt + a[1].output)
      );
      const nameW = Math.min(14, Math.max(...entries.map(([n]) => n.length)));
      let totP = 0, totO = 0, totC = 0;
      lines.push("[agents]");
      for (const [name, {prompt, output, cost}] of entries) {
        totP += prompt; totO += output; totC += (cost || 0);
        lines.push(`         ${name.padEnd(nameW)}  in=${fmtTok(prompt).padStart(6)}  out=${fmtTok(output).padStart(5)}  ${fmtCost(cost)}`);
      }
      if (entries.length > 1) {
        lines.push(`         ${"─".repeat(nameW + 32)}`);
        lines.push(`         ${"total".padEnd(nameW)}  in=${fmtTok(totP).padStart(6)}  out=${fmtTok(totO).padStart(5)}  ${fmtCost(totC)}`);
      }
    }
    this.badge.textContent = lines.join("\n");
  },
};
AgentDebug.reset();
if (AgentDebug.enabled) AgentDebug._paint();
window.AgentDebug = AgentDebug;

// `els` holds only the GLOBAL, single-instance UI: the sidebar and the
// full-screen modal overlays. Per-pane chat elements (transcript, composer,
// prompt, send/cancel, status, context ring, ask-user slot, attachments, …)
// live on each panel object's `.els` map instead — see the Panels section.
const els = {
  sidebar:       document.getElementById("sidebar"),
  sidebarResize: document.getElementById("sidebar-resize"),
  sidebarToggle: document.getElementById("sidebar-toggle"),
  newChat:       document.getElementById("new-chat"),
  newChatWrap:   document.getElementById("new-chat-wrap"),
  squadToggle:   document.getElementById("squad-toggle"),
  squadMenu:     document.getElementById("squad-menu"),
  list:          document.getElementById("session-list"),
  archivedPanel: document.getElementById("archived-panel"),
  archivedHeader:document.getElementById("archived-header"),
  archivedList:  document.getElementById("archived-list"),
  archivedCount: document.getElementById("archived-count"),
  foldersPanel:  document.getElementById("folders-panel"),
  foldersResize: document.getElementById("folders-resize"),
  foldersHeader: document.getElementById("folders-header"),
  foldersBody:   document.getElementById("folders-body"),
  foldersPath:   document.getElementById("folders-path"),
  foldersList:   document.getElementById("folders-list"),
  chat:          document.getElementById("chat"),
  paneTpl:       document.getElementById("chat-pane-tpl"),
  ctxBrowserOverlay:  document.getElementById("ctx-browser-overlay"),
  ctxBrowserClose:    document.getElementById("ctx-browser-close"),
  ctxBrowserPath:     document.getElementById("ctx-browser-path"),
  ctxBrowserList:     document.getElementById("ctx-browser-list"),
  ctxBrowserCount:    document.getElementById("ctx-browser-count"),
  ctxBrowserCancel:   document.getElementById("ctx-browser-cancel"),
  ctxBrowserAdd:      document.getElementById("ctx-browser-add"),
  userCmdOverlay:     document.getElementById("user-cmd-modal-overlay"),
  userCmdTitle:      document.getElementById("user-cmd-modal-title"),
  userCmdClose:      document.getElementById("user-cmd-modal-close"),
  userCmdCancel:     document.getElementById("user-cmd-modal-cancel"),
  userCmdSave:       document.getElementById("user-cmd-modal-save"),
  userCmdName:       document.getElementById("user-cmd-name"),
  userCmdDesc:       document.getElementById("user-cmd-desc"),
  userCmdArgs:       document.getElementById("user-cmd-args"),
  userCmdPrompt:     document.getElementById("user-cmd-prompt"),
  userCmdError:      document.getElementById("user-cmd-error"),
  skillNameOverlay:  document.getElementById("skill-name-modal-overlay"),
  skillNameTitle:    document.getElementById("skill-name-modal-title"),
  skillNameClose:    document.getElementById("skill-name-modal-close"),
  skillNameCancel:   document.getElementById("skill-name-modal-cancel"),
  skillNameStart:    document.getElementById("skill-name-modal-start"),
  skillNameInput:    document.getElementById("skill-name-input"),
  skillNameError:    document.getElementById("skill-name-modal-error"),
};

let token = localStorage.getItem(TOKEN_KEY) || "";
// `activeSessionId` is a compatibility shim: it mirrors the FOCUSED panel's
// session. Global actions (sidebar, modals, slash commands) act on the focused
// pane through it. Per-session display writes are routed to the right pane(s)
// via panelsForSession() regardless of focus, so background panes update too.
let activeSessionId = null;
let sendOnEnter = true;
const ctxBrowserSelected = new Map(); // path → {name, path, size}

// ─── Panels (split-screen) ───────────────────────────────────────────────────
// The chat area is a horizontal row of one-or-more independent panes. Each pane
// owns a cloned copy of the chat UI (transcript + composer + context ring + …)
// and is bound to at most one session. A session can be shown in at most one
// pane (its transcript DOM is a single node — see getContainer). `panels` is
// ordered left→right; `focusedPanelId` is the pane that sidebar clicks and the
// shared menus/modals target.

let panels = [];
let focusedPanelId = null;
let panelSeq = 0;
const PANE_MIN_W = 360;       // px; minimum width of a pane
const PANE_DIVIDER_W = 6;     // px; width of a draggable divider
const LAYOUT_KEY = "agent_toolkit_layout";

// Pending ask-user widgets for sessions not currently shown in any pane, keyed
// by sessionId → [question objects]. Flushed into a pane when the session is
// bound to it (bindSessionToPanel).
const queuedAskWidgets = new Map();

// Per-pane element ids resolved (scoped) from the cloned template. The cloned
// panes intentionally repeat these ids; we always resolve them via
// root.querySelector so duplicates never matter to JS (and #id CSS selectors
// style every pane identically).
const PANE_EL_IDS = {
  promptHeader: "prompt-header", transcript: "transcript",
  composerWrap: "composer-wrap", composer: "composer", prompt: "prompt",
  editModeBtn: "edit-mode-btn", slashBtn: "slash-btn", slashMenu: "slash-menu",
  send: "send", cancel: "cancel", status: "status",
  ctxRingWrap: "ctx-ring-wrap", ctxRingSvg: "ctx-ring-svg",
  ctxPopup: "ctx-popup", ctxPopUsed: "ctx-pop-used", ctxPopMax: "ctx-pop-max",
  ctxPopPct: "ctx-pop-pct", ctxPopBudget: "ctx-pop-budget", ctxPopAgents: "ctx-pop-agents",
  ctxCompactBtn: "ctx-compact-btn", composerResize: "composer-resize",
  fileInput: "file-input", attachBtn: "attach-btn", attachMenu: "attach-menu",
  attachComputer: "attach-computer", attachContext: "attach-context",
  attachments: "attachments",
};

function bindPaneEls(root) {
  const e = {};
  for (const k in PANE_EL_IDS) e[k] = root.querySelector("#" + PANE_EL_IDS[k]);
  e.promptHighlight = root.querySelector(".prompt-highlight");
  e.ctxRingFill = root.querySelector(".ctx-ring-fill");
  e.askSlot     = root.querySelector("#ask-user-slot");
  e.editorWrap  = root.querySelector(".pane-editor");
  e.editorHost  = root.querySelector(".pane-editor-host");
  e.editorPath  = root.querySelector(".pane-editor-path");
  e.editorSave  = root.querySelector(".pane-editor-save");
  e.editorStale = root.querySelector(".pane-editor-stale");
  e.editorReload = root.querySelector(".pane-editor-reload");
  e.terminalWrap = root.querySelector(".pane-terminal");
  e.terminalHost = root.querySelector(".pane-terminal-host");
  e.termBtn     = root.querySelector(".pane-term-btn");
  e.toolbar     = root.querySelector(".pane-toolbar");
  e.splitBtn    = root.querySelector(".pane-split-btn");
  e.closeBtn    = root.querySelector(".pane-close-btn");
  e.tabs        = root.querySelector(".pane-tabs");
  e.newTabBtn   = root.querySelector(".pane-newtab-btn");
  e.picker      = root.querySelector(".pane-picker");
  e.pickerNew   = root.querySelector(".pane-picker-new");
  e.pickerSquad = root.querySelector(".pane-picker-squad");
  e.pickerSquadToggle = root.querySelector(".pane-picker-squad-toggle");
  e.pickerSquadName   = root.querySelector(".pane-picker-squad-name");
  e.pickerSquadMenu   = root.querySelector(".pane-picker-squad-menu");
  e.pickerList  = root.querySelector(".pane-picker-list");
  return e;
}

// panelsForSession returns panes where `id` is the ACTIVE (visible) tab — used
// to update visible pane chrome (status, ctx ring, ask widget, scroll).
function panelsForSession(id) { return id ? panels.filter(p => p.sessionId === id) : []; }
// panelsWithTab returns panes that hold `id` as a tab at all (active or in the
// background) — used for "is it open anywhere" checks: push subscriptions,
// sidebar highlight, dedupe-on-open, and delete/archive cleanup.
function panelsWithTab(id) { return id ? panels.filter(p => p.tabs.includes(id)) : []; }

// Draft tabs are pending "New Chat" tabs with no session yet. They live in
// panel.tabs as synthetic keys so several can coexist in one pane.
let draftSeq = 0;
function isDraft(key) { return typeof key === "string" && key.startsWith("draft#"); }
function newDraftKey() { return "draft#" + (++draftSeq); }
function getPanel(pid) { return panels.find(p => p.id === pid) || null; }
function focusedPanel() { return getPanel(focusedPanelId) || panels[0] || null; }
function fp() { return focusedPanel(); }

// createPanel clones the template and registers a panel object (not yet
// inserted into the DOM — see rebuildChatDOM / layoutWidths).
function createPanel(sessionId) {
  const frag = els.paneTpl.content.cloneNode(true);
  // Translate the cloned pane's static strings (data-i18n*) before it mounts.
  if (window.I18N) I18N.translateDom(frag);
  const root = frag.querySelector(".chat-pane");
  const id = "p" + (panelSeq++);
  root.dataset.panelId = id;
  const panel = {
    // `tabs` is the ordered list of tab keys open in this pane — each key is
    // either a real sessionId or a synthetic draft key ("draft#N", a pending
    // "New Chat" tab with no session yet). `activeTab` is the visible tab key;
    // `sessionId` mirrors it but is null while a draft tab is active (kept for
    // the many call sites that read the active session directly).
    id, sessionId: sessionId || null, activeTab: sessionId || null,
    tabs: sessionId ? [sessionId] : [], root,
    els: bindPaneEls(root),
    width: 0, _stick: true, _scrollPending: false,
  };
  panels.push(panel);
  attachPaneHandlers(panel);
  renderPaneTabs(panel);
  return panel;
}

// mountInPanel swaps a session's transcript container into the pane (or clears
// it when sessionId is null).
function mountInPanel(panel, sessionId) {
  const t = panel.els.transcript;
  const next = sessionId ? getContainer(sessionId) : null;
  if (next && t.contains(next)) return;
  while (t.firstChild) t.removeChild(t.firstChild);
  if (next) t.appendChild(next);
}

function setFocusedPanel(pid) {
  focusedPanelId = pid;
  for (const p of panels) p.root.classList.toggle("is-focused", p.id === pid);
  const p = getPanel(pid);
  activeSessionId = p ? p.sessionId : null;
  if (AgentDebug.enabled) { AgentDebug.activeSession = activeSessionId; AgentDebug._paint(); }
  refreshSidebarActive();
  refreshFoldersPanel();
  saveLayout();
}

// refreshSidebarActive highlights every session currently shown in any pane,
// and marks the focused pane's session distinctly (`.active-focused`) so the
// chat the user is actually working in stands out from those open in other
// panes.
function refreshSidebarActive() {
  if (!els.list) return;
  const shown = new Set(panels.flatMap(p => p.tabs));
  const focusedId = (focusedPanel() || {}).sessionId || null;
  for (const li of els.list.children) {
    const id = li.dataset.id;
    li.classList.toggle("active", shown.has(id));
    li.classList.toggle("active-focused", !!focusedId && id === focusedId);
  }
}

// ─── Pane layout (widths + dividers) ──────────────────────────────────────────

function applyPaneWidths() {
  for (const p of panels) p.root.style.flex = `0 0 ${Math.round(p.width)}px`;
}

// layoutWidths normalizes the stored per-pane widths to fill the chat area,
// clamping each to PANE_MIN_W, then writes them to the DOM.
function layoutWidths() {
  if (!panels.length) return;
  const total = els.chat.clientWidth || (panels.length * PANE_MIN_W);
  const dividers = Math.max(0, panels.length - 1) * PANE_DIVIDER_W;
  const avail = Math.max(panels.length * PANE_MIN_W, total - dividers);
  let sum = panels.reduce((s, p) => s + (p.width > 0 ? p.width : 0), 0);
  if (sum <= 0) {
    const w = avail / panels.length;
    for (const p of panels) p.width = w;
  } else {
    const k = avail / sum;
    for (const p of panels) p.width = Math.max(PANE_MIN_W, (p.width > 0 ? p.width : avail / panels.length) * k);
  }
  applyPaneWidths();
}

// rebuildChatDOM re-lays the #chat row as pane / divider / pane / divider / …
// It removes only existing panes and dividers, preserving #settings-panel
// (which Settings.js appends to #chat) and inserts panes before it.
function rebuildChatDOM() {
  for (const el of [...els.chat.querySelectorAll(":scope > .chat-pane, :scope > .pane-divider")]) {
    el.remove();
  }
  const anchor = els.chat.querySelector(":scope > #settings-panel");
  panels.forEach((p, i) => {
    if (i > 0) els.chat.insertBefore(makeDivider(panels[i - 1], p), anchor);
    els.chat.insertBefore(p.root, anchor);
  });
  els.chat.classList.toggle("solo", panels.length <= 1);
  layoutWidths();
}

function makeDivider(left, right) {
  const d = document.createElement("div");
  d.className = "pane-divider";
  d.setAttribute("aria-hidden", "true");
  d.addEventListener("mousedown", (e) => {
    paneDividerDrag = {
      left, right, startX: e.clientX,
      startLeftW: left.width, startRightW: right.width,
    };
    d.classList.add("is-dragging");
    document.body.classList.add("resizing");
    document.body.style.userSelect = "none";
    e.preventDefault();
  });
  return d;
}

let paneDividerDrag = null;

// ─── Split / close ─────────────────────────────────────────────────────────

function splitPanel(after) {
  // The new pane takes half of `after`'s width.
  const half = Math.max(PANE_MIN_W, (after.width || els.chat.clientWidth / panels.length) / 2);
  const np = createPanel(null);
  // Insert np right after `after` in the ordering.
  const idx = panels.indexOf(np); // np is at the end after createPanel
  panels.splice(idx, 1);
  const afterIdx = panels.indexOf(after);
  panels.splice(afterIdx + 1, 0, np);
  after.width = half;
  np.width = half;
  rebuildChatDOM();
  setFocusedPanel(np.id);
  newDraftTab(np); // a new pane starts with an empty "New Chat" tab
  saveLayout();
}

function closePanel(panel) {
  if (panels.length <= 1) return; // never close the last pane
  const idx = panels.indexOf(panel);
  const tabIds = panel.tabs.slice();
  panels.splice(idx, 1);
  if (focusedPanelId === panel.id) {
    const neighbor = panels[Math.min(idx, panels.length - 1)];
    focusedPanelId = neighbor ? neighbor.id : null;
  }
  // Each tab's transcript DOM stays cached in sessionContainers (no loss); drop
  // the push subscription of any session tab not still open in another pane, and
  // free any editor-tab models this pane owned.
  for (const k of tabIds) {
    if (isEditorTab(k)) { if (panelsWithTab(k).length === 0) disposeEditor(editorPathOf(k)); }
    else if (isTermTab(k)) disposeTerminal(k);
    else releaseSessionIfUnviewed(k);
  }
  if (panel._editor) { panel._editor.dispose(); panel._editor = null; }
  if (panel._composerRO) { panel._composerRO.disconnect(); panel._composerRO = null; }
  rebuildChatDOM();
  setFocusedPanel(focusedPanelId);
  saveLayout();
}

// releaseSessionIfUnviewed drops a session's push subscription once no pane
// holds it as a tab anymore (and it isn't actively streaming).
function releaseSessionIfUnviewed(sessionId) {
  if (!sessionId) return;
  if (panelsWithTab(sessionId).length === 0 && !sessionSending.has(sessionId)) {
    unsubscribeSessionEvents(sessionId);
  }
}

// ─── Empty-pane session picker ────────────────────────────────────────────────

function showPanePicker(panel) {
  renderPanePicker(panel);
  if (panel.els.picker) panel.els.picker.hidden = false;
}
function hidePanePicker(panel) {
  if (panel.els.picker) panel.els.picker.hidden = true;
}

function renderPanePicker(panel) {
  renderPickerSquad(panel);
  const list = panel.els.pickerList;
  if (!list) return;
  list.innerHTML = "";
  const shown = new Set(panels.flatMap(p => p.tabs));
  for (const li of els.list.children) {
    const id = li.dataset.id;
    if (!id) continue;
    const nameEl = li.querySelector(".session-name");
    const name = nameEl ? nameEl.textContent : id;
    const item = document.createElement("li");
    item.className = "pane-picker-item";
    item.textContent = name;
    if (shown.has(id)) item.classList.add("is-open"); // open in some pane
    item.addEventListener("click", () => {
      const existing = panelsWithTab(id)[0];
      if (existing && existing !== panel) { setFocusedPanel(existing.id); activateTab(existing, id); return; }
      bindSessionToPanel(panel, id);
    });
    list.appendChild(item);
  }
  if (!list.children.length) {
    const empty = document.createElement("li");
    empty.className = "pane-picker-empty";
    empty.textContent = tr("app.session.noOthers");
    list.appendChild(empty);
  }
}

// Populate the empty-pane squad selector from the loaded squads. Mirrors the
// sidebar squad menu (custom dropdown of .squad-menu-item buttons, so each item
// carries a themed `data-tip` tooltip): hidden entirely when only the default
// squad exists, and defaulted to the globally-selected squad so it matches the
// New Chat button. The per-pane choice lives on `panel._pickerSquad`.
function renderPickerSquad(panel) {
  const wrap = panel.els.pickerSquad;
  const menu = panel.els.pickerSquadMenu;
  if (!wrap || !menu) return;
  if (availableSquads.length <= 1) {
    wrap.hidden = true;
    panel._pickerSquad = null;
    return;
  }
  wrap.hidden = false;
  // Keep a prior valid choice; otherwise fall back to the global selection.
  if (!availableSquads.some(s => s.name === panel._pickerSquad)) {
    panel._pickerSquad = currentSquadChoice();
  }
  menu.innerHTML = "";
  for (const sq of availableSquads) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "squad-menu-item" + (sq.name === panel._pickerSquad ? " selected" : "");
    btn.dataset.squad = sq.name;
    btn.setAttribute("role", "menuitem");
    btn.setAttribute("data-tip", sq.description || `${sq.leader} + ${(sq.members || []).join(", ")}`);
    btn.innerHTML = squadIconSVG();
    const label = document.createElement("span");
    label.className = "squad-menu-label";
    label.textContent = sq.name;
    btn.appendChild(label);
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      panel._pickerSquad = sq.name;
      closePickerSquadMenu(panel);
      renderPickerSquad(panel);
    });
    menu.appendChild(btn);
  }
  if (panel.els.pickerSquadName) panel.els.pickerSquadName.textContent = panel._pickerSquad;
}

function openPickerSquadMenu(panel) {
  const menu = panel.els.pickerSquadMenu;
  const toggle = panel.els.pickerSquadToggle;
  if (!menu || !toggle) return;
  menu.hidden = false;
  toggle.setAttribute("aria-expanded", "true");
}
function closePickerSquadMenu(panel) {
  const menu = panel.els.pickerSquadMenu;
  const toggle = panel.els.pickerSquadToggle;
  if (!menu || !toggle) return;
  menu.hidden = true;
  toggle.setAttribute("aria-expanded", "false");
}

// ─── Per-pane event handlers ──────────────────────────────────────────────────

function attachPaneHandlers(panel) {
  const pe = panel.els;

  // Focus tracking: any pointer/focus inside the pane makes it the target of
  // sidebar clicks and shared menus.
  panel.root.addEventListener("mousedown", () => {
    if (focusedPanelId !== panel.id) setFocusedPanel(panel.id);
  }, true);
  panel.root.addEventListener("focusin", () => {
    if (focusedPanelId !== panel.id) setFocusedPanel(panel.id);
  });

  // Model-connection warning banners (above the composer + above the new-chat
  // picker button). Clicking either opens the fix-connection popup; their
  // visibility is driven by the latest provider-health probe.
  for (const b of panel.root.querySelectorAll(".provider-warn-banner")) {
    b.addEventListener("click", (e) => { e.stopPropagation(); openProviderHealthModal(); });
  }
  applyProviderWarning(panel);

  // Toolbar: terminal / split / close.
  if (pe.termBtn) pe.termBtn.addEventListener("click", (e) => { e.stopPropagation(); openTerminalTab(panel, { sid: panel.sessionId || "" }); });
  if (pe.splitBtn) pe.splitBtn.addEventListener("click", (e) => { e.stopPropagation(); splitPanel(panel); });
  if (pe.closeBtn) pe.closeBtn.addEventListener("click", (e) => { e.stopPropagation(); closePanel(panel); });

  // Editor Save button (Monaco editor tabs).
  if (pe.editorSave) pe.editorSave.addEventListener("click", (e) => {
    e.stopPropagation();
    if (isEditorTab(panel.activeTab)) saveEditor(panel, editorPathOf(panel.activeTab));
  });

  // "Reload from disk" on the stale banner — discard unsaved edits and load the
  // agent's on-disk version.
  if (pe.editorReload) pe.editorReload.addEventListener("click", (e) => {
    e.stopPropagation();
    if (isEditorTab(panel.activeTab)) reloadEditorFromDisk(editorPathOf(panel.activeTab));
  });

  // "+" always opens a fresh "New Chat" tab showing the start picker — no
  // session is created until the user clicks "Start a new chat". Several drafts
  // can coexist.
  if (pe.newTabBtn) pe.newTabBtn.addEventListener("click", (e) => { e.stopPropagation(); newDraftTab(panel); });

  // Empty-pane picker. The squad selector (when shown) overrides the global
  // choice for this new chat only.
  if (pe.pickerSquadToggle) {
    pe.pickerSquadToggle.addEventListener("click", (e) => {
      e.stopPropagation();
      if (pe.pickerSquadMenu.hidden) openPickerSquadMenu(panel);
      else closePickerSquadMenu(panel);
    });
  }
  if (pe.pickerSquadMenu) pe.pickerSquadMenu.addEventListener("click", (e) => e.stopPropagation());
  if (pe.pickerNew) pe.pickerNew.addEventListener("click", () => {
    const squad = (pe.pickerSquad && !pe.pickerSquad.hidden) ? panel._pickerSquad : undefined;
    newChat(panel, squad);
  });

  // Composer submit.
  pe.composer.addEventListener("submit", (e) => { e.preventDefault(); sendMessage(panel); });

  // Attach menu.
  pe.attachBtn.addEventListener("click", (e) => { e.stopPropagation(); pe.attachMenu.toggleAttribute("hidden"); });
  pe.attachMenu.addEventListener("click", (e) => e.stopPropagation());
  pe.attachComputer.addEventListener("click", () => { pe.attachMenu.setAttribute("hidden", ""); pe.fileInput.click(); });
  pe.attachContext.addEventListener("click", () => { pe.attachMenu.setAttribute("hidden", ""); openCtxBrowser(); });

  pe.fileInput.addEventListener("change", () => uploadPickedFiles(panel, Array.from(pe.fileInput.files), () => { pe.fileInput.value = ""; }));

  pe.prompt.addEventListener("paste", async (e) => {
    const items = e.clipboardData?.items;
    if (!items) return;
    const imageItems = Array.from(items).filter(it => it.kind === "file" && it.type.startsWith("image/"));
    if (!imageItems.length) return;
    e.preventDefault();
    const files = imageItems.map(it => it.getAsFile()).filter(Boolean);
    uploadPickedFiles(panel, files);
  });

  // Pasting a reference copied from the Folders panel (Ctrl/Cmd+C) inserts it
  // space-padded so it stays a valid "@path" file ref. Only triggers when the
  // clipboard exactly matches the ref we last copied — all other pastes are
  // native.
  pe.prompt.addEventListener("paste", (e) => {
    if (!lastCopiedRef) return;
    const text = (e.clipboardData?.getData("text") || "").trim();
    if (text !== lastCopiedRef) return;
    e.preventDefault();
    insertRefIntoComposer(panel, lastCopiedRef);
  });

  // Drag & drop onto the composer.
  let dragCounter = 0;
  pe.composerWrap.addEventListener("dragenter", (e) => {
    if (!e.dataTransfer?.types.includes("Files")) return;
    e.preventDefault(); dragCounter++; pe.composerWrap.classList.add("drag-over");
  });
  pe.composerWrap.addEventListener("dragover", (e) => {
    if (!e.dataTransfer?.types.includes("Files")) return;
    e.preventDefault(); e.dataTransfer.dropEffect = "copy";
  });
  pe.composerWrap.addEventListener("dragleave", () => {
    dragCounter--; if (dragCounter <= 0) { dragCounter = 0; pe.composerWrap.classList.remove("drag-over"); }
  });
  pe.composerWrap.addEventListener("drop", (e) => {
    e.preventDefault(); dragCounter = 0; pe.composerWrap.classList.remove("drag-over");
    const files = Array.from(e.dataTransfer?.files || []);
    if (files.length) uploadPickedFiles(panel, files);
  });

  // Cancel streaming. The run now outlives the HTTP request, so Stop must do two
  // things: flag the intent (so the aborted fetch isn't mistaken for a network
  // drop and auto-reconnected) and tell the server to actually abort the run.
  pe.cancel.addEventListener("click", () => {
    const sid = panel.sessionId;
    if (!sid) return;
    sessionStopped.add(sid);
    apiFetch(`/api/sessions/${sid}/cancel`, { method: "POST" }).catch(() => {});
    const ctrl = sessionAbortCtrls.get(sid);
    if (ctrl) ctrl.abort();
  });

  // Enter-key mode toggle (a global preference; reflect on all panes).
  pe.editModeBtn.addEventListener("click", () => { sendOnEnter = !sendOnEnter; updateEditModeBtn(); });

  // Prompt keydown (slash-menu nav + send).
  pe.prompt.addEventListener("keydown", (e) => onPromptKeydown(e, panel));
  pe.prompt.addEventListener("input", () => {
    autoGrowPrompt(panel);
    const val = pe.prompt.value;
    const firstLine = val.split("\n")[0];
    const firstWord = firstLine.split(" ")[0];
    if (val.startsWith("/") && !firstLine.includes(" ")) renderSlashMenu(firstWord);
    else if (val.startsWith("!")) renderBangMenu(panel, firstLine);
    else if (atTokenAtCaret(pe.prompt) !== null) renderAtMenu(panel);
    else hideSlashMenu();
  });
  // Keep the "@file" highlight backdrop aligned when the textarea scrolls.
  pe.prompt.addEventListener("scroll", () => syncPromptHighlightScroll(panel));
  // The textarea text is transparent (the backdrop shows it). IME pre-edit text
  // never reaches .value, so make the textarea's own text visible during
  // composition and repaint the backdrop once the character is committed.
  pe.prompt.addEventListener("compositionstart", () => pe.composerWrap.classList.add("ime-composing"));
  pe.prompt.addEventListener("compositionend", () => { pe.composerWrap.classList.remove("ime-composing"); renderPromptHighlight(panel); });

  pe.slashBtn.addEventListener("click", () => {
    if (focusedPanelId !== panel.id) setFocusedPanel(panel.id);
    if (!pe.prompt.value.startsWith("/")) pe.prompt.value = "/" + pe.prompt.value;
    pe.prompt.focus();
    autoGrowPrompt(panel);
    renderSlashMenu(pe.prompt.value.split("\n")[0].split(" ")[0]);
  });

  // Composer resize handle.
  pe.composerResize.addEventListener("mousedown", (e) => {
    composerDragging   = true;
    composerDragStartY = e.clientY;
    // --composer-h sizes the editor (the textarea), not the whole wrap, so start
    // the drag from the textarea's current height for a 1:1 grab.
    composerDragStartH = pe.prompt.getBoundingClientRect().height;
    pe.composerResize.classList.add("is-dragging");
    document.body.classList.add("resizing-composer");
    document.body.style.userSelect = "none";
    e.preventDefault();
  });

  // Context ring popup.
  pe.ctxRingWrap.addEventListener("click", (e) => {
    e.stopPropagation();
    if (pe.ctxPopup.hasAttribute("hidden")) openCtxPopup(panel);
    else closeCtxPopup(panel);
  });
  pe.ctxCompactBtn.addEventListener("click", (e) => onCompactClick(e, panel));

  // Sticky-bottom autoscroll + pinned prompt header per pane.
  pe.transcript.addEventListener("scroll", () => {
    panel._stick = isAtBottom(pe.transcript);
    updatePinnedForScroll(panel);
  });

  // The composer floats over the transcript, so publish its measured height as
  // --composer-overlay-h on the pane root: #transcript's bottom padding and the
  // ask-user slot's bottom margin track it so content always clears the card.
  // Re-pin to bottom while stuck so a growing composer never hides the last line.
  // The same observer keeps --ask-card-max-h current (see updateAskCardBounds):
  // it must be recomputed whenever the composer, the top chrome (tab bar +
  // pinned prompt header, whose height varies with the prompt length), or the
  // pane itself changes size.
  if (window.ResizeObserver) {
    const ro = new ResizeObserver(() => {
      const h = pe.composerWrap.offsetHeight || 0;
      panel.root.style.setProperty("--composer-overlay-h", h + "px");
      updateAskCardBounds(panel);
      if (panel._stick) scrollBottom(panel);
    });
    ro.observe(pe.composerWrap);
    if (pe.promptHeader && pe.promptHeader.parentElement) ro.observe(pe.promptHeader.parentElement);
    ro.observe(panel.root);
    panel._composerRO = ro;
  }
}

// updateAskCardBounds publishes --ask-card-max-h on the pane root: the exact
// pixel height the ask-user card may occupy between the bottom of the top chrome
// (tab bar + the variable-height pinned prompt header) and the top of the
// floating composer. Measuring it — rather than the old fixed `100vh - 170px`
// guess — keeps the whole card (prompt, options AND the Submit/Skip row) visible
// regardless of the pinned prompt's height or a config banner pushing the pane
// down; only in a genuinely short window does the card cap out and scroll its
// prompt internally while the action row stays pinned. The transcript's top edge
// is anchored just below the chrome and the composer's top is anchored to the
// pane bottom, so neither moves when the card appears — no measurement feedback
// loop. A min clamp guards against transient negative values mid-layout.
function updateAskCardBounds(panel) {
  const pe = panel && panel.els;
  if (!pe || !pe.transcript || !pe.composerWrap) return;
  const top = pe.transcript.getBoundingClientRect().top;
  const bottom = pe.composerWrap.getBoundingClientRect().top;
  const avail = Math.max(120, Math.round(bottom - top - 12));
  if (panel._askMaxH === avail) return;
  panel._askMaxH = avail;
  panel.root.style.setProperty("--ask-card-max-h", avail + "px");
}

// uploadPickedFiles uploads files to a pane's session (creating one if the
// pane is empty), then renders the attachment chips.
async function uploadPickedFiles(panel, files, after) {
  if (after) after();
  if (!files || !files.length) return;
  if (!panel.sessionId) { await newChat(panel); }
  if (!panel.sessionId) return;
  const sessionId = panel.sessionId;
  const form = new FormData();
  for (const f of files) form.append("files", f, f.name || `screenshot-${Date.now()}.png`);
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/files`, { method: "POST", body: form });
    if (!res.ok) { console.error("upload failed:", await res.text()); return; }
    const data = await res.json();
    for (const f of (data.files || [])) addAttachment(sessionId, f);
    renderAttachmentsUI(sessionId);
  } catch (e) { console.error("upload error:", e); }
}

// onPromptKeydown handles slash-menu navigation and send/newline for a pane.
function onPromptKeydown(e, panel) {
  const pe = panel.els;
  if (!pe.slashMenu.hasAttribute("hidden")) {
    const items = Array.from(pe.slashMenu.querySelectorAll(".slash-menu-item"));
    if (e.key === "ArrowDown") {
      e.preventDefault();
      slashMenuFocusIdx = Math.min(slashMenuFocusIdx + 1, items.length - 1);
      updateSlashMenuFocus();
      return;
    }
    if (e.key === "ArrowUp") {
      e.preventDefault();
      slashMenuFocusIdx = Math.max(slashMenuFocusIdx - 1, -1);
      updateSlashMenuFocus();
      return;
    }
    if (e.key === "Tab") {
      e.preventDefault();
      if (menuMode === "bang" || menuMode === "at") {
        const apply = menuMode === "bang" ? applyBangCompletion : applyAtCompletion;
        if (items.length === 1) apply(panel, items[0].dataset.value);
        else if (items.length > 0) { slashMenuFocusIdx = (slashMenuFocusIdx + 1) % items.length; updateSlashMenuFocus(); }
        return;
      }
      const selectable = items.filter(it => !it.classList.contains("slash-menu-add"));
      if (selectable.length === 1) {
        selectSlashCommand(selectable[0].dataset.value);
      } else if (items.length > 0) {
        slashMenuFocusIdx = (slashMenuFocusIdx + 1) % items.length;
        updateSlashMenuFocus();
      }
      return;
    }
    if (e.key === "Enter" && slashMenuFocusIdx >= 0) {
      e.preventDefault();
      const focused = items[slashMenuFocusIdx];
      if (menuMode === "bang") { applyBangCompletion(panel, focused.dataset.value); return; }
      if (menuMode === "at") { applyAtCompletion(panel, focused.dataset.value); return; }
      if (focused.classList.contains("slash-menu-add")) { hideSlashMenu(); openUserCommandModal(null); }
      else selectSlashCommand(focused.dataset.value);
      return;
    }
    if (e.key === "Escape") { hideSlashMenu(); return; }
  }
  if (sendOnEnter) {
    if (e.key === "Enter" && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
      e.preventDefault();
      sendMessage(panel);
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      const start = pe.prompt.selectionStart, end = pe.prompt.selectionEnd;
      pe.prompt.value = pe.prompt.value.substring(0, start) + "\n" + pe.prompt.value.substring(end);
      pe.prompt.selectionStart = pe.prompt.selectionEnd = start + 1;
      pe.prompt.dispatchEvent(new Event("input"));
    }
  } else {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") { e.preventDefault(); sendMessage(panel); }
  }
}

// ─── Layout persistence ───────────────────────────────────────────────────────

let _layoutSaveTimer = null;
function saveLayout() {
  if (_layoutSaveTimer) return;
  _layoutSaveTimer = setTimeout(() => {
    _layoutSaveTimer = null;
    try {
      const rec = {
        version: 2,
        panes: panels.map(p => ({
          // Persist session + editor tabs — draft "New Chat" and terminal tabs
          // are ephemeral (a server PTY can't survive a reload).
          tabs: p.tabs.filter(k => !isDraft(k) && !isTermTab(k)),
          activeId: p.sessionId,     // null while a draft/editor tab is active
          activeKey: p.activeTab,    // the active tab key (session id or "file#<abs>")
          width: Math.round(p.width),
        })),
        focusedIndex: Math.max(0, panels.findIndex(p => p.id === focusedPanelId)),
      };
      localStorage.setItem(LAYOUT_KEY, JSON.stringify(rec));
    } catch (_) { /* ignore quota errors */ }
  }, 250);
}

function loadSavedLayout() {
  try {
    const raw = localStorage.getItem(LAYOUT_KEY);
    if (!raw) return null;
    const rec = JSON.parse(raw);
    if (!rec || !Array.isArray(rec.panes) || !rec.panes.length) return null;
    return rec;
  } catch (_) { return null; }
}

// ─── Per-session streaming state ─────────────────────────────────────────────
// Tracks which sessions are actively streaming so switching sessions doesn't
// carry over the disabled Send button or the "streaming…" status label.

const sessionAbortCtrls = new Map(); // sessionId → AbortController
const sessionSending    = new Set(); // sessionIds currently streaming
const sessionStopped    = new Set(); // sessionIds whose turn the user explicitly Stopped
const sessionStatus     = new Map(); // sessionId → status string
// sessionId → epoch ms a chat-reply OS notification last fired. A completed turn
// has two notification sources — the send-path `finally` (fires first, with the
// final-answer text) and the global `chat_reply` event (fires a moment later,
// previewing the full turn text incl. any "handing off to <sub-agent>" narration).
// This lets the first one win and suppress the redundant second for that turn.
const sessionNotifiedAt = new Map();

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const archivedSessions  = new Set(); // sessionIds in the archived (read-only) state
const sessionTitles     = new Map(); // sessionId → display title (for pane tabs)

// ─── Per-session push event subscriptions ────────────────────────────────────
// Each open session has a persistent SSE connection to /api/sessions/:id/events
// so background mailbox-push turns are reflected in real time.

const sessionTurnCounts  = new Map(); // sessionId → number of turns rendered
const sessionTodos       = new Map(); // sessionId → [{ task, status }] live plan view
const sessionTodoBlock   = new Map(); // sessionId → latest .todo-block (older ones auto-collapse)

// ─── Per-session file attachments ────────────────────────────────────────────
// Pending uploads are stored per session so switching sessions preserves them.

const sessionAttachments = new Map(); // sessionId → [{name, path, size}]

function getAttachments(sessionId) {
  return sessionAttachments.get(sessionId) || [];
}

function addAttachment(sessionId, file) {
  const list = sessionAttachments.get(sessionId) || [];
  list.push(file);
  sessionAttachments.set(sessionId, list);
}

function removeAttachment(sessionId, path) {
  const list = sessionAttachments.get(sessionId) || [];
  sessionAttachments.set(sessionId, list.filter(f => f.path !== path));
}

function clearAttachments(sessionId) {
  sessionAttachments.delete(sessionId);
}

// Composer text is held per tab, so switching tabs/sessions inside a pane never
// clobbers a half-typed message: leaving a tab snapshots its composer text and
// returning restores it. Keyed by tab key (a sessionId for chat tabs, a
// "draft#N" key for empty draft tabs); editor/terminal tabs have no composer and
// are skipped. In-memory only (like sessionAttachments) — a page reload starts
// fresh. The composer is only typeable on a session tab (the opaque pane-picker
// covers it on a draft tab), but keying by tab key keeps this correct regardless.
const composerDrafts = new Map(); // tabKey → string

function isChatTab(key) {
  return typeof key === "string" && !isEditorTab(key) && !isTermTab(key);
}

// saveComposerDraft snapshots a pane's current composer text under the tab it is
// showing (panel.activeTab), so the text returns when that tab is reselected.
// Empty text drops any stored draft, so a sent/cleared composer leaves nothing
// stale behind. No-op for editor/terminal tabs (they have no composer).
function saveComposerDraft(panel) {
  if (!panel || !panel.els || !panel.els.prompt) return;
  const key = panel.activeTab;
  if (!isChatTab(key)) return;
  const val = panel.els.prompt.value;
  if (val) composerDrafts.set(key, val);
  else composerDrafts.delete(key);
}

// restoreComposerDraft loads the stored composer text for `key` into the pane's
// composer (empty when there is none — which also clears a previous tab's text),
// then refreshes auto-grow + the transparent "@file" highlight backdrop (without
// firing an input event, so a restored "/…" or "!…" never pops the slash/bang
// menu).
function restoreComposerDraft(panel, key) {
  if (!panel || !panel.els || !panel.els.prompt) return;
  panel.els.prompt.value = isChatTab(key) ? (composerDrafts.get(key) || "") : "";
  autoGrowPrompt(panel); // also repaints the @file highlight backdrop
}

// renderAttachmentsUI renders the pending-upload chips into whichever pane
// currently shows the session (0 or 1 pane).
function renderAttachmentsUI(sessionId) {
  for (const panel of panelsForSession(sessionId)) {
    const slot = panel.els.attachments;
    if (!slot) continue;
    const files = getAttachments(sessionId);
    if (files.length === 0) { slot.hidden = true; slot.innerHTML = ""; continue; }
    slot.hidden = false;
    slot.innerHTML = "";
    for (const f of files) {
      const chip = document.createElement("div");
      chip.className = "attachment-chip";
      chip.setAttribute("data-tip", f.path);
      const name = document.createElement("span");
      name.className = "attachment-chip-name";
      name.textContent = f.name;
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "attachment-chip-remove";
      remove.setAttribute("aria-label", `Remove ${f.name}`);
      remove.textContent = "×";
      remove.addEventListener("click", () => {
        removeAttachment(sessionId, f.path);
        renderAttachmentsUI(sessionId);
      });
      chip.appendChild(name);
      chip.appendChild(remove);
      slot.appendChild(chip);
    }
  }
}

function setSessionStatus(sessionId, s) {
  sessionStatus.set(sessionId, s);
  for (const p of panelsForSession(sessionId)) setStatus(p, s);
}

// applySessionUI reflects a session's streaming/archived state on every pane
// that currently shows it.
function applySessionUI(id) {
  const active = sessionSending.has(id);
  const archived = archivedSessions.has(id);
  for (const p of panelsForSession(id)) {
    // While a turn streams the send button stays enabled but becomes a "Steer"
    // button — clicking it (or Enter) submits the composer text as a mid-turn
    // steering note (sendMessage routes to steerMessage when sessionSending).
    p.els.send.disabled   = archived;
    setSendButtonMode(p, active && !archived);
    p.els.cancel.disabled = !active;
    setStatus(p, sessionStatus.get(id) || "");
    setComposerReadOnly(p, archived);
    setCtxRingSpinning(p, active);
    renderCtxRing(p);
  }
  // Refresh tab chrome (busy dot) on every pane holding this session as a tab,
  // including background tabs whose session is streaming.
  for (const p of panelsWithTab(id)) renderPaneTabs(p);
}

// setSendButtonMode flips a pane's send button between its normal "Send" state
// and the mid-turn "Steer" state (label + tooltip + an `.is-steer` accent so the
// different action reads at a glance).
function setSendButtonMode(panel, steering) {
  const btn = panel.els.send;
  if (!btn) return;
  btn.classList.toggle("is-steer", steering);
  btn.textContent = steering ? tr("composer.steer") : tr("composer.send");
  if (steering) btn.setAttribute("data-tip", tr("composer.steerTip"));
  else btn.removeAttribute("data-tip");
}

// paneTabTitle resolves a tab's label: the session's title (falling back to id).
function paneTabTitle(sessionId) {
  if (!sessionId) return "New Chat";
  return sessionTitles.get(sessionId) || sessionId;
}

const ICON_TAB_CLOSE = `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>`;
const ICON_TERM_GLYPH = `<svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>`;

// renderPaneTabs rebuilds a pane's tab strip — one button per open tab (session
// or pending "New Chat" draft), the active one highlighted, each with a close
// affordance. A busy dot marks session tabs whose session is streaming (so
// background tabs show activity too).
function renderPaneTabs(panel) {
  const strip = panel.els.tabs;
  if (!strip) return;
  strip.innerHTML = "";
  for (const key of panel.tabs) {
    const draft = isDraft(key);
    const editor = isEditorTab(key);
    const term = isTermTab(key);
    const abs = editor ? editorPathOf(key) : null;
    const label = draft ? "New Chat" : editor ? baseName(abs) : term ? "Terminal" : paneTabTitle(key);
    const tab = document.createElement("div");
    tab.className = "pane-tab"
      + (draft ? " pane-tab-draft" : "")
      + (editor ? " pane-tab-editor" : "")
      + (term ? " pane-tab-term" : "")
      + (editor && editorDirty.get(abs) ? " is-dirty" : "")
      + (key === panel.activeTab ? " active" : "");
    tab.setAttribute("role", "tab");
    tab.dataset.tab = key;
    tab.setAttribute("data-tip", draft ? "New chat" : editor ? abs : label);

    if (editor) {
      const glyph = document.createElement("span");
      glyph.className = "pane-tab-glyph";
      glyph.innerHTML = fileIconSvg(baseName(abs));
      tab.appendChild(glyph);
    } else if (term) {
      const glyph = document.createElement("span");
      glyph.className = "pane-tab-glyph";
      glyph.innerHTML = ICON_TERM_GLYPH;
      tab.appendChild(glyph);
    } else {
      const dot = document.createElement("span");
      dot.className = "pane-tab-busy";
      tab.appendChild(dot);
    }

    const name = document.createElement("span");
    name.className = "pane-tab-name";
    name.textContent = label;
    tab.appendChild(name);

    // Dirty editor tabs show a dot that becomes the close button on hover.
    if (editor) {
      const dirty = document.createElement("span");
      dirty.className = "pane-tab-dirty";
      tab.appendChild(dirty);
    }

    const close = document.createElement("button");
    close.type = "button";
    close.className = "pane-tab-close";
    close.setAttribute("aria-label", "Close tab");
    close.innerHTML = ICON_TAB_CLOSE;
    close.addEventListener("click", (e) => { e.stopPropagation(); closeTab(panel, key); });
    tab.appendChild(close);

    tab.classList.toggle("is-busy", !draft && !editor && !term && sessionSending.has(key));
    tab.addEventListener("mousedown", (e) => {
      // Middle-click closes the tab, like a browser.
      if (e.button === 1) { e.preventDefault(); closeTab(panel, key); return; }
    });
    tab.addEventListener("click", (e) => {
      if (e.target.closest(".pane-tab-close")) return;
      activateTab(panel, key);
    });
    strip.appendChild(tab);
  }
}

// newDraftTab always appends a fresh pending "New Chat" tab and activates it —
// even when the active tab is already a draft (so several can coexist).
function newDraftTab(panel) {
  const key = newDraftKey();
  panel.tabs.push(key);
  activateTab(panel, key);
}

// updatePaneTabsForSession refreshes tabs on every pane holding a session
// (used when a title changes after a turn / rename).
function updatePaneTabsForSession(sessionId) {
  for (const p of panelsWithTab(sessionId)) renderPaneTabs(p);
}

// updatePaneTabsForSession refreshes tabs on every pane holding a session
// (used when a title changes after a turn / rename).
function updatePaneTabsForSession(sessionId) {
  for (const p of panelsWithTab(sessionId)) renderPaneTabs(p);
}

// setComposerReadOnly disables a pane's composer when viewing an archived session.
// Archived sessions are view-only; the user must unarchive to chat again.
function setComposerReadOnly(panel, readonly) {
  if (panel.els.composerWrap) panel.els.composerWrap.classList.toggle("archived-readonly", readonly);
  if (panel.els.prompt) {
    panel.els.prompt.disabled = readonly;
    panel.els.prompt.placeholder = readonly
      ? "Session archived — unarchive to continue the conversation"
      : (sendOnEnter ? "Message the agent… (Enter to send)" : "Message the agent… (Ctrl+Enter to send)");
  }
}

// ─── Context ring ────────────────────────────────────────────────────────────

const CTX_RING_CIRCUMFERENCE = 56.55; // 2π × r(9)
const sessionCtxUsage  = new Map(); // sessionId → {tokens_used, soft_limit, hard_limit, window_tokens}
// sessionTokenAccum keeps a session-wide token total only for the legacy budget
// fallback (a session whose turns persisted no per-agent usage). The live budget
// and per-agent rows are driven by sessionAgentTokens (frozen per-agent cost).
const sessionTokenAccum = new Map(); // sessionId → {prompt: number, output: number}

function setCtxRingSpinning(panel, spinning) {
  if (panel.els.ctxRingSvg) panel.els.ctxRingSvg.classList.toggle("spinning", spinning);
}

// renderCtxRing renders the context-usage ring for a pane, reading the usage of
// the session that pane currently shows.
function renderCtxRing(panel) {
  const e = panel.els;
  if (!e.ctxRingFill || !e.ctxRingSvg || !e.ctxRingWrap) return;
  const usage = sessionCtxUsage.get(panel.sessionId);
  if (!usage || !usage.window_tokens) {
    e.ctxRingFill.style.strokeDashoffset = CTX_RING_CIRCUMFERENCE;
    e.ctxRingSvg.dataset.zone = "ok";
    e.ctxRingWrap.classList.remove("has-data");
    e.ctxRingWrap.dataset.tip = "Context window — click for details";
    return;
  }
  const { tokens_used, soft_limit, hard_limit, window_tokens } = usage;
  const ratio = Math.min(tokens_used / window_tokens, 1);
  const pct = Math.round(ratio * 100);
  e.ctxRingFill.style.strokeDashoffset = CTX_RING_CIRCUMFERENCE * (1 - ratio);
  e.ctxRingSvg.dataset.zone = tokens_used >= hard_limit ? "danger"
    : tokens_used >= soft_limit ? "warn" : "ok";
  e.ctxRingWrap.classList.add("has-data");
  e.ctxRingWrap.dataset.tip = `Context: ${pct}% used — click for more information`;
}

function renderCtxPopup(panel) {
  const e = panel.els;
  if (!e.ctxPopup) return;
  const sessionId = panel.sessionId;
  const usage = sessionCtxUsage.get(sessionId);
  if (!usage || !usage.window_tokens) {
    e.ctxPopUsed.textContent   = "—";
    e.ctxPopMax.textContent    = "—";
    e.ctxPopPct.textContent    = "—";
    e.ctxPopBudget.textContent = "—";
    if (e.ctxPopAgents) e.ctxPopAgents.hidden = true;
    return;
  }
  const { tokens_used, window_tokens } = usage;
  const ratio = Math.min(tokens_used / window_tokens, 1);
  const pct   = Math.round(ratio * 100);
  e.ctxPopUsed.textContent = tokens_used.toLocaleString();
  e.ctxPopMax.textContent  = window_tokens.toLocaleString();
  e.ctxPopPct.textContent  = `${pct}%`;

  // Budget = sum of the per-agent frozen costs, so the total always equals the
  // sum of the rows below. Each agent's cost was priced at its model's rate at
  // turn time. Only when no per-agent usage exists (fully-legacy session) do we
  // fall back to pricing the rolling-context text estimate at the default rate.
  const agentMap = sessionAgentTokens.get(sessionId);
  let cost = 0;
  if (agentMap && agentMap.size) {
    for (const u of agentMap.values()) cost += (u.cost || 0);
  } else {
    const acc = sessionTokenAccum.get(sessionId) || { prompt: 0, output: 0 };
    cost = usageCostUSD(acc.prompt, acc.output, 0, 0, null);
  }
  e.ctxPopBudget.textContent = cost > 0 ? `$${cost.toFixed(4)}` : "—";

  // Per-agent breakdown
  const agentsEl = e.ctxPopAgents;
  if (agentsEl) {
    if (!agentMap || agentMap.size === 0) {
      agentsEl.hidden = true;
    } else {
      agentsEl.hidden = false;
      agentsEl.innerHTML = "";
      const entries = [...agentMap.entries()].sort((a, b) =>
        a[0] === "leader" ? -1 : b[0] === "leader" ? 1 : (b[1].prompt + b[1].output) - (a[1].prompt + a[1].output)
      );
      for (const [name, u] of entries) {
        const row = document.createElement("div");
        row.className = "ctx-pop-agent-row";
        const nameEl = document.createElement("span");
        nameEl.className = "ctx-pop-agent-name";
        nameEl.textContent = name;
        const costEl = document.createElement("span");
        costEl.className = "ctx-pop-agent-cost";
        costEl.textContent = `$${(u.cost || 0).toFixed(4)}`;
        row.appendChild(nameEl);
        row.appendChild(costEl);
        agentsEl.appendChild(row);
      }
    }
  }
}

// fetchUsageEstimate fetches a server-side token/cost estimate for a cold
// session (no real-time SSE data yet) and seeds the ring + popup.
async function fetchUsageEstimate(sessionId) {
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/usage-estimate`);
    if (!res.ok) return;
    const data = await res.json();
    if (!data.tokens_used && !data.prompt_total) return;
    // Only seed if real-time events haven't already populated the maps.
    if (!sessionCtxUsage.has(sessionId)) {
      sessionCtxUsage.set(sessionId, {
        tokens_used:   data.tokens_used   || 0,
        window_tokens: data.window_tokens || 0,
        soft_limit:    data.soft_limit    || 0,
        hard_limit:    data.hard_limit    || 0,
      });
    }
    if (!sessionTokenAccum.has(sessionId)) {
      sessionTokenAccum.set(sessionId, {
        prompt: data.prompt_total || 0,
        output: data.output_total || 0,
      });
    }
    // Restore the per-agent cost breakdown (lost on reload otherwise, since it
    // is built only from live turn_usage events). The server replays the
    // persisted per-turn usage here, including the frozen cost (priced at each
    // turn's own rate), so the restored budget matches what was billed.
    if (data.agents && !sessionAgentTokens.has(sessionId)) {
      const m = new Map();
      for (const [name, u] of Object.entries(data.agents)) {
        m.set(name, { prompt: u.prompt || 0, output: u.output || 0, cost: u.cost || 0 });
      }
      if (m.size > 0) sessionAgentTokens.set(sessionId, m);
    }
    for (const p of panelsForSession(sessionId)) {
      renderCtxRing(p);
      if (p.els.ctxPopup && !p.els.ctxPopup.hasAttribute("hidden")) renderCtxPopup(p);
    }
  } catch (e) {
    console.error("failed to fetch usage estimate:", e);
  }
}

function openCtxPopup(panel) {
  panel = panel || fp();
  if (!panel || !panel.els.ctxPopup) return;
  renderCtxPopup(panel);
  panel.els.ctxPopup.removeAttribute("hidden");
}

function closeCtxPopup(panel) {
  panel = panel || fp();
  if (!panel || !panel.els.ctxPopup) return;
  panel.els.ctxPopup.setAttribute("hidden", "");
}

// ─── Per-session transcript containers ──────────────────────────────────────
// Each session owns a detached <div style="display:contents"> that holds its
// transcript DOM. Only one is mounted inside els.transcript at a time. This
// preserves in-progress streaming DOM when the user switches sessions.

const sessionContainers = new Map();

function getContainer(sessionId) {
  if (!sessionContainers.has(sessionId)) {
    const div = document.createElement("div");
    div.style.display = "contents";
    sessionContainers.set(sessionId, div);
  }
  return sessionContainers.get(sessionId);
}

// ─── Monaco editor tabs ───────────────────────────────────────────────────────
// A third pane-tab kind ("file#<abs>") opens a file in an embedded Monaco
// editor next to chat-session tabs. One editor instance is lazily created per
// pane (panel._editor); a per-file model (editorModels) holds the content and
// edit history, so switching tabs preserves unsaved edits. Files are saved to
// disk via PUT /api/file (Ctrl+S or the Save button).

const EDITOR_TAB_PREFIX = "file#";
function isEditorTab(key) { return typeof key === "string" && key.startsWith(EDITOR_TAB_PREFIX); }
function editorPathOf(key) { return key.slice(EDITOR_TAB_PREFIX.length); }
function editorKey(abs) { return EDITOR_TAB_PREFIX + abs; }
function baseName(p) { const s = String(p).replace(/\/+$/, ""); const i = s.lastIndexOf("/"); return i >= 0 ? s.slice(i + 1) : s; }
function dirOf(p) { const s = String(p).replace(/\/+$/, ""); const i = s.lastIndexOf("/"); return i > 0 ? s.slice(0, i) : (i === 0 ? "/" : ""); }

const editorModels = new Map(); // absPath → monaco.ITextModel
const editorDirty  = new Map(); // absPath → bool (unsaved changes)
const editorStale  = new Map(); // absPath → bool (agent changed file on disk while we held unsaved edits)
const editorRoots  = new Map(); // absPath → the Folders-panel ROOT dir the file was opened from
const editorApplyingExternal = new Set(); // absPaths currently being refreshed from disk (suppress dirty marking)

// extension → Monaco language id (best-effort; unknown → plaintext).
const EDITOR_LANGS = {
  go: "go", js: "javascript", mjs: "javascript", cjs: "javascript", jsx: "javascript",
  ts: "typescript", tsx: "typescript", html: "html", htm: "html", css: "css",
  scss: "scss", sass: "scss", less: "less", json: "json", jsonc: "json",
  md: "markdown", markdown: "markdown", py: "python", rs: "rust", rb: "ruby",
  java: "java", c: "c", h: "c", cpp: "cpp", cc: "cpp", hpp: "cpp", cs: "csharp",
  php: "php", swift: "swift", kt: "kotlin", sh: "shell", bash: "shell", zsh: "shell",
  yml: "yaml", yaml: "yaml", toml: "ini", ini: "ini", cfg: "ini", conf: "ini",
  sql: "sql", xml: "xml", svg: "xml", proto: "proto", lua: "lua", r: "r",
  dockerfile: "dockerfile", makefile: "makefile", txt: "plaintext", log: "plaintext",
};
function langForPath(abs) {
  const name = baseName(abs).toLowerCase();
  if (name === "dockerfile" || name.startsWith("dockerfile.")) return "dockerfile";
  if (name === "makefile") return "makefile";
  if (name === "go.mod" || name === "go.sum") return "plaintext";
  const dot = name.lastIndexOf(".");
  if (dot <= 0) return "plaintext";
  return EDITOR_LANGS[name.slice(dot + 1)] || "plaintext";
}

// Map the active app theme (data-theme on <html>) to a Monaco theme.
function monacoTheme() {
  const t = (document.documentElement.getAttribute("data-theme") || "").toLowerCase();
  return t.includes("light") ? "vs" : "vs-dark";
}

// ensureMonaco lazily injects the vendored AMD loader and resolves once
// window.monaco is ready. Cached so it loads at most once.
let _monacoPromise = null;
function ensureMonaco() {
  if (_monacoPromise) return _monacoPromise;
  _monacoPromise = new Promise((resolve, reject) => {
    if (window.monaco && window.monaco.editor) { resolve(window.monaco); return; }
    const vsBase = new URL("assets/monaco/vs", document.baseURI).href.replace(/\/$/, "");
    // Monaco ≥ 0.54 resolves its language workers itself (the hashed
    // `vs/assets/<label>.worker-<hash>.js` files) via the AMD loader's `toUrl`,
    // relative to the configured `vs` base below — already absolute + same-origin,
    // so it works under a BasePath without the old blob-worker indirection.
    // We must therefore NOT define a custom `MonacoEnvironment.getWorkerUrl`
    // (the pre-0.54 `base/worker/workerMain.js` it pointed at no longer exists,
    // and overriding it would break every language worker). Leave it undefined.
    const loader = document.createElement("script");
    loader.src = vsBase + "/loader.js";
    loader.onload = () => {
      try {
        window.require.config({ paths: { vs: vsBase } });
        window.require(["vs/editor/editor.main"], () => resolve(window.monaco));
      } catch (e) { reject(e); }
    };
    loader.onerror = () => reject(new Error("failed to load Monaco loader.js"));
    document.head.appendChild(loader);
  });
  return _monacoPromise;
}

// ensureEditorModel fetches a file's content (once) and returns its Monaco model.
async function ensureEditorModel(monaco, abs) {
  if (editorModels.has(abs)) return editorModels.get(abs);
  const res = await apiFetch(`/api/file?path=${encodeURIComponent(abs)}&session=${encodeURIComponent(activeSessionId || "")}`);
  const text = res.ok ? await res.text() : "";
  const model = monaco.editor.createModel(text, langForPath(abs), monaco.Uri.file(abs));
  editorModels.set(abs, model);
  editorDirty.set(abs, false);
  model.onDidChangeContent(() => {
    if (editorApplyingExternal.has(abs)) return; // disk refresh, not a user edit
    if (!editorDirty.get(abs)) {
      editorDirty.set(abs, true);
      for (const p of panelsWithTab(editorKey(abs))) renderPaneTabs(p);
    }
  });
  return model;
}

// mountEditor shows the file `abs` in `panel`'s Monaco editor, creating the
// per-pane editor instance on first use.
async function mountEditor(panel, abs) {
  const monaco = await ensureMonaco();
  // The tab may have been switched away while Monaco/content loaded.
  if (panel.activeTab !== editorKey(abs)) return;
  if (!panel._editor) {
    panel._editor = monaco.editor.create(panel.els.editorHost, {
      automaticLayout: true,
      theme: monacoTheme(),
      fontSize: 13,
      minimap: { enabled: true },
      scrollBeyondLastLine: false,
    });
    panel._editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      const key = panel.activeTab;
      if (isEditorTab(key)) saveEditor(panel, editorPathOf(key));
    });
  }
  const model = await ensureEditorModel(monaco, abs);
  if (panel.activeTab !== editorKey(abs)) return;
  panel._editor.setModel(model);
  panel._editor.layout();
  if (panel.els.editorPath) {
    panel.els.editorPath.textContent = abs;
    panel.els.editorPath.setAttribute("data-tip", abs);
  }
  updateEditorStaleUI(panel);
  panel._editor.focus();
  // Anchor the Folders panel to the ROOT this file was opened from (mirrors
  // mountTerminal's cwd re-alignment). The editor tab carries no chat session,
  // so without this the panel would fall back to the global root. Fall back to
  // the file's own directory when the open-time root is unknown (layout restore).
  if (focusedPanelId === panel.id && !foldersCollapsed()) {
    const root = editorRoots.get(abs) || dirOf(abs);
    if (root && root !== foldersDir) loadFolder(root);
  }
}

// saveEditor writes the model's current content to disk via PUT /api/file.
async function saveEditor(panel, abs) {
  const model = editorModels.get(abs);
  if (!model) return;
  if (panel.els.editorSave) panel.els.editorSave.classList.add("saving");
  try {
    const res = await apiFetch(`/api/file`, {
      method: "PUT",
      body: JSON.stringify({ path: abs, content: model.getValue(), session: activeSessionId || "" }),
    });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      setStatus(panel, tr("app.editor.saveFailed", { error: (body.error || res.status) }));
      return;
    }
    editorDirty.set(abs, false);
    editorStale.delete(abs);
    for (const p of panelsWithTab(editorKey(abs))) { renderPaneTabs(p); updateEditorStaleUI(p); }
    setStatus(panel, tr("app.editor.saved", { name: baseName(abs) }));
    setTimeout(() => { if ((sessionStatus.get(panel.sessionId) || "") === "") setStatus(panel, ""); }, 1500);
  } catch (e) {
    setStatus(panel, tr("app.editor.saveFailed", { error: e }));
  } finally {
    if (panel.els.editorSave) panel.els.editorSave.classList.remove("saving");
  }
}

// disposeEditor drops a closed file's model + dirty flag (called from closeTab).
function disposeEditor(abs) {
  const model = editorModels.get(abs);
  if (model) model.dispose();
  editorModels.delete(abs);
  editorDirty.delete(abs);
  editorStale.delete(abs);
  editorRoots.delete(abs);
}

// paneShowingEditor returns the pane whose Monaco instance currently displays
// the file `abs` (its editor tab is active), or null when no pane shows it
// (the tab may be open but backgrounded, or not open at all).
function paneShowingEditor(abs) {
  const key = editorKey(abs);
  return panels.find(p => p._editor && p.activeTab === key) || null;
}

// reloadEditorFromDisk replaces an open editor model's content with the current
// on-disk version, preserving cursor/scroll and *without* marking the tab dirty.
// Used both for the silent auto-refresh and the manual "Reload" of a stale tab.
async function reloadEditorFromDisk(abs) {
  const model = editorModels.get(abs);
  if (!model) return;
  const res = await apiFetch(`/api/file?path=${encodeURIComponent(abs)}&session=${encodeURIComponent(activeSessionId || "")}`);
  const text = res.ok ? await res.text() : "";
  if (text === model.getValue()) { // no real change — just clear any stale flag
    editorStale.delete(abs);
    editorDirty.set(abs, false);
    for (const p of panelsWithTab(editorKey(abs))) renderPaneTabs(p);
    return;
  }
  const pane = paneShowingEditor(abs);
  const view = pane && pane._editor ? pane._editor.saveViewState() : null;
  editorApplyingExternal.add(abs);
  try {
    // Full-range replace keeps the model identity (and undo stack) rather than
    // setValue's hard reset, so the editor reflows in place.
    model.pushEditOperations([], [{ range: model.getFullModelRange(), text }], () => null);
  } finally {
    editorApplyingExternal.delete(abs);
  }
  if (pane && pane._editor && view) pane._editor.restoreViewState(view);
  editorDirty.set(abs, false);
  editorStale.delete(abs);
  for (const p of panelsWithTab(editorKey(abs))) { renderPaneTabs(p); updateEditorStaleUI(p); }
}

// onAgentFileChanged reacts to a `file_changed` SSE event (the agent wrote to a
// file on disk). If we have that file open in an editor tab: refresh it live
// when there are no unsaved edits, otherwise flag it stale so the user can
// reload (or keep their edits) without us silently clobbering their work.
function onAgentFileChanged(abs) {
  if (!abs || !editorModels.has(abs)) return;
  if (editorDirty.get(abs)) {
    editorStale.set(abs, true);
    for (const p of panelsWithTab(editorKey(abs))) updateEditorStaleUI(p);
    return;
  }
  reloadEditorFromDisk(abs);
}

// updateEditorStaleUI toggles the "changed on disk" banner for a pane whose
// active editor tab has been flagged stale (unsaved-edits collision case).
function updateEditorStaleUI(panel) {
  const bar = panel.els.editorStale;
  if (!bar) return;
  const key = panel.activeTab;
  const stale = isEditorTab(key) && editorStale.get(editorPathOf(key));
  bar.hidden = !stale;
}

// openFileInEditor opens a Folders-panel file (relative to foldersDir) as an
// editor tab. If already open anywhere it focuses that tab; otherwise it opens
// in the focused pane (replacing an active draft slot when present).
// absForRel resolves a Folders-panel entry path (relative to the panel's
// current dir) to an absolute host path.
function absForRel(rel) {
  const root = (foldersDir || "").replace(/\/+$/, "");
  return root ? root + "/" + rel : "/" + rel;
}

function openFileInEditor(rel) {
  const abs = absForRel(rel);
  const key = editorKey(abs);
  // Remember the Folders-panel ROOT this file was opened from, so when the
  // (sessionless) editor tab is activated the panel stays anchored to that dir
  // instead of snapping back to the global "no session" root (where omnis-server
  // was started). Only set on first open — a later refocus keeps the original.
  if (!editorRoots.has(abs)) editorRoots.set(abs, foldersDir || "");
  const existing = panelsWithTab(key)[0];
  if (existing) { setFocusedPanel(existing.id); activateTab(existing, key); return; }
  const panel = focusedPanel() || panels[0];
  if (!panel) return;
  if (!panel.tabs.includes(key)) {
    const ai = panel.tabs.indexOf(panel.activeTab);
    if (isDraft(panel.activeTab) && ai !== -1) panel.tabs[ai] = key;
    else panel.tabs.push(key);
  }
  activateTab(panel, key);
}

// Keep open editors' (and terminals') theme in sync with the app theme
// (settings.js toggles the <html> data-theme attribute).
new MutationObserver(() => {
  if (window.monaco && window.monaco.editor) window.monaco.editor.setTheme(monacoTheme());
  for (const entry of termTabs.values()) { try { entry.term.options.theme = xtermTheme(); } catch (_) {} }
}).observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

// ─── Terminal tabs (xterm.js + PTY WebSocket) ─────────────────────────────────
// A fourth pane-tab kind ("term#<n>") runs a real interactive shell over a
// WebSocket to /api/terminal/ws (PTY-backed on the server). Unlike editor tabs
// these are EPHEMERAL — stripped from the persisted layout — because the server
// PTY does not survive a page reload. Each terminal tab owns its own xterm.js
// instance + WebSocket + detached host element, kept alive while the tab sits in
// the background (output keeps streaming into the scrollback buffer).

const TERM_TAB_PREFIX = "term#";
function isTermTab(key) { return typeof key === "string" && key.startsWith(TERM_TAB_PREFIX); }
let termSeq = 0;
function newTermKey() { return TERM_TAB_PREFIX + (++termSeq); }

const termTabs = new Map(); // term key → { term, fit, ws, host }
const termOpts = new Map(); // term key → { sid, cwd } resolved at open time

// xtermTheme maps the active app theme to an xterm.js colour theme.
function xtermTheme() {
  const light = (document.documentElement.getAttribute("data-theme") || "").toLowerCase().includes("light");
  return light
    ? { background: "#ffffff", foreground: "#1e1e1e", cursor: "#1e1e1e", selectionBackground: "#bcd6f7" }
    : { background: "#000000", foreground: "#e6e6e6", cursor: "#e6e6e6", selectionBackground: "#3a3d41" };
}

// ensureXterm lazily injects the vendored xterm.js + fit addon + stylesheet
// (served at assets/xterm/… like the vendored Monaco). Cached so it loads once.
let _xtermPromise = null;
function ensureXterm() {
  if (_xtermPromise) return _xtermPromise;
  _xtermPromise = new Promise((resolve, reject) => {
    if (window.Terminal && window.FitAddon) { resolve(); return; }
    const base = new URL("assets/xterm", document.baseURI).href.replace(/\/$/, "");
    const css = document.createElement("link");
    css.rel = "stylesheet";
    css.href = base + "/xterm.css";
    document.head.appendChild(css);
    const load = (src) => new Promise((res, rej) => {
      const s = document.createElement("script");
      s.src = src; s.onload = res; s.onerror = () => rej(new Error("failed to load " + src));
      document.head.appendChild(s);
    });
    load(base + "/xterm.js")
      .then(() => load(base + "/xterm-addon-fit.js"))
      .then(resolve)
      .catch(reject);
  });
  return _xtermPromise;
}

function termWsUrl(opts) {
  const u = new URL("api/terminal/ws", document.baseURI);
  u.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  if (opts && opts.sid) u.searchParams.set("session", opts.sid);
  if (opts && opts.cwd) u.searchParams.set("cwd", opts.cwd);
  if (token) u.searchParams.set("token", token);
  return u.href;
}

function sendTermResize(entry) {
  if (!entry || !entry.ws || entry.ws.readyState !== WebSocket.OPEN) return;
  const t = entry.term;
  if (t.cols > 0 && t.rows > 0) entry.ws.send(JSON.stringify({ cols: t.cols, rows: t.rows }));
}

// onTerminalCwd handles a server-reported shell working-directory change: it
// remembers it on the entry and, while this terminal is the visible (focused-
// pane active) tab and the Folders panel is open, navigates the panel to follow
// `cd`. Since a terminal tab has no active chat session, `loadFolder` targets the
// global folder cwd (the same dir the panel shows with no session), so the panel
// and the global "no session" environment both track the terminal.
function onTerminalCwd(key, dir) {
  const entry = termTabs.get(key);
  if (entry) entry.cwd = dir;
  const fpr = focusedPanel();
  if (!fpr || fpr.activeTab !== key) return;     // not the visible terminal
  if (foldersCollapsed()) return;                 // panel closed — nothing to move
  if (!dir || dir === foldersDir) return;         // already there
  loadFolder(dir);
}

// createTerminal builds the xterm instance + WebSocket for a term tab (once).
async function createTerminal(key) {
  await ensureXterm();
  if (termTabs.has(key)) return termTabs.get(key);
  const opts = termOpts.get(key) || {};
  const host = document.createElement("div");
  host.style.width = "100%";
  host.style.height = "100%";
  const term = new window.Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    theme: xtermTheme(),
    scrollback: 5000,
  });
  const fit = new window.FitAddon.FitAddon();
  term.loadAddon(fit);
  // NOTE: do NOT term.open(host) here — the host is still detached, and xterm
  // initialised on a 0×0 element renders nothing (a black pane where even the
  // shell prompt and error text are invisible). open() happens in mountTerminal
  // once the host is attached to the visible pane.

  const ws = new WebSocket(termWsUrl(opts));
  ws.binaryType = "arraybuffer";
  const entry = { term, fit, ws, host, opened: false };
  termTabs.set(key, entry);

  const enc = new TextEncoder();
  ws.onopen = () => sendTermResize(entry);
  ws.onmessage = (ev) => {
    // Binary frames are raw PTY output; text frames are control JSON (cwd sync).
    if (ev.data instanceof ArrayBuffer) { term.write(new Uint8Array(ev.data)); return; }
    try {
      const m = JSON.parse(ev.data);
      if (m && typeof m.cwd === "string") { onTerminalCwd(key, m.cwd); return; }
    } catch (_) { /* not control JSON — fall through to write as text */ }
    term.write(ev.data);
  };
  ws.onclose = () => term.write("\r\n\x1b[2m[terminal session ended]\x1b[0m\r\n");
  ws.onerror = () => term.write("\r\n\x1b[31m[terminal connection error]\x1b[0m\r\n");
  term.onData((d) => { if (ws.readyState === WebSocket.OPEN) ws.send(enc.encode(d)); });
  term.onResize(() => sendTermResize(entry));
  return entry;
}

// mountTerminal shows term tab `key` in `panel`, creating it on first use, and
// moves its host element into the pane's terminal host.
async function mountTerminal(panel, key) {
  let entry;
  try { entry = await createTerminal(key); }
  catch (e) { setStatus(panel, tr("app.terminal.loadFailed", { error: e })); return; }
  if (panel.activeTab !== key) return; // tab switched away while xterm loaded
  const hostWrap = panel.els.terminalHost;
  if (!hostWrap) return;
  while (hostWrap.firstChild) hostWrap.removeChild(hostWrap.firstChild);
  hostWrap.appendChild(entry.host);
  // Open xterm only once, and only now that the host is attached + visible so the
  // renderer measures real dimensions (see the NOTE in createTerminal).
  if (!entry.opened) { entry.term.open(entry.host); entry.opened = true; }
  requestAnimationFrame(() => {
    try { entry.fit.fit(); } catch (_) {}
    sendTermResize(entry);
    entry.term.focus();
    // Align the Folders panel to this terminal's last-known cwd on (re)activation.
    if (entry.cwd && !foldersCollapsed() && entry.cwd !== foldersDir) loadFolder(entry.cwd);
  });
}

// refitVisibleTerminals re-fits the xterm grid of every pane currently showing
// a terminal tab (called after a pane/window resize) and pushes the new size to
// the PTY so server-side programs re-wrap.
function refitVisibleTerminals() {
  for (const panel of panels) {
    if (!isTermTab(panel.activeTab)) continue;
    const entry = termTabs.get(panel.activeTab);
    if (!entry) continue;
    try { entry.fit.fit(); } catch (_) {}
    sendTermResize(entry);
  }
}

function disposeTerminal(key) {
  const entry = termTabs.get(key);
  termOpts.delete(key);
  if (!entry) return;
  try { entry.ws.close(); } catch (_) {}
  try { entry.term.dispose(); } catch (_) {}
  if (entry.host && entry.host.parentNode) entry.host.parentNode.removeChild(entry.host);
  termTabs.delete(key);
}

// openTerminalTab opens a new terminal tab in `panel` (default: focused pane).
// `opts` chooses the working directory: { cwd } for an explicit dir (Folders
// "Open Terminal here") or { sid } to inherit a chat session's cwd. When the
// active tab is a pending draft, the terminal takes that slot in place.
function openTerminalTab(panel, opts) {
  panel = panel || focusedPanel() || panels[0];
  if (!panel) return;
  const key = newTermKey();
  termOpts.set(key, opts || { sid: activeSessionId || "" });
  if (!panel.tabs.includes(key)) {
    const ai = panel.tabs.indexOf(panel.activeTab);
    if (isDraft(panel.activeTab) && ai !== -1) panel.tabs[ai] = key;
    else panel.tabs.push(key);
  }
  activateTab(panel, key);
}

// ─── Helpers ────────────────────────────────────────────────────────────────

function setStatus(panel, s) {
  if (!panel || !panel.els.status) return;
  panel.els.status.textContent = s;
  panel.els.status.classList.toggle("active", s !== "");
}

// fpTranscript returns the focused pane's transcript element (fallback target
// for append helpers that are normally handed an explicit container).
function fpTranscript() {
  const p = fp();
  return p ? p.els.transcript : null;
}

// paneOfNode resolves the panel a DOM node currently lives in (or null when the
// node belongs to a session not mounted in any pane). Used by append helpers to
// scroll the right pane regardless of focus.
// sessionIdOfNode maps a DOM node back to the session whose (possibly detached,
// background-tab) container holds it. Used when paneOfNode can't resolve it.
function sessionIdOfNode(node) {
  if (!node) return null;
  for (const [sid, c] of sessionContainers) {
    if (c.contains(node)) return sid;
  }
  return null;
}

function paneOfNode(node) {
  const root = node && node.closest ? node.closest(".chat-pane") : null;
  return root ? getPanel(root.dataset.panelId) : null;
}

function authHeaders(extra = {}) {
  if (!token) return { ...extra };
  return { ...extra, "Authorization": `Bearer ${token}` };
}

function escHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// Sticky-bottom autoscroll. The user is "pinned" to the bottom as long as
// their scroll position is within STICK_THRESHOLD_PX of the end. While
// pinned, streaming output keeps the view at the bottom. As soon as they
// scroll up to read earlier content, we stop yanking them back — new tokens
// keep arriving below their viewport without disturbing their position.
// User-initiated actions (send, switch session, load history) call
// scrollBottom(true) to re-pin unconditionally.
const STICK_THRESHOLD_PX = 80;

function isAtBottom(el) {
  return (el.scrollHeight - el.scrollTop - el.clientHeight) <= STICK_THRESHOLD_PX;
}

// scrollBottom keeps a pane pinned to the bottom while it is "stuck" (see the
// per-pane scroll listener in attachPaneHandlers). Stickiness is per-pane so
// streaming in one pane never yanks another pane's scroll position.
function scrollBottom(panel, force = false) {
  if (!panel || !panel.els.transcript) return;
  if (!force && !panel._stick) return;
  if (panel._scrollPending) return;
  panel._scrollPending = true;
  requestAnimationFrame(() => {
    panel._scrollPending = false;
    panel.els.transcript.scrollTop = panel.els.transcript.scrollHeight;
    panel._stick = true;
  });
}

// ─── Tool metadata ──────────────────────────────────────────────────────────

const TOOL_META = [
  { match: /^bash/,              label: "Bash",     color: "amber"   },
  { match: /^read/,              label: "Read",     color: "sky"     },
  { match: /^write/,             label: "Write",    color: "emerald" },
  { match: /^edit/,              label: "Edit",     color: "teal"    },
  { match: /^grep/,              label: "Grep",     color: "purple"  },
  { match: /^glob/,              label: "Glob",     color: "purple"  },
  { match: /^revert/,            label: "Revert",   color: "rose"    },
  { match: /^load_skill/,        label: "Skill",    color: "orange"  },
  { match: /^list_skill/,        label: "Skills",   color: "orange"  },
  { match: /^load_softskill/,    label: "Softskill", color: "orange" },
  { match: /^list_softskill/,    label: "Softskills",color: "orange" },
  { match: /^task/,              label: "Task",     color: "indigo"  },
  { match: /^todo/,              label: "Todo",     color: "indigo"  },
  { match: /^teammate/,          label: "Teammate", color: "indigo"  },
  { match: /^worktree/,          label: "Worktree", color: "indigo"  },
  { match: /^bg/,                label: "BG",       color: "slate"   },
];

function toolMeta(name) {
  const n = (name || "").toLowerCase();
  for (const m of TOOL_META) {
    if (m.match.test(n)) return m;
  }
  const label = name
    .replace(/_/g, " ")
    .replace(/\b\w/g, c => c.toUpperCase());
  return { label, color: "slate" };
}

function toolDesc(name, args) {
  if (!args || typeof args !== "object") return "";
  if (args.command) {
    const c = String(args.command).replace(/\n/g, " ").trim();
    return c.length > 80 ? c.slice(0, 80) + "…" : c;
  }
  if (args.request) {
    const r = String(args.request).replace(/\n/g, " ").trim();
    return r.length > 80 ? r.slice(0, 80) + "…" : r;
  }
  if (args.file_path) return args.file_path;
  if (args.path) return args.path;
  if (args.pattern) {
    const loc = args.directory || args.path || "";
    return loc ? `${args.pattern}  in ${loc}` : args.pattern;
  }
  if (args.name) return args.name;
  return "";
}

function extractResponse(response) {
  if (typeof response === "string") return response;
  if (!response || typeof response !== "object") return JSON.stringify(response, null, 2);
  // Skills/softskills: parse XML into a readable list
  for (const k of ["skills", "softskills"]) {
    if (typeof response[k] === "string") return formatSkillsList(response[k]);
  }
  // Common single-key wrappers the tool functions return
  for (const k of ["output", "content", "matches", "result", "results", "markdown", "text", "plan"]) {
    if (typeof response[k] === "string") return response[k];
  }
  return JSON.stringify(response, null, 2);
}

function formatSkillsList(xml) {
  const txt = xml
    .replace(/&#34;/g, '"').replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'").replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<").replace(/&gt;/g, ">");
  const skills = [];
  const re = /<skill>\s*<name>([\s\S]*?)<\/name>\s*<description>([\s\S]*?)<\/description>\s*<\/skill>/g;
  let m;
  while ((m = re.exec(txt)) !== null) {
    skills.push({ name: m[1].trim(), description: m[2].trim() });
  }
  if (!skills.length) return txt;
  return skills.map(s => `${s.name}\n  ${s.description}`).join("\n\n");
}

// "how-the-library-works" is the curator's own meta-procedure — it explains
// the soft-skill system to the lead agent itself and is not user-facing
// content. We strip it from list_softskills chips: if it's the only entry,
// the chip is suppressed entirely; otherwise it's filtered out of the list.
const CURATOR_META_SOFTSKILL = "how-the-library-works";

function countSoftskillsExcludingCurator(xml) {
  const re = /<skill>\s*<name>([\s\S]*?)<\/name>/g;
  let kept = 0, total = 0, m;
  while ((m = re.exec(xml)) !== null) {
    total++;
    if (m[1].trim() !== CURATOR_META_SOFTSKILL) kept++;
  }
  return { kept, total };
}

function stripCuratorSoftskill(xml) {
  const re = /<skill>\s*<name>([\s\S]*?)<\/name>[\s\S]*?<\/skill>\s*/g;
  return xml.replace(re, (match, name) =>
    name.trim() === CURATOR_META_SOFTSKILL ? "" : match);
}

// ─── Markdown ────────────────────────────────────────────────────────────────

// Escape raw HTML blocks so agent output cannot inject scripts via markdown.
if (typeof marked !== "undefined") {
  marked.use({ renderer: { html(token) { return escHtml(token.raw); } } });
}

function renderMarkdown(el, text) {
  if (typeof marked === "undefined") {
    el.textContent = text;
    return;
  }
  const t0 = AgentDebug.enabled ? performance.now() : 0;
  el._rawText = text || "";
  el.innerHTML = marked.parse(text || "");
  el.classList.add("rendered");
  if (el._stream) el._stream = null;
  if (AgentDebug.enabled) AgentDebug.render(performance.now() - t0);
  rewriteLocalImages(el);
  linkifyFilePaths(el);
}

// ─── Local image rendering ───────────────────────────────────────────────────
// The agent may reference image files generated on disk (e.g. by the
// image_generator sub-agent or an MCP tool). Markdown like `![](path)` parses
// to <img src="path">, but the browser can't load that path directly: the
// server enforces auth and the path may live in /tmp. We rewrite each local
// <img> to a blob URL by fetching the bytes through the authenticated
// /api/sessions/<id>/media endpoint, so the token never leaks into URLs.

const mediaBlobCache = new Map(); // key = sessionId|path → object URL

function isRemoteOrInlineSrc(src) {
  return /^(https?:|data:|blob:|\/api\/|\/assets\/)/i.test(src);
}

// Recognised on-disk image extensions (mirrors imageMIME in server/media.go).
const LOCAL_IMG_EXT_RE = /\.(png|jpe?g|gif|webp)$/i;

// A markdown <img> src is fetched through the authenticated media proxy only
// when it really looks like a local image file: not a remote/inline/own-route
// URL, not protocol-relative, no query string or fragment, and a recognised
// image extension. This is what keeps fetched web-page images — notably
// Next.js's site-relative `/_next/image?url=…&w=640&q=75` — from being sprayed
// at /api/sessions/<id>/media (where they 403/404, since they are not files on
// disk). Agent-generated images (e.g. /tmp/omnis-images/abc.png, whether given
// as an absolute or relative path) still pass and are proxied as before.
function looksLikeLocalImagePath(src) {
  if (!src) return false;
  if (isRemoteOrInlineSrc(src)) return false;
  const path = src.replace(/^file:\/\//, "");
  if (path.startsWith("//")) return false;            // protocol-relative remote URL
  if (path.includes("?") || path.includes("#")) return false; // query/fragment ⇒ web URL
  return LOCAL_IMG_EXT_RE.test(path);
}

async function fetchMediaBlobURL(sessionId, path) {
  const key = sessionId + "|" + path;
  if (mediaBlobCache.has(key)) {
    const cached = mediaBlobCache.get(key);
    if (cached === null) throw new Error("not available");
    return cached;
  }
  const url = `/api/sessions/${encodeURIComponent(sessionId)}/media?path=${encodeURIComponent(path)}`;
  const res = await apiFetch(url);
  if (!res.ok) {
    let detail = `${res.status}`;
    try { const j = await res.json(); if (j && j.error) detail = j.error; } catch (_) {}
    mediaBlobCache.set(key, null); // cache failures to avoid repeated requests
    throw new Error(detail);
  }
  const blob = await res.blob();
  const objURL = URL.createObjectURL(blob);
  mediaBlobCache.set(key, objURL);
  return objURL;
}

function rewriteLocalImages(rootEl) {
  if (!rootEl) return;
  // Resolve the session that owns this DOM so media fetches use the right
  // session. A background tab's container is detached from every pane, so fall
  // back to the session-container map before the focused-session shim.
  const ownerPanel = paneOfNode(rootEl);
  const sessionId = (ownerPanel && ownerPanel.sessionId) || sessionIdOfNode(rootEl) || activeSessionId;
  const imgs = rootEl.querySelectorAll("img");
  imgs.forEach(img => {
    let src = img.getAttribute("src") || "";
    if (isRemoteOrInlineSrc(src)) return; // remote/inline — the browser loads it directly
    if (!looksLikeLocalImagePath(src)) {
      // Neither a remote URL the browser can load nor a file we can proxy —
      // i.e. an unresolvable site-relative URL from a fetched web page (e.g.
      // /_next/image?url=…). Left in place it would fire a doomed direct
      // request (404 here) and show a broken-image icon. Running synchronously
      // right after innerHTML is set, removing it now means the browser never
      // requests the original src at all.
      img.remove();
      return;
    }
    if (!sessionId) return;
    src = src.replace(/^file:\/\//, "");
    img.classList.add("local-media");
    fetchMediaBlobURL(sessionId, src).then(url => {
      img.src = url;
    }).catch(err => {
      img.classList.add("local-media-error");
      const msg = String(err && err.message ? err.message : err);
      img.replaceWith(Object.assign(document.createElement("span"), {
        className: "local-media-error-msg",
        textContent: `[image unavailable: ${src} — ${msg}]`,
      }));
    });
  });
}

// ─── Downloadable file paths in assistant replies ───────────────────────────
// When the agent produces a deliverable that it did NOT create with the Write
// tool — e.g. a .docx/.pdf/.zip exported via pandoc/zip in a Bash command, which
// fires no `file_changed` event — there is no download card. But the agent
// almost always *names* the produced file in its reply, either as an inline-code
// path (``Fichier produit : `/tmp/report.docx` ``) or in prose ("Fichier :
// /tmp/report.docx (≈ 12 Ko)"). We scan both, and for any path that points at a
// real file on disk add a small download button right after it. Runs on both the
// live finalize and history replay (renderMarkdown), so unlike the Write card
// these survive a reload.

// Small download glyph (down-arrow into a tray), inherits currentColor.
const DOWNLOAD_ICON_SVG =
  `<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" ` +
  `stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` +
  `<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>` +
  `<polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`;

// An ABSOLUTE filesystem path ending in a file extension. Relative paths are
// deliberately excluded so we don't decorate every source file mentioned in a
// coding chat (src/main.go, package.json, …) — generated deliverables almost
// always land at an absolute path (/tmp/…, an export dir, a Windows drive).
// `ABS_FILE_PATH_RE` matches a whole inline-code span; `EMBED_FILE_PATH_RE` (the
// global form) extracts such paths embedded in prose text.
const ABS_FILE_PATH_RE = /^(?:[A-Za-z]:[\\/]|\/)[^\s'"<>()[\]]+\.[A-Za-z0-9]{1,12}$/;
const EMBED_FILE_PATH_RE = /(?:[A-Za-z]:[\\/]|\/)[^\s'"<>()[\]]+\.[A-Za-z0-9]{1,12}/g;

// makeInlineDlBtn builds the small download button placed after a produced-file
// path; clicking it downloads the file through the session's folder route.
function makeInlineDlBtn(path, sessionId) {
  const name = path.split("/").filter(Boolean).pop() || path;
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "inline-dl-btn";
  btn.setAttribute("data-tip", `${tr("menu.download")} — ${name}`);
  btn.setAttribute("aria-label", `${tr("menu.download")} ${name}`);
  btn.innerHTML = DOWNLOAD_ICON_SVG;
  btn.addEventListener("click", () => downloadHostFile(path, name, sessionId));
  return btn;
}

function linkifyFilePaths(rootEl) {
  if (!rootEl) return;
  // Resolve the session that owns this DOM (mirrors rewriteLocalImages) so the
  // resolve + download routes use the right working directory.
  const ownerPanel = paneOfNode(rootEl);
  const sessionId = (ownerPanel && ownerPanel.sessionId) || sessionIdOfNode(rootEl) || activeSessionId;
  if (!sessionId) return;

  // Pass 1 — inline code spans whose entire text is an absolute file path.
  const codeTargets = [];
  rootEl.querySelectorAll("code").forEach(code => {
    if (code.closest("pre")) return;        // skip fenced code blocks (snippets)
    if (code.dataset.dlChecked) return;     // idempotent across repeat calls
    const p = (code.textContent || "").trim();
    if (!ABS_FILE_PATH_RE.test(p)) return;
    code.dataset.dlChecked = "1";
    codeTargets.push({ code, path: p });
  });

  // Pass 2 — absolute paths embedded in prose text nodes (not inside code/pre/
  // links/already-decorated paths). Snapshot the matches now; mutate after the
  // async resolve so the live TreeWalker isn't invalidated mid-walk.
  const textTargets = []; // { node, matches: [{path, start, end}] }
  const walker = document.createTreeWalker(rootEl, NodeFilter.SHOW_TEXT, {
    acceptNode(n) {
      if (!n.nodeValue || n.nodeValue.indexOf("/") === -1) return NodeFilter.FILTER_REJECT;
      if (n.parentElement && n.parentElement.closest("code, pre, a, .dl-path, .inline-dl-btn")) {
        return NodeFilter.FILTER_REJECT;
      }
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const text = n.nodeValue;
    EMBED_FILE_PATH_RE.lastIndex = 0;
    const matches = [];
    let m;
    while ((m = EMBED_FILE_PATH_RE.exec(text)) !== null) {
      // A genuine path mention starts at a boundary (start, whitespace, or an
      // opening delimiter). This rejects mid-token matches — the "/main.go" tail
      // of a relative "src/main.go", the "s://" of "https://…", a "./out/a.pdf"
      // — and protocol-relative "//host/…" URLs.
      const before = m.index > 0 ? text[m.index - 1] : "";
      const atBoundary = before === "" || /[\s([{<"'=]/.test(before);
      if (!atBoundary || m[0].startsWith("//")) continue;
      matches.push({ path: m[0], start: m.index, end: m.index + m[0].length });
    }
    if (matches.length) textTargets.push({ node: n, matches });
  }

  const allPaths = [...new Set([
    ...codeTargets.map(t => t.path),
    ...textTargets.flatMap(t => t.matches.map(mm => mm.path)),
  ])];
  if (!allPaths.length) return;

  apiFetch("/api/fileref/resolve", {
    method: "POST",
    body: JSON.stringify({ paths: allPaths, session: sessionId }),
  }).then(r => r.json()).then(data => {
    const kinds = (data && data.kinds) || {};
    const isFile = p => kinds[p] === "file";

    // Apply: inline-code spans.
    codeTargets.forEach(({ code, path }) => {
      if (!isFile(path) || !code.parentNode) return;
      const sib = code.nextElementSibling;
      if (sib && sib.classList && sib.classList.contains("inline-dl-btn")) return;
      code.classList.add("dl-path");
      code.insertAdjacentElement("afterend", makeInlineDlBtn(path, sessionId));
    });

    // Apply: prose text nodes — rebuild each into [text][span.dl-path][btn][text].
    textTargets.forEach(({ node, matches }) => {
      const hits = matches.filter(mm => isFile(mm.path));
      if (!hits.length || !node.parentNode) return;
      const text = node.nodeValue;
      const frag = document.createDocumentFragment();
      let last = 0;
      hits.forEach(({ path, start, end }) => {
        if (start < last) return; // defensive: ignore overlaps
        frag.appendChild(document.createTextNode(text.slice(last, start)));
        const span = document.createElement("span");
        span.className = "dl-path";
        span.textContent = text.slice(start, end);
        frag.appendChild(span);
        frag.appendChild(makeInlineDlBtn(path, sessionId));
        last = end;
      });
      frag.appendChild(document.createTextNode(text.slice(last)));
      node.parentNode.replaceChild(frag, node);
    });
  }).catch(() => {});
}

// Heuristic extractor: returns local-filesystem paths referenced anywhere in
// a tool-result payload, restricted to known image extensions. Used to surface
// thumbnails directly in the tool-result chip even when the leader hasn't yet
// included markdown image syntax in its reply.
//
// Two extraction modes per visited string:
//   1. The whole string IS a path (no whitespace, ends in image extension).
//      e.g. response.image_path = "/tmp/omnis-images/abc.png".
//   2. The string is a sentence that EMBEDS a path. We pull each substring
//      that starts with "/" (or a Windows drive) and ends in an image
//      extension. e.g. "Generated image saved to /tmp/omnis-images/abc.png".
function collectImagePathsFromResponse(response) {
  if (!response || typeof response !== "object") return [];
  const found = new Set();
  // Bare-path: the entire string is the path.
  const isBarePath = s => /^[^\s'"<>()\[\]]+\.(png|jpe?g|gif|webp)(\?[^\s]*)?$/i.test(s);
  // Embedded-path: extract paths that begin at "/" (POSIX) or "X:\" (Windows).
  const embeddedRe = /(?:[A-Za-z]:[\\/]|\/)[^\s'"<>()\[\]]+\.(?:png|jpe?g|gif|webp)(?:\?[^\s'"<>()\[\]]*)?/gi;
  const visit = node => {
    if (!node) return;
    if (typeof node === "string") {
      if (isBarePath(node)) {
        found.add(node);
      } else {
        const matches = node.match(embeddedRe);
        if (matches) matches.forEach(m => found.add(m));
      }
      return;
    }
    if (Array.isArray(node)) { node.forEach(visit); return; }
    if (typeof node === "object") {
      for (const v of Object.values(node)) visit(v);
    }
  };
  visit(response);
  return Array.from(found);
}

// ─── Incremental markdown streaming ──────────────────────────────────────────
// Strategy: keep already-closed blocks rendered as HTML, append new tokens to
// a single trailing "tail" node (Text or <pre><code>). When a block boundary
// (blank line outside a fence) or a closing fence appears, parse just that
// block with marked.parse and insert the result before the tail. Each parse
// is O(block-size) instead of O(message²), so cost stays bounded as the
// response grows. The tail keeps streaming at wire speed between flushes.

// Inline-only markdown for one line of the streaming tail: emphasis + inline
// code. Inline code is first swapped for private-use sentinels (\uE000<n>\uE001
// — they survive escaping and never occur in real text) so emphasis can still
// match across a code span (e.g. **`x`** -> bold wrapping code, like marked),
// then restored verbatim afterwards. Only CLOSED markers render — a half-typed
// "**bo" stays literal until its closer arrives — mirroring marked (GFM) so the
// preview doesn't reflow when the real parser flushes the block.
function lightInline(s) {
  const code = [];
  let t = String(s).replace(/`([^`\n]+)`/g, (_, c) => "\uE000" + (code.push(c) - 1) + "\uE001");
  t = escHtml(t);
  t = t.replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>");
  t = t.replace(/__([^_\n]+)__/g, "<strong>$1</strong>");
  t = t.replace(/(^|[^*])\*([^*\n]+)\*/g, "$1<em>$2</em>");
  t = t.replace(/(^|[^_\w])_([^_\n]+)_/g, "$1<em>$2</em>");
  t = t.replace(/~~([^~\n]+)~~/g, "<del>$1</del>");
  return t.replace(/\uE000(\d+)\uE001/g, (_, i) => "<code>" + escHtml(code[+i]) + "</code>");
}
// Lightweight block+inline renderer for the prose tail (the still-open block).
// It runs on every token, so by design it only ever touches the current block
// — never the whole message — which keeps it bounded (O(block)) and cheap; the
// heavy marked.parse is still reserved for block flush/finalize. It makes the
// in-flight preview readable instead of showing raw syntax: blank-line runs
// collapse (a single blank flushes upstream; extras pile up here), and the
// common block constructs render — ATX headings, unordered/ordered lists,
// horizontal rules, blockquotes — with inline emphasis inside. The HTML mirrors
// marked's tight-list/heading output so the preview doesn't reflow on flush.
function lightStreamMd(text) {
  let html = "";
  let para = [];        // buffered consecutive paragraph lines
  let quote = [];       // buffered consecutive blockquote lines
  let items = [];       // buffered consecutive list items
  let listTag = null;   // "ul" | "ol" for the open list
  const flushPara = () => {
    if (para.length) { html += "<p>" + para.map(lightInline).join("<br>") + "</p>"; para = []; }
  };
  const flushQuote = () => {
    if (quote.length) { html += "<blockquote><p>" + quote.map(lightInline).join("<br>") + "</p></blockquote>"; quote = []; }
  };
  const flushList = () => {
    if (items.length) {
      html += "<" + listTag + ">" + items.map(it => "<li>" + lightInline(it) + "</li>").join("") + "</" + listTag + ">";
      items = []; listTag = null;
    }
  };
  const flushAll = () => { flushPara(); flushQuote(); flushList(); };
  // Within a tail block, content-separating blank lines have already triggered
  // an upstream flush, so the only blanks here are leading/accumulated ones.
  for (const line of String(text || "").split("\n")) {
    if (line.trim() === "") continue;
    let m;
    if ((m = /^(#{1,6})\s+(.*)$/.exec(line))) {
      flushAll();
      const lvl = m[1].length;
      html += "<h" + lvl + ">" + lightInline(m[2]) + "</h" + lvl + ">";
    } else if (/^\s*(?:-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      flushAll();
      html += "<hr>";
    } else if ((m = /^\s*[-*+]\s+(.*)$/.exec(line))) {
      flushPara(); flushQuote();
      if (listTag && listTag !== "ul") flushList();
      listTag = "ul"; items.push(m[1]);
    } else if ((m = /^\s*\d+[.)]\s+(.*)$/.exec(line))) {
      flushPara(); flushQuote();
      if (listTag && listTag !== "ol") flushList();
      listTag = "ol"; items.push(m[1]);
    } else if ((m = /^>\s?(.*)$/.exec(line))) {
      flushPara(); flushList();
      quote.push(m[1]);
    } else {
      flushQuote(); flushList();
      para.push(line);
    }
  }
  flushAll();
  return html;
}
function streamMdInit(bubble) {
  bubble.textContent = "";
  const tail = document.createElement("span");
  tail.className = "md-stream-tail";
  bubble.appendChild(tail);
  bubble._stream = {
    scanIdx: 0,         // chars of segAcc scanned for line boundaries
    blockStart: 0,      // start of the current unfinished block in segAcc
    inFence: false,
    fenceCodeStart: 0,  // start of fenced code body (after opening fence line)
    tailEl: tail,       // outer DOM node holding the streaming text
    tailTextNode: null, // raw Text node for code tails (appendData target)
    tailKind: "text",
    tailSyncedTo: 0,    // segAcc index up to which a code tail mirrors
  };
}

function streamMdReplaceTail(bubble, kind, lang) {
  const s = bubble._stream;
  s.tailEl.remove();
  let newTail, textNode = null;
  if (kind === "code") {
    newTail = document.createElement("pre");
    const code = document.createElement("code");
    if (lang) code.className = "language-" + lang;
    textNode = document.createTextNode("");
    code.appendChild(textNode);
    newTail.appendChild(code);
  } else {
    newTail = document.createElement("span");
    newTail.className = "md-stream-tail";
  }
  bubble.appendChild(newTail);
  s.tailEl = newTail;
  s.tailTextNode = textNode;
  s.tailKind = kind;
  s.tailSyncedTo = kind === "code" ? s.fenceCodeStart : s.blockStart;
}

function streamMdFlushBlock(bubble, text) {
  if (!text || !text.trim()) return;
  const s = bubble._stream;
  const t0 = AgentDebug.enabled ? performance.now() : 0;
  const wrap = document.createElement("div");
  wrap.innerHTML = marked.parse(text);
  rewriteLocalImages(wrap);
  while (wrap.firstChild) bubble.insertBefore(wrap.firstChild, s.tailEl);
  if (AgentDebug.enabled) AgentDebug.render(performance.now() - t0);
}

function streamMdAdvance(bubble, fullText) {
  if (!bubble._stream) streamMdInit(bubble);
  const s = bubble._stream;
  // Process newly-completed lines for state transitions.
  while (true) {
    const nl = fullText.indexOf("\n", s.scanIdx);
    if (nl === -1) break;
    const lineStart = s.scanIdx;
    const line = fullText.slice(lineStart, nl);
    s.scanIdx = nl + 1;
    const fence = /^```([\w+-]*)\s*$/.exec(line);
    if (fence) {
      if (s.inFence) {
        // Closing fence — flush the whole fenced block (incl. opener/closer).
        streamMdFlushBlock(bubble, fullText.slice(s.blockStart, nl + 1));
        s.inFence = false;
        s.blockStart = s.scanIdx;
        streamMdReplaceTail(bubble, "text");
      } else {
        // Opening fence — flush any preceding prose, then switch to code mode.
        if (lineStart > s.blockStart) {
          streamMdFlushBlock(bubble, fullText.slice(s.blockStart, lineStart));
        }
        s.blockStart = lineStart;
        s.inFence = true;
        s.fenceCodeStart = s.scanIdx;
        streamMdReplaceTail(bubble, "code", fence[1]);
      }
    } else if (!s.inFence && line.trim() === "" && lineStart > s.blockStart) {
      // Blank line outside a fence — promote the block before it and reset
      // the tail node so its previous content doesn't duplicate the new HTML.
      streamMdFlushBlock(bubble, fullText.slice(s.blockStart, lineStart));
      s.blockStart = s.scanIdx;
      streamMdReplaceTail(bubble, "text");
    }
  }
  // Update the live tail. Code tails append the raw delta to their Text node
  // at wire speed (code must stay verbatim). Prose tails re-render the current
  // block — bounded, since everything before s.blockStart is already flushed —
  // through the lightweight inline renderer so emphasis/code show and blank
  // lines collapse during the stream instead of waiting for the block flush.
  if (s.tailKind === "code") {
    const delta = fullText.slice(s.tailSyncedTo);
    if (delta) s.tailTextNode.appendData(delta);
    s.tailSyncedTo = fullText.length;
  } else {
    s.tailEl.innerHTML = lightStreamMd(fullText.slice(s.blockStart));
  }
}

function streamMdFinalize(bubble, fullText) {
  bubble._rawText = fullText;
  if (!bubble._stream) {
    renderMarkdown(bubble, fullText);
    return;
  }
  // Drain any remaining unprocessed text via streamMdAdvance, then flush
  // whatever is still buffered in the tail (possibly an unclosed fence).
  streamMdAdvance(bubble, fullText);
  const s = bubble._stream;
  let trailing = fullText.slice(s.blockStart);
  if (s.inFence && !/```\s*$/.test(trailing)) trailing += "\n```";
  streamMdFlushBlock(bubble, trailing);
  s.tailEl.remove();
  bubble._stream = null;
  bubble.classList.add("rendered");
  linkifyFilePaths(bubble);
}

// ─── Pinned prompt header ────────────────────────────────────────────────────

// Return the full prompt text for the pinned header; CSS handles 3-line clamping.
function pinnedPromptLabel(text) {
  const s = String(text || "");
  return s.length > 1000 ? s.slice(0, 1000) + "…" : s;
}

// Apply a header mutation while keeping the transcript content visually
// stationary. The floating header is a flex sibling of #transcript, so
// showing/hiding/resizing it steals (or returns) height from the transcript
// — which would otherwise shove every visible line up or down by the header's
// height. That shove is the "jump" users see when the pinned prompt kicks in
// mid-scroll. We measure the height the mutation costs the transcript and
// counter-scroll by the same amount, so the content stays put and the header
// simply appears in the constant gap above it. (This also keeps the activeBubble
// decision in updatePinnedForScroll stable, avoiding a show/hide flicker.)
function withStableScroll(panel, mutate) {
  const t = panel.els.transcript;
  const before = t.clientHeight;
  mutate();
  const delta = before - t.clientHeight; // >0 when header grew
  if (delta) t.scrollTop += delta;
}

// Show the user prompt text in the floating header above the transcript.
// Attachments are intentionally NOT rendered here — they live in the inline
// user bubble so the floating header stays compact.
function setPinnedPrompt(panel, text, _files) {
  const ph = panel.els.promptHeader;
  const label = pinnedPromptLabel(text);
  // Called on every scroll tick — skip the rebuild (and the forced reflow it
  // would cost) when the visible header already shows this prompt.
  if (ph.classList.contains("visible") && ph._pinnedLabel === label) return;
  withStableScroll(panel, () => {
    ph.innerHTML = "";
    if (label) {
      const textEl = document.createElement("span");
      textEl.className = "pinned-prompt-text";
      renderUserText(textEl, label, panel.sessionId);
      ph.appendChild(textEl);
    }
    ph._pinnedLabel = label;
    ph.classList.add("visible");
  });
}

// Trailing punctuation stripped from an "@" reference token — mirrors
// fileref.TrailingTrim on the server so highlighting lines up with inlining.
const FILE_REF_TRAILING_RE = /[.,;:!?)\]}>"']+$/;

// renderUserText fills textEl with the user message, rendering "@path" file
// references (at start or after whitespace, so emails are excluded) as
// tentative links. Validity is resolved server-side; valid files/dirs become
// openable links and the rest are downgraded to plain text.
function renderUserText(textEl, text, sessionId) {
  textEl.textContent = "";
  const re = /(^|\s)@(\S+)/g;
  let last = 0, m;
  const refs = [];
  while ((m = re.exec(text)) !== null) {
    let token = m[2];
    const tm = token.match(FILE_REF_TRAILING_RE);
    const trailing = tm ? tm[0] : "";
    if (trailing) token = token.slice(0, token.length - trailing.length);
    if (!token) continue;
    const atIdx = m.index + m[1].length; // index of the "@"
    textEl.appendChild(document.createTextNode(text.slice(last, atIdx)));
    const a = document.createElement("a");
    a.className = "file-ref file-ref-pending";
    a.href = "#";
    a.textContent = "@" + token;
    refs.push({ anchor: a, token });
    textEl.appendChild(a);
    if (trailing) textEl.appendChild(document.createTextNode(trailing));
    last = atIdx + 1 + token.length + trailing.length;
  }
  textEl.appendChild(document.createTextNode(text.slice(last)));
  if (refs.length) resolveFileRefs(refs, sessionId);
}

// resolveFileRefs asks the server to classify the referenced paths, then turns
// valid files/dirs into openable links and replaces invalid ones with plain text.
async function resolveFileRefs(refs, sessionId) {
  const paths = [...new Set(refs.map(r => r.token))];
  let kinds = {};
  try {
    const res = await apiFetch("/api/fileref/resolve", {
      method: "POST",
      body: JSON.stringify({ paths, session: sessionId || "" }),
    });
    kinds = (await res.json()).kinds || {};
  } catch { return; }
  for (const { anchor, token } of refs) {
    const kind = kinds[token];
    anchor.classList.remove("file-ref-pending");
    if (kind === "file" || kind === "dir") {
      if (kind === "dir") anchor.classList.add("file-ref-dir");
      anchor.setAttribute("data-tip", token);
      anchor.addEventListener("click", e => { e.preventDefault(); openFileRef(token, sessionId); });
    } else {
      anchor.replaceWith(document.createTextNode(anchor.textContent));
    }
  }
}

// openFileRef fetches a referenced file/dir (with auth) and opens it in a new tab.
async function openFileRef(token, sessionId) {
  const q = new URLSearchParams({ path: token });
  if (sessionId) q.set("session", sessionId);
  try {
    const res = await apiFetch(`/api/file?${q.toString()}`);
    if (!res.ok) return;
    const blob = await res.blob();
    window.open(URL.createObjectURL(blob), "_blank", "noopener");
  } catch { /* ignore */ }
}

// Insert a user message bubble at the current end of the transcript (before streaming).
function appendUserBubble(text, container, files, turnIndex) {
  if (typeof text === "string" && text.startsWith("[mailbox]")) {
    appendMailboxBlock(text, container);
    return;
  }
  // Background-task / monitor completions are injected as a synthetic "user"
  // turn so the model reacts to them, but the "[Background …]" / "[Monitor …]"
  // prompt is an internal message — don't render it as a user bubble. The
  // assistant's reply that follows is the user-facing content.
  if (typeof text === "string" && (text.startsWith("[Background ") || text.startsWith("[Monitor "))) {
    return;
  }
  const sessionId = sessionIdOfNode(container) || (fp() && fp().sessionId) || activeSessionId;
  const row = document.createElement("div");
  row.className = "msg-row msg-row-user";
  const bubble = document.createElement("div");
  bubble.className = "bubble-user";
  bubble.dataset.text = text || "";
  // Mid-turn steering the model folded into this turn is persisted in the prompt
  // as a "[Sent while working]" block; split it back out so the question renders
  // as text and the notes render as chips (matching the live in-flight view).
  const { base, notes } = splitSteerText(text);
  if (base) {
    const textEl = document.createElement("div");
    textEl.className = "bubble-user-text";
    renderUserText(textEl, base, sessionId);
    bubble.appendChild(textEl);
  }
  if (files && files.length > 0) {
    const chips = document.createElement("div");
    chips.className = "bubble-attachments";
    for (const f of files) {
      const chip = document.createElement("span");
      chip.className = "attachment-chip attachment-chip-sent";
      chip.textContent = f.name;
      chip.setAttribute("data-tip", f.path || f.name);
      chips.appendChild(chip);
    }
    bubble.appendChild(chips);
  }
  if (notes.length) {
    bubble.dataset.textOriginal = base;
    bubble._steerNotes = notes.slice();
    renderSteerChips(bubble, notes);
  }
  row.appendChild(bubble);
  // A real (persisted) turn carries its index, so the per-turn fork/rewind
  // control can name an exact cut point. Synthetic echoes (`!` shell, mailbox,
  // background) pass no index and get no control. The index is the turn's
  // position in the conversation file; each render site stamps the true value.
  if (sessionId && Number.isInteger(turnIndex) && turnIndex >= 0) {
    row.dataset.turnIndex = String(turnIndex);
    addTurnActions(row, sessionId, turnIndex, text || "");
  }
  (container || fpTranscript()).appendChild(row);
  // After layout, decide whether the message overflows three lines and, if so,
  // mark it truncated and add a click-to-expand affordance.
  requestAnimationFrame(() => applyUserBubbleTruncation(bubble));
}

// ICON_REWIND is the U-turn "return / undo" arrow for the per-turn control: a
// left-pointing arrowhead whose tail loops down and around the right side.
const ICON_REWIND =
  '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" ' +
  'stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
  '<path d="M8 8h6a5 5 0 0 1 0 10H7"/><polyline points="8 4 4 8 8 12"/></svg>';

// addTurnActions attaches the hover ↺ button to a user-turn row. Clicking it
// opens the fork/rewind menu anchored to the button.
function addTurnActions(row, sessionId, turnIndex, userText) {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "turn-action-btn";
  btn.setAttribute("data-tip", "Fork or rewind from here");
  btn.setAttribute("aria-label", "Fork or rewind from here");
  btn.innerHTML = ICON_REWIND;
  btn.addEventListener("click", (e) => {
    e.preventDefault();
    e.stopPropagation();
    openTurnMenu(btn, sessionId, turnIndex, userText);
  });
  row.appendChild(btn);
}

// ─── Per-turn fork / rewind menu ─────────────────────────────────────────────
// A small body-appended popup anchored to a turn's ↺ button, offering the two
// conversation-branch actions. Dismissed on outside click / Escape / scroll /
// resize (capture-phase so a stopPropagation'd app click can't keep it open).
let _turnMenuEl = null;
function closeTurnMenu() {
  if (_turnMenuEl) { _turnMenuEl.remove(); _turnMenuEl = null; }
  document.removeEventListener("click", _turnMenuDismiss, true);
  document.removeEventListener("contextmenu", _turnMenuDismiss, true);
  document.removeEventListener("keydown", _turnMenuKey, true);
  window.removeEventListener("scroll", closeTurnMenu, true);
  window.removeEventListener("resize", closeTurnMenu, true);
}
function _turnMenuDismiss(e) { if (_turnMenuEl && !_turnMenuEl.contains(e.target)) closeTurnMenu(); }
function _turnMenuKey(e) { if (e.key === "Escape") closeTurnMenu(); }

function openTurnMenu(anchor, sessionId, turnIndex, userText) {
  closeTurnMenu();
  const menu = document.createElement("div");
  menu.className = "turn-menu";
  const items = [
    [tr("menu.fork"), () => forkConversation(sessionId, turnIndex, userText)],
    [tr("menu.rewind"), () => rewindConversation(sessionId, turnIndex, userText)],
  ];
  for (const [label, action] of items) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "turn-menu-item";
    b.textContent = label;
    b.addEventListener("click", (e) => { e.stopPropagation(); closeTurnMenu(); action(); });
    menu.appendChild(b);
  }
  document.body.appendChild(menu);
  // Position below the button, right-aligned to it, clamped to the viewport.
  const r = anchor.getBoundingClientRect();
  const mw = menu.offsetWidth, mh = menu.offsetHeight;
  let left = Math.max(8, Math.min(r.right - mw, window.innerWidth - mw - 8));
  let top = r.bottom + 4;
  if (top + mh > window.innerHeight - 8) top = Math.max(8, r.top - mh - 4);
  menu.style.left = left + "px";
  menu.style.top = top + "px";
  _turnMenuEl = menu;
  setTimeout(() => {
    document.addEventListener("click", _turnMenuDismiss, true);
    document.addEventListener("contextmenu", _turnMenuDismiss, true);
    document.addEventListener("keydown", _turnMenuKey, true);
    window.addEventListener("scroll", closeTurnMenu, true);
    window.addEventListener("resize", closeTurnMenu, true);
  }, 0);
}

// prefillComposer drops `text` into a pane's composer (only when it is empty, so
// a half-typed message is never clobbered) and focuses it — used after rewind /
// fork so the dropped user message is ready to edit & resend.
function prefillComposer(panel, text) {
  if (!panel || !panel.els || !panel.els.prompt || !text) return;
  const el = panel.els.prompt;
  if (el.value.trim()) { el.focus(); return; }
  el.value = text;
  el.focus();
  el.setSelectionRange(el.value.length, el.value.length);
  el.dispatchEvent(new Event("input")); // refresh ref highlight + auto-grow
}

// rewindConversation truncates the live session to before `turnIndex` (dropping
// that turn and everything after), reseeds the model context server-side, then
// re-renders the transcript and pre-fills the composer with the dropped message.
async function rewindConversation(sessionId, turnIndex, userText) {
  const ok = await uiConfirm({
    title: tr("app.rewind.title"),
    message: tr("app.rewind.message"),
    confirmText: tr("app.rewind.confirm"),
    danger: true,
  });
  if (!ok) return;
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/rewind`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ turn_index: turnIndex }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      showToast(err.error || "Rewind failed", "err");
      return;
    }
    await rerenderSessionFromHistory(sessionId);
    for (const p of panelsForSession(sessionId)) prefillComposer(p, userText);
    loadSessions(); // refresh sidebar turn counter
  } catch (e) {
    console.error("rewind failed:", e);
    showToast("Rewind failed", "err");
  }
}

// forkConversation branches a new session from before `turnIndex`, opens it in
// the focused pane, and pre-fills its composer with the dropped message so the
// user can try a different continuation. The source session is left untouched.
async function forkConversation(sessionId, turnIndex, userText) {
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/fork`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ turn_index: turnIndex }),
    });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      showToast(err.error || "Fork failed", "err");
      return;
    }
    const data = await res.json();
    await loadSessions();
    await selectSession(data.session_id);
    const p = panelsWithTab(data.session_id)[0];
    if (p) prefillComposer(p, data.dropped_user_text || userText || "");
    showToast("Forked conversation", "info");
  } catch (e) {
    console.error("fork failed:", e);
    showToast("Fork failed", "err");
  }
}

// applyUserBubbleTruncation clamps long user messages to ~3 lines and adds a
// "Show more / Show less" affordance. Toggling is wired on the indicator only,
// so users can still select text inside the bubble without expanding it.
function applyUserBubbleTruncation(bubble) {
  const textEl = bubble.querySelector(".bubble-user-text");
  if (!textEl) return;
  // Trigger clamp via class so we can measure overflow.
  textEl.classList.add("clamped");
  const overflows = textEl.scrollHeight - textEl.clientHeight > 1;
  if (!overflows) {
    textEl.classList.remove("clamped");
    return;
  }
  bubble.classList.add("bubble-user-truncated");
  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "bubble-user-toggle";
  toggle.textContent = tr("app.msg.showMore");
  toggle.addEventListener("click", (e) => {
    e.stopPropagation();
    const expanded = bubble.classList.toggle("bubble-user-expanded");
    textEl.classList.toggle("clamped", !expanded);
    toggle.textContent = expanded ? tr("app.msg.showLess") : tr("app.msg.showMore");
  });
  bubble.appendChild(toggle);
}

// Parse a mailbox user_text into { from, body }.
// Format: "[mailbox] Cross-session message received:\nFrom: <sender>\nBody: <body>"
function parseMailboxText(text) {
  const fromMatch = text.match(/^From:\s*(.+)$/m);
  const bodyMatch = text.match(/^Body:\s*([\s\S]*)$/m);
  return {
    from: fromMatch ? fromMatch[1].trim() : "unknown",
    body: bodyMatch ? bodyMatch[1].trim() : text,
  };
}

// Render a mailbox push event as a collapsible block, like a tool call.
function appendMailboxBlock(text, container) {
  const { from, body } = parseMailboxText(text);

  const row = document.createElement("div");
  row.className = "tool-row";

  const block = document.createElement("div");
  block.className = "tool-block border-sky";

  const header = document.createElement("div");
  header.className = "tool-header";
  header.innerHTML = `
    <span class="tool-badge badge-sky">Inbox</span>
    <span class="tool-desc">from ${escHtml(from)}</span>
    <span class="tool-chevron">▶</span>
  `;
  header.addEventListener("click", () => block.classList.toggle("expanded"));

  const bodyEl = document.createElement("div");
  bodyEl.className = "tool-body";
  bodyEl.innerHTML = `
    <div class="tool-section">
      <div class="tool-section-label label-in">FROM: ${escHtml(from)}</div>
      <pre class="tool-pre output">${escHtml(body)}</pre>
    </div>
  `;

  block.appendChild(header);
  block.appendChild(bodyEl);
  row.appendChild(block);
  (container || fpTranscript()).appendChild(row);
}

// Update the floating prompt header to show the question whose agent reply is
// currently at the top of the viewport. We pin a question only once its bubble
// has scrolled *completely* above the transcript top (its bottom edge crosses the
// line) — NOT the moment its top edge crosses. Pinning on the top edge meant that
// as soon as the *next* question's top scrolled up, it was pinned into the header
// while its bubble was still visible inline just below it, so the floating panel
// and the inline bubble showed the same prompt twice. Keying on the bottom edge
// keeps the previous question pinned until the next one has fully left the viewport,
// so the header never duplicates a question that's still on screen.
// The header steals height from the transcript when it appears, so transcriptRect.top
// shifts down by the header height once shown; that visibility-dependent line gives
// natural hysteresis (pin at the top line, unpin one header-height lower) so the
// decision can't flicker around the threshold. withStableScroll counter-scrolls the
// height it costs, keeping the content stationary as the bubble becomes the header.
function updatePinnedForScroll(panel) {
  const t = panel.els.transcript;
  const transcriptRect = t.getBoundingClientRect();
  const userBubbles = t.querySelectorAll(".bubble-user");
  let activeBubble = null;
  for (const bubble of userBubbles) {
    const rowRect = bubble.parentElement.getBoundingClientRect();
    if (rowRect.bottom < transcriptRect.top) activeBubble = bubble;
  }
  if (activeBubble !== null) {
    // Pin the question only — steering notes show as chips on the inline bubble.
    const text = activeBubble.dataset.textOriginal ?? activeBubble.dataset.text ?? "";
    let files = [];
    if (activeBubble.dataset.files) {
      try { files = JSON.parse(activeBubble.dataset.files); } catch { files = []; }
    } else {
      const sentChips = activeBubble.querySelectorAll(".attachment-chip-sent");
      files = Array.from(sentChips).map(c => ({ name: c.textContent, path: c.title }));
    }
    setPinnedPrompt(panel, text, files);
  } else {
    clearPinnedPrompt(panel);
  }
}

function clearPinnedPrompt(panel) {
  panel = panel || fp();
  if (!panel) return;
  const ph = panel.els.promptHeader;
  if (!ph.classList.contains("visible") && ph.innerHTML === "") return;
  withStableScroll(panel, () => {
    ph.innerHTML = "";
    ph._pinnedLabel = "";
    ph.classList.remove("visible");
  });
}

// Copy text to the clipboard. The async Clipboard API is only available in
// secure contexts (HTTPS or localhost); when the web UI is served over plain
// HTTP on a LAN address `navigator.clipboard` is undefined, so we fall back to
// the legacy execCommand("copy") via a hidden textarea. Resolves to true on
// success, false otherwise.
function copyTextToClipboard(text) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopy(text));
  }
  return Promise.resolve(fallbackCopy(text));
}

function fallbackCopy(text) {
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.top = "-9999px";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch (_) {
    return false;
  }
}

// ─── Reply export formats ────────────────────────────────────────────────────
// The assistant-reply copy control can export the reply in several "flavors".
// Standard markdown (what the model emits) is GitHub-Flavored and pastes fine
// into GitHub/GitLab/Reddit/Obsidian, but renders as literal symbols in Slack,
// Jira, Outlook/Word, etc. We convert by walking the `marked` token tree.

// COPY_FORMATS drives both the default button (markdown) and the caret menu.
const COPY_FORMATS = [
  ["markdown", "Markdown (default)"],
  ["slack", "Slack"],
  ["jira", "Jira"],
  ["html", "Rich text (HTML)"],
  ["plain", "Plain text"],
];

// Decode the handful of HTML entities `marked` escapes in token text, so the
// plain/Slack/Jira output carries literal characters rather than `&amp;` etc.
function unescapeHtml(s) {
  if (!s) return s || "";
  return s
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#0?39;|&#x27;/gi, "'")
    .replace(/&amp;/g, "&");
}

// lexBlocks tokenizes markdown into `marked`'s block-token tree, tolerating both
// the modern (`marked.lexer`) and legacy (`new marked.Lexer().lex`) APIs.
function lexBlocks(src) {
  try {
    if (typeof marked === "undefined") return null;
    if (typeof marked.lexer === "function") return marked.lexer(src || "");
    if (marked.Lexer) return new marked.Lexer().lex(src || "");
  } catch (_) {}
  return null;
}

// walkInline renders a list of inline tokens using a per-format `rules` object
// (strong/em/del/codespan/link/image). It recurses through nested emphasis.
function walkInline(tokens, rules) {
  if (!tokens) return "";
  let out = "";
  for (const t of tokens) {
    switch (t.type) {
      case "text": out += t.tokens ? walkInline(t.tokens, rules) : unescapeHtml(t.text); break;
      case "escape": out += t.text; break;
      case "strong": out += rules.strong(walkInline(t.tokens, rules)); break;
      case "em": out += rules.em(walkInline(t.tokens, rules)); break;
      case "del": out += rules.del(walkInline(t.tokens, rules)); break;
      case "codespan": out += rules.codespan(unescapeHtml(t.text)); break;
      case "br": out += "\n"; break;
      case "link": out += rules.link(walkInline(t.tokens, rules) || unescapeHtml(t.text), t.href); break;
      case "image": out += rules.image(t.text || "", t.href); break;
      case "html": out += unescapeHtml(t.text || t.raw || ""); break; // keep literal, like the chat renderer
      default: out += t.tokens ? walkInline(t.tokens, rules) : unescapeHtml(t.text || "");
    }
  }
  return out;
}

// cellText renders one table cell, tolerating both modern ({tokens}) and legacy
// (string) table-cell shapes.
function cellText(c, f) {
  if (c == null) return "";
  if (typeof c === "string") return unescapeHtml(c);
  if (c.tokens) return walkInline(c.tokens, f.inline);
  return unescapeHtml(c.text || "");
}

function renderItem(item, f, depth, ordered, idx) {
  const parts = [];
  const nested = [];
  for (const child of item.tokens || []) {
    if (child.type === "list") nested.push(renderList(child, f, depth + 1));
    else if (child.tokens) parts.push(walkInline(child.tokens, f.inline));
    else if (child.text != null) parts.push(unescapeHtml(child.text));
  }
  const lead = parts.join(" ").trim();
  const line = ordered ? f.ordered(depth, idx, lead) : f.bullet(depth, lead);
  return nested.length ? line + "\n" + nested.join("\n") : line;
}

function renderList(list, f, depth) {
  const lines = [];
  let n = (list.ordered && Number.isInteger(list.start)) ? list.start : 1;
  for (const item of list.items || []) {
    lines.push(renderItem(item, f, depth, !!list.ordered, n));
    if (list.ordered) n++;
  }
  return lines.join("\n");
}

// renderBlocks turns a block-token list into flavored text via the format `f`.
function renderBlocks(tokens, f, depth) {
  const out = [];
  for (const t of tokens || []) {
    switch (t.type) {
      case "space": break;
      case "heading": out.push(f.heading(t.depth, walkInline(t.tokens, f.inline))); break;
      case "paragraph": out.push(f.paragraph(walkInline(t.tokens, f.inline))); break;
      case "text": out.push(f.paragraph(t.tokens ? walkInline(t.tokens, f.inline) : unescapeHtml(t.text))); break;
      case "code": out.push(f.code(t.text || "", t.lang || "")); break;
      case "blockquote": out.push(f.quote(renderBlocks(t.tokens, f, depth))); break;
      case "list": out.push(renderList(t, f, depth)); break;
      case "hr": out.push(f.hr()); break;
      case "table": out.push(f.table(
        (t.header || []).map((c) => cellText(c, f)),
        (t.rows || t.cells || []).map((r) => r.map((c) => cellText(c, f))),
      )); break;
      case "html": out.push(unescapeHtml(t.text || t.raw || "")); break; // keep literal, like the chat renderer
      default:
        if (t.tokens) out.push(walkInline(t.tokens, f.inline));
        else if (t.text) out.push(unescapeHtml(t.text));
    }
  }
  return out.filter((s) => s != null && s !== "").join("\n\n");
}

// Tab-separated table for flavors without table markup (Slack, Plain).
function tabTable(header, rows) {
  const lines = [];
  if (header && header.length) lines.push(header.join("\t"));
  for (const r of rows) lines.push(r.join("\t"));
  return lines.join("\n");
}

// Slack "mrkdwn": *bold*, _italic_, ~strike~, `code`, <url|text> links.
const SLACK = {
  inline: {
    strong: (s) => "*" + s + "*",
    em: (s) => "_" + s + "_",
    del: (s) => "~" + s + "~",
    codespan: (s) => "`" + s + "`",
    link: (text, href) => (!text || text === href) ? "<" + href + ">" : "<" + href + "|" + text + ">",
    image: (alt, href) => alt ? "<" + href + "|" + alt + ">" : "<" + href + ">",
  },
  heading: (_lvl, text) => "*" + text + "*",
  paragraph: (text) => text,
  bullet: (depth, text) => "    ".repeat(depth) + "• " + text,
  ordered: (depth, n, text) => "    ".repeat(depth) + n + ". " + text,
  code: (text) => "```\n" + text + "\n```",
  quote: (inner) => inner.split("\n").map((l) => "> " + l).join("\n"),
  hr: () => "──────────",
  table: tabTable,
};

// Jira wiki markup: hN. headings, *bold*, _italic_, {{code}}, {code} blocks.
const JIRA = {
  inline: {
    strong: (s) => "*" + s + "*",
    em: (s) => "_" + s + "_",
    del: (s) => "-" + s + "-",
    codespan: (s) => "{{" + s + "}}",
    link: (text, href) => (!text || text === href) ? "[" + href + "]" : "[" + text + "|" + href + "]",
    image: (_alt, href) => "!" + href + "!",
  },
  heading: (lvl, text) => "h" + Math.min(Math.max(lvl, 1), 6) + ". " + text,
  paragraph: (text) => text,
  bullet: (depth, text) => "*".repeat(depth + 1) + " " + text,
  ordered: (depth, _n, text) => "#".repeat(depth + 1) + " " + text,
  code: (text, lang) => "{code" + (lang ? ":" + lang : "") + "}\n" + text + "\n{code}",
  quote: (inner) => "{quote}\n" + inner + "\n{quote}",
  hr: () => "----",
  table: (header, rows) => {
    const lines = [];
    if (header && header.length) lines.push("||" + header.join("||") + "||");
    for (const r of rows) lines.push("|" + (r.length ? r.join("|") : " ") + "|");
    return lines.join("\n");
  },
};

// Plain text: strip all markup, keep structure (bullets, numbers, line breaks).
const PLAIN = {
  inline: {
    strong: (s) => s,
    em: (s) => s,
    del: (s) => s,
    codespan: (s) => s,
    link: (text, href) => (!text || text === href) ? href : text,
    image: (alt, href) => alt || href,
  },
  heading: (_lvl, text) => text,
  paragraph: (text) => text,
  bullet: (depth, text) => "  ".repeat(depth) + "- " + text,
  ordered: (depth, n, text) => "  ".repeat(depth) + n + ". " + text,
  code: (text) => text,
  quote: (inner) => inner.split("\n").map((l) => "> " + l).join("\n"),
  hr: () => "----------",
  table: tabTable,
};

const COPY_FLAVORS = { slack: SLACK, jira: JIRA, plain: PLAIN };

// convertReply renders the raw reply markdown into the named flavor. Markdown is
// a passthrough; anything we can't tokenize falls back to the raw source.
function convertReply(src, fmtKey) {
  if (fmtKey === "markdown") return src || "";
  const f = COPY_FLAVORS[fmtKey];
  const toks = f ? lexBlocks(src) : null;
  if (!f || !toks) return src || "";
  return renderBlocks(toks, f, 0).replace(/\n{3,}/g, "\n\n").trim();
}

// copyRichText puts HTML on the clipboard as text/html (so pasting into
// Outlook/Word/Gmail/Docs yields formatted text), with a plain-text alternative.
// Falls back to a hidden contenteditable + execCommand("copy"), which preserves
// rich text on paste in most browsers and works in insecure (LAN-HTTP) contexts.
async function copyRichText(html, plain) {
  try {
    if (navigator.clipboard && window.ClipboardItem && window.isSecureContext) {
      const item = new ClipboardItem({
        "text/html": new Blob([html], { type: "text/html" }),
        "text/plain": new Blob([plain || ""], { type: "text/plain" }),
      });
      await navigator.clipboard.write([item]);
      return true;
    }
  } catch (_) { /* fall through */ }
  return fallbackCopyHtml(html);
}

function fallbackCopyHtml(html) {
  try {
    const div = document.createElement("div");
    div.contentEditable = "true";
    div.innerHTML = html;
    div.style.position = "fixed";
    div.style.left = "-9999px";
    div.style.top = "0";
    document.body.appendChild(div);
    const range = document.createRange();
    range.selectNodeContents(div);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    const ok = document.execCommand("copy");
    sel.removeAllRanges();
    document.body.removeChild(div);
    return ok;
  } catch (_) {
    return false;
  }
}

// Short, friendly labels for the confirmation toast (the menu labels are more
// verbose, e.g. "Markdown (default)").
const COPY_FORMAT_LABELS = { markdown: "Markdown", slack: "Slack", jira: "Jira", html: tr("app.copy.richText"), plain: tr("app.copy.plainText") };

// copyReplyAs copies an assistant reply in the requested flavor, flashes the
// `.copied` state on the anchor button, and pops a confirmation toast so it's
// clear which format was copied (especially for the caret-menu choices).
function copyReplyAs(bubble, fmtKey, anchorBtn) {
  const src = (bubble && (bubble._rawText || bubble.textContent)) || "";
  const done = (ok) => {
    if (ok && anchorBtn) {
      anchorBtn.classList.add("copied");
      setTimeout(() => anchorBtn.classList.remove("copied"), 1500);
    }
    showToast(ok ? ("Copied as " + (COPY_FORMAT_LABELS[fmtKey] || "text")) : "Copy failed", ok ? "ok" : "err");
  };
  if (fmtKey === "html") {
    const html = (typeof marked !== "undefined") ? marked.parse(src) : escHtml(src);
    copyRichText(html, convertReply(src, "plain")).then(done);
    return;
  }
  copyTextToClipboard(convertReply(src, fmtKey)).then(done);
}

// ─── DOM builders ───────────────────────────────────────────────────────────

// formatDuration renders a millisecond duration as a short human label:
// "740ms", "1.2s", "9.8s", "42s", "1m 5s".
function formatDuration(ms) {
  ms = Math.max(0, Math.round(ms || 0));
  if (ms < 1000) return ms + "ms";
  const s = ms / 1000;
  if (s < 10) return s.toFixed(1) + "s";
  if (s < 60) return Math.round(s) + "s";
  const m = Math.floor(s / 60);
  return m + "m " + Math.round(s % 60) + "s";
}

// setReplyTime fills the per-reply timing chip next to an assistant bubble's
// copy button. No-op when the bubble has no chip or no duration was reported.
function setReplyTime(bubble, ms) {
  if (!bubble || !bubble._timeEl || ms == null) return;
  const label = formatDuration(ms);
  bubble._timeEl.textContent = label;
  bubble._timeEl.setAttribute("data-tip", "Reply generated in " + label);
}

// setToolTime fills a tool block's header timing chip (its own header only, not
// a nested child's). Used for both top-level tools — including sub-agent
// invocations — and the sub-agent's own nested tool calls.
function setToolTime(block, ms) {
  if (!block || ms == null) return;
  const el = block.querySelector(":scope > .tool-header > .tool-time");
  if (el) {
    el.textContent = formatDuration(ms);
    el.setAttribute("data-tip", "Took " + formatDuration(ms));
  }
}

// lastAssistantBubbleIn returns the last rendered assistant bubble in a
// transcript container (fallback target for a turn that ended on a tool block
// with no trailing text segment).
function lastAssistantBubbleIn(container) {
  const rows = (container || document).querySelectorAll(".msg-row.assistant");
  const row = rows[rows.length - 1];
  return row ? row.querySelector(".bubble-assistant") : null;
}

function appendAssistantBubble(container) {
  const row = document.createElement("div");
  row.className = "msg-row assistant";
  const bubble = document.createElement("div");
  bubble.className = "bubble-assistant";

  // Split button: the main icon copies the reply as Markdown (one click); the
  // caret opens a menu to copy it in another flavor (Slack / Jira / HTML / …).
  const copyGroup = document.createElement("div");
  copyGroup.className = "copy-msg-group";

  const copyBtn = document.createElement("button");
  copyBtn.className = "copy-msg-btn";
  copyBtn.dataset.tip = "Copy as Markdown";
  copyBtn.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="2" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`;
  copyBtn.addEventListener("click", () => copyReplyAs(bubble, "markdown", copyBtn));

  const caretBtn = document.createElement("button");
  caretBtn.className = "copy-msg-caret";
  caretBtn.dataset.tip = "Copy as…";
  caretBtn.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="6 9 12 15 18 9"/></svg>`;
  caretBtn.addEventListener("click", (ev) => {
    const items = [];
    COPY_FORMATS.forEach(([key, label], i) => {
      if (i === 1) items.push(SEP); // separate the default (Markdown) from the rest
      items.push([label, () => copyReplyAs(bubble, key, copyBtn)]);
    });
    showFolderCtxMenu(ev, items);
  });

  copyGroup.appendChild(copyBtn);
  copyGroup.appendChild(caretBtn);

  // Bottom-right action cluster: the (always-visible) reply-time chip sits to
  // the left of the (hover-revealed) copy split-button.
  const actions = document.createElement("div");
  actions.className = "msg-actions";
  const timeEl = document.createElement("span");
  timeEl.className = "reply-time";
  actions.appendChild(timeEl);
  actions.appendChild(copyGroup);
  bubble._timeEl = timeEl;

  row.appendChild(bubble);
  row.appendChild(actions);
  (container || fpTranscript()).appendChild(row);
  scrollBottom(paneOfNode(row));
  return bubble;
}

function appendErrorBubble(text, container) {
  const row = document.createElement("div");
  row.className = "msg-row error";
  const bubble = document.createElement("div");
  bubble.className = "bubble-error";
  bubble.textContent = text;
  row.appendChild(bubble);
  (container || fpTranscript()).appendChild(row);
  scrollBottom(paneOfNode(row));
}

// appendRoutingChip drops a centred "→ routed to <squad>" note into the
// transcript when the Omnis router hands control to another squad mid-turn.
function appendRoutingChip(container, to, reason) {
  const row = document.createElement("div");
  row.className = "msg-row routing";
  const chip = document.createElement("div");
  chip.className = "routing-chip";
  chip.textContent = tr("app.routing.routedTo", { squad: (to || "?") });
  if (reason) chip.setAttribute("data-tip", reason);
  row.appendChild(chip);
  (container || fpTranscript()).appendChild(row);
  scrollBottom(paneOfNode(row));
}

// buildToolBlock creates the shared DOM structure for both top-level and nested
// tool call blocks. Returns the block element; the caller appends it.
function buildToolBlock(name, args) {
  const { label, color } = toolMeta(name);
  const desc = toolDesc(name, args);
  const block = document.createElement("div");
  block.className = `tool-block border-${color}`;
  block.dataset.toolName = (name || "").toLowerCase();

  const header = document.createElement("div");
  header.className = "tool-header";
  header.innerHTML = `
    <span class="tool-dot pending"></span>
    <span class="tool-badge badge-${color}">${escHtml(label)}</span>
    <span class="tool-desc">${escHtml(desc)}</span>
    <span class="tool-time"></span>
    <span class="tool-chevron">▶</span>
  `;
  header.addEventListener("click", () => block.classList.toggle("expanded"));

  const body = document.createElement("div");
  body.className = "tool-body";
  body.innerHTML = `<div class="tool-out-slot"></div>`;

  block.appendChild(header);
  block.appendChild(body);
  return block;
}

// appendToolCall adds a top-level tool block to the transcript.
function appendToolCall(name, args, container) {
  const block = buildToolBlock(name, args);
  const row = document.createElement("div");
  row.className = "tool-row";
  row.appendChild(block);
  (container || fpTranscript()).appendChild(row);
  scrollBottom(paneOfNode(row));
  return block;
}

// appendFileDownloadCard renders a compact "file ready" card in the transcript
// with a Download button, shown when the agent's Write tool generates a file so
// the user can download the artifact straight from chat. Live-only, like tool
// blocks (not replayed on reload — the file stays available in the Folders
// panel). A fresh card per Write is intentional: a regenerated file gets a new
// card, and same-turn double-writes are rare.
function appendFileDownloadCard(sessionId, abs, container) {
  if (!abs) return;
  const root = container || getContainer(sessionId);
  if (!root) return;
  const name = abs.split("/").filter(Boolean).pop() || abs;
  const row = document.createElement("div");
  row.className = "tool-row";
  const card = document.createElement("div");
  card.className = "file-dl-card";
  card.innerHTML =
    `<span class="file-dl-icon">${fileIconSvg(name)}</span>` +
    `<span class="file-dl-meta">` +
      `<span class="file-dl-label">${escHtml(tr("chat.fileReady"))}</span>` +
      `<span class="file-dl-name" data-tip="${escHtml(abs)}">${escHtml(name)}</span>` +
    `</span>` +
    `<button type="button" class="file-dl-btn">${escHtml(tr("menu.download"))}</button>`;
  card.querySelector(".file-dl-btn").addEventListener("click", () => downloadHostFile(abs, name, sessionId));
  row.appendChild(card);
  root.appendChild(row);
  scrollBottom(paneOfNode(row));
}

// downloadHostFile downloads a host file via the folder download route and saves
// it via an object URL. A `sessionId` (when given) targets that session's route
// so a relative path resolves against the session's working dir (matching the
// fileref resolve); otherwise the global route is used (absolute paths resolve
// as-is on either route).
async function downloadHostFile(abs, name, sessionId) {
  try {
    const base = sessionId
      ? `/api/sessions/${encodeURIComponent(sessionId)}/folder/download`
      : folderOpBase("download");
    const res = await apiFetch(`${base}?path=${encodeURIComponent(abs)}`);
    if (!res.ok) { console.warn("download failed", abs); return; }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = name || (abs.split("/").filter(Boolean).pop() || "download");
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch {
    console.warn("download failed", abs);
  }
}

// appendNestedToolCall adds an inner tool block inside a parent sub-agent block.
// The parent is auto-expanded so the user can see activity in real time.
function appendNestedToolCall(parentBlock, name, args) {
  let nested = parentBlock.querySelector(".tool-nested");
  if (!nested) {
    const slot = parentBlock.querySelector(".tool-out-slot");
    nested = document.createElement("div");
    nested.className = "tool-nested";
    slot.parentNode.insertBefore(nested, slot);
  }
  const block = buildToolBlock(name, args);
  block.classList.add("tool-nested-item");
  nested.appendChild(block);
  scrollBottom(paneOfNode(block));
  return block;
}

// ─── Todo plan widget ────────────────────────────────────────────────────────
// The todo_* tools drive a live plan. Rather than render each call as an
// opaque, collapsed tool block, we keep a per-session view of the plan and
// render an always-expanded checklist on every change so users can follow
// execution at a glance (done = struck through, in_progress = spinner).

const TODO_STATUS_ICON = {
  done:        "✓",
  failed:      "✕",
  in_progress: "✳",
  pending:     "",
};

function isTodoTool(name) {
  return /^todo_/.test((name || "").toLowerCase());
}

// isRoutingTool reports whether a tool is part of the Omnis router's internal
// control flow (squad routing, hand-back, and the hidden capability probe).
// These never render as tool blocks in the transcript — the routing transition
// shows as a `routing` chip instead, and the probe negotiation stays hidden.
function isRoutingTool(name) {
  switch ((name || "").toLowerCase()) {
    case "route_to_squad":
    case "handoff_to_router":
    case "ask_squad":
      return true;
    default:
      return false;
  }
}

// applyTodoEvent folds a todo tool call into the session's plan state and
// returns the resulting list, or null when the call carries nothing renderable
// (e.g. an update before any write was observed this page-session).
function applyTodoEvent(sessionId, name, args) {
  const n = (name || "").toLowerCase();
  let list = sessionTodos.get(sessionId);
  if (n === "todo_write") {
    const tasks = Array.isArray(args && args.tasks) ? args.tasks : [];
    list = tasks.map(t => ({ task: String(t), status: "pending" }));
    sessionTodos.set(sessionId, list);
    return list;
  }
  if (!list) return null;
  if (n === "todo_update") {
    const idx = Number(args && args.index);
    const status = String((args && args.status) || "").trim();
    if (Number.isInteger(idx) && idx >= 0 && idx < list.length && status) {
      list[idx] = { ...list[idx], status };
    }
  }
  // todo_read (and any other todo_*) just re-renders the known state.
  return list;
}

// appendTodoBlock renders the checklist for the current plan. Only the latest
// snapshot per session stays expanded; appending a new one collapses the
// previous block down to its header (still click-to-toggle).
function appendTodoBlock(sessionId, list, container) {
  // Collapse the previous snapshot for this session, if any.
  const prev = sessionTodoBlock.get(sessionId);
  if (prev && prev.isConnected) prev.classList.add("collapsed");

  const row = document.createElement("div");
  row.className = "tool-row";

  const block = document.createElement("div");
  block.className = "todo-block";

  const done = list.filter(t => t.status === "done" || t.status === "failed").length;

  const header = document.createElement("div");
  header.className = "todo-header";
  header.innerHTML =
    `<span class="todo-bullet"></span>` +
    `<span class="todo-title">Update Todos</span>` +
    `<span class="todo-progress">${done}/${list.length}</span>` +
    `<span class="todo-chevron">▶</span>`;
  header.addEventListener("click", () => block.classList.toggle("collapsed"));
  block.appendChild(header);

  const items = document.createElement("div");
  items.className = "todo-items";
  for (const item of list) {
    const st = item.status || "pending";
    const li = document.createElement("div");
    li.className = `todo-item status-${st}`;
    const box = document.createElement("span");
    box.className = "todo-check";
    box.textContent = TODO_STATUS_ICON[st] || "";
    const txt = document.createElement("span");
    txt.className = "todo-text";
    txt.textContent = item.task;
    li.appendChild(box);
    li.appendChild(txt);
    items.appendChild(li);
  }
  block.appendChild(items);

  row.appendChild(block);
  (container || fpTranscript()).appendChild(row);
  sessionTodoBlock.set(sessionId, block);
  scrollBottom(paneOfNode(row));
  return block;
}

// ─── Curator block ───────────────────────────────────────────────────────────

function appendCuratorBlock(container) {
  const block = document.createElement("div");
  block.className = "tool-block border-orange";
  block.innerHTML =
    `<div class="tool-header">` +
      `<span class="tool-dot pending"></span>` +
      `<span class="tool-badge badge-orange">Curator</span>` +
      `<span class="tool-desc">analyzing session…</span>` +
      `<span class="tool-chevron">▶</span>` +
    `</div>` +
    `<div class="tool-body"><div class="tool-out-slot"></div></div>`;
  block.querySelector(".tool-header").addEventListener("click", () => block.classList.toggle("expanded"));
  const row = document.createElement("div");
  row.className = "tool-row";
  row.appendChild(block);
  (container || fpTranscript()).appendChild(row);
  scrollBottom(paneOfNode(row));
  return block;
}

function resolveCuratorBlock(block, data, errorMsg) {
  const dot  = block.querySelector(".tool-dot");
  const desc = block.querySelector(".tool-desc");
  const slot = block.querySelector(".tool-out-slot");

  if (errorMsg) {
    if (dot)  { dot.classList.remove("pending"); dot.classList.add("error"); }
    if (desc) desc.textContent = tr("app.curation.failed");
    if (slot) {
      const div = document.createElement("div");
      div.className = "tool-section";
      div.innerHTML =
        `<div class="tool-section-label label-error">ERROR</div>` +
        `<pre class="tool-pre tool-error">${escHtml(errorMsg)}</pre>`;
      slot.replaceWith(div);
    }
    return;
  }

  const text = data.skipped
    ? (data.reason || "Session too shallow for soft-skill curation.")
    : (data.summary || "Curation complete.");

  if (dot)  { dot.classList.remove("pending"); dot.classList.add("done"); }
  if (desc) desc.textContent = data.skipped ? "nothing to learn" : "curation complete";
  if (slot) {
    const div = document.createElement("div");
    div.className = "tool-section";
    div.innerHTML =
      `<div class="tool-section-label label-out">OUT</div>` +
      `<pre class="tool-pre output">${escHtml(text)}</pre>`;
    slot.replaceWith(div);
  }
  if (!data.skipped) block.classList.add("expanded");
}

// ─── Ask-user wizard ─────────────────────────────────────────────────────────
// Multiple pending ask_user questions for a session are presented as a single
// multi-step "wizard" card in the pane's #ask-user-slot: a clickable step rail
// at the top, one question body shown at a time, Back/Next navigation, and
// auto-advance to the next unanswered step after each answer. A burst of
// install-permission prompts (questions sharing a `group` tag) folds in as a
// single step that applies one shared Allow/Deny scope to every member question.
//
// Each step is resolved server-side as soon as it is answered (so a long wizard
// never lets early questions hit the 5-minute timeout); revisiting a resolved
// step via the rail shows a read-only summary.

// questionId → { sessionId } so a server-side ask_user_cancel can locate the
// owning wizard + step.
const pendingAskWidgets = new Map();
// sessionId → wizard state { sessionId, row, card, steps, current, busy }.
// Each step is either { type:"single", q, resolved, answer } or
// { type:"group", group, questions:[], scopeIdx, resolved, cancelled }.
const askWizards = new Map();

// renderAskUserWidget routes a freshly-arrived question into its session's
// wizard, creating the wizard card on first arrival.
function renderAskUserWidget(sessionId, q) {
  const panel = panelsForSession(sessionId)[0];
  if (!panel) {
    // No pane currently shows this session — queue it; bindSessionToPanel /
    // activateTab flush the queue when the session is opened in a pane.
    const list = queuedAskWidgets.get(sessionId) || [];
    if (!list.some(x => x.question_id === q.question_id)) list.push(q);
    queuedAskWidgets.set(sessionId, list);
    return;
  }
  const slot = panel.els.askSlot;
  if (!slot) return;

  const wiz = ensureWizard(sessionId, slot);
  const firstRender = wiz.steps.length === 0;
  addQuestionToWizard(wiz, q);
  pendingAskWidgets.set(q.question_id, { sessionId });
  // Park the view on the first unanswered step. Don't yank the user off a step
  // they're mid-answer on, so only re-home when the current step is unset or
  // already resolved.
  const curResolved = wiz.current == null || wiz.steps[wiz.current]?.resolved;
  if (curResolved) {
    const i = firstUnansweredStep(wiz);
    if (i >= 0) wiz.current = i;
  }
  // When the active step is an unresolved single question already on screen,
  // refresh only the rail so the user's in-progress input survives the arrival
  // of a sibling question. Otherwise (first render, re-homed, or a group step
  // whose item list changed) rebuild the card.
  const cur = wiz.steps[wiz.current];
  if (!firstRender && !curResolved && cur && !cur.resolved && cur.type === "single") {
    refreshWizardRail(wiz);
  } else {
    renderWizard(wiz);
  }
  // Size the card to the real space above the composer now, so its bottom (the
  // Submit/Skip row) is never clipped on the first paint before the observer fires.
  updateAskCardBounds(panel);
  scrollBottom(panel);
}

// ensureWizard returns the session's wizard, creating its row/card in the slot
// on first use.
function ensureWizard(sessionId, slot) {
  let wiz = askWizards.get(sessionId);
  if (wiz && wiz.row.isConnected) return wiz;
  const row = document.createElement("div");
  row.className = "ask-user-row";
  row.setAttribute("data-session-id", sessionId);
  const card = document.createElement("div");
  card.className = "ask-user-card ask-wizard-card";
  row.appendChild(card);
  slot.appendChild(row);
  wiz = { sessionId, row, card, steps: [], current: null, busy: false, _submit: null };
  row._askWizard = wiz; // so a tab switch can requeue unanswered questions
  // Enter activates the current step's primary action (submit, or advance on a
  // resolved step). Wired once; the handler reads wiz._submit, which each render
  // keeps pointed at the live action. Ignored inside a textarea / when Shift.
  card.addEventListener("keydown", e => {
    if (e.key !== "Enter" || e.shiftKey) return;
    if (e.target && e.target.tagName === "TEXTAREA") return;
    e.preventDefault();
    if (wiz._submit) wiz._submit();
  });
  askWizards.set(sessionId, wiz);
  return wiz;
}

// addQuestionToWizard appends a question as a new step, or merges it into an
// existing group step when it carries a matching group tag.
function addQuestionToWizard(wiz, q) {
  if (q.group) {
    let step = wiz.steps.find(s => s.type === "group" && s.group === q.group);
    if (!step) {
      step = { type: "group", group: q.group, questions: [], scopeIdx: 1, resolved: false, cancelled: false };
      wiz.steps.push(step);
    }
    if (!step.questions.some(x => x.question_id === q.question_id)) step.questions.push(q);
    return;
  }
  if (wiz.steps.some(s => s.type === "single" && s.q.question_id === q.question_id)) return;
  wiz.steps.push({ type: "single", q, resolved: false, answer: null });
}

function firstUnansweredStep(wiz) {
  return wiz.steps.findIndex(s => !s.resolved);
}

// stepWasSkipped reports whether a resolved step was cancelled/skipped (vs
// answered), for the rail glyph and summary icon.
function stepWasSkipped(step) {
  if (step.type === "group") return !!step.cancelled;
  return !!(step.answer && step.answer.cancelled);
}

// renderWizard rebuilds the wizard card: an optional step rail plus the active
// step's body and navigation. The card element persists across renders (only
// its children are replaced), so listeners wired in ensureWizard survive.
function renderWizard(wiz) {
  const { card, steps } = wiz;
  while (card.lastChild) card.removeChild(card.lastChild);
  if (steps.length === 0) return;
  if (wiz.current == null || wiz.current < 0 || wiz.current >= steps.length) {
    const i = firstUnansweredStep(wiz);
    wiz.current = i >= 0 ? i : 0;
  }

  const rail = buildWizardRail(wiz);
  if (rail) card.appendChild(rail);

  const step = steps[wiz.current];
  if (step.type === "group") renderGroupStepBody(wiz, step);
  else renderSingleStepBody(wiz, step);
}

// buildWizardRail returns the clickable step rail element, or null when there
// is only one step (a lone question reads like the old single card).
function buildWizardRail(wiz) {
  const { steps } = wiz;
  if (steps.length <= 1) return null;
  const rail = document.createElement("div");
  rail.className = "ask-wizard-rail";
  steps.forEach((s, i) => {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = "ask-wizard-step";
    if (i === wiz.current) chip.classList.add("is-current");
    if (s.resolved) chip.classList.add("is-done");
    chip.textContent = s.resolved ? (stepWasSkipped(s) ? "✗" : "✓") : String(i + 1);
    chip.setAttribute("data-tip", stepTitle(s));
    chip.addEventListener("click", () => { if (!wiz.busy) { wiz.current = i; renderWizard(wiz); } });
    rail.appendChild(chip);
  });
  const count = document.createElement("span");
  count.className = "ask-wizard-count";
  const done = steps.filter(s => s.resolved).length;
  count.textContent = done
    ? tr("app.askwizard.stepDone", { current: wiz.current + 1, total: steps.length, done })
    : tr("app.askwizard.step", { current: wiz.current + 1, total: steps.length });
  rail.appendChild(count);
  return rail;
}

// refreshWizardRail swaps just the rail in place, leaving the active step body
// untouched — used when a new question lands while the user may be mid-answer
// on the current step, so their typed input / selection survives.
function refreshWizardRail(wiz) {
  const existing = wiz.card.querySelector(":scope > .ask-wizard-rail");
  const rail = buildWizardRail(wiz);
  if (existing) {
    if (rail) wiz.card.replaceChild(rail, existing);
    else existing.remove();
  } else if (rail) {
    wiz.card.insertBefore(rail, wiz.card.firstChild);
  }
}

// buildAskInput builds the per-kind input controls for a single question and
// returns the fragment plus an answer getter and a preferred focus element.
function buildAskInput(q) {
  const kind = q.kind;
  const choices = Array.isArray(q.choices) ? q.choices : [];
  const el = document.createDocumentFragment();
  let getAnswer, focusEl = null;

  if (kind === "single" || kind === "confirm") {
    const choicesDiv = document.createElement("div");
    choicesDiv.className = "ask-user-choices";
    // Pre-select the suggested default (when valid) so the user can just
    // press Enter / click Submit to accept it.
    let selectedValue = (q.default && choices.includes(q.default)) ? q.default : null;
    const labels = [];
    const paint = () => labels.forEach(l =>
      l.classList.toggle("is-selected", l.dataset.choice === selectedValue));
    choices.forEach(ch => {
      const label = document.createElement("label");
      label.className = "ask-user-choice";
      label.dataset.choice = ch;
      const radio = document.createElement("input");
      radio.type = "radio";
      radio.name = "ask_" + q.question_id;
      radio.value = ch;
      if (ch === selectedValue) { radio.checked = true; focusEl = radio; }
      radio.addEventListener("change", () => { selectedValue = ch; paint(); });
      label.appendChild(radio);
      label.appendChild(document.createTextNode(ch));
      choicesDiv.appendChild(label);
      labels.push(label);
    });
    paint();
    el.appendChild(choicesDiv);
    getAnswer = () => selectedValue ? { selected: [selectedValue], text: "", cancelled: false } : null;
  } else if (kind === "multi") {
    const choicesDiv = document.createElement("div");
    choicesDiv.className = "ask-user-choices";
    const checkboxes = [];
    choices.forEach(ch => {
      const label = document.createElement("label");
      label.className = "ask-user-choice";
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.value = ch;
      checkboxes.push(cb);
      label.appendChild(cb);
      label.appendChild(document.createTextNode(ch));
      choicesDiv.appendChild(label);
    });
    el.appendChild(choicesDiv);
    let textArea = null;
    if (q.allow_text) {
      textArea = document.createElement("textarea");
      textArea.className = "ask-user-text-input";
      textArea.placeholder = tr("app.askuser.notesPlaceholder");
      el.appendChild(textArea);
    }
    getAnswer = () => {
      const sel = checkboxes.filter(c => c.checked).map(c => c.value);
      return { selected: sel, text: textArea ? textArea.value.trim() : "", cancelled: false };
    };
  } else {
    // text — a password-typed question gets a single-line masked input;
    // everything else gets the regular multi-line textarea.
    let inputEl;
    if (q.password) {
      inputEl = document.createElement("input");
      inputEl.type = "password";
      inputEl.autocomplete = "off";
      inputEl.spellcheck = false;
    } else {
      inputEl = document.createElement("textarea");
    }
    inputEl.className = "ask-user-text-input";
    inputEl.placeholder = tr("app.askuser.answerPlaceholder");
    el.appendChild(inputEl);
    focusEl = inputEl;
    getAnswer = () => ({ selected: [], text: inputEl.value.trim(), cancelled: false });
  }
  return { el, getAnswer, focusEl };
}

// renderSingleStepBody renders one ordinary question step (its prompt + inputs,
// or a read-only summary when already resolved) into the wizard card.
function renderSingleStepBody(wiz, step) {
  const { card } = wiz;
  const q = step.q;

  const promptEl = document.createElement("div");
  promptEl.className = "ask-user-prompt";
  if (typeof marked !== "undefined" && typeof marked.parse === "function") {
    promptEl.innerHTML = marked.parse(q.prompt || "");
  } else {
    promptEl.textContent = q.prompt;
  }
  card.appendChild(promptEl);

  if (step.resolved) {
    appendStepSummary(card, step);
    appendWizardNav(wiz, { canSubmit: false });
    return;
  }

  const input = buildAskInput(q);
  card.appendChild(input.el);

  const nav = appendWizardNav(wiz, {
    canSubmit: true,
    skipLabel: tr("common.skip"),
    onSkip: () => submitSingleStep(wiz, step, { selected: [], text: "", cancelled: true }),
    onSubmit: () => {
      const answer = input.getAnswer();
      if (answer) submitSingleStep(wiz, step, answer);
    },
  });

  // Focus the selected radio / text input (or the primary button) so Enter
  // works without a prior click — only when this pane is focused.
  const panel = panelsForSession(wiz.sessionId)[0];
  if (panel && focusedPanelId === panel.id) {
    (input.focusEl || nav.submitBtn || card).focus();
  }
}

// ─── Wizard: group step (install bursts) ─────────────────────────────────────
// Friendly plural labels per item kind for the grouped install step.
const ASK_GROUP_KIND_LABELS = {
  skill: tr("askkind.skill"), agent: tr("askkind.agent"), mcp: tr("askkind.mcp"), squad: tr("askkind.squad"),
  a2a: tr("askkind.a2a"), command: tr("askkind.command"), permission: tr("askkind.permission"),
  item: tr("askkind.item"),
};

// The five permission scopes, positional in every grouped question's `choices`
// array ([Deny, allow-once, allow-tool-session, allow-project, allow-always]).
// A grouped step picks one index and applies it to every member question.
const ASK_GROUP_SCOPES = [
  { idx: 0, label: tr("askscope.denyAll") },
  { idx: 1, label: tr("askscope.allowOnce") },
  { idx: 2, label: tr("askscope.allowSession") },
  { idx: 3, label: tr("askscope.allowProject") },
  { idx: 4, label: tr("askscope.allowAlways") },
];

// renderGroupStepBody renders an install-burst step: a "what will be installed"
// list grouped by kind plus one shared set of scope choices (or a read-only
// summary when already resolved).
function renderGroupStepBody(wiz, step) {
  const { card } = wiz;
  const questions = step.questions;

  const title = document.createElement("div");
  title.className = "ask-user-prompt";
  const strong = document.createElement("strong");
  const n = questions.length;
  strong.textContent = trN("app.askuser.installItems", n);
  title.appendChild(strong);
  card.appendChild(title);

  if (step.resolved) {
    appendStepSummary(card, step);
    appendWizardNav(wiz, { canSubmit: false });
    return;
  }

  // Group by kind, preserving first-seen order.
  const byKind = new Map();
  for (const q of questions) {
    const kind = (q.item && q.item.kind) || "item";
    if (!byKind.has(kind)) byKind.set(kind, []);
    byKind.get(kind).push(q);
  }
  const itemsWrap = document.createElement("div");
  itemsWrap.className = "ask-group-items";
  for (const [kind, qs] of byKind) {
    const section = document.createElement("div");
    section.className = "ask-group-section";
    const h = document.createElement("div");
    h.className = "ask-group-kind";
    h.textContent = (ASK_GROUP_KIND_LABELS[kind] || kind) + " (" + qs.length + ")";
    section.appendChild(h);
    for (const q of qs) {
      const it = document.createElement("div");
      it.className = "ask-group-item";
      const nameEl = document.createElement("span");
      nameEl.className = "ask-group-item-name";
      nameEl.textContent = (q.item && q.item.name) || tr("app.askuser.unnamed");
      it.appendChild(nameEl);
      const src = q.item && q.item.source;
      if (src) {
        const srcEl = document.createElement("span");
        srcEl.className = "ask-group-item-src";
        srcEl.textContent = tr("app.askuser.from", { src });
        it.appendChild(srcEl);
      }
      section.appendChild(it);
    }
    itemsWrap.appendChild(section);
  }
  card.appendChild(itemsWrap);

  const choicesDiv = document.createElement("div");
  choicesDiv.className = "ask-user-choices";
  const labels = [];
  const paint = () => labels.forEach(l =>
    l.classList.toggle("is-selected", Number(l.dataset.idx) === step.scopeIdx));
  ASK_GROUP_SCOPES.forEach(s => {
    const label = document.createElement("label");
    label.className = "ask-user-choice";
    label.dataset.idx = String(s.idx);
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "askgrp_" + wiz.sessionId + "_" + step.group;
    radio.value = String(s.idx);
    if (s.idx === step.scopeIdx) radio.checked = true;
    radio.addEventListener("change", () => { step.scopeIdx = s.idx; paint(); });
    label.appendChild(radio);
    label.appendChild(document.createTextNode(s.label));
    choicesDiv.appendChild(label);
    labels.push(label);
  });
  paint();
  card.appendChild(choicesDiv);

  appendWizardNav(wiz, {
    canSubmit: true,
    skipLabel: "Skip all",
    onSkip: () => submitGroupStep(wiz, step, true),
    onSubmit: () => submitGroupStep(wiz, step, false),
  });
}

// ─── Wizard: navigation, submission, finalization ────────────────────────────

// appendWizardNav appends the Back / Skip / Next-or-Submit action row for the
// current step. For an already-resolved step (canSubmit:false) it offers only
// Back / Next navigation. Returns { submitBtn } for focus.
function appendWizardNav(wiz, opts) {
  const { steps, current: i } = wiz;
  const actions = document.createElement("div");
  actions.className = "ask-user-actions";

  if (i > 0) {
    const back = document.createElement("button");
    back.type = "button";
    back.className = "ask-user-cancel-btn ask-wizard-back";
    back.textContent = tr("common.back");
    back.addEventListener("click", () => { if (!wiz.busy) { wiz.current = i - 1; renderWizard(wiz); } });
    actions.appendChild(back);
  }

  let submitBtn = null;
  if (opts.canSubmit) {
    const skip = document.createElement("button");
    skip.type = "button";
    skip.className = "ask-user-cancel-btn";
    skip.textContent = opts.skipLabel || tr("common.skip");
    skip.addEventListener("click", () => { if (!wiz.busy) opts.onSkip(); });
    actions.appendChild(skip);

    submitBtn = document.createElement("button");
    submitBtn.type = "button";
    submitBtn.className = "ask-user-submit";
    // "Next →" while any other step is still unanswered, otherwise "Submit".
    const moreUnanswered = steps.some((s, j) => j !== i && !s.resolved);
    submitBtn.textContent = moreUnanswered ? tr("common.next") : tr("common.submit");
    submitBtn.addEventListener("click", () => { if (!wiz.busy) opts.onSubmit(); });
    actions.appendChild(submitBtn);
    wiz._submit = () => { if (!wiz.busy && !submitBtn.disabled) opts.onSubmit(); };
  } else if (i < steps.length - 1) {
    const next = document.createElement("button");
    next.type = "button";
    next.className = "ask-user-submit";
    next.textContent = tr("common.next");
    next.addEventListener("click", () => { if (!wiz.busy) { wiz.current = i + 1; renderWizard(wiz); } });
    actions.appendChild(next);
    wiz._submit = () => { if (!wiz.busy) { wiz.current = i + 1; renderWizard(wiz); } };
  } else {
    wiz._submit = null;
  }

  wiz.card.appendChild(actions);
  return { submitBtn };
}

// resolveQuestion POSTs one answer; returns true on success.
async function resolveQuestion(sessionId, questionId, answer) {
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/ask-user/${questionId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(answer),
    });
    return res.ok;
  } catch { return false; }
}

// setWizardBusy disables/enables every button in the card during an in-flight POST.
function setWizardBusy(wiz, busy) {
  wiz.busy = busy;
  wiz.card.querySelectorAll("button").forEach(b => { b.disabled = busy; });
}

// submitSingleStep resolves an ordinary step's question, then advances.
async function submitSingleStep(wiz, step, answer) {
  if (wiz.busy) return;
  setWizardBusy(wiz, true);
  const ok = await resolveQuestion(wiz.sessionId, step.q.question_id, answer);
  setWizardBusy(wiz, false);
  if (!ok) return; // leave the step editable so the user can retry
  pendingAskWidgets.delete(step.q.question_id);
  step.resolved = true;
  step.answer = answer;
  afterStepResolved(wiz);
}

// submitGroupStep resolves every member of a group step with the shared scope
// (or cancels them all), then advances. Members whose POST fails are kept.
async function submitGroupStep(wiz, step, cancelled) {
  if (wiz.busy) return;
  setWizardBusy(wiz, true);
  const qs = step.questions.slice();
  const results = await Promise.all(qs.map(q => {
    let answer;
    if (cancelled) {
      answer = { selected: [], text: "", cancelled: true };
    } else {
      const choices = Array.isArray(q.choices) ? q.choices : [];
      const choice = choices[step.scopeIdx];
      if (!choice) return Promise.resolve({ q, ok: false });
      answer = { selected: [choice], text: "", cancelled: false };
    }
    return resolveQuestion(wiz.sessionId, q.question_id, answer).then(ok => ({ q, ok }));
  }));
  setWizardBusy(wiz, false);
  for (const r of results) if (r.ok) pendingAskWidgets.delete(r.q.question_id);
  const failed = results.filter(r => !r.ok).map(r => r.q);
  if (failed.length === 0) {
    step.resolved = true;
    step.cancelled = cancelled;
    step.resolvedCount = qs.length;
    afterStepResolved(wiz);
  } else {
    step.questions = failed;
    renderWizard(wiz);
  }
}

// afterStepResolved moves to the next unanswered step, or finalizes the wizard
// when every step is resolved.
function afterStepResolved(wiz) {
  const next = firstUnansweredStep(wiz);
  if (next < 0) { finalizeWizard(wiz); return; }
  wiz.current = next;
  renderWizard(wiz);
  const panel = panelsForSession(wiz.sessionId)[0];
  if (panel) scrollBottom(panel);
}

// appendStepSummary appends a resolved step's one-line summary.
function appendStepSummary(card, step) {
  const resolved = document.createElement("div");
  resolved.className = "ask-user-resolved-text";
  resolved.textContent = stepSummaryText(step);
  card.appendChild(resolved);
}

// stepSummaryText renders the read-only summary line for a resolved step.
function stepSummaryText(step) {
  if (step.type === "group") {
    const count = step.resolvedCount != null ? step.resolvedCount : step.questions.length;
    const noun = "install" + (count === 1 ? "" : "s");
    if (step.cancelled) return "✗ skipped " + count + " " + noun;
    const scope = ASK_GROUP_SCOPES.find(s => s.idx === step.scopeIdx);
    return "✓ " + (scope ? scope.label : "submitted") + " — " + count + " " + noun;
  }
  const q = step.q, a = step.answer || {};
  // Never echo a password answer back to the transcript.
  const maskText = t => q.password && t ? "••••••••" : t;
  if (a.cancelled) return "✗ skipped";
  let summary;
  if (a.selected && a.selected.length) {
    summary = a.selected.join(", ");
    if (a.text) summary += " — " + maskText(a.text);
  } else {
    summary = maskText(a.text) || "(empty)";
  }
  return "✓ " + summary;
}

// stepTitle is the rail-chip tooltip: the question prompt (plain-ish, truncated)
// or a label for a group step.
function stepTitle(step) {
  if (step.type === "group") {
    const n = step.questions.length;
    return "Install " + n + " item" + (n === 1 ? "" : "s");
  }
  const t = (step.q.prompt || "").replace(/[#*_`>\n]+/g, " ").replace(/\s+/g, " ").trim();
  if (!t) return "Question";
  return t.length > 80 ? t.slice(0, 80) + "…" : t;
}

// finalizeWizard collapses a fully-resolved wizard to a stacked per-step summary
// and moves it into the transcript so it scrolls as history.
function finalizeWizard(wiz) {
  askWizards.delete(wiz.sessionId);
  for (const step of wiz.steps) {
    if (step.type === "group") { for (const q of step.questions) pendingAskWidgets.delete(q.question_id); }
    else pendingAskWidgets.delete(step.q.question_id);
  }
  const { row, card, sessionId } = wiz;
  card.classList.add("resolved");
  row.classList.add("resolved");
  while (card.lastChild) card.removeChild(card.lastChild);
  for (const step of wiz.steps) {
    const line = document.createElement("div");
    line.className = "ask-user-resolved-text";
    line.textContent = stepSummaryText(step);
    card.appendChild(line);
  }
  const container = getContainer(sessionId);
  if (container) container.appendChild(row);
  const panel = panelsForSession(sessionId)[0];
  if (panel) scrollBottom(panel);
}

// cancelAskUserWidget handles a server-side ask_user_cancel: it marks the owning
// step resolved/skipped and either re-renders or finalizes the wizard.
function cancelAskUserWidget(questionId) {
  const entry = pendingAskWidgets.get(questionId);
  if (!entry) return;
  pendingAskWidgets.delete(questionId);
  const wiz = askWizards.get(entry.sessionId);
  if (!wiz) return;
  for (const step of wiz.steps) {
    if (step.resolved) continue;
    if (step.type === "single" && step.q.question_id === questionId) {
      step.resolved = true;
      step.answer = { selected: [], text: "", cancelled: true };
      afterServerCancel(wiz);
      return;
    }
    if (step.type === "group") {
      const idx = step.questions.findIndex(q => q.question_id === questionId);
      if (idx >= 0) {
        step.questions.splice(idx, 1);
        if (step.questions.length === 0) {
          step.resolved = true;
          step.cancelled = true;
          step.resolvedCount = 0;
        }
        afterServerCancel(wiz);
        return;
      }
    }
  }
}

// afterServerCancel re-homes the view onto the first unanswered step (or
// finalizes when none remain) without clobbering an in-flight submit.
function afterServerCancel(wiz) {
  if (firstUnansweredStep(wiz) < 0) { finalizeWizard(wiz); return; }
  if (wiz.steps[wiz.current]?.resolved) {
    const i = firstUnansweredStep(wiz);
    if (i >= 0) wiz.current = i;
  }
  if (!wiz.busy) renderWizard(wiz);
}

// formatTeammateResponse returns a human-readable string for a teammate tool
// response, or null to signal "suppress this output entirely".
function formatTeammateResponse(response) {
  if (!response || typeof response !== "object") return String(response || "");

  // teammate_check: empty mailbox → suppress
  if (response.message === "(none)") return null;

  // teammate_check: message with [Sender] prefix → show structured
  if (typeof response.message === "string") {
    const m = response.message.match(/^\[(.+?)\]\s*([\s\S]*)$/);
    if (m) return `From: ${m[1]}\n\n${m[2].trim()}`;
    return response.message;
  }

  // teammate_ask: {"reply": "..."}
  if (typeof response.reply === "string") {
    const m = response.reply.match(/^\[(.+?)\]\s*([\s\S]*)$/);
    if (m) return `From: ${m[1]}\n\n${m[2].trim()}`;
    return response.reply;
  }

  // teammate_tell: {"result": "delivered"}
  if (typeof response.result === "string") return response.result;

  // teammate_list: {"sessions": {"name": "addr", ...}}
  if (response.sessions && typeof response.sessions === "object") {
    const names = Object.keys(response.sessions);
    return names.length === 0 ? "(no sessions available)" : names.join("\n");
  }

  return JSON.stringify(response, null, 2);
}

function resolveToolCall(block, response, durationMs) {
  const isError = response && typeof response === "object" && typeof response.error === "string";
  const toolName = block.dataset.toolName || "";
  const isTeammate = /^teammate/.test(toolName);
  const isSoftskillList = /^list_softskill/.test(toolName);

  const dot = block.querySelector(".tool-dot");
  if (dot) { dot.classList.remove("pending"); dot.classList.add(isError ? "error" : "done"); }
  setToolTime(block, durationMs);

  const slot = block.querySelector(".tool-out-slot");
  if (!slot) return;

  if (isSoftskillList && !isError && response && typeof response === "object") {
    // The wrapped list_softskills tool inherits upstream skilltoolset's
    // response schema, which uses the key `skills` — not `softskills` — so
    // we have to filter whichever string field carries the XML payload.
    const xmlKey = typeof response.softskills === "string" ? "softskills"
                 : typeof response.skills === "string" ? "skills"
                 : null;
    if (xmlKey) {
      const { kept } = countSoftskillsExcludingCurator(response[xmlKey]);
      if (kept === 0) {
        // Only the curator's own meta-procedure was listed — suppress the chip.
        const row = block.closest(".tool-row");
        const parentNested = block.parentElement && block.parentElement.classList.contains("tool-nested")
          ? block.parentElement
          : null;
        block.remove();
        if (parentNested && parentNested.children.length === 0) parentNested.remove();
        if (row && row.children.length === 0) row.remove();
        return;
      }
      // Hide the curator entry from the rendered list but keep the chip.
      response = { ...response, [xmlKey]: stripCuratorSoftskill(response[xmlKey]) };
    }
  }

  if (isTeammate && !isError) {
    const formatted = formatTeammateResponse(response);
    if (formatted === null) {
      // Empty teammate output (e.g. teammate_check on empty mailbox):
      // remove the whole chip so it doesn't clutter the transcript.
      const row = block.closest(".tool-row");
      const parentNested = block.parentElement && block.parentElement.classList.contains("tool-nested")
        ? block.parentElement
        : null;
      block.remove();
      if (parentNested && parentNested.children.length === 0) parentNested.remove();
      if (row && row.children.length === 0) row.remove();
      return;
    }
    const outDiv = document.createElement("div");
    outDiv.className = "tool-section";
    outDiv.innerHTML = `<div class="tool-section-label label-out">OUT</div>
       <pre class="tool-pre output">${escHtml(formatted)}</pre>`;
    slot.replaceWith(outDiv);
    return;
  }

  // load_skill returns the skill's SKILL.md body in `instructions` (markdown)
  // alongside its `frontmatter`/`skill_name`. Render that body as markdown
  // rather than dumping the whole JSON object as a <pre>.
  const isSkillLoad = /^load_skill/.test(toolName);
  if (isSkillLoad && !isError && response && typeof response === "object"
      && typeof response.instructions === "string") {
    const outDiv = document.createElement("div");
    outDiv.className = "tool-section";
    const label = document.createElement("div");
    label.className = "tool-section-label label-out";
    label.textContent = "SKILL";
    outDiv.appendChild(label);

    // Surface the dependency gate's notice (e.g. a missing/declined install)
    // so the user sees why a skill may be running in its fallback mode.
    if (typeof response.dependency_status === "string" && response.dependency_status.trim()) {
      const note = document.createElement("div");
      note.className = "tool-skill-note";
      note.textContent = response.dependency_status.trim();
      outDiv.appendChild(note);
    }

    const md = document.createElement("div");
    md.className = "tool-md";
    if (typeof marked !== "undefined" && typeof marked.parse === "function") {
      md.innerHTML = marked.parse(response.instructions);
    } else {
      md.textContent = response.instructions;
    }
    outDiv.appendChild(md);
    slot.replaceWith(outDiv);
    return;
  }

  const text = isError ? response.error : extractResponse(response);
  const outDiv = document.createElement("div");
  outDiv.className = "tool-section";
  outDiv.innerHTML = isError
    ? `<div class="tool-section-label label-error">ERROR</div>
       <pre class="tool-pre tool-error">${escHtml(text)}</pre>`
    : `<div class="tool-section-label label-out">OUT</div>
       <pre class="tool-pre output">${escHtml(text)}</pre>`;

  // If the tool returned references to image files (image_generator,
  // MCP image tools, etc.), render thumbnails alongside the textual output.
  if (!isError) {
    const imagePaths = collectImagePathsFromResponse(response);
    if (imagePaths.length > 0) {
      const gallery = document.createElement("div");
      gallery.className = "tool-image-gallery";
      imagePaths.forEach(p => {
        const img = document.createElement("img");
        img.setAttribute("src", p);
        img.alt = p;
        img.loading = "lazy";
        gallery.appendChild(img);
      });
      outDiv.appendChild(gallery);
      rewriteLocalImages(gallery);
    }
  }
  slot.replaceWith(outDiv);
}

// ─── API helpers ─────────────────────────────────────────────────────────────

async function apiFetch(path, opts = {}) {
  const headers = authHeaders(opts.headers || {});
  if (opts.body && !(opts.body instanceof FormData) && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
  const res = await fetch((window.BASE_PATH || "") + path, { ...opts, headers });
  if (res.status === 401) { promptForToken(); throw new Error("unauthorized"); }
  return res;
}

function promptForToken() {
  const t = window.prompt("Enter API bearer token (OMNIS_SERVER_TOKEN):", token || "");
  if (t !== null) {
    token = t.trim();
    localStorage.setItem(TOKEN_KEY, token);
  }
}

// ─── Sessions ────────────────────────────────────────────────────────────────

// Monotonic sequence guard: bursts of session creation/deletion fire several
// overlapping loadSessions() calls, and their GET /api/sessions responses can
// resolve out of order. Without this, an older response (issued when fewer
// sessions existed) could resolve last and clobber a newer one — dropping the
// just-created session from the sidebar until the next refresh. We tag each
// request and only render the latest one to resolve.
let _loadSessionsSeq = 0;
async function loadSessions() {
  const seq = ++_loadSessionsSeq;
  try {
    const res = await apiFetch("/api/sessions", { cache: "no-store" });
    const data = await res.json();
    if (seq !== _loadSessionsSeq) return; // a newer load superseded this one
    renderSessions(data.sessions || []);
  } catch (e) { console.error(e); }
}

// Three-dots "kebab" trigger that opens each session row's actions menu.
const ICON_DOTS = `<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor"><circle cx="12" cy="5" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="12" cy="19" r="1.7"/></svg>`;
// Icons shown beside the session actions-menu entries.
const ICON_COPY = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"/><path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/></svg>`;
const ICON_RENAME = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>`;
const ICON_ARCHIVE = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5"/><line x1="10" y1="12" x2="14" y2="12"/></svg>`;
const ICON_UNARCHIVE = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5"/><path d="M12 16V9"/><polyline points="9 12 12 9 15 12"/></svg>`;
const ICON_DELETE = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6"/><path d="M10 11v6M14 11v6"/><path d="M9 6V4a1 1 0 011-1h4a1 1 0 011 1v2"/></svg>`;

// buildSessionRow renders one session <li>. A single ⋮ "kebab" button opens a
// menu grouping the row's actions: Copy name + Rename (active rows only), a
// thin separator, then Archive/Unarchive + Delete. Archived rows route a click
// to a read-only view.
function buildSessionRow(s, { archived }) {
  const li = document.createElement("li");
  li.dataset.id = s.id;
  if (panelsWithTab(s.id).length) li.classList.add("active");
  const ts = new Date(s.last_used_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  const displayName = s.title || s.id;
  // Two-letter glyph shown when the sidebar is collapsed to a rail
  // (e.g. "alive-sole" → "al"). First two alphanumerics, lowercased.
  const abbr = (displayName.match(/[a-zA-Z0-9]/g) || []).slice(0, 2).join("").toLowerCase()
    || displayName.trim().slice(0, 2).toLowerCase();

  if (!archived && sessionSending.has(s.id)) li.classList.add("session-busy");
  // Show a squad badge only when the session uses a non-default squad,
  // so single-squad / default setups stay visually quiet.
  const showBadge = s.squad && s.squad !== defaultSquadName;
  const badgeHtml = showBadge
    ? `<span class="session-squad-badge" data-tip="Squad: ${escHtml(s.squad)}">${escHtml(s.squad)}</span>`
    : "";
  li.innerHTML = `
    <span class="session-abbr" data-tip="${escHtml(displayName)}" aria-hidden="true">${escHtml(abbr)}</span>
    <div class="session-name-row">
      <span class="session-busy-dot"></span>
      <div class="session-name" data-tip="${escHtml(displayName)}">${escHtml(displayName)}</div>
      <div class="session-actions">
        <button class="session-action-btn session-menu-btn" data-tip="Actions" tabindex="-1" aria-label="Session actions">${ICON_DOTS}</button>
      </div>
    </div>
    <div class="session-bottom-row">
      ${badgeHtml}
      <span class="meta">${s.turns} turn${s.turns === 1 ? "" : "s"} · ${ts}</span>
    </div>
  `;

  li.addEventListener("click", (e) => {
    if (e.target.closest(".session-actions")) return;
    selectSession(s.id);
  });
  li.querySelector(".session-menu-btn").addEventListener("click", (e) => {
    e.stopPropagation();
    openSessionCtxMenu(e, s, archived, li);
  });
  return li;
}

// openSessionCtxMenu builds a session row's actions menu, reusing the themed
// context-menu renderer shared with the Folders panel. A thin separator (SEP)
// splits the benign Copy/Rename actions from the Archive/Delete ones.
function openSessionCtxMenu(ev, s, archived, li) {
  const displayName = s.title || s.id;
  const items = [[tr("menu.copyName"), () => writeClipboard(displayName), { icon: ICON_COPY }]];
  if (!archived) items.push([tr("menu.rename"), () => startRename(li, s.id, s.title || ""), { icon: ICON_RENAME }]);
  items.push(SEP);
  items.push(archived
    ? [tr("menu.unarchive"), () => unarchiveSession(s.id), { icon: ICON_UNARCHIVE }]
    : [tr("menu.archive"), () => archiveSession(s.id), { icon: ICON_ARCHIVE }]);
  items.push([tr("menu.delete"), () => deleteSession(s.id, li), { icon: ICON_DELETE }]);
  showFolderCtxMenu(ev, items);
}

// sessionTimeframe buckets a session by its last-activity date relative to
// `now`, returning a stable `key` (used to detect group changes) and a human
// `label`. Buckets, newest → oldest: Today, Yesterday, This week, Last week,
// This month, then one bucket per older calendar month ("May 2026", …). Because
// the list arrives sorted by last_used_at descending, re-touching an old session
// bumps its last_used_at to now and it re-enters the "Today" group automatically.
function sessionTimeframe(date, now) {
  const t = date.getTime();
  if (isNaN(t)) return { key: "today", label: "Today" }; // guard a bad timestamp
  const DAY = 86400000;
  const startOfDay = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const today0 = startOfDay(now);
  const dow = (now.getDay() + 6) % 7;                 // 0 = Monday … 6 = Sunday
  const weekStart0 = today0 - dow * DAY;              // Monday of the current week
  const monthStart0 = new Date(now.getFullYear(), now.getMonth(), 1).getTime();
  if (t >= today0) return { key: "today", label: "Today" };
  if (t >= today0 - DAY) return { key: "yesterday", label: "Yesterday" };
  if (t >= weekStart0) return { key: "this-week", label: "This week" };
  if (t >= weekStart0 - 7 * DAY) return { key: "last-week", label: "Last week" };
  if (t >= monthStart0) return { key: "this-month", label: "This month" };
  return {
    key: `m-${date.getFullYear()}-${date.getMonth()}`,
    label: date.toLocaleDateString(undefined, { month: "long", year: "numeric" }),
  };
}

// buildTimeframeHeader renders a non-interactive group separator <li> for the
// session list. It carries no data-id, so the list-iteration sites that key on
// dataset.id (refreshSidebarActive, pane picker, layout id collection) skip it.
function buildTimeframeHeader(label) {
  const li = document.createElement("li");
  li.className = "session-group";
  li.setAttribute("aria-hidden", "true");
  li.textContent = label;
  return li;
}

function renderSessions(sessions) {
  // Keep the archived-state index in sync so the composer read-only guard and
  // applySessionUI can consult it without re-fetching.
  archivedSessions.clear();
  const active = [];
  const archived = [];
  for (const s of sessions) {
    if (s.archived) { archived.push(s); archivedSessions.add(s.id); }
    else active.push(s);
    sessionTitles.set(s.id, s.title || s.id);
  }

  // Active sessions arrive newest-first; emit a timeframe header whenever the
  // bucket changes so they read as Today / Yesterday / This week / … sections.
  els.list.innerHTML = "";
  const now = new Date();
  let curGroup = null;
  for (const s of active) {
    const tf = sessionTimeframe(new Date(s.last_used_at), now);
    if (tf.key !== curGroup) {
      curGroup = tf.key;
      els.list.appendChild(buildTimeframeHeader(tf.label));
    }
    els.list.appendChild(buildSessionRow(s, { archived: false }));
  }

  els.archivedList.innerHTML = "";
  for (const s of archived) els.archivedList.appendChild(buildSessionRow(s, { archived: true }));
  els.archivedPanel.hidden = archived.length === 0;
  els.archivedCount.textContent = archived.length ? `(${archived.length})` : "";

  // Reflect each shown session's (possibly changed) archived state, refresh the
  // sidebar highlight, and re-render any open empty-pane pickers.
  refreshSidebarActive();
  for (const p of panels) {
    renderPaneTabs(p);
    if (p.sessionId) applySessionUI(p.sessionId);
    else if (p.els.picker && !p.els.picker.hidden) renderPanePicker(p);
  }
}

function setSessionBusy(sessionId, busy) {
  const li = els.list.querySelector(`li[data-id="${CSS.escape(sessionId)}"]`);
  if (li) li.classList.toggle("session-busy", busy);
}

// closeTabEverywhere removes `id` as a tab from every pane that holds it.
// Used when a session is deleted or archived. Snapshots the pane list first
// since closeTab may close a pane (mutating `panels`).
function closeTabEverywhere(id) {
  for (const p of panelsWithTab(id)) closeTab(p, id);
}

// forgetSession drops all client-side state for a session and closes any tab
// holding it. Shared by the local delete flow and the remote "session_deleted"
// push (another browser deleted the session).
function forgetSession(id) {
  unsubscribeSessionEvents(id);
  sessionTurnCounts.delete(id);
  sessionContainers.delete(id);
  sessionCtxUsage.delete(id);
  sessionTokenAccum.delete(id);
  sessionAgentTokens.delete(id);
  sessionTodos.delete(id);
  sessionTodoBlock.delete(id);
  sessionNotifiedAt.delete(id);
  composerDrafts.delete(id);
  // Remove any pending ask_user widgets belonging to this session from every
  // pane's slot, plus the queued/ pending maps.
  for (const p of panels) {
    const slot = p.els.askSlot;
    if (!slot) continue;
    for (const row of [...slot.children]) {
      if (row.getAttribute("data-session-id") === id) row.remove();
    }
  }
  for (const [qid, entry] of pendingAskWidgets) {
    if (entry.sessionId === id) pendingAskWidgets.delete(qid);
  }
  askWizards.delete(id);
  queuedAskWidgets.delete(id);
  // Close the session's tab in every pane that held it.
  closeTabEverywhere(id);
}

async function deleteSession(id, li) {
  try {
    await apiFetch(`/api/sessions/${id}`, { method: "DELETE" });
    forgetSession(id);
    li.remove();
    refreshSidebarActive();
    saveLayout();
    // Reconcile against the server: removing the <li> directly gives instant
    // feedback, but a loadSessions() still in flight from an earlier create/
    // delete burst could re-render and resurrect the deleted row. Issuing the
    // latest load (seq-guarded) makes the sidebar authoritative.
    loadSessions();
  } catch (e) {
    console.error("failed to delete session:", e);
  }
}

// archiveSession sets a session aside (read-only). Its history stays viewable
// in the archived panel; the server detaches it from its agent generation.
async function archiveSession(id) {
  try {
    await apiFetch(`/api/sessions/${id}/archive`, { method: "POST" });
    unsubscribeSessionEvents(id);
    // Close its tab in every pane — an archived session is set aside, so it
    // shouldn't stay open. Its DOM stays cached in sessionContainers, so
    // clicking it in the archived panel re-mounts the read-only history.
    closeTabEverywhere(id);
    await loadSessions();
    saveLayout();
  } catch (e) {
    console.error("failed to archive session:", e);
  }
}

// unarchiveSession restores an archived session to active and re-enables chat.
async function unarchiveSession(id) {
  try {
    await apiFetch(`/api/sessions/${id}/unarchive`, { method: "POST" });
    await loadSessions();
    if (panelsWithTab(id).length) {
      subscribeSessionEvents(id);
      applySessionUI(id);
    }
  } catch (e) {
    console.error("failed to unarchive session:", e);
  }
}

function startRename(li, id, currentTitle) {
  const nameEl = li.querySelector(".session-name");
  const input = document.createElement("input");
  input.type = "text";
  input.className = "session-rename-input";
  input.value = currentTitle;
  input.placeholder = tr("app.session.namePlaceholder");

  nameEl.replaceWith(input);
  input.focus();
  input.select();

  async function commit() {
    const title = input.value.trim();
    try {
      await apiFetch(`/api/sessions/${id}`, {
        method: "PATCH",
        body: JSON.stringify({ title }),
      });
    } catch (e) {
      console.error("failed to rename session:", e);
    }
    await loadSessions();
  }

  input.addEventListener("keydown", (e) => {
    if (e.key === "Enter") { e.preventDefault(); commit(); }
    if (e.key === "Escape") { loadSessions(); }
  });
  input.addEventListener("blur", commit);
}

// selectSession is the sidebar-click entry point. If a pane already shows the
// session, just focus that pane; otherwise bind it into the focused pane.
async function selectSession(id) {
  if (window.Settings && window.Settings.isOpen()) window.Settings.close();
  // Already open as a tab somewhere → focus that pane and surface the tab.
  const existing = panelsWithTab(id)[0];
  if (existing) { setFocusedPanel(existing.id); await activateTab(existing, id); return; }
  // Otherwise open it as a new tab in the focused pane.
  let panel = fp();
  if (!panel) panel = createPanel(null), rebuildChatDOM(), setFocusedPanel(panel.id);
  await bindSessionToPanel(panel, id);
}

// bindSessionToPanel opens `id` as a tab in `panel` and makes it active. If the
// pane's active tab is a pending draft, the session replaces that draft slot
// in place (rather than appending a third tab); otherwise it's appended.
async function bindSessionToPanel(panel, id) {
  if (!panel.tabs.includes(id)) {
    const ai = panel.tabs.indexOf(panel.activeTab);
    if (isDraft(panel.activeTab) && ai !== -1) panel.tabs[ai] = id;
    else panel.tabs.push(id);
  }
  await activateTab(panel, id);
}

// closeTab removes `key` (a session or draft tab) from a pane's tab strip. When
// the active tab closes the neighbour is activated; closing the last tab closes
// the pane (or, for the sole pane, leaves a fresh empty "New Chat" draft).
function closeTab(panel, key) {
  const idx = panel.tabs.indexOf(key);
  if (idx === -1) return;
  const editor = isEditorTab(key);
  const term = isTermTab(key);
  if (editor) {
    const abs = editorPathOf(key);
    if (editorDirty.get(abs) && !confirm(`Discard unsaved changes to ${baseName(abs)}?`)) return;
  }
  const wasActive = panel.activeTab === key;
  panel.tabs.splice(idx, 1);
  // Editor tabs live in at most one pane; free the model once it's gone.
  if (editor && panelsWithTab(key).length === 0) disposeEditor(editorPathOf(key));
  // Terminal tabs are pane-local and ephemeral; tear down the shell + WebSocket.
  if (term) disposeTerminal(key);
  // Session-only cleanup (push subscription); editor/terminal keys have none.
  const releaseIfSession = () => { if (!editor && !term) releaseSessionIfUnviewed(key); };

  if (!panel.tabs.length) {
    if (panels.length > 1) {
      closePanel(panel);            // drops the pane; releases its (now empty) tabs
      releaseIfSession();
      refreshSidebarActive();
      return;
    }
    // Sole pane: never leave it tab-less — open a fresh "New Chat" draft.
    if (focusedPanelId === panel.id) activeSessionId = null;
    newDraftTab(panel);
    releaseIfSession();
    refreshSidebarActive();
    return;
  }

  if (wasActive) {
    const nextKey = panel.tabs[Math.min(idx, panel.tabs.length - 1)];
    activateTab(panel, nextKey);    // re-renders tabs + saveLayout
  } else {
    renderPaneTabs(panel);
    saveLayout();
  }
  releaseIfSession();
  refreshSidebarActive();
}

// requeueHiddenWizards pulls any ask-user wizard belonging to a now-hidden tab
// out of the pane's shared #ask-user-slot and back into its queue (requeuing
// every still-unanswered question), then tears the wizard down so re-selecting
// the tab rebuilds a fresh one from the queue. `activeId` is the session that
// stays visible; pass null for draft/editor/terminal tabs (they own no session,
// so every wizard in the slot is hidden) — otherwise a leftover card covers the
// new tab's picker/composer.
function requeueHiddenWizards(panel, activeId) {
  const slot = panel.els.askSlot;
  if (!slot) return;
  for (const row of [...slot.children]) {
    const sid = row.getAttribute("data-session-id");
    if (!sid || sid === activeId) continue;
    if (!row._askWizard) continue;
    const wiz = row._askWizard;
    const list = queuedAskWidgets.get(sid) || [];
    for (const step of wiz.steps) {
      if (step.resolved) continue;
      const qs = step.type === "group" ? step.questions : [step.q];
      for (const q of qs) {
        if (!list.some(x => x.question_id === q.question_id)) list.push(q);
      }
    }
    queuedAskWidgets.set(sid, list);
    askWizards.delete(sid);
    row.remove();
  }
}

// activateTab makes `key` the visible tab of `panel` (key must already be in
// panel.tabs). A draft key shows the start picker with no session; a session key
// mounts its transcript, loads history if needed, subscribes to push events, and
// flushes any queued ask-user widgets.
async function activateTab(panel, key) {
  // Snapshot the outgoing tab's composer before we swap the shared textarea, so
  // a half-typed message returns when that tab is reselected.
  if (panel.activeTab !== key) saveComposerDraft(panel);

  // Editor tab — show the Monaco editor for a file, no chat session is active.
  if (isEditorTab(key)) {
    panel.activeTab = key;
    panel.sessionId = null;
    panel.root.classList.add("editing");
    panel.root.classList.remove("terminal");
    hidePanePicker(panel);
    mountInPanel(panel, null);
    clearPinnedPrompt(panel);
    setFocusedPanel(panel.id);
    renderPaneTabs(panel);
    saveLayout();
    mountEditor(panel, editorPathOf(key));
    return;
  }

  // Terminal tab — show the interactive shell, no chat session is active.
  if (isTermTab(key)) {
    panel.activeTab = key;
    panel.sessionId = null;
    panel.root.classList.add("terminal");
    panel.root.classList.remove("editing");
    hidePanePicker(panel);
    mountInPanel(panel, null);
    clearPinnedPrompt(panel);
    setFocusedPanel(panel.id);
    renderPaneTabs(panel);
    saveLayout();
    mountTerminal(panel, key);
    return;
  }
  panel.root.classList.remove("editing");
  panel.root.classList.remove("terminal");

  // Draft tab — show the picker, no session is active. The shared ask-user slot
  // is still visible here (unlike editor/terminal tabs, which CSS hides), so
  // requeue any prior session's wizard or its card would cover the picker.
  if (isDraft(key)) {
    panel.activeTab = key;
    panel.sessionId = null;
    requeueHiddenWizards(panel, null);
    mountInPanel(panel, null);
    clearPinnedPrompt(panel);
    restoreComposerDraft(panel, key);
    setFocusedPanel(panel.id);
    renderPaneTabs(panel);
    showPanePicker(panel);
    saveLayout();
    return;
  }

  const id = key;
  if (panel.sessionId === id && panel.activeTab === id) {
    setFocusedPanel(panel.id);
    renderPaneTabs(panel);
    mountInPanel(panel, id);
    return;
  }

  panel.activeTab = id;
  panel.sessionId = id;
  hidePanePicker(panel);
  setFocusedPanel(panel.id); // updates activeSessionId + sidebar highlight
  clearPinnedPrompt(panel);
  renderPaneTabs(panel);
  if (AgentDebug.enabled) { AgentDebug.activeSession = id; AgentDebug._paint(); }

  applySessionUI(id);
  renderAttachmentsUI(id);
  restoreComposerDraft(panel, id);

  // Seed ring/popup with server-side estimates for sessions that have no
  // real-time SSE data yet (cold load or page refresh).
  if (!sessionCtxUsage.has(id)) fetchUsageEstimate(id);

  subscribeSessionEvents(id);

  // The ask-user slot is shared by the pane's tabs. Pull any widget belonging
  // to a now-hidden tab out of the slot and back into its queue, so it reappears
  // when that tab is reselected — then flush the active tab's queued widgets.
  requeueHiddenWizards(panel, id);
  const queued = queuedAskWidgets.get(id);
  if (queued) { queuedAskWidgets.delete(id); for (const q of queued) renderAskUserWidget(id, q); }

  saveLayout();

  const container = getContainer(id);

  // If the container already has content it's a live stream or a previously
  // viewed session — show it and pull any background turns that arrived since.
  if (container.childNodes.length > 0) {
    mountInPanel(panel, id);
    scrollBottom(panel, true);
    await appendNewPushTurns(id);
    return;
  }

  mountInPanel(panel, id);

  try {
    const res = await apiFetch(`/api/sessions/${id}/messages`);
    if (!res.ok) return;
    const data = await res.json();
    const turns = data.turns || [];
    sessionTurnCounts.set(id, turns.length);
    if (turns.length === 0) {
      const row = document.createElement("div");
      row.className = "msg-row no-messages-placeholder";
      const b = document.createElement("div");
      b.className = "bubble-assistant";
      b.style.opacity = ".4";
      b.style.fontStyle = "italic";
      b.textContent = tr("app.session.noMessages");
      row.appendChild(b);
      container.appendChild(row);
      return;
    }
    for (let i = 0; i < turns.length; i++) {
      const turn = turns[i];
      appendUserBubble(turn.user_text, container, null, i);
      const bubble = appendAssistantBubble(container);
      renderMarkdown(bubble, turn.assistant_text);
      setReplyTime(bubble, turn.duration_ms);
    }
    scrollBottom(panel, true);
  } catch (e) {
    console.error("failed to load session history:", e);
  }
}

// ─── Squads ──────────────────────────────────────────────────────────────────
// Squads group agents into named profiles (leader + members). The picker
// next to the New Chat button selects which squad each new session uses;
// the existing session's squad is recorded on the session itself and
// surfaced as a small badge on the sidebar entry. The picker hides itself
// when only the default squad is available so the UI stays empty for
// single-squad setups.

const SQUAD_PREF_KEY = "agent_toolkit_squad";
let availableSquads = [];          // [{name, description, leader, members, ...}]
let defaultSquadName = "default";
let selectedSquadName = "";

async function loadSquads() {
  try {
    const res = await apiFetch("/api/squads");
    if (!res.ok) return;
    const data = await res.json();
    availableSquads = Array.isArray(data.squads) ? data.squads : [];
    defaultSquadName = data.default || "default";
    const saved = localStorage.getItem(SQUAD_PREF_KEY);
    selectedSquadName = (saved && availableSquads.some(s => s.name === saved))
      ? saved
      : defaultSquadName;
    renderSquadMenu();
    updateNewChatSubLabel();
  } catch (e) {
    // Non-fatal: an offline /api/squads just means the menu stays hidden
    // and new chats fall back to the server's default squad.
    console.error("failed to load squads:", e);
  }
}

// Show the selected squad as a subtitle under "New Chat" whenever the
// user has picked something other than the default. Keeps the primary
// affordance ("New Chat") readable while making the active squad
// visible at a glance.
function updateNewChatSubLabel() {
  const sub = els.newChat && els.newChat.querySelector(".new-chat-sub");
  if (!sub) return;
  if (selectedSquadName && selectedSquadName !== defaultSquadName) {
    sub.textContent = selectedSquadName;
    sub.hidden = false;
  } else {
    sub.textContent = "";
    sub.hidden = true;
  }
}

// SVG icon for a squad menu row. Kept generic (a small "team" glyph) so
// the menu stays consistent regardless of how the user names squads.
function squadIconSVG() {
  return '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg>';
}

function renderSquadMenu() {
  const menu = els.squadMenu;
  const toggle = els.squadToggle;
  if (!menu || !toggle) return;
  menu.innerHTML = "";
  if (availableSquads.length <= 1) {
    toggle.hidden = true;
    menu.hidden = true;
    return;
  }
  toggle.hidden = false;
  for (const sq of availableSquads) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "squad-menu-item" + (sq.name === selectedSquadName ? " selected" : "");
    btn.dataset.squad = sq.name;
    btn.setAttribute("role", "menuitem");
    btn.setAttribute("data-tip", sq.description || `${sq.leader} + ${(sq.members || []).join(", ")}`);
    btn.innerHTML = squadIconSVG();
    const label = document.createElement("span");
    label.className = "squad-menu-label";
    label.textContent = sq.name;
    btn.appendChild(label);
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      selectSquad(sq.name);
      closeSquadMenu();
    });
    menu.appendChild(btn);
  }
}

function selectSquad(name) {
  if (!availableSquads.some(s => s.name === name)) return;
  selectedSquadName = name;
  localStorage.setItem(SQUAD_PREF_KEY, name);
  // Update the .selected state without re-rendering the whole menu.
  for (const item of els.squadMenu.querySelectorAll(".squad-menu-item")) {
    item.classList.toggle("selected", item.dataset.squad === name);
  }
  updateNewChatSubLabel();
}

function openSquadMenu() {
  if (els.squadToggle.hidden) return;
  els.squadMenu.hidden = false;
  els.squadToggle.setAttribute("aria-expanded", "true");
}

function closeSquadMenu() {
  els.squadMenu.hidden = true;
  els.squadToggle.setAttribute("aria-expanded", "false");
}

function currentSquadChoice() {
  return selectedSquadName || defaultSquadName;
}

// newChat creates a fresh session and binds it into `panel` (the focused pane
// when omitted — e.g. the sidebar "New Chat" button). `squadOverride` picks the
// squad for this session only (used by the empty-pane picker); when omitted the
// globally-selected squad applies. `dirOverride` roots the session at a chosen
// folder (the Folders panel's "Open Chat here"); when omitted it starts at the
// fixed initial root. Returns the new id.
async function newChat(panel, squadOverride, dirOverride) {
  if (window.Settings && window.Settings.isOpen()) window.Settings.close();
  panel = panel || fp();
  if (!panel) { panel = createPanel(null); rebuildChatDOM(); setFocusedPanel(panel.id); }
  // Preserve the outgoing tab's half-typed message (it returns when reselected);
  // the new session starts with an empty composer (restored below).
  saveComposerDraft(panel);
  const squad = squadOverride || currentSquadChoice();
  try {
    const body = { squad };
    if (dirOverride) body.dir = dirOverride;
    const res = await apiFetch("/api/sessions", {
      method: "POST",
      body: JSON.stringify(body),
    });
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}));
      console.error("new chat failed:", errBody.error || res.statusText);
      return null;
    }
    const data = await res.json();
    const newId = data.session_id;
    // Persist the choice so the same squad is preselected next time.
    if (squad) localStorage.setItem(SQUAD_PREF_KEY, squad);

    // Open the new session as a tab in the pane and make it active. If the
    // active tab is a pending draft, the session takes that draft's slot.
    if (!panel.tabs.includes(newId)) {
      const ai = panel.tabs.indexOf(panel.activeTab);
      if (isDraft(panel.activeTab) && ai !== -1) panel.tabs[ai] = newId;
      else panel.tabs.push(newId);
    }
    panel.activeTab = newId;
    panel.sessionId = newId;
    panel.root.classList.remove("editing");
    panel.root.classList.remove("terminal");
    hidePanePicker(panel);
    setFocusedPanel(panel.id);
    clearPinnedPrompt(panel);
    renderPaneTabs(panel);
    mountInPanel(panel, newId);
    applySessionUI(newId);
    restoreComposerDraft(panel, newId); // fresh session → empty composer
    subscribeSessionEvents(newId);
    saveLayout();
    await loadSessions();
    return newId;
  } catch (e) { console.error(e); return null; }
}

// ─── Background push helpers ─────────────────────────────────────────────────

// subscribeGlobalEvents opens ONE persistent SSE connection (/api/events) that
// carries push events for every session, each tagged with its session_id. This
// replaces the old one-connection-per-session model, which exhausted the
// browser's ~6-per-host HTTP/1.1 connection limit once ~6 sessions were open —
// after that, every further request (loadSessions, message sends, …) stalled
// waiting for a free socket. With a single connection there is always headroom.
// Auto-reconnects with backoff so a dropped stream (server restart, network
// blip) re-establishes without a page reload.
let _globalEventsCtrl = null;
async function subscribeGlobalEvents() {
  if (_globalEventsCtrl) _globalEventsCtrl.abort();
  let backoff = 1000;
  while (true) {
    const ctrl = new AbortController();
    _globalEventsCtrl = ctrl;
    try {
      const res = await apiFetch("/api/events", { signal: ctrl.signal, cache: "no-store" });
      if (!res.ok) throw new Error("events stream " + res.status);
      backoff = 1000; // connected — reset backoff
      for await (const { event, data } of parseSSE(res)) {
        const sid = data && typeof data === "object" ? data.session_id : null;
        if (event === "mailbox_push" && sid && !sessionSending.has(sid)) {
          await appendNewPushTurns(sid);
        } else if (event === "task_notification" && sid) {
          // A background task / monitor produced a result. In active-wake mode
          // the server injected a synthetic turn (picked up by appendNewPushTurns);
          // in passive mode there is no new turn and the toast is the only signal.
          if (!sessionSending.has(sid)) await appendNewPushTurns(sid);
          notifyTaskEvent(sid);
        } else if (event === "chat_reply" && sid) {
          // A chat turn finished. This fires for EVERY completed reply on the
          // persistent /api/events stream — the same robust channel as
          // task_notification — so an OS notification still raises even when the
          // initiating tab was backgrounded (and its per-turn stream suspended).
          // notifyChatReply self-gates on the preference + the "user is away from
          // this session" check, and the notification's tag coalesces with any
          // duplicate the initiating tab's send path raised, so at most one shows.
          notifyChatReply(sid, (data && data.text) || "");
        } else if (event === "update_available") {
          // The self-update poller found a newer stable release — refresh the
          // sidebar button without waiting for a page reload.
          checkForUpdate();
        } else if (event === "ask_user" && data && typeof data === "object" && sid) {
          renderAskUserWidget(sid, data);
        } else if (event === "ask_user_cancel" && data && data.question_id) {
          cancelAskUserWidget(data.question_id);
        } else if (event === "session_created" && sid) {
          // Another browser (or this one) created a session — refresh the
          // sidebar so the new row appears. We never auto-open it.
          loadSessions();
        } else if (event === "session_deleted" && sid) {
          // Another browser deleted a session — drop all local state, close any
          // tab holding it, and re-render the sidebar. Idempotent if this
          // browser was the deleter (its own broadcast echoes back).
          forgetSession(sid);
          saveLayout();
          loadSessions();
        } else if (event === "session_renamed" && sid) {
          // Title changed elsewhere — re-render the sidebar to pick it up.
          loadSessions();
        } else if (event === "session_rewound" && sid) {
          // The session was rewound (here or in another browser) — rebuild the
          // truncated transcript from history. Idempotent on the originator,
          // which already re-rendered after its own POST. Skip while a turn is
          // streaming locally so we don't wipe an in-progress reply.
          if (!sessionSending.has(sid) && panelsWithTab(sid).length > 0) {
            rerenderSessionFromHistory(sid);
          }
        }
      }
    } catch (e) {
      if (e.name === "AbortError") return; // intentional teardown
      console.error("global events stream error:", e);
    }
    // Stream ended or errored — wait, then reconnect (capped backoff).
    await new Promise(r => setTimeout(r, backoff));
    backoff = Math.min(backoff * 2, 15000);
  }
}

// The per-session subscribe/unsubscribe helpers are retained as no-ops so the
// many call sites keep working: a single /api/events stream now covers every
// session, so there is nothing to open or close per session.
function subscribeSessionEvents(_sessionId) { /* covered by subscribeGlobalEvents */ }
function unsubscribeSessionEvents(_sessionId) { /* covered by subscribeGlobalEvents */ }

// notifyTaskEvent surfaces a background-task / monitor notification: an in-app
// toast always, plus an optional OS notification (gated by the Settings toggle
// `agent_toolkit_os_notify`) when the tab is backgrounded.
function notifyTaskEvent(sid) {
  showTaskToast(sid);
  // Fire the OS notification when the user isn't actively looking at omnis:
  // document.hidden covers a backgrounded/minimized tab; !document.hasFocus()
  // also covers switching to another *application* (where the tab stays active
  // but the window loses focus) — document.hidden alone misses that case.
  const away = document.hidden || !document.hasFocus();
  if (localStorage.getItem("agent_toolkit_os_notify") === "1" &&
      away && "Notification" in window &&
      Notification.permission === "granted") {
    try {
      const n = new Notification(paneTabTitle(sid), {
        body: "A background task or monitor reported a result.",
        tag: "omnis-task-" + sid,
      });
      n.onclick = () => { window.focus(); selectSession(sid); n.close(); };
    } catch { /* ignore */ }
  }
}

// notifyChatReply fires a desktop notification when a chat turn finishes while
// the user is NOT looking at that session — they switched to another session,
// or the window is hidden / unfocused. Gated by the same unified preference as
// notifyTaskEvent (localStorage cache, durable choice in the server prefs file).
function notifyChatReply(sessionId, replyText) {
  if (localStorage.getItem("agent_toolkit_os_notify") !== "1") return;
  if (!("Notification" in window) || Notification.permission !== "granted") return;
  // "Away" = the finished session is not the active tab in any visible pane
  // (different session) OR the whole window is backgrounded / unfocused.
  const away = document.hidden || !document.hasFocus() ||
    panelsForSession(sessionId).length === 0;
  if (!away) return;
  // De-dupe the two sources for one completed turn (send-path `finally` and the
  // global `chat_reply` event). They fire within a moment of each other; the
  // send-path runs first and carries the better final-answer text, so whichever
  // fires first wins and the other is suppressed within a short window. Without
  // this the user gets a second, redundant notification previewing the leader's
  // "handing off to <sub-agent>" narration.
  const now = Date.now();
  if (now - (sessionNotifiedAt.get(sessionId) || 0) < 8000) return;
  sessionNotifiedAt.set(sessionId, now);
  try {
    // The chat/session name is the bold title; the first lines of the reply are
    // the multi-line body, so the user gains some knowledge of the result
    // without switching back. (The browser appends its own origin source line
    // below — web code cannot rename or suppress it.)
    const title = paneTabTitle(sessionId);
    const preview = notificationPreview(replyText);
    const n = new Notification(title, {
      body: preview || "Finished responding.",
      tag: "omnis-chat-" + sessionId,
    });
    n.onclick = () => { window.focus(); selectSession(sessionId); n.close(); };
  } catch { /* ignore */ }
}

// notificationPreview turns a reply's raw markdown into a short, plain-text
// snippet for an OS-notification body. It keeps the first few non-empty lines
// (markdown markup stripped) AS SEPARATE LINES — joined with "\n" so the OS
// renders a multi-line notification — and caps the total length.
function notificationPreview(text, maxLines = 4, maxLen = 220) {
  if (!text) return "";
  const stripped = String(text)
    .replace(/```[\s\S]*?```/g, " ")            // fenced code blocks
    .replace(/`([^`]*)`/g, "$1")                // inline code
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")       // images
    .replace(/\[([^\]]*)\]\([^)]*\)/g, "$1")    // links → link text
    .replace(/^\s{0,3}#{1,6}\s+/gm, "")         // ATX headings
    .replace(/^\s*>\s?/gm, "")                  // blockquotes
    .replace(/^\s*[-*+]\s+/gm, "• ")            // unordered list bullets
    .replace(/[*_~]/g, "");                     // emphasis markers
  const lines = stripped
    .split("\n")
    .map((s) => s.replace(/\s+/g, " ").trim())
    .filter(Boolean)
    .slice(0, maxLines);
  let out = lines.join("\n");
  if (out.length > maxLen) out = out.slice(0, maxLen - 1).trimEnd() + "…";
  return out;
}

// requestDesktopNotifications asks the browser for notification permission and
// reports the state before/after. The browser only shows its native prompt while
// permission is still "default"; once "denied" (blocked at the site level) a
// website CANNOT re-grant it — only the user can, through the browser's own
// address-bar site controls. Callers use the before/after pair to tell an active
// "Block" (default → denied) from a pre-existing block (denied → denied).
async function requestDesktopNotifications() {
  if (!("Notification" in window)) return { before: "unsupported", after: "unsupported" };
  const before = Notification.permission;
  let after = before;
  if (before === "default") {
    try { after = await Notification.requestPermission(); }
    catch { after = Notification.permission; }
  }
  return { before, after };
}

// notificationUnblockHint returns a short, browser-specific instruction for the
// address-bar site controls a user must use to unblock notifications.
function notificationUnblockHint() {
  const ua = navigator.userAgent || "";
  if (/Firefox\//.test(ua))
    return tr("app.notify.unblockFirefox");
  if (/Safari\//.test(ua) && !/Chrome|Chromium|Edg|OPR/.test(ua))
    return tr("app.notify.unblockSafari");
  // Chromium family (Chrome, Edge, Brave, Opera) and anything else.
  return tr("app.notify.unblockChromium");
}

// showNotificationBlockedHelp explains how to allow notifications in the browser
// when the site-level permission is blocking them — a website can't grant this
// itself, so this is guidance, not an action.
function showNotificationBlockedHelp() {
  return uiConfirm({
    title: tr("app.notify.allowTitle"),
    message: tr("app.notify.allowMsg", { hint: notificationUnblockHint() }),
    confirmText: tr("common.gotIt"),
  });
}

// offerNotificationGrant shows an opt-in confirm (whose click is also the user
// gesture the native permission prompt needs), requests the browser permission,
// and persists the outcome via save(). Shared by the first-run opt-in and the
// boot-time reconciliation below.
async function offerNotificationGrant({ title, message, confirmText }, save) {
  const ok = await uiConfirm({ title, message, confirmText });
  if (!ok) { save(false); return; }
  const { before, after } = await requestDesktopNotifications();
  if (after === "granted") { save(true); return; }
  // User actively clicked "Block" in the just-shown native prompt → respect it.
  if (before === "default" && after === "denied") { save(false); return; }
  // They opted in but the browser is still blocking it (pre-denied, or the
  // prompt was suppressed/dismissed). Record the intent so it fires the moment
  // they unblock it, and show how — a website can't grant the permission.
  save(true);
  await showNotificationBlockedHelp();
}

// maybePromptLocale runs once on first launch. The UI defaults to English and
// NEVER auto-switches to the browser language (a non-English user may prefer the
// English UI). Instead, when the browser prefers a supported non-English language
// and the user has made no locale choice yet (neither locally nor server-side),
// we ASK — bilingually (English + the detected language) — whether to switch.
// Either answer is persisted so it's asked at most once per home dir.
async function maybePromptLocale() {
  try {
    if (!window.I18N) return;
    const detected = I18N.detectedLocale;
    if (!detected) return;            // browser is English / unsupported → nothing to offer
    if (I18N.localeStored) return;    // user already chose locally
    const S = window.Settings;
    const prefs = await (S && S.prefsReady ? S.prefsReady : Promise.resolve(null));
    // A server-side choice exists (this or another device already decided) → respect it.
    if (prefs && typeof prefs.locale === "string") return;
    // prefs unreachable (offline/unauthenticated): still offer locally.

    const lang = I18N.labelFor(detected); // native name, e.g. "Français"
    // Build a genuinely bilingual prompt: English first, then the detected
    // language. Buttons mirror the two choices in their respective languages.
    const titleEn = tr("app.locale.offerTitle");
    const titleLoc = I18N.trIn(detected, "app.locale.offerTitle");
    const title = titleEn === titleLoc ? titleEn : titleEn + " · " + titleLoc;
    const msgEn = tr("app.locale.offerMsg", { language: lang });
    const msgLoc = I18N.trIn(detected, "app.locale.offerMsg", { language: lang });
    const message = msgEn + "\n\n" + msgLoc;
    const confirmText = I18N.trIn(detected, "app.locale.useLanguage", { language: lang });
    const cancelText = tr("app.locale.keepEnglish");

    const ok = await uiConfirm({ title, message, confirmText, cancelText });
    if (ok) {
      I18N.setLocale(detected);       // persists + reloads into the new language
    } else {
      I18N.persistLocale(I18N.DEFAULT_LOCALE); // record "keep English"; no reload needed
    }
  } catch (e) { console.error("locale opt-in failed:", e); }
}

// maybePromptNotifications runs once on launch and does two things:
//   1. First run (no recorded choice) → ask the user to opt in.
//   2. Reconcile a recorded "enabled" intent with the per-browser permission.
//      The server-side intent and the browser's Notification permission are
//      independent: clearing the site's data/permissions resets the browser
//      grant to "default" (or it can be "denied") while omnis still believes
//      notifications are on — so every notification silently no-ops with no
//      message telling the user why. When that mismatch is detected we re-offer
//      the grant at startup (once per tab session, so we don't nag).
// The answer persists to the server prefs file (shared across browsers on the
// same home dir); Settings → Appearance lets the user change it later.
async function maybePromptNotifications() {
  try {
    const S = window.Settings;
    const prefs = await (S && S.prefsReady ? S.prefsReady : Promise.resolve(null));
    const save = (v) => (S && S.saveNotifications ? S.saveNotifications(v) : undefined);
    if (!prefs) return; // prefs unreachable
    const supported = "Notification" in window;

    // A choice was already recorded.
    if (typeof prefs.notifications === "boolean") {
      // Only the "enabled" intent can be out of sync with the browser; a
      // disabled or unsupported state has nothing to reconcile.
      if (!supported || prefs.notifications !== true) return;
      if (Notification.permission === "granted") return; // intent ⇄ grant agree
      // Reconcile at most once per tab session so a dismissed re-offer (or a
      // hard browser block) doesn't re-prompt on every navigation.
      if (sessionStorage.getItem("agent_toolkit_notify_resynced") === "1") return;
      sessionStorage.setItem("agent_toolkit_notify_resynced", "1");
      if (Notification.permission === "denied") {
        // A site can't re-grant a hard block — just explain how to unblock it.
        await showNotificationBlockedHelp();
        return;
      }
      // permission === "default" (e.g. site data was cleared): re-offer the grant.
      await offerNotificationGrant({
        title: "Re-enable desktop notifications?",
        message: "Notifications are turned on in Omnis, but your browser no longer has permission for this site (this happens when the site's data or permissions are cleared). Allow them again?",
        confirmText: "Allow notifications",
      }, save);
      return;
    }

    // First run — no recorded choice yet.
    if (!supported) { save(false); return; }
    await offerNotificationGrant({
      title: "Enable desktop notifications?",
      message: "Get a desktop notification when a chat finishes replying while you're on another session or app — and when a background task completes. You can change this any time in Settings → Appearance.",
      confirmText: "Enable notifications",
    }, save);
  } catch (e) { console.error("notification opt-in failed:", e); }
}

function showTaskToast(sid) {
  let layer = document.getElementById("task-toast-layer");
  if (!layer) {
    layer = document.createElement("div");
    layer.id = "task-toast-layer";
    document.body.appendChild(layer);
  }
  const el = document.createElement("button");
  el.className = "task-toast";
  el.type = "button";
  el.innerHTML = '<span class="task-toast-dot"></span>' +
    '<span class="task-toast-text"></span>';
  el.querySelector(".task-toast-text").textContent = tr("app.toast.bgTaskFinished");
  el.setAttribute("data-tip", tr("app.toast.openSession"));
  el.addEventListener("click", () => { selectSession(sid); el.remove(); });
  layer.appendChild(el);
  setTimeout(() => { el.classList.add("leaving"); }, 6000);
  setTimeout(() => { el.remove(); }, 6400);
}

// showToast pops a small, non-clickable, short-lived confirmation in the
// bottom-right corner, reusing the task-toast layer + styling. `kind` is "ok"
// (default, green check) or "err" (danger). Used e.g. to confirm a reply copy.
function showToast(text, kind) {
  let layer = document.getElementById("task-toast-layer");
  if (!layer) {
    layer = document.createElement("div");
    layer.id = "task-toast-layer";
    document.body.appendChild(layer);
  }
  const el = document.createElement("div");
  el.className = "task-toast toast-info" + (kind === "err" ? " toast-err" : "");
  el.innerHTML = '<span class="toast-glyph"></span><span class="task-toast-text"></span>';
  el.querySelector(".toast-glyph").textContent = kind === "err" ? "✕" : "✓";
  el.querySelector(".task-toast-text").textContent = text;
  layer.appendChild(el);
  setTimeout(() => { el.classList.add("leaving"); }, 2000);
  setTimeout(() => { el.remove(); }, 2400);
}

// rerenderSessionFromHistory clears a session's transcript and rebuilds it from
// the persisted history. Used as the recovery path when a reconnect can't replay
// the live stream cleanly (the server's frame buffer was trimmed) — it drops any
// partially-streamed bubbles and shows the durable turns instead.
async function rerenderSessionFromHistory(sessionId) {
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/messages`, { cache: "no-store" });
    if (!res.ok) return;
    const data = await res.json();
    const turns = data.turns || [];
    const container = getContainer(sessionId);
    container.innerHTML = "";
    for (let i = 0; i < turns.length; i++) {
      const t = turns[i];
      appendUserBubble(t.user_text, container, null, i);
      const bubble = appendAssistantBubble(container);
      renderMarkdown(bubble, t.assistant_text);
      setReplyTime(bubble, t.duration_ms);
    }
    sessionTurnCounts.set(sessionId, turns.length);
    for (const p of panelsForSession(sessionId)) requestAnimationFrame(() => scrollBottom(p));
  } catch (e) {
    console.error("rerenderSessionFromHistory failed:", e);
  }
}

// appendNewPushTurns fetches the full history and renders any turns that
// arrived after the last locally-known count (background turns).
async function appendNewPushTurns(sessionId) {
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/messages`);
    if (!res.ok) return;
    const data = await res.json();
    const turns = data.turns || [];
    const rendered = sessionTurnCounts.get(sessionId) ?? 0;
    if (turns.length <= rendered) return;

    const container = getContainer(sessionId);
    // Remove "No messages yet" placeholder before inserting real content.
    const placeholder = container.querySelector(".no-messages-placeholder");
    if (placeholder) placeholder.remove();

    for (let i = rendered; i < turns.length; i++) {
      appendUserBubble(turns[i].user_text, container, null, i);
      const bubble = appendAssistantBubble(container);
      renderMarkdown(bubble, turns[i].assistant_text);
      setReplyTime(bubble, turns[i].duration_ms);
    }
    sessionTurnCounts.set(sessionId, turns.length);

    for (const p of panelsForSession(sessionId)) {
      // Defer scroll until after the browser has reflowed the rendered markdown.
      requestAnimationFrame(() => scrollBottom(p));
      showPushBanner(container);
    }
    // Refresh sidebar turn counter.
    loadSessions();
  } catch (e) {
    console.error("appendNewPushTurns failed:", e);
  }
}

// showPushBanner inserts a temporary notice into the transcript container.
function showPushBanner(container) {
  const banner = document.createElement("div");
  banner.className = "push-banner";
  banner.textContent = tr("app.banner.bgMessage");
  container.appendChild(banner);
  setTimeout(() => banner.remove(), 4000);
}

// ─── SSE parser ──────────────────────────────────────────────────────────────

async function* parseSSE(res) {
  const reader = res.body.getReader();
  const decoder = new TextDecoder("utf-8");
  let buf = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let idx;
    while ((idx = buf.indexOf("\n\n")) >= 0) {
      const frame = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      let event = "message", data = "", id = 0;
      for (const line of frame.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data += line.slice(5).trim();
        else if (line.startsWith("id:")) id = parseInt(line.slice(3).trim(), 10) || 0;
      }
      let parsed = data;
      try { parsed = JSON.parse(data); } catch (_) { /* keep raw */ }
      yield { event, data: parsed, id };
    }
  }
}

// ─── Send message ────────────────────────────────────────────────────────────

// ─── Mid-turn steering ────────────────────────────────────────────────────────
// While a turn is computing, a submitted message becomes a "steering" note: it
// is queued on the server (POST /steer) so the agent picks it up at its next
// reasoning step. It is shown immediately appended to the in-flight question
// bubble it steers (a "[Sent while working]" block — the same shape the server
// persists when the model folds the note into the current turn). If the model
// never reaches the note it runs as a follow-up turn (a `steer_turn` frame),
// in which case those notes are moved out of the question bubble into their own
// user bubble so the live transcript matches the persisted/reloaded history.

// lastUserBubbleIn returns the `.bubble-user` of the most recent user-turn row
// in the container (the question currently being answered). Mailbox / background
// injected turns don't render a `.msg-row-user`, so this is the real question.
function lastUserBubbleIn(container) {
  const rows = (container || document).querySelectorAll(".msg-row-user");
  const row = rows[rows.length - 1];
  return row ? row.querySelector(".bubble-user") : null;
}

// The server folds steering the model consumed mid-turn into the persisted
// prompt with this marker. splitSteerText recovers the original prompt and the
// individual notes so both the live and reloaded transcripts can render the
// notes as chips (rather than as raw "[Sent while working]" text).
const STEER_MARKER = "\n\n[Sent while working]\n";
function splitSteerText(text) {
  const t = String(text || "");
  const idx = t.indexOf(STEER_MARKER);
  if (idx === -1) return { base: t, notes: [] };
  return {
    base: t.slice(0, idx),
    notes: t.slice(idx + STEER_MARKER.length).split("\n").filter(n => n.length > 0),
  };
}

// makeSteerChip builds one "↑"-tagged chip for a steering note (full text in the
// tooltip when truncated).
function makeSteerChip(note) {
  const chip = document.createElement("span");
  chip.className = "steer-chip";
  chip.setAttribute("data-tip", note);
  chip.innerHTML =
    `<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19V5"/><path d="m5 12 7-7 7 7"/></svg>` +
    `<span class="steer-chip-text"></span>`;
  chip.querySelector(".steer-chip-text").textContent =
    note.length > 120 ? note.slice(0, 120) + "…" : note;
  return chip;
}

// renderSteerChips (re)paints the steering-note chips on a user bubble, kept
// after the question text/attachments but before the truncation toggle.
function renderSteerChips(bubble, notes) {
  let wrap = bubble.querySelector(".bubble-steer");
  if (!notes || !notes.length) {
    if (wrap) wrap.remove();
    return;
  }
  if (!wrap) wrap = document.createElement("div");
  wrap.className = "bubble-steer";
  wrap.replaceChildren();
  for (const note of notes) wrap.appendChild(makeSteerChip(note));
  const toggle = bubble.querySelector(".bubble-user-toggle");
  if (toggle) bubble.insertBefore(wrap, toggle);
  else bubble.appendChild(wrap);
}

// renderUserBubbleSteer syncs a question bubble's steering chips + dataset.text
// from its `_steerNotes` (and remembered `textOriginal`). The base question text
// is untouched — steering shows as chips, not inline text.
function renderUserBubbleSteer(bubble) {
  const orig = bubble.dataset.textOriginal ?? bubble.dataset.text ?? "";
  const notes = bubble._steerNotes || [];
  bubble.dataset.text = notes.length ? orig + STEER_MARKER + notes.join("\n") : orig;
  renderSteerChips(bubble, notes);
}

// steerAppendToQuestion appends `note` to the in-flight question bubble (as a
// chip) and returns that bubble (or null). The original prompt is remembered
// once so the notes can be removed later.
function steerAppendToQuestion(sessionId, note) {
  const bubble = lastUserBubbleIn(getContainer(sessionId));
  if (!bubble) return null;
  if (bubble.dataset.textOriginal === undefined) bubble.dataset.textOriginal = bubble.dataset.text || "";
  (bubble._steerNotes || (bubble._steerNotes = [])).push(note);
  renderUserBubbleSteer(bubble);
  return bubble;
}

// steerUndoFromQuestion drops the most recently appended note (used when the
// turn had already finished and the note must be sent as a normal new turn).
function steerUndoFromQuestion(bubble) {
  if (!bubble || !bubble._steerNotes || !bubble._steerNotes.length) return;
  bubble._steerNotes.pop();
  renderUserBubbleSteer(bubble);
}

// steerExtractFromQuestion removes the suffix of appended notes that the server
// ran as a follow-up turn (its `steer_turn` text), so they can be re-rendered as
// their own user bubble. Pending notes are always a suffix of those sent (each
// model boundary drains all pending at once), so the smallest trailing run whose
// join matches `combined` is the set to move out.
function steerExtractFromQuestion(container, combined) {
  const bubble = lastUserBubbleIn(container);
  if (!bubble || !bubble._steerNotes || !bubble._steerNotes.length) return;
  const notes = bubble._steerNotes;
  for (let k = 1; k <= notes.length; k++) {
    if (notes.slice(notes.length - k).join("\n") === combined) {
      bubble._steerNotes = notes.slice(0, notes.length - k);
      renderUserBubbleSteer(bubble);
      return;
    }
  }
}

// steerMessage queues `note` on the in-flight turn. If the server reports no
// live turn (it finished in the gap between keypress and POST), it falls back to
// sending the note as an ordinary new turn.
async function steerMessage(panel, sessionId, note) {
  note = (note || "").trim();
  if (!sessionId || !note) return;
  // Show the note immediately, appended to the question it steers.
  const bubble = steerAppendToQuestion(sessionId, note);
  scrollBottom(panel, true);
  setSessionStatus(sessionId, tr("app.steer.queued"));
  let queued = false;
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/steer`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text: note }),
    });
    const data = await res.json().catch(() => ({}));
    queued = !!(data && data.queued);
  } catch (_) {
    queued = false;
  }
  if (!queued) {
    // The turn had already finished — undo the append and send it normally.
    steerUndoFromQuestion(bubble);
    sessionSending.delete(sessionId);
    if (!panel.els.prompt.value.trim()) {
      panel.els.prompt.value = note;
      autoGrowPrompt(panel);
    }
    await sendMessage(panel);
  }
}

async function sendMessage(panel) {
  panel = panel || fp();
  if (!panel) return;
  const prompt = panel.els.prompt.value.trim();
  const pendingFiles = getAttachments(panel.sessionId);
  if (!prompt && pendingFiles.length === 0) return;
  if (prompt.startsWith("/") && pendingFiles.length === 0) {
    panel.els.prompt.value = "";
    composerDrafts.delete(panel.activeTab);
    hideSlashMenu();
    await handleSlashCommand(prompt, panel);
    return;
  }
  // Bang shell-escape: "!<cmd>" runs directly on the host, bypassing the
  // agent (the hard safety floor still applies server-side).
  if (prompt.startsWith("!") && pendingFiles.length === 0) {
    panel.els.prompt.value = "";
    composerDrafts.delete(panel.activeTab);
    autoGrowPrompt(panel);
    hideSlashMenu();
    await runBangCommand(prompt.slice(1), panel);
    return;
  }
  // Hash memory: "#<text>" appends a one-line memory to the project AGENT.md
  // instead of being sent to the agent (symmetric with the "!" shell-escape).
  if (prompt.startsWith("#") && pendingFiles.length === 0) {
    panel.els.prompt.value = "";
    composerDrafts.delete(panel.activeTab);
    autoGrowPrompt(panel);
    hideSlashMenu();
    await runHashMemory(prompt.slice(1), panel);
    return;
  }
  if (!panel.sessionId) await newChat(panel);
  if (!panel.sessionId) return;

  // If a turn is already streaming for this session, this submission is a
  // mid-turn steering note (extra information, a remark, an insight) — not a new
  // turn. Hand it to the steering path, which queues it on the server so the
  // agent can pick it up at its next reasoning step. Attachments (if any) are
  // left in place for a normal turn once the current one finishes.
  if (sessionSending.has(panel.sessionId)) {
    const note = prompt;
    panel.els.prompt.value = "";
    composerDrafts.delete(panel.activeTab);
    autoGrowPrompt(panel);
    hideSlashMenu();
    await steerMessage(panel, panel.sessionId, note);
    return;
  }

  // Capture session identity and container. The user may switch the pane's
  // session mid-stream; these captured references keep writes flowing to the
  // right DOM.
  const sessionId = panel.sessionId;
  const files = getAttachments(sessionId);
  const container = getContainer(sessionId);
  mountInPanel(panel, sessionId);

  // Collect uploaded file paths. Images are passed as structured data so the
  // server can attach them as inline binary parts for vision-capable models.
  // Non-image files are currently ignored (the agent can still find them via
  // their on-disk paths if the user mentions them in the prompt).
  const filePaths = files.map(f => f.path);

  // Insert the user message into the transcript before streaming starts. The
  // new turn's index is the count of turns already persisted for this session.
  const newTurnIndex = sessionTurnCounts.get(sessionId) ?? 0;
  appendUserBubble(prompt, container, files.length > 0 ? files : null, newTurnIndex);
  scrollBottom(panel, true);
  panel.els.prompt.value = "";
  composerDrafts.delete(sessionId);
  autoGrowPrompt(panel);
  clearAttachments(sessionId);
  renderAttachmentsUI(sessionId);

  // Per-segment state: each burst of text between tool calls gets its own bubble.
  let segBubble = null;     // current assistant text element
  let segAcc = "";          // accumulated text for the current segment
  let segHadToken = false;  // whether we received streaming tokens this segment
  let lastReplyText = "";   // last non-empty text segment — used as the OS-notification preview

  function ensureSegment() {
    if (!segBubble) {
      segBubble = appendAssistantBubble(container);
      segAcc = "";
      segHadToken = false;
    }
    return segBubble;
  }

  // Stream incoming tokens via an incremental markdown renderer: completed
  // blocks (separated by a blank line outside fenced code) are promoted to
  // rendered HTML immediately; the trailing in-progress block stays as a
  // Text node (or a <pre><code> inside a fence) and keeps appending at wire
  // speed. See streamMdAdvance for the state machine.
  function scheduleRender() {
    if (!segBubble || !segAcc) return;
    streamMdAdvance(segBubble, segAcc);
  }

  // Seal the current text segment: flush any remaining in-progress block and
  // drop the streaming tail node.
  function finalizeSegment() {
    if (!segBubble) return;
    if (segAcc) {
      streamMdFinalize(segBubble, segAcc);
      lastReplyText = segAcc;
    } else {
      segBubble.remove();
    }
    segBubble = null;
    segAcc = "";
    segHadToken = false;
  }

  // Pending tool_call blocks awaiting their tool_result. Each entry is
  // { id, block }. When events carry a call_id we match by it; otherwise we
  // fall back to FIFO order (oldest pending entry first).
  const pendingTools = [];
  // Track the currently active outer block so nested sub-agent events can be
  // appended inside it, plus a list for the nested blocks themselves.
  let activeOuterBlock = null;
  const innerPending = [];

  const takePending = (queue, callID) => {
    if (callID) {
      const idx = queue.findIndex(e => e.id === callID);
      if (idx >= 0) return queue.splice(idx, 1)[0].block;
    }
    const head = queue.shift();
    return head ? head.block : null;
  };

  let ctrl = new AbortController();
  sessionAbortCtrls.set(sessionId, ctrl);
  sessionStopped.delete(sessionId);
  sessionSending.add(sessionId);
  setSessionBusy(sessionId, true);
  setSessionStatus(sessionId, "thinking…");
  applySessionUI(sessionId);

  // Highest SSE frame id processed so far. On a reconnect we ask the server to
  // replay only frames newer than this, so the transcript resumes seamlessly.
  let lastSeq = 0;
  // The finally below bumps the rendered-turn count by one (the normal case).
  // The history re-render path sets that count authoritatively, so it opts out.
  let skipTurnCount = false;

  // processStreamEvent applies one decoded SSE event to the live transcript.
  // Shared by the initial POST stream and any reconnect stream, so a dropped
  // connection resumes rendering into the same bubbles.
  function processStreamEvent(event, data) {
      switch (event) {
        case "token": {
          ensureSegment();
          if (!segHadToken) AgentDebug.firstToken();
          segHadToken = true;
          // Visible text is flowing — (re)assert the streaming label in case a
          // prior idle heartbeat flipped it to "working…".
          if ((sessionStatus.get(sessionId) || "") !== "streaming…") {
            setSessionStatus(sessionId, "streaming…");
          }
          const txt = data.text || "";
          segAcc += txt;
          AgentDebug.token(txt.length);
          scheduleRender();
          scrollBottom(panel);
          break;
        }

        case "debug_timing": {
          AgentDebug.serverTiming(data);
          break;
        }

        case "heartbeat": {
          // The turn is still alive on the server but no visible chat text has
          // arrived for a while — typically the model streaming a large
          // tool-call argument (e.g. the AGENT.md body during /init), which the
          // backend surfaces only once complete. Replace a frozen-looking
          // "streaming…"/"thinking…" with a ticking "working… (Ns)" so the turn
          // doesn't read as stuck. Leave an explicit "running <tool>…" alone.
          const cur = sessionStatus.get(sessionId) || "";
          if (cur === "streaming…" || cur === "thinking…" || cur.startsWith("working…")) {
            const secs = Math.round((data.elapsed_ms || 0) / 1000);
            setSessionStatus(sessionId, secs > 0 ? `working… (${secs}s)` : "working…");
          }
          break;
        }

        case "message": {
          // Non-streaming final text; skip if we already got streaming tokens.
          if (!segHadToken && data.text) {
            ensureSegment();
            segAcc = data.text;
            renderMarkdown(segBubble, segAcc);
            scrollBottom(panel);
          }
          break;
        }

        case "tool_call": {
          // Seal the preceding text segment before showing the tool.
          finalizeSegment();
          // Omnis routing/probe tools are internal control flow — never shown as
          // tool blocks (the `routing` chip is the visible signal; the capability
          // probe stays hidden). Their tool_result has no pending block, so it's
          // a no-op below.
          if (isRoutingTool(data.name)) {
            activeOuterBlock = null;
            setSessionStatus(sessionId, "thinking…");
            break;
          }
          // The todo_* tools render as a live, always-expanded checklist
          // instead of an opaque collapsed block. Their tool_result carries
          // no extra signal, so we don't track them in pendingTools.
          if (isTodoTool(data.name)) {
            const list = applyTodoEvent(sessionId, data.name, data.args);
            if (list) {
              appendTodoBlock(sessionId, list, container);
              activeOuterBlock = null;
              setSessionStatus(sessionId, "thinking…");
              break;
            }
          }
          const block = appendToolCall(data.name, data.args, container);
          pendingTools.push({ id: data.call_id || "", block });
          activeOuterBlock = block;
          innerPending.length = 0;
          setSessionStatus(sessionId, `running ${data.name}…`);
          break;
        }

        case "tool_result": {
          const block = takePending(pendingTools, data.call_id);
          if (block) resolveToolCall(block, data.response, data.duration_ms);
          activeOuterBlock = null;
          setSessionStatus(sessionId, "thinking…");
          break;
        }

        case "file_changed": {
          // The agent wrote to a file on disk; live-refresh any open editor tab
          // showing it (or flag it stale when it has unsaved edits), and reflect
          // a new/changed file in the Folders panel when it's in the visible dir.
          onAgentFileChanged(data.path);
          if (pathUnderFoldersDir(data.path)) scheduleFoldersRefresh();
          // A freshly *generated* file (the Write tool) is offered as a download
          // card in the transcript so the user can grab the artifact (a report,
          // PDF, markdown, …) without opening the Folders panel. Edits/reverts to
          // existing files don't render a card (they'd be noisy mid-coding) but
          // still refresh editors above.
          if ((data.tool || "") === "write") appendFileDownloadCard(sessionId, data.path, container);
          break;
        }

        case "agent_tool_call": {
          if (activeOuterBlock) {
            const inner = appendNestedToolCall(activeOuterBlock, data.name, data.args);
            innerPending.push({ id: data.call_id || "", block: inner });
          }
          break;
        }

        case "agent_tool_result": {
          const inner = takePending(innerPending, data.call_id);
          if (inner) resolveToolCall(inner, data.response, data.duration_ms);
          break;
        }

        case "agent_tool_error": {
          const inner = takePending(innerPending, data.call_id);
          if (inner) resolveToolCall(inner, { error: data.error });
          break;
        }

        case "context_usage": {
          sessionCtxUsage.set(sessionId, data);
          for (const p of panelsForSession(sessionId)) renderCtxRing(p);
          break;
        }

        case "turn_usage": {
          const acc = sessionTokenAccum.get(sessionId) || { prompt: 0, output: 0 };
          acc.prompt += (data.prompt_tokens || 0);
          acc.output += (data.output_tokens || 0);
          sessionTokenAccum.set(sessionId, acc);
          // Always accumulate per-agent tokens + frozen cost (the per-million
          // prices the server stamped at turn time, including the prompt-cache
          // read/creation rates). Used by the ctx popup budget and debug badge.
          AgentDebug.addAgentUsage(
            sessionId, data.agent,
            data.prompt_tokens || 0, data.output_tokens || 0,
            data.cache_read_tokens || 0, data.cache_create_tokens || 0,
            {
              in:          data.in_price_per_m || 0,
              out:         data.out_price_per_m || 0,
              cacheRead:   data.cache_read_price_per_m || 0,
              cacheCreate: data.cache_create_price_per_m || 0,
            },
          );
          for (const p of panelsForSession(sessionId)) {
            if (p.els.ctxPopup && !p.els.ctxPopup.hasAttribute("hidden")) renderCtxPopup(p);
          }
          if (AgentDebug.enabled && sessionId === activeSessionId) AgentDebug._paint();
          break;
        }

        case "error": {
          finalizeSegment();
          appendErrorBubble(data.message || String(data), container);
          break;
        }

        case "ask_user": {
          finalizeSegment();
          renderAskUserWidget(sessionId, data);
          break;
        }

        case "ask_user_cancel": {
          cancelAskUserWidget(data.question_id);
          break;
        }

        case "routing": {
          // The Omnis router handed control to another squad mid-turn. Close the
          // current text segment, drop a chip, and refresh the sidebar so the
          // session's squad badge reflects the new squad (the server already
          // persisted it before emitting this frame).
          finalizeSegment();
          appendRoutingChip(container, data.to, data.reason);
          loadSessions();
          break;
        }

        case "steer_turn": {
          // Steering the model didn't reach during the turn is run as a follow-up
          // turn. Close the current segment, move these notes out of the question
          // bubble they were provisionally appended to, render them as their own
          // user bubble, and let the follow-up reply stream into a fresh segment
          // below it.
          finalizeSegment();
          const combined = data.text || "";
          steerExtractFromQuestion(container, combined);
          appendUserBubble(combined, container, null);
          scrollBottom(panel, true);
          sessionTurnCounts.set(sessionId, (sessionTurnCounts.get(sessionId) ?? 0) + 1);
          break;
        }

        case "done":
          // Stamp the reply time onto the turn's final assistant bubble (next to
          // its copy button). segBubble is the live final text segment; fall back
          // to the last assistant bubble if the turn ended on a tool block.
          if (data && data.duration_ms != null) {
            const b = segBubble || lastAssistantBubbleIn(container);
            if (b) setReplyTime(b, data.duration_ms);
          }
          break;
      }
  }

  // consume drains one SSE Response, applying each event. Returns "done" when
  // the turn completed, "reload" when the server asked us to re-sync from
  // history (its replay buffer was trimmed), or "ended" when the stream closed
  // without a terminal event (a dropped connection — the caller reconnects).
  async function consume(res) {
    for await (const { event, data, id } of parseSSE(res)) {
      if (id > lastSeq) lastSeq = id;
      if (event === "reload") return "reload";
      processStreamEvent(event, data);
      if (event === "done") return "done";
    }
    return "ended";
  }

  // reconnectStream re-attaches to an in-flight turn after the connection drops,
  // replaying the frames it missed. It retries with capped backoff so a brief
  // proxy/Wi-Fi blip is invisible, until the turn finishes, the server reports it
  // already completed (204 → reload from history), the user Stops, or we give up.
  async function reconnectStream() {
    setSessionStatus(sessionId, "reconnecting…");
    let delay = 1000;
    let deadline = Date.now() + 60000; // up to ~60s of consecutive failures
    while (!sessionStopped.has(sessionId)) {
      await sleep(delay);
      if (sessionStopped.has(sessionId)) return "stopped";
      let res;
      try {
        ctrl = new AbortController();
        sessionAbortCtrls.set(sessionId, ctrl);
        res = await apiFetch(`/api/sessions/${sessionId}/messages/stream?from=${lastSeq}`, { signal: ctrl.signal });
      } catch (e) {
        if (sessionStopped.has(sessionId) || e.name === "AbortError") return "stopped";
        if (Date.now() > deadline) return "exhausted";
        delay = Math.min(delay * 2, 8000);
        continue;
      }
      if (res.status === 204) return "reload";   // turn already finished server-side
      if (!res.ok) {
        if (Date.now() > deadline) return "exhausted";
        delay = Math.min(delay * 2, 8000);
        continue;
      }
      // Reconnected — resume streaming. Reset the backoff window so a connection
      // that keeps making progress (even if flaky) is never abandoned.
      delay = 1000;
      deadline = Date.now() + 60000;
      setSessionStatus(sessionId, "streaming…");
      try {
        const out = await consume(res);
        if (out === "done" || out === "reload") return out;
        setSessionStatus(sessionId, "reconnecting…"); // "ended": dropped again
      } catch (e) {
        if (sessionStopped.has(sessionId) || e.name === "AbortError") return "stopped";
        setSessionStatus(sessionId, "reconnecting…");
      }
    }
    return "stopped";
  }

  let outcome;
  try {
    try {
      const res = await apiFetch(`/api/sessions/${sessionId}/messages`, {
        method: "POST",
        body: JSON.stringify({ prompt, ...(filePaths.length > 0 && { files: filePaths }) }),
        signal: ctrl.signal,
      });
      if (!res.ok) {
        const txt = await res.text();
        appendErrorBubble(tr("app.error.httpStatus", { status: res.status, text: txt }), container);
        outcome = "error";
      } else {
        AgentDebug.start(sessionId);
        outcome = await consume(res);
      }
    } catch (e) {
      // An AbortError means the user hit Stop (we abort our own controller).
      // Anything else is a network/connection drop → transparently reconnect.
      if (e.name === "AbortError" || sessionStopped.has(sessionId)) outcome = "stopped";
      else outcome = await reconnectStream();
    }
    // The initial stream can also close without a terminal event (server still
    // working, connection cut mid-flight): treat it as a drop and reconnect.
    if (outcome === "ended") outcome = await reconnectStream();

    if (outcome === "reload") {
      finalizeSegment();
      skipTurnCount = true;
      await rerenderSessionFromHistory(sessionId);
    } else if (outcome === "exhausted") {
      finalizeSegment();
      appendErrorBubble(tr("app.error.lostConnection"), container);
    } else if (outcome === "stopped") {
      finalizeSegment();
      appendErrorBubble(tr("app.error.stopped"), container);
    }
  } finally {
    finalizeSegment();
    // Steering notes appended to the question bubble stay there: the ones the
    // model consumed mid-turn match the server's persisted "[Sent while working]"
    // fold, and any that ran as a follow-up were already moved out by steer_turn.
    AgentDebug.end();
    // Clean up any still-pending tool dots (e.g. on cancel).
    for (const b of [...pendingTools, ...innerPending]) {
      const dot = b.querySelector(".tool-dot");
      if (dot) dot.classList.remove("pending");
    }
    sessionAbortCtrls.delete(sessionId);
    sessionStopped.delete(sessionId);
    sessionSending.delete(sessionId);
    setSessionBusy(sessionId, false);
    sessionStatus.delete(sessionId);
    applySessionUI(sessionId);
    // A reply finished: ping the user if they navigated away (different session
    // or backgrounded window). "done"/"reload" mean a reply is ready; "stopped",
    // "error" and "exhausted" do not (the user was present, or there's no reply).
    if (outcome === "done" || outcome === "reload") notifyChatReply(sessionId, lastReplyText);
    // Track turn count so appendNewPushTurns knows where to start. The history
    // re-render path already set it authoritatively, so skip the bump there.
    if (!skipTurnCount) sessionTurnCounts.set(sessionId, (sessionTurnCounts.get(sessionId) ?? 0) + 1);
    loadSessions();
    // Catch any filesystem changes the turn made that didn't surface a
    // `file_changed` event (e.g. folders created/removed via the Bash tool).
    if (sessionId === activeSessionId) scheduleFoldersRefresh();
    scrollBottom(panel);
  }
}

// ─── Slash commands ───────────────────────────────────────────────────────────

// Built-in commands are handled directly in handleSlashCommand below.
// User commands are loaded lazily from /api/user-commands and live in
// userSlashCommands. They expand to a prompt template that is then sent
// to the agent as a normal message (see invokeUserCommand).
const BUILTIN_SLASH_COMMANDS = [
  { cmd: "/help",          args: "",       desc: "Show available commands", builtin: true },
  { cmd: "/compress",      args: "",       desc: "Trigger context compression before the next model call", builtin: true },
  { cmd: "/create-skill",  args: "[name]", desc: "Create a new skill playbook with agent guidance", builtin: true },
  { cmd: "/update-skill",  args: "<name>", desc: "Update an existing skill playbook with agent guidance", builtin: true },
  { cmd: "/learn",         args: "[reason]", desc: "Mark session for soft-skill curation (runs on session end)", builtin: true },
  { cmd: "/learn-now",     args: "[reason]", desc: "Immediately run soft-skill curation and show result", builtin: true },
  { cmd: "/status",        args: "",       desc: "Show current session info", builtin: true },
  { cmd: "/init",          args: "",       desc: "Analyze the repo and write a starter AGENT.md", builtin: true },
];
const BUILTIN_NAMES = new Set(BUILTIN_SLASH_COMMANDS.map(c => c.cmd.slice(1)));

let userSlashCommands = []; // { name, description, args, prompt }
let userCommandsLoaded = false;
let userCommandsListeners = [];

async function loadUserCommands() {
  try {
    const r = await apiFetch("/api/user-commands");
    if (!r.ok) { userSlashCommands = []; return; }
    const j = await r.json();
    userSlashCommands = Array.isArray(j.commands) ? j.commands : [];
  } catch (_) {
    userSlashCommands = [];
  } finally {
    userCommandsLoaded = true;
    userCommandsListeners.forEach(fn => { try { fn(userSlashCommands); } catch (_) {} });
  }
}

function getUserCommands() { return userSlashCommands.slice(); }
function onUserCommandsChanged(fn) { userCommandsListeners.push(fn); }

function userCommandAsMenuEntry(uc) {
  return {
    cmd: "/" + uc.name,
    args: uc.args || "",
    desc: uc.description || "",
    builtin: false,
  };
}

function getAllSlashEntries() {
  return BUILTIN_SLASH_COMMANDS.concat(userSlashCommands.map(userCommandAsMenuEntry));
}

// The slash menu lives in the focused pane's composer (the user is typing
// there). All slash helpers resolve elements through fp().els.
let slashMenuFocusIdx = -1;
// The shared #slash-menu element doubles as the bang ("!") shell completion
// menu. menuMode tracks which content it currently holds so the keydown nav
// and selection logic dispatch correctly.
let menuMode = "slash"; // "slash" | "bang" | "at"
let bangReqSeq = 0;      // guards against out-of-order completion responses
let atReqSeq = 0;        // same, for "@file" reference completion
// atMenuState tracks the in-progress "@" reference so a chosen candidate
// replaces exactly its path token: { panel, tokenStart, pathLen }.
let atMenuState = null;

function renderSlashMenu(prefix) {
  menuMode = "slash";
  const panel = fp();
  if (!panel) return;
  const sm = panel.els.slashMenu;
  const p = prefix.toLowerCase();
  const all = getAllSlashEntries();
  const matches = p === "/" ? all : all.filter(c => c.cmd.startsWith(p));
  sm.innerHTML = "";

  if (matches.length === 0 && p !== "/") {
    hideSlashMenu();
    return;
  }

  // "+ Add command" header row — always present, opens the inline modal.
  const add = document.createElement("div");
  add.className = "slash-menu-item slash-menu-add";
  add.innerHTML = `<span class="slash-menu-add-icon">+</span><span class="slash-menu-add-label">Add command</span>`;
  add.addEventListener("mousedown", e => {
    e.preventDefault();
    hideSlashMenu();
    openUserCommandModal(null);
  });
  sm.appendChild(add);

  matches.forEach(item => {
    const row = document.createElement("div");
    row.className = "slash-menu-item" + (item.builtin ? "" : " is-user");
    row.dataset.value = item.cmd + (item.args ? " " : "");
    row.innerHTML =
      `<span class="slash-menu-cmd">${escHtml(item.cmd)}</span>` +
      (item.args ? `<span class="slash-menu-args">${escHtml(item.args)}</span>` : "") +
      `<span class="slash-menu-desc">${escHtml(item.desc)}</span>`;
    row.addEventListener("mousedown", e => {
      e.preventDefault(); // keep textarea focus
      selectSlashCommand(item.cmd + (item.args ? " " : ""));
    });
    sm.appendChild(row);
  });

  slashMenuFocusIdx = -1;
  sm.removeAttribute("hidden");
  panel.els.slashBtn.classList.add("active");
}

function hideSlashMenu() {
  // Hide the slash menu in every pane (only one is ever open, but the focused
  // pane may have changed between open and hide).
  for (const p of panels) {
    p.els.slashMenu.setAttribute("hidden", "");
    p.els.slashBtn.classList.remove("active");
  }
  slashMenuFocusIdx = -1;
}

function updateSlashMenuFocus() {
  const panel = fp();
  if (!panel) return;
  const items = panel.els.slashMenu.querySelectorAll(".slash-menu-item");
  items.forEach((it, i) => it.classList.toggle("focused", i === slashMenuFocusIdx));
  if (slashMenuFocusIdx >= 0 && items[slashMenuFocusIdx]) {
    items[slashMenuFocusIdx].scrollIntoView({ block: "nearest" });
  }
}

function selectSlashCommand(text) {
  const panel = fp();
  if (!panel) return;
  panel.els.prompt.value = text;
  panel.els.prompt.setSelectionRange(text.length, text.length);
  panel.els.prompt.focus();
  hideSlashMenu();
  autoGrowPrompt(panel);
}

// ─── Bang ("!") shell-escape ───────────────────────────────────────────────

// renderBangMenu fetches bash-like completions for the line typed after "!"
// and renders them into the shared slash-menu element. Stale responses (the
// user kept typing) are dropped via bangReqSeq.
async function renderBangMenu(panel, val) {
  const line = val.slice(1);
  if (line === "") { hideSlashMenu(); return; } // bare "!" — don't dump every command
  const seq = ++bangReqSeq;
  const q = new URLSearchParams({ line });
  if (panel.sessionId) q.set("session", panel.sessionId);
  let data;
  try {
    const res = await apiFetch(`/api/complete?${q.toString()}`);
    data = await res.json();
  } catch { return; }
  if (seq !== bangReqSeq) return;                  // superseded by a newer keystroke
  if (panel.els.prompt.value !== val) return;      // input changed while awaiting
  const cands = data.candidates || [];
  if (!cands.length) { hideSlashMenu(); return; }
  const prefix = "!" + line.slice(0, data.start || 0);
  const sm = panel.els.slashMenu;
  sm.innerHTML = "";
  menuMode = "bang";
  cands.forEach(c => {
    const row = document.createElement("div");
    row.className = "slash-menu-item";
    row.dataset.value = prefix + c;
    row.innerHTML = `<span class="slash-menu-cmd">${escHtml(c)}</span>`;
    row.addEventListener("mousedown", e => { e.preventDefault(); applyBangCompletion(panel, prefix + c); });
    sm.appendChild(row);
  });
  slashMenuFocusIdx = -1;
  sm.removeAttribute("hidden");
}

// applyBangCompletion splices a chosen completion into the composer and
// re-triggers completion (so e.g. completing a directory shows its contents).
function applyBangCompletion(panel, full) {
  panel.els.prompt.value = full;
  panel.els.prompt.setSelectionRange(full.length, full.length);
  panel.els.prompt.focus();
  hideSlashMenu();
  autoGrowPrompt(panel);
  if (full.endsWith("/")) renderBangMenu(panel, full);
}

// ─── "@file" reference completion ──────────────────────────────────────────

// atTokenAtCaret returns the path part of the "@" reference being typed
// immediately before the caret (start of buffer or after whitespace, so emails
// are excluded), or null when the caret is not inside such a token. The empty
// string is a valid result (caret right after a bare "@").
function atTokenAtCaret(el) {
  const upto = el.value.slice(0, el.selectionStart);
  const m = upto.match(/(?:^|\s)@(\S*)$/);
  return m ? m[1] : null;
}

// renderAtMenu fetches filesystem completions for the "@" reference at the
// caret and renders them into the shared slash-menu element.
async function renderAtMenu(panel) {
  const el = panel.els.prompt;
  const pathTok = atTokenAtCaret(el);
  if (pathTok === null) { hideSlashMenu(); return; }
  const tokenStart = el.selectionStart - pathTok.length; // index just after "@"
  const seq = ++atReqSeq;
  const q = new URLSearchParams({ path: pathTok });
  if (panel.sessionId) q.set("session", panel.sessionId);
  let data;
  try {
    const res = await apiFetch(`/api/complete-file?${q.toString()}`);
    data = await res.json();
  } catch { return; }
  if (seq !== atReqSeq) return;                 // superseded by a newer keystroke
  if (atTokenAtCaret(el) !== pathTok) return;   // input/caret changed while awaiting
  const cands = data.candidates || [];
  if (!cands.length) { hideSlashMenu(); return; }
  const sm = panel.els.slashMenu;
  sm.innerHTML = "";
  menuMode = "at";
  atMenuState = { panel, tokenStart, pathLen: pathTok.length };
  cands.forEach(c => {
    const row = document.createElement("div");
    row.className = "slash-menu-item";
    row.dataset.value = c;
    row.innerHTML = `<span class="slash-menu-cmd">@${escHtml(c)}</span>`;
    row.addEventListener("mousedown", e => { e.preventDefault(); applyAtCompletion(panel, c); });
    sm.appendChild(row);
  });
  slashMenuFocusIdx = -1;
  sm.removeAttribute("hidden");
}

// applyAtCompletion replaces the in-progress "@" reference's path token with the
// chosen candidate and re-triggers completion when a directory was picked.
function applyAtCompletion(panel, cand) {
  const st = atMenuState;
  if (!st) return;
  const el = panel.els.prompt;
  const before = el.value.slice(0, st.tokenStart);
  const after = el.value.slice(st.tokenStart + st.pathLen);
  el.value = before + cand + after;
  const caret = before.length + cand.length;
  el.setSelectionRange(caret, caret);
  el.focus();
  hideSlashMenu();
  autoGrowPrompt(panel);
  if (cand.endsWith("/")) renderAtMenu(panel); // drill into the directory
}

// runBangCommand executes a "!" shell command against a pane's session and
// renders the output as a local (non-streamed, non-persisted) bash block.
async function runBangCommand(command, panel) {
  command = command.trim();
  if (!command) return;
  if (!panel.sessionId) await newChat(panel);
  if (!panel.sessionId) return;
  const sessionId = panel.sessionId;
  const container = getContainer(sessionId);
  mountInPanel(panel, sessionId);
  appendUserBubble("!" + command, container);
  const block = appendBashBlock(container, command);
  scrollBottom(panel, true);
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/bash`, {
      method: "POST",
      body: JSON.stringify({ command }),
    });
    const data = await res.json();
    if (!res.ok) setBashBlockOutput(block, data.error || `error ${res.status}`, "");
    else {
      setBashBlockOutput(block, data.output || "", data.dir || "");
      // The command may have moved the cwd ("!cd") or created/removed files;
      // keep the Folders panel in sync either way.
      if (sessionId === activeSessionId) refreshFoldersPanel();
    }
  } catch (e) {
    setBashBlockOutput(block, "error: " + e, "");
  }
  scrollBottom(panel, true);
}

// runHashMemory appends a one-line memory ("#<text>") to the project AGENT.md
// resolved from the pane's working directory. It does not start a chat or send
// anything to the agent — symmetric with the "!" shell-escape.
async function runHashMemory(text, panel) {
  text = text.trim();
  if (!text) return;
  const base = panel.sessionId
    ? `/api/sessions/${panel.sessionId}/agentmd/append`
    : `/api/agentmd/append`;
  try {
    const res = await apiFetch(base, {
      method: "POST",
      body: JSON.stringify({ text }),
    });
    const data = await res.json();
    if (!res.ok) appendCommandBubble(data.error || `error ${res.status}`, true, panel);
    else {
      appendCommandBubble(`Saved to \`${data.path}\``, false, panel);
      refreshFoldersPanel();
    }
  } catch (e) {
    appendCommandBubble("error: " + e, true, panel);
  }
}

// appendBashBlock renders the command header + a pending output area.
function appendBashBlock(container, command) {
  const row = document.createElement("div");
  row.className = "msg-row";
  const block = document.createElement("div");
  block.className = "bash-block";
  const head = document.createElement("div");
  head.className = "bash-block-cmd";
  head.textContent = "$ " + command;
  const out = document.createElement("pre");
  out.className = "bash-block-out";
  out.textContent = "…";
  block.appendChild(head);
  block.appendChild(out);
  row.appendChild(block);
  if (container) container.appendChild(row);
  return block;
}

// setBashBlockOutput fills in a bash block's output and (optional) cwd footer.
function setBashBlockOutput(block, output, dir) {
  const out = block.querySelector(".bash-block-out");
  if (out) out.textContent = output || "(no output)";
  if (dir) {
    let foot = block.querySelector(".bash-block-cwd");
    if (!foot) {
      foot = document.createElement("div");
      foot.className = "bash-block-cwd";
      block.appendChild(foot);
    }
    foot.textContent = dir;
  }
}

// appendCommandBubble renders a local (non-streamed) reply for slash commands
// into a pane. Defaults to the focused pane when none is supplied.
function appendCommandBubble(text, isError = false, panel) {
  panel = panel || fp();
  const sessionId = panel ? panel.sessionId : null;
  const container = sessionId ? getContainer(sessionId) : (panel && panel.els.transcript);
  if (panel && sessionId) mountInPanel(panel, sessionId);
  const row = document.createElement("div");
  row.className = "msg-row";
  const bubble = document.createElement("div");
  if (isError) {
    bubble.className = "bubble-error";
    bubble.textContent = text;
  } else {
    bubble.className = "bubble-assistant rendered";
    renderMarkdown(bubble, text);
  }
  row.appendChild(bubble);
  if (container) container.appendChild(row);
  scrollBottom(panel, true);
}

// Substitute $1..$N positional args and $* (all args joined) in a user
// command's prompt template. When the template has no placeholders and
// the user supplied args, the args are appended on a new line so simple
// shortcuts (`/review` → "Review the diff") still pass extra context.
function applyUserCommandTemplate(promptTemplate, argText) {
  const argText_ = argText || "";
  const args = argText_ ? argText_.split(/\s+/) : [];
  const hasPlaceholder = /\$\d|\$\*/.test(promptTemplate);
  let out = promptTemplate
    .replace(/\$\*/g, argText_)
    .replace(/\$(\d+)/g, (_, n) => args[parseInt(n, 10) - 1] || "");
  if (!hasPlaceholder && argText_) out = out + "\n\n" + argText_;
  return out;
}

async function handleSlashCommand(raw, panel) {
  panel = panel || fp();
  if (!panel) return;
  const trimmed = raw.trim();
  const spaceIdx = trimmed.indexOf(" ");
  const cmdPart = spaceIdx >= 0 ? trimmed.slice(0, spaceIdx) : trimmed;
  const argPart = spaceIdx >= 0 ? trimmed.slice(spaceIdx + 1).trim() : "";
  const cmd = cmdPart.slice(1).toLowerCase();

  // User-defined commands take precedence over the unknown-command path.
  // They cannot shadow built-ins (enforced server-side via reservedNames).
  if (!BUILTIN_NAMES.has(cmd)) {
    const uc = userSlashCommands.find(c => c.name.toLowerCase() === cmd);
    if (uc) {
      const expanded = applyUserCommandTemplate(uc.prompt, argPart).trim();
      if (!expanded) {
        appendCommandBubble(`Command \`/${cmd}\` expanded to an empty prompt.`, true, panel);
        return;
      }
      panel.els.prompt.value = expanded;
      autoGrowPrompt(panel);
      await sendMessage(panel);
      return;
    }
  }

  switch (cmd) {
    case "help": {
      let body =
        "**Built-in commands**\n\n" +
        "- `/help` — Show this help\n" +
        "- `/compress` — Trigger context compression before the next model call\n" +
        "- `/create-skill [name]` — Create a new skill playbook with agent guidance\n" +
        "- `/update-skill <name>` — Update an existing skill playbook with agent guidance\n" +
        "- `/learn [reason]` — Mark session for soft-skill curation (runs on session end)\n" +
        "- `/learn-now [reason]` — Immediately run soft-skill curation and show result\n" +
        "- `/status` — Show current session info\n" +
        "- `/init` — Analyze the repo and write a starter AGENT.md\n\n" +
        "Tip: start a line with `#` to append a one-line memory to the project AGENT.md.";
      if (userSlashCommands.length) {
        body += "\n\n**User commands**\n\n" + userSlashCommands.map(c => {
          const args = c.args ? ` ${c.args}` : "";
          const desc = c.description ? ` — ${c.description}` : "";
          return `- \`/${c.name}${args}\`${desc}`;
        }).join("\n");
      }
      appendCommandBubble(body, false, panel);
      break;
    }

    case "status": {
      const sid = panel.sessionId || "none";
      appendCommandBubble(
        `**Session status**\n\n- Session: \`${sid}\`\n` +
        `- Use \`/learn\` to schedule soft-skill curation`,
        false, panel
      );
      break;
    }

    case "learn": {
      if (!panel.sessionId) {
        appendCommandBubble("No active session — start a chat first.", true, panel);
        return;
      }
      const reason = argPart || "manual /learn request from web UI";
      try {
        const res = await apiFetch(`/api/sessions/${panel.sessionId}/curate`, {
          method: "POST",
          body: JSON.stringify({ reason, immediate: false }),
        });
        if (!res.ok) {
          const d = await res.json();
          appendCommandBubble(d.error || "curate request failed", true, panel);
          return;
        }
        appendCommandBubble("Session marked for soft-skill curation — runs on session end.", false, panel);
      } catch (err) {
        appendCommandBubble(String(err), true, panel);
      }
      break;
    }

    case "learn-now": {
      if (!panel.sessionId) {
        appendCommandBubble("No active session — start a chat first.", true, panel);
        return;
      }
      const reason = argPart || "manual /learn-now request from web UI";
      const container = getContainer(panel.sessionId);
      mountInPanel(panel, panel.sessionId);
      const curatorBlock = appendCuratorBlock(container);
      scrollBottom(panel, true);
      try {
        const res = await apiFetch(`/api/sessions/${panel.sessionId}/curate`, {
          method: "POST",
          body: JSON.stringify({ reason, immediate: true }),
        });
        if (!res.ok) {
          const txt = await res.text();
          let errMsg = "curate request failed";
          try { const d = JSON.parse(txt); errMsg = d.error || errMsg; } catch (_) {}
          resolveCuratorBlock(curatorBlock, {}, errMsg);
          return;
        }
        let gotStart = false;
        for await (const { event, data } of parseSSE(res)) {
          if (event === "curator_start") {
            gotStart = true;
          } else if (event === "curator_end") {
            resolveCuratorBlock(curatorBlock, data || {}, (data && data.error) || null);
          } else if (event === "done") {
            break;
          }
        }
        // If the curator was skipped before emitting curator_start, gotStart
        // is false and curator_end was emitted directly — the block is already
        // resolved. Nothing extra to do.
        if (!gotStart) {
          // curator_end may not have arrived (timeout / empty session); show generic skip.
          const dot = curatorBlock.querySelector(".tool-dot");
          if (dot && dot.classList.contains("pending")) {
            resolveCuratorBlock(curatorBlock, { skipped: true, reason: "no response from curator" }, null);
          }
        }
      } catch (err) {
        resolveCuratorBlock(curatorBlock, {}, String(err));
      }
      break;
    }

    case "create-skill": {
      openSkillNameModal("Create skill", argPart.trim(), (name) => {
        sendSkillPrompt(
          `Create a new skill called "${name}". Load the skill-creator skill and guide me through defining it interactively.`,
          panel
        );
      });
      break;
    }

    case "update-skill": {
      openSkillNameModal("Update skill", argPart.trim(), (name) => {
        sendSkillPrompt(
          `Update the skill "${name}". Load the skill-creator skill and help me revise it.`,
          panel
        );
      });
      break;
    }

    case "init": {
      let prompt;
      try {
        const res = await apiFetch("/api/agentmd/init-prompt");
        const d = await res.json();
        prompt = d.prompt;
      } catch (err) {
        appendCommandBubble(String(err), true, panel);
        return;
      }
      if (!prompt) {
        appendCommandBubble("Could not load the /init prompt.", true, panel);
        return;
      }
      appendCommandBubble("Initializing AGENT.md — the agent will analyze the repo and write the file.", false, panel);
      await sendSkillPrompt(prompt, panel);
      break;
    }

    case "compress": {
      if (!panel.sessionId) {
        appendCommandBubble("No active session — start a chat first.", true, panel);
        return;
      }
      try {
        const res = await apiFetch(`/api/sessions/${panel.sessionId}/compact`, {
          method: "POST",
        });
        if (!res.ok) {
          const d = await res.json();
          appendCommandBubble(d.error || "compress request failed", true, panel);
          return;
        }
        appendCommandBubble("Context compression queued — runs before the next model call.", false, panel);
      } catch (err) {
        appendCommandBubble(String(err), true, panel);
      }
      break;
    }

    default:
      appendCommandBubble(`Unknown command: \`/${cmd}\` — try \`/help\``, true, panel);
  }
}

// ─── User command modal ─────────────────────────────────────────────────────
// Inline modal opened from the slash menu's "+ Add command" row and from
// the Settings → User Commands section (via window.UserCommands.openModal).
// State tracks which command, if any, is being edited.

let userCmdModalState = { editing: null, onSaved: null };

function openUserCommandModal(existing, opts) {
  userCmdModalState = { editing: existing || null, onSaved: (opts && opts.onSaved) || null };
  const isEdit = !!existing;
  els.userCmdTitle.textContent = isEdit ? tr("usercmd.editTitle") : tr("usercmd.addTitle");
  els.userCmdName.value = isEdit ? existing.name : "";
  els.userCmdDesc.value = isEdit ? (existing.description || "") : "";
  els.userCmdArgs.value = isEdit ? (existing.args || "") : "";
  els.userCmdPrompt.value = isEdit ? (existing.prompt || "") : "";
  els.userCmdError.hidden = true;
  els.userCmdError.textContent = "";
  els.userCmdOverlay.removeAttribute("hidden");
  // Focus first empty field for fastest entry.
  setTimeout(() => {
    if (!els.userCmdName.value) els.userCmdName.focus();
    else els.userCmdPrompt.focus();
  }, 0);
}

function closeUserCommandModal() {
  els.userCmdOverlay.setAttribute("hidden", "");
  userCmdModalState = { editing: null, onSaved: null };
}

function showUserCmdError(msg) {
  els.userCmdError.textContent = msg;
  els.userCmdError.hidden = false;
}

async function saveUserCommandFromModal() {
  const name = els.userCmdName.value.trim();
  const description = els.userCmdDesc.value.trim();
  const args = els.userCmdArgs.value.trim();
  const prompt = els.userCmdPrompt.value;

  if (!name) { showUserCmdError("Name is required."); return; }
  if (!/^[a-zA-Z0-9_-]{1,40}$/.test(name)) {
    showUserCmdError("Name must be 1–40 chars of letters, digits, '-' or '_'.");
    return;
  }
  if (BUILTIN_NAMES.has(name.toLowerCase())) {
    showUserCmdError(`/${name} is a built-in command.`);
    return;
  }
  if (!prompt.trim()) { showUserCmdError("Prompt is required."); return; }

  const body = JSON.stringify({ name, description, args, prompt });
  const original = userCmdModalState.editing && userCmdModalState.editing.name;
  const url = original
    ? `/api/user-commands/${encodeURIComponent(original)}`
    : "/api/user-commands";
  const method = original ? "PUT" : "POST";

  els.userCmdSave.disabled = true;
  try {
    const r = await apiFetch(url, { method, body });
    if (!r.ok) {
      let msg = `Save failed (${r.status})`;
      try { const j = await r.json(); if (j.error) msg = j.error; } catch (_) {}
      showUserCmdError(msg);
      return;
    }
    const j = await r.json();
    userSlashCommands = Array.isArray(j.commands) ? j.commands : userSlashCommands;
    userCommandsListeners.forEach(fn => { try { fn(userSlashCommands); } catch (_) {} });
    const cb = userCmdModalState.onSaved;
    closeUserCommandModal();
    if (cb) try { cb(); } catch (_) {}
  } catch (err) {
    showUserCmdError(String(err));
  } finally {
    els.userCmdSave.disabled = false;
  }
}

async function deleteUserCommand(name) {
  const r = await apiFetch(`/api/user-commands/${encodeURIComponent(name)}`, { method: "DELETE" });
  if (!r.ok) {
    let msg = `Delete failed (${r.status})`;
    try { const j = await r.json(); if (j.error) msg = j.error; } catch (_) {}
    throw new Error(msg);
  }
  const j = await r.json();
  userSlashCommands = Array.isArray(j.commands) ? j.commands : userSlashCommands;
  userCommandsListeners.forEach(fn => { try { fn(userSlashCommands); } catch (_) {} });
}

// Expose a tiny façade so settings.js can drive the same modal and CRUD
// without duplicating fetch logic.
window.UserCommands = {
  list: getUserCommands,
  refresh: loadUserCommands,
  onChanged: onUserCommandsChanged,
  openModal: openUserCommandModal,
  remove: deleteUserCommand,
  builtins: () => BUILTIN_SLASH_COMMANDS.slice(),
};

// ─── Context browser ─────────────────────────────────────────────────────────

const _ctxFolderIcon = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/></svg>`;
const _ctxFileIcon  = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/></svg>`;
const _ctxBackIcon  = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="15 18 9 12 15 6"/></svg>`;

function _ctxFmtSize(b) {
  if (b < 1024) return b + " B";
  if (b < 1048576) return (b / 1024).toFixed(1) + " KB";
  return (b / 1048576).toFixed(1) + " MB";
}

function _ctxSyncFooter() {
  const n = ctxBrowserSelected.size;
  els.ctxBrowserCount.textContent = n === 0 ? "No files selected" : `${n} file${n === 1 ? "" : "s"} selected`;
  els.ctxBrowserAdd.disabled = n === 0;
}

async function _ctxLoad(path) {
  const url = path ? `/api/browse?path=${encodeURIComponent(path)}` : "/api/browse";
  let data;
  try {
    const res = await apiFetch(url);
    if (!res.ok) return;
    data = await res.json();
  } catch (e) {
    console.error("browse error:", e);
    return;
  }

  els.ctxBrowserPath.textContent = data.path;
  els.ctxBrowserList.innerHTML = "";

  if (data.parent) {
    const li = document.createElement("li");
    li.className = "ctx-browser-item is-dir";
    li.innerHTML = `<span class="ctx-browser-item-icon">${_ctxBackIcon}</span><span class="ctx-browser-item-name">..</span>`;
    li.addEventListener("click", () => _ctxLoad(data.parent));
    els.ctxBrowserList.appendChild(li);
  }

  for (const e of (data.entries || [])) {
    const li = document.createElement("li");
    const sel = ctxBrowserSelected.has(e.path);
    li.className = `ctx-browser-item${e.is_dir ? " is-dir" : ""}${sel ? " selected" : ""}`;

    if (e.is_dir) {
      li.innerHTML = `<span class="ctx-browser-item-icon">${_ctxFolderIcon}</span><span class="ctx-browser-item-name">${e.name}</span>`;
      li.addEventListener("click", () => _ctxLoad(e.path));
    } else {
      li.innerHTML = `<span class="ctx-browser-item-icon">${_ctxFileIcon}</span><span class="ctx-browser-item-name">${e.name}</span><span class="ctx-browser-item-size">${_ctxFmtSize(e.size)}</span>`;
      li.addEventListener("click", () => {
        if (ctxBrowserSelected.has(e.path)) {
          ctxBrowserSelected.delete(e.path);
          li.classList.remove("selected");
        } else {
          ctxBrowserSelected.set(e.path, { name: e.name, path: e.path, size: e.size });
          li.classList.add("selected");
        }
        _ctxSyncFooter();
      });
    }
    els.ctxBrowserList.appendChild(li);
  }
}

async function openCtxBrowser() {
  ctxBrowserSelected.clear();
  _ctxSyncFooter();
  els.ctxBrowserOverlay.removeAttribute("hidden");
  await _ctxLoad("");
}

function closeCtxBrowser() {
  els.ctxBrowserOverlay.setAttribute("hidden", "");
  ctxBrowserSelected.clear();
}

// ─── Event listeners ─────────────────────────────────────────────────────────

// "New Chat" in the sidebar opens a new session in the focused pane.
els.newChat.addEventListener("click", () => newChat());

// Squad picker dropdown — chevron next to the New Chat button toggles
// a menu that picks which squad future sessions use. The menu is hidden
// outright when only the default squad exists.
els.squadToggle.addEventListener("click", (e) => {
  e.stopPropagation();
  if (els.squadMenu.hidden) openSquadMenu();
  else closeSquadMenu();
});
els.squadMenu.addEventListener("click", (e) => e.stopPropagation());
document.addEventListener("click", () => {
  closeSquadMenu();
  for (const p of panels) closePickerSquadMenu(p);
});
document.addEventListener("keydown", (e) => {
  if (e.key !== "Escape") return;
  if (!els.squadMenu.hidden) closeSquadMenu();
  for (const p of panels) closePickerSquadMenu(p);
});

// ─── Hover tooltips (data-tip) ────────────────────────────────────────────────
// A single body-appended `#tip-layer` element renders every `[data-tip]`
// tooltip. Because it is `position: fixed`, it escapes the sidebar /
// archived-list `overflow` clipping and sits above every panel — fixing the
// case where an action-button tooltip was masked by the surrounding panel.
// Placement is *above* the target by default, flipping below only when the
// target sits too close to the viewport top to fit. `.model-status-dot` keeps
// its dedicated CSS pseudo tooltip, so it is excluded here.
function initTooltips() {
  const layer = document.createElement("div");
  layer.id = "tip-layer";
  document.body.appendChild(layer);

  let current = null; // the [data-tip] element the tooltip is tracking

  function place() {
    if (!current) return;
    const r = current.getBoundingClientRect();
    const tw = layer.offsetWidth;
    const th = layer.offsetHeight;
    const gap = 8;
    const vw = window.innerWidth;
    const vh = window.innerHeight;

    // Prefer above; flip below when there isn't room near the top.
    let below = false;
    let top = r.top - th - gap;
    if (top < 4) { below = true; top = r.bottom + gap; }
    if (below && top + th > vh - 4) { below = false; top = r.top - th - gap; }
    layer.classList.toggle("below", below);

    // Centre horizontally over the target, clamped to the viewport.
    const center = r.left + r.width / 2;
    let left = center - tw / 2;
    left = Math.max(4, Math.min(left, vw - tw - 4));
    layer.style.left = left + "px";
    layer.style.top = top + "px";

    // Keep the arrow pointing at the target's centre even after clamping.
    const arrowX = Math.max(8, Math.min(center - left, tw - 8));
    layer.style.setProperty("--tip-arrow-x", arrowX + "px");
  }

  function show(el) {
    current = el;
    layer.textContent = el.getAttribute("data-tip") || "";
    layer.classList.remove("below");
    place();
    layer.classList.add("show");
  }
  function hide() {
    current = null;
    layer.classList.remove("show");
  }

  document.addEventListener("pointerover", (e) => {
    const el = e.target.closest && e.target.closest("[data-tip]");
    if (!el || el.classList.contains("model-status-dot")) { if (current) hide(); return; }
    if (el === current) return;
    show(el);
  });
  document.addEventListener("pointerout", (e) => {
    if (!current) return;
    const to = e.relatedTarget;
    if (to && to.closest && to.closest("[data-tip]") === current) return;
    hide();
  });
  // Any layout shift invalidates the cached rect — drop the tooltip.
  window.addEventListener("scroll", hide, true);
  window.addEventListener("resize", hide);
  document.addEventListener("click", hide, true);
}
initTooltips();

// The composer, attach menu, prompt keys, drag-and-drop, cancel, slash button,
// context ring and composer-resize listeners are wired per-pane in
// attachPaneHandlers (each pane owns its own copy of these elements). A few
// document-level fallbacks below close per-pane flyouts on an outside click.

// Clicks outside any attach menu close every open one.
document.addEventListener("click", () => {
  for (const p of panels) p.els.attachMenu.setAttribute("hidden", "");
});

// ─── Skill name modal ────────────────────────────────────────────────────────
// Lightweight single-field modal used by /create-skill and /update-skill.
// On submit it calls the provided onConfirm(name) callback.

const SKILL_NAME_RE = /^[a-z0-9][a-z0-9._-]{0,62}$/;
let skillNameModalCallback = null;

function openSkillNameModal(title, prefill, onConfirm) {
  skillNameModalCallback = onConfirm;
  els.skillNameTitle.textContent = title;
  els.skillNameInput.value = prefill || "";
  els.skillNameError.hidden = true;
  els.skillNameError.textContent = "";
  els.skillNameOverlay.removeAttribute("hidden");
  setTimeout(() => els.skillNameInput.focus(), 0);
}

function closeSkillNameModal() {
  els.skillNameOverlay.setAttribute("hidden", "");
  skillNameModalCallback = null;
}

function confirmSkillNameModal() {
  const name = els.skillNameInput.value.trim();
  if (!name) {
    els.skillNameError.textContent = tr("app.skill.nameRequired");
    els.skillNameError.hidden = false;
    return;
  }
  if (!SKILL_NAME_RE.test(name)) {
    els.skillNameError.textContent = tr("app.skill.nameInvalid");
    els.skillNameError.hidden = false;
    return;
  }
  const cb = skillNameModalCallback;
  closeSkillNameModal();
  if (cb) cb(name);
}

// Sends a pre-crafted prompt to a pane's session (creating one if needed).
async function sendSkillPrompt(prompt, panel) {
  panel = panel || fp();
  if (!panel) return;
  if (!panel.sessionId) await newChat(panel);
  if (!panel.sessionId) return;
  panel.els.prompt.value = prompt;
  autoGrowPrompt(panel);
  await sendMessage(panel);
}

els.skillNameClose.addEventListener("click", closeSkillNameModal);
els.skillNameCancel.addEventListener("click", closeSkillNameModal);
els.skillNameStart.addEventListener("click", confirmSkillNameModal);
els.skillNameOverlay.addEventListener("click", (e) => {
  if (e.target === els.skillNameOverlay) closeSkillNameModal();
});
els.skillNameInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") { e.preventDefault(); confirmSkillNameModal(); }
  if (e.key === "Escape") { e.preventDefault(); closeSkillNameModal(); }
});
document.addEventListener("keydown", (e) => {
  if (!els.skillNameOverlay.hasAttribute("hidden") && e.key === "Escape") {
    e.preventDefault();
    closeSkillNameModal();
  }
});

// User command modal handlers
els.userCmdClose.addEventListener("click", closeUserCommandModal);
els.userCmdCancel.addEventListener("click", closeUserCommandModal);
els.userCmdOverlay.addEventListener("click", (e) => {
  if (e.target === els.userCmdOverlay) closeUserCommandModal();
});
els.userCmdSave.addEventListener("click", saveUserCommandFromModal);
document.addEventListener("keydown", (e) => {
  if (!els.userCmdOverlay.hasAttribute("hidden") && e.key === "Escape") {
    e.preventDefault();
    closeUserCommandModal();
  }
});

// Context browser event handlers
els.ctxBrowserClose.addEventListener("click", closeCtxBrowser);
els.ctxBrowserCancel.addEventListener("click", closeCtxBrowser);
els.ctxBrowserOverlay.addEventListener("click", (e) => {
  if (e.target === els.ctxBrowserOverlay) closeCtxBrowser();
});
els.ctxBrowserAdd.addEventListener("click", async () => {
  if (!ctxBrowserSelected.size) return;
  const panel = fp();
  if (!panel) { closeCtxBrowser(); return; }
  if (!panel.sessionId) await newChat(panel);
  if (!panel.sessionId) { closeCtxBrowser(); return; }
  const sid = panel.sessionId;
  for (const f of ctxBrowserSelected.values()) addAttachment(sid, f);
  renderAttachmentsUI(sid);
  closeCtxBrowser();
});

// (file upload, paste, drag-and-drop and cancel are wired per-pane in
// attachPaneHandlers via uploadPickedFiles.)

function updateEditModeBtn() {
  // The Enter-key mode is a global preference; reflect it on every pane.
  for (const p of panels) {
    p.els.editModeBtn.classList.toggle("active", !sendOnEnter);
    p.els.editModeBtn.dataset.tip = sendOnEnter
      ? "Edit mode: switch to Enter=new line, Ctrl+Enter=send"
      : "Send mode: switch to Enter=send, Ctrl+Enter=new line";
    p.els.prompt.placeholder = archivedSessions.has(p.sessionId)
      ? "Session archived — unarchive to continue the conversation"
      : (sendOnEnter ? "Message the agent… (Enter to send)" : "Message the agent… (Ctrl+Enter to send)");
  }
}
// Clicking outside the focused pane's composer closes its slash menu.
document.addEventListener("mousedown", (e) => {
  const open = panels.find(p => !p.els.slashMenu.hasAttribute("hidden"));
  if (open && !open.els.composerWrap.contains(e.target)) hideSlashMenu();
});

// ─── Composer resize ─────────────────────────────────────────────────────────

// Minimum height of the EDITOR (textarea/prompt-stack) in manual-resize mode —
// --composer-h now sizes the editor, not the whole wrap (which auto-grows for
// attachments + actions). ~1 text lines: 1*1.5em(15px) + 20px padding + 2px
// border + 8 px padding ≈ 45px.
const COMPOSER_MIN_H  = 45;
const COMPOSER_H_KEY  = "agent_toolkit_composer_h";
const MAX_AUTO_LINES  = 10;

let composerDragging       = false;
let composerDragStartY     = 0;
let composerDragStartH     = 0;
let composerManuallyResized = false;

// Composer height is a global CSS var shared by every pane's composer.
function setComposerHeight(h) {
  const clamped = Math.max(COMPOSER_MIN_H, h);
  document.documentElement.style.setProperty("--composer-h", clamped + "px");
  localStorage.setItem(COMPOSER_H_KEY, clamped + "px");
  if (!composerManuallyResized) {
    composerManuallyResized = true;
    for (const p of panels) {
      p.els.composerWrap.classList.add("is-manual");
      p.els.prompt.style.height = "";
    }
  }
}

function autoGrowPrompt(panel) {
  panel = panel || fp();
  if (!panel) return;
  renderPromptHighlight(panel);
  if (composerManuallyResized) return;
  const el = panel.els.prompt;
  const cs = getComputedStyle(el);
  const lineH  = parseFloat(cs.lineHeight);
  const padY   = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
  const maxH   = lineH * MAX_AUTO_LINES + padY;
  el.style.height = "auto";
  const natural = Math.min(el.scrollHeight, maxH);
  el.style.height = natural + "px";
  el.style.overflowY = el.scrollHeight > maxH ? "auto" : "hidden";
}

// ─── Composer "@file" reference highlighting ───────────────────────────────

// renderPromptHighlight rebuilds the backdrop layer behind the composer
// textarea so valid "@path" references show in colour while the user types.
// The textarea's own text is transparent (see CSS), so the backdrop is what
// the user sees. Validity is cached per panel and resolved server-side.
function renderPromptHighlight(panel) {
  const hl = panel.els.promptHighlight;
  if (!hl) return;
  hl.innerHTML = highlightRefsHTML(panel.els.prompt.value, panel);
  syncPromptHighlightScroll(panel);
}

// syncPromptHighlightScroll keeps the backdrop aligned with the textarea when
// its content scrolls (manual-resize mode, long prompts).
function syncPromptHighlightScroll(panel) {
  const hl = panel.els.promptHighlight, el = panel.els.prompt;
  if (!hl || !el) return;
  hl.scrollTop = el.scrollTop;
  hl.scrollLeft = el.scrollLeft;
}

// highlightRefsHTML returns escaped HTML mirroring text, with valid "@file"
// references wrapped in coloured spans. Unknown tokens render plain and are
// queued for a debounced server resolve that repaints when they come back.
function highlightRefsHTML(text, panel) {
  const cache = panel._fileRefKinds || (panel._fileRefKinds = new Map());
  const inflight = panel._fileRefInflight || (panel._fileRefInflight = new Set());
  const re = /(^|\s)@(\S+)/g;
  let out = "", last = 0, m;
  const need = [];
  while ((m = re.exec(text)) !== null) {
    let token = m[2];
    const tm = token.match(FILE_REF_TRAILING_RE);
    const trailing = tm ? tm[0] : "";
    if (trailing) token = token.slice(0, token.length - trailing.length);
    if (!token) continue;
    const atIdx = m.index + m[1].length;
    out += escHtml(text.slice(last, atIdx));
    const kind = cache.get(token);
    if (kind === "file" || kind === "dir") {
      const cls = kind === "dir" ? "file-ref file-ref-dir" : "file-ref";
      out += `<span class="${cls}">@${escHtml(token)}</span>`;
    } else {
      out += escHtml("@" + token);
      if (kind === undefined && !inflight.has(token)) need.push(token);
    }
    out += escHtml(trailing);
    last = atIdx + 1 + token.length + trailing.length;
  }
  out += escHtml(text.slice(last));
  if (text.endsWith("\n")) out += " "; // make a trailing blank line render under pre-wrap
  if (need.length) scheduleRefResolve(panel, need);
  return out;
}

// scheduleRefResolve batches+debounces classification of new "@" tokens, caches
// the result on the panel, then repaints the backdrop.
function scheduleRefResolve(panel, tokens) {
  const inflight = panel._fileRefInflight || (panel._fileRefInflight = new Set());
  const pending = panel._fileRefPending || (panel._fileRefPending = new Set());
  for (const t of tokens) { inflight.add(t); pending.add(t); }
  clearTimeout(panel._fileRefTimer);
  panel._fileRefTimer = setTimeout(async () => {
    const batch = [...pending];
    pending.clear();
    const cache = panel._fileRefKinds || (panel._fileRefKinds = new Map());
    try {
      const res = await apiFetch("/api/fileref/resolve", {
        method: "POST",
        body: JSON.stringify({ paths: batch, session: panel.sessionId || "" }),
      });
      const kinds = (await res.json()).kinds || {};
      batch.forEach(t => cache.set(t, kinds[t] || "missing"));
    } catch {
      /* leave uncached so a later keystroke retries */
    } finally {
      batch.forEach(t => inflight.delete(t));
    }
    renderPromptHighlight(panel);
  }, 150);
}

// onCompactClick backs each pane's "Compress Now" button in the ctx popup.
async function onCompactClick(e, panel) {
  e.stopPropagation();
  if (!panel.sessionId) return;
  const btn = panel.els.ctxCompactBtn;
  btn.disabled = true;
  btn.textContent = tr("app.ctx.queuing");
  try {
    const res = await apiFetch(`/api/sessions/${panel.sessionId}/compact`, { method: "POST" });
    if (!res.ok) throw new Error(await res.text());
    btn.textContent = tr("app.ctx.queued");
  } catch (err) {
    console.error("compact request failed:", err);
    btn.textContent = tr("common.error");
  }
  setTimeout(() => { btn.disabled = false; btn.textContent = tr("ctx.compressNow"); closeCtxPopup(panel); }, 1400);
}

// ─── Sidebar resize & toggle ─────────────────────────────────────────────────

const SIDEBAR_MIN_W   = 140;
const SIDEBAR_MAX_W   = 500;
const SIDEBAR_W_KEY   = "agent_toolkit_sidebar_w";
const SIDEBAR_COL_KEY = "agent_toolkit_sidebar_collapsed";

let sidebarDragging = false;

function setSidebarWidth(px) {
  document.documentElement.style.setProperty("--sidebar-w", px + "px");
  localStorage.setItem(SIDEBAR_W_KEY, px + "px");
}

function collapseSidebar() {
  els.sidebar.classList.add("collapsed");
  els.sidebarToggle.setAttribute("data-tip", "Show sidebar");
  localStorage.setItem(SIDEBAR_COL_KEY, "1");
}

function expandSidebar() {
  els.sidebar.classList.remove("collapsed");
  els.sidebarToggle.setAttribute("data-tip", "Hide sidebar");
  localStorage.setItem(SIDEBAR_COL_KEY, "0");
}

els.sidebarResize.addEventListener("mousedown", (e) => {
  if (els.sidebar.classList.contains("collapsed")) {
    expandSidebar();
    return;
  }
  sidebarDragging = true;
  els.sidebarResize.classList.add("is-dragging");
  document.body.classList.add("resizing");
  document.body.style.userSelect = "none";
  e.preventDefault();
});

document.addEventListener("mousemove", (e) => {
  if (composerDragging) {
    const newH = composerDragStartH + (composerDragStartY - e.clientY);
    setComposerHeight(newH);
  }
  if (sidebarDragging) {
    const w = Math.min(SIDEBAR_MAX_W, Math.max(SIDEBAR_MIN_W, e.clientX));
    setSidebarWidth(w);
    layoutWidths(); // chat width changed live; refill panes (transition is off while dragging)
  }
  if (paneDividerDrag) {
    const d = paneDividerDrag;
    const totalPair = d.startLeftW + d.startRightW;
    const delta = e.clientX - d.startX;
    let leftW = Math.max(PANE_MIN_W, Math.min(totalPair - PANE_MIN_W, d.startLeftW + delta));
    d.left.width = leftW;
    d.right.width = totalPair - leftW;
    applyPaneWidths();
  }
});

document.addEventListener("mouseup", () => {
  if (composerDragging) {
    composerDragging = false;
    for (const p of panels) p.els.composerResize.classList.remove("is-dragging");
    document.body.classList.remove("resizing-composer");
    document.body.style.userSelect = "";
  }
  if (sidebarDragging) {
    sidebarDragging = false;
    els.sidebarResize.classList.remove("is-dragging");
    document.body.classList.remove("resizing");
    document.body.style.userSelect = "";
  }
  if (paneDividerDrag) {
    paneDividerDrag = null;
    for (const d of els.chat.querySelectorAll(".pane-divider")) d.classList.remove("is-dragging");
    document.body.classList.remove("resizing");
    document.body.style.userSelect = "";
    refitVisibleTerminals();
    saveLayout();
  }
});

els.sidebarToggle.addEventListener("click", () => {
  if (els.sidebar.classList.contains("collapsed")) expandSidebar();
  else collapseSidebar();
});

// Collapsing/expanding the sidebar animates its width (0.15s), which changes
// the chat area's available width. The panes are sized in fixed pixels, so
// they must be re-normalized to refill the chat area once the animation ends —
// otherwise the tabbar/panes keep their old widths until a reload. (A sidebar
// drag disables the transition and so won't fire this; the drag path handles
// its own width via the resize loop.)
els.sidebar.addEventListener("transitionend", (e) => {
  if (e.target === els.sidebar && e.propertyName === "width") layoutWidths();
});

// ─── Archived sessions panel collapse ─────────────────────────────────────────

const ARCHIVED_COL_KEY = "agent_archived_collapsed";
function applyArchivedCollapse(collapsed) {
  els.archivedPanel.classList.toggle("collapsed", collapsed);
  els.archivedHeader.setAttribute("aria-expanded", String(!collapsed));
}
// Default collapsed; persists the user's choice across reloads.
applyArchivedCollapse(localStorage.getItem(ARCHIVED_COL_KEY) !== "0");
function toggleArchivedPanel() {
  const collapsed = !els.archivedPanel.classList.contains("collapsed");
  applyArchivedCollapse(collapsed);
  localStorage.setItem(ARCHIVED_COL_KEY, collapsed ? "1" : "0");
}
els.archivedHeader.addEventListener("click", toggleArchivedPanel);
els.archivedHeader.addEventListener("keydown", (e) => {
  if (e.key === "Enter" || e.key === " ") { e.preventDefault(); toggleArchivedPanel(); }
});

// ─── Folders browser panel ────────────────────────────────────────────────────
// Browses the active session's working directory — the same process-wide cwd the
// "!cd" shell-escape mutates — so navigating here is equivalent to typing "!cd".
const FOLDERS_COL_KEY = "agent_folders_collapsed";
let foldersDir = "";          // last-rendered directory
function foldersCollapsed() { return els.foldersPanel.classList.contains("collapsed"); }
function applyFoldersCollapse(collapsed) {
  els.foldersPanel.classList.toggle("collapsed", collapsed);
  els.foldersHeader.setAttribute("aria-expanded", String(!collapsed));
}
// Default collapsed; persists the user's choice across reloads.
applyFoldersCollapse(localStorage.getItem(FOLDERS_COL_KEY) !== "0");
function toggleFoldersPanel() {
  const collapsed = !foldersCollapsed();
  applyFoldersCollapse(collapsed);
  localStorage.setItem(FOLDERS_COL_KEY, collapsed ? "1" : "0");
  if (!collapsed) loadFolder();
}
els.foldersHeader.addEventListener("click", toggleFoldersPanel);
els.foldersHeader.addEventListener("keydown", (e) => {
  if (e.key === "Enter" || e.key === " ") { e.preventDefault(); toggleFoldersPanel(); }
});

// Drag the top border of the folders panel to resize the listing's height.
// The list's max-height is overridden inline (px); the choice persists across
// reloads. Dragging up grows the list, down shrinks it.
const FOLDERS_H_KEY = "agent_folders_height";
const FOLDERS_H_MIN = 60;
// Reserve at least this much for the session list (~two rows + a timeframe
// header) when capping how tall the folders panel may grow. Mirrors the
// #session-list min-height floor in features/sidebar.css.
const SESSION_LIST_MIN = 130;
// foldersHeightCap is the largest the folder listing may grow to. The folder
// listing and the session list share the sidebar's flexible space (everything
// else is fixed chrome); we leave SESSION_LIST_MIN of it for the sessions. When
// the panel is collapsed or not yet laid out (clientHeight 0) that measurement
// is unreliable, so fall back to a viewport-based cap that still reserves the
// session minimum (and the footer's room).
function foldersHeightCap() {
  const viewCap = Math.max(FOLDERS_H_MIN, window.innerHeight - 60 - SESSION_LIST_MIN);
  const flexible = els.foldersList.clientHeight + els.list.clientHeight;
  if (flexible <= SESSION_LIST_MIN + FOLDERS_H_MIN) return viewCap; // unreliable
  return Math.max(FOLDERS_H_MIN, Math.min(viewCap, flexible - SESSION_LIST_MIN));
}
function applyFoldersHeight(px) {
  const h = Math.round(Math.min(foldersHeightCap(), Math.max(FOLDERS_H_MIN, px)));
  els.foldersList.style.maxHeight = h + "px";
  return h;
}
(function () {
  const saved = parseInt(localStorage.getItem(FOLDERS_H_KEY) || "", 10);
  if (Number.isFinite(saved)) applyFoldersHeight(saved);
})();
els.foldersResize.addEventListener("pointerdown", (e) => {
  e.preventDefault();
  const startY = e.clientY;
  const startH = els.foldersList.clientHeight;
  els.foldersResize.setPointerCapture(e.pointerId);
  document.body.style.userSelect = "none";
  const onMove = (ev) => {
    const h = applyFoldersHeight(startH + (startY - ev.clientY));
    localStorage.setItem(FOLDERS_H_KEY, String(h));
  };
  const onUp = (ev) => {
    els.foldersResize.releasePointerCapture(ev.pointerId);
    document.body.style.userSelect = "";
    els.foldersResize.removeEventListener("pointermove", onMove);
    els.foldersResize.removeEventListener("pointerup", onUp);
  };
  els.foldersResize.addEventListener("pointermove", onMove);
  els.foldersResize.addEventListener("pointerup", onUp);
});

// refreshFoldersPanel reloads the listing when the panel is open. Called when the
// active session changes, after a "!cd" mutates the cwd, or when the agent
// creates/changes files or folders (see scheduleFoldersRefresh). The reload
// preserves the currently expanded subtree so an agent-driven refresh doesn't
// collapse what the user opened.
async function refreshFoldersPanel() {
  if (foldersCollapsed()) return;
  const expanded = [];
  for (const li of els.foldersList.querySelectorAll("li.folder-dir.expanded")) {
    if (li.dataset.rel) expanded.push(li.dataset.rel);
  }
  await loadFolder();
  if (!expanded.length) return;
  // Shallowest first so a parent is expanded (and its children fetched) before
  // we try to re-expand a nested child.
  expanded.sort((a, b) => a.split("/").length - b.split("/").length);
  for (const rel of expanded) {
    const li = [...els.foldersList.querySelectorAll("li.folder-dir")].find(x => x.dataset.rel === rel);
    if (!li || li.classList.contains("expanded")) continue;
    const row = li.querySelector(".folder-entry-row");
    const children = li.querySelector(".folder-children");
    if (row && children) await toggleFolderExpand(li, row, children, rel);
  }
}

// scheduleFoldersRefresh coalesces bursty refresh requests (an agent turn can
// touch many files) into a single reload shortly after activity settles.
let _foldersRefreshTimer = null;
function scheduleFoldersRefresh() {
  if (foldersCollapsed()) return;
  clearTimeout(_foldersRefreshTimer);
  _foldersRefreshTimer = setTimeout(() => { refreshFoldersPanel(); }, 250);
}

// pathUnderFoldersDir reports whether an absolute path lives inside the folder
// the panel is currently showing (so we only refresh on relevant changes).
function pathUnderFoldersDir(abs) {
  if (!abs || !foldersDir) return false;
  const root = foldersDir === "/" ? "/" : foldersDir.replace(/\/+$/, "") + "/";
  return abs.startsWith(root);
}

// folderApiBase returns the folder endpoint for the current context: the active
// session's working directory when one is active, else the global "no session"
// default environment (so browsing works while a Monaco editor / draft tab is
// active). New chat sessions start at the global root (snapshotted server-side).
function folderApiBase() {
  return activeSessionId ? `/api/sessions/${activeSessionId}/folder` : `/api/folder`;
}

// loadFolder fetches and renders the current working directory listing (active
// session's, or the global default when no session is active).
async function loadFolder(path) {
  try {
    const opts = path != null
      ? { method: "POST", body: JSON.stringify({ path }) }
      : { method: "GET" };
    const res = await apiFetch(folderApiBase(), opts);
    const data = await res.json();
    if (!res.ok) { renderFolder(null, data.error); return; }
    renderFolder(data);
  } catch {
    renderFolder(null, "failed to read folder");
  }
}

// folderUploadBase returns the upload endpoint for the current context (active
// session's working directory, else the global "no session" default).
function folderUploadBase() {
  return activeSessionId
    ? `/api/sessions/${activeSessionId}/folder/upload`
    : `/api/folder/upload`;
}

// collectDropEntries walks a DataTransfer into a flat list of {file, relPath},
// recursing into dropped directories via the webkit entries API so a dropped
// folder uploads with its structure preserved. Falls back to the flat file list
// when the entries API is unavailable.
function collectDropEntries(dt) {
  const items = dt && dt.items;
  if (items && items.length && items[0].webkitGetAsEntry) {
    const out = [];
    const walks = [];
    for (const it of items) {
      const entry = it.webkitGetAsEntry && it.webkitGetAsEntry();
      if (entry) walks.push(walkDropEntry(entry, "", out));
      else if (it.kind === "file") { const f = it.getAsFile(); if (f) out.push({ file: f, relPath: f.name }); }
    }
    return Promise.all(walks).then(() => out);
  }
  return Promise.resolve(Array.from((dt && dt.files) || []).map(f => ({ file: f, relPath: f.name })));
}

// walkDropEntry recurses one FileSystemEntry, appending {file, relPath} to out.
function walkDropEntry(entry, prefix, out) {
  if (entry.isFile) {
    return new Promise((res) => entry.file(
      (f) => { out.push({ file: f, relPath: prefix + entry.name }); res(); },
      () => res()));
  }
  if (entry.isDirectory) {
    const reader = entry.createReader();
    const dirPrefix = prefix + entry.name + "/";
    return new Promise((resolve) => {
      const pending = [];
      // readEntries returns at most ~100 entries per call; keep reading until
      // it yields an empty batch.
      const readBatch = () => reader.readEntries((ents) => {
        if (!ents.length) { Promise.all(pending).then(resolve); return; }
        for (const e of ents) pending.push(walkDropEntry(e, dirPrefix, out));
        readBatch();
      }, () => resolve());
      readBatch();
    });
  }
  return Promise.resolve();
}

// uploadEntriesToFolder POSTs the collected {file, relPath} entries to the
// host filesystem at the Folders-panel cwd (or a `dest` sub-directory of it),
// then refreshes the listing. relPath is sent as each part's filename so the
// server can recreate the folder structure.
async function uploadEntriesToFolder(entries, dest) {
  if (!entries || !entries.length) return;
  const fd = new FormData();
  if (dest) fd.append("dest", dest);
  for (const { file, relPath } of entries) fd.append("files", file, relPath || file.name);
  try {
    const res = await apiFetch(folderUploadBase(), { method: "POST", body: fd });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) { renderFolder(null, data.error || "upload failed"); return; }
    loadFolder(); // re-list the current directory to show the new files
  } catch {
    renderFolder(null, "upload failed");
  }
}

// Drag-and-drop and paste upload into the Folders panel. Dropping onto a folder
// row targets that sub-directory; dropping elsewhere targets the current dir.
(function wireFolderUpload() {
  const panel = els.foldersPanel;
  let dragCounter = 0;
  const hasFiles = (e) => e.dataTransfer && Array.from(e.dataTransfer.types || []).includes("Files");
  panel.addEventListener("dragenter", (e) => {
    if (foldersCollapsed() || !hasFiles(e)) return;
    e.preventDefault(); dragCounter++; panel.classList.add("drag-over");
  });
  panel.addEventListener("dragover", (e) => {
    if (foldersCollapsed() || !hasFiles(e)) return;
    e.preventDefault(); e.dataTransfer.dropEffect = "copy";
  });
  panel.addEventListener("dragleave", () => {
    dragCounter--; if (dragCounter <= 0) { dragCounter = 0; panel.classList.remove("drag-over"); }
  });
  panel.addEventListener("drop", async (e) => {
    if (foldersCollapsed() || !hasFiles(e)) return;
    e.preventDefault(); dragCounter = 0; panel.classList.remove("drag-over");
    // If dropped onto a folder row, upload into that sub-directory.
    const dirLi = e.target.closest && e.target.closest("li.folder-dir");
    const dest = dirLi ? (dirLi.dataset.rel || "") : "";
    const entries = await collectDropEntries(e.dataTransfer);
    uploadEntriesToFolder(entries, dest);
  });

  // Ctrl/Cmd+V while the pointer is over the panel uploads files from the
  // clipboard (e.g. files copied in the OS file manager). Only fires when the
  // clipboard actually carries files — text/ref pastes are left untouched.
  document.addEventListener("paste", (e) => {
    if (!foldersHover || foldersCollapsed()) return;
    const files = Array.from((e.clipboardData && e.clipboardData.files) || []);
    if (!files.length) return;
    e.preventDefault();
    uploadEntriesToFolder(files.map(f => ({ file: f, relPath: f.name })), "");
  });
})();

// ─── Folders panel filesystem operations ──────────────────────────────────────
// An in-app clipboard holding the host path of a file/dir picked via the
// context-menu Cut/Copy. Paste then copies (op:"copy") or moves (op:"cut") it
// server-side into a target directory.
let folderClipboard = null; // { abs, name, isDir, op } | null
function setFolderClipboard(abs, name, isDir, op) { folderClipboard = { abs, name, isDir, op }; }

// folderOpBase returns the endpoint for an op ("copy"/"move"/"delete"/"new"/
// "rename"/"download") in the current context (active session's working dir,
// else the global "no session" default).
function folderOpBase(op) {
  return activeSessionId
    ? `/api/sessions/${activeSessionId}/folder/${op}`
    : `/api/folder/${op}`;
}

// runFolderOp POSTs a JSON body to a folder op endpoint and refreshes the
// listing on success; surfaces the error in the panel otherwise.
async function runFolderOp(op, body, failMsg) {
  try {
    const res = await apiFetch(folderOpBase(op), { method: "POST", body: JSON.stringify(body) });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) { renderFolder(null, data.error || failMsg); return false; }
    loadFolder();
    return true;
  } catch {
    renderFolder(null, failMsg);
    return false;
  }
}

// folderPasteInto pastes the clipboard entry into destAbs (a host directory):
// a "cut" entry is moved (and the clipboard cleared); a "copy" entry is copied.
async function folderPasteInto(destAbs) {
  if (!folderClipboard) return;
  const move = folderClipboard.op === "cut";
  const ok = await runFolderOp(move ? "move" : "copy",
    { src: folderClipboard.abs, dest: destAbs }, move ? "move failed" : "paste failed");
  if (ok && move) folderClipboard = null; // a cut entry is consumed once moved
}

// folderDownload fetches a file (or a directory as a zip) with the auth header
// and triggers a browser "Save as" via an object URL.
async function folderDownload(abs, name, isDir) {
  try {
    const res = await apiFetch(`${folderOpBase("download")}?path=${encodeURIComponent(abs)}`);
    if (!res.ok) { const d = await res.json().catch(() => ({})); renderFolder(null, d.error || "download failed"); return; }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = isDir ? `${name}.zip` : name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch {
    renderFolder(null, "download failed");
  }
}

// folderDelete removes a file/dir after confirmation.
async function folderDelete(abs, name, isDir) {
  const ok = await uiConfirm({
    title: tr(isDir ? "app.folder.deleteFolder" : "app.folder.deleteFile"),
    message: tr(isDir ? "app.folder.deleteMsgFolder" : "app.folder.deleteMsgFile", { name }),
    confirmText: tr("common.delete"),
    danger: true,
  });
  if (!ok) return;
  runFolderOp("delete", { path: abs }, tr("app.folder.deleteFailed"));
}

// folderNewEntry prompts for a name and creates a file or folder inside dirAbs.
async function folderNewEntry(dirAbs, kind) {
  const name = await uiPrompt({
    title: tr(kind === "dir" ? "app.folder.newFolder" : "app.folder.newFile"),
    label: tr("common.name"),
    placeholder: kind === "dir" ? "my-folder" : "file.txt",
    confirmText: tr("common.create"),
  });
  if (!name) return;
  runFolderOp("new", { dir: dirAbs, name, kind }, tr("app.folder.createFailed"));
}

// folderRename prompts for a new name and renames the entry in place.
async function folderRename(abs, name) {
  const next = await uiPrompt({ title: tr("common.rename"), label: tr("app.folder.newName"), value: name, confirmText: tr("common.rename") });
  if (!next || next === name) return;
  runFolderOp("rename", { src: abs, name: next }, tr("app.folder.renameFailed"));
}

// folderMoveTo / folderCopyTo prompt for a destination directory (prefilled with
// the current dir) and move/copy the entry there.
async function folderMoveTo(abs) {
  const dest = await uiPrompt({ title: tr("app.folder.moveTo"), label: tr("app.folder.destDir"), value: foldersDir, confirmText: tr("common.move") });
  if (!dest) return;
  runFolderOp("move", { src: abs, dest }, tr("app.folder.moveFailed"));
}
async function folderCopyTo(abs) {
  const dest = await uiPrompt({ title: tr("app.folder.copyTo"), label: tr("app.folder.destDir"), value: foldersDir, confirmText: tr("common.copy") });
  if (!dest) return;
  runFolderOp("copy", { src: abs, dest }, tr("app.folder.copyFailed"));
}

// ─── Generic themed modal helpers (prompt / confirm) ──────────────────────────
// Lightweight overlays reusing the .user-cmd-modal-* classes, created on demand
// and removed on close. uiPrompt resolves to the trimmed string (or null when
// cancelled); uiConfirm resolves to a boolean.
function uiModalShell(titleText) {
  const overlay = document.createElement("div");
  overlay.className = "ui-modal-overlay";
  overlay.innerHTML = `
    <div class="ui-modal" role="dialog" aria-modal="true">
      <div class="user-cmd-modal-header">
        <span class="ui-modal-title"></span>
        <button type="button" class="ui-modal-close" aria-label="Close">
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
      </div>
      <div class="user-cmd-modal-body"></div>
      <div class="user-cmd-modal-footer">
        <button type="button" class="ui-modal-cancel">Cancel</button>
        <button type="button" class="primary ui-modal-ok"></button>
      </div>
    </div>`;
  overlay.querySelector(".ui-modal-title").textContent = titleText || "";
  document.body.appendChild(overlay);
  return overlay;
}

function uiPrompt({ title, label, value, placeholder, confirmText }) {
  return new Promise((resolve) => {
    const overlay = uiModalShell(title);
    const body = overlay.querySelector(".user-cmd-modal-body");
    body.innerHTML = `<label class="user-cmd-field"><span class="user-cmd-field-label"></span><input type="text" autocomplete="off" spellcheck="false" /></label>`;
    body.querySelector(".user-cmd-field-label").textContent = label || "Name";
    const input = body.querySelector("input");
    input.value = value || "";
    input.placeholder = placeholder || "";
    const ok = overlay.querySelector(".ui-modal-ok");
    ok.textContent = confirmText || "OK";
    let done = false;
    const close = (val) => { if (done) return; done = true; overlay.remove(); document.removeEventListener("keydown", onKey, true); resolve(val); };
    const submit = () => close(input.value.trim() || null);
    overlay.querySelector(".ui-modal-ok").addEventListener("click", submit);
    overlay.querySelector(".ui-modal-cancel").addEventListener("click", () => close(null));
    overlay.querySelector(".ui-modal-close").addEventListener("click", () => close(null));
    overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) close(null); });
    const onKey = (e) => {
      if (e.key === "Escape") { e.preventDefault(); close(null); }
      else if (e.key === "Enter" && document.activeElement === input) { e.preventDefault(); submit(); }
    };
    document.addEventListener("keydown", onKey, true);
    setTimeout(() => { input.focus(); input.select(); }, 0);
  });
}

function uiConfirm({ title, message, confirmText, cancelText, danger }) {
  return new Promise((resolve) => {
    const overlay = uiModalShell(title);
    overlay.querySelector(".user-cmd-modal-body").innerHTML = `<div class="ui-modal-message"></div>`;
    overlay.querySelector(".ui-modal-message").textContent = message || "Are you sure?";
    const ok = overlay.querySelector(".ui-modal-ok");
    ok.textContent = confirmText || "OK";
    if (cancelText) overlay.querySelector(".ui-modal-cancel").textContent = cancelText;
    if (danger) ok.classList.add("danger");
    let done = false;
    const close = (val) => { if (done) return; done = true; overlay.remove(); document.removeEventListener("keydown", onKey, true); resolve(val); };
    ok.addEventListener("click", () => close(true));
    overlay.querySelector(".ui-modal-cancel").addEventListener("click", () => close(false));
    overlay.querySelector(".ui-modal-close").addEventListener("click", () => close(false));
    overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) close(false); });
    const onKey = (e) => {
      if (e.key === "Escape") { e.preventDefault(); close(false); }
      else if (e.key === "Enter") { e.preventDefault(); close(true); }
    };
    document.addEventListener("keydown", onKey, true);
    setTimeout(() => ok.focus(), 0);
  });
}

// ─── Self-update (new-release button + install dialog) ───────────────────────
// The server polls GitHub for a newer stable release; checkForUpdate reads the
// cached result and reveals the blue "Update" button in the sidebar header.
// Clicking it opens a dialog that installs the new package for the detected
// install method (deb/rpm prompt for a sudo password), with a manual-steps
// fallback when an automated install isn't possible or fails.
let updateStatus = null; // last { current, latest, available, method, asset_name, manual_steps, release_url }

async function checkForUpdate() {
  const btn = document.getElementById("update-btn");
  if (!btn) return;
  try {
    const res = await apiFetch("/api/update/status");
    if (!res.ok) return;
    updateStatus = await res.json();
  } catch (_) { return; /* best-effort */ }
  if (updateStatus && updateStatus.available) {
    const label = btn.querySelector(".update-btn-label");
    if (label) label.textContent = `Update → v${updateStatus.latest}`;
    btn.setAttribute("data-tip", `omnis v${updateStatus.latest} is available (you have v${updateStatus.current || "?"}) — click to update`);
    btn.hidden = false;
  } else {
    btn.hidden = true;
  }
}

function manualStepsHtml(steps) {
  if (!steps || !steps.length) return "";
  const items = steps.map(s => `<li><code>${escHtml(s)}</code></li>`).join("");
  return `<div class="update-manual"><div class="update-manual-title">Manual install steps</div><ol class="update-manual-list">${items}</ol></div>`;
}

function openUpdateDialog() {
  const st = updateStatus;
  if (!st || !st.available) return;
  const needsSudo = st.method === "deb" || st.method === "rpm";
  const overlay = uiModalShell("Update omnis");
  const body = overlay.querySelector(".user-cmd-modal-body");
  body.innerHTML = `
    <div class="update-dialog">
      <div class="update-versions">
        <span class="update-cur">v${escHtml(st.current || "?")}</span>
        <span class="update-arrow">→</span>
        <span class="update-new">v${escHtml(st.latest || "?")}</span>
      </div>
      <div class="update-method">${escHtml(tr("app.update.installMethod"))} <strong>${escHtml(st.method || "unknown")}</strong></div>
      ${needsSudo ? `<label class="user-cmd-field"><span class="user-cmd-field-label">${escHtml(tr("app.update.sudoPassword"))}</span><input type="password" class="update-pass" autocomplete="off" /></label>` : ""}
      <div class="update-result" hidden></div>
    </div>`;
  const ok = overlay.querySelector(".ui-modal-ok");
  ok.textContent = tr("app.update.install");
  const result = body.querySelector(".update-result");
  const passInput = body.querySelector(".update-pass");

  let done = false;
  const close = () => { if (done) return; done = true; overlay.remove(); document.removeEventListener("keydown", onKey, true); };
  overlay.querySelector(".ui-modal-cancel").addEventListener("click", close);
  overlay.querySelector(".ui-modal-close").addEventListener("click", close);
  overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) close(); });

  const showManual = (extraMsg) => {
    result.hidden = false;
    result.innerHTML = (extraMsg ? `<div class="update-error">${escHtml(extraMsg)}</div>` : "") + manualStepsHtml(st.manual_steps);
  };

  const doInstall = async () => {
    ok.disabled = true;
    ok.textContent = tr("app.update.installing");
    result.hidden = false;
    result.innerHTML = `<div class="update-progress">${escHtml(tr("app.update.downloading"))}</div>`;
    try {
      const res = await apiFetch("/api/update/install", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: passInput ? passInput.value : "" }),
      });
      const j = await res.json().catch(() => ({}));
      if (res.ok && j.ok) {
        ok.textContent = tr("app.update.restartNow");
        ok.disabled = false;
        result.innerHTML = `<div class="update-success">${escHtml(tr("app.update.installedRestart", { version: st.latest }))}</div>`;
        // Re-point the primary button to restart.
        ok.replaceWith(ok.cloneNode(true));
        const restartBtn = overlay.querySelector(".ui-modal-ok");
        restartBtn.textContent = tr("app.update.restartNow");
        restartBtn.addEventListener("click", () => { close(); restartServerAndReload(); });
      } else {
        ok.disabled = false;
        ok.textContent = tr("app.update.install");
        showManual(j.error || tr("app.update.installFailed"));
      }
    } catch (e) {
      ok.disabled = false;
      ok.textContent = tr("app.update.install");
      showManual(String(e && e.message || e));
    }
  };

  // For a raw/unknown method there is no automated install — show manual steps
  // immediately and relabel the primary button.
  if (st.method === "raw" || st.method === "unknown") {
    ok.textContent = tr("app.update.showManual");
    ok.addEventListener("click", () => showManual(""));
  } else {
    ok.addEventListener("click", doInstall);
  }

  const onKey = (e) => { if (e.key === "Escape") { e.preventDefault(); close(); } };
  document.addEventListener("keydown", onKey, true);
  setTimeout(() => { (passInput || ok).focus(); }, 0);
}

// restartServerAndReload triggers the existing /api/server/restart endpoint and
// reloads the page once the server is reachable again (mirrors the Settings
// restart flow so the new binary is picked up by the re-exec).
async function restartServerAndReload() {
  try {
    const r = await apiFetch("/api/server/restart", { method: "POST" });
    if (!r.ok) throw new Error(`HTTP ${r.status}`);
  } catch (_) { /* the process may already be tearing down */ }
  const start = Date.now();
  const tick = async () => {
    try {
      const h = await fetch((window.BASE_PATH || "") + "/api/health");
      if (h.ok) { window.location.reload(); return; }
    } catch (_) { /* not back yet */ }
    if (Date.now() - start > 30000) return; // give up quietly
    setTimeout(tick, 750);
  };
  setTimeout(tick, 1000);
}

// ─── Provider connection health ──────────────────────────────────────────────
// On boot (and after a config reload) probe every configured model provider via
// GET /api/providers/health. When any provider can't connect, reveal the orange
// warning banner inside each chat pane — above the composer in an active chat,
// and above the "Start a new chat" button in an empty/draft pane. Clicking it
// opens a popup to fix the base URL / API key and reload the agent. Best-effort:
// any failure to reach the health endpoint itself leaves the banners hidden (no
// false alarm).
let providerHealthState = null; // last { ok, providers:[{ref,kind,base_url,has_api_key,ok,error}] }

async function checkProviderHealth() {
  try {
    const res = await apiFetch("/api/providers/health");
    if (!res.ok) return;
    providerHealthState = await res.json();
  } catch (_) { return; /* best-effort */ }
  renderProviderWarning();
}

// providerWarningBad returns the providers from the last probe that failed.
function providerWarningBad() {
  return (providerHealthState?.providers || []).filter(p => !p.ok);
}

// applyProviderWarning toggles a single pane's warning banners (composer + picker
// variants together — the pane-picker overlay decides which is on screen) from
// the cached health state, so a freshly created/split pane reflects it too.
function applyProviderWarning(panel) {
  if (!panel || !panel.root) return;
  const bad = providerWarningBad();
  const show = bad.length > 0;
  const tip = show
    ? `Can't reach model provider${bad.length > 1 ? "s" : ""}: ${bad.map(p => p.ref).join(", ")} — click to fix`
    : "";
  for (const b of panel.root.querySelectorAll(".provider-warn-banner")) {
    b.hidden = !show;
    if (show) b.setAttribute("data-tip", tip);
  }
}

// renderProviderWarning refreshes the warning banners across every pane.
function renderProviderWarning() {
  for (const p of panels) applyProviderWarning(p);
}

// openProviderHealthModal shows a popup listing the unreachable providers (or
// all of them when none are currently failing) with editable base URL + API key
// fields. Saving writes the raw models.json back via the config editor route
// (preserving its on-disk shape and untouched fields), reloads the agent, and
// re-probes so the user sees whether the fix worked.
async function openProviderHealthModal() {
  await checkProviderHealth(); // freshest verdict before opening
  const health = providerHealthState || { providers: [] };
  const failing = (health.providers || []).filter(p => !p.ok);
  const list = failing.length ? failing : (health.providers || []);

  // Load the raw models.json so edits preserve env-var references and any
  // fields the popup doesn't touch; mtime guards against a concurrent edit.
  let raw = {}, mtime;
  try {
    const res = await apiFetch("/api/config/parsed/models");
    if (res.ok) {
      const body = await res.json();
      raw = body.data || {};
      mtime = body.mtime;
    }
  } catch (_) { /* fall through with empty raw */ }
  raw.providers = raw.providers || {};

  const overlay = uiModalShell("Model connection");
  overlay.classList.add("provider-health-modal");
  const body = overlay.querySelector(".user-cmd-modal-body");

  const intro = document.createElement("p");
  intro.className = "provider-health-intro";
  intro.textContent = failing.length
    ? "These model providers couldn't be reached. Update the base URL and/or API key, then save to reload the agent."
    : "All providers responded. You can still update a provider's base URL or API key here.";
  body.appendChild(intro);

  // Resolve a health ref (lower-cased) back to its raw models.json provider key.
  const rawKeyFor = (ref) => {
    const lc = String(ref).toLowerCase();
    return Object.keys(raw.providers).find(k => k.toLowerCase() === lc) || ref;
  };

  const fields = [];
  for (const p of list) {
    const rk = rawKeyFor(p.ref);
    const prov = raw.providers[rk] || {};
    const card = document.createElement("div");
    card.className = "provider-health-card";
    card.innerHTML = `
      <div class="provider-health-name">${escHtml(p.ref)} <span class="provider-health-kind">${escHtml(p.kind || "")}</span></div>
      ${p.error ? `<div class="provider-health-error">${escHtml(p.error)}</div>` : ""}
      <label class="user-cmd-field"><span class="user-cmd-field-label">Base URL</span>
        <input type="text" class="ph-base" autocomplete="off" spellcheck="false" placeholder="https://api.example.com/v1" /></label>
      <label class="user-cmd-field"><span class="user-cmd-field-label">API key</span>
        <input type="password" class="ph-key" autocomplete="off" placeholder="${prov.api_key ? "leave blank to keep current" : "enter API key"}" /></label>
      <div class="provider-health-actions">
        <button type="button" class="ph-test">Test connection</button>
        <span class="ph-test-status"></span>
      </div>`;
    card.querySelector(".ph-base").value = prov.base_url || "";
    const errEl = card.querySelector(".provider-health-error");
    const testBtn = card.querySelector(".ph-test");
    const testStatus = card.querySelector(".ph-test-status");
    const f = { ref: p.ref, kind: p.kind, rk, baseEl: card.querySelector(".ph-base"), keyEl: card.querySelector(".ph-key") };
    testBtn.addEventListener("click", async () => {
      testBtn.disabled = true;
      testStatus.className = "ph-test-status";
      testStatus.textContent = tr("app.provider.testing");
      try {
        const res = await apiFetch("/api/providers/test", {
          method: "POST",
          body: JSON.stringify({ ref: f.ref, kind: f.kind, base_url: f.baseEl.value.trim(), api_key: f.keyEl.value.trim() }),
        });
        const data = await res.json().catch(() => ({}));
        if (res.ok && data.ok) {
          testStatus.className = "ph-test-status is-ok";
          testStatus.textContent = typeof data.model_count === "number"
            ? tr("app.provider.testedCount", { count: data.model_count })
            : tr("app.provider.tested");
          if (errEl) errEl.hidden = true; // stale boot-time error no longer applies
        } else {
          testStatus.className = "ph-test-status is-error";
          testStatus.textContent = data.error || `HTTP ${res.status}`;
        }
      } catch (e) {
        testStatus.className = "ph-test-status is-error";
        testStatus.textContent = e.message || tr("app.provider.testFailed");
      } finally {
        testBtn.disabled = false;
      }
    });
    body.appendChild(card);
    fields.push(f);
  }

  const status = document.createElement("div");
  status.className = "provider-health-status";
  status.hidden = true;
  body.appendChild(status);

  const ok = overlay.querySelector(".ui-modal-ok");
  ok.textContent = tr("app.provider.saveReload");

  let done = false;
  const close = () => {
    if (done) return;
    done = true;
    overlay.remove();
    document.removeEventListener("keydown", onKey, true);
  };
  const onKey = (e) => { if (e.key === "Escape") { e.preventDefault(); close(); } };
  document.addEventListener("keydown", onKey, true);
  overlay.querySelector(".ui-modal-cancel").addEventListener("click", close);
  overlay.querySelector(".ui-modal-close").addEventListener("click", close);
  overlay.addEventListener("mousedown", (e) => { if (e.target === overlay) close(); });

  const setStatus = (text, cls) => {
    status.hidden = false;
    status.className = "provider-health-status" + (cls ? " " + cls : "");
    status.textContent = text;
  };

  ok.addEventListener("click", async () => {
    // Apply the edits onto the raw providers block.
    for (const f of fields) {
      const prov = raw.providers[f.rk] || (raw.providers[f.rk] = {});
      prov.base_url = f.baseEl.value.trim();
      const key = f.keyEl.value.trim();
      if (key) prov.api_key = key; // blank keeps the existing value / env ref
    }
    ok.disabled = true;
    setStatus(tr("app.provider.saving"));
    try {
      const putRes = await apiFetch("/api/config/parsed/models", {
        method: "PUT",
        body: JSON.stringify({ data: raw, mtime }),
      });
      if (!putRes.ok) {
        const j = await putRes.json().catch(() => ({}));
        throw new Error(j.error || `HTTP ${putRes.status}`);
      }
      setStatus(tr("app.provider.reloading"));
      const relRes = await apiFetch("/api/config/reload", { method: "POST" });
      if (!relRes.ok) {
        const j = await relRes.json().catch(() => ({}));
        throw new Error(j.error || `HTTP ${relRes.status}`);
      }
      const relBody = await relRes.json().catch(() => ({}));
      window.dispatchEvent(new CustomEvent("omnis:config-reloaded", { detail: { generation: relBody.generation } }));
      await checkProviderHealth();
      const stillBad = (providerHealthState?.providers || []).filter(p => !p.ok);
      if (stillBad.length) {
        ok.disabled = false;
        setStatus(tr("app.provider.stillBad", { providers: stillBad.map(p => p.ref).join(", ") }), "is-error");
      } else {
        setStatus(tr("app.provider.connected"), "is-ok");
        setTimeout(close, 1000);
      }
    } catch (e) {
      ok.disabled = false;
      setStatus(tr("app.provider.failed", { error: e.message }), "is-error");
    }
  });
}

// Entry icons for the Folders tree (module scope so the recursive entry builder
// can reuse them).
const FOLDER_SVG = `<svg class="folder-entry-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>`;
const FOLDER_FILE_SVG = `<svg class="folder-entry-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/></svg>`;
const FOLDER_UP_SVG = `<svg class="folder-entry-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M9 14 4 9l5-5"/><path d="M4 9h10.5a5.5 5.5 0 0 1 5.5 5.5 5.5 5.5 0 0 1-5.5 5.5H11"/></svg>`;

// File-type glyphs for the Folders tree (VS Code / Seti style): a recognised
// extension renders as a coloured document glyph with a short label, both in the
// language brand colour on a transparent background; unknown types fall back to
// the neutral (currentColor) document icon. Keyed by lower-cased extension;
// FILE_NAMES keys on whole names.
const FILE_TYPES = {
  go: { label: "GO", color: "#00add8" }, mod: { label: "GO", color: "#00add8" }, sum: { label: "GO", color: "#00add8" },
  js: { label: "JS", color: "#e8d44d" }, mjs: { label: "JS", color: "#e8d44d" }, cjs: { label: "JS", color: "#e8d44d" },
  jsx: { label: "JSX", color: "#61dafb" }, ts: { label: "TS", color: "#3178c6" }, tsx: { label: "TSX", color: "#61dafb" },
  html: { label: "<>", color: "#e34c26" }, htm: { label: "<>", color: "#e34c26" },
  css: { label: "CSS", color: "#519aba" }, scss: { label: "SCSS", color: "#c6538c" }, sass: { label: "SASS", color: "#c6538c" },
  json: { label: "{}", color: "#cbcb41" }, jsonc: { label: "{}", color: "#cbcb41" },
  md: { label: "MD", color: "#519aba" }, markdown: { label: "MD", color: "#519aba" },
  py: { label: "PY", color: "#3572a5" }, rs: { label: "RS", color: "#dea584" }, rb: { label: "RB", color: "#cc342d" },
  java: { label: "JV", color: "#cc8e34" }, c: { label: "C", color: "#7f95a3" }, h: { label: "H", color: "#7f95a3" },
  cpp: { label: "C++", color: "#f34b7d" }, cc: { label: "C++", color: "#f34b7d" }, hpp: { label: "H++", color: "#f34b7d" },
  cs: { label: "C#", color: "#37a533" }, php: { label: "PHP", color: "#6a74b3" }, swift: { label: "SW", color: "#f05138" },
  kt: { label: "KT", color: "#a97bff" }, sh: { label: "SH", color: "#89e051" }, bash: { label: "SH", color: "#89e051" }, zsh: { label: "SH", color: "#89e051" },
  yml: { label: "YML", color: "#cb6b6b" }, yaml: { label: "YML", color: "#cb6b6b" }, toml: { label: "TOM", color: "#bb7755" },
  ini: { label: "INI", color: "#8a9aa3" }, cfg: { label: "CFG", color: "#8a9aa3" }, conf: { label: "CFG", color: "#8a9aa3" }, env: { label: "ENV", color: "#67b06a" },
  sql: { label: "SQL", color: "#e0922f" }, xml: { label: "XML", color: "#e3a04c" }, svg: { label: "SVG", color: "#b073d6" },
  png: { label: "IMG", color: "#b073d6" }, jpg: { label: "IMG", color: "#b073d6" }, jpeg: { label: "IMG", color: "#b073d6" }, gif: { label: "IMG", color: "#b073d6" }, webp: { label: "IMG", color: "#b073d6" }, ico: { label: "IMG", color: "#b073d6" },
  pdf: { label: "PDF", color: "#d4564b" }, txt: { label: "TXT", color: "#8a9aa3" }, log: { label: "LOG", color: "#8a9aa3" }, lock: { label: "LCK", color: "#8a9aa3" },
  zip: { label: "ZIP", color: "#8a9aa3" }, gz: { label: "GZ", color: "#8a9aa3" }, tar: { label: "TAR", color: "#8a9aa3" },
};
const FILE_NAMES = {
  "go.mod": { label: "GO", color: "#00add8" }, "go.sum": { label: "GO", color: "#00add8" },
  "makefile": { label: "MK", color: "#8a9aa3" }, "license": { label: "LIC", color: "#8a9aa3" },
  ".gitignore": { label: "GIT", color: "#f14e32" }, ".env": { label: "ENV", color: "#67b06a" },
};

// fileTypeInfo resolves a file name to its glyph descriptor, or null (→ generic
// document icon) when the type isn't recognised.
function fileTypeInfo(name) {
  const lower = name.toLowerCase();
  if (FILE_NAMES[lower]) return FILE_NAMES[lower];
  if (lower === "dockerfile" || lower.startsWith("dockerfile.")) return { label: "DCK", color: "#3a8fc4" };
  const dot = lower.lastIndexOf(".");
  if (dot <= 0) return null; // no extension, or a dotfile we don't special-case
  return FILE_TYPES[lower.slice(dot + 1)] || null;
}

// fileIconSvg returns the coloured document glyph for a recognised file type
// (transparent background, language-coloured outline + label), else the generic
// neutral document icon.
function fileIconSvg(name) {
  const info = fileTypeInfo(name);
  if (!info) return FOLDER_FILE_SVG;
  const c = info.color;
  const fs = info.label.length >= 3 ? 7 : 9;
  return `<svg class="folder-entry-icon file-glyph" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="${c}" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">`
    + `<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>`
    + `<polyline points="14 2 14 8 20 8"/>`
    + `<text x="11.5" y="18.5" text-anchor="middle" stroke="none" fill="${c}" font-family="ui-monospace,Menlo,Consolas,monospace" font-weight="700" font-size="${fs}">${escHtml(info.label)}</text>`
    + `</svg>`;
}

// wireClickDblClick distinguishes a single click from a double click on el.
// A single click is delayed briefly; a double click cancels the pending single
// and fires the double handler instead.
function wireClickDblClick(el, single, double) {
  let timer = null;
  el.addEventListener("click", () => {
    if (timer) return;
    timer = setTimeout(() => { timer = null; single(); }, 220);
  });
  el.addEventListener("dblclick", () => {
    if (timer) { clearTimeout(timer); timer = null; }
    double();
  });
}

// lastCopiedRef holds the "@path" reference most recently copied from the
// Folders panel (Ctrl/Cmd+C). The composer's paste handler uses it to recognise
// our own ref on the clipboard and insert it space-padded; everything else
// pastes natively.
let lastCopiedRef = "";

// insertRefIntoComposer inserts a ready "@path" reference (ref already includes
// the leading "@") into a pane's composer at the caret, padding with spaces so
// it stays a valid file ref.
function insertRefIntoComposer(panel, ref) {
  if (!panel || !panel.els || !panel.els.prompt) return;
  const el = panel.els.prompt;
  const start = el.selectionStart ?? el.value.length;
  const end = el.selectionEnd ?? el.value.length;
  const before = el.value.slice(0, start);
  const after = el.value.slice(end);
  const lead = before && !/\s$/.test(before) ? " " : "";
  const trail = after && !/^\s/.test(after) ? " " : (after ? "" : " ");
  const insert = lead + ref + trail;
  el.value = before + insert + after;
  const caret = before.length + insert.length;
  el.setSelectionRange(caret, caret);
  el.focus();
  el.dispatchEvent(new Event("input")); // refresh ref highlight + auto-grow
}

// insertFileRef inserts an "@rel" reference into the focused pane's composer.
function insertFileRef(rel) {
  insertRefIntoComposer(focusedPanel(), "@" + rel);
}

// copyFileRef copies an "@rel" reference to the system clipboard (with a legacy
// execCommand fallback for non-secure contexts) and remembers it in
// lastCopiedRef. The copied row keeps a persistent "copied" marker (cleared
// from any previously-copied row) so the user can see which item is armed for
// pasting; markCopiedRow re-applies it across tree re-renders.
// writeClipboard copies text to the system clipboard, with a legacy
// execCommand fallback for non-secure (plain-http) contexts.
async function writeClipboard(text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return;
    }
  } catch { /* fall through to execCommand */ }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.cssText = "position:fixed;top:0;left:0;opacity:0;";
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    document.body.removeChild(ta);
  } catch { /* clipboard unavailable */ }
}

async function copyFileRef(rel, rowEl) {
  const ref = "@" + rel;
  lastCopiedRef = ref;
  await writeClipboard(ref);
  // Clear the marker from any previously-copied row, then mark this one. A brief
  // "flash" class layers a one-shot pulse on top of the persistent highlight.
  for (const r of els.foldersList.querySelectorAll(".folder-entry-row.copied")) {
    r.classList.remove("copied");
  }
  if (rowEl) {
    rowEl.classList.add("copied", "flash");
    setTimeout(() => rowEl.classList.remove("flash"), 600);
  }
}

// markCopiedRow re-applies the persistent "copied" highlight to an entry row
// when it matches the currently-armed lastCopiedRef (entries are rebuilt on
// every render / lazy expand, which would otherwise drop the class).
function markCopiedRow(row, rel) {
  if (lastCopiedRef && "@" + rel === lastCopiedRef) row.classList.add("copied");
}

// clearCopiedRef disarms the current Folders-panel selection: forgets
// lastCopiedRef and strips the persistent highlight from every row.
function clearCopiedRef() {
  lastCopiedRef = "";
  for (const r of els.foldersList.querySelectorAll(".folder-entry-row.copied")) {
    r.classList.remove("copied");
  }
}

// Escape clears the armed selection while the pointer is over the Folders panel.
let foldersHover = false;
els.foldersPanel.addEventListener("mouseenter", () => { foldersHover = true; });
els.foldersPanel.addEventListener("mouseleave", () => { foldersHover = false; });
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && foldersHover && lastCopiedRef) {
    e.preventDefault();
    clearCopiedRef();
  }
});

// Right-clicking the path header (or empty list area) offers "Paste here" into
// the current directory — the target with no folder row to right-click (e.g. the
// root). Only shown when the in-app clipboard holds an entry.
function openFolderDirCtxMenu(ev) {
  if (!foldersDir) return;
  const items = [];
  items.push([tr("menu.openTerminalHere"), () => openTerminalTab(null, { cwd: foldersDir })]);
  items.push(SEP);
  items.push([tr("menu.newFile"), () => folderNewEntry(foldersDir, "file")]);
  items.push([tr("menu.newFolder"), () => folderNewEntry(foldersDir, "dir")]);
  if (folderClipboard) {
    items.push([folderClipboard.name ? tr("menu.pasteHereNamed", { name: folderClipboard.name }) : tr("menu.pasteHere"), () => folderPasteInto(foldersDir)]);
  }
  items.push(SEP);
  items.push([tr("menu.downloadFolder"), () => folderDownload(foldersDir, foldersDir.split("/").filter(Boolean).pop() || "root", true)]);
  items.push([tr("menu.copyPath"), () => writeClipboard(foldersDir)]);
  showFolderCtxMenu(ev, items);
}
els.foldersPath.addEventListener("contextmenu", openFolderDirCtxMenu);
els.foldersList.addEventListener("contextmenu", (ev) => {
  // Only the empty area of the list — entry rows + the ".." row have their own
  // handlers.
  if (ev.target.closest(".folder-entry-row") || ev.target.closest("li.folder-up")) return;
  openFolderDirCtxMenu(ev);
});

// parentDirAbs returns the absolute path of the current dir's parent (the ".."
// target); clamps at the filesystem root.
function parentDirAbs() {
  const d = (foldersDir || "").replace(/\/+$/, "");
  const idx = d.lastIndexOf("/");
  return idx <= 0 ? "/" : d.slice(0, idx);
}

// Context menu for the ".." (parent) row. ".." is a navigable directory, so the
// filesystem container actions (download / new / paste / copy-path) apply to the
// parent directory, while the entry-targeting actions (cut / copy / rename /
// move / copy-to / delete) make no sense for ".." and are shown greyed out.
// EXCEPTION: "Open Chat here" / "Open Terminal here" root at the *currently
// displayed* folder (foldersDir), not the parent — users read "here" as "the
// folder I'm looking at", and the parent surprised them by landing on the app
// root when they'd navigated just one level down.
function openFolderUpCtxMenu(ev) {
  const abs = parentDirAbs();
  const cur = foldersDir; // the folder currently shown in the panel
  const name = abs.split("/").filter(Boolean).pop() || "root";
  const D = { disabled: true };
  const items = [
    [tr("menu.openChatHere"), () => newChat(null, undefined, cur)],
    [tr("menu.openTerminalHere"), () => openTerminalTab(null, { cwd: cur })],
    [tr("menu.download"), () => folderDownload(abs, name, true)],
    SEP,
    [tr("menu.newFile"), () => folderNewEntry(abs, "file")],
    [tr("menu.newFolder"), () => folderNewEntry(abs, "dir")],
  ];
  if (folderClipboard) {
    items.push([folderClipboard.name ? tr("menu.pasteNamed", { name: folderClipboard.name }) : tr("menu.paste"), () => folderPasteInto(abs)]);
  }
  items.push(
    SEP,
    [tr("menu.cut"), null, D],
    [tr("menu.copy"), null, D],
    [tr("menu.copyPath"), () => writeClipboard(abs)],
    SEP,
    [tr("menu.renameDots"), null, D],
    [tr("menu.moveTo"), null, D],
    [tr("menu.copyTo"), null, D],
    [tr("menu.delete"), null, D],
  );
  showFolderCtxMenu(ev, items);
}

// buildFolderEntry builds one <li> for the Folders tree. Directories carry a
// collapsible nested <ul> (lazy-loaded on first expand) and respond to a single
// click with expand/collapse, a double click with "navigate into" (mutates the
// session cwd). Files do nothing on single click and insert an "@rel" reference
// on double click. `rel` is the path of this entry relative to the session cwd.
// ─── Folders panel context menu ───────────────────────────────────────────────
// Right-clicking a folder/file row opens a small themed menu (body-appended so it
// escapes panel overflow). Items adapt to the entry kind + state:
//   folder: Open Chat here · Copy path · [Add to chat editor]
//   file:   Open · Copy path · [Add to chat editor] · [Save (when dirty)]
// (bracketed items appear conditionally).
let folderCtxMenu = null;
function ensureFolderCtxMenu() {
  if (folderCtxMenu) return folderCtxMenu;
  const m = document.createElement("div");
  m.id = "folder-ctx-menu";
  m.hidden = true;
  document.body.appendChild(m);
  folderCtxMenu = m;
  // Close on any click/right-click/scroll anywhere — in the CAPTURE phase so it
  // fires even when an app element stops propagation on its own click handler.
  // A menu item's own action still runs: the click event is already in flight to
  // the button, so hiding the menu here doesn't cancel it.
  document.addEventListener("click", () => hideFolderCtxMenu(), true);
  document.addEventListener("contextmenu", (e) => { if (!m.contains(e.target)) hideFolderCtxMenu(); }, true);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") hideFolderCtxMenu(); });
  document.addEventListener("scroll", () => hideFolderCtxMenu(), true);
  window.addEventListener("blur", () => hideFolderCtxMenu());
  window.addEventListener("resize", () => hideFolderCtxMenu());
  return m;
}
function hideFolderCtxMenu() { if (folderCtxMenu) folderCtxMenu.hidden = true; }

// SEP is a separator sentinel for showFolderCtxMenu — renders a horizontal rule
// that groups items like a traditional context menu.
const SEP = "---";

function openFolderCtxMenu(ev, rel, isDir, row) {
  if (row) row.focus();
  const abs = absForRel(rel);
  const name = rel.split("/").pop();
  const pasteLabel = folderClipboard
    ? (folderClipboard.name ? tr("menu.pasteNamed", { name: folderClipboard.name }) : tr("menu.paste"))
    : null;
  const items = [];
  if (isDir) {
    // Open / download
    items.push([tr("menu.openChatHere"), () => newChat(null, undefined, abs)]);
    items.push([tr("menu.openTerminalHere"), () => openTerminalTab(null, { cwd: abs })]);
    items.push([tr("menu.download"), () => folderDownload(abs, name, true)]);
    items.push(SEP);
    // Create inside this folder
    items.push([tr("menu.newFile"), () => folderNewEntry(abs, "file")]);
    items.push([tr("menu.newFolder"), () => folderNewEntry(abs, "dir")]);
    if (pasteLabel) items.push([pasteLabel, () => folderPasteInto(abs)]);
    items.push(SEP);
    // Clipboard
    items.push([tr("menu.cut"), () => setFolderClipboard(abs, name, true, "cut")]);
    items.push([tr("menu.copy"), () => setFolderClipboard(abs, name, true, "copy")]);
    items.push([tr("menu.copyPath"), () => writeClipboard(abs)]);
    items.push(SEP);
    // Mutating ops
    items.push([tr("menu.renameDots"), () => folderRename(abs, name)]);
    items.push([tr("menu.moveTo"), () => folderMoveTo(abs)]);
    items.push([tr("menu.copyTo"), () => folderCopyTo(abs)]);
    items.push([tr("menu.delete"), () => folderDelete(abs, name, true)]);
    if (activeSessionId) { items.push(SEP); items.push([tr("menu.addToChatEditor"), () => insertFileRef(rel)]); }
  } else {
    items.push([tr("menu.open"), () => openFileInEditor(rel)]);
    items.push([tr("menu.download"), () => folderDownload(abs, name, false)]);
    items.push(SEP);
    items.push([tr("menu.cut"), () => setFolderClipboard(abs, name, false, "cut")]);
    items.push([tr("menu.copy"), () => setFolderClipboard(abs, name, false, "copy")]);
    items.push([tr("menu.copyPath"), () => writeClipboard(abs)]);
    items.push(SEP);
    items.push([tr("menu.renameDots"), () => folderRename(abs, name)]);
    items.push([tr("menu.moveTo"), () => folderMoveTo(abs)]);
    items.push([tr("menu.copyTo"), () => folderCopyTo(abs)]);
    items.push([tr("menu.delete"), () => folderDelete(abs, name, false)]);
    const extras = [];
    if (activeSessionId) extras.push([tr("menu.addToChatEditor"), () => insertFileRef(rel)]);
    if (editorDirty.get(abs)) {
      const panel = panelsWithTab(editorKey(abs))[0];
      if (panel) extras.push([tr("menu.save"), () => saveEditor(panel, abs)]);
    }
    if (extras.length) { items.push(SEP); items.push(...extras); }
  }
  showFolderCtxMenu(ev, items);
}

// showFolderCtxMenu renders [[label, action], …] (with SEP entries as group
// separators) into the shared context menu and positions it at the event,
// clamped to the viewport. Leading/trailing/duplicate separators are dropped.
function showFolderCtxMenu(ev, items) {
  ev.preventDefault();
  ev.stopPropagation();
  const m = ensureFolderCtxMenu();
  m.innerHTML = "";
  let lastWasSep = true; // suppress a leading separator
  for (const item of items) {
    if (item === SEP) {
      if (lastWasSep) continue;
      const hr = document.createElement("div");
      hr.className = "folder-ctx-sep";
      m.appendChild(hr);
      lastWasSep = true;
      continue;
    }
    // [label, action] or [label, action, {disabled, hidden, icon}] — `disabled`
    // greys the item (no action), `hidden` omits it, `icon` (trusted SVG markup)
    // prefixes the label. The label is always set via textContent (never the
    // icon's innerHTML path) so a user-derived label can't inject markup.
    const [label, action, opts] = item;
    if (opts && opts.hidden) continue;
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "folder-ctx-item";
    if (opts && opts.icon) {
      btn.classList.add("has-icon");
      btn.innerHTML = `<span class="ctx-item-icon" aria-hidden="true">${opts.icon}</span><span class="ctx-item-label"></span>`;
      btn.querySelector(".ctx-item-label").textContent = label;
    } else {
      btn.textContent = label;
    }
    if (opts && opts.disabled) {
      btn.disabled = true;
    } else {
      btn.addEventListener("click", (e) => { e.stopPropagation(); hideFolderCtxMenu(); action(); });
    }
    m.appendChild(btn);
    lastWasSep = false;
  }
  // Drop a trailing separator if present.
  while (m.lastChild && m.lastChild.classList && m.lastChild.classList.contains("folder-ctx-sep")) {
    m.removeChild(m.lastChild);
  }
  m.hidden = false;
  const mw = m.offsetWidth, mh = m.offsetHeight;
  const x = Math.min(ev.clientX, window.innerWidth - mw - 4);
  const y = Math.min(ev.clientY, window.innerHeight - mh - 4);
  m.style.left = Math.max(4, x) + "px";
  m.style.top = Math.max(4, y) + "px";
}

function buildFolderEntry(e, rel) {
  const li = document.createElement("li");
  li.className = e.dir ? "folder-dir" : "folder-file";
  if (e.dir) li.dataset.rel = rel; // drop-onto-folder upload target
  const row = document.createElement("div");
  row.className = "folder-entry-row";
  row.tabIndex = -1; // focusable on click (so Ctrl/Cmd+C targets it), not in tab order
  const chevron = e.dir
    ? `<span class="folder-chevron">▸</span>`
    : `<span class="folder-chevron-spacer"></span>`;
  row.innerHTML = `${chevron}${e.dir ? FOLDER_SVG : fileIconSvg(e.name)}<span class="folder-entry-name"></span>`;
  row.querySelector(".folder-entry-name").textContent = e.name;
  markCopiedRow(row, rel); // restore the persistent "copied" highlight if armed
  // Ctrl/Cmd+C copies the entry's "@rel" reference (file or directory) to the
  // clipboard, so a Ctrl/Cmd+V in the chat editor inserts the reference.
  row.addEventListener("keydown", (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && (ev.key === "c" || ev.key === "C")) {
      ev.preventDefault();
      copyFileRef(rel, row);
    }
  });
  // Right-click → contextual menu (Open / Open Chat here / Copy path / …).
  row.addEventListener("contextmenu", (ev) => openFolderCtxMenu(ev, rel, e.dir, row));
  li.appendChild(row);

  if (e.dir) {
    const children = document.createElement("ul");
    children.className = "folder-children";
    children.hidden = true;
    li.appendChild(children);
    wireClickDblClick(row,
      () => toggleFolderExpand(li, row, children, rel),
      () => loadFolder(rel));
  } else {
    wireClickDblClick(row, () => {}, () => openFileInEditor(rel));
  }
  return li;
}

// toggleFolderExpand expands or collapses a directory <li> in the Folders tree,
// lazily fetching its children (via the non-mutating ?sub= listing) on first
// open.
async function toggleFolderExpand(li, row, children, rel) {
  const expanded = li.classList.toggle("expanded");
  const chev = row.querySelector(".folder-chevron");
  if (chev) chev.textContent = expanded ? "▾" : "▸";
  if (!expanded) { children.hidden = true; return; }
  children.hidden = false;
  if (li.dataset.loaded === "1") return;
  children.innerHTML = `<li class="folder-loading">…</li>`;
  try {
    const res = await apiFetch(`${folderApiBase()}?sub=${encodeURIComponent(rel)}`);
    const data = await res.json();
    children.innerHTML = "";
    if (!res.ok) { children.innerHTML = `<li class="folder-loading">${escHtml(data.error || "error")}</li>`; return; }
    for (const ce of (data.entries || [])) {
      children.appendChild(buildFolderEntry(ce, rel + "/" + ce.name));
    }
    if (!children.children.length) children.innerHTML = `<li class="folder-loading">empty</li>`;
    li.dataset.loaded = "1";
  } catch {
    children.innerHTML = `<li class="folder-loading">error</li>`;
  }
}

// renderFolder paints the path header and the entry tree ("..", dirs, files).
// Single-click a directory to expand/collapse it in place, double-click to
// navigate into it (loadFolder mutates the cwd). Single-click a file does
// nothing; double-click opens it in the Monaco editor.
function renderFolder(data, err) {
  els.foldersList.innerHTML = "";
  if (!data) {
    els.foldersPath.textContent = "";
    els.foldersPath.removeAttribute("data-tip");
    const li = document.createElement("li");
    li.id = "folders-empty";
    li.textContent = err || "empty";
    els.foldersList.appendChild(li);
    return;
  }
  foldersDir = data.dir || "";
  // RTL on the element keeps the deepest path visible; wrap in LRM so the
  // leading slash isn't reordered.
  els.foldersPath.textContent = "‎" + foldersDir;
  els.foldersPath.setAttribute("data-tip", foldersDir);

  // Parent ".." unless we're at the filesystem root.
  if (foldersDir && foldersDir !== "/") {
    const up = document.createElement("li");
    up.className = "folder-up";
    up.innerHTML = `${FOLDER_UP_SVG}<span class="folder-entry-name">..</span>`;
    up.addEventListener("click", () => loadFolder(".."));
    up.addEventListener("contextmenu", openFolderUpCtxMenu);
    els.foldersList.appendChild(up);
  }

  for (const e of (data.entries || [])) {
    els.foldersList.appendChild(buildFolderEntry(e, e.name));
  }
  if (!els.foldersList.children.length) {
    const li = document.createElement("li");
    li.id = "folders-empty";
    li.textContent = tr("app.folders.empty");
    els.foldersList.appendChild(li);
  }
}

// ─── Context ring popup ───────────────────────────────────────────────────────
// The ring + popup live per-pane (wired in attachPaneHandlers). A document-level
// click closes any open popup when the click lands outside its ring wrap.
document.addEventListener("click", (e) => {
  for (const p of panels) {
    if (p.els.ctxPopup && !p.els.ctxPopup.hasAttribute("hidden") &&
        !p.els.ctxRingWrap.contains(e.target)) {
      closeCtxPopup(p);
    }
  }
});

// Re-normalize pane widths when the window resizes.
window.addEventListener("resize", () => { layoutWidths(); refitVisibleTerminals(); });

// ─── Init ─────────────────────────────────────────────────────────────────────

// restoreLayout rebuilds the pane row from a saved layout record, dropping any
// pane whose session no longer exists. Returns true if at least one pane was
// bound to a live session.
async function restoreLayout(rec, liveIds) {
  panels = [];
  const plans = [];
  for (const pane of rec.panes) {
    // v2 records carry { tabs, activeId }; v1 carried a single { sessionId }.
    const rawTabs = Array.isArray(pane.tabs)
      ? pane.tabs
      : (pane.sessionId ? [pane.sessionId] : []);
    // Keep editor tabs (file#<abs>) through the live-session filter.
    const tabs = rawTabs.filter(k => isEditorTab(k) || liveIds.has(k));
    const preferred = (pane.activeKey && tabs.includes(pane.activeKey))
      ? pane.activeKey
      : (pane.activeId && tabs.includes(pane.activeId) ? pane.activeId : null);
    let active = preferred || tabs[0] || null;
    const panel = createPanel(null);
    panel.width = pane.width > 0 ? pane.width : 0;
    plans.push({ tabs, active });
  }
  if (!panels.length) return false;
  rebuildChatDOM();
  const focusIdx = Math.min(Math.max(0, rec.focusedIndex | 0), panels.length - 1);
  setFocusedPanel((panels[focusIdx] || panels[0]).id);
  let bound = false;
  for (let i = 0; i < panels.length; i++) {
    const panel = panels[i];
    const { tabs, active } = plans[i];
    if (!tabs.length) { newDraftTab(panel); continue; }
    // Register every tab on the pane, then mount the active one (loads history /
    // editor content). Subscribe background session tabs (editor tabs have none).
    panel.tabs = tabs.slice();
    renderPaneTabs(panel);
    for (const id of tabs) if (id !== active && !isEditorTab(id)) subscribeSessionEvents(id);
    await activateTab(panel, active);
    bound = true;
  }
  return bound;
}

(async function init() {
  if (typeof marked !== "undefined") {
    marked.setOptions({ gfm: true, breaks: true });
  }
  const savedW = localStorage.getItem(SIDEBAR_W_KEY);
  if (savedW) document.documentElement.style.setProperty("--sidebar-w", savedW);
  const savedComposerH = localStorage.getItem(COMPOSER_H_KEY);
  if (savedComposerH) {
    document.documentElement.style.setProperty("--composer-h", savedComposerH);
    composerManuallyResized = true;
  }
  if (localStorage.getItem(SIDEBAR_COL_KEY) === "1") collapseSidebar();
  await loadSquads();
  // After a hot-reload from the Settings panel, refresh the squad picker so
  // newly installed squads show up without a page refresh.
  window.addEventListener("omnis:config-reloaded", () => {
    loadSquads().then(() => {
      // Refresh any open empty-pane picker so new squads show up immediately.
      for (const p of panels) {
        if (!p.sessionId && p.els.picker && !p.els.picker.hidden) renderPickerSquad(p);
      }
    });
    checkProviderHealth(); // a reload may have fixed (or broken) a connection
  });
  loadUserCommands(); // fire-and-forget; menu re-renders when it lands
  subscribeGlobalEvents(); // single multiplexed push stream for all sessions
  // Probe model-provider connectivity and reveal the in-pane warning on failure.
  checkProviderHealth(); // fire-and-forget; renderProviderWarning paints the panes
  // Wire the self-update button and reveal it if a newer release is cached.
  const updateBtn = document.getElementById("update-btn");
  if (updateBtn) updateBtn.addEventListener("click", openUpdateDialog);
  checkForUpdate(); // fire-and-forget
  await loadSessions();

  // Collect live session ids for layout validation.
  const liveIds = new Set();
  for (const li of els.list.children) if (li.dataset.id) liveIds.add(li.dataset.id);

  const saved = loadSavedLayout();
  let restored = false;
  if (saved) {
    try { restored = await restoreLayout(saved, liveIds); }
    catch (e) { console.error("layout restore failed:", e); panels = []; }
  }

  // Fall back to a single pane bound to the most recent session.
  if (!panels.length) {
    const panel = createPanel(null);
    rebuildChatDOM();
    setFocusedPanel(panel.id);
  }
  if (composerManuallyResized) for (const p of panels) p.els.composerWrap.classList.add("is-manual");
  if (!restored && !panels.some(p => p.sessionId)) {
    const first = els.list.querySelector("li[data-id]");
    if (first) await bindSessionToPanel(fp(), first.dataset.id);
    else { newDraftTab(fp()); autoGrowPrompt(fp()); }
  }

  // First-run prompts (each awaits the server prefs sync; no-op once a choice is
  // recorded). The language offer runs first and may reload (switching language),
  // so chain the notification opt-in after it to avoid two stacked modals.
  (async () => {
    await maybePromptLocale(); // may location.reload() when the user switches
    maybePromptNotifications();
  })();
})();
