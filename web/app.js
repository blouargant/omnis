// Vanilla-JS client for the agent-toolkit HTTP API.
// Uses fetch + ReadableStream to consume SSE (EventSource doesn't allow
// custom headers, so we use fetch with Authorization).

const TOKEN_KEY = "agent_toolkit_token";

const els = {
  sidebar:      document.getElementById("sidebar"),
  newChat:      document.getElementById("new-chat"),
  setToken:     document.getElementById("set-token"),
  list:         document.getElementById("session-list"),
  promptHeader: document.getElementById("prompt-header"),
  transcript:   document.getElementById("transcript"),
  composer:     document.getElementById("composer"),
  prompt:       document.getElementById("prompt"),
  send:         document.getElementById("send"),
  cancel:       document.getElementById("cancel"),
  status:       document.getElementById("status"),
};

let token = localStorage.getItem(TOKEN_KEY) || "";
let activeSessionId = null;
let abortCtrl = null;

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

function renderMarkdown(el, text) {
  if (typeof marked === "undefined") {
    el.textContent = text;
    return;
  }
  el.innerHTML = marked.parse(text || "");
  el.classList.add("rendered");
}

// ─── Pinned prompt header ────────────────────────────────────────────────────

// Show text in the fixed header above the transcript.
function setPinnedPrompt(text) {
  els.promptHeader.textContent = text;
  els.promptHeader.classList.add("visible");
}

