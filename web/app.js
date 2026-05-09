// Vanilla-JS client for the agent-toolkit HTTP API.
// Uses fetch + ReadableStream to consume SSE (EventSource doesn't allow
// custom headers, so we use fetch with Authorization).

const TOKEN_KEY = "agent_toolkit_token";

const els = {
  sidebar:       document.getElementById("sidebar"),
  sidebarResize: document.getElementById("sidebar-resize"),
  sidebarToggle: document.getElementById("sidebar-toggle"),
  newChat:       document.getElementById("new-chat"),
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
  ctxCompactBtn:  document.getElementById("ctx-compact-btn"),
  composerResize: document.getElementById("composer-resize"),
  fileInput:      document.getElementById("file-input"),
  attachBtn:      document.getElementById("attach-btn"),
  attachments:    document.getElementById("attachments"),
};

let token = localStorage.getItem(TOKEN_KEY) || "";
let activeSessionId = null;
let sendOnEnter = true;

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
  return { ...extra, "Authorization": `Bearer ${token}` };
}

function escHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function scrollBottom() {
  els.transcript.scrollTop = els.transcript.scrollHeight;
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
  { match: /^load_softskill/,    label: "SoftSkill",color: "orange"  },
  { match: /^list_softskill/,    label: "Skills",   color: "orange"  },
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
  for (const k of ["output", "content", "matches", "result", "text"]) {
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
  el.innerHTML = marked.parse(text || "");
  el.classList.add("rendered");
}

// ─── Pinned prompt header ────────────────────────────────────────────────────

// Reduce a (possibly huge / multi-line) user prompt to a one-line label.
function pinnedPromptLabel(text) {
  const firstLine = String(text || "").split("\n", 1)[0];
  return firstLine.length > 300 ? firstLine.slice(0, 300) + "…" : firstLine;
}

// Show text in the fixed header above the transcript.
function setPinnedPrompt(text) {
  els.promptHeader.textContent = pinnedPromptLabel(text);
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
  if (text) bubble.textContent = text;
  if (files && files.length > 0) {
    const chips = document.createElement("div");
    chips.className = "bubble-attachments";
    for (const f of files) {
      const chip = document.createElement("span");
      chip.className = "attachment-chip attachment-chip-sent";
      chip.textContent = f.name;
      chip.title = f.path;
      chips.appendChild(chip);
    }
    bubble.appendChild(chips);
  }
  row.appendChild(bubble);
  (container || els.transcript).appendChild(row);
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
  let activeText = null;
  for (const bubble of userBubbles) {
    const rowRect = bubble.parentElement.getBoundingClientRect();
    if (rowRect.bottom < transcriptRect.top) activeText = bubble.textContent;
  }
  if (activeText !== null) {
    els.promptHeader.textContent = pinnedPromptLabel(activeText);
    els.promptHeader.classList.add("visible");
  } else {
    els.promptHeader.classList.remove("visible");
  }
}

function clearPinnedPrompt() {
  els.promptHeader.textContent = "";
  els.promptHeader.classList.remove("visible");
}

// ─── DOM builders ───────────────────────────────────────────────────────────

function appendAssistantBubble(container) {
  const row = document.createElement("div");
  row.className = "msg-row assistant";
  const bubble = document.createElement("div");
  bubble.className = "bubble-assistant";
  row.appendChild(bubble);
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
  const isTeammate = /^teammate/.test(block.dataset.toolName || "");

  const dot = block.querySelector(".tool-dot");
  if (dot) { dot.classList.remove("pending"); dot.classList.add(isError ? "error" : "done"); }

  const slot = block.querySelector(".tool-out-slot");
  if (!slot) return;

  if (isTeammate && !isError) {
    const formatted = formatTeammateResponse(response);
    if (formatted === null) {
      slot.remove();
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
  slot.replaceWith(outDiv);
}

// ─── API helpers ─────────────────────────────────────────────────────────────

async function apiFetch(path, opts = {}) {
  if (!token) {
    promptForToken();
    throw new Error("token required");
  }
  const headers = authHeaders(opts.headers || {});
  if (opts.body && !(opts.body instanceof FormData) && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
  const res = await fetch(path, { ...opts, headers });
  if (res.status === 401) { promptForToken(); throw new Error("unauthorized"); }
  return res;
}

function promptForToken() {
  const t = window.prompt("Enter API bearer token (GOAGENT_SERVER_TOKEN):", token || "");
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
    li.innerHTML = `
      <div class="session-name-row">
        <span class="session-busy-dot"></span>
        <div class="session-name" title="${escHtml(displayName)}">${escHtml(displayName)}</div>
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
  clearPinnedPrompt();
  for (const li of els.list.children) {
    li.classList.toggle("active", li.dataset.id === id);
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
    scrollBottom();
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
    scrollBottom();
  } catch (e) {
    console.error("failed to load session history:", e);
  }
}

async function newChat() {
  // Leave the settings panel if it's open so the new chat is visible.
  if (window.Settings && window.Settings.isOpen()) window.Settings.close();
  // Drop the outgoing session's push subscription before switching.
  if (activeSessionId) unsubscribeSessionEvents(activeSessionId);
  try {
    const res = await apiFetch("/api/sessions", { method: "POST" });
    const data = await res.json();
    activeSessionId = data.session_id;
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
    for await (const { event } of parseSSE(res)) {
      if (event === "mailbox_push" && !sessionSending.has(sessionId)) {
        await appendNewPushTurns(sessionId);
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
  scrollBottom();
  els.prompt.value = "";
  autoGrowPrompt();
  clearAttachments(sessionId);
  renderAttachmentsUI(sessionId);

  // Per-segment state: each burst of text between tool calls gets its own bubble.
  let segBubble = null;     // current assistant text element
  let segAcc = "";          // accumulated text for the current segment
  let segHadToken = false;  // whether we received streaming tokens this segment
  let segRenderTimer = null; // throttle handle for incremental markdown renders

  function ensureSegment() {
    if (!segBubble) {
      segBubble = appendAssistantBubble(container);
      segAcc = "";
      segHadToken = false;
    }
    return segBubble;
  }

  // Schedule a markdown render of the current segment, throttled to ~200 ms.
  // The very first call renders immediately so the user sees content at once.
  function scheduleRender() {
    if (!segBubble || !segAcc) return;
    if (!segBubble.innerHTML) {
      // First content — render right away for instant feedback.
      renderMarkdown(segBubble, segAcc);
      return;
    }
    if (segRenderTimer !== null) clearTimeout(segRenderTimer);
    segRenderTimer = setTimeout(() => {
      segRenderTimer = null;
      if (segBubble && segAcc) renderMarkdown(segBubble, segAcc);
    }, 200);
  }

  // Render and seal the current text segment, then reset.
  function finalizeSegment() {
    if (segRenderTimer !== null) { clearTimeout(segRenderTimer); segRenderTimer = null; }
    if (!segBubble) return;
    if (segAcc) {
      renderMarkdown(segBubble, segAcc);
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

    for await (const { event, data } of parseSSE(res)) {
      switch (event) {
        case "token": {
          ensureSegment();
          segHadToken = true;
          segAcc += data.text || "";
          scheduleRender();
          scrollBottom();
          setSessionStatus(sessionId, "streaming…");
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
          if (sessionId === activeSessionId && els.ctxPopup && !els.ctxPopup.hasAttribute("hidden")) {
            renderCtxPopup(sessionId);
          }
          break;
        }

        case "error": {
          finalizeSegment();
          appendErrorBubble(data.message || String(data), container);
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

const SLASH_COMMANDS = [
  { cmd: "/help",      args: "",         desc: "Show available commands" },
  { cmd: "/learn",     args: "[reason]", desc: "Mark session for soft-skill curation (runs on session end)" },
  { cmd: "/learn-now", args: "[reason]", desc: "Mark and immediately trigger soft-skill curation" },
  { cmd: "/status",    args: "",         desc: "Show current session info" },
];

let slashMenuFocusIdx = -1;

function renderSlashMenu(prefix) {
  const p = prefix.toLowerCase();
  const matches = p === "/" ? SLASH_COMMANDS : SLASH_COMMANDS.filter(c => c.cmd.startsWith(p));
  if (matches.length === 0) { hideSlashMenu(); return; }
  els.slashMenu.innerHTML = "";
  matches.forEach(item => {
    const row = document.createElement("div");
    row.className = "slash-menu-item";
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
  scrollBottom();
}

async function handleSlashCommand(raw) {
  const trimmed = raw.trim();
  const spaceIdx = trimmed.indexOf(" ");
  const cmdPart = spaceIdx >= 0 ? trimmed.slice(0, spaceIdx) : trimmed;
  const argPart = spaceIdx >= 0 ? trimmed.slice(spaceIdx + 1).trim() : "";
  const cmd = cmdPart.slice(1).toLowerCase();

  switch (cmd) {
    case "help":
      appendCommandBubble(
        "**Available commands**\n\n" +
        "- `/help` — Show this help\n" +
        "- `/learn [reason]` — Mark session for soft-skill curation (runs on session end)\n" +
        "- `/learn-now [reason]` — Mark and immediately trigger soft-skill curation\n" +
        "- `/status` — Show current session info"
      );
      break;

    case "status": {
      const sid = activeSessionId || "none";
      appendCommandBubble(
        `**Session status**\n\n- Session: \`${sid}\`\n` +
        `- Use \`/learn\` to schedule soft-skill curation`
      );
      break;
    }

    case "learn":
    case "learn-now": {
      if (!activeSessionId) {
        appendCommandBubble("No active session — start a chat first.", true);
        return;
      }
      const immediate = cmd === "learn-now";
      const reason = argPart || `manual /${cmd} request from web UI`;
      try {
        const res = await fetch(`/api/sessions/${activeSessionId}/curate`, {
          method: "POST",
          headers: authHeaders({ "Content-Type": "application/json" }),
          body: JSON.stringify({ reason, immediate }),
        });
        const data = await res.json();
        if (!res.ok) { appendCommandBubble(data.error || "curate request failed", true); return; }
        const note = immediate
          ? "\n\nTriggered curation now. Check logs for curator completion."
          : "\n\nCuration runs on session end.";
        appendCommandBubble(data.message + note);
      } catch (err) {
        appendCommandBubble(String(err), true);
      }
      break;
    }

    default:
      appendCommandBubble(`Unknown command: \`/${cmd}\` — try \`/help\``, true);
  }
}

// ─── Event listeners ─────────────────────────────────────────────────────────

els.transcript.addEventListener("scroll", updatePinnedForScroll);
els.newChat.addEventListener("click", newChat);
els.composer.addEventListener("submit", (e) => { e.preventDefault(); sendMessage(); });

els.attachBtn.addEventListener("click", () => els.fileInput.click());

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
      if (items.length === 1) {
        selectSlashCommand(items[0].dataset.value);
      } else if (items.length > 0) {
        slashMenuFocusIdx = (slashMenuFocusIdx + 1) % items.length;
        updateSlashMenuFocus();
      }
      return;
    }
    if (e.key === "Enter" && slashMenuFocusIdx >= 0) {
      e.preventDefault();
      selectSlashCommand(items[slashMenuFocusIdx].dataset.value);
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
  if (!token) promptForToken();
  await loadSessions();
  // Auto-select the most recent session so the user is never left without an
  // active session. This prevents implicit session creation inside sendMessage()
  // which caused session/mailbox state to get scrambled.
  if (!activeSessionId) {
    const first = els.list.querySelector("li[data-id]");
    if (first) await selectSession(first.dataset.id);
  }
})();
