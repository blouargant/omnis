// Vanilla-JS client for the yoke HTTP API.
// Uses fetch + ReadableStream to consume SSE (EventSource doesn't allow
// custom headers, so we use fetch with Authorization).

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
const sessionAgentTokens = new Map(); // sessionId → Map(agentName → {prompt: number, output: number})
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
  addAgentUsage(sessionId, agentName, prompt, output) {
    if (!sessionId || !agentName) return;
    let agents = sessionAgentTokens.get(sessionId);
    if (!agents) { agents = new Map(); sessionAgentTokens.set(sessionId, agents); }
    const ag = agents.get(agentName) || { prompt: 0, output: 0 };
    ag.prompt += prompt | 0;
    ag.output += output | 0;
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
      const fmtCost = (p, o) => "~$" + (p * 3.0/1_000_000 + o * 15.0/1_000_000).toFixed(4);
      const entries = [...agentMap.entries()].sort((a, b) =>
        a[0] === "leader" ? -1 : b[0] === "leader" ? 1 : (b[1].prompt + b[1].output) - (a[1].prompt + a[1].output)
      );
      const nameW = Math.min(14, Math.max(...entries.map(([n]) => n.length)));
      let totP = 0, totO = 0;
      lines.push("[agents]");
      for (const [name, {prompt, output}] of entries) {
        totP += prompt; totO += output;
        lines.push(`         ${name.padEnd(nameW)}  in=${fmtTok(prompt).padStart(6)}  out=${fmtTok(output).padStart(5)}  ${fmtCost(prompt, output)}`);
      }
      if (entries.length > 1) {
        lines.push(`         ${"─".repeat(nameW + 32)}`);
        lines.push(`         ${"total".padEnd(nameW)}  in=${fmtTok(totP).padStart(6)}  out=${fmtTok(totO).padStart(5)}  ${fmtCost(totP, totO)}`);
      }
    }
    this.badge.textContent = lines.join("\n");
  },
};
AgentDebug.reset();
if (AgentDebug.enabled) AgentDebug._paint();
window.AgentDebug = AgentDebug;