// Insert a user message bubble at the current end of the transcript (before streaming).
function appendUserBubble(text) {
  const row = document.createElement("div");
  row.className = "msg-row msg-row-user";
  const bubble = document.createElement("div");
  bubble.className = "bubble-user";
  bubble.textContent = text;
  row.appendChild(bubble);
  els.transcript.appendChild(row);
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
    els.promptHeader.textContent = activeText;
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

function appendAssistantBubble() {
  const row = document.createElement("div");
  row.className = "msg-row assistant";
  const bubble = document.createElement("div");
  bubble.className = "bubble-assistant";
  row.appendChild(bubble);
  els.transcript.appendChild(row);
  scrollBottom();
  return bubble;
}

function appendErrorBubble(text) {
  const row = document.createElement("div");
  row.className = "msg-row error";
  const bubble = document.createElement("div");
  bubble.className = "bubble-error";
  bubble.textContent = text;
  row.appendChild(bubble);
  els.transcript.appendChild(row);
  scrollBottom();
}

// buildToolBlock creates the shared DOM structure for both top-level and nested
// tool call blocks. Returns the block element; the caller appends it.
function buildToolBlock(name, args) {
  const { label, color } = toolMeta(name);
  const desc = toolDesc(name, args);
  const block = document.createElement("div");
  block.className = `tool-block border-${color}`;

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
function appendToolCall(name, args) {
  const block = buildToolBlock(name, args);
  const row = document.createElement("div");
  row.className = "tool-row";
  row.appendChild(block);
  els.transcript.appendChild(row);
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

function resolveToolCall(block, response) {
  const isError = response && typeof response === "object" && typeof response.error === "string";

  const dot = block.querySelector(".tool-dot");
  if (dot) { dot.classList.remove("pending"); dot.classList.add(isError ? "error" : "done"); }

  const slot = block.querySelector(".tool-out-slot");
  if (slot) {
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
}

// ─── API helpers ─────────────────────────────────────────────────────────────

async function apiFetch(path, opts = {}) {
  if (!token) {
    promptForToken();
    throw new Error("token required");
  }
  const headers = authHeaders(opts.headers || {});
  if (opts.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
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
    const displayName = s.title || (s.id.slice(0, 8) + "…");

    li.innerHTML = `
      <div class="session-name">${escHtml(displayName)}</div>
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

async function deleteSession(id, li) {
  try {
    await apiFetch(`/api/sessions/${id}`, { method: "DELETE" });
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
  activeSessionId = id;
  clearPinnedPrompt();
  els.transcript.innerHTML = "";
  for (const li of els.list.children) {
    li.classList.toggle("active", li.dataset.id === id);
  }

  try {
    const res = await apiFetch(`/api/sessions/${id}/messages`);
    if (!res.ok) return;
    const data = await res.json();
    const turns = data.turns || [];
    if (turns.length === 0) {
      const row = document.createElement("div");
      row.className = "msg-row";
      const b = document.createElement("div");
      b.className = "bubble-assistant";
      b.style.opacity = ".4";
      b.style.fontStyle = "italic";
      b.textContent = "No messages yet.";
      row.appendChild(b);
      els.transcript.appendChild(row);
      return;
    }
    for (const turn of turns) {
      appendUserBubble(turn.user_text);
      const bubble = appendAssistantBubble();
      renderMarkdown(bubble, turn.assistant_text);
    }
    scrollBottom();
  } catch (e) {
    console.error("failed to load session history:", e);
  }
}

async function newChat() {
  try {
    const res = await apiFetch("/api/sessions", { method: "POST" });
    const data = await res.json();
    activeSessionId = data.session_id;
    clearPinnedPrompt();
    els.transcript.innerHTML = "";
    await loadSessions();
  } catch (e) { console.error(e); }
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
  if (!prompt) return;
  if (!activeSessionId) await newChat();
  if (!activeSessionId) return;

  // Insert the user message into the transcript before streaming starts.
  appendUserBubble(prompt);
  scrollBottom();
  els.prompt.value = "";

  // Per-segment state: each burst of text between tool calls gets its own bubble.
  let segBubble = null;     // current assistant text element
  let segAcc = "";          // accumulated text for the current segment
  let segHadToken = false;  // whether we received streaming tokens this segment
  let segRenderTimer = null; // throttle handle for incremental markdown renders

  function ensureSegment() {
    if (!segBubble) {
      segBubble = appendAssistantBubble();
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

  abortCtrl = new AbortController();
  els.send.disabled = true;
  els.cancel.disabled = false;
  setStatus("thinking…");

  try {
    const res = await apiFetch(`/api/sessions/${activeSessionId}/messages`, {
      method: "POST",
      body: JSON.stringify({ prompt }),
      signal: abortCtrl.signal,
    });
    if (!res.ok) {
      const txt = await res.text();
      appendErrorBubble(`error ${res.status}: ${txt}`);
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
          setStatus("streaming…");
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
          const block = appendToolCall(data.name, data.args);
          pendingTools.push(block);
          activeOuterBlock = block;
          innerPending.length = 0;
          setStatus(`running ${data.name}…`);
          break;
        }

        case "tool_result": {
          const block = pendingTools.shift();
          if (block) resolveToolCall(block, data.response);
          activeOuterBlock = null;
          setStatus("thinking…");
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

        case "error": {
          finalizeSegment();
          appendErrorBubble(data.message || String(data));
          break;
        }

        case "done":
          break;
      }
    }
  } catch (e) {
    if (e.name === "AbortError") {
      appendErrorBubble("(cancelled)");
    } else {
      appendErrorBubble(String(e));
    }
  } finally {
    finalizeSegment();
    // Clean up any still-pending tool dots (e.g. on cancel).
    for (const b of [...pendingTools, ...innerPending]) {
      const dot = b.querySelector(".tool-dot");
      if (dot) dot.classList.remove("pending");
    }
    abortCtrl = null;
    els.send.disabled = false;
    els.cancel.disabled = true;
    setStatus("");
    loadSessions();
    scrollBottom();
  }
}

// ─── Event listeners ─────────────────────────────────────────────────────────

els.transcript.addEventListener("scroll", updatePinnedForScroll);
els.newChat.addEventListener("click", newChat);
els.setToken.addEventListener("click", promptForToken);
els.composer.addEventListener("submit", (e) => { e.preventDefault(); sendMessage(); });
els.cancel.addEventListener("click", () => { if (abortCtrl) abortCtrl.abort(); });
els.prompt.addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
    e.preventDefault();
    sendMessage();
  }
});

// ─── Init ─────────────────────────────────────────────────────────────────────

(async function init() {
  if (typeof marked !== "undefined") {
    marked.setOptions({ gfm: true, breaks: true });
  }
  if (!token) promptForToken();
  await loadSessions();
})();
