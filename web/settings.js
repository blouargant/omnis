// Settings panel — agent-toolkit configuration editor.
// Loaded after app.js. Uses the same `token` and `authHeaders` defined there.
// Exposes Settings.open() / Settings.close() / Settings.isOpen().

(function () {
  // Small inline SVG icons rendered next to each entry in the sidebar menu.
  const ICONS = {
    agent: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="7" width="18" height="13" rx="2"/><path d="M8 7V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><circle cx="9" cy="13" r="1"/><circle cx="15" cy="13" r="1"/></svg>`,
    permissions: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`,
    mcp: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>`,
    skills: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>`,
    appearance: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18c1.5 0 2-1 2-2 0-1.5 1-2 2-2h2a3 3 0 0 0 3-3 9 9 0 0 0-9-9z"/><circle cx="7.5" cy="10.5" r="1"/><circle cx="12" cy="7.5" r="1"/><circle cx="16.5" cy="10.5" r="1"/></svg>`,
    raw: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`,
  };

  // Pseudo-id used to mark the Raw YAML toggle in the sidebar menu. It is
  // not a real section: clicking it flips activeView on the current file.
  const RAW_VIEW_ID = "__raw__";

  // Server-backed YAML configs. Each id matches /api/config/{parsed,file}/<id>.
  const FILES = [
    { id: "agent",       label: "Agent",       form: "agent" },
    { id: "permissions", label: "Permissions", form: "permissions" },
    { id: "mcp",         label: "MCP",         form: "mcp" },
  ];

  // Sidebar menu entries (YAML configs + client-only views like Appearance).
  // `title` is the human-readable section name shown in the breadcrumb header.
  const APPEARANCE_ID = "appearance";
  const MENU_ITEMS = [
    { id: "skills",        label: "Skills",      title: "Skills",                    kind: "client" },
    { id: "agent",         label: "Agent",       title: "Agent Configuration",       kind: "yaml" },
    { id: "permissions",   label: "Permissions", title: "Permissions",               kind: "yaml" },
    { id: "mcp",           label: "MCP",         title: "MCP Servers",               kind: "yaml" },
    { id: APPEARANCE_ID,   label: "Appearance",  title: "Appearance",                kind: "client" },
  ];

  // Theme catalogue — id must match a [data-theme] selector in styles.css
  // (empty id = the default :root palette, no attribute set).
  // `tier` splits Principal (curated, default-quality) from Secondary
  // (well-known community themes shipped as alternatives).
  // `tone` groups Dark/Light within a tier in the picker.
  const THEME_STORAGE_KEY = "agent_toolkit_theme";
  const THEMES = [
    // Principal
    { id: "",                label: "VS Code Dark",    tier: "principal", tone: "Dark",  swatch: ["#1e1e1e", "#252526", "#0e639c", "#cccccc"] },
    { id: "github-dark",     label: "GitHub Dark",     tier: "principal", tone: "Dark",  swatch: ["#0d1117", "#161b22", "#388bfd", "#e6edf3"] },
    { id: "one-dark",        label: "One Dark",        tier: "principal", tone: "Dark",  swatch: ["#282c34", "#21252b", "#61afef", "#abb2bf"] },
    { id: "vscode-light",    label: "VS Code Light",   tier: "principal", tone: "Light", swatch: ["#ffffff", "#f3f3f3", "#0e639c", "#1e1e1e"] },
    { id: "github-light",    label: "GitHub Light",    tier: "principal", tone: "Light", swatch: ["#ffffff", "#f6f8fa", "#0969da", "#24292f"] },
    // Secondary
    { id: "dracula",         label: "Dracula",         tier: "secondary", tone: "Dark",  swatch: ["#282a36", "#21222c", "#bd93f9", "#f8f8f2"] },
    { id: "nord",            label: "Nord",            tier: "secondary", tone: "Dark",  swatch: ["#2e3440", "#3b4252", "#5e81ac", "#d8dee9"] },
    { id: "tokyo-night",     label: "Tokyo Night",     tier: "secondary", tone: "Dark",  swatch: ["#1a1b26", "#1f2335", "#7aa2f7", "#c0caf5"] },
    { id: "solarized-dark",  label: "Solarized Dark",  tier: "secondary", tone: "Dark",  swatch: ["#002b36", "#073642", "#268bd2", "#93a1a1"] },
    { id: "monokai",         label: "Monokai",         tier: "secondary", tone: "Dark",  swatch: ["#272822", "#1e1f1c", "#66d9ef", "#f8f8f2"] },
    { id: "gruvbox-dark",    label: "Gruvbox Dark",    tier: "secondary", tone: "Dark",  swatch: ["#282828", "#1d2021", "#fe8019", "#ebdbb2"] },
    { id: "solarized-light", label: "Solarized Light", tier: "secondary", tone: "Light", swatch: ["#fdf6e3", "#eee8d5", "#268bd2", "#586e75"] },
  ];
  const TIERS = [
    { id: "principal", label: "Principal themes" },
    { id: "secondary", label: "Secondary themes" },
  ];

  function getActiveTheme() {
    return localStorage.getItem(THEME_STORAGE_KEY) || "";
  }
  function applyTheme(id, opts) {
    const root = document.documentElement;
    if (id) root.setAttribute("data-theme", id);
    else root.removeAttribute("data-theme");
    localStorage.setItem(THEME_STORAGE_KEY, id);
    // Persist to the server so the choice survives restarts. Skipped when
    // applying a value that just came from the server.
    if (!opts || opts.persist !== false) {
      fetch("/api/preferences", {
        method: "PUT",
        headers: authHeaders({ "Content-Type": "application/json" }),
        body: JSON.stringify({ theme: id }),
      }).catch(() => { /* offline / unauthenticated — local cache wins */ });
    }
  }

  // Pull the server-side theme once on boot and reconcile with the local
  // cache (which the inline <head> script applied synchronously).
  async function syncThemeFromServer() {
    try {
      const r = await fetch("/api/preferences", { headers: authHeaders() });
      if (!r.ok) return;
      const p = await r.json();
      const serverTheme = (p && typeof p.theme === "string") ? p.theme : "";
      if (serverTheme !== getActiveTheme()) {
        applyTheme(serverTheme, { persist: false });
      }
    } catch (_) { /* ignore */ }
  }

  const RESTART_FLAG = "agent_toolkit_needs_restart";
  const BANNER_DISMISS_FLAG = "agent_toolkit_restart_dismissed";
  const TOOL_GROUPS = ["fs", "mcp", "skills", "softskills", "calc", "ddg", "serpapi", "web"];
  const TOOL_DESCRIPTIONS = {
    fs:         "File-system tools: read, write, grep, glob, revert files, and run bash commands.",
    mcp:        "MCP (Model Context Protocol) tools: connect to external MCP servers defined in mcp_config.yaml.",
    skills:     "Skill tools: load and list skill playbooks from the skills/ directory.",
    softskills: "Soft-skill tools: load and list curator-distilled procedures from the softskills/ directory.",
    calc:       "Calculator: evaluate mathematical expressions (arithmetic, sqrt, trig, log, pow…).",
    ddg:        "Web search: search the web via DuckDuckGo (no API key required).",
    serpapi:    "Web search: search the web via SerpAPI (Google). Requires serpapi_key in globals. Cannot be used together with ddg.",
    web:        "Web tools: fetch a web page as Markdown (web_fetch) or convert an HTML string to Markdown (html_to_markdown).",
  };
  // Tools that are mutually exclusive: selecting one auto-deselects the other.
  const TOOL_MUTEX = { ddg: "serpapi", serpapi: "ddg" };

  const AGENT_SUBTABS = [
    { id: "globals", label: "Globals" },
    { id: "models",  label: "Models"  },
    { id: "agents",  label: "Agents"  },
  ];

  const state = {
    activeFile: "skills",
    activeView: "form", // 'form' | 'raw'
    activeAgentSubtab: "globals", // only used when activeFile === 'agent'
    raw: {}, // id → { content, mtime, dirty, value }
    parsed: {}, // id → { data, mtime, dirty, value }
    open: false,
    skills: { editing: null, browsingRemote: null, viewingRemote: null }, // skills panel state
  };

  // ─── DOM refs ──────────────────────────────────────────────────────────
  let panelEl, tabsEl, viewToggleEl, bodyEl, footerEl, statusEl;
  let sidebarMenuEl, sidebarMenuListEl; // in-sidebar settings categories

  function escHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function authHeaders(extra = {}) {
    const t = localStorage.getItem("agent_toolkit_token") || "";
    return { ...extra, "Authorization": `Bearer ${t}` };
  }

  // ─── Banner ────────────────────────────────────────────────────────────
  function ensureBanner() {
    let b = document.getElementById("restart-banner");
    if (b) return b;
    b = document.createElement("div");
    b.id = "restart-banner";
    b.hidden = true;
    b.innerHTML = `
      <span class="restart-banner-text">
        Configuration changed — restart the server to apply.
      </span>
      <button type="button" id="restart-banner-btn">Restart server</button>
      <button type="button" id="restart-banner-dismiss" title="Dismiss">×</button>
    `;
    const main = document.getElementById("chat");
    main.insertBefore(b, main.firstChild);
    b.querySelector("#restart-banner-btn").addEventListener("click", () => doRestart());
    b.querySelector("#restart-banner-dismiss").addEventListener("click", () => {
      // Persistent dismissal until the next successful save re-arms the banner.
      localStorage.setItem(BANNER_DISMISS_FLAG, "1");
      b.hidden = true;
    });
    return b;
  }

  function showBanner() {
    localStorage.setItem(RESTART_FLAG, "1");
    // Re-arm visibility: a fresh save invalidates any earlier dismissal.
    localStorage.removeItem(BANNER_DISMISS_FLAG);
    const b = ensureBanner();
    b.hidden = false;
  }

  function refreshBannerVisibility() {
    if (localStorage.getItem(RESTART_FLAG) !== "1") return;
    if (localStorage.getItem(BANNER_DISMISS_FLAG) === "1") return;
    ensureBanner().hidden = false;
  }

  function showRestartingOverlay(msg) {
    let el = document.getElementById("restart-overlay");
    if (!el) {
      el = document.createElement("div");
      el.id = "restart-overlay";
      el.innerHTML = `
        <div id="restart-overlay-spinner"></div>
        <div id="restart-overlay-msg"></div>
      `;
      document.body.appendChild(el);
    }
    el.querySelector("#restart-overlay-msg").textContent = msg || "Server is restarting…";
    el.hidden = false;
  }

  function hideRestartingOverlay() {
    const el = document.getElementById("restart-overlay");
    if (el) el.hidden = true;
  }

  async function doRestart() {
    if (!await appConfirm("Restart the agent-toolkit server now? Active streams will be interrupted.")) return;
    setStatus("Restarting…");
    showRestartingOverlay("Server is restarting…\nThe page will reload automatically.");
    try {
      const r = await fetch("/api/server/restart", { method: "POST", headers: authHeaders() });
      if (!r.ok) {
        const j = await r.json().catch(() => ({}));
        throw new Error(j.error || `HTTP ${r.status}`);
      }
      localStorage.removeItem(RESTART_FLAG);
      localStorage.removeItem(BANNER_DISMISS_FLAG);
      const b = document.getElementById("restart-banner");
      if (b) b.hidden = true;
      setStatus("Server restarting — page will reload shortly…");
      showRestartingOverlay("Server is restarting…\nThe page will reload automatically.");
      // Poll /api/health until reachable, then reload.
      const start = Date.now();
      const tick = async () => {
        try {
          const h = await fetch("/api/health");
          if (h.ok) { window.location.reload(); return; }
        } catch (_) { /* not yet up */ }
        if (Date.now() - start > 30000) {
          hideRestartingOverlay();
          setStatus("Server did not come back within 30s. Reload manually.");
          return;
        }
        setTimeout(tick, 750);
      };
      setTimeout(tick, 1000);
    } catch (e) {
      hideRestartingOverlay();
      setStatus("Restart failed: " + e.message);
    }
  }

  // ─── Panel scaffolding ─────────────────────────────────────────────────
  function ensurePanel() {
    if (panelEl) return panelEl;
    panelEl = document.createElement("div");
    panelEl.id = "settings-panel";
    panelEl.hidden = true;
    panelEl.innerHTML = `
      <header class="settings-header">
        <nav class="settings-breadcrumb" aria-label="Breadcrumb">
          <span class="settings-breadcrumb-root">Settings</span>
          <svg class="settings-breadcrumb-sep" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="9 18 15 12 9 6"/></svg>
          <span class="settings-breadcrumb-current"></span>
        </nav>
        <div class="settings-tabs" role="tablist"></div>
      </header>
      <div class="settings-body">
        <div class="settings-body-toolbar">
          <div class="settings-content-inner">
            <div class="settings-view-toggle" role="tablist">
              <button type="button" data-view="form" class="active">Form</button>
              <button type="button" data-view="raw">Raw YAML</button>
            </div>
          </div>
        </div>
        <div class="settings-body-content"></div>
      </div>
      <footer class="settings-footer">
        <span class="settings-status"></span>
        <button type="button" class="btn-discard">Discard</button>
        <button type="button" class="btn-save">Save</button>
      </footer>
    `;
    const main = document.getElementById("chat");
    main.appendChild(panelEl);

    tabsEl = panelEl.querySelector(".settings-tabs");
    viewToggleEl = panelEl.querySelector(".settings-view-toggle");
    bodyEl = panelEl.querySelector(".settings-body-content");
    footerEl = panelEl.querySelector(".settings-footer");
    statusEl = panelEl.querySelector(".settings-status");

    for (const f of FILES) {
      const b = document.createElement("button");
      b.type = "button";
      b.dataset.file = f.id;
      b.textContent = f.label;
      b.addEventListener("click", () => setActiveFile(f.id));
      tabsEl.appendChild(b);
    }
    buildSidebarMenu();
    viewToggleEl.querySelectorAll("button").forEach(b => {
      b.addEventListener("click", () => setActiveView(b.dataset.view));
    });

    panelEl.querySelector(".btn-save").addEventListener("click", saveActive);
    panelEl.querySelector(".btn-discard").addEventListener("click", discardActive);

    return panelEl;
  }

  function setStatus(s, kind) {
    if (!statusEl) return;
    statusEl.textContent = s || "";
    statusEl.className = "settings-status" + (kind ? " " + kind : "");
  }

  // Builds the in-sidebar category list (Agent / Permissions / MCP / Appearance).
  // The list is rendered once into #settings-menu-list and stays in the DOM;
  // open()/close() toggle the parent #settings-menu's visibility.
  function buildSidebarMenu() {
    sidebarMenuEl = document.getElementById("settings-menu");
    sidebarMenuListEl = document.getElementById("settings-menu-list");
    if (!sidebarMenuListEl || sidebarMenuListEl.children.length) return;
    for (const m of MENU_ITEMS) {
      const li = document.createElement("li");
      li.dataset.file = m.id;
      li.innerHTML = `${ICONS[m.id] || ""}<span>${escHtml(m.label)}</span>`;
      li.addEventListener("click", () => setActiveFile(m.id));
      sidebarMenuListEl.appendChild(li);
    }
    // Raw YAML is appended last so new section entries inserted into
    // MENU_ITEMS always render above it.
    const raw = document.createElement("li");
    raw.dataset.file = RAW_VIEW_ID;
    raw.className = "settings-menu-raw";
    raw.innerHTML = `${ICONS.raw}<span>Raw YAML</span>`;
    raw.addEventListener("click", () => {
      if (raw.classList.contains("disabled")) return;
      toggleRawView();
    });
    sidebarMenuListEl.appendChild(raw);
  }

  // Toggle between form and raw view for the currently active YAML file.
  // No-op for client-only sections (e.g. Appearance) — they have no YAML.
  async function toggleRawView() {
    if (isClientOnly(state.activeFile)) return;
    const next = state.activeView === "raw" ? "form" : "raw";
    await setActiveView(next);
  }

  function syncActiveHighlight(id) {
    tabsEl?.querySelectorAll("button").forEach(b => {
      b.classList.toggle("active", b.dataset.file === id);
    });
    sidebarMenuListEl?.querySelectorAll("li").forEach(li => {
      const f = li.dataset.file;
      if (f === RAW_VIEW_ID) {
        li.classList.toggle("active", state.activeView === "raw" && !isClientOnly(id));
        li.classList.toggle("disabled", isClientOnly(id));
      } else {
        li.classList.toggle("active", f === id);
      }
    });
    updateBreadcrumb(id);
  }

  function updateBreadcrumb(id) {
    if (!panelEl) return;
    const el = panelEl.querySelector(".settings-breadcrumb-current");
    if (!el) return;
    const item = MENU_ITEMS.find(m => m.id === id);
    const base = item ? item.title : "";
    el.textContent = (state.activeView === "raw" && !isClientOnly(id))
      ? `${base} › Raw YAML`
      : base;
  }

  async function setActiveFile(id) {
    if (state.activeFile !== id && hasUnsavedActive() &&
        !await appConfirm("Discard unsaved changes in the current tab?")) {
      return;
    }
    state.activeFile = id;
    // Switching sections always returns to the form view; raw is opt-in
    // per visit via the sidebar Raw YAML entry.
    state.activeView = "form";
    // Clicking "Skills" always resets sub-navigation back to the root list.
    if (id === "skills") {
      state.skills.editing = null;
      state.skills.browsingRemote = null;
      state.skills.viewingRemote = null;
    }
    syncActiveHighlight(id);
    renderBody();
  }

  async function setActiveView(v) {
    if (state.activeView === v) return;
    if (hasUnsavedActive() && !await appConfirm("Discard unsaved changes in this view?")) return;
    state.activeView = v;
    viewToggleEl?.querySelectorAll("button").forEach(b => {
      b.classList.toggle("active", b.dataset.view === v);
    });
    syncActiveHighlight(state.activeFile);
    renderBody();
  }

  function hasUnsavedActive() {
    if (state.activeFile === APPEARANCE_ID) return false;
    if (state.activeView === "raw") {
      const r = state.raw[state.activeFile];
      return r && r.dirty;
    }
    const p = state.parsed[state.activeFile];
    return p && p.dirty;
  }

  // True for menu entries with no server-side YAML — these hide the
  // Form/Raw toggle and the Save/Discard footer.
  function isClientOnly(id) { return id === APPEARANCE_ID || id === "skills"; }

  function applyClientOnlyChrome() {
    const clientOnly = isClientOnly(state.activeFile);
    panelEl.classList.toggle("settings-panel--client-only", clientOnly);
  }

  // ─── Loading ───────────────────────────────────────────────────────────
  async function loadRaw(id) {
    const r = await fetch(`/api/config/file/${id}`, { headers: authHeaders() });
    if (!r.ok) throw new Error(await errText(r));
    const j = await r.json();
    state.raw[id] = { content: j.content || "", mtime: j.mtime, dirty: false, value: j.content || "" };
  }

  async function loadParsed(id) {
    const r = await fetch(`/api/config/parsed/${id}`, { headers: authHeaders() });
    if (!r.ok) throw new Error(await errText(r));
    const j = await r.json();
    const data = j.data == null ? defaultDataFor(id) : j.data;
    state.parsed[id] = { data, mtime: j.mtime, dirty: false, value: deepClone(data) };
  }

  async function errText(r) {
    try { const j = await r.json(); return j.error || `HTTP ${r.status}`; }
    catch { return `HTTP ${r.status}`; }
  }

  function defaultDataFor(id) {
    if (id === "agent") return { models: {}, agents: [] };
    if (id === "permissions") return { always_deny: [], always_allow: [], ask_user: [] };
    if (id === "mcp") return { servers: [] };
    return {};
  }

  function deepClone(x) { return JSON.parse(JSON.stringify(x ?? null)); }

  // ─── Rendering ─────────────────────────────────────────────────────────
  async function renderBody() {
    bodyEl.innerHTML = `<p class="settings-loading">Loading…</p>`;
    setStatus("");
    applyClientOnlyChrome();
    const id = state.activeFile;
    if (isClientOnly(id)) {
      if (id === APPEARANCE_ID) renderAppearance();
      else if (id === "skills") renderSkills();
      return;
    }
    try {
      if (state.activeView === "raw") {
        if (!state.raw[id]) await loadRaw(id);
        renderRaw(id);
      } else {
        if (!state.parsed[id]) await loadParsed(id);
        renderForm(id);
      }
    } catch (e) {
      bodyEl.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
    }
  }

  // ─── Appearance / theme picker ─────────────────────────────────────────
  function renderAppearance() {
    const active = getActiveTheme();

    const cardHTML = t => `
      <button type="button" class="theme-card ${active === t.id ? "active" : ""}" data-theme-id="${escHtml(t.id)}">
        <span class="theme-card-preview">
          ${t.swatch.map(c => `<span class="theme-swatch" style="background:${c}"></span>`).join("")}
        </span>
        <span class="theme-card-label">${escHtml(t.label)}</span>
        <span class="theme-card-check" aria-hidden="true">✓</span>
      </button>
    `;

    // Render: tier section header → per-tone subheader → grid of cards.
    const sections = TIERS.map(tier => {
      const inTier = THEMES.filter(t => t.tier === tier.id);
      if (!inTier.length) return "";
      const byTone = {};
      for (const t of inTier) (byTone[t.tone] = byTone[t.tone] || []).push(t);
      return `
        <section class="form-section">
          <h3>${escHtml(tier.label)}</h3>
          ${Object.entries(byTone).map(([tone, list]) => `
            <div class="theme-group-label">${escHtml(tone)}</div>
            <div class="theme-grid">${list.map(cardHTML).join("")}</div>
          `).join("")}
        </section>
      `;
    }).join("");

    bodyEl.innerHTML = `
      <div class="settings-form">
        <p class="settings-hint" style="margin:0;">
          Pick a color palette. Applied immediately and saved on the server.
        </p>
        ${sections}
      </div>
    `;

    bodyEl.querySelectorAll(".theme-card").forEach(btn => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.themeId;
        applyTheme(id);
        bodyEl.querySelectorAll(".theme-card").forEach(b => {
          b.classList.toggle("active", b.dataset.themeId === id);
        });
      });
    });
  }

  function renderRaw(id) {
    const s = state.raw[id];
    bodyEl.innerHTML = `
      <div class="settings-raw">
        <div class="raw-meta">
          <span>Last modified: ${s.mtime ? new Date(s.mtime).toLocaleString() : "—"}</span>
        </div>
        <textarea class="raw-editor" spellcheck="false" autocomplete="off"></textarea>
      </div>
    `;
    const ta = bodyEl.querySelector(".raw-editor");
    ta.value = s.value;
    ta.addEventListener("input", () => {
      s.value = ta.value;
      s.dirty = ta.value !== s.content;
      updateFooter();
    });
    updateFooter();
  }

  function updateFooter() {
    const dirty = hasUnsavedActive();
    footerEl.querySelector(".btn-save").disabled = !dirty;
    footerEl.querySelector(".btn-discard").disabled = !dirty;
  }

  // ─── Form rendering (per file) ─────────────────────────────────────────
  function renderForm(id) {
    if (id === "agent") return renderAgentForm();
    if (id === "permissions") return renderPermissionsForm();
    if (id === "mcp") return renderMCPForm();
  }

  function markFormDirty(id) {
    state.parsed[id].dirty = JSON.stringify(state.parsed[id].value) !== JSON.stringify(state.parsed[id].data);
    updateFooter();
  }

  // ── agent.yaml form ──
  function renderAgentForm() {
    const id = "agent";
    const d = state.parsed[id].value;
    if (!d.models || typeof d.models !== "object") d.models = {};
    if (!Array.isArray(d.agents)) d.agents = [];

    const sub = state.activeAgentSubtab;
    bodyEl.innerHTML = `
      <div class="settings-form">
        <div class="settings-subtabs" role="tablist">
          ${AGENT_SUBTABS.map(t => `
            <button type="button" data-subtab="${t.id}" class="${sub === t.id ? "active" : ""}">${escHtml(t.label)}</button>
          `).join("")}
        </div>
        <div class="settings-subtab-body"></div>
      </div>
    `;

    bodyEl.querySelectorAll(".settings-subtabs button").forEach(b => {
      b.addEventListener("click", () => {
        if (state.activeAgentSubtab === b.dataset.subtab) return;
        state.activeAgentSubtab = b.dataset.subtab;
        renderAgentForm();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "globals") {
      host.innerHTML = `
        <section class="form-section">
          <h3>Globals</h3>
          <div class="form-card" style="margin-bottom:0">
            <div class="form-grid" id="agent-globals"></div>
          </div>
        </section>
      `;
      renderAgentGlobals(d);
    } else if (sub === "models") {
      host.innerHTML = `
        <section class="form-section">
          <h3>Models <button type="button" class="add-btn" id="add-model">+ Add model</button></h3>
          <div id="agent-models"></div>
        </section>
      `;
      bodyEl.querySelector("#add-model").addEventListener("click", async () => {
        let name = await appPrompt("New model name:");
        if (!name) return;
        name = name.trim().toLowerCase();
        if (!name || d.models[name]) return;
        d.models[name] = { provider: "", model: "", base_url: "", api_key: "" };
        markFormDirty(id);
        renderAgentModels(d);
      });
      renderAgentModels(d);
    } else {
      host.innerHTML = `
        <section class="form-section">
          <h3>Agents <button type="button" class="add-btn" id="add-agent">+ Add agent</button></h3>
          <div id="agent-agents"></div>
        </section>
      `;
      bodyEl.querySelector("#add-agent").addEventListener("click", () => {
        d.agents.push({ name: "new-agent", enabled: true, mailbox: false, tools: [] });
        markFormDirty(id);
        renderAgentAgents(d);
      });
      renderAgentAgents(d);
    }
    updateFooter();
  }

  function renderAgentGlobals(d) {
    const el = bodyEl.querySelector("#agent-globals");
    const fields = [
      ["skills_dir", "string"], ["softskills_dir", "string"], ["app_name", "string"],
      ["token_optimization", "bool"], ["bash_output_filters_dir", "string"],
      ["bash_timeout_seconds", "number"],
      ["mcp_config_path", "string"], ["permissions_config_path", "string"],
      ["serpapi_key", "string"],
    ];
    el.innerHTML = "";
    for (const [key, kind] of fields) {
      const row = field(key, d[key], kind, v => { d[key] = v; markFormDirty("agent"); });
      el.appendChild(row);
    }
  }

  function renderAgentModels(d) {
    const el = bodyEl.querySelector("#agent-models");
    el.innerHTML = "";
    const names = Object.keys(d.models);
    if (!names.length) { el.innerHTML = `<p class="empty">No models defined.</p>`; return; }
    for (const name of names) {
      const m = d.models[name] || {};
      const row = document.createElement("div");
      row.className = "form-card";
      row.innerHTML = `
        <div class="form-card-header">
          <strong>${escHtml(name)}</strong>
          <button type="button" class="del-btn">Remove</button>
        </div>
        <div class="form-grid"></div>
      `;
      const grid = row.querySelector(".form-grid");
      const onChange = () => markFormDirty("agent");

      // provider field — plain text, but changing it clears cached model list
      grid.appendChild(field("provider", m.provider, "string", v => { m.provider = v; onChange(); }));

      // model field — combobox: free-text input + datalist populated from provider API
      grid.appendChild(modelComboField(m, onChange));

      const remainingFields = [
        ["base_url", "string"], ["api_key", "string"],
        ["context_length", "number"],
        ["input_token_price_per_million", "number"],
        ["output_token_price_per_million", "number"],
        ["cached_input_token_price_per_million", "number"],
        ["cache_creation_token_price_per_million", "number"],
      ];
      for (const [k, kind] of remainingFields) {
        grid.appendChild(field(k, m[k], kind, v => { m[k] = v; onChange(); }));
      }
      row.querySelector(".del-btn").addEventListener("click", async () => {
        if (!await appConfirm(`Remove model "${name}"?`)) return;
        delete d.models[name];
        markFormDirty("agent");
        renderAgentModels(d);
      });
      el.appendChild(row);
    }
  }

  // modelComboField builds a form row for the "model" field: a free-text input
  // with a custom dropdown panel populated from the provider's model list API.
  // The panel shows ALL fetched models (filtered by what's typed); clicking one
  // sets the value. The ⟳ button fetches and opens the panel automatically.
  function modelComboField(m, onChange) {
    const row = document.createElement("div");
    row.className = "form-row form-row-combo";

    const span = document.createElement("span");
    span.textContent = "model";

    const wrap = document.createElement("div");
    wrap.className = "combo-wrap";

    const input = document.createElement("input");
    input.type = "text";
    input.value = m.model == null ? "" : String(m.model);
    input.setAttribute("autocomplete", "off");
    input.setAttribute("spellcheck", "false");

    const panel = document.createElement("div");
    panel.className = "combo-panel";
    panel.hidden = true;

    const list = document.createElement("ul");
    list.className = "combo-list";
    panel.appendChild(list);

    // All fetched model objects [{id, display_name}]
    let allModels = [];

    function renderList(filter) {
      const q = (filter || "").toLowerCase();
      list.innerHTML = "";
      const shown = q ? allModels.filter(mdl =>
        mdl.id.toLowerCase().includes(q) ||
        (mdl.display_name || "").toLowerCase().includes(q)
      ) : allModels;
      if (!shown.length) {
        const li = document.createElement("li");
        li.className = "combo-empty";
        li.textContent = q ? "No match" : "No models loaded";
        list.appendChild(li);
        return;
      }
      for (const mdl of shown) {
        const li = document.createElement("li");
        li.dataset.value = mdl.id;
        if (mdl.display_name && mdl.display_name !== mdl.id) {
          li.innerHTML = `<span class="combo-item-id">${escHtml(mdl.id)}</span><span class="combo-item-name">${escHtml(mdl.display_name)}</span>`;
        } else {
          li.textContent = mdl.id;
        }
        li.addEventListener("mousedown", e => {
          e.preventDefault(); // keep input focus
          input.value = mdl.id;
          m.model = mdl.id;
          onChange();
          panel.hidden = true;
        });
        list.appendChild(li);
      }
    }

    function openPanel() { panel.hidden = false; renderList(input.value); }
    function closePanel() { panel.hidden = true; }

    input.addEventListener("input", () => {
      m.model = input.value;
      onChange();
      if (!panel.hidden) renderList(input.value);
    });
    input.addEventListener("focus", () => {
      if (allModels.length) { panel.hidden = false; renderList(""); }
    });
    input.addEventListener("blur", () => { setTimeout(closePanel, 150); });

    const fetchBtn = document.createElement("button");
    fetchBtn.type = "button";
    fetchBtn.className = "combo-fetch-btn";
    fetchBtn.title = "Load models from provider";
    fetchBtn.textContent = "⟳";

    fetchBtn.addEventListener("click", async () => {
      const provider = (m.provider || "").trim();
      if (!provider) { setStatus("Set a provider first."); return; }
      fetchBtn.disabled = true;
      fetchBtn.textContent = "…";
      setStatus("Fetching model list…");
      try {
        const params = new URLSearchParams({ provider });
        if (m.api_key) params.set("api_key", m.api_key);
        if (m.base_url) params.set("base_url", m.base_url);
        const r = await fetch(`/api/providers/models?${params}`, { headers: authHeaders() });
        const j = await r.json();
        if (!r.ok) throw new Error(j.error || r.statusText);
        allModels = j.models || [];
        // Show all models unfiltered; typing will narrow the list.
        panel.hidden = false;
        renderList("");
        input.focus();
        setStatus(`Loaded ${allModels.length} model(s) from ${provider}.`);
      } catch (e) {
        // Show error inside the panel so it's visible even if the status bar is offscreen.
        allModels = [];
        list.innerHTML = "";
        const li = document.createElement("li");
        li.className = "combo-empty combo-error";
        li.textContent = e.message;
        list.appendChild(li);
        panel.hidden = false;
        setStatus("Failed to load models: " + e.message);
      } finally {
        fetchBtn.disabled = false;
        fetchBtn.textContent = "⟳";
      }
    });

    wrap.appendChild(input);
    wrap.appendChild(fetchBtn);
    wrap.appendChild(panel);
    row.appendChild(span);
    row.appendChild(wrap);
    return row;
  }

  function renderAgentAgents(d) {
    const el = bodyEl.querySelector("#agent-agents");
    el.innerHTML = "";
    if (!d.agents.length) { el.innerHTML = `<p class="empty">No agents defined.</p>`; return; }
    const modelOptions = Object.keys(d.models || {});
    const leaderIsFirst = d.agents[0]?.name === "leader";

    d.agents.forEach((a, idx) => {
      const isLeader = a.name === "leader";
      // Leader is pinned at top; no agent may move above it.
      const upDisabled   = idx === 0 || (leaderIsFirst && idx === 1);
      const downDisabled = idx === d.agents.length - 1 || isLeader;

      const row = document.createElement("div");
      row.className = "form-card";
      row.innerHTML = `
        <div class="form-card-header">
          <strong>${escHtml(a.name || "(unnamed)")}</strong>
          <span class="card-actions">
            ${isLeader ? "" : `<button type="button" class="up-btn" title="Move up" ${upDisabled ? "disabled" : ""}>▲</button>`}
            ${isLeader ? "" : `<button type="button" class="down-btn" title="Move down" ${downDisabled ? "disabled" : ""}>▼</button>`}
            ${isLeader ? "" : `<button type="button" class="del-btn">Remove</button>`}
          </span>
        </div>
        <div class="form-grid"></div>
        <label class="form-row form-row-textarea">
          <span>instruction</span>
          <textarea rows="3"></textarea>
        </label>
      `;
      const grid = row.querySelector(".form-grid");
      const onChange = () => markFormDirty("agent");

      const nameRow = field("name", a.name, "string", v => { a.name = v; renderAgentAgents(d); });
      if (isLeader) nameRow.querySelector("input").disabled = true;
      grid.appendChild(nameRow);
      grid.appendChild(selectField("model_ref", a.model_ref || "", modelOptions, v => { a.model_ref = v; onChange(); }));

      // Leader is always enabled — show the checkbox but lock it.
      const enabledRow = field("enabled", isLeader ? true : a.enabled, "bool", v => { a.enabled = v; onChange(); });
      if (isLeader) enabledRow.querySelector("input").disabled = true;
      grid.appendChild(enabledRow);

      // Mailbox defaults to true for leader when not explicitly set.
      grid.appendChild(field("mailbox", (isLeader && a.mailbox == null) ? true : a.mailbox, "bool", v => { a.mailbox = v; onChange(); }));

      grid.appendChild(field("allow_file_attachments", a.allow_file_attachments || false, "bool", v => { a.allow_file_attachments = v; onChange(); }));

      // Tools default to all available for leader when not explicitly set.
      const effectiveTools = (isLeader && (!a.tools || !a.tools.length)) ? [...TOOL_GROUPS] : a.tools;
      grid.appendChild(toolsField("tools", effectiveTools, v => { a.tools = v; onChange(); }, { serpApiKeySet: !!d.serpapi_key }));

      // skills_dir / softskills_dir show "(default)" placeholder for leader when absent.
      const skillsRow = field("skills_dir", a.skills_dir, "string", v => { a.skills_dir = v; onChange(); });
      if (isLeader && !a.skills_dir) skillsRow.querySelector("input").placeholder = "(default)";
      grid.appendChild(skillsRow);

      const softskillsRow = field("softskills_dir", a.softskills_dir, "string", v => { a.softskills_dir = v; onChange(); });
      if (isLeader && !a.softskills_dir) softskillsRow.querySelector("input").placeholder = "(default)";
      grid.appendChild(softskillsRow);

      grid.appendChild(field("mcp_config_path", a.mcp_config_path, "string", v => { a.mcp_config_path = v; onChange(); }));
      grid.appendChild(field("permissions_config_path", a.permissions_config_path, "string", v => { a.permissions_config_path = v; onChange(); }));
      grid.appendChild(field("description", a.description, "string", v => { a.description = v; onChange(); }));

      const ta = row.querySelector("textarea");
      ta.value = a.instruction || "";
      ta.addEventListener("input", () => { a.instruction = ta.value; onChange(); });

      // Skills block — collapsible, collapsed by default.
      if (a.skills_dir !== undefined) {
        const skillsSection = document.createElement("div");
        skillsSection.className = "skills-agent-section";

        const toggle = document.createElement("button");
        toggle.type = "button";
        toggle.className = "skills-agent-toggle";
        toggle.setAttribute("aria-expanded", "false");
        toggle.innerHTML = `<i class="skills-agent-chevron">▶</i> Skills`;

        const skillsBody = document.createElement("div");
        skillsBody.className = "skills-agent-body";
        skillsBody.hidden = true;

        let loaded = false;
        toggle.addEventListener("click", () => {
          const expanded = toggle.getAttribute("aria-expanded") === "true";
          toggle.setAttribute("aria-expanded", String(!expanded));
          skillsBody.hidden = expanded;
          if (!loaded) {
            loaded = true;
            populateAgentSkillBlock(skillsBody, a.name);
          }
        });

        skillsSection.appendChild(toggle);
        skillsSection.appendChild(skillsBody);
        row.appendChild(skillsSection);
      }

      row.querySelector(".up-btn")?.addEventListener("click", () => {
        if (upDisabled) return;
        [d.agents[idx - 1], d.agents[idx]] = [d.agents[idx], d.agents[idx - 1]];
        markFormDirty("agent"); renderAgentAgents(d);
      });
      row.querySelector(".down-btn")?.addEventListener("click", () => {
        if (downDisabled) return;
        [d.agents[idx + 1], d.agents[idx]] = [d.agents[idx], d.agents[idx + 1]];
        markFormDirty("agent"); renderAgentAgents(d);
      });
      if (!isLeader) {
        row.querySelector(".del-btn").addEventListener("click", async () => {
          if (!await appConfirm(`Remove agent "${a.name}"?`)) return;
          d.agents.splice(idx, 1);
          markFormDirty("agent"); renderAgentAgents(d);
        });
      }
      el.appendChild(row);
    });
  }

  // ── permissions.yaml form ──
  function renderPermissionsForm() {
    const id = "permissions";
    const d = state.parsed[id].value;
    for (const k of ["always_deny", "always_allow", "ask_user"]) {
      if (!Array.isArray(d[k])) d[k] = [];
    }
    bodyEl.innerHTML = `
      <div class="settings-form">
        ${["always_deny", "always_allow", "ask_user"].map(k => `
          <section class="form-section">
            <h3>${k} <button type="button" class="add-btn" data-list="${k}">+ Add rule</button></h3>
            <div class="form-card" style="margin-bottom:0">
              <div class="rule-list" data-list="${k}"></div>
            </div>
          </section>
        `).join("")}
      </div>
    `;
    bodyEl.querySelectorAll(".add-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        const k = btn.dataset.list;
        d[k].push("");
        markFormDirty(id);
        renderPermRule(d, k);
      });
    });
    for (const k of ["always_deny", "always_allow", "ask_user"]) renderPermRule(d, k);
    updateFooter();
  }

  function renderPermRule(d, key) {
    const el = bodyEl.querySelector(`.rule-list[data-list="${key}"]`);
    el.innerHTML = "";
    if (!d[key].length) { el.innerHTML = `<p class="empty">No rules.</p>`; return; }
    d[key].forEach((rule, idx) => {
      const isObj = rule && typeof rule === "object";
      const row = document.createElement("div");
      row.className = "rule-row";
      row.innerHTML = `
        <select class="rule-kind">
          <option value="string" ${!isObj ? "selected" : ""}>pattern</option>
          <option value="object" ${isObj ? "selected" : ""}>pattern + reason</option>
        </select>
        <input type="text" class="rule-pattern" placeholder="regex pattern" />
        <input type="text" class="rule-reason" placeholder="reason (optional)" />
        <button type="button" class="del-btn">Remove</button>
      `;
      const kindSel = row.querySelector(".rule-kind");
      const patIn = row.querySelector(".rule-pattern");
      const reaIn = row.querySelector(".rule-reason");
      patIn.value = isObj ? (rule.pattern || "") : String(rule || "");
      reaIn.value = isObj ? (rule.reason || "") : "";
      reaIn.style.display = isObj ? "" : "none";

      const commit = () => {
        if (kindSel.value === "object") {
          d[key][idx] = { pattern: patIn.value, reason: reaIn.value };
        } else {
          d[key][idx] = patIn.value;
        }
        markFormDirty("permissions");
      };
      kindSel.addEventListener("change", () => {
        reaIn.style.display = kindSel.value === "object" ? "" : "none";
        commit();
      });
      patIn.addEventListener("input", commit);
      reaIn.addEventListener("input", commit);
      row.querySelector(".del-btn").addEventListener("click", () => {
        d[key].splice(idx, 1);
        markFormDirty("permissions");
        renderPermRule(d, key);
      });
      el.appendChild(row);
    });
  }

  // ── mcp_config.yaml form ──
  function renderMCPForm() {
    const id = "mcp";
    const d = state.parsed[id].value;
    if (!Array.isArray(d.servers)) d.servers = [];
    bodyEl.innerHTML = `
      <div class="settings-form">
        <section class="form-section">
          <h3>MCP Servers <button type="button" class="add-btn" id="add-mcp">+ Add server</button></h3>
          <div id="mcp-list"></div>
        </section>
      </div>
    `;
    bodyEl.querySelector("#add-mcp").addEventListener("click", () => {
      d.servers.push({ name: "new-server", command: "", args: [], env: {} });
      markFormDirty(id);
      renderMCPList(d);
    });
    renderMCPList(d);
    updateFooter();
  }

  function renderMCPList(d) {
    const el = bodyEl.querySelector("#mcp-list");
    el.innerHTML = "";
    if (!d.servers.length) { el.innerHTML = `<p class="empty">No MCP servers configured.</p>`; return; }
    d.servers.forEach((s, idx) => {
      if (!Array.isArray(s.args)) s.args = [];
      if (!s.env || typeof s.env !== "object") s.env = {};
      const row = document.createElement("div");
      row.className = "form-card";
      row.innerHTML = `
        <div class="form-card-header">
          <strong>${escHtml(s.name || "(unnamed)")}</strong>
          <button type="button" class="del-btn">Remove</button>
        </div>
        <div class="form-grid"></div>
        <div class="kv-list" data-kind="args">
          <div class="kv-list-header">
            <span>args</span>
            <button type="button" class="add-btn add-arg">+ arg</button>
          </div>
          <div class="kv-rows args-rows"></div>
        </div>
        <div class="kv-list" data-kind="env">
          <div class="kv-list-header">
            <span>env</span>
            <button type="button" class="add-btn add-env">+ var</button>
          </div>
          <div class="kv-rows env-rows"></div>
        </div>
      `;
      const grid = row.querySelector(".form-grid");
      grid.appendChild(field("name", s.name, "string", v => { s.name = v; markFormDirty("mcp"); renderMCPList(d); }));
      grid.appendChild(field("command", s.command, "string", v => { s.command = v; markFormDirty("mcp"); }));

      const argsEl = row.querySelector(".args-rows");
      const renderArgs = () => {
        argsEl.innerHTML = "";
        s.args.forEach((a, ai) => {
          const r = document.createElement("div");
          r.className = "kv-row";
          r.innerHTML = `<input type="text" value="${escHtml(a)}" /><button type="button" class="del-btn">×</button>`;
          r.querySelector("input").addEventListener("input", e => { s.args[ai] = e.target.value; markFormDirty("mcp"); });
          r.querySelector(".del-btn").addEventListener("click", () => { s.args.splice(ai, 1); markFormDirty("mcp"); renderArgs(); });
          argsEl.appendChild(r);
        });
      };
      renderArgs();
      row.querySelector(".add-arg").addEventListener("click", () => { s.args.push(""); markFormDirty("mcp"); renderArgs(); });

      const envEl = row.querySelector(".env-rows");
      const renderEnv = () => {
        envEl.innerHTML = "";
        Object.entries(s.env).forEach(([k, v]) => {
          const r = document.createElement("div");
          r.className = "kv-row";
          r.innerHTML = `
            <input type="text" class="kv-k" placeholder="KEY" value="${escHtml(k)}" />
            <input type="text" class="kv-v" placeholder="value" value="${escHtml(v)}" />
            <button type="button" class="del-btn">×</button>
          `;
          const kIn = r.querySelector(".kv-k"), vIn = r.querySelector(".kv-v");
          let oldKey = k;
          kIn.addEventListener("change", () => {
            const nk = kIn.value.trim();
            if (!nk || nk === oldKey) return;
            const val = s.env[oldKey];
            delete s.env[oldKey];
            s.env[nk] = val;
            oldKey = nk;
            markFormDirty("mcp");
          });
          vIn.addEventListener("input", () => { s.env[oldKey] = vIn.value; markFormDirty("mcp"); });
          r.querySelector(".del-btn").addEventListener("click", () => { delete s.env[oldKey]; markFormDirty("mcp"); renderEnv(); });
          envEl.appendChild(r);
        });
      };
      renderEnv();
      row.querySelector(".add-env").addEventListener("click", async () => {
        let nk = await appPrompt("Env var name:");
        if (!nk) return;
        nk = nk.trim();
        if (!nk || nk in s.env) return;
        s.env[nk] = "";
        markFormDirty("mcp"); renderEnv();
      });

      row.querySelector(".del-btn").addEventListener("click", async () => {
        if (!await appConfirm(`Remove server "${s.name}"?`)) return;
        d.servers.splice(idx, 1);
        markFormDirty("mcp"); renderMCPList(d);
      });
      el.appendChild(row);
    });
  }

  // ─── Custom dialogs ────────────────────────────────────────────────────
  function appDialog({ message, withInput = false, placeholder = "" }) {
    return new Promise(resolve => {
      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");

      const msg = document.createElement("p");
      msg.className = "app-dialog-msg";
      msg.textContent = message;
      box.appendChild(msg);

      let inputEl = null;
      if (withInput) {
        inputEl = document.createElement("input");
        inputEl.type = "text";
        inputEl.className = "app-dialog-input";
        inputEl.placeholder = placeholder;
        box.appendChild(inputEl);
      }

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";

      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.textContent = "Cancel";

      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = withInput ? "OK" : "Confirm";

      const close = result => { overlay.remove(); resolve(result); };
      cancelBtn.addEventListener("click", () => close(withInput ? null : false));
      okBtn.addEventListener("click", () => close(withInput ? (inputEl.value.trim() || null) : true));
      overlay.addEventListener("click", e => { if (e.target === overlay) close(withInput ? null : false); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(withInput ? null : false); }
        if (e.key === "Enter")  { e.stopPropagation(); close(withInput ? (inputEl?.value.trim() || null) : true); }
      });

      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      box.appendChild(actions);
      overlay.appendChild(box);
      document.body.appendChild(overlay);
      (inputEl ?? okBtn).focus();
    });
  }

  const appConfirm = msg => appDialog({ message: msg });
  const appPrompt  = (msg, placeholder = "") => appDialog({ message: msg, withInput: true, placeholder });

  // ─── Registry multi-field dialog ───────────────────────────────────────

  function detectRegistryProvider(rawURL) {
    try {
      const u = new URL(rawURL);
      if (u.hostname === "github.com") return "github";
      if (u.hostname === "gitlab.com" || u.pathname.includes("/-/tree/")) return "gitlab";
      if (u.pathname.includes("/src/branch/")) return "gitea";
    } catch (_) {}
    return "";
  }

  function appRegistryDialog({ title = "Add Remote Registry", initial = {}, isEdit = false } = {}) {
    return new Promise(resolve => {
      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog registry-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");

      const titleEl = document.createElement("p");
      titleEl.className = "app-dialog-msg";
      titleEl.textContent = title;
      box.appendChild(titleEl);

      const form = document.createElement("div");
      form.className = "registry-dialog-form";
      const tokenPlaceholder = isEdit && initial.hasToken
        ? "Leave blank to keep existing token"
        : "PAT / PRIVATE-TOKEN / personal token…";
      form.innerHTML = `
        <div class="registry-dialog-field">
          <label for="reg-dlg-name">Name <span class="registry-dialog-hint">(optional)</span></label>
          <input type="text" id="reg-dlg-name" autocomplete="off"
            placeholder="My skill registry"
            value="${escHtml(initial.name || "")}" />
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-url">Repository URL</label>
          <input type="url" id="reg-dlg-url" autocomplete="off"
            placeholder="https://github.com/owner/repo/tree/main/skills"
            value="${escHtml(initial.url || "")}" />
          <span class="registry-dialog-hint">GitHub · GitLab · Gitea (cloud or self-hosted)</span>
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-provider">Provider</label>
          <select id="reg-dlg-provider">
            <option value="">Auto-detect</option>
            <option value="github"${initial.provider === "github" ? " selected" : ""}>GitHub</option>
            <option value="gitlab"${initial.provider === "gitlab" ? " selected" : ""}>GitLab</option>
            <option value="gitea"${initial.provider === "gitea" ? " selected" : ""}>Gitea</option>
          </select>
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-token">Access token <span class="registry-dialog-hint">(optional, for private repos)</span></label>
          <input type="password" id="reg-dlg-token" autocomplete="off"
            placeholder="${escHtml(tokenPlaceholder)}" />
        </div>
      `;
      box.appendChild(form);

      const urlInput      = form.querySelector("#reg-dlg-url");
      const providerSelect = form.querySelector("#reg-dlg-provider");

      urlInput.addEventListener("input", () => {
        if (providerSelect.value !== "") return;
        const detected = detectRegistryProvider(urlInput.value.trim());
        if (detected) providerSelect.value = detected;
      });

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";

      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.textContent = "Cancel";

      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = isEdit ? "Save" : "Add";

      const close = result => { overlay.remove(); resolve(result); };
      cancelBtn.addEventListener("click", () => close(null));
      okBtn.addEventListener("click", () => {
        const urlVal = form.querySelector("#reg-dlg-url").value.trim();
        if (!urlVal) { form.querySelector("#reg-dlg-url").focus(); return; }
        close({
          name:     form.querySelector("#reg-dlg-name").value.trim(),
          url:      urlVal,
          provider: form.querySelector("#reg-dlg-provider").value,
          token:    form.querySelector("#reg-dlg-token").value,
        });
      });

      overlay.addEventListener("click", e => { if (e.target === overlay) close(null); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(null); }
        if (e.key === "Enter" && e.target.tagName !== "SELECT") {
          e.stopPropagation(); okBtn.click();
        }
      });

      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      box.appendChild(actions);
      overlay.appendChild(box);
      document.body.appendChild(overlay);
      (initial.url ? form.querySelector("#reg-dlg-name") : urlInput).focus();
    });
  }

  // ─── Field helpers ─────────────────────────────────────────────────────
  function field(label, val, kind, onChange) {
    const row = document.createElement("label");
    row.className = "form-row";
    let input;
    if (kind === "bool") {
      input = document.createElement("input");
      input.type = "checkbox";
      input.checked = !!val;
      input.addEventListener("change", () => onChange(input.checked));
    } else if (kind === "number") {
      input = document.createElement("input");
      input.type = "number";
      input.value = (val == null ? "" : val);
      input.addEventListener("input", () => {
        const n = input.value === "" ? undefined : Number(input.value);
        onChange(Number.isFinite(n) ? n : undefined);
      });
    } else {
      input = document.createElement("input");
      input.type = "text";
      input.value = (val == null ? "" : String(val));
      input.addEventListener("input", () => onChange(input.value));
    }
    const span = document.createElement("span");
    span.textContent = label;
    row.appendChild(span);
    row.appendChild(input);
    return row;
  }

  function selectField(label, val, options, onChange) {
    const row = document.createElement("label");
    row.className = "form-row";
    const sel = document.createElement("select");
    for (const o of options) {
      const opt = document.createElement("option");
      opt.value = o; opt.textContent = o || "(none)";
      if (o === val) opt.selected = true;
      sel.appendChild(opt);
    }
    sel.addEventListener("change", () => onChange(sel.value));
    const span = document.createElement("span");
    span.textContent = label;
    row.appendChild(span);
    row.appendChild(sel);
    return row;
  }

  function toolsField(label, val, onChange, opts) {
    const serpApiKeySet = opts && !!opts.serpApiKeySet;
    const row = document.createElement("div");
    row.className = "form-row form-row-tools";
    const span = document.createElement("span");
    span.textContent = label;
    row.appendChild(span);
    const wrap = document.createElement("div");
    wrap.className = "tools-checks";
    const cur = new Set(Array.isArray(val) ? val : []);
    const cbByTool = {};
    for (const t of TOOL_GROUPS) {
      const lab = document.createElement("label");
      lab.className = "tools-check";
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.dataset.tool = t;
      cb.checked = cur.has(t);
      // serpapi requires its API key; disable checkbox when key is absent.
      if (t === "serpapi" && !serpApiKeySet) {
        cb.disabled = true;
        lab.className += " tools-check-disabled";
        lab.title = "Set serpapi_key in Globals to enable this tool.";
      }
      cb.addEventListener("change", () => {
        if (cb.checked) {
          cur.add(t);
          // Auto-deselect the mutually-exclusive peer.
          const peer = TOOL_MUTEX[t];
          if (peer && cbByTool[peer]) {
            cur.delete(peer);
            cbByTool[peer].checked = false;
          }
        } else {
          cur.delete(t);
        }
        onChange(Array.from(cur));
      });
      cbByTool[t] = cb;
      lab.appendChild(cb);
      lab.appendChild(document.createTextNode(" " + t));
      const desc = TOOL_DESCRIPTIONS[t];
      if (desc) {
        const tip = document.createElement("span");
        tip.className = "tool-tip-icon";
        tip.textContent = "?";
        tip.setAttribute("aria-label", desc);
        const tipBox = document.createElement("span");
        tipBox.className = "tool-tip-box";
        tipBox.textContent = desc;
        tip.appendChild(tipBox);
        lab.appendChild(tip);
      }
      wrap.appendChild(lab);
    }
    row.appendChild(wrap);
    return row;
  }

  // ─── Skills API helpers ────────────────────────────────────────────────

  class SkillsAPIError extends Error {
    constructor(code, msg, details) {
      super(msg);
      this.code = code;
      this.details = details;
    }
  }

  async function skillsAPI(method, path, body) {
    const opts = { method, headers: authHeaders(body != null ? { "Content-Type": "application/json" } : {}) };
    if (body != null) opts.body = JSON.stringify(body);
    const r = await fetch(`/api${path}`, opts);
    if (r.status === 204) return null;
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new SkillsAPIError(j.code || "HTTP_ERROR", j.error || `HTTP ${r.status}`, j.details);
    return j;
  }

  const skillsGet    = path       => skillsAPI("GET",    path, null);
  const skillsPost   = (path, b)  => skillsAPI("POST",   path, b);
  const skillsPut    = (path, b)  => skillsAPI("PUT",    path, b);
  const skillsDel    = path       => skillsAPI("DELETE", path, null);

  // ─── Skills — shared block renderer ───────────────────────────────────

  // Renders skill checkboxes + Enable all / Disable all into container.
  // agentInfo: {name, skills_dir, has_skills_tool, linked:[], broken:[]}
  // registry: [{name, description, ...}]
  // onChanged: optional callback after a mutation
  function renderSkillBlockContent(container, agentInfo, registry, onChanged) {
    container.innerHTML = "";

    if (!agentInfo.skills_dir) {
      const p = document.createElement("p");
      p.className = "settings-hint";
      p.textContent = "No skills_dir configured — skills cannot be assigned. Set one in the fields above.";
      container.appendChild(p);
      return;
    }

    if (!agentInfo.has_skills_tool) {
      const warn = document.createElement("p");
      warn.className = "skills-tool-warning";
      warn.textContent = '"skills" tool not enabled — assignments will be ignored until re-enabled in Agent → Agents.';
      container.appendChild(warn);
    }

    if (agentInfo.broken && agentInfo.broken.length) {
      const bwrap = document.createElement("div");
      bwrap.className = "skills-broken-warning";
      bwrap.innerHTML = `<span>Broken links: ${escHtml(agentInfo.broken.join(", "))}</span>`;
      const fixBtn = document.createElement("button");
      fixBtn.type = "button"; fixBtn.className = "del-btn"; fixBtn.textContent = "Remove broken";
      fixBtn.addEventListener("click", async () => {
        for (const n of agentInfo.broken) {
          try { await skillsDel(`/skills/agents/${agentInfo.name}/skills/${n}`); } catch (_) {}
        }
        if (onChanged) onChanged();
      });
      bwrap.appendChild(fixBtn);
      container.appendChild(bwrap);
    }

    const linked = new Set(agentInfo.linked || []);

    if (!registry.length) {
      const p = document.createElement("p"); p.className = "empty";
      p.textContent = "No skills installed.";
      container.appendChild(p);
    } else {
      const grid = document.createElement("div");
      grid.className = "skills-check-grid";
      for (const sk of registry) {
        const label = document.createElement("label");
        label.className = "skills-check-item";
        label.dataset.skill = sk.name;
        const cb = document.createElement("input");
        cb.type = "checkbox"; cb.checked = linked.has(sk.name);
        cb.addEventListener("change", async () => {
          cb.disabled = true;
          try {
            if (cb.checked) {
              await skillsPost(`/skills/agents/${agentInfo.name}/skills/${sk.name}`, null);
              linked.add(sk.name);
            } else {
              await skillsDel(`/skills/agents/${agentInfo.name}/skills/${sk.name}`);
              linked.delete(sk.name);
            }
          } catch (e) {
            cb.checked = !cb.checked;
            setStatus("Skills: " + e.message, "error");
          } finally { cb.disabled = false; }
        });
        label.appendChild(cb);
        const nameSpan = document.createElement("span");
        nameSpan.className = "skills-check-name"; nameSpan.textContent = sk.name;
        label.appendChild(nameSpan);
        if (sk.description) {
          const desc = document.createElement("span");
          desc.className = "skills-check-desc"; desc.textContent = sk.description;
          label.appendChild(desc);
        }
        grid.appendChild(label);
      }
      container.appendChild(grid);

      const actions = document.createElement("div");
      actions.className = "skills-block-actions";

      const enableAllBtn = document.createElement("button");
      enableAllBtn.type = "button"; enableAllBtn.className = "add-btn";
      enableAllBtn.textContent = "Enable all";

      const disableAllBtn = document.createElement("button");
      disableAllBtn.type = "button"; disableAllBtn.className = "del-btn";
      disableAllBtn.textContent = "Disable all";

      enableAllBtn.addEventListener("click", async () => {
        enableAllBtn.disabled = disableAllBtn.disabled = true;
        try {
          const res = await skillsPost(`/skills/agents/${agentInfo.name}/skills`, { action: "all" });
          (res.linked || []).forEach(n => linked.add(n));
          container.querySelectorAll(".skills-check-item input").forEach(cb => {
            if (linked.has(cb.closest(".skills-check-item").dataset.skill)) cb.checked = true;
          });
        } catch (e) { setStatus("Skills: " + e.message, "error"); }
        finally { enableAllBtn.disabled = disableAllBtn.disabled = false; }
      });

      disableAllBtn.addEventListener("click", async () => {
        if (!await appConfirm(`Remove all skill links from "${agentInfo.name}"?`)) return;
        enableAllBtn.disabled = disableAllBtn.disabled = true;
        try {
          await skillsPost(`/skills/agents/${agentInfo.name}/skills`, { action: "none" });
          linked.clear();
          container.querySelectorAll(".skills-check-item input").forEach(cb => { cb.checked = false; });
        } catch (e) { setStatus("Skills: " + e.message, "error"); }
        finally { enableAllBtn.disabled = disableAllBtn.disabled = false; }
      });

      actions.appendChild(enableAllBtn);
      actions.appendChild(disableAllBtn);

      const manageLink = document.createElement("button");
      manageLink.type = "button"; manageLink.className = "skills-manage-link";
      manageLink.textContent = "Manage in Skills →";
      manageLink.addEventListener("click", () => {
        state.skills.editing = null;
        setActiveFile("skills");
      });
      actions.appendChild(manageLink);
      container.appendChild(actions);
    }
  }

  // Populates a container with the agent's skill block (fetches data async).
  async function populateAgentSkillBlock(container, agentName) {
    container.innerHTML = `<p class="settings-hint">Loading skills…</p>`;
    try {
      const [regRes, agtsRes] = await Promise.all([
        skillsGet("/skills/registry"),
        skillsGet("/skills/agents"),
      ]);
      const registry = regRes.skills || [];
      const agentInfo = (agtsRes.agents || []).find(a => a.name === agentName);
      if (!agentInfo) { container.innerHTML = ""; return; }
      const refresh = async () => {
        try {
          const fresh = await skillsGet("/skills/agents");
          const fa = (fresh.agents || []).find(a => a.name === agentName);
          if (fa) renderSkillBlockContent(container, fa, registry, refresh);
        } catch (_) {}
      };
      renderSkillBlockContent(container, agentInfo, registry, refresh);
    } catch (e) {
      container.innerHTML = `<p class="settings-error">Skills unavailable: ${escHtml(e.message)}</p>`;
    }
  }

  // ─── Skills — main panel renderer ─────────────────────────────────────

  async function renderSkills() {
    bodyEl.innerHTML = `<p class="settings-loading">Loading…</p>`;
    applyClientOnlyChrome();

    if (state.skills.editing) {
      await renderSkillDetailView();
      return;
    }
    if (state.skills.viewingRemote) {
      await renderRemoteSkillDetailView();
      return;
    }
    if (state.skills.browsingRemote) {
      await renderRemoteBrowseView();
      return;
    }

    bodyEl.innerHTML = `<div class="settings-form"><div class="skills-subtab-body"></div></div>`;
    await renderSkillsRegistryTab(bodyEl.querySelector(".skills-subtab-body"));
  }

  async function renderSkillsRegistryTab(host) {
    host.innerHTML = `<p class="settings-loading">Loading registry…</p>`;
    let skills;
    try {
      const res = await skillsGet("/skills/registry");
      skills = res.skills || [];
    } catch (e) {
      host.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }

    host.innerHTML = `
      <section class="form-section">
        <h3>Installed skills
          <button type="button" class="add-btn" id="skill-new">+ New</button>
          <label class="add-btn skill-upload-label" id="skill-upload-label" style="cursor:pointer">
            Upload archive
            <input type="file" id="skill-upload-input" accept=".zip,.tar.gz,.tgz" style="display:none">
          </label>
        </h3>
        <div id="skills-registry-list"></div>
      </section>
    `;

    renderSkillCards(host.querySelector("#skills-registry-list"), skills);
    await renderRemoteRegistriesSection(host);

    host.querySelector("#skill-new").addEventListener("click", async () => {
      const name = await appPrompt("Skill name (lowercase, hyphens ok):", "my-skill");
      if (!name) return;
      const n = name.trim().toLowerCase();
      try {
        await skillsPost("/skills/registry", { name: n });
        state.skills.editing = { name: n };
        renderSkills();
      } catch (e) {
        setStatus("Create failed: " + e.message, "error");
      }
    });

    host.querySelector("#skill-upload-input").addEventListener("change", async e => {
      const file = e.target.files[0]; e.target.value = "";
      if (file) await doSkillUpload(host, file, false);
    });

    setupSkillDropZone(host, file => doSkillUpload(host, file, false));
  }

  function renderSkillCards(container, skills) {
    container.innerHTML = "";
    if (!skills.length) {
      container.innerHTML = `
        <p class="empty">No skills installed yet. Add one or upload an archive.</p>
        <p class="settings-hint">Skills live in <code>skills-registry/installed/</code> — commit them yourself to track in git.</p>
      `;
      return;
    }
    const grid = document.createElement("div");
    grid.className = "skill-marketplace-grid";
    for (const sk of skills) {
      const card = document.createElement("div");
      card.className = "skill-mkt-card";

      const dateStr = sk.mtime ? new Date(sk.mtime).toLocaleDateString("en-CA") : "";
      const tagsHtml = (sk.tags && sk.tags.length)
        ? `<div class="skill-mkt-tags">${sk.tags.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")}</div>`
        : "";
      const authorHtml = sk.author
        ? `<div class="skill-mkt-author"><span class="skill-mkt-author-icon">◆</span><span class="skill-mkt-author-name">${escHtml(sk.author)}</span></div>`
        : "";
      const linkedStr = sk.linked_in && sk.linked_in.length
        ? `<span class="skill-mkt-linked">Used by: ${escHtml(sk.linked_in.join(", "))}</span>`
        : `<span class="skill-mkt-unlinked">Not linked</span>`;

      card.innerHTML = `
        <div class="skill-mkt-header">
          <span class="skill-mkt-filename">${ICONS.skills}${escHtml(sk.name)}</span>
        </div>
        <div class="skill-mkt-body">
          ${authorHtml}
          <p class="skill-mkt-desc">${escHtml(sk.description || "(no description)")}</p>
          ${tagsHtml}
        </div>
        <div class="skill-mkt-footer">
          <span class="skill-mkt-date">${dateStr}</span>
          <span class="skill-mkt-footer-right">${linkedStr}</span>
        </div>
      `;
      card.addEventListener("click", () => {
        state.skills.editing = { name: sk.name };
        renderSkills();
      });
      grid.appendChild(card);
    }
    container.appendChild(grid);
  }

  function parseFrontmatter(content) {
    const s = content.trimStart();
    if (!s.startsWith("---")) return {};
    const rest = s.slice(3);
    const idx = rest.indexOf("\n---");
    if (idx < 0) return {};
    const result = {};
    let section = null;
    for (const line of rest.slice(0, idx).split("\n")) {
      if (!line.trim()) continue;
      const indented = line.startsWith("  ") || line.startsWith("\t");
      const col = line.indexOf(":");
      if (col < 0) continue;
      const key = line.slice(0, col).trim();
      const val = line.slice(col + 1).trim();
      if (indented && section) {
        if (val.startsWith("[") && val.endsWith("]")) {
          result[section][key] = val.slice(1, -1).split(",").map(t => t.trim().replace(/^["']|["']$/g, "")).filter(Boolean);
        } else {
          result[section][key] = val;
        }
      } else if (!indented) {
        if (val === "") { section = key; result[key] = {}; }
        else { section = null; result[key] = val; }
      }
    }
    return result;
  }

  function stripFrontmatter(content) {
    const s = content.trimStart();
    if (!s.startsWith("---")) return content;
    const rest = s.slice(3);
    const idx = rest.indexOf("\n---");
    if (idx < 0) return content;
    return rest.slice(idx + 4).trimStart();
  }

  async function renderSkillDetailView() {
    const { name } = state.skills.editing;
    bodyEl.innerHTML = `<p class="settings-loading">Loading…</p>`;
    let detail;
    try { detail = await skillsGet(`/skills/registry/${name}`); }
    catch (e) {
      bodyEl.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }

    bodyEl.innerHTML = `
      <div class="settings-form skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to registry</button>
        </div>
        <div class="skill-frontmatter-card" id="skill-fm-card"></div>
        <div class="skill-content-wrap">
          <div class="skill-resource-tabs"></div>
          <div class="skill-md-preview markdown-body"></div>
          <textarea class="skill-md-editor raw-editor" spellcheck="false" hidden></textarea>
        </div>
        <div class="skill-detail-footer">
          <button type="button" class="del-btn skill-del-btn">Delete</button>
          <span class="skill-save-status"></span>
          <button type="button" class="add-btn skill-edit-btn">Edit</button>
        </div>
      </div>
    `;

    bodyEl.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.skills.editing = null;
      renderSkills();
    });

    const tabsEl   = bodyEl.querySelector(".skill-resource-tabs");
    const preview  = bodyEl.querySelector(".skill-md-preview");
    const ta       = bodyEl.querySelector(".skill-md-editor");
    const footer   = bodyEl.querySelector(".skill-detail-footer");
    const saveStatus = bodyEl.querySelector(".skill-save-status");
    let currentMtime   = detail.mtime;
    let currentContent = detail.content;
    let currentTab     = "skill-md";
    let isEditing      = false;

    function renderPreview(content) {
      if (typeof marked !== "undefined") {
        preview.innerHTML = marked.parse(stripFrontmatter(content));
      } else {
        preview.textContent = stripFrontmatter(content);
      }
    }

    function renderFrontmatterCard(content) {
      const fm = parseFrontmatter(content);
      const fmCard = bodyEl.querySelector("#skill-fm-card");
      const rows = [];
      for (const [k, v] of Object.entries(fm)) {
        if (typeof v === "object" && !Array.isArray(v)) {
          for (const [sk, sv] of Object.entries(v)) {
            const display = Array.isArray(sv)
              ? sv.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")
              : escHtml(String(sv));
            const cls = Array.isArray(sv) ? "skill-fm-value skill-fm-tags" : "skill-fm-value";
            rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(sk)}</span><span class="${cls}">${display}</span></div>`);
          }
        } else {
          const display = Array.isArray(v)
            ? v.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")
            : escHtml(String(v));
          const cls = Array.isArray(v) ? "skill-fm-value skill-fm-tags" : "skill-fm-value";
          rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(k)}</span><span class="${cls}">${display}</span></div>`);
        }
      }
      fmCard.innerHTML = rows.join("");
    }

    function setEditMode(editing) {
      isEditing = editing;
      if (editing) {
        preview.hidden = true;
        ta.hidden = false;
        ta.value = currentContent;
        footer.innerHTML = `
          <button type="button" class="btn-discard skill-cancel-btn">Discard</button>
          <span class="skill-save-status"></span>
          <button type="button" class="btn-save skill-save-btn">Save</button>
        `;
        footer.querySelector(".skill-cancel-btn").addEventListener("click", () => setEditMode(false));
        footer.querySelector(".skill-save-btn").addEventListener("click", async () => {
          const saveBtn = footer.querySelector(".skill-save-btn");
          const status  = footer.querySelector(".skill-save-status");
          saveBtn.disabled = true; status.textContent = "Saving…"; status.className = "skill-save-status";
          try {
            currentContent = ta.value;
            const res = await skillsPut(`/skills/registry/${name}`, { content: currentContent, mtime: currentMtime });
            currentMtime = res.mtime;
            renderFrontmatterCard(currentContent);
            status.textContent = "Saved."; status.className = "skill-save-status success";
            setTimeout(() => setEditMode(false), 800);
          } catch (e) {
            status.textContent = "Save failed: " + e.message;
            status.className = "skill-save-status error";
          } finally { saveBtn.disabled = false; }
        });
      } else {
        ta.hidden = true;
        preview.hidden = false;
        renderPreview(currentContent);
        footer.innerHTML = `
          <button type="button" class="del-btn skill-del-btn">Delete</button>
          <span class="skill-save-status"></span>
          <button type="button" class="btn-save skill-edit-btn">Edit</button>
        `;
        footer.querySelector(".skill-edit-btn").addEventListener("click", () => setEditMode(true));
        footer.querySelector(".skill-del-btn").addEventListener("click", async () => {
          if (!await appConfirm(`Delete skill "${name}"?`)) return;
          try {
            await skillsDel(`/skills/registry/${name}`);
            state.skills.editing = null;
            renderSkills();
          } catch (e) {
            if (e.code === "LINKED_IN_AGENTS") {
              const agents = (e.details && e.details.agents || []).join(", ");
              if (!await appConfirm(`"${name}" is still used by: ${agents}. Remove links and delete?`)) return;
              try { await skillsDel(`/skills/registry/${name}?force=1`); state.skills.editing = null; renderSkills(); }
              catch (e2) { setStatus("Delete failed: " + e2.message, "error"); }
            } else {
              setStatus("Delete failed: " + e.message, "error");
            }
          }
        });
      }
    }

    // Build resource sub-tabs.
    const resourceDirs = [...new Set((detail.resources || []).map(r => r.split("/")[0]))];
    const tabs = [{ label: "SKILL.md", key: "skill-md" }, ...resourceDirs.map(d => {
      const count = (detail.resources || []).filter(r => r.startsWith(d + "/")).length;
      return { label: `${d}/ (${count})`, key: d };
    })];
    tabsEl.innerHTML = `<div class="settings-subtabs" role="tablist">
      ${tabs.map((t, i) => `<button type="button" data-tabkey="${escHtml(t.key)}" class="${i === 0 ? "active" : ""}">${escHtml(t.label)}</button>`).join("")}
    </div>`;
    tabsEl.querySelectorAll("button").forEach(btn => {
      btn.addEventListener("click", () => {
        tabsEl.querySelectorAll("button").forEach(b => b.classList.remove("active"));
        btn.classList.add("active");
        currentTab = btn.dataset.tabkey;
        if (currentTab === "skill-md") {
          if (isEditing) { ta.hidden = false; preview.hidden = true; ta.value = currentContent; }
          else           { ta.hidden = true;  preview.hidden = false; renderPreview(currentContent); }
        } else {
          const files = (detail.resources || []).filter(r => r.startsWith(currentTab + "/"));
          ta.value = files.length ? files.join("\n") : "(empty)";
          ta.hidden = false; preview.hidden = true;
          ta.readOnly = true;
        }
      });
    });

    // Initial render.
    renderFrontmatterCard(currentContent);
    setEditMode(false);
  }

  // ─── Skills — remote registries ───────────────────────────────────────

  async function renderRemoteRegistriesSection(host) {
    const section = document.createElement("section");
    section.className = "form-section";
    section.innerHTML = `
      <h3>Remote registries
        <button type="button" class="add-btn" id="remote-reg-add">+ Add</button>
      </h3>
      <div id="remote-reg-list"></div>
    `;
    host.appendChild(section);

    const listEl = section.querySelector("#remote-reg-list");
    await refreshRemoteRegList(listEl);

    section.querySelector("#remote-reg-add").addEventListener("click", async () => {
      const result = await appRegistryDialog();
      if (!result) return;
      try {
        await skillsPost("/skills/remotes", result);
        await refreshRemoteRegList(listEl);
      } catch (e) {
        setStatus("Failed to add registry: " + e.message, "error");
      }
    });
  }

  async function refreshRemoteRegList(container) {
    container.innerHTML = `<p class="settings-loading">Loading…</p>`;
    let remotes;
    try {
      const res = await skillsGet("/skills/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install skills.</p>`;
      return;
    }
    container.innerHTML = "";
    for (const r of remotes) {
      const providerLabel = r.provider ? r.provider.charAt(0).toUpperCase() + r.provider.slice(1) : "";
      const row = document.createElement("div");
      row.className = "remote-reg-row";
      row.innerHTML = `
        <div class="remote-reg-info">
          <span class="remote-reg-name">${escHtml(r.name)}${providerLabel ? ` <span class="remote-reg-provider">${escHtml(providerLabel)}</span>` : ""}</span>
          <span class="remote-reg-url">${escHtml(r.url)}</span>
        </div>
        <div class="remote-reg-actions">
          <button type="button" class="add-btn remote-browse-btn">Browse</button>
          <button type="button" class="edit-btn remote-edit-btn">Edit</button>
          <button type="button" class="del-btn remote-remove-btn">Remove</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.skills.browsingRemote = { id: r.id, name: r.name, url: r.url };
        renderSkills();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: "Edit Registry",
          initial: { name: r.name, url: r.url, provider: r.provider || "", hasToken: !!r.has_token },
          isEdit: true,
        });
        if (!result) return;
        try {
          await skillsPut(`/skills/remotes/${r.id}`, result);
          delete remoteSkillsCache[r.id];
          await refreshRemoteRegList(container);
        } catch (e) {
          setStatus("Failed to update registry: " + e.message, "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(`Remove registry "${r.name}"?`)) return;
        try {
          await skillsDel(`/skills/remotes/${r.id}`);
          delete remoteSkillsCache[r.id];
          await refreshRemoteRegList(container);
        } catch (e) {
          setStatus("Failed to remove registry: " + e.message, "error");
        }
      });
      container.appendChild(row);
    }
  }

  const remoteSkillsCache = {}; // keyed by registry ID → { skills, timestamp }
  const REMOTE_CACHE_TTL = 90 * 60 * 1000; // 90 minutes

  async function renderRemoteBrowseView() {
    const { id, name } = state.skills.browsingRemote;
    const cached = remoteSkillsCache[id];
    const hasCached = !!(cached && (Date.now() - cached.timestamp < REMOTE_CACHE_TTL));

    bodyEl.innerHTML = `
      <div class="settings-form skill-detail-view">
        <div class="skill-detail-header remote-browse-top">
          <button type="button" class="skill-back-btn">Back to registry</button>
          <span class="remote-browse-refresh-badge"${hasCached ? "" : " hidden"}>Refreshing…</span>
        </div>
        ${!hasCached ? `
          <div class="remote-browse-loading">
            <p class="settings-loading">Browsing <strong>${escHtml(name)}</strong>…</p>
            <p class="settings-hint">Scanning the full repository tree for SKILL.md files. This may take a moment.</p>
          </div>
        ` : ""}
        <div id="remote-browse-content"></div>
      </div>
    `;
    bodyEl.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.skills.browsingRemote = null;
      renderSkills();
    });

    const contentEl = bodyEl.querySelector("#remote-browse-content");

    function populateContent(skills) {
      contentEl.innerHTML = "";

      const truncated = skills.some(sk => sk.dir_path === "__truncated__");
      const realSkills = skills.filter(sk => sk.dir_path !== "__truncated__");

      const skillCount = realSkills.length;
      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${skillCount} skill${skillCount !== 1 ? "s" : ""}${truncated ? " (tree truncated — some skills may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!realSkills.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = "No skills found in this registry.";
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const sk of realSkills) {
        const g = sk.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(sk);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1;
        if (b === "") return 1;
        return a.localeCompare(b);
      });

      function buildSkillCard(sk) {
        const card = document.createElement("div");
        card.className = "skill-mkt-card remote-skill-card";

        const tagsHtml = (sk.tags && sk.tags.length)
          ? `<div class="skill-mkt-tags">${sk.tags.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")}</div>`
          : "";
        const authorHtml = sk.author
          ? `<div class="skill-mkt-author"><span class="skill-mkt-author-icon">◆</span><span class="skill-mkt-author-name">${escHtml(sk.author)}</span></div>`
          : "";
        const actionHtml = sk.installed
          ? `<span class="remote-skill-installed-badge">Installed</span>`
          : `<button type="button" class="add-btn remote-install-btn">Install</button>`;

        card.innerHTML = `
          <div class="skill-mkt-header">
            <span class="skill-mkt-filename">${ICONS.skills}${escHtml(sk.name)}</span>
            ${actionHtml}
          </div>
          <div class="skill-mkt-body">
            ${authorHtml}
            <p class="skill-mkt-desc">${escHtml(sk.description || "(no description)")}</p>
            ${tagsHtml}
          </div>
        `;

        if (!sk.installed) {
          const installBtn = card.querySelector(".remote-install-btn");
          installBtn.addEventListener("click", async () => {
            installBtn.disabled = true;
            installBtn.textContent = "Installing…";
            try {
              const res = await skillsPost(`/skills/remotes/${id}/install/${sk.dir_path}`, {});
              installBtn.outerHTML = `<span class="remote-skill-installed-badge">Installed</span>`;
              sk.installed = true;
              setStatus(`Skill "${res.name}" installed successfully.`, "success");
            } catch (e) {
              installBtn.disabled = false;
              installBtn.textContent = "Install";
              setStatus("Install failed: " + e.message, "error");
            }
          });
        }

        card.addEventListener("click", e => {
          if (e.target.closest(".remote-install-btn")) return;
          state.skills.viewingRemote = { ...state.skills.browsingRemote, skill: sk };
          renderSkills();
        });

        return card;
      }

      for (const group of sortedGroups) {
        const groupSkills = grouped.get(group);
        if (group) {
          const groupHdr = document.createElement("div");
          groupHdr.className = "remote-group-header";
          groupHdr.textContent = group.replace(/\//g, " › ");
          contentEl.appendChild(groupHdr);
        }
        const grid = document.createElement("div");
        grid.className = "skill-marketplace-grid";
        for (const sk of groupSkills) grid.appendChild(buildSkillCard(sk));
        contentEl.appendChild(grid);
      }
    }

    // Show cached data immediately while the fresh fetch runs in the background.
    if (hasCached) populateContent(cached.skills);

    let skills;
    try {
      const res = await skillsGet(`/skills/remotes/${id}/browse`);
      skills = res.skills || [];
    } catch (e) {
      if (!hasCached) {
        const loadEl = bodyEl.querySelector(".remote-browse-loading");
        if (loadEl) loadEl.outerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      }
      const badge = bodyEl.querySelector(".remote-browse-refresh-badge");
      if (badge) badge.hidden = true;
      return;
    }

    // Guard: user may have navigated away while the fetch was in flight.
    if (!bodyEl.contains(contentEl)) return;

    remoteSkillsCache[id] = { skills, timestamp: Date.now() };

    const loadEl = bodyEl.querySelector(".remote-browse-loading");
    if (loadEl) loadEl.remove();
    const badge = bodyEl.querySelector(".remote-browse-refresh-badge");
    if (badge) badge.hidden = true;

    populateContent(skills);
  }

  async function renderRemoteSkillDetailView() {
    const { id, name, skill } = state.skills.viewingRemote;
    bodyEl.innerHTML = `
      <div class="settings-form skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to ${escHtml(name)}</button>
        </div>
        <div class="skill-frontmatter-card" id="skill-fm-card">
          <p class="settings-loading">Loading…</p>
        </div>
        <div class="skill-content-wrap">
          <div class="skill-md-preview markdown-body"></div>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${skill.installed
            ? `<span class="remote-skill-installed-badge">Installed</span>`
            : `<button type="button" class="add-btn remote-install-btn">Install</button>`}
        </div>
      </div>
    `;

    bodyEl.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.skills.viewingRemote = null;
      renderSkills();
    });

    const preview = bodyEl.querySelector(".skill-md-preview");
    const fmCard  = bodyEl.querySelector("#skill-fm-card");

    let content;
    try {
      const res = await skillsGet(`/skills/remotes/${id}/skill/${skill.dir_path}`);
      content = res.content;
    } catch (e) {
      fmCard.innerHTML = "";
      preview.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }

    // Frontmatter card.
    const fm = parseFrontmatter(content);
    const rows = [];
    for (const [k, v] of Object.entries(fm)) {
      if (typeof v === "object" && !Array.isArray(v)) {
        for (const [sk2, sv] of Object.entries(v)) {
          const display = Array.isArray(sv)
            ? sv.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")
            : escHtml(String(sv));
          const cls = Array.isArray(sv) ? "skill-fm-value skill-fm-tags" : "skill-fm-value";
          rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(sk2)}</span><span class="${cls}">${display}</span></div>`);
        }
      } else {
        const display = Array.isArray(v)
          ? v.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")
          : escHtml(String(v));
        const cls = Array.isArray(v) ? "skill-fm-value skill-fm-tags" : "skill-fm-value";
        rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(k)}</span><span class="${cls}">${display}</span></div>`);
      }
    }
    fmCard.innerHTML = rows.join("") || "";

    // Markdown preview.
    if (typeof marked !== "undefined") {
      preview.innerHTML = marked.parse(stripFrontmatter(content));
    } else {
      preview.textContent = stripFrontmatter(content);
    }

    // Install button.
    const installBtn = bodyEl.querySelector(".remote-install-btn");
    if (installBtn) {
      installBtn.addEventListener("click", async () => {
        installBtn.disabled = true;
        installBtn.textContent = "Installing…";
        const statusEl = bodyEl.querySelector(".skill-save-status");
        try {
          const res = await skillsPost(`/skills/remotes/${id}/install/${skill.dir_path}`, {});
          installBtn.outerHTML = `<span class="remote-skill-installed-badge">Installed</span>`;
          skill.installed = true;
          setStatus(`Skill "${res.name}" installed successfully.`, "success");
        } catch (e) {
          installBtn.disabled = false;
          installBtn.textContent = "Install";
          if (statusEl) { statusEl.textContent = e.message; statusEl.className = "skill-save-status error"; }
        }
      });
    }
  }

  // ─── Skills — upload helpers ───────────────────────────────────────────

  async function doSkillUpload(host, file, overwrite) {
    const fd = new FormData(); fd.append("file", file);
    const url = `/api/skills/registry/upload${overwrite ? "?overwrite=1" : ""}`;
    setStatus("Uploading…");
    try {
      const r = await fetch(url, { method: "POST", headers: authHeaders(), body: fd });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) {
        if (r.status === 409 && j.code === "NAME_TAKEN") {
          // Extract skill name from error message if possible.
          const m = j.error && j.error.match(/"([^"]+)"/);
          const sname = m ? m[1] : "existing skill";
          if (await appConfirm(`"${sname}" already exists. Overwrite?`)) {
            await doSkillUpload(host, file, true);
          }
          return;
        }
        throw new Error(j.error || `HTTP ${r.status}`);
      }
      setStatus(`Skill "${j.name}" uploaded successfully.`, "success");
      renderSkills();
    } catch (e) { setStatus("Upload failed: " + e.message, "error"); }
  }

  function setupSkillDropZone(el, onFile) {
    el.addEventListener("dragover", e => { e.preventDefault(); el.classList.add("drop-active"); });
    el.addEventListener("dragleave", e => { if (!el.contains(e.relatedTarget)) el.classList.remove("drop-active"); });
    el.addEventListener("drop", e => {
      e.preventDefault(); el.classList.remove("drop-active");
      const file = e.dataTransfer.files[0]; if (!file) return;
      if (!/\.(zip|tar\.gz|tgz)$/i.test(file.name)) {
        setStatus("Only .zip or .tar.gz archives are accepted.", "error"); return;
      }
      onFile(file);
    });
  }

  // ─── Save / Discard ────────────────────────────────────────────────────
  async function saveActive() {
    const id = state.activeFile;
    setStatus("Saving…");
    try {
      if (state.activeView === "raw") {
        const s = state.raw[id];
        const r = await fetch(`/api/config/file/${id}`, {
          method: "PUT",
          headers: authHeaders({ "Content-Type": "application/json" }),
          body: JSON.stringify({ content: s.value, mtime: s.mtime }),
        });
        if (!r.ok) throw new Error(await errText(r));
        const j = await r.json();
        s.content = j.content; s.mtime = j.mtime; s.dirty = false;
        // Invalidate parsed cache so the form view re-fetches.
        delete state.parsed[id];
      } else {
        const p = state.parsed[id];
        const r = await fetch(`/api/config/parsed/${id}`, {
          method: "PUT",
          headers: authHeaders({ "Content-Type": "application/json" }),
          body: JSON.stringify({ data: p.value, mtime: p.mtime }),
        });
        if (!r.ok) throw new Error(await errText(r));
        const j = await r.json();
        p.data = deepClone(p.value);
        p.mtime = j.mtime;
        p.dirty = false;
        // Invalidate raw cache so the raw view re-fetches the canonical YAML.
        delete state.raw[id];
      }
      setStatus("Saved. Restart the server to apply.", "success");
      showBanner();
      renderBody();
    } catch (e) {
      setStatus("Save failed: " + e.message, "error");
    }
  }

  async function discardActive() {
    if (!hasUnsavedActive()) return;
    if (!await appConfirm("Discard unsaved changes?")) return;
    const id = state.activeFile;
    if (state.activeView === "raw") delete state.raw[id];
    else delete state.parsed[id];
    setStatus("");
    renderBody();
  }

  // ─── Public API ────────────────────────────────────────────────────────
  function open() {
    ensurePanel();
    refreshBannerVisibility();
    state.open = true;
    // Single CSS class drives chat-vs-settings layout; no inline style fights
    // with app.js for control of #transcript / #composer-wrap / #prompt-header.
    document.getElementById("chat").classList.add("chat--settings");
    panelEl.hidden = false;
    const sb = document.getElementById("settings-btn");
    if (sb) sb.classList.add("active");
    if (sidebarMenuEl) sidebarMenuEl.hidden = false;
    syncActiveHighlight(state.activeFile);
    renderBody();
  }

  function close() {
    if (!state.open) return;
    state.open = false;
    if (panelEl) panelEl.hidden = true;
    document.getElementById("chat").classList.remove("chat--settings");
    const sb = document.getElementById("settings-btn");
    if (sb) sb.classList.remove("active");
    if (sidebarMenuEl) sidebarMenuEl.hidden = true;
  }

  function isOpen() { return state.open; }

  // Window-level dirty guard.
  window.addEventListener("beforeunload", e => {
    for (const id of Object.keys(state.raw)) if (state.raw[id].dirty) { e.preventDefault(); e.returnValue = ""; return; }
    for (const id of Object.keys(state.parsed)) if (state.parsed[id].dirty) { e.preventDefault(); e.returnValue = ""; return; }
  });

  // Expose & wire button.
  window.Settings = { open, close, isOpen };

  document.addEventListener("DOMContentLoaded", () => {
    refreshBannerVisibility();
    syncThemeFromServer();
    const btn = document.getElementById("settings-btn");
    if (btn) btn.addEventListener("click", () => {
      if (isOpen()) close(); else open();
    });
  });
})();