const els = {
  sidebar:       document.getElementById("sidebar"),
  sidebarResize: document.getElementById("sidebar-resize"),
  sidebarToggle: document.getElementById("sidebar-toggle"),
  newChat:       document.getElementById("new-chat"),
  newChatWrap:   document.getElementById("new-chat-wrap"),
  squadToggle:   document.getElementById("squad-toggle"),
  squadMenu:     document.getElementById("squad-menu"),
  list:          document.getElementById("session-list"),
  promptHeader:  document.getElementById("prompt-header"),
  transcript:    document.getElementById("transcript"),
  composerWrap:  document.getElementById("composer-wrap"),
  composer:      document.getElementById("composer"),
  prompt:        document.getElementById("prompt"),
  editModeBtn:   document.getElementById("edit-mode-btn"),
  slashBtn:      document.getElementById("slash-btn"),
  slashMenu:     document.getElementById("slash-menu"),
  send:          document.getElementById("send"),
  cancel:        document.getElementById("cancel"),
  status:        document.getElementById("status"),
  ctxRingWrap:    document.getElementById("ctx-ring-wrap"),
  ctxRingSvg:     document.getElementById("ctx-ring-svg"),
  ctxRingFill:    document.querySelector(".ctx-ring-fill"),
  ctxPopup:       document.getElementById("ctx-popup"),
  ctxPopUsed:     document.getElementById("ctx-pop-used"),
  ctxPopMax:      document.getElementById("ctx-pop-max"),
  ctxPopPct:      document.getElementById("ctx-pop-pct"),
  ctxPopBudget:   document.getElementById("ctx-pop-budget"),
  ctxPopAgents:   document.getElementById("ctx-pop-agents"),
  ctxCompactBtn:  document.getElementById("ctx-compact-btn"),
  composerResize: document.getElementById("composer-resize"),
  fileInput:          document.getElementById("file-input"),
  attachBtn:          document.getElementById("attach-btn"),
  attachMenu:         document.getElementById("attach-menu"),
  attachComputer:     document.getElementById("attach-computer"),
  attachContext:      document.getElementById("attach-context"),
  attachments:        document.getElementById("attachments"),
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
let activeSessionId = null;
let sendOnEnter = true;
const ctxBrowserSelected = new Map(); // path → {name, path, size}

// ─── Per-session streaming state ─────────────────────────────────────────────
// Tracks which sessions are actively streaming so switching sessions doesn't
// carry over the disabled Send button or the "streaming…" status label.

const sessionAbortCtrls = new Map(); // sessionId → AbortController
const sessionSending    = new Set(); // sessionIds currently streaming
const sessionStatus     = new Map(); // sessionId → status string

// ─── Per-session push event subscriptions ────────────────────────────────────
// Each open session has a persistent SSE connection to /api/sessions/:id/events
// so background mailbox-push turns are reflected in real time.

const sessionEventsCtrls = new Map(); // sessionId → AbortController
const sessionTurnCounts  = new Map(); // sessionId → number of turns rendered

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

function renderAttachmentsUI(sessionId) {
  if (!els.attachments) return;
  const files = getAttachments(sessionId);
  if (files.length === 0) {
    els.attachments.hidden = true;
    els.attachments.innerHTML = "";
    return;
  }
  els.attachments.hidden = false;
  els.attachments.innerHTML = "";
  for (const f of files) {
    const chip = document.createElement("div");
    chip.className = "attachment-chip";
    chip.title = f.path;
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
    els.attachments.appendChild(chip);
  }
}

function setSessionStatus(sessionId, s) {
  sessionStatus.set(sessionId, s);
  if (sessionId === activeSessionId) setStatus(s);
}

function applySessionUI(id) {
  const active = sessionSending.has(id);
  els.send.disabled   = active;
  els.cancel.disabled = !active;
  setStatus(sessionStatus.get(id) || "");
  if (id === activeSessionId) {
    setCtxRingSpinning(active);
    renderCtxRing(id);
  }
}

// ─── Context ring ────────────────────────────────────────────────────────────

const CTX_RING_CIRCUMFERENCE = 56.55; // 2π × r(9)
const sessionCtxUsage  = new Map(); // sessionId → {tokens_used, soft_limit, hard_limit, window_tokens}
const sessionTokenAccum = new Map(); // sessionId → {prompt: number, output: number}

// Approximate Sonnet-class pricing used for cost estimation.
const PRICE_INPUT_PER_TOK  = 3.0  / 1_000_000; // $3  per million input tokens
const PRICE_OUTPUT_PER_TOK = 15.0 / 1_000_000; // $15 per million output tokens

function setCtxRingSpinning(spinning) {
  if (els.ctxRingSvg) els.ctxRingSvg.classList.toggle("spinning", spinning);
}

function renderCtxRing(sessionId) {
  if (!els.ctxRingFill || !els.ctxRingSvg || !els.ctxRingWrap) return;
  const usage = sessionCtxUsage.get(sessionId);
  if (!usage || !usage.window_tokens) {
    els.ctxRingFill.style.strokeDashoffset = CTX_RING_CIRCUMFERENCE;
    els.ctxRingSvg.dataset.zone = "ok";
    els.ctxRingWrap.classList.remove("has-data");
    els.ctxRingWrap.title = "Context window — click for details";
    return;
  }
  const { tokens_used, soft_limit, hard_limit, window_tokens } = usage;
  const ratio = Math.min(tokens_used / window_tokens, 1);
  const pct = Math.round(ratio * 100);
  els.ctxRingFill.style.strokeDashoffset = CTX_RING_CIRCUMFERENCE * (1 - ratio);
  els.ctxRingSvg.dataset.zone = tokens_used >= hard_limit ? "danger"
    : tokens_used >= soft_limit ? "warn" : "ok";
  els.ctxRingWrap.classList.add("has-data");
  els.ctxRingWrap.title = `Context: ${pct}% used — click for more information`;
}

function renderCtxPopup(sessionId) {
  if (!els.ctxPopup) return;
  const usage = sessionCtxUsage.get(sessionId);
  if (!usage || !usage.window_tokens) {
    els.ctxPopUsed.textContent   = "—";
    els.ctxPopMax.textContent    = "—";
    els.ctxPopPct.textContent    = "—";
    els.ctxPopBudget.textContent = "—";
    if (els.ctxPopAgents) els.ctxPopAgents.hidden = true;
    return;
  }
  const { tokens_used, window_tokens } = usage;
  const ratio = Math.min(tokens_used / window_tokens, 1);
  const pct   = Math.round(ratio * 100);
  els.ctxPopUsed.textContent = tokens_used.toLocaleString();
  els.ctxPopMax.textContent  = window_tokens.toLocaleString();
  els.ctxPopPct.textContent  = `${pct}%`;

  const acc  = sessionTokenAccum.get(sessionId) || { prompt: 0, output: 0 };
  const cost = acc.prompt * PRICE_INPUT_PER_TOK + acc.output * PRICE_OUTPUT_PER_TOK;
  els.ctxPopBudget.textContent = cost > 0 ? `$${cost.toFixed(4)}` : "—";

  // Per-agent breakdown
  const agentsEl = els.ctxPopAgents;
  if (agentsEl) {
    const agentMap = sessionAgentTokens.get(sessionId);
    if (!agentMap || agentMap.size === 0) {
      agentsEl.hidden = true;
    } else {
      agentsEl.hidden = false;
      agentsEl.innerHTML = "";
      const entries = [...agentMap.entries()].sort((a, b) =>
        a[0] === "leader" ? -1 : b[0] === "leader" ? 1 : (b[1].prompt + b[1].output) - (a[1].prompt + a[1].output)
      );
      for (const [name, {prompt, output}] of entries) {
        const agentCost = prompt * PRICE_INPUT_PER_TOK + output * PRICE_OUTPUT_PER_TOK;
        const row = document.createElement("div");
        row.className = "ctx-pop-agent-row";
        const nameEl = document.createElement("span");
        nameEl.className = "ctx-pop-agent-name";
        nameEl.textContent = name;
        const costEl = document.createElement("span");
        costEl.className = "ctx-pop-agent-cost";
        costEl.textContent = `$${agentCost.toFixed(4)}`;
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
    if (sessionId === activeSessionId) renderCtxRing(sessionId);
  } catch (e) {
    console.error("failed to fetch usage estimate:", e);
  }
}

function openCtxPopup() {
  if (!els.ctxPopup) return;
  renderCtxPopup(activeSessionId);
  els.ctxPopup.removeAttribute("hidden");
}

function closeCtxPopup() {
  if (!els.ctxPopup) return;
  els.ctxPopup.setAttribute("hidden", "");
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

function mountSession(sessionId) {
  const next = sessionId ? getContainer(sessionId) : null;
  if (next && els.transcript.contains(next)) return; // already mounted
  while (els.transcript.firstChild) els.transcript.removeChild(els.transcript.firstChild);
  if (next) els.transcript.appendChild(next);
}

// ─── Helpers ────────────────────────────────────────────────────────────────

function setStatus(s) {
  els.status.textContent = s;
  els.status.classList.toggle("active", s !== "");
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
let _stickToBottom = true;
let _scrollPending = false;

function isAtBottom(el) {
  return (el.scrollHeight - el.scrollTop - el.clientHeight) <= STICK_THRESHOLD_PX;
}

function scrollBottom(force = false) {
  if (!force && !_stickToBottom) return;
  if (_scrollPending) return;
  _scrollPending = true;
  requestAnimationFrame(() => {
    _scrollPending = false;
    els.transcript.scrollTop = els.transcript.scrollHeight;
    _stickToBottom = true;
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
  el.innerHTML = marked.parse(text || "");
  el.classList.add("rendered");
  if (el._stream) el._stream = null;
  if (AgentDebug.enabled) AgentDebug.render(performance.now() - t0);
  rewriteLocalImages(el);
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

async function fetchMediaBlobURL(sessionId, path) {
  const key = sessionId + "|" + path;
  if (mediaBlobCache.has(key)) return mediaBlobCache.get(key);
  const url = `/api/sessions/${encodeURIComponent(sessionId)}/media?path=${encodeURIComponent(path)}`;
  const res = await apiFetch(url);
  if (!res.ok) {
    let detail = `${res.status}`;
    try { const j = await res.json(); if (j && j.error) detail = j.error; } catch (_) {}
    throw new Error(detail);
  }
  const blob = await res.blob();
  const objURL = URL.createObjectURL(blob);
  mediaBlobCache.set(key, objURL);
  return objURL;
}

function rewriteLocalImages(rootEl) {
  if (!rootEl || !activeSessionId) return;
  const sessionId = activeSessionId;
  const imgs = rootEl.querySelectorAll("img");
  imgs.forEach(img => {
    let src = img.getAttribute("src") || "";
    if (!src || isRemoteOrInlineSrc(src)) return;
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

// Heuristic extractor: returns local-filesystem paths referenced anywhere in
// a tool-result payload, restricted to known image extensions. Used to surface
// thumbnails directly in the tool-result chip even when the leader hasn't yet
// included markdown image syntax in its reply.
//
// Two extraction modes per visited string:
//   1. The whole string IS a path (no whitespace, ends in image extension).
//      e.g. response.image_path = "/tmp/yoke-images/abc.png".
//   2. The string is a sentence that EMBEDS a path. We pull each substring
//      that starts with "/" (or a Windows drive) and ends in an image
//      extension. e.g. "Generated image saved to /tmp/yoke-images/abc.png".
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

function streamMdInit(bubble) {
  bubble.textContent = "";
  const tail = document.createTextNode("");
  bubble.appendChild(tail);
  bubble._stream = {
    scanIdx: 0,         // chars of segAcc scanned for line boundaries
    blockStart: 0,      // start of the current unfinished block in segAcc
    inFence: false,
    fenceCodeStart: 0,  // start of fenced code body (after opening fence line)
    tailEl: tail,       // outer DOM node holding the streaming text
    tailTextNode: tail, // Text node we appendData() into
    tailKind: "text",
    tailSyncedTo: 0,    // segAcc index up to which the tail mirrors
  };
}

function streamMdReplaceTail(bubble, kind, lang) {
  const s = bubble._stream;
  s.tailEl.remove();
  let newTail, textNode;
  if (kind === "code") {
    newTail = document.createElement("pre");
    const code = document.createElement("code");
    if (lang) code.className = "language-" + lang;
    textNode = document.createTextNode("");
    code.appendChild(textNode);
    newTail.appendChild(code);
  } else {
    newTail = document.createTextNode("");
    textNode = newTail;
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
  // Append any unprocessed tail (incl. partial trailing line) to the live node.
  const delta = fullText.slice(s.tailSyncedTo);
  if (delta) s.tailTextNode.appendData(delta);
  s.tailSyncedTo = fullText.length;
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
}

// ─── Pinned prompt header ────────────────────────────────────────────────────

// Return the full prompt text for the pinned header; CSS handles 3-line clamping.
function pinnedPromptLabel(text) {
  const s = String(text || "");
  return s.length > 1000 ? s.slice(0, 1000) + "…" : s;
}

// Show the user prompt text in the floating header above the transcript.
// Attachments are intentionally NOT rendered here — they live in the inline
// user bubble so the floating header stays compact.
function setPinnedPrompt(text, _files) {
  els.promptHeader.innerHTML = "";
  const label = pinnedPromptLabel(text);
  if (label) {
    const textEl = document.createElement("span");
    textEl.className = "pinned-prompt-text";
    textEl.textContent = label;
    els.promptHeader.appendChild(textEl);
  }
  els.promptHeader.classList.add("visible");
}

// Insert a user message bubble at the current end of the transcript (before streaming).
function appendUserBubble(text, container, files) {
  if (typeof text === "string" && text.startsWith("[mailbox]")) {
    appendMailboxBlock(text, container);
    return;
  }
  const row = document.createElement("div");
  row.className = "msg-row msg-row-user";
  const bubble = document.createElement("div");
  bubble.className = "bubble-user";
  bubble.dataset.text = text || "";
  if (text) {
    const textEl = document.createElement("div");
    textEl.className = "bubble-user-text";
    textEl.textContent = text;
    bubble.appendChild(textEl);
  }
  if (files && files.length > 0) {
    const chips = document.createElement("div");
    chips.className = "bubble-attachments";
    for (const f of files) {
      const chip = document.createElement("span");
      chip.className = "attachment-chip attachment-chip-sent";
      chip.textContent = f.name;
      chip.title = f.path || f.name;
      chips.appendChild(chip);
    }
    bubble.appendChild(chips);
  }
  row.appendChild(bubble);
  (container || els.transcript).appendChild(row);
  // After layout, decide whether the message overflows three lines and, if so,
  // mark it truncated and add a click-to-expand affordance.
  requestAnimationFrame(() => applyUserBubbleTruncation(bubble));
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
  toggle.textContent = "Show more";
  toggle.addEventListener("click", (e) => {
    e.stopPropagation();
    const expanded = bubble.classList.toggle("bubble-user-expanded");
    textEl.classList.toggle("clamped", !expanded);
    toggle.textContent = expanded ? "Show less" : "Show more";
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
  (container || els.transcript).appendChild(row);
}

// Update the floating prompt header to show the question whose agent reply is
// currently at the top of the viewport. That is the last question whose bubble
// has scrolled completely above the transcript top — its reply is what the
// reader sees first, so the header provides the matching context.
// While a bubble is still visible no header is shown (the bubble itself is the label).
function updatePinnedForScroll() {
  const transcriptRect = els.transcript.getBoundingClientRect();
  const userBubbles = els.transcript.querySelectorAll(".bubble-user");
  let activeBubble = null;
  for (const bubble of userBubbles) {
    const rowRect = bubble.parentElement.getBoundingClientRect();
    if (rowRect.bottom < transcriptRect.top) activeBubble = bubble;
  }
  if (activeBubble !== null) {
    const text = activeBubble.dataset.text || "";
    let files = [];
    if (activeBubble.dataset.files) {
      try { files = JSON.parse(activeBubble.dataset.files); } catch { files = []; }
    } else {
      const sentChips = activeBubble.querySelectorAll(".attachment-chip-sent");
      files = Array.from(sentChips).map(c => ({ name: c.textContent, path: c.title }));
    }
    setPinnedPrompt(text, files);
  } else {
    clearPinnedPrompt();
  }
}

function clearPinnedPrompt() {
  els.promptHeader.innerHTML = "";
  els.promptHeader.classList.remove("visible");
}

// ─── DOM builders ───────────────────────────────────────────────────────────

function appendAssistantBubble(container) {
  const row = document.createElement("div");
  row.className = "msg-row assistant";
  const bubble = document.createElement("div");
  bubble.className = "bubble-assistant";

  const copyBtn = document.createElement("button");
  copyBtn.className = "copy-msg-btn";
  copyBtn.title = "Copy message";
  copyBtn.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="2" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`;
  copyBtn.addEventListener("click", () => {
    const text = bubble._rawText || bubble.textContent || "";
    navigator.clipboard.writeText(text).then(() => {
      copyBtn.classList.add("copied");
      setTimeout(() => copyBtn.classList.remove("copied"), 1500);
    }).catch(() => {});
  });

  row.appendChild(bubble);
  row.appendChild(copyBtn);
  (container || els.transcript).appendChild(row);
  scrollBottom();
  return bubble;
}

function appendErrorBubble(text, container) {
  const row = document.createElement("div");
  row.className = "msg-row error";
  const bubble = document.createElement("div");
  bubble.className = "bubble-error";
  bubble.textContent = text;
  row.appendChild(bubble);
  (container || els.transcript).appendChild(row);
  scrollBottom();
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
  (container || els.transcript).appendChild(row);
  scrollBottom();
  return block;
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
  scrollBottom();
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
  (container || els.transcript).appendChild(row);
  scrollBottom();
  return block;
}

function resolveCuratorBlock(block, data, errorMsg) {
  const dot  = block.querySelector(".tool-dot");
  const desc = block.querySelector(".tool-desc");
  const slot = block.querySelector(".tool-out-slot");

  if (errorMsg) {
    if (dot)  { dot.classList.remove("pending"); dot.classList.add("error"); }
    if (desc) desc.textContent = "curation failed";
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

// ─── Ask-user widget ─────────────────────────────────────────────────────────
// Maps questionId → { card, sessionId } so ask_user_cancel can collapse it.
const pendingAskWidgets = new Map();

// renderAskUserWidget creates an interactive card for a structured question.
// Widgets are appended to a fixed slot just above the composer (#ask-user-slot)
// and tagged with the owning session so we can show/hide them on session switch.
function renderAskUserWidget(sessionId, q) {
  const slot = document.getElementById("ask-user-slot");
  if (!slot) return;

  const row = document.createElement("div");
  row.className = "ask-user-row";
  row.setAttribute("data-ask-id", q.question_id);
  row.setAttribute("data-session-id", sessionId);
  if (sessionId !== activeSessionId) row.style.display = "none";

  const card = document.createElement("div");
  card.className = "ask-user-card";

  const promptEl = document.createElement("div");
  promptEl.className = "ask-user-prompt";
  promptEl.textContent = q.prompt;
  card.appendChild(promptEl);

  const kind = q.kind;
  const choices = Array.isArray(q.choices) ? q.choices : [];

  let getAnswer;

  if (kind === "single" || kind === "confirm") {
    const choicesDiv = document.createElement("div");
    choicesDiv.className = "ask-user-choices";
    let selectedValue = null;
    choices.forEach(ch => {
      const label = document.createElement("label");
      label.className = "ask-user-choice";
      const radio = document.createElement("input");
      radio.type = "radio";
      radio.name = "ask_" + q.question_id;
      radio.value = ch;
      radio.addEventListener("change", () => { selectedValue = ch; });
      label.appendChild(radio);
      label.appendChild(document.createTextNode(ch));
      choicesDiv.appendChild(label);
    });
    card.appendChild(choicesDiv);
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
    card.appendChild(choicesDiv);
    let textArea = null;
    if (q.allow_text) {
      textArea = document.createElement("textarea");
      textArea.className = "ask-user-text-input";
      textArea.placeholder = "Additional notes (optional)…";
      card.appendChild(textArea);
    }
    getAnswer = () => {
      const sel = checkboxes.filter(c => c.checked).map(c => c.value);
      return { selected: sel, text: textArea ? textArea.value.trim() : "", cancelled: false };
    };
  } else {
    // text — a password-typed question gets a single-line masked
    // input; everything else gets the regular multi-line textarea so
    // longer free-form answers stay comfortable.
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
    inputEl.placeholder = "Your answer…";
    card.appendChild(inputEl);
    getAnswer = () => ({ selected: [], text: inputEl.value.trim(), cancelled: false });
  }

  const actions = document.createElement("div");
  actions.className = "ask-user-actions";

  const cancelBtn = document.createElement("button");
  cancelBtn.className = "ask-user-cancel-btn";
  cancelBtn.textContent = "Skip";
  actions.appendChild(cancelBtn);

  const submitBtn = document.createElement("button");
  submitBtn.className = "ask-user-submit";
  submitBtn.textContent = "Submit";
  actions.appendChild(submitBtn);

  card.appendChild(actions);
  row.appendChild(card);
  slot.appendChild(row);
  pendingAskWidgets.set(q.question_id, { row, card, sessionId });
  scrollBottom();

  function resolveWidget(answer) {
    pendingAskWidgets.delete(q.question_id);
    card.classList.add("resolved");
    row.classList.add("resolved");
    // Replace interactive content with a summary.
    while (card.lastChild) card.removeChild(card.lastChild);
    const icon = answer.cancelled ? "✗" : "✓";
    // For password questions the entered value is a secret — never echo
    // it back to the transcript. We still show that an answer was given
    // so the conversation history stays coherent.
    const maskText = t => q.password && t ? "••••••••" : t;
    let summary;
    if (answer.cancelled) {
      summary = "skipped";
    } else if (answer.selected && answer.selected.length) {
      summary = answer.selected.join(", ");
      if (answer.text) summary += " — " + maskText(answer.text);
    } else {
      summary = maskText(answer.text) || "(empty)";
    }
    const resolved = document.createElement("div");
    resolved.className = "ask-user-resolved-text";
    resolved.textContent = icon + " " + summary;
    card.appendChild(resolved);
    // Move out of the bottom slot into the transcript so it scrolls as history.
    const container = getContainer(sessionId);
    if (container) container.appendChild(row);
    scrollBottom();
  }

  submitBtn.addEventListener("click", async () => {
    const answer = getAnswer();
    if (!answer) return;
    submitBtn.disabled = true;
    cancelBtn.disabled = true;
    try {
      const res = await apiFetch(`/api/sessions/${sessionId}/ask-user/${q.question_id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(answer),
      });
      if (res.ok) resolveWidget(answer);
      else submitBtn.disabled = false;
    } catch { submitBtn.disabled = false; }
  });

  cancelBtn.addEventListener("click", async () => {
    submitBtn.disabled = true;
    cancelBtn.disabled = true;
    const answer = { selected: [], text: "", cancelled: true };
    try {
      const res = await apiFetch(`/api/sessions/${sessionId}/ask-user/${q.question_id}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(answer),
      });
      if (res.ok) resolveWidget(answer);
      else { submitBtn.disabled = false; cancelBtn.disabled = false; }
    } catch { submitBtn.disabled = false; cancelBtn.disabled = false; }
  });
}

// cancelAskUserWidget collapses a pending widget when the server cancels the question.
function cancelAskUserWidget(questionId) {
  const entry = pendingAskWidgets.get(questionId);
  if (!entry) return;
  pendingAskWidgets.delete(questionId);
  const { row, card, sessionId } = entry;
  card.classList.add("resolved");
  row.classList.add("resolved");
  while (card.lastChild) card.removeChild(card.lastChild);
  const resolved = document.createElement("div");
  resolved.className = "ask-user-resolved-text";
  resolved.textContent = "✗ cancelled";
  card.appendChild(resolved);
  // Move out of the bottom slot into the transcript so it scrolls as history.
  const container = getContainer(sessionId);
  if (container) container.appendChild(row);
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

function resolveToolCall(block, response) {
  const isError = response && typeof response === "object" && typeof response.error === "string";
  const toolName = block.dataset.toolName || "";
  const isTeammate = /^teammate/.test(toolName);
  const isSoftskillList = /^list_softskill/.test(toolName);

  const dot = block.querySelector(".tool-dot");
  if (dot) { dot.classList.remove("pending"); dot.classList.add(isError ? "error" : "done"); }

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
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401) { promptForToken(); throw new Error("unauthorized"); }
  return res;
}

function promptForToken() {
  const t = window.prompt("Enter API bearer token (YOKE_SERVER_TOKEN):", token || "");
  if (t !== null) {
    token = t.trim();
    localStorage.setItem(TOKEN_KEY, token);
  }
}

// ─── Sessions ────────────────────────────────────────────────────────────────

async function loadSessions() {
  try {
    const res = await apiFetch("/api/sessions");
    const data = await res.json();
    renderSessions(data.sessions || []);
  } catch (e) { console.error(e); }
}

function renderSessions(sessions) {
  els.list.innerHTML = "";
  for (const s of sessions) {
    const li = document.createElement("li");
    li.dataset.id = s.id;
    if (s.id === activeSessionId) li.classList.add("active");
    const ts = new Date(s.last_used_at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    const displayName = s.title || s.id;

    if (sessionSending.has(s.id)) li.classList.add("session-busy");
    // Show a squad badge only when the session uses a non-default squad,
    // so single-squad / default setups stay visually quiet.
    const showBadge = s.squad && s.squad !== defaultSquadName;
    const badgeHtml = showBadge
      ? `<span class="session-squad-badge" title="Squad: ${escHtml(s.squad)}">${escHtml(s.squad)}</span>`
      : "";
    li.innerHTML = `
      <div class="session-name-row">
        <span class="session-busy-dot"></span>
        <div class="session-name" title="${escHtml(displayName)}">${escHtml(displayName)}</div>
        ${badgeHtml}
      </div>
      <div class="session-bottom-row">
        <span class="meta">${s.turns} turn${s.turns === 1 ? "" : "s"} · ${ts}</span>
        <div class="session-actions">
          <button class="session-action-btn rename-btn" title="Rename" tabindex="-1">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
          </button>
          <button class="session-action-btn delete-btn" title="Delete" tabindex="-1">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6"/><path d="M10 11v6M14 11v6"/><path d="M9 6V4a1 1 0 011-1h4a1 1 0 011 1v2"/></svg>
          </button>
        </div>
      </div>
    `;

    li.addEventListener("click", (e) => {
      if (e.target.closest(".session-actions")) return;
      selectSession(s.id);
    });
    li.querySelector(".rename-btn").addEventListener("click", (e) => {
      e.stopPropagation();
      startRename(li, s.id, s.title || "");
    });
    li.querySelector(".delete-btn").addEventListener("click", (e) => {
      e.stopPropagation();
      deleteSession(s.id, li);
    });

    els.list.appendChild(li);
  }
}

function setSessionBusy(sessionId, busy) {
  const li = els.list.querySelector(`li[data-id="${CSS.escape(sessionId)}"]`);
  if (li) li.classList.toggle("session-busy", busy);
}

async function deleteSession(id, li) {
  try {
    await apiFetch(`/api/sessions/${id}`, { method: "DELETE" });
    unsubscribeSessionEvents(id);
    sessionTurnCounts.delete(id);
    sessionContainers.delete(id);
    sessionCtxUsage.delete(id);
    sessionTokenAccum.delete(id);
    sessionAgentTokens.delete(id);
    // Remove any pending ask_user widgets belonging to this session.
    const slot = document.getElementById("ask-user-slot");
    if (slot) {
      for (const row of [...slot.children]) {
        if (row.getAttribute("data-session-id") === id) row.remove();
      }
    }
    for (const [qid, entry] of pendingAskWidgets) {
      if (entry.sessionId === id) pendingAskWidgets.delete(qid);
    }
    if (activeSessionId === id) {
      activeSessionId = null;
      clearPinnedPrompt();
      els.transcript.innerHTML = "";
    }
    li.remove();
  } catch (e) {
    console.error("failed to delete session:", e);
  }
}

function startRename(li, id, currentTitle) {
  const nameEl = li.querySelector(".session-name");
  const input = document.createElement("input");
  input.type = "text";
  input.className = "session-rename-input";
  input.value = currentTitle;
  input.placeholder = "Session name…";

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

async function selectSession(id) {
  // Leave the settings panel if it's open so the chat is visible.
  if (window.Settings && window.Settings.isOpen()) window.Settings.close();
  // Unsubscribe from the previous session's push events.
  if (activeSessionId && activeSessionId !== id) {
    unsubscribeSessionEvents(activeSessionId);
  }

  activeSessionId = id;
  if (AgentDebug.enabled) { AgentDebug.activeSession = id; AgentDebug._paint(); }
  clearPinnedPrompt();
  for (const li of els.list.children) {
    li.classList.toggle("active", li.dataset.id === id);
  }

  // Toggle ask-user widgets so only this session's pending question is visible.
  const slot = document.getElementById("ask-user-slot");
  if (slot) {
    for (const row of slot.children) {
      row.style.display = row.getAttribute("data-session-id") === id ? "" : "none";
    }
  }

  applySessionUI(id);
  renderAttachmentsUI(id);

  // Seed ring/popup with server-side estimates for sessions that have no
  // real-time SSE data yet (cold load or page refresh).
  if (!sessionCtxUsage.has(id)) fetchUsageEstimate(id);

  // Subscribe to background push events for the newly opened session.
  subscribeSessionEvents(id);

  const container = getContainer(id);

  // If the container already has content it's either a live stream in progress
  // or was previously viewed — show it and check for background turns that
  // arrived while this session was not the active view.
  if (container.childNodes.length > 0) {
    mountSession(id);
    scrollBottom(true);
    await appendNewPushTurns(id);
    return;
  }

  mountSession(id);

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
      b.textContent = "No messages yet.";
      row.appendChild(b);
      container.appendChild(row);
      return;
    }
    for (const turn of turns) {
      appendUserBubble(turn.user_text, container);
      const bubble = appendAssistantBubble(container);
      renderMarkdown(bubble, turn.assistant_text);
    }
    scrollBottom(true);
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
    btn.title = sq.description || `${sq.leader} + ${(sq.members || []).join(", ")}`;
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

async function newChat() {
  // Leave the settings panel if it's open so the new chat is visible.
  if (window.Settings && window.Settings.isOpen()) window.Settings.close();
  // Drop the outgoing session's push subscription before switching.
  if (activeSessionId) unsubscribeSessionEvents(activeSessionId);
  const squad = currentSquadChoice();
  try {
    const res = await apiFetch("/api/sessions", {
      method: "POST",
      body: JSON.stringify({ squad }),
    });
    if (!res.ok) {
      const errBody = await res.json().catch(() => ({}));
      console.error("new chat failed:", errBody.error || res.statusText);
      return;
    }
    const data = await res.json();
    activeSessionId = data.session_id;
    // Persist the choice so the same squad is preselected next time.
    if (squad) localStorage.setItem(SQUAD_PREF_KEY, squad);
    clearPinnedPrompt();
    mountSession(activeSessionId);
    applySessionUI(activeSessionId);
    subscribeSessionEvents(activeSessionId);
    await loadSessions();
  } catch (e) { console.error(e); }
}

// ─── Background push helpers ─────────────────────────────────────────────────

// subscribeSessionEvents opens a persistent SSE connection for sessionId and
// reloads new turns whenever the server emits a mailbox_push event.
async function subscribeSessionEvents(sessionId) {
  if (sessionEventsCtrls.has(sessionId)) {
    sessionEventsCtrls.get(sessionId).abort();
  }
  const ctrl = new AbortController();
  sessionEventsCtrls.set(sessionId, ctrl);
  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/events`, {
      signal: ctrl.signal,
    });
    if (!res.ok) return;
    for await (const { event, data } of parseSSE(res)) {
      if (event === "mailbox_push" && !sessionSending.has(sessionId)) {
        await appendNewPushTurns(sessionId);
      } else if (event === "ask_user" && data && typeof data === "object") {
        renderAskUserWidget(sessionId, data);
      } else if (event === "ask_user_cancel" && data && data.question_id) {
        cancelAskUserWidget(data.question_id);
      }
    }
  } catch (e) {
    if (e.name !== "AbortError") {
      console.error("push events error for", sessionId, e);
    }
  }
}

function unsubscribeSessionEvents(sessionId) {
  const ctrl = sessionEventsCtrls.get(sessionId);
  if (ctrl) { ctrl.abort(); sessionEventsCtrls.delete(sessionId); }
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
      appendUserBubble(turns[i].user_text, container);
      const bubble = appendAssistantBubble(container);
      renderMarkdown(bubble, turns[i].assistant_text);
    }
    sessionTurnCounts.set(sessionId, turns.length);

    if (sessionId === activeSessionId) {
      // Defer scroll until after the browser has reflowed the rendered markdown.
      requestAnimationFrame(scrollBottom);
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
  banner.textContent = "📬 Background message received and processed";
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
      let event = "message", data = "";
      for (const line of frame.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) data += line.slice(5).trim();
      }
      let parsed = data;
      try { parsed = JSON.parse(data); } catch (_) { /* keep raw */ }
      yield { event, data: parsed };
    }
  }
}

// ─── Send message ────────────────────────────────────────────────────────────

async function sendMessage() {
  const prompt = els.prompt.value.trim();
  const pendingFiles = getAttachments(activeSessionId);
  if (!prompt && pendingFiles.length === 0) return;
  if (prompt.startsWith("/") && pendingFiles.length === 0) {
    els.prompt.value = "";
    hideSlashMenu();
    await handleSlashCommand(prompt);
    return;
  }
  if (!activeSessionId) await newChat();
  if (!activeSessionId) return;

  // Capture session identity and container. The user may switch sessions
  // mid-stream; these captured references keep writes going to the right DOM.
  const sessionId = activeSessionId;
  const files = getAttachments(sessionId);
  const container = getContainer(sessionId);
  if (!els.transcript.contains(container)) mountSession(sessionId);

  // Collect uploaded file paths. Images are passed as structured data so the
  // server can attach them as inline binary parts for vision-capable models.
  // Non-image files are currently ignored (the agent can still find them via
  // their on-disk paths if the user mentions them in the prompt).
  const filePaths = files.map(f => f.path);

  // Insert the user message into the transcript before streaming starts.
  appendUserBubble(prompt, container, files.length > 0 ? files : null);
  scrollBottom(true);
  els.prompt.value = "";
  autoGrowPrompt();
  clearAttachments(sessionId);
  renderAttachmentsUI(sessionId);

  // Per-segment state: each burst of text between tool calls gets its own bubble.
  let segBubble = null;     // current assistant text element
  let segAcc = "";          // accumulated text for the current segment
  let segHadToken = false;  // whether we received streaming tokens this segment

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
    } else {
      segBubble.remove();
    }
    segBubble = null;
    segAcc = "";
    segHadToken = false;
  }

  // FIFO queue pairing tool_call blocks with their tool_result.
  const pendingTools = [];
  // Track the currently active outer block so nested sub-agent events can be
  // appended inside it, plus a FIFO queue for the nested blocks themselves.
  let activeOuterBlock = null;
  const innerPending = [];

  const ctrl = new AbortController();
  sessionAbortCtrls.set(sessionId, ctrl);
  sessionSending.add(sessionId);
  setSessionBusy(sessionId, true);
  setSessionStatus(sessionId, "thinking…");
  if (sessionId === activeSessionId) applySessionUI(sessionId);

  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/messages`, {
      method: "POST",
      body: JSON.stringify({ prompt, ...(filePaths.length > 0 && { files: filePaths }) }),
      signal: ctrl.signal,
    });
    if (!res.ok) {
      const txt = await res.text();
      appendErrorBubble(`error ${res.status}: ${txt}`, container);
      return;
    }

    AgentDebug.start(sessionId);
    for await (const { event, data } of parseSSE(res)) {
      switch (event) {
        case "token": {
          ensureSegment();
          if (!segHadToken) { AgentDebug.firstToken(); setSessionStatus(sessionId, "streaming…"); }
          segHadToken = true;
          const txt = data.text || "";
          segAcc += txt;
          AgentDebug.token(txt.length);
          scheduleRender();
          scrollBottom();
          break;
        }

        case "debug_timing": {
          AgentDebug.serverTiming(data);
          break;
        }

        case "message": {
          // Non-streaming final text; skip if we already got streaming tokens.
          if (!segHadToken && data.text) {
            ensureSegment();
            segAcc = data.text;
            renderMarkdown(segBubble, segAcc);
            scrollBottom();
          }
          break;
        }

        case "tool_call": {
          // Seal the preceding text segment before showing the tool.
          finalizeSegment();
          const block = appendToolCall(data.name, data.args, container);
          pendingTools.push(block);
          activeOuterBlock = block;
          innerPending.length = 0;
          setSessionStatus(sessionId, `running ${data.name}…`);
          break;
        }

        case "tool_result": {
          const block = pendingTools.shift();
          if (block) resolveToolCall(block, data.response);
          activeOuterBlock = null;
          setSessionStatus(sessionId, "thinking…");
          break;
        }

        case "agent_tool_call": {
          if (activeOuterBlock) {
            const inner = appendNestedToolCall(activeOuterBlock, data.name, data.args);
            innerPending.push(inner);
          }
          break;
        }

        case "agent_tool_result": {
          const inner = innerPending.shift();
          if (inner) resolveToolCall(inner, data.response);
          break;
        }

        case "agent_tool_error": {
          const inner = innerPending.shift();
          if (inner) resolveToolCall(inner, { error: data.error });
          break;
        }

        case "context_usage": {
          sessionCtxUsage.set(sessionId, data);
          if (sessionId === activeSessionId) renderCtxRing(sessionId);
          break;
        }

        case "turn_usage": {
          const acc = sessionTokenAccum.get(sessionId) || { prompt: 0, output: 0 };
          acc.prompt += (data.prompt_tokens || 0);
          acc.output += (data.output_tokens || 0);
          sessionTokenAccum.set(sessionId, acc);
          // Always accumulate per-agent tokens (used by ctx popup and debug badge).
          AgentDebug.addAgentUsage(sessionId, data.agent, data.prompt_tokens || 0, data.output_tokens || 0);
          if (sessionId === activeSessionId) {
            if (els.ctxPopup && !els.ctxPopup.hasAttribute("hidden")) renderCtxPopup(sessionId);
            if (AgentDebug.enabled) AgentDebug._paint();
          }
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

        case "done":
          break;
      }
    }
  } catch (e) {
    if (e.name === "AbortError") {
      appendErrorBubble("(cancelled)", container);
    } else {
      appendErrorBubble(String(e), container);
    }
  } finally {
    finalizeSegment();
    AgentDebug.end();
    // Clean up any still-pending tool dots (e.g. on cancel).
    for (const b of [...pendingTools, ...innerPending]) {
      const dot = b.querySelector(".tool-dot");
      if (dot) dot.classList.remove("pending");
    }
    sessionAbortCtrls.delete(sessionId);
    sessionSending.delete(sessionId);
    setSessionBusy(sessionId, false);
    sessionStatus.delete(sessionId);
    if (sessionId === activeSessionId) applySessionUI(sessionId);
    // Track turn count so appendNewPushTurns knows where to start.
    sessionTurnCounts.set(sessionId, (sessionTurnCounts.get(sessionId) ?? 0) + 1);
    loadSessions();
    scrollBottom();
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

let slashMenuFocusIdx = -1;

function renderSlashMenu(prefix) {
  const p = prefix.toLowerCase();
  const all = getAllSlashEntries();
  const matches = p === "/" ? all : all.filter(c => c.cmd.startsWith(p));
  els.slashMenu.innerHTML = "";

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
  els.slashMenu.appendChild(add);

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
    els.slashMenu.appendChild(row);
  });

  slashMenuFocusIdx = -1;
  els.slashMenu.removeAttribute("hidden");
  els.slashBtn.classList.add("active");
}

function hideSlashMenu() {
  els.slashMenu.setAttribute("hidden", "");
  els.slashBtn.classList.remove("active");
  slashMenuFocusIdx = -1;
}

function updateSlashMenuFocus() {
  const items = els.slashMenu.querySelectorAll(".slash-menu-item");
  items.forEach((it, i) => it.classList.toggle("focused", i === slashMenuFocusIdx));
  if (slashMenuFocusIdx >= 0 && items[slashMenuFocusIdx]) {
    items[slashMenuFocusIdx].scrollIntoView({ block: "nearest" });
  }
}

function selectSlashCommand(text) {
  els.prompt.value = text;
  els.prompt.setSelectionRange(text.length, text.length);
  els.prompt.focus();
  hideSlashMenu();
}

function appendCommandBubble(text, isError = false) {
  const container = activeSessionId ? getContainer(activeSessionId) : els.transcript;
  if (activeSessionId && !els.transcript.contains(container)) mountSession(activeSessionId);
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
  container.appendChild(row);
  scrollBottom(true);
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

async function handleSlashCommand(raw) {
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
        appendCommandBubble(`Command \`/${cmd}\` expanded to an empty prompt.`, true);
        return;
      }
      els.prompt.value = expanded;
      autoGrowPrompt();
      await sendMessage();
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
        "- `/status` — Show current session info";
      if (userSlashCommands.length) {
        body += "\n\n**User commands**\n\n" + userSlashCommands.map(c => {
          const args = c.args ? ` ${c.args}` : "";
          const desc = c.description ? ` — ${c.description}` : "";
          return `- \`/${c.name}${args}\`${desc}`;
        }).join("\n");
      }
      appendCommandBubble(body);
      break;
    }

    case "status": {
      const sid = activeSessionId || "none";
      appendCommandBubble(
        `**Session status**\n\n- Session: \`${sid}\`\n` +
        `- Use \`/learn\` to schedule soft-skill curation`
      );
      break;
    }

    case "learn": {
      if (!activeSessionId) {
        appendCommandBubble("No active session — start a chat first.", true);
        return;
      }
      const reason = argPart || "manual /learn request from web UI";
      try {
        const res = await apiFetch(`/api/sessions/${activeSessionId}/curate`, {
          method: "POST",
          body: JSON.stringify({ reason, immediate: false }),
        });
        if (!res.ok) {
          const d = await res.json();
          appendCommandBubble(d.error || "curate request failed", true);
          return;
        }
        appendCommandBubble("Session marked for soft-skill curation — runs on session end.");
      } catch (err) {
        appendCommandBubble(String(err), true);
      }
      break;
    }

    case "learn-now": {
      if (!activeSessionId) {
        appendCommandBubble("No active session — start a chat first.", true);
        return;
      }
      const reason = argPart || "manual /learn-now request from web UI";
      const container = getContainer(activeSessionId);
      if (!els.transcript.contains(container)) mountSession(activeSessionId);
      const curatorBlock = appendCuratorBlock(container);
      scrollBottom(true);
      try {
        const res = await apiFetch(`/api/sessions/${activeSessionId}/curate`, {
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
          `Create a new skill called "${name}". Load the skill-creator skill and guide me through defining it interactively.`
        );
      });
      break;
    }

    case "update-skill": {
      openSkillNameModal("Update skill", argPart.trim(), (name) => {
        sendSkillPrompt(
          `Update the skill "${name}". Load the skill-creator skill and help me revise it.`
        );
      });
      break;
    }

    case "compress": {
      if (!activeSessionId) {
        appendCommandBubble("No active session — start a chat first.", true);
        return;
      }
      try {
        const res = await apiFetch(`/api/sessions/${activeSessionId}/compact`, {
          method: "POST",
        });
        if (!res.ok) {
          const d = await res.json();
          appendCommandBubble(d.error || "compress request failed", true);
          return;
        }
        appendCommandBubble("Context compression queued — runs before the next model call.");
      } catch (err) {
        appendCommandBubble(String(err), true);
      }
      break;
    }

    default:
      appendCommandBubble(`Unknown command: \`/${cmd}\` — try \`/help\``, true);
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
  els.userCmdTitle.textContent = isEdit ? "Edit command" : "Add command";
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

els.transcript.addEventListener("scroll", () => {
  _stickToBottom = isAtBottom(els.transcript);
  updatePinnedForScroll();
});
els.newChat.addEventListener("click", newChat);

// Squad picker dropdown — chevron next to the New Chat button toggles
// a menu that picks which squad future sessions use. The menu is hidden
// outright when only the default squad exists.
els.squadToggle.addEventListener("click", (e) => {
  e.stopPropagation();
  if (els.squadMenu.hidden) openSquadMenu();
  else closeSquadMenu();
});
els.squadMenu.addEventListener("click", (e) => e.stopPropagation());
document.addEventListener("click", () => closeSquadMenu());
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !els.squadMenu.hidden) closeSquadMenu();
});

els.composer.addEventListener("submit", (e) => { e.preventDefault(); sendMessage(); });

// Attach button toggles the popup menu
els.attachBtn.addEventListener("click", (e) => {
  e.stopPropagation();
  els.attachMenu.toggleAttribute("hidden");
});
// Clicks outside the popup close it; clicks inside don't bubble up
document.addEventListener("click", () => els.attachMenu.setAttribute("hidden", ""));
els.attachMenu.addEventListener("click", (e) => e.stopPropagation());

els.attachComputer.addEventListener("click", () => {
  els.attachMenu.setAttribute("hidden", "");
  els.fileInput.click();
});
els.attachContext.addEventListener("click", () => {
  els.attachMenu.setAttribute("hidden", "");
  openCtxBrowser();
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
    els.skillNameError.textContent = "Skill name is required.";
    els.skillNameError.hidden = false;
    return;
  }
  if (!SKILL_NAME_RE.test(name)) {
    els.skillNameError.textContent = "Name must match: lowercase letters, digits, '-' or '.' (1–63 chars).";
    els.skillNameError.hidden = false;
    return;
  }
  const cb = skillNameModalCallback;
  closeSkillNameModal();
  if (cb) cb(name);
}

// Sends a pre-crafted prompt to the active session (creating one if needed).
async function sendSkillPrompt(prompt) {
  if (!activeSessionId) await newChat();
  if (!activeSessionId) return;
  els.prompt.value = prompt;
  autoGrowPrompt();
  await sendMessage();
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
  if (!activeSessionId) await newChat();
  if (!activeSessionId) { closeCtxBrowser(); return; }
  const sid = activeSessionId;
  for (const f of ctxBrowserSelected.values()) addAttachment(sid, f);
  renderAttachmentsUI(sid);
  closeCtxBrowser();
});

els.fileInput.addEventListener("change", async () => {
  const picked = Array.from(els.fileInput.files);
  if (!picked.length) return;
  els.fileInput.value = "";

  if (!activeSessionId) await newChat();
  if (!activeSessionId) return;

  const sessionId = activeSessionId;
  const form = new FormData();
  for (const f of picked) form.append("files", f);

  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/files`, {
      method: "POST",
      body: form,
    });
    if (!res.ok) {
      const txt = await res.text();
      console.error("upload failed:", txt);
      return;
    }
    const data = await res.json();
    for (const f of (data.files || [])) addAttachment(sessionId, f);
    if (sessionId === activeSessionId) renderAttachmentsUI(sessionId);
  } catch (e) {
    console.error("upload error:", e);
  }
});
els.prompt.addEventListener("paste", async (e) => {
  const items = e.clipboardData?.items;
  if (!items) return;

  const imageItems = Array.from(items).filter(it => it.kind === "file" && it.type.startsWith("image/"));
  if (!imageItems.length) return;

  e.preventDefault();

  if (!activeSessionId) await newChat();
  if (!activeSessionId) return;

  const sessionId = activeSessionId;
  const form = new FormData();
  for (const item of imageItems) {
    const file = item.getAsFile();
    if (file) form.append("files", file, file.name || `screenshot-${Date.now()}.png`);
  }

  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/files`, {
      method: "POST",
      body: form,
    });
    if (!res.ok) {
      console.error("paste upload failed:", await res.text());
      return;
    }
    const data = await res.json();
    for (const f of (data.files || [])) addAttachment(sessionId, f);
    if (sessionId === activeSessionId) renderAttachmentsUI(sessionId);
  } catch (err) {
    console.error("paste upload error:", err);
  }
});

// ─── Drag-and-drop file upload ────────────────────────────────────────────────
let dragCounter = 0;

els.composerWrap.addEventListener("dragenter", (e) => {
  if (!e.dataTransfer?.types.includes("Files")) return;
  e.preventDefault();
  dragCounter++;
  els.composerWrap.classList.add("drag-over");
});

els.composerWrap.addEventListener("dragover", (e) => {
  if (!e.dataTransfer?.types.includes("Files")) return;
  e.preventDefault();
  e.dataTransfer.dropEffect = "copy";
});

els.composerWrap.addEventListener("dragleave", () => {
  dragCounter--;
  if (dragCounter <= 0) {
    dragCounter = 0;
    els.composerWrap.classList.remove("drag-over");
  }
});

els.composerWrap.addEventListener("drop", async (e) => {
  e.preventDefault();
  dragCounter = 0;
  els.composerWrap.classList.remove("drag-over");

  const files = Array.from(e.dataTransfer?.files || []);
  if (!files.length) return;

  if (!activeSessionId) await newChat();
  if (!activeSessionId) return;

  const sessionId = activeSessionId;
  const form = new FormData();
  for (const f of files) form.append("files", f);

  try {
    const res = await apiFetch(`/api/sessions/${sessionId}/files`, {
      method: "POST",
      body: form,
    });
    if (!res.ok) {
      console.error("drop upload failed:", await res.text());
      return;
    }
    const data = await res.json();
    for (const f of (data.files || [])) addAttachment(sessionId, f);
    if (sessionId === activeSessionId) renderAttachmentsUI(sessionId);
  } catch (err) {
    console.error("drop upload error:", err);
  }
});

els.cancel.addEventListener("click", () => {
  const ctrl = sessionAbortCtrls.get(activeSessionId);
  if (ctrl) ctrl.abort();
});
function updateEditModeBtn() {
  els.editModeBtn.classList.toggle("active", !sendOnEnter);
  els.editModeBtn.title = sendOnEnter
    ? "Edit mode: switch to Enter=new line, Ctrl+Enter=send"
    : "Send mode: switch to Enter=send, Ctrl+Enter=new line";
  els.prompt.placeholder = sendOnEnter
    ? "Message the agent… (Enter to send)"
    : "Message the agent… (Ctrl+Enter to send)";
}

els.editModeBtn.addEventListener("click", () => {
  sendOnEnter = !sendOnEnter;
  updateEditModeBtn();
});

els.prompt.addEventListener("keydown", (e) => {
  if (!els.slashMenu.hasAttribute("hidden")) {
    const items = Array.from(els.slashMenu.querySelectorAll(".slash-menu-item"));
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
      if (focused.classList.contains("slash-menu-add")) {
        hideSlashMenu();
        openUserCommandModal(null);
      } else {
        selectSlashCommand(focused.dataset.value);
      }
      return;
    }
    if (e.key === "Escape") {
      hideSlashMenu();
      return;
    }
  }
  if (sendOnEnter) {
    if (e.key === "Enter" && !e.ctrlKey && !e.metaKey && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      const start = els.prompt.selectionStart;
      const end = els.prompt.selectionEnd;
      els.prompt.value = els.prompt.value.substring(0, start) + "\n" + els.prompt.value.substring(end);
      els.prompt.selectionStart = els.prompt.selectionEnd = start + 1;
      els.prompt.dispatchEvent(new Event("input"));
    }
  } else {
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      sendMessage();
    }
  }
});

els.prompt.addEventListener("input", () => {
  autoGrowPrompt();
  const val = els.prompt.value;
  const firstLine = val.split("\n")[0];
  const firstWord = firstLine.split(" ")[0];
  if (val.startsWith("/") && !firstLine.includes(" ")) {
    renderSlashMenu(firstWord);
  } else {
    hideSlashMenu();
  }
});

els.slashBtn.addEventListener("click", () => {
  if (!els.prompt.value.startsWith("/")) {
    els.prompt.value = "/" + els.prompt.value;
  }
  els.prompt.focus();
  const firstWord = els.prompt.value.split("\n")[0].split(" ")[0];
  renderSlashMenu(firstWord);
});

document.addEventListener("mousedown", (e) => {
  if (!els.slashMenu.hasAttribute("hidden") && !els.composerWrap.contains(e.target)) {
    hideSlashMenu();
  }
});

// ─── Composer resize ─────────────────────────────────────────────────────────

const COMPOSER_MIN_H  = 116;
const COMPOSER_H_KEY  = "agent_toolkit_composer_h";
const MAX_AUTO_LINES  = 10;

let composerDragging       = false;
let composerDragStartY     = 0;
let composerDragStartH     = 0;
let composerManuallyResized = false;

function setComposerHeight(h) {
  const clamped = Math.max(COMPOSER_MIN_H, h);
  document.documentElement.style.setProperty("--composer-h", clamped + "px");
  localStorage.setItem(COMPOSER_H_KEY, clamped + "px");
  if (!composerManuallyResized) {
    composerManuallyResized = true;
    els.composerWrap.classList.add("is-manual");
    els.prompt.style.height = "";
  }
}

function autoGrowPrompt() {
  if (composerManuallyResized) return;
  const el = els.prompt;
  const cs = getComputedStyle(el);
  const lineH  = parseFloat(cs.lineHeight);
  const padY   = parseFloat(cs.paddingTop) + parseFloat(cs.paddingBottom);
  const maxH   = lineH * MAX_AUTO_LINES + padY;
  el.style.height = "auto";
  const natural = Math.min(el.scrollHeight, maxH);
  el.style.height = natural + "px";
  el.style.overflowY = el.scrollHeight > maxH ? "auto" : "hidden";
}

els.composerResize.addEventListener("mousedown", (e) => {
  composerDragging   = true;
  composerDragStartY = e.clientY;
  composerDragStartH = els.composerWrap.getBoundingClientRect().height;
  els.composerResize.classList.add("is-dragging");
  document.body.classList.add("resizing-composer");
  document.body.style.userSelect = "none";
  e.preventDefault();
});

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
  localStorage.setItem(SIDEBAR_COL_KEY, "1");
}

function expandSidebar() {
  els.sidebar.classList.remove("collapsed");
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
  }
});

document.addEventListener("mouseup", () => {
  if (composerDragging) {
    composerDragging = false;
    els.composerResize.classList.remove("is-dragging");
    document.body.classList.remove("resizing-composer");
    document.body.style.userSelect = "";
  }
  if (sidebarDragging) {
    sidebarDragging = false;
    els.sidebarResize.classList.remove("is-dragging");
    document.body.classList.remove("resizing");
    document.body.style.userSelect = "";
  }
});

els.sidebarToggle.addEventListener("click", () => {
  if (els.sidebar.classList.contains("collapsed")) expandSidebar();
  else collapseSidebar();
});

// ─── Context ring popup ───────────────────────────────────────────────────────

els.ctxRingWrap.addEventListener("click", (e) => {
  e.stopPropagation();
  if (els.ctxPopup.hasAttribute("hidden")) openCtxPopup();
  else closeCtxPopup();
});

// Close popup when clicking outside it.
document.addEventListener("click", (e) => {
  if (els.ctxPopup && !els.ctxPopup.hasAttribute("hidden") &&
      !els.ctxRingWrap.contains(e.target)) {
    closeCtxPopup();
  }
});

// Compress Now button.
els.ctxCompactBtn.addEventListener("click", async (e) => {
  e.stopPropagation();
  if (!activeSessionId) return;
  els.ctxCompactBtn.disabled = true;
  els.ctxCompactBtn.textContent = "Queuing…";
  try {
    const res = await apiFetch(`/api/sessions/${activeSessionId}/compact`, { method: "POST" });
    if (!res.ok) throw new Error(await res.text());
    els.ctxCompactBtn.textContent = "Queued ✓";
  } catch (err) {
    console.error("compact request failed:", err);
    els.ctxCompactBtn.textContent = "Error";
  }
  setTimeout(() => {
    els.ctxCompactBtn.disabled = false;
    els.ctxCompactBtn.textContent = "Compress Now";
    closeCtxPopup();
  }, 1400);
});

// ─── Init ─────────────────────────────────────────────────────────────────────

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
    els.composerWrap.classList.add("is-manual");
  } else {
    autoGrowPrompt();
  }
  if (localStorage.getItem(SIDEBAR_COL_KEY) === "1") els.sidebar.classList.add("collapsed");
  await loadSquads();
  loadUserCommands(); // fire-and-forget; menu re-renders when it lands
  await loadSessions();
  // Auto-select the most recent session so the user is never left without an
  // active session. This prevents implicit session creation inside sendMessage()
  // which caused session/mailbox state to get scrambled.
  if (!activeSessionId) {
    const first = els.list.querySelector("li[data-id]");
    if (first) await selectSession(first.dataset.id);
  }
})();
