// Settings panel — yoke configuration editor.
// Loaded after app.js. Uses the same `token` and `authHeaders` defined there.
// Exposes Settings.open() / Settings.close() / Settings.isOpen().

(function () {
const BASE_PATH = window.BASE_PATH || "";
  // Small inline SVG icons rendered next to each entry in the sidebar menu.
  const ICONS = {
    agent: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="7" width="18" height="13" rx="2"/><path d="M8 7V5a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/><circle cx="9" cy="13" r="1"/><circle cx="15" cy="13" r="1"/></svg>`,
    models: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="3"/><path d="M12 2v3"/><path d="M12 19v3"/><path d="M4.22 4.22l2.12 2.12"/><path d="M17.66 17.66l2.12 2.12"/><path d="M2 12h3"/><path d="M19 12h3"/><path d="M4.22 19.78l2.12-2.12"/><path d="M17.66 6.34l2.12-2.12"/></svg>`,
    permissions: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="11" width="18" height="11" rx="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`,
    mcp: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>`,
    a2a: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="18" cy="5" r="3"/><circle cx="6" cy="12" r="3"/><circle cx="18" cy="19" r="3"/><line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/><line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/></svg>`,
    skills: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>`,
    appearance: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18c1.5 0 2-1 2-2 0-1.5 1-2 2-2h2a3 3 0 0 0 3-3 9 9 0 0 0-9-9z"/><circle cx="7.5" cy="10.5" r="1"/><circle cx="12" cy="7.5" r="1"/><circle cx="16.5" cy="10.5" r="1"/></svg>`,
    documentation: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"/><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/></svg>`,
    "user-commands": `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="3"/><line x1="15" y1="7" x2="9" y2="17"/></svg>`,
    raw: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`,
  };

  // Pseudo-id used to mark the Raw JSON toggle in the sidebar menu. It is
  // not a real section: clicking it flips activeView on the current file.
  const RAW_VIEW_ID = "__raw__";

  // Server-backed JSON configs. Each id matches /api/config/{parsed,file}/<id>.
  const FILES = [
    { id: "agent",       label: "Agents",      form: "agent" },
    { id: "models",      label: "Models",      form: "models" },
    { id: "permissions", label: "Permissions", form: "permissions" },
    { id: "mcp",         label: "MCP",         form: "mcp" },
    { id: "a2a",         label: "A2A",         form: "a2a" },
  ];

  // Sidebar menu entries (JSON configs + client-only views like Appearance).
  // `title` is the human-readable section name shown in the breadcrumb header.
  const APPEARANCE_ID = "appearance";
  const DOCUMENTATION_ID = "documentation";
  const USER_COMMANDS_ID = "user-commands";
  const MENU_ITEMS = [
    { id: "skills",        label: "Skills",      title: "Skills",                    kind: "client" },
    { id: "agent",         label: "Agents",      title: "Agent Configuration",       kind: "json" },
    { id: "models",        label: "Models",      title: "Models & Providers",        kind: "json" },
    { id: "permissions",   label: "Permissions", title: "Permissions",               kind: "json" },
    { id: "mcp",           label: "MCP",         title: "MCP Servers",               kind: "json" },
    { id: "a2a",           label: "A2A",         title: "A2A Agents",                kind: "json" },
    { id: USER_COMMANDS_ID,label: "Commands",    title: "Slash Commands",            kind: "client" },
    { id: APPEARANCE_ID,   label: "Appearance",  title: "Appearance",                kind: "client" },
    { id: DOCUMENTATION_ID,label: "Documentation", title: "Documentation",           kind: "client" },
  ];

  // Documentation pages: ordered list of markdown files served from /assets/docs/.
  // Each entry maps to <web/docs/<file>>; `group` partitions the TOC sidebar.
  const DOC_PAGES = [
    { id: "getting-started", file: "01-getting-started.md", label: "Getting Started", group: "Web UI" },
    { id: "composer",        file: "02-composer.md",        label: "The Composer",    group: "Web UI" },
    { id: "sessions",        file: "03-sessions.md",        label: "Sessions",        group: "Web UI" },
    { id: "settings-panel",  file: "04-settings-panel.md",  label: "Settings Panel",  group: "Web UI" },
    { id: "themes",          file: "05-themes.md",          label: "Appearance & Themes", group: "Web UI" },
    { id: "architecture",    file: "10-architecture.md",    label: "Architecture",    group: "Core Concepts" },
    { id: "skills-concept",  file: "11-skills.md",          label: "Skills",          group: "Core Concepts" },
    { id: "agents-settings", file: "19-agents.md",          label: "Agents Settings",      group: "Core Concepts" },
    { id: "mcp-concept",     file: "12-mcp.md",             label: "MCP Servers",     group: "Core Concepts" },
    { id: "a2a",             file: "17-a2a.md",             label: "A2A Agents",          group: "Core Concepts" },
    { id: "commands",        file: "18-commands.md",        label: "Slash Commands",       group: "Core Concepts" },
    { id: "permissions-concept", file: "13-permissions.md", label: "Permissions",     group: "Core Concepts" },
    { id: "config",          file: "14-config.md",          label: "Configuration & Filesystem", group: "Core Concepts" },
    { id: "providers",       file: "15-providers.md",       label: "Providers & Models", group: "Core Concepts" },
    { id: "env-vars",        file: "16-env-vars.md",        label: "Environment Variables", group: "Core Concepts" },
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
    { id: "solarized-light",     label: "Solarized Light", tier: "secondary", tone: "Light", swatch: ["#fdf6e3", "#eee8d5", "#268bd2", "#586e75"] },
    { id: "subtle-grey", label: "subtle Grey",      tier: "secondary", tone: "Light", swatch: ["#f8f9fa", "#ffffff", "#4a5d5e", "#212529"] },
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
      fetch(BASE_PATH + "/api/preferences", {
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
      const r = await fetch(BASE_PATH + "/api/preferences", { headers: authHeaders() });
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
  const ACTIVE_AGENT_KEY = "agent_toolkit_active_agent";
  const TOOL_GROUPS = ["Bash", "Read", "Write", "Edit", "Grep", "Glob", "revert", "mime", "mcp", "Skill", "softskills", "calc", "ddg", "serpapi", "web", "registries"];
  const TOOL_DESCRIPTIONS = {
    Bash:       "Run shell commands in the working directory.",
    Read:       "Read file contents from the filesystem.",
    Write:      "Write or overwrite files on the filesystem.",
    Edit:       "Surgical in-place string replacement in a file.",
    Grep:       "Search file contents with regular expressions.",
    Glob:       "Find files by path pattern (glob).",
    revert:     "Revert a file to its last snapshot.",
    mime:       "Detect the MIME type of a file.",
    mcp:        "MCP (Model Context Protocol) tools: connect to external MCP servers defined in mcp_config.json.",
    Skill:      "Skill tools: load and list skill playbooks from the skills/ directory.",
    softskills: "Soft-skill tools: load and list curator-distilled procedures from the softskills/ directory.",
    calc:       "Calculator: evaluate mathematical expressions (arithmetic, sqrt, trig, log, pow…).",
    ddg:        "Web search: search the web via DuckDuckGo (no API key required).",
    serpapi:    "Web search: search the web via SerpAPI (Google). Requires serpapi_key in globals. Cannot be used together with ddg.",
    web:        "Web tools: fetch a web page as Markdown (web_fetch) or convert an HTML string to Markdown (html_to_markdown).",
    registries: "Skill registry tools: list configured remote registries, browse them, fetch a SKILL.md, install a skill, and link it to an agent.",
  };
  // Tools that are mutually exclusive: selecting one auto-deselects the other.
  const TOOL_MUTEX = { ddg: "serpapi", serpapi: "ddg" };

  const AGENT_SUBTABS = [
    { id: "agents",  label: "Agents"  },
    { id: "squads",  label: "Squads"  },
    { id: "remotes", label: "Remotes" },
    { id: "globals", label: "Global Environment" },
  ];

  const MODELS_SUBTABS = [
    { id: "providers", label: "Providers" },
    { id: "models",    label: "Models"    },
  ];

  const SKILLS_SUBTABS = [
    { id: "installed", label: "Installed" },
    { id: "remotes",   label: "Remotes"   },
  ];

  const MCP_SUBTABS = [
    { id: "servers", label: "Servers" },
    { id: "remotes", label: "Remotes" },
  ];

  const A2A_SUBTABS = [
    { id: "agents",  label: "Agents" },
    { id: "remotes", label: "Remotes" },
  ];

  const state = {
    activeFile: "skills",
    activeView: "form", // 'form' | 'raw'
    activeAgentSubtab: "agents", // only used when activeFile === 'agent'
    activeModelsSubtab: "models", // only used when activeFile === 'models'
    activeProviderName: null,     // selected provider in the Providers sub-tab
    activeSkillsSubtab: "installed", // only used when activeFile === 'skills'
    activeMCPSubtab: "servers",    // only used when activeFile === 'mcp'
    activeA2ASubtab: "agents",     // only used when activeFile === 'a2a'
    activeAgentIdx: 0,            // selected agent in the fleet list
    activeAgentInitialized: false, // true once localStorage restore has been attempted
    activeSquadIdx: 0,            // selected squad in the squads list
    raw: {}, // id → { content, mtime, dirty, value }
    parsed: {}, // id → { data, mtime, dirty, value }
    open: false,
    skills: { editing: null, browsingRemote: null, viewingRemote: null }, // skills panel state
    mcpRemotes: { browsing: null, viewing: null }, // MCP remotes panel state
    a2aRemotes: { browsing: null }, // A2A remotes panel state
    squadRemotes: { browsing: null }, // Squad remotes panel state
    activeRemoteKind: "agents",       // Selected kind in the Registries left panel
    docs: { activePage: "getting-started", cache: {} }, // documentation viewer state
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
  // The reload button hot-reloads the agent generation in place (no
  // downtime, no SSE interruption — in-flight sessions stay on their
  // current generation, new sessions get the reloaded config). The
  // restart button remains as the escape hatch for changes that the
  // hot-reload path cannot apply (env vars, binary updates).
  function ensureBanner() {
    let b = document.getElementById("restart-banner");
    if (b) return b;
    b = document.createElement("div");
    b.id = "restart-banner";
    b.hidden = true;
    b.innerHTML = `
      <span class="restart-banner-text">
        Configuration changed — apply with hot-reload (no downtime) or restart the server.
      </span>
      <button type="button" id="restart-banner-reload" class="reload-primary">Reload</button>
      <button type="button" id="restart-banner-btn">Restart server</button>
      <button type="button" id="restart-banner-dismiss" title="Dismiss">×</button>
    `;
    const main = document.getElementById("chat");
    main.insertBefore(b, main.firstChild);
    b.querySelector("#restart-banner-reload").addEventListener("click", () => doReload());
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
    // Clear any leftover fade-out state from a previous successful reload
    // before un-hiding, otherwise the banner would appear in its 0-opacity
    // / 0-height collapsed state.
    b.classList.remove("is-fading-out");
    b.hidden = false;
  }

  function refreshBannerVisibility() {
    if (localStorage.getItem(RESTART_FLAG) !== "1") return;
    if (localStorage.getItem(BANNER_DISMISS_FLAG) === "1") return;
    const b = ensureBanner();
    b.classList.remove("is-fading-out");
    b.hidden = false;
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

  // doReload performs a hot-reload of the agent generation without
  // restarting the server. New sessions immediately use the reloaded
  // config; in-flight sessions stay on the previous generation until
  // they finish (draining), so streams are never interrupted.
  //
  // UX: the banner enters a loading state (spinner on the Reload button,
  // both action buttons disabled), then fades out on success. On error
  // the banner stays put so the user can see the failure reason inline
  // and retry.
  async function doReload() {
    const banner = document.getElementById("restart-banner");
    const reloadBtn = banner && banner.querySelector("#restart-banner-reload");
    const restartBtn = banner && banner.querySelector("#restart-banner-btn");
    const textEl = banner && banner.querySelector(".restart-banner-text");
    const origText = textEl ? textEl.textContent : "";
    const origReloadHtml = reloadBtn ? reloadBtn.innerHTML : "";

    const setLoading = (on) => {
      if (!banner) return;
      banner.classList.toggle("is-loading", !!on);
      if (reloadBtn) {
        reloadBtn.disabled = !!on;
        reloadBtn.innerHTML = on
          ? '<span class="reload-spinner" aria-hidden="true"></span>Reloading…'
          : origReloadHtml;
      }
      if (restartBtn) restartBtn.disabled = !!on;
    };

    if (hasAnyUnsaved()) {
      if (!await appConfirm("You have unsaved changes. Save them before reloading?")) return;
      await saveActive();
      if (hasUnsavedActive()) return;
    }

    setLoading(true);
    setStatus("Reloading agent…");
    try {
      const r = await fetch(BASE_PATH + "/api/config/reload", { method: "POST", headers: authHeaders() });
      if (!r.ok) {
        const j = await r.json().catch(() => ({}));
        throw new Error(j.error || `HTTP ${r.status}`);
      }
      const body = await r.json().catch(() => ({}));
      localStorage.removeItem(RESTART_FLAG);
      localStorage.removeItem(BANNER_DISMISS_FLAG);

      const draining = body.draining_sessions || 0;
      const summary = draining > 0
        ? `Reloaded — generation ${body.generation}. ${draining} session(s) draining on previous version.`
        : `Reloaded — generation ${body.generation}.`;
      setStatus(summary);

      // Animate the banner out before hiding it. The is-fading-out class
      // stays on the element while hidden so the reverse transition can
      // never play; showBanner clears the class on re-show.
      if (banner) {
        if (textEl) textEl.textContent = "Reloaded — new sessions will use the updated configuration.";
        setLoading(false);
        banner.classList.add("is-fading-out");
        let done = false;
        const onEnd = (e) => {
          // transitionend fires once per animated property; ignore repeats.
          if (done) return;
          // Only react to the property we expect to last the full duration.
          if (e && e.propertyName && e.propertyName !== "opacity") return;
          done = true;
          banner.removeEventListener("transitionend", onEnd);
          banner.hidden = true;
          if (textEl) textEl.textContent = origText;
        };
        banner.addEventListener("transitionend", onEnd);
        // Fallback for prefers-reduced-motion or browsers that suppress
        // transitionend (e.g. tab in background).
        setTimeout(() => onEnd(), 400);
      }
      refreshGenerationPill();
      // Invalidate the cached MCP config so switching to the Servers subtab
      // re-fetches the freshly-reloaded data. Re-render immediately if the
      // user is already viewing the MCP settings.
      delete state.parsed["mcp"];
      if (state.activeFile === "mcp") renderBody();
      // Notify the main app shell so it can refresh anything cached from the
      // server side (squad picker, etc.) without a full page reload.
      window.dispatchEvent(new CustomEvent("yoke:config-reloaded", { detail: { generation: body.generation } }));
    } catch (e) {
      setLoading(false);
      if (textEl) {
        textEl.textContent = "Reload failed: " + e.message + " — fix the configuration and try again.";
      }
      setStatus("Reload failed: " + e.message);
    }
  }

  // refreshGenerationPill polls /api/config/status and updates a small
  // pill in the header that shows the current generation + number of
  // sessions still draining on previous generations. The pill is hidden
  // entirely when nothing is draining.
  let generationPollHandle = null;
  async function refreshGenerationPill() {
    try {
      const r = await fetch(BASE_PATH + "/api/config/status", { headers: authHeaders() });
      if (!r.ok) return;
      const body = await r.json();
      let pill = document.getElementById("generation-pill");
      if (!pill) {
        pill = document.createElement("span");
        pill.id = "generation-pill";
        pill.className = "generation-pill";
        const anchor = document.querySelector("#header-actions") || document.querySelector("header");
        if (anchor) anchor.appendChild(pill); else document.body.appendChild(pill);
      }
      const draining = body.draining_sessions || 0;
      if (draining > 0) {
        pill.textContent = `gen ${body.generation} · ${draining} draining`;
        pill.hidden = false;
        if (!generationPollHandle) {
          generationPollHandle = setInterval(refreshGenerationPill, 5000);
        }
      } else {
        pill.hidden = true;
        if (generationPollHandle) {
          clearInterval(generationPollHandle);
          generationPollHandle = null;
        }
      }
    } catch (_) { /* ignore */ }
  }

  async function doRestart() {
    if (!await appConfirm("Restart the yoke server now? Active streams will be interrupted.")) return;
    setStatus("Restarting…");
    showRestartingOverlay("Server is restarting…\nThe page will reload automatically.");
    try {
      const r = await fetch(BASE_PATH + "/api/server/restart", { method: "POST", headers: authHeaders() });
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
          const h = await fetch(BASE_PATH + "/api/health");
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
              <button type="button" data-view="raw">Raw JSON</button>
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
    // Raw JSON is appended last so new section entries inserted into
    // MENU_ITEMS always render above it.
    const raw = document.createElement("li");
    raw.dataset.file = RAW_VIEW_ID;
    raw.className = "settings-menu-raw";
    raw.innerHTML = `${ICONS.raw}<span>Raw JSON</span>`;
    raw.addEventListener("click", () => {
      if (raw.classList.contains("disabled")) return;
      toggleRawView();
    });
    sidebarMenuListEl.appendChild(raw);
  }

  // Toggle between form and raw view for the currently active JSON file.
  // No-op for client-only sections (e.g. Appearance) — they have no JSON.
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
      ? `${base} › Raw JSON`
      : base;
  }

  async function setActiveFile(id) {
    if (state.activeFile !== id && hasUnsavedActive() &&
        !await appConfirm("Discard unsaved changes in the current tab?")) {
      return;
    }
    state.activeFile = id;
    // Switching sections always returns to the form view; raw is opt-in
    // per visit via the sidebar Raw JSON entry.
    state.activeView = "form";
    // Clicking "Skills" always resets sub-navigation back to the root list.
    if (id === "skills") {
      state.skills.editing = null;
      state.skills.browsingRemote = null;
      state.skills.viewingRemote = null;
    }
    if (id === "mcp") {
      state.mcpRemotes.browsing = null;
      state.mcpRemotes.viewing = null;
    }
    if (id === "a2a") {
      state.a2aRemotes.browsing = null;
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
    if (state.activeFile === DOCUMENTATION_ID) return false;
    if (state.activeView === "raw") {
      const r = state.raw[state.activeFile];
      return r && r.dirty;
    }
    const p = state.parsed[state.activeFile];
    return p && p.dirty;
  }

  function hasAnyUnsaved() {
    for (const id of Object.keys(state.raw)) if (state.raw[id].dirty) return true;
    for (const id of Object.keys(state.parsed)) if (state.parsed[id].dirty) return true;
    return false;
  }

  // True for menu entries with no server-side JSON — these hide the
  // Form/Raw toggle and the Save/Discard footer.
  function isClientOnly(id) {
    return id === APPEARANCE_ID || id === "skills" || id === DOCUMENTATION_ID || id === USER_COMMANDS_ID;
  }

  function applyClientOnlyChrome() {
    const clientOnly = isClientOnly(state.activeFile);
    panelEl.classList.toggle("settings-panel--client-only", clientOnly);
  }

  // ─── Loading ───────────────────────────────────────────────────────────
  async function loadRaw(id) {
    const r = await fetch(BASE_PATH + `/api/config/file/${id}`, { headers: authHeaders() });
    if (!r.ok) throw new Error(await errText(r));
    const j = await r.json();
    state.raw[id] = { content: j.content || "", mtime: j.mtime, dirty: false, value: j.content || "" };
  }

  async function loadParsed(id) {
    const r = await fetch(BASE_PATH + `/api/config/parsed/${id}`, { headers: authHeaders() });
    if (!r.ok) throw new Error(await errText(r));
    const j = await r.json();
    const data = j.data == null ? defaultDataFor(id) : j.data;
    state.parsed[id] = { data, mtime: j.mtime, dirty: false, value: deepClone(data) };
  }

  // loadBuiltinAgents fetches the embedded default description + system
  // instruction for each built-in agent (leader, investigator, web_agent,
  // summariser, curator). Cached for the lifetime of the page. Failures
  // degrade to "no built-in metadata" — the UI then treats every agent as
  // user-configured.
  async function loadBuiltinAgents() {
    if (state.builtinAgents) return;
    state.builtinAgents = {};
    state.builtinNames = new Set();
    try {
      const r = await fetch(BASE_PATH + "/api/agent/builtin-defaults", { headers: authHeaders() });
      if (!r.ok) return;
      const j = await r.json();
      state.builtinAgents = j.agents || {};
      state.builtinNames = new Set(j.names || Object.keys(state.builtinAgents));
    } catch { /* network error — fall back to no built-ins */ }
  }

  function isBuiltinAgent(name) {
    return !!(state.builtinNames && state.builtinNames.has(name));
  }

  async function errText(r) {
    try { const j = await r.json(); return j.error || `HTTP ${r.status}`; }
    catch { return `HTTP ${r.status}`; }
  }

  function defaultDataFor(id) {
    if (id === "agent") return { agents: [] };
    if (id === "models") return { providers: {}, models: {} };
    if (id === "permissions") return { always_deny: [], always_allow: [], ask_user: [] };
    if (id === "mcp") return { servers: {}, inputs: [] };
    if (id === "a2a") return { agents: {}, inputs: [] };
    return {};
  }

  // prepareForSave returns a clean copy of the form value with fields
  // that don't apply to the current shape stripped out, so saving doesn't
  // accumulate cruft from prior edits (e.g. an http server keeping a
  // legacy empty `command`/`args`/`env` from when it was stdio).
  //
  // For MCP the on-disk shape follows VS Code's mcp.json:
  //   { "servers": { <name>: <Server> }, "inputs": [<Input>] }
  function prepareForSave(id, value) {
    const v = deepClone(value);
    if (id === "a2a") {
      const agents = (v.agents && typeof v.agents === "object" && !Array.isArray(v.agents)) ? v.agents : {};
      const cleanAgents = {};
      Object.entries(agents).forEach(([name, a]) => {
        if (!name || !a) return;
        const out = {};
        if (a.url) out.url = a.url;
        if (a.description) out.description = a.description;
        if (a.headers && Object.keys(a.headers).length) out.headers = a.headers;
        cleanAgents[name] = out;
      });
      const cleanInputs = Array.isArray(v.inputs) ? v.inputs.map(inp => {
        const o = { id: inp.id || "", type: (inp.type || "promptString") };
        if (inp.description) o.description = inp.description;
        if (inp.password) o.password = true;
        if (inp.default) o.default = inp.default;
        if (o.type === "pickString" && Array.isArray(inp.options) && inp.options.length) {
          o.options = inp.options.filter(s => s);
        }
        return o;
      }).filter(o => o.id) : [];
      const out = { agents: cleanAgents };
      if (cleanInputs.length) out.inputs = cleanInputs;
      return out;
    }
    if (id !== "mcp") return v;
    const servers = (v.servers && typeof v.servers === "object" && !Array.isArray(v.servers)) ? v.servers : {};
    const cleanServers = {};
    Object.entries(servers).forEach(([name, s]) => {
      if (!name) return;
      const type = (s && s.type || "").toLowerCase() === "http" ? "http" : "stdio";
      const out = {};
      if (type === "http") {
        out.type = "http";
        if (s.url) out.url = s.url;
        if (s.headers && Object.keys(s.headers).length) out.headers = s.headers;
      } else {
        // Omit `type` for stdio (the default) so configs stay terse.
        if (s.command) out.command = s.command;
        if (Array.isArray(s.args) && s.args.length) out.args = s.args;
        if (s.env && Object.keys(s.env).length) out.env = s.env;
      }
      cleanServers[name] = out;
    });
    const cleanInputs = Array.isArray(v.inputs) ? v.inputs.map(inp => {
      const o = { id: inp.id || "", type: (inp.type || "promptString") };
      if (inp.description) o.description = inp.description;
      if (inp.password) o.password = true;
      if (inp.default) o.default = inp.default;
      if (o.type === "pickString" && Array.isArray(inp.options) && inp.options.length) {
        o.options = inp.options.filter(s => s);
      }
      return o;
    }).filter(o => o.id) : [];
    const out = { servers: cleanServers };
    if (cleanInputs.length) out.inputs = cleanInputs;
    return out;
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
      else if (id === DOCUMENTATION_ID) renderDocumentation();
      else if (id === USER_COMMANDS_ID) renderUserCommands();
      return;
    }
    try {
      if (state.activeView === "raw") {
        if (!state.raw[id]) await loadRaw(id);
        renderRaw(id);
      } else {
        if (!state.parsed[id]) await loadParsed(id);
        await renderForm(id);
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

  // ─── User commands editor ──────────────────────────────────────────────
  // Lists the built-in slash commands as read-only context, then a
  // CRUD view of the user-defined commands persisted via
  // /api/user-commands. Editing reuses the inline modal defined in
  // app.js (window.UserCommands.openModal).
  async function renderUserCommands() {
    const UC = window.UserCommands;
    if (!UC) {
      bodyEl.innerHTML = `<p class="settings-error">User commands API not available.</p>`;
      return;
    }
    await UC.refresh();
    paintUserCommands(UC);
    // Repaint when the underlying list changes (modal save, delete, etc.),
    // but only while this section is still the active view. The listener
    // accumulates across navigations; the guard makes that harmless.
    if (!state._userCmdListenerWired) {
      state._userCmdListenerWired = true;
      UC.onChanged(() => {
        if (state.activeFile === USER_COMMANDS_ID && state.open) {
          paintUserCommands(UC);
        }
      });
    }
  }

  function paintUserCommands(UC) {
    const builtins = UC.builtins();
    const commands = UC.list();

    const builtinRows = builtins.map(b => `
      <tr>
        <td class="cmd-name">${escHtml(b.cmd)}</td>
        <td class="cmd-args">${escHtml(b.args || "")}</td>
        <td class="cmd-desc">${escHtml(b.desc || "")}</td>
      </tr>
    `).join("");

    const userRows = commands.length === 0
      ? `<tr><td colspan="4" class="cmd-empty">No user commands yet. Click "Add command" to create one.</td></tr>`
      : commands.map(c => `
        <tr data-name="${escHtml(c.name)}">
          <td class="cmd-name">/${escHtml(c.name)}</td>
          <td class="cmd-args">${escHtml(c.args || "")}</td>
          <td class="cmd-desc">${escHtml(c.description || "")}</td>
          <td class="cmd-actions">
            <button type="button" class="btn-edit" data-name="${escHtml(c.name)}">Edit</button>
            <button type="button" class="btn-del" data-name="${escHtml(c.name)}">Delete</button>
          </td>
        </tr>
      `).join("");

    bodyEl.innerHTML = `
      <div class="settings-form user-cmd-settings">
        <p class="settings-hint" style="margin:0;">
          Slash commands shown in the chat composer. Built-in commands are reserved.
          User commands expand to a prompt template that is sent to the agent — use
          <code>$1</code>, <code>$2</code>, … for positional args and <code>$*</code> for all args.
        </p>

        <section class="form-section">
          <h3>Built-in commands</h3>
          <table class="cmd-table">
            <thead><tr><th>Command</th><th>Args</th><th>Description</th></tr></thead>
            <tbody>${builtinRows}</tbody>
          </table>
        </section>

        <section class="form-section">
          <div class="cmd-section-header">
            <h3 style="margin:0;">User commands</h3>
            <button type="button" id="user-cmd-add-btn" class="primary">+ Add command</button>
          </div>
          <table class="cmd-table">
            <thead><tr><th>Command</th><th>Args</th><th>Description</th><th></th></tr></thead>
            <tbody>${userRows}</tbody>
          </table>
        </section>
      </div>
    `;

    bodyEl.querySelector("#user-cmd-add-btn")?.addEventListener("click", () => {
      UC.openModal(null);
    });
    bodyEl.querySelectorAll(".btn-edit").forEach(btn => {
      btn.addEventListener("click", () => {
        const name = btn.dataset.name;
        const cmd = UC.list().find(c => c.name === name);
        if (cmd) UC.openModal(cmd);
      });
    });
    bodyEl.querySelectorAll(".btn-del").forEach(btn => {
      btn.addEventListener("click", async () => {
        const name = btn.dataset.name;
        if (!await appConfirm(`Remove command "/${name}"?`)) return;
        try { await UC.remove(name); }
        catch (e) { await appConfirm(`Delete failed: ${e.message}`); }
      });
    });
  }

  // ─── Documentation viewer ──────────────────────────────────────────────
  // Two-pane layout: a sticky TOC on the left and a markdown-rendered article
  // on the right. Pages are fetched lazily from /assets/docs/<file> and cached
  // for the lifetime of the panel.
  async function renderDocumentation() {
    const active = state.docs.activePage;
    const groups = {};
    for (const p of DOC_PAGES) (groups[p.group] = groups[p.group] || []).push(p);

    const tocHTML = Object.entries(groups).map(([group, pages]) => `
      <div class="docs-toc-group">
        <div class="docs-toc-group-label">${escHtml(group)}</div>
        <ul class="docs-toc-list">
          ${pages.map(p => `
            <li class="docs-toc-item ${p.id === active ? "active" : ""}" data-page="${escHtml(p.id)}">
              ${escHtml(p.label)}
            </li>`).join("")}
        </ul>
      </div>
    `).join("");

    bodyEl.innerHTML = `
      <div class="docs-viewer">
        <aside class="docs-toc" aria-label="Documentation table of contents">
          ${tocHTML}
        </aside>
        <article class="docs-article" tabindex="-1">
          <div class="docs-article-body">
            <p class="settings-loading">Loading…</p>
          </div>
        </article>
      </div>
    `;

    bodyEl.querySelectorAll(".docs-toc-item").forEach(li => {
      li.addEventListener("click", () => {
        const id = li.dataset.page;
        if (id === state.docs.activePage) return;
        state.docs.activePage = id;
        bodyEl.querySelectorAll(".docs-toc-item").forEach(el => {
          el.classList.toggle("active", el.dataset.page === id);
        });
        loadDocPage(id);
      });
    });

    await loadDocPage(active);
  }

  async function loadDocPage(id) {
    const page = DOC_PAGES.find(p => p.id === id) || DOC_PAGES[0];
    const host = bodyEl.querySelector(".docs-article-body");
    const article = bodyEl.querySelector(".docs-article");
    if (!host) return;
    let text = state.docs.cache[page.id];
    if (text == null) {
      try {
        const r = await fetch(BASE_PATH + `/assets/docs/${page.file}`, { headers: authHeaders() });
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        text = await r.text();
        state.docs.cache[page.id] = text;
      } catch (e) {
        host.innerHTML = `<p class="settings-error">Could not load documentation: ${escHtml(e.message)}</p>`;
        return;
      }
    }
    if (typeof marked !== "undefined") {
      // Disable `breaks` so soft-wrapped lines in the .md source don't become
      // forced <br> in the rendered article — paragraphs should reflow to the
      // container width. The global `breaks: true` set in app.js is intended
      // for streaming chat output, not hand-authored documentation.
      host.innerHTML = marked.parse(text, { breaks: false, gfm: true });
    } else {
      host.textContent = text;
    }
    // Reset scroll so each page starts at the top. The actual scroll
    // container is the panel's body, not the article element itself.
    const scroller = panelEl?.querySelector(".settings-body-content");
    if (scroller) scroller.scrollTop = 0;
    if (article) article.focus({ preventScroll: true });
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
    if (id === "models") return renderModelsForm();
    if (id === "permissions") return renderPermissionsForm();
    if (id === "mcp") return renderMCPForm();
    if (id === "a2a") return renderA2AForm();
  }

  function markFormDirty(id) {
    state.parsed[id].dirty = JSON.stringify(state.parsed[id].value) !== JSON.stringify(state.parsed[id].data);
    updateFooter();
  }

  // ── agent.json form ──
  async function renderAgentForm() {
    await loadBuiltinAgents();
    const id = "agent";
    const d = state.parsed[id].value;
    if (!Array.isArray(d.agents)) d.agents = [];
    if (!Array.isArray(d.squads)) d.squads = [];
    // Models now live in models.json; ensure the parsed cache is warm so
    // the agent's "Model Reference" dropdown sees the catalogue without a
    // race against its own render.
    if (!state.parsed.models) {
      try { await loadParsed("models"); }
      catch { /* missing models.json is fine — dropdown shows (none) */ }
    }

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
        if (state.activeAgentSubtab === b.dataset.subtab) {
          if (b.dataset.subtab === "remotes" && state.agentRemotes &&
              (state.agentRemotes.browsing || state.agentRemotes.viewing || state.squadRemotes?.browsing)) {
            state.agentRemotes = { browsing: null, viewing: null };
            state.squadRemotes = { browsing: null };
            renderAgentForm();
          }
          return;
        }
        if (b.dataset.subtab === "remotes") {
          state.agentRemotes = { browsing: null, viewing: null };
          state.squadRemotes = { browsing: null };
        }
        state.activeAgentSubtab = b.dataset.subtab;
        renderAgentForm();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "globals") {
      host.innerHTML = `<div id="agent-globals-host" class="env-sections"></div>`;
      renderAgentGlobals(d);
    } else if (sub === "squads") {
      // Surface a synthesised `default` squad in the editor whenever it is
      // missing, so users see what the server already provides as fallback
      // and never end up saving a squads list without one. Marking the
      // form dirty here writes the default into agent.json on Save —
      // intentional, so it round-trips cleanly through the JSON view.
      if (!d.squads.some(sq => (sq.name || "").toLowerCase() === "default")) {
        d.squads.unshift(synthesizeDefaultSquad(d.agents));
        markFormDirty(id);
      }
      host.innerHTML = `
        <div class="agent-split-layout">
          <div class="agent-fleet-panel">
            <div class="agent-fleet-header">
              <span class="agent-fleet-title">SQUADS</span>
              <button type="button" class="agent-fleet-add" id="add-squad" title="Add squad">+</button>
            </div>
            <div class="agent-fleet-list" id="squad-list"></div>
          </div>
          <div class="agent-detail-panel" id="squad-detail-panel"></div>
        </div>
      `;
      bodyEl.querySelector("#add-squad").addEventListener("click", () => {
        const isLeader = a => !!a.leader || (a.name || "").toLowerCase() === "leader";
        const leaderName = (d.agents.find(a => isLeader(a)) || d.agents.find(a => a.name === "leader") || d.agents[0] || { name: "leader" }).name;
        d.squads.push({ name: "new-squad", description: "", leader: leaderName, members: [] });
        state.activeSquadIdx = d.squads.length - 1;
        markFormDirty(id);
        renderAgentSquads(d);
      });
      renderAgentSquads(d);
    } else if (sub === "remotes") {
      renderAgentRemotesTab(d, host);
    } else {
      host.innerHTML = `
        <div class="agent-split-layout">
          <div class="agent-fleet-panel">
            <div class="agent-fleet-header">
              <span class="agent-fleet-title">ACTIVE FLEET</span>
              <button type="button" class="agent-fleet-import" id="import-agent" title="Import Claude Code agent (.md / .json)">&#8595;</button>
              <button type="button" class="agent-fleet-add" id="add-agent" title="Add agent">+</button>
            </div>
            <div class="agent-fleet-list" id="agent-fleet-list"></div>
          </div>
          <div class="agent-detail-panel" id="agent-detail-panel"></div>
        </div>
      `;
      bodyEl.querySelector("#add-agent").addEventListener("click", () => {
        d.agents.push({ name: "new-agent", enabled: true, tools: [] });
        state.activeAgentIdx = d.agents.length - 1;
        markFormDirty(id);
        renderAgentAgents(d);
      });
      bodyEl.querySelector("#import-agent").addEventListener("click", async () => {
        const result = await importAgentDialog();
        if (!result) return;
        try {
          const res = await skillsPost("/agents/import", { content: result.content, enable: result.enable });
          const names = (res.agents || []).map(a => a.name).join(", ");
          const anyEnabled = (res.agents || []).some(a => a.enabled);
          if (anyEnabled) {
            await doReload();
            await loadParsed("agent");
            state.activeAgentSubtab = "agents";
            const lastName = (res.agents || []).filter(a => a.enabled).map(a => a.name).pop();
            const newAgents = (state.parsed["agent"].value || {}).agents || [];
            const newIdx = lastName ? newAgents.findIndex(a => a.name === lastName) : newAgents.length - 1;
            state.activeAgentIdx = newIdx >= 0 ? newIdx : newAgents.length - 1;
            renderAgentForm();
          } else {
            setStatus(`Imported: ${names}.`, "success");
          }
        } catch (e) {
          setStatus("Import failed: " + e.message, "error");
        }
      });
      renderAgentAgents(d);
    }
    updateFooter();
  }

  // synthesizeDefaultSquad mirrors the server-side logic for the editor:
  // build a `default` squad from the enabled agents so the user always sees
  // it in the Squads sub-tab — even when the JSON file has no squads block.
  function synthesizeDefaultSquad(agents) {
    const enabled = (Array.isArray(agents) ? agents : [])
      .filter(a => a && a.name && (a.enabled === undefined || a.enabled));
    let leader = (enabled.find(a => (a.name || "").toLowerCase() === "leader") || enabled[0] || { name: "leader" }).name;
    const members = enabled
      .map(a => a.name)
      .filter(n => n && n.toLowerCase() !== "leader" && n.toLowerCase() !== "curator");
    return {
      name: "default",
      description: "General-purpose squad — automatically generated.",
      leader,
      members,
    };
  }

  // ── Squads sub-tab: list + detail editor ──
  // Squads compose existing agents (by name) into named profiles. The
  // editor mirrors the Agents sub-tab visually: a left-hand list of
  // squads with an inline detail panel for the selected entry.
  function renderAgentSquads(d) {
    const id = "agent";
    const listEl = bodyEl.querySelector("#squad-list");
    if (!listEl) return;
    if (!Array.isArray(d.squads)) d.squads = [];
    if (state.activeSquadIdx >= d.squads.length) state.activeSquadIdx = Math.max(0, d.squads.length - 1);

    listEl.innerHTML = "";
    d.squads.forEach((sq, idx) => {
      const item = document.createElement("div");
      item.className = "agent-fleet-item" + (idx === state.activeSquadIdx ? " active" : "");
      const isDefault = (sq.name || "").toLowerCase() === "default";
      const memberCount = Array.isArray(sq.members) ? sq.members.length : 0;
      item.innerHTML = `
        <div class="agent-fleet-item-name">${escHtml(sq.name || "(unnamed)")} ${isDefault ? '<span class="squad-default-tag">default</span>' : ""}</div>
        <div class="agent-fleet-item-meta">${memberCount} member${memberCount === 1 ? "" : "s"}</div>
      `;
      item.addEventListener("click", () => { state.activeSquadIdx = idx; renderAgentSquads(d); });
      listEl.appendChild(item);
    });

    renderSquadDetail(d, state.activeSquadIdx);
  }

  function renderSquadDetail(d, idx) {
    const panel = bodyEl.querySelector("#squad-detail-panel");
    if (!panel) return;
    if (!Array.isArray(d.squads) || d.squads.length === 0) {
      panel.innerHTML = `<div class="agent-detail-empty">No squads defined. Click + to add one.</div>`;
      return;
    }
    const sq = d.squads[idx];
    if (!sq) {
      panel.innerHTML = `<div class="agent-detail-empty">Select a squad.</div>`;
      return;
    }
    // Leader candidates: only agents marked `leader: true` (the agent named
    // "leader" is the canonical default — auto-flagged when the field is
    // absent, matching the server-side resolver).
    const isLeaderAgent = a => !!a.leader || (a.name || "").toLowerCase() === "leader";
    const leaderCandidates = d.agents
      .filter(a => a && a.name && (a.enabled === undefined || a.enabled) && (a.name || "").toLowerCase() !== "curator" && isLeaderAgent(a))
      .map(a => a.name);
    const memberCandidates = d.agents
      .filter(a => a && a.name && (a.enabled === undefined || a.enabled) && (a.name || "").toLowerCase() !== "curator");
    const members = Array.isArray(sq.members) ? sq.members : [];
    const isDefault = (sq.name || "").toLowerCase() === "default";

    // Sort: selected members first (in the order they appear in `members`),
    // then the rest. The leader of the squad is forced last and rendered
    // as disabled — a squad cannot list its own leader as a member.
    const memberOrder = (a) => {
      if (a.name === sq.leader) return 2;
      return members.includes(a.name) ? 0 : 1;
    };
    const sortedMembers = [...memberCandidates].sort((a, b) => {
      const ra = memberOrder(a), rb = memberOrder(b);
      if (ra !== rb) return ra - rb;
      if (ra === 0) return members.indexOf(a.name) - members.indexOf(b.name);
      return 0;
    });

    const agentIcon = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/></svg>`;

    panel.innerHTML = `
      <div class="agent-detail-section">
        <div class="agent-detail-field">
          <label class="agent-detail-label">Name</label>
          <input type="text" class="agent-detail-input" id="squad-name" value="${escHtml(sq.name || "")}" ${isDefault ? "disabled" : ""} />
          ${isDefault ? '<div class="agent-detail-hint">The default squad is required and its name cannot be changed.</div>' : ""}
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">Description</label>
          <input type="text" class="agent-detail-input" id="squad-desc" value="${escHtml(sq.description || "")}" placeholder="What this squad is for" />
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">Leader</label>
          <select class="agent-detail-input" id="squad-leader">
            ${leaderCandidates.map(n => `<option value="${escHtml(n)}" ${n === sq.leader ? "selected" : ""}>${escHtml(n)}</option>`).join("")}
          </select>
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">Members</label>
          <div class="agent-tools-grid" id="squad-members">
            ${sortedMembers.map(a => {
              const isOn = members.includes(a.name);
              const isLeaderRow = a.name === sq.leader;
              const desc = a.description || "";
              return `
              <div class="agent-tool-card${isOn ? " tool-on" : ""}${isLeaderRow ? " tool-disabled" : ""}" data-name="${escHtml(a.name)}" title="${escHtml(desc)}">
                <div class="agent-tool-icon">${agentIcon}</div>
                <div class="agent-tool-info">
                  <span class="agent-tool-name">${escHtml(a.name)}</span>
                  <span class="agent-tool-desc">${escHtml(desc)}</span>
                </div>
                <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
              </div>
            `;
            }).join("")}
          </div>
          <div class="agent-detail-hint">Sub-agents the leader can delegate to (the leader itself is not selectable).</div>
        </div>
        ${!isDefault ? `<div class="squad-detail-actions"><button type="button" class="agent-detail-remove" id="squad-remove">Delete squad</button></div>` : ""}
      </div>
    `;

    const onChange = () => { markFormDirty("agent"); };

    const nameInput = panel.querySelector("#squad-name");
    if (nameInput && !isDefault) {
      nameInput.addEventListener("input", () => {
        sq.name = nameInput.value;
        // Re-render the list so the label tracks the input. Keep selection.
        renderAgentSquads(d);
        // Restore focus / caret to the still-mounted input.
        const ref = bodyEl.querySelector("#squad-name");
        if (ref) { ref.focus(); ref.setSelectionRange(ref.value.length, ref.value.length); }
        onChange();
      });
    }
    panel.querySelector("#squad-desc").addEventListener("input", (e) => {
      sq.description = e.target.value; onChange();
    });
    panel.querySelector("#squad-leader").addEventListener("change", (e) => {
      sq.leader = e.target.value;
      // Drop the new leader from the members list (a squad cannot list its
      // own leader as a member). Re-render so the disabled state updates.
      if (Array.isArray(sq.members)) {
        sq.members = sq.members.filter(m => m !== sq.leader);
      }
      onChange();
      renderSquadDetail(d, idx);
    });
    panel.querySelectorAll("#squad-members .agent-tool-card").forEach(card => {
      if (card.classList.contains("tool-disabled")) return;
      card.addEventListener("click", () => {
        const name = card.dataset.name;
        if (!Array.isArray(sq.members)) sq.members = [];
        if (sq.members.includes(name)) {
          sq.members = sq.members.filter(m => m !== name);
        } else {
          sq.members.push(name);
        }
        onChange();
        renderAgentSquads(d);
      });
    });
    if (!isDefault) {
      panel.querySelector("#squad-remove").addEventListener("click", async () => {
        if (!await appConfirm(`Delete squad "${sq.name}"?`)) return;
        d.squads.splice(idx, 1);
        state.activeSquadIdx = Math.max(0, idx - 1);
        markFormDirty("agent");
        renderAgentSquads(d);
      });
    }
  }

  function renderAgentGlobals(d) {
    const el = bodyEl.querySelector("#agent-globals-host");
    const onChange = () => markFormDirty("agent");

    function envText(key) {
      const wrap = document.createElement("div");
      wrap.className = "env-field";
      const lbl = document.createElement("label");
      lbl.className = "env-field-label";
      lbl.textContent = key;
      const inp = document.createElement("input");
      inp.type = "text";
      inp.className = "env-field-input";
      inp.value = d[key] == null ? "" : String(d[key]);
      inp.addEventListener("input", () => { d[key] = inp.value; onChange(); });
      wrap.appendChild(lbl);
      wrap.appendChild(inp);
      return wrap;
    }

    function envNum(key) {
      const wrap = document.createElement("div");
      wrap.className = "env-field";
      const lbl = document.createElement("label");
      lbl.className = "env-field-label";
      lbl.textContent = key;
      const inp = document.createElement("input");
      inp.type = "number";
      inp.className = "env-field-input";
      inp.value = d[key] == null ? "" : d[key];
      inp.addEventListener("input", () => {
        const n = inp.value === "" ? undefined : Number(inp.value);
        d[key] = Number.isFinite(n) ? n : undefined;
        onChange();
      });
      wrap.appendChild(lbl);
      wrap.appendChild(inp);
      return wrap;
    }

    function envSection(title, desc, buildFn) {
      const s = document.createElement("div");
      s.className = "env-section";
      const hdr = document.createElement("div");
      hdr.className = "env-section-hdr";
      const ttl = document.createElement("span");
      ttl.className = "env-section-title";
      ttl.textContent = title;
      hdr.appendChild(ttl);
      if (desc) {
        const d2 = document.createElement("p");
        d2.className = "env-section-desc";
        d2.textContent = desc;
        hdr.appendChild(d2);
      }
      s.appendChild(hdr);
      const body = document.createElement("div");
      body.className = "env-section-body";
      buildFn(body);
      s.appendChild(body);
      return s;
    }

    el.innerHTML = "";

    // CORE DIRECTORIES
    el.appendChild(envSection("CORE DIRECTORIES", "Path where soft-skill playbooks are stored.", body => {
      const g = document.createElement("div");
      g.className = "env-grid-2";
      g.appendChild(envText("softskills_dir"));
      body.appendChild(g);
    }));

    // OPTIMIZATION
    el.appendChild(envSection("OPTIMIZATION", null, body => {
      const isOn = !!d.token_optimization;
      const chip = document.createElement("div");
      chip.className = "agent-tool-card env-opt-chip" + (isOn ? " tool-on" : "");
      chip.innerHTML = `
        <div class="agent-tool-icon"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg></div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">token_optimization</span>
          <span class="agent-tool-desc">Reduce token usage</span>
        </div>
        <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
      `;
      chip.addEventListener("click", () => {
        d.token_optimization = !d.token_optimization;
        const on = !!d.token_optimization;
        chip.classList.toggle("tool-on", on);
        chip.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill " + (on ? "pill-on" : "pill-off");
        onChange();
      });
      body.appendChild(chip);
    }));

    // RUNTIME CONFIG
    el.appendChild(envSection("RUNTIME CONFIG", null, body => {
      const g = document.createElement("div");
      g.className = "env-grid-2";
      g.appendChild(envText("bash_output_filters_dir"));
      g.appendChild(envNum("bash_timeout_seconds"));
      g.appendChild(envText("mcp_config_path"));
      g.appendChild(envText("permissions_config_path"));
      body.appendChild(g);
    }));

    // EXTERNAL API KEYS
    el.appendChild(envSection("EXTERNAL API KEYS", null, body => {
      body.className += " env-section-keys";
      const wrap = document.createElement("div");
      wrap.className = "env-field";
      const lbl = document.createElement("label");
      lbl.className = "env-field-label";
      lbl.textContent = "serpapi_key";
      const inputWrap = document.createElement("div");
      inputWrap.className = "env-secret-wrap";
      const inp = document.createElement("input");
      inp.type = "password";
      inp.className = "env-field-input";
      inp.value = d.serpapi_key == null ? "" : String(d.serpapi_key);
      inp.addEventListener("input", () => { d.serpapi_key = inp.value; onChange(); });
      const eye = document.createElement("button");
      eye.type = "button";
      eye.className = "env-secret-eye";
      eye.title = "Show/hide";
      eye.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>`;
      eye.addEventListener("click", () => { inp.type = inp.type === "password" ? "text" : "password"; });
      inputWrap.appendChild(inp);
      inputWrap.appendChild(eye);
      wrap.appendChild(lbl);
      wrap.appendChild(inputWrap);
      body.appendChild(wrap);
    }));
  }

  // renderModelsForm — top-level Models & Providers editor backed by models.json.
  // Providers hold credentials + endpoint; Models reference a provider via
  // provider_ref, with optional per-model overrides (kept off the UI for clarity).
  async function renderModelsForm() {
    const id = "models";
    const d = state.parsed[id].value;
    if (!d.providers || typeof d.providers !== "object") d.providers = {};
    if (!d.models || typeof d.models !== "object") d.models = {};

    const sub = state.activeModelsSubtab;
    bodyEl.innerHTML = `
      <div class="settings-form">
        <div class="settings-subtabs" role="tablist">
          ${MODELS_SUBTABS.map(t => `
            <button type="button" data-subtab="${t.id}" class="${sub === t.id ? "active" : ""}">${escHtml(t.label)}</button>
          `).join("")}
        </div>
        <div class="settings-subtab-body"></div>
      </div>
    `;
    bodyEl.querySelectorAll(".settings-subtabs button").forEach(b => {
      b.addEventListener("click", () => {
        if (state.activeModelsSubtab === b.dataset.subtab) return;
        state.activeModelsSubtab = b.dataset.subtab;
        renderModelsForm();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "providers") renderProvidersPanel(d, host);
    else renderModelsPanel(d, host);
    updateFooter();
  }

  function renderProvidersPanel(d, host) {
    host.innerHTML = `
      <div class="model-panel-header">
        <div>
          <h2 class="model-panel-title">Configured Providers</h2>
          <p class="model-panel-desc">A provider groups a kind (anthropic, openai, openai_compat, gemini) with a base URL and API key. Models reference a provider and inherit its credentials.</p>
        </div>
        <button type="button" class="add-btn model-add-btn" id="add-provider">+ Add provider</button>
      </div>
      <div id="providers-grid"></div>
    `;
    host.querySelector("#add-provider").addEventListener("click", async () => {
      let name = await appPrompt("New provider name:");
      if (!name) return;
      name = name.trim().toLowerCase();
      if (!name || d.providers[name]) return;
      d.providers[name] = { kind: "openai_compat", base_url: "", api_key: "" };
      state.activeProviderName = name;
      markFormDirty("models");
      renderProvidersPanel(d, host);
    });
    renderProviderCards(d, host.querySelector("#providers-grid"));
  }

  function renderProviderCards(d, el) {
    el.innerHTML = "";
    const grid = document.createElement("div");
    grid.className = "model-cards-grid";
    const names = Object.keys(d.providers);

    names.forEach(name => {
      const p = d.providers[name] || {};
      const onChange = () => markFormDirty("models");
      const card = document.createElement("div");
      card.className = "model-card";
      card.innerHTML = `
        <div class="model-card-hdr">
          <div class="model-card-title">
            <span class="model-status-dot dot-active"></span>
            <strong>${escHtml(name.toUpperCase())}</strong>
          </div>
          <div>
            <button type="button" class="model-test-link" title="Fetch model list from this provider">⟳ TEST</button>
            <button type="button" class="model-remove-link">⏷ REMOVE</button>
          </div>
        </div>
        <div class="model-card-body"></div>
      `;
      const body = card.querySelector(".model-card-body");
      const fg = document.createElement("div");
      fg.className = "model-field-grid";

      // KIND (select)
      const kindF = document.createElement("div");
      kindF.className = "model-field";
      const kindLbl = document.createElement("label");
      kindLbl.className = "model-field-label";
      kindLbl.textContent = "KIND";
      const kindSel = document.createElement("select");
      kindSel.className = "model-field-input";
      ["anthropic", "openai", "openai_compat", "gemini"].forEach(k => {
        const opt = document.createElement("option");
        opt.value = k; opt.textContent = k;
        if ((p.kind || "openai_compat") === k) opt.selected = true;
        kindSel.appendChild(opt);
      });
      kindSel.addEventListener("change", () => { p.kind = kindSel.value; onChange(); });
      kindF.appendChild(kindLbl); kindF.appendChild(kindSel);
      fg.appendChild(kindF);

      // BASE URL
      const urlF = textField("base_url", p.base_url, v => { p.base_url = v; onChange(); });
      urlF.classList.add("model-field-full");
      fg.appendChild(urlF);

      // API KEY
      fg.appendChild(secretField("api_key", p.api_key, v => { p.api_key = v; onChange(); }));

      body.appendChild(fg);

      card.querySelector(".model-remove-link").addEventListener("click", async () => {
        const refs = Object.entries(d.models || {}).filter(([, m]) => (m.provider_ref || "").toLowerCase() === name).map(([n]) => n);
        const msg = refs.length
          ? `Remove provider "${name}"?\n\nThese models reference it and will lose their credentials: ${refs.join(", ")}`
          : `Remove provider "${name}"?`;
        if (!await appConfirm(msg)) return;
        delete d.providers[name];
        markFormDirty("models");
        renderProviderCards(d, el);
      });
      card.querySelector(".model-test-link").addEventListener("click", async () => {
        try {
          const params = new URLSearchParams({ provider: p.kind || "openai_compat" });
          if (p.api_key) params.set("api_key", p.api_key);
          if (p.base_url) params.set("base_url", p.base_url);
          setStatus(`Testing ${name}…`);
          const r = await fetch(BASE_PATH + `/api/providers/models?${params}`, { headers: authHeaders() });
          const j = await r.json();
          if (!r.ok) throw new Error(j.error || r.statusText);
          setStatus(`${name}: ${j.models?.length || 0} models reachable.`, "success");
        } catch (e) {
          setStatus(`${name}: ${e.message}`, "error");
        }
      });
      grid.appendChild(card);
    });
    el.appendChild(grid);
  }

  function renderModelsPanel(d, host) {
    host.innerHTML = `
      <div class="model-panel-header">
        <div>
          <h2 class="model-panel-title">Configured Models</h2>
          <p class="model-panel-desc">Each model references a provider for credentials and endpoint. Use the ⟳ button to populate the model list from the provider.</p>
        </div>
        <button type="button" class="add-btn model-add-btn" id="add-model">+ Add model</button>
      </div>
      <div id="models-grid"></div>
    `;
    host.querySelector("#add-model").addEventListener("click", async () => {
      const providerNames = Object.keys(d.providers || {});
      if (!providerNames.length) {
        setStatus("Add a provider first (Providers tab).", "error");
        return;
      }
      let name = await appPrompt("New model name:");
      if (!name) return;
      name = name.trim().toLowerCase();
      if (!name || d.models[name]) return;
      d.models[name] = { provider_ref: providerNames[0], model: "" };
      markFormDirty("models");
      renderModelCards(d, host.querySelector("#models-grid"));
    });
    renderModelCards(d, host.querySelector("#models-grid"));
  }

  function renderModelCards(d, el) {
    el.innerHTML = "";
    const grid = document.createElement("div");
    grid.className = "model-cards-grid";
    const providerNames = Object.keys(d.providers || {});

    Object.keys(d.models).forEach(name => {
      const m = d.models[name] || {};
      const onChange = () => markFormDirty("models");
      const card = document.createElement("div");
      card.className = "model-card";
      card.innerHTML = `
        <div class="model-card-hdr">
          <div class="model-card-title">
            <span class="model-status-dot dot-active"></span>
            <strong>${escHtml(name.toUpperCase())}</strong>
            <span class="model-status-badge badge-active">ACTIVE</span>
          </div>
          <button type="button" class="model-remove-link">⏷ REMOVE</button>
        </div>
        <div class="model-card-body"></div>
      `;
      const body = card.querySelector(".model-card-body");
      const fg = document.createElement("div");
      fg.className = "model-field-grid";

      // PROVIDER (dropdown sourced from d.providers)
      const provF = document.createElement("div");
      provF.className = "model-field";
      const provLbl = document.createElement("label");
      provLbl.className = "model-field-label";
      provLbl.textContent = "PROVIDER";
      const provSel = document.createElement("select");
      provSel.className = "model-field-input";
      if (!providerNames.length) {
        const opt = document.createElement("option");
        opt.value = ""; opt.textContent = "(no providers — add one first)";
        provSel.appendChild(opt);
        provSel.disabled = true;
      } else {
        for (const pn of providerNames) {
          const opt = document.createElement("option");
          opt.value = pn; opt.textContent = pn;
          if (pn === (m.provider_ref || "")) opt.selected = true;
          provSel.appendChild(opt);
        }
      }
      provSel.addEventListener("change", () => { m.provider_ref = provSel.value; onChange(); });
      provF.appendChild(provLbl); provF.appendChild(provSel);
      fg.appendChild(provF);

      // MODEL (combobox, sourced via provider_ref)
      const combo = modelComboField(m, onChange, name => d.providers[name]);
      combo.className = "model-field model-field-combo";
      const comboSpan = combo.querySelector("span");
      if (comboSpan) { comboSpan.className = "model-field-label"; comboSpan.textContent = "MODEL"; }
      fg.appendChild(combo);

      fg.appendChild(numField("context_length", m.context_length, v => { m.context_length = v; onChange(); }));
      fg.appendChild(numField("input_token_price_per_million", m.input_token_price_per_million, v => { m.input_token_price_per_million = v; onChange(); }));
      fg.appendChild(numField("cached_input_token_price_per_million", m.cached_input_token_price_per_million, v => { m.cached_input_token_price_per_million = v; onChange(); }));
      fg.appendChild(numField("output_token_price_per_million", m.output_token_price_per_million, v => { m.output_token_price_per_million = v; onChange(); }));

      body.appendChild(fg);

      card.querySelector(".model-remove-link").addEventListener("click", async () => {
        if (!await appConfirm(`Remove model "${name}"?`)) return;
        delete d.models[name];
        markFormDirty("models");
        renderModelCards(d, el);
      });
      grid.appendChild(card);
    });

    el.appendChild(grid);
  }

  function textField(key, val, onCh) {
    const f = document.createElement("div");
    f.className = "model-field";
    const lbl = document.createElement("label");
    lbl.className = "model-field-label";
    lbl.textContent = key.toUpperCase().replace(/_/g, " ");
    const inp = document.createElement("input");
    inp.type = "text";
    inp.className = "model-field-input";
    inp.value = val == null ? "" : String(val);
    inp.addEventListener("input", () => onCh(inp.value));
    f.appendChild(lbl);
    f.appendChild(inp);
    return f;
  }

  function numField(key, val, onCh) {
    const f = document.createElement("div");
    f.className = "model-field";
    const lbl = document.createElement("label");
    lbl.className = "model-field-label";
    lbl.textContent = key.toUpperCase().replace(/_/g, " ");
    const inp = document.createElement("input");
    inp.type = "number";
    inp.className = "model-field-input";
    inp.value = val == null ? "" : val;
    inp.addEventListener("input", () => {
      const n = inp.value === "" ? undefined : Number(inp.value);
      onCh(Number.isFinite(n) ? n : undefined);
    });
    f.appendChild(lbl);
    f.appendChild(inp);
    return f;
  }

  function secretField(key, val, onCh) {
    const f = document.createElement("div");
    f.className = "model-field model-field-full";
    const lbl = document.createElement("label");
    lbl.className = "model-field-label";
    lbl.textContent = key.toUpperCase().replace(/_/g, " ");
    const wrap = document.createElement("div");
    wrap.className = "env-secret-wrap";
    const inp = document.createElement("input");
    inp.type = "password";
    inp.className = "model-field-input";
    inp.value = val == null ? "" : String(val);
    inp.addEventListener("input", () => onCh(inp.value));
    const eye = document.createElement("button");
    eye.type = "button";
    eye.className = "env-secret-eye";
    eye.title = "Show/hide";
    eye.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>`;
    eye.addEventListener("click", () => { inp.type = inp.type === "password" ? "text" : "password"; });
    wrap.appendChild(inp); wrap.appendChild(eye);
    f.appendChild(lbl); f.appendChild(wrap);
    return f;
  }

  // modelComboField builds a form row for the "model" field: a free-text input
  // with a custom dropdown panel populated from the provider's model list API.
  // The panel shows ALL fetched models (filtered by what's typed); clicking one
  // sets the value. The ⟳ button fetches and opens the panel automatically.
  // resolveProvider is an optional callback that returns the provider entry by
  // name; when set, the fetch uses provider_ref so credentials stay on the
  // server. Otherwise it falls back to the legacy inline (provider/api_key/
  // base_url) shape on the model itself.
  function modelComboField(m, onChange, resolveProvider) {
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
          if (mdl.context_length && !m.context_length) m.context_length = mdl.context_length;
          if (mdl.input_token_price_per_million && !m.input_token_price_per_million) m.input_token_price_per_million = mdl.input_token_price_per_million;
          if (mdl.output_token_price_per_million && !m.output_token_price_per_million) m.output_token_price_per_million = mdl.output_token_price_per_million;
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
      const providerRef = (m.provider_ref || "").trim();
      let params;
      if (providerRef && typeof resolveProvider === "function") {
        params = new URLSearchParams({ provider_ref: providerRef });
      } else {
        const provider = (m.provider || "").trim();
        if (!provider) { setStatus("Set a provider first."); return; }
        params = new URLSearchParams({ provider });
        if (m.api_key) params.set("api_key", m.api_key);
        if (m.base_url) params.set("base_url", m.base_url);
      }
      fetchBtn.disabled = true;
      fetchBtn.textContent = "…";
      setStatus("Fetching model list…");
      try {
        const r = await fetch(BASE_PATH + `/api/providers/models?${params}`, { headers: authHeaders() });
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

  const TOOL_ICONS = {
    Bash:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/></svg>`,
    Read:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/></svg>`,
    Write:      `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>`,
    Edit:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/></svg>`,
    Grep:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/><line x1="8" y1="11" x2="14" y2="11"/></svg>`,
    Glob:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><line x1="9" y1="14" x2="15" y2="14"/><line x1="12" y1="11" x2="12" y2="17"/></svg>`,
    revert:     `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/></svg>`,
    mime:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/></svg>`,
    mcp:        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="2"/><path d="M16.24 7.76a6 6 0 0 1 0 8.49m-8.48-.01a6 6 0 0 1 0-8.49m11.31-2.82a10 10 0 0 1 0 14.14m-14.14 0a10 10 0 0 1 0-14.14"/></svg>`,
    Skill:      `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>`,
    softskills: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/></svg>`,
    calc:       `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="4" y="2" width="16" height="20" rx="2"/><line x1="8" y1="6" x2="16" y2="6"/><line x1="8" y1="10" x2="12" y2="10"/><line x1="8" y1="14" x2="12" y2="14"/><line x1="8" y1="18" x2="12" y2="18"/><line x1="16" y1="10" x2="16" y2="18"/></svg>`,
    ddg:        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>`,
    serpapi:    `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>`,
    web:        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>`,
    registries: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></svg>`,
  };
  const TOOL_DISPLAY = {
    Bash: "Shell", Read: "File Read", Write: "File Write", Edit: "Inline Edit",
    Grep: "Grep", Glob: "Glob", revert: "Revert", mime: "MIME Type",
    mcp: "Context Proto", Skill: "Core Skills",
    softskills: "Soft Skills", calc: "Math Eng", ddg: "Web Search",
    serpapi: "SerpAPI", web: "Browser Tool", registries: "Skill Registries",
  };

  function renderAgentAgents(d) {
    const fleetList = bodyEl.querySelector("#agent-fleet-list");
    const detailPanel = bodyEl.querySelector("#agent-detail-panel");
    if (!fleetList || !detailPanel) return;

    if (!d.agents.length) {
      fleetList.innerHTML = `<p class="empty" style="padding:1rem">No agents defined.</p>`;
      detailPanel.innerHTML = "";
      return;
    }

    if (!state.activeAgentInitialized) {
      state.activeAgentInitialized = true;
      const savedName = localStorage.getItem(ACTIVE_AGENT_KEY);
      if (savedName) {
        const idx = d.agents.findIndex(a => a.name === savedName);
        if (idx >= 0) state.activeAgentIdx = idx;
      }
    }

    if (state.activeAgentIdx >= d.agents.length) state.activeAgentIdx = d.agents.length - 1;
    if (state.activeAgentIdx < 0) state.activeAgentIdx = 0;

    const activeName = d.agents[state.activeAgentIdx]?.name;
    if (activeName) localStorage.setItem(ACTIVE_AGENT_KEY, activeName);

    // Fleet list
    fleetList.innerHTML = "";

    // Separate agents into built-in and custom
    // Check the builtin flag from API, or fall back to known built-in agent names
    const BUILTIN_AGENT_NAMES = new Set(["leader", "skill_editor", "registries_crawler", "summariser", "curator"]);
    const isBuiltinByName = (a) => a.builtin === true || BUILTIN_AGENT_NAMES.has(a.name);
    const builtinAgents = d.agents.filter(isBuiltinByName);
    const customAgents = d.agents.filter(a => !isBuiltinByName(a));

    const renderAgentGroup = (agents, label) => {
      if (agents.length === 0) return;

      // Add section header
      const header = document.createElement("div");
      header.className = "agent-fleet-section-header";
      header.innerHTML = `<div class="section-label">${label}</div>`;
      fleetList.appendChild(header);

      // Add agents in this section
      agents.forEach((a) => {
        const item = document.createElement("div");
        const originalIdx = d.agents.indexOf(a);
        item.className = "agent-fleet-item" + (originalIdx === state.activeAgentIdx ? " active" : "");
        const agentSourceBadge = a.source === "local"
          ? `<span class="source-badge source-badge-local">local</span>`
          : "";
        item.innerHTML = `
          <span class="agent-fleet-dot ${a.enabled !== false ? "dot-live" : "dot-off"}"></span>
          <div class="agent-fleet-info">
            <span class="agent-fleet-name">${escHtml(a.name || "(unnamed)")} ${agentSourceBadge}</span>
            <span class="agent-fleet-model">${escHtml(a.model_ref || "")}</span>
          </div>
        `;
        item.addEventListener("click", () => { state.activeAgentIdx = originalIdx; renderAgentAgents(d); });
        fleetList.appendChild(item);
      });
    };

    // Render built-in agents first, then custom
    renderAgentGroup(builtinAgents, "BUILT-IN AGENTS");
    renderAgentGroup(customAgents, "CUSTOM AGENTS");

    // Detail panel
    const modelsCatalog = state.parsed.models?.value?.models;
    const modelOptions = modelsCatalog && typeof modelsCatalog === "object" ? Object.keys(modelsCatalog) : [];
    renderAgentDetail(d, state.activeAgentIdx, modelOptions);
  }

  function renderAgentDetail(d, idx, modelOptions) {
    const detailPanel = bodyEl.querySelector("#agent-detail-panel");
    const a = d.agents[idx];
    if (!a) { detailPanel.innerHTML = ""; return; }

    const isLeader = a.name === "leader";
    const isBuiltin = isBuiltinAgent(a.name);
    const builtinDefaults = (state.builtinAgents && state.builtinAgents[a.name]) || null;
    const onChange = () => markFormDirty("agent");

    detailPanel.innerHTML = "";

    // Title bar
    const titleBar = document.createElement("div");
    titleBar.className = "agent-detail-titlebar";
    const isEnabled = isLeader ? true : a.enabled !== false;
    const detailSourceBadge = a.source === "local"
      ? `<span class="source-badge source-badge-local">local</span>`
      : "";
    titleBar.innerHTML = `
      <div class="agent-detail-title-left">
        <h2 class="agent-detail-name">${escHtml(a.name || "(unnamed)")}</h2>
        ${detailSourceBadge}
        <span class="agent-live-badge">LIVE</span>
      </div>
      <div class="agent-detail-title-right">
        <label class="agent-active-toggle-wrap">
          <span class="agent-active-toggle-label">Active State</span>
          <span class="agent-toggle-switch">
            <input type="checkbox" class="agent-toggle-input" ${isEnabled ? "checked" : ""} ${isLeader ? "disabled" : ""}>
            <span class="agent-toggle-slider"></span>
          </span>
        </label>
        ${isBuiltin ? "" : `<button type="button" class="model-remove-link agent-remove-link">⏷ REMOVE</button>`}
      </div>
    `;
    titleBar.querySelector(".agent-toggle-input").addEventListener("change", e => {
      a.enabled = e.target.checked;
      // update dot in fleet list
      const dot = bodyEl.querySelectorAll(".agent-fleet-item")[idx]?.querySelector(".agent-fleet-dot");
      if (dot) { dot.className = "agent-fleet-dot " + (a.enabled ? "dot-live" : "dot-off"); }
      onChange();
    });
    if (!isBuiltin) {
      titleBar.querySelector(".agent-remove-link").addEventListener("click", async () => {
        if (!await appConfirm(`Remove agent "${a.name}"?`)) return;
        d.agents.splice(idx, 1);
        if (state.activeAgentIdx >= d.agents.length) state.activeAgentIdx = Math.max(0, d.agents.length - 1);
        markFormDirty("agent"); renderAgentAgents(d);
      });
    }
    detailPanel.appendChild(titleBar);

    const body = document.createElement("div");
    body.className = "agent-detail-body";

    // ── General Settings ──
    const genSection = document.createElement("section");
    genSection.className = "agent-detail-section";
    const genHdr = document.createElement("div");
    genHdr.className = "agent-section-hdr";
    genHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg><h3>General Settings</h3>`;
    genSection.appendChild(genHdr);

    const genGrid = document.createElement("div");
    genGrid.className = "agent-gen-grid";

    function genField(labelText, buildInput) {
      const f = document.createElement("div");
      f.className = "agent-gen-field";
      const lbl = document.createElement("label");
      lbl.className = "agent-gen-label";
      lbl.textContent = labelText;
      f.appendChild(lbl);
      buildInput(f);
      return f;
    }

    // Agent Display Name
    genGrid.appendChild(genField("Agent Display Name", f => {
      const inp = document.createElement("input");
      inp.type = "text"; inp.className = "agent-gen-input"; inp.value = a.name || "";
      if (isLeader) inp.disabled = true;
      inp.addEventListener("input", () => {
        a.name = inp.value;
        detailPanel.querySelector(".agent-detail-name").textContent = a.name || "(unnamed)";
        const nameEl = bodyEl.querySelectorAll(".agent-fleet-item")[idx]?.querySelector(".agent-fleet-name");
        if (nameEl) nameEl.textContent = a.name || "(unnamed)";
        onChange();
      });
      f.appendChild(inp);
    }));

    // Model Reference
    genGrid.appendChild(genField("Model Reference", f => {
      const sel = document.createElement("select");
      sel.className = "agent-gen-input";
      for (const o of ["", ...modelOptions]) {
        const opt = document.createElement("option");
        opt.value = o; opt.textContent = o || "(none)";
        if (o === (a.model_ref || "")) opt.selected = true;
        sel.appendChild(opt);
      }
      sel.addEventListener("change", () => {
        a.model_ref = sel.value;
        const modelEl = bodyEl.querySelectorAll(".agent-fleet-item")[idx]?.querySelector(".agent-fleet-model");
        if (modelEl) modelEl.textContent = a.model_ref || "";
        onChange();
      });
      f.appendChild(sel);
    }));

    genSection.appendChild(genGrid);
    body.appendChild(genSection);

    // ── Available Tools ──
    const toolSection = document.createElement("section");
    toolSection.className = "agent-detail-section";
    const toolHdr = document.createElement("div");
    toolHdr.className = "agent-section-hdr";
    toolHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg><h3>Available Tools</h3>`;
    toolSection.appendChild(toolHdr);

    const toolGrid = document.createElement("div");
    toolGrid.className = "agent-tools-grid";
    const effectiveTools = (isLeader && (!a.tools || !a.tools.length)) ? [...TOOL_GROUPS] : (a.tools || []);
    const cur = new Set(effectiveTools);
    const btnByTool = {};
    const toolEntries = [];

    for (const t of TOOL_GROUPS) {
      const isSerpDisabled = t === "serpapi" && !d.serpapi_key;
      const isOn = cur.has(t);
      const btn = document.createElement("div");
      btn.className = "agent-tool-card" + (isOn ? " tool-on" : "") + (isSerpDisabled ? " tool-disabled" : "");
      btn.title = TOOL_DISPLAY[t] || "";
      btn.innerHTML = `
        <div class="agent-tool-icon">${TOOL_ICONS[t] || ""}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${escHtml(t)}</span>
          <span class="agent-tool-desc">${escHtml(TOOL_DISPLAY[t] || "")}</span>
        </div>
        <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
      `;
      if (!isSerpDisabled) {
        btn.addEventListener("click", () => {
          const wasOn = cur.has(t);
          if (wasOn) {
            cur.delete(t);
            btn.classList.remove("tool-on");
            btn.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill pill-off";
          } else {
            cur.add(t);
            const peer = TOOL_MUTEX[t];
            if (peer && btnByTool[peer]) {
              cur.delete(peer);
              btnByTool[peer].classList.remove("tool-on");
              btnByTool[peer].querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill pill-off";
            }
            btn.classList.add("tool-on");
            btn.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill pill-on";
          }
          a.tools = Array.from(cur);
          if (t === "Skill") {
            skillsSec.classList.toggle("section-inactive", !cur.has("Skill"));
          }
          if (t === "mcp") {
            mcpSec.classList.toggle("section-inactive", !cur.has("mcp"));
          }
          onChange();
        });
      }
      btnByTool[t] = btn;
      toolEntries.push({ btn, isOn });
    }

    // ── Feature toggle cards (Leader, Allow File Attachments) ──
    // The "Leader" toggle marks an agent as eligible to lead a squad. The
    // canonical agent named "leader" is auto-flagged and the toggle is
    // locked on (cannot be unmarked).
    const featureCards = [
      {
        key: "leader", label: "leader", desc: "Can Lead a Squad",
        icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M2 20h20"/><path d="M5 20V8l7-4 7 4v12"/><path d="M9 20v-6h6v6"/></svg>`,
        getValue: () => isLeader ? true : !!a.leader,
        setValue: v => { a.leader = v; onChange(); },
        locked: isLeader,
      },
      {
        key: "allow_file_attachments", label: "files", desc: "File Attachments",
        icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>`,
        getValue: () => !!a.allow_file_attachments,
        setValue: v => { a.allow_file_attachments = v; onChange(); },
      },
    ];
    for (const fc of featureCards) {
      let fcOn = fc.getValue();
      const fcBtn = document.createElement("div");
      fcBtn.className = "agent-tool-card" + (fcOn ? " tool-on" : "") + (fc.locked ? " tool-disabled" : "");
      fcBtn.title = fc.desc || "";
      fcBtn.innerHTML = `
        <div class="agent-tool-icon">${fc.icon}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${escHtml(fc.label)}</span>
          <span class="agent-tool-desc">${escHtml(fc.desc)}</span>
        </div>
        <div class="agent-tool-toggle-pill ${fcOn ? "pill-on" : "pill-off"}"></div>
      `;
      if (!fc.locked) {
        fcBtn.addEventListener("click", () => {
          fcOn = !fcOn;
          fc.setValue(fcOn);
          fcBtn.classList.toggle("tool-on", fcOn);
          fcBtn.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill " + (fcOn ? "pill-on" : "pill-off");
        });
      }
      toolEntries.push({ btn: fcBtn, isOn: fcOn });
    }

    // selected first, then unselected
    toolEntries.sort((a, b) => Number(b.isOn) - Number(a.isOn));
    for (const { btn } of toolEntries) toolGrid.appendChild(btn);

    toolSection.appendChild(toolGrid);
    body.appendChild(toolSection);

    // ── Skills ──
    const skillsSec = document.createElement("section");
    skillsSec.className = "agent-detail-section" + (cur.has("Skill") ? "" : " section-inactive");
    const skillsHdr = document.createElement("div");
    skillsHdr.className = "agent-section-hdr";
    skillsHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg><h3>Skills</h3>`;
    skillsSec.appendChild(skillsHdr);
    const skillsBody = document.createElement("div");
    skillsBody.className = "skills-agent-body";
    skillsSec.appendChild(skillsBody);
    populateAgentSkillBlock(skillsBody, a, cur.has("Skill"), onChange);
    body.appendChild(skillsSec);

    // ── MCP Servers ──
    const mcpSec = document.createElement("section");
    mcpSec.className = "agent-detail-section" + (cur.has("mcp") ? "" : " section-inactive");
    const mcpHdr = document.createElement("div");
    mcpHdr.className = "agent-section-hdr";
    mcpHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="12" rx="2"/><line x1="2" y1="20" x2="22" y2="20"/><line x1="8" y1="16" x2="8" y2="20"/><line x1="16" y1="16" x2="16" y2="20"/></svg><h3>MCP Servers</h3>`;
    mcpSec.appendChild(mcpHdr);
    const mcpBody = document.createElement("div");
    mcpBody.className = "skills-agent-body";
    mcpSec.appendChild(mcpBody);
    populateAgentMCPBlock(mcpBody, a, cur.has("mcp"), onChange);
    body.appendChild(mcpSec);

    // ── A2A Agents ──
    const a2aSec = document.createElement("section");
    a2aSec.className = "agent-detail-section";
    const a2aHdr = document.createElement("div");
    a2aHdr.className = "agent-section-hdr";
    a2aHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z"/></svg><h3>A2A Agents</h3>`;
    a2aSec.appendChild(a2aHdr);
    const a2aBody = document.createElement("div");
    a2aBody.className = "skills-agent-body";
    a2aSec.appendChild(a2aBody);
    populateAgentA2ABlock(a2aBody, a, onChange);
    body.appendChild(a2aSec);

    // ── Instruction Set ──
    const instrSection = document.createElement("section");
    instrSection.className = "agent-detail-section";
    const instrHdr = document.createElement("div");
    instrHdr.className = "agent-section-hdr";
    instrHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg><h3>Instruction Set</h3>`;
    instrSection.appendChild(instrHdr);

    const instrBody = document.createElement("div");
    instrBody.className = "agent-instr-body";

    // Public Description
    const descF = document.createElement("div");
    descF.className = "agent-instr-field";
    const descLbl = document.createElement("label");
    descLbl.className = "agent-instr-label";
    descLbl.textContent = "Public Description";
    if (isBuiltin) {
      const tag = document.createElement("span");
      tag.className = "agent-builtin-tag";
      tag.textContent = "BUILT-IN";
      descLbl.appendChild(tag);
    }
    const descInp = document.createElement("input");
    descInp.type = "text"; descInp.className = "agent-gen-input";
    descInp.placeholder = "Explain what this agent does in one sentence…";
    const descVal = isBuiltin && builtinDefaults ? (builtinDefaults.description || "") : (a.description || "");
    descInp.value = descVal;
    if (isBuiltin) {
      descInp.disabled = true;
      descInp.classList.add("agent-builtin-readonly");
    } else {
      descInp.addEventListener("input", () => { a.description = descInp.value; onChange(); });
    }
    descF.appendChild(descLbl);
    descF.appendChild(descInp);
    instrBody.appendChild(descF);

    // System Instructions
    const sysF = document.createElement("div");
    sysF.className = "agent-instr-field";
    const sysTop = document.createElement("div");
    sysTop.className = "agent-instr-top-row";
    const sysLbl = document.createElement("label");
    sysLbl.className = "agent-instr-label";
    sysLbl.textContent = "System Instructions";
    if (isBuiltin) {
      const tag = document.createElement("span");
      tag.className = "agent-builtin-tag";
      tag.textContent = "BUILT-IN";
      sysLbl.appendChild(tag);
    }
    const sysCount = document.createElement("span");
    sysCount.className = "agent-instr-count";
    const instrVal = isBuiltin && builtinDefaults ? (builtinDefaults.instruction || "") : (a.instruction || "");
    sysCount.textContent = Math.round(instrVal.length / 4) + " tokens used";
    sysTop.appendChild(sysLbl);
    sysTop.appendChild(sysCount);
    sysF.appendChild(sysTop);
    const ta = document.createElement("textarea");
    ta.className = "agent-instr-textarea"; ta.rows = 8; ta.value = instrVal;
    if (isBuiltin) {
      ta.disabled = true;
      ta.classList.add("agent-builtin-readonly");
    } else {
      ta.addEventListener("input", () => {
        a.instruction = ta.value;
        sysCount.textContent = Math.round(ta.value.length / 4) + " tokens used";
        onChange();
      });
    }
    sysF.appendChild(ta);
    instrBody.appendChild(sysF);

    instrSection.appendChild(instrBody);
    body.appendChild(instrSection);

    // ── Advanced paths (collapsible) ──
    const adv = document.createElement("details");
    adv.className = "agent-advanced";
    adv.innerHTML = `<summary class="agent-advanced-summary">Advanced path overrides</summary>`;
    const advGrid = document.createElement("div");
    advGrid.className = "agent-gen-grid";
    for (const [key, label] of [
      ["softskills_dir", "softskills_dir"],
      ["mcp_config_path", "mcp_config_path"], ["permissions_config_path", "permissions_config_path"],
    ]) {
      const f = document.createElement("div");
      f.className = "agent-gen-field";
      const lbl = document.createElement("label");
      lbl.className = "agent-gen-label"; lbl.textContent = label;
      const inp = document.createElement("input");
      inp.type = "text"; inp.className = "agent-gen-input"; inp.value = a[key] || "";
      if (isLeader && !a[key]) inp.placeholder = "(default)";
      inp.addEventListener("input", () => { a[key] = inp.value; onChange(); });
      f.appendChild(lbl); f.appendChild(inp); advGrid.appendChild(f);
    }
    adv.appendChild(advGrid);
    body.appendChild(adv);

    // ── Move / Delete ──
    if (!isLeader) {
      const leaderFirst = d.agents[0]?.name === "leader";
      const upOk   = idx > 0 && !(leaderFirst && idx === 1);
      const downOk = idx < d.agents.length - 1;
      const acts = document.createElement("div");
      acts.className = "agent-detail-actions";
      const upBtn = document.createElement("button");
      upBtn.type = "button"; upBtn.className = "up-btn"; upBtn.textContent = "▲ Move up";
      if (!upOk) upBtn.disabled = true;
      const dnBtn = document.createElement("button");
      dnBtn.type = "button"; dnBtn.className = "down-btn"; dnBtn.textContent = "▼ Move down";
      if (!downOk) dnBtn.disabled = true;
      upBtn.addEventListener("click", () => {
        [d.agents[idx - 1], d.agents[idx]] = [d.agents[idx], d.agents[idx - 1]];
        state.activeAgentIdx = idx - 1; markFormDirty("agent"); renderAgentAgents(d);
      });
      dnBtn.addEventListener("click", () => {
        [d.agents[idx + 1], d.agents[idx]] = [d.agents[idx], d.agents[idx + 1]];
        state.activeAgentIdx = idx + 1; markFormDirty("agent"); renderAgentAgents(d);
      });
      acts.appendChild(upBtn); acts.appendChild(dnBtn);
      body.appendChild(acts);
    }

    detailPanel.appendChild(body);
  }

  // ── permissions.json form ──
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
        <section class="form-section" id="skill-perms-section" style="display:none">
          <h3>From skills</h3>
          <div id="skill-perms-list"></div>
        </section>
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
    renderSkillPermissions();
  }

  function renderSkillPermissions() {
    fetch(BASE_PATH + "/api/config/skill-permissions", { headers: authHeaders() })
      .then(r => r.ok ? r.json() : null)
      .then(data => {
        if (!data || !data.contributions || !data.contributions.length) return;
        const section = bodyEl.querySelector("#skill-perms-section");
        const list = bodyEl.querySelector("#skill-perms-list");
        if (!section || !list) return;
        const tiers = ["always_deny", "always_allow", "ask_user"];
        data.contributions.forEach(contrib => {
          const hasRules = tiers.some(t => contrib[t] && contrib[t].length > 0);
          if (!hasRules) return;
          const card = document.createElement("div");
          card.className = "form-card skill-perm-card";
          const tiersHtml = tiers.map(tier => {
            const rules = contrib[tier] || [];
            if (!rules.length) return "";
            const rows = rules.map(r => {
              const pat = typeof r === "string" ? r : (r.pattern || "");
              const reason = typeof r === "object" ? (r.reason || "") : "";
              return `<div class="rule-row skill-perm-row">
                <span class="skill-perm-tier-badge">${tier}</span>
                <code class="rule-pattern-ro">${escHtml(pat)}</code>
                ${reason ? `<span class="rule-reason-ro">${escHtml(reason)}</span>` : ""}
              </div>`;
            }).join("");
            return rows;
          }).join("");
          card.innerHTML = `
            <div class="form-card-header">
              <strong class="skill-perm-name">${escHtml(contrib.skill)}</strong>
              <span class="skill-perm-badge">skill</span>
            </div>
            <div class="skill-perm-rules">${tiersHtml}</div>
          `;
          list.appendChild(card);
        });
        const hasAny = list.children.length > 0;
        if (hasAny) section.style.display = "";
      })
      .catch(() => {});
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

  // ── mcp_config.json form (VS Code mcp.json schema) ────────────────
  // On-disk shape:
  //   { "servers": { <name>: { type, command/url, args, env, headers } },
  //     "inputs":  [ { id, type, description, password, options, default } ] }
  // Server string fields may embed "${input:id}" references; those are
  // resolved interactively at first connect by the backend.
  function renderMCPForm() {
    const id = "mcp";
    if (!state.parsed[id]) {
      bodyEl.innerHTML = `<p class="settings-loading">Loading…</p>`;
      loadParsed(id).then(() => renderMCPForm()).catch(e => {
        bodyEl.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      });
      return;
    }
    const d = state.parsed[id].value;
    // Normalise legacy / partial shapes so the renderers can assume the
    // fields exist.
    if (!d.servers || typeof d.servers !== "object" || Array.isArray(d.servers)) d.servers = {};
    if (!Array.isArray(d.inputs)) d.inputs = [];

    const sub = state.activeMCPSubtab;
    bodyEl.innerHTML = `
      <div class="settings-form">
        <div class="settings-subtabs" role="tablist">
          ${MCP_SUBTABS.map(t => `
            <button type="button" data-subtab="${t.id}" class="${sub === t.id ? "active" : ""}">${escHtml(t.label)}</button>
          `).join("")}
        </div>
        <div class="settings-subtab-body"></div>
      </div>
    `;

    bodyEl.querySelectorAll(".settings-subtabs button").forEach(b => {
      b.addEventListener("click", () => {
        if (state.activeMCPSubtab === b.dataset.subtab) {
          if (b.dataset.subtab === "remotes" && state.mcpRemotes.browsing) {
            state.mcpRemotes.browsing = null;
            state.mcpRemotes.viewing = null;
            renderMCPForm();
          }
          return;
        }
        if (b.dataset.subtab === "remotes") {
          state.mcpRemotes.browsing = null;
          state.mcpRemotes.viewing = null;
        }
        state.activeMCPSubtab = b.dataset.subtab;
        renderMCPForm();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "remotes") {
      renderMCPRemotesSection(host);
    } else {
      renderMCPServersSubtab(host, d);
    }
  }

  function renderMCPServersSubtab(host, d) {
    host.innerHTML = `
      <div class="mcp-form-toolbar">
        <button type="button" class="add-btn" id="mcp-import-btn">Import JSON…</button>
        <span class="settings-hint">Merge servers and inputs from a VS Code or Claude Code <code>mcp.json</code> snippet.</span>
      </div>
      <section class="form-section">
        <h3>Inputs</h3>
        <p class="settings-hint">Declare values the user is prompted for at first use. Reference them from server fields as <code>\${input:id}</code>.</p>
        <div id="mcp-inputs"></div>
      </section>
      <section class="form-section">
        <h3>MCP Servers</h3>
        <div id="mcp-list"></div>
      </section>
    `;
    host.querySelector("#mcp-import-btn").addEventListener("click", () => importMCPJSON(d));
    renderMCPInputs(d);
    renderMCPList(d);
    updateFooter();
  }

  // importMCPJSON opens a paste dialog, parses a VS Code mcp.json snippet,
  // and merges its `servers` and `inputs` into the form's working copy.
  // Existing entries are never overwritten — name/id collisions are
  // resolved by appending `-2`, `-3`, … and reported in a summary.
  async function importMCPJSON(d) {
    const text = await appMCPImportDialog();
    if (!text) return;

    let parsed;
    try { parsed = JSON.parse(text); }
    catch (e) { await appConfirm(`Invalid JSON: ${e.message}`); return; }

    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      await appConfirm("Expected a JSON object with `servers` and/or `inputs`.");
      return;
    }

    // Accept both VS Code's `servers` key and Claude Code's `mcpServers`
    // key. When both are present (unlikely), `servers` wins and entries
    // from `mcpServers` are merged in afterwards — duplicates inside the
    // snippet itself fall through to the same conflict-rename path used
    // for collisions with existing data.
    const rawServers = {};
    if (parsed.servers && typeof parsed.servers === "object" && !Array.isArray(parsed.servers)) {
      Object.assign(rawServers, parsed.servers);
    }
    if (parsed.mcpServers && typeof parsed.mcpServers === "object" && !Array.isArray(parsed.mcpServers)) {
      for (const [k, v] of Object.entries(parsed.mcpServers)) {
        if (!Object.prototype.hasOwnProperty.call(rawServers, k)) rawServers[k] = v;
      }
    }
    const importedServers = rawServers;
    const importedInputs = Array.isArray(parsed.inputs) ? parsed.inputs : [];

    const addedServers = [];
    const addedInputs = [];
    const renamed = [];

    for (const [name, server] of Object.entries(importedServers)) {
      if (!server || typeof server !== "object" || Array.isArray(server)) continue;
      let target = name;
      if (Object.prototype.hasOwnProperty.call(d.servers, target)) {
        let i = 2;
        while (Object.prototype.hasOwnProperty.call(d.servers, `${name}-${i}`)) i++;
        target = `${name}-${i}`;
        renamed.push(`server "${name}" → "${target}"`);
      }
      d.servers[target] = JSON.parse(JSON.stringify(server));
      addedServers.push(target);
    }

    const existingInputIds = new Set(d.inputs.map(i => i.id).filter(Boolean));
    for (const input of importedInputs) {
      if (!input || typeof input !== "object" || !input.id) continue;
      let target = input.id;
      if (existingInputIds.has(target)) {
        let i = 2;
        while (existingInputIds.has(`${input.id}-${i}`)) i++;
        target = `${input.id}-${i}`;
        renamed.push(`input "${input.id}" → "${target}"`);
      }
      const cloned = JSON.parse(JSON.stringify(input));
      cloned.id = target;
      d.inputs.push(cloned);
      existingInputIds.add(target);
      addedInputs.push(target);
    }

    if (addedServers.length === 0 && addedInputs.length === 0) {
      await appConfirm("Nothing to import — no valid servers or inputs found.");
      return;
    }

    markFormDirty("mcp");
    renderMCPInputs(d);
    renderMCPList(d);

    const parts = [];
    if (addedServers.length) parts.push(`${addedServers.length} server${addedServers.length === 1 ? "" : "s"}`);
    if (addedInputs.length)  parts.push(`${addedInputs.length} input${addedInputs.length === 1 ? "" : "s"}`);
    let msg = `Imported ${parts.join(" and ")}.`;
    if (renamed.length) msg += `\n\nRenamed to avoid conflicts:\n• ${renamed.join("\n• ")}`;
    await appConfirm(msg);
  }

  // appMCPImportDialog shows a textarea for pasting an mcp.json snippet.
  // Resolves to the raw text when the user clicks Import, or null on
  // Cancel / Escape.
  function appMCPImportDialog() {
    return new Promise(resolve => {
      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog mcp-import-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");

      const titleEl = document.createElement("p");
      titleEl.className = "app-dialog-msg";
      titleEl.textContent = "Import MCP JSON";
      box.appendChild(titleEl);

      const hint = document.createElement("p");
      hint.className = "settings-hint";
      hint.style.margin = "0";
      hint.textContent = "Paste a snippet with `servers` and/or `inputs`. Existing entries are kept; name conflicts are renamed.";
      box.appendChild(hint);

      const ta = document.createElement("textarea");
      ta.className = "mcp-import-textarea";
      ta.spellcheck = false;
      ta.placeholder = `{
  "servers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "\${input:github_pat}" }
    }
  },
  "inputs": [
    { "id": "github_pat", "type": "promptString",
      "description": "GitHub Personal Access Token", "password": true }
  ]
}`;
      box.appendChild(ta);

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.textContent = "Cancel";
      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = "Import";

      const close = result => { overlay.remove(); resolve(result); };
      cancelBtn.addEventListener("click", () => close(null));
      okBtn.addEventListener("click", () => {
        const v = ta.value.trim();
        close(v || null);
      });
      overlay.addEventListener("click", e => { if (e.target === overlay) close(null); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(null); }
      });

      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      box.appendChild(actions);
      overlay.appendChild(box);
      document.body.appendChild(overlay);
      ta.focus();
    });
  }

  // SVG icons used in input chips to telegraph kind at a glance.
  const LOCK_ICON_SVG = `<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg>`;
  const LIST_ICON_SVG = `<svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><line x1="8" y1="6" x2="20" y2="6"/><line x1="8" y1="12" x2="20" y2="12"/><line x1="8" y1="18" x2="20" y2="18"/><circle cx="4" cy="6" r="1"/><circle cx="4" cy="12" r="1"/><circle cx="4" cy="18" r="1"/></svg>`;
  const CLOSE_ICON_SVG = `<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><line x1="6" y1="6" x2="18" y2="18"/><line x1="18" y1="6" x2="6" y2="18"/></svg>`;

  // renderMCPInputs draws the top-level Inputs editor as a row of compact
  // chips. Each chip shows the input id with a type icon; clicking the
  // chip opens an edit dialog. The trailing "+" chip adds a new input
  // and opens the same dialog so the user can fill it in immediately.
  function renderMCPInputs(d) {
    const el = bodyEl.querySelector("#mcp-inputs");
    el.innerHTML = "";

    const chips = document.createElement("div");
    chips.className = "mcp-input-chips";

    d.inputs.forEach((inp, idx) => {
      if (!inp.type) inp.type = "promptString";
      if (inp.type === "pickString" && !Array.isArray(inp.options)) inp.options = [];

      const chip = document.createElement("div");
      chip.className = "mcp-input-chip";
      chip.dataset.kind = inp.type === "pickString" ? "pick" : (inp.password ? "secret" : "text");
      chip.tabIndex = 0;
      chip.setAttribute("role", "button");
      chip.title = inp.description || inp.id || "";

      const icon = document.createElement("span");
      icon.className = "mcp-input-chip-icon";
      icon.innerHTML = inp.type === "pickString"
        ? LIST_ICON_SVG
        : (inp.password ? LOCK_ICON_SVG : "");
      if (icon.innerHTML) chip.appendChild(icon);

      const label = document.createElement("span");
      label.className = "mcp-input-chip-label";
      label.textContent = inp.id || "(no id)";
      chip.appendChild(label);

      const close = document.createElement("button");
      close.type = "button";
      close.className = "mcp-input-chip-close";
      close.setAttribute("aria-label", "Remove input");
      close.innerHTML = CLOSE_ICON_SVG;
      close.addEventListener("click", e => {
        e.stopPropagation();
        d.inputs.splice(idx, 1);
        markFormDirty("mcp");
        renderMCPInputs(d);
      });
      chip.appendChild(close);

      const openEditor = async () => {
        const result = await appMCPInputDialog(inp, d.inputs.filter((_, i) => i !== idx));
        if (!result) return;
        d.inputs[idx] = result;
        markFormDirty("mcp");
        renderMCPInputs(d);
      };
      chip.addEventListener("click", openEditor);
      chip.addEventListener("keydown", e => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); openEditor(); }
      });

      chips.appendChild(chip);
    });

    // Trailing add-chip: creates a default input and immediately opens
    // its editor so the user doesn't see a placeholder "new_input" entry
    // in the chip row.
    const addChip = document.createElement("button");
    addChip.type = "button";
    addChip.className = "mcp-input-chip mcp-input-chip-add";
    addChip.innerHTML = `<span class="mcp-input-chip-icon">+</span><span class="mcp-input-chip-label">Add input</span>`;
    addChip.addEventListener("click", async () => {
      const result = await appMCPInputDialog(
        { id: "", type: "promptString", description: "" },
        d.inputs,
      );
      if (!result) return;
      d.inputs.push(result);
      markFormDirty("mcp");
      renderMCPInputs(d);
    });
    chips.appendChild(addChip);

    el.appendChild(chips);

    if (d.inputs.length === 0) {
      const empty = document.createElement("p");
      empty.className = "settings-hint";
      empty.style.marginTop = "0.5rem";
      empty.textContent = "No inputs declared. Add one if any server needs a user-supplied value.";
      el.appendChild(empty);
    }
  }

  // appMCPInputDialog opens a modal for creating or editing one input
  // declaration. Resolves to the new input object on Save, or null on
  // Cancel / Escape. `siblings` is used to validate that the id is
  // unique among other inputs.
  function appMCPInputDialog(initial, siblings) {
    return new Promise(resolve => {
      // Work on a shallow clone so we can mutate freely until the user
      // commits or cancels.
      const draft = JSON.parse(JSON.stringify(initial || {}));
      if (!draft.type) draft.type = "promptString";
      if (draft.type === "pickString" && !Array.isArray(draft.options)) draft.options = [];

      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog mcp-input-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");

      const titleEl = document.createElement("p");
      titleEl.className = "app-dialog-msg";
      titleEl.textContent = initial && initial.id ? `Edit input: ${initial.id}` : "Add input";
      box.appendChild(titleEl);

      const form = document.createElement("div");
      form.className = "mcp-input-dialog-form";
      box.appendChild(form);

      const fg = document.createElement("div");
      fg.className = "model-field-grid";
      form.appendChild(fg);

      // ID
      const idField = document.createElement("div");
      idField.className = "model-field";
      idField.innerHTML = `
        <label class="model-field-label model-field-label--title">ID</label>
        <input type="text" class="model-field-input" placeholder="github_pat" />
      `;
      const idInp = idField.querySelector("input");
      idInp.value = draft.id || "";
      idInp.addEventListener("input", () => { draft.id = idInp.value.trim(); });
      fg.appendChild(idField);

      // Type
      const typeField = document.createElement("div");
      typeField.className = "model-field";
      typeField.innerHTML = `
        <label class="model-field-label model-field-label--title">Type</label>
        <select class="model-field-input">
          <option value="promptString">promptString</option>
          <option value="pickString">pickString</option>
        </select>
      `;
      const typeSel = typeField.querySelector("select");
      typeSel.value = draft.type === "pickString" ? "pickString" : "promptString";
      fg.appendChild(typeField);

      // Description (full width)
      const descField = document.createElement("div");
      descField.className = "model-field model-field-full";
      descField.innerHTML = `
        <label class="model-field-label model-field-label--title">Description</label>
        <input type="text" class="model-field-input" placeholder="Shown to the user at prompt time" />
      `;
      const descInp = descField.querySelector("input");
      descInp.value = draft.description || "";
      descInp.addEventListener("input", () => { draft.description = descInp.value; });
      form.appendChild(descField);

      // Variant slot — password checkbox or pickString options. Rebuilt
      // when the type changes.
      const variantSlot = document.createElement("div");
      form.appendChild(variantSlot);

      // Default (full width) — last so password masking applies.
      const defField = document.createElement("div");
      defField.className = "model-field model-field-full";
      defField.innerHTML = `
        <label class="model-field-label model-field-label--title">Default (optional)</label>
        <input type="text" class="model-field-input" />
      `;
      const defInp = defField.querySelector("input");
      defInp.value = draft.default || "";
      defInp.addEventListener("input", () => { draft.default = defInp.value; });
      form.appendChild(defField);

      const renderVariant = () => {
        variantSlot.innerHTML = "";
        if (typeSel.value === "promptString") {
          if (!Array.isArray(draft.options)) draft.options = undefined;
          const pw = document.createElement("label");
          pw.className = "mcp-input-checkbox";
          pw.innerHTML = `<input type="checkbox" /><span>Treat as password (mask input when prompting)</span>`;
          const cb = pw.querySelector("input");
          cb.checked = !!draft.password;
          cb.addEventListener("change", () => {
            draft.password = cb.checked;
            defInp.type = cb.checked ? "password" : "text";
          });
          variantSlot.appendChild(pw);
          defInp.type = draft.password ? "password" : "text";
        } else {
          if (!Array.isArray(draft.options)) draft.options = [];
          draft.password = undefined;
          defInp.type = "text";

          const sec = document.createElement("div");
          sec.className = "mcp-section";
          sec.innerHTML = `<h4 class="mcp-section-title">Options</h4>`;
          const rows = document.createElement("div");
          rows.className = "mcp-arg-rows";
          sec.appendChild(rows);
          const drawOpts = () => {
            rows.innerHTML = "";
            draft.options.forEach((o, oi) => {
              const r = document.createElement("div");
              r.className = "mcp-arg-row";
              const inp = document.createElement("input");
              inp.type = "text";
              inp.value = o;
              inp.addEventListener("input", () => { draft.options[oi] = inp.value; });
              r.appendChild(inp);
              const tr = document.createElement("button");
              tr.type = "button";
              tr.className = "mcp-trash";
              tr.innerHTML = TRASH_ICON_SVG;
              tr.addEventListener("click", () => { draft.options.splice(oi, 1); drawOpts(); });
              r.appendChild(tr);
              rows.appendChild(r);
            });
          };
          drawOpts();
          const addBtn = document.createElement("button");
          addBtn.type = "button";
          addBtn.className = "mcp-add-full";
          addBtn.innerHTML = `<span class="mcp-add-full-icon">+</span><span>Add Option</span>`;
          addBtn.addEventListener("click", () => { draft.options.push(""); drawOpts(); });
          sec.appendChild(addBtn);
          variantSlot.appendChild(sec);
        }
      };
      typeSel.addEventListener("change", () => {
        draft.type = typeSel.value;
        renderVariant();
      });
      renderVariant();

      // Validation banner — reused for any commit-time failure.
      const errEl = document.createElement("p");
      errEl.className = "mcp-input-dialog-error";
      errEl.hidden = true;
      form.appendChild(errEl);
      const showErr = msg => { errEl.textContent = msg; errEl.hidden = false; };

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.textContent = "Cancel";
      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = "Save";

      const close = result => { overlay.remove(); resolve(result); };
      cancelBtn.addEventListener("click", () => close(null));
      okBtn.addEventListener("click", () => {
        const id = (draft.id || "").trim();
        if (!id) { showErr("ID is required."); idInp.focus(); return; }
        if (siblings.some(s => s && s.id === id)) {
          showErr(`Another input already uses the id "${id}".`);
          idInp.focus();
          return;
        }
        // Strip empty fields so the persisted JSON stays clean.
        const out = { id, type: draft.type };
        if (draft.description) out.description = draft.description;
        if (draft.type === "promptString" && draft.password) out.password = true;
        if (draft.type === "pickString") out.options = (draft.options || []).filter(o => o !== "");
        if (draft.default) out.default = draft.default;
        close(out);
      });
      overlay.addEventListener("click", e => { if (e.target === overlay) close(null); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(null); }
      });

      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      box.appendChild(actions);
      overlay.appendChild(box);
      document.body.appendChild(overlay);
      idInp.focus();
      idInp.select();
    });
  }

  // Normalised transport kind for a server entry. Unknown values fall
  // back to "stdio" so legacy entries without a `type` field keep
  // working unchanged.
  function mcpTransportKind(s) {
    const t = (s.type || "").toLowerCase();
    return t === "http" ? "http" : "stdio";
  }

  // Reusable trash icon for inline row delete buttons. Matches the
  // icon-button style used elsewhere in the panel.
  const TRASH_ICON_SVG = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>`;

  function renderMCPList(d) {
    const el = bodyEl.querySelector("#mcp-list");
    el.innerHTML = "";

    const grid = document.createElement("div");
    grid.className = "mcp-cards-grid";

    // VS Code's mcp.json uses an object keyed by name; iterate in
    // lexicographic order so card placement is deterministic between
    // renders.
    const names = Object.keys(d.servers).sort((a, b) => a.localeCompare(b));
    names.forEach(name => {
      const s = d.servers[name];
      // Make sure every shape an editor row mutates is materialised so
      // dirty-tracking and renderers don't have to null-check.
      if (!Array.isArray(s.args)) s.args = [];
      if (!s.env || typeof s.env !== "object") s.env = {};
      if (!s.headers || typeof s.headers !== "object") s.headers = {};

      // Local mutable name for rename support — renaming a server is a
      // key swap on the parent object, not an in-place mutation.
      let currentName = name;

      const card = document.createElement("div");
      card.className = "mcp-card";
      card.innerHTML = `
        <div class="mcp-card-hdr">
          <div class="mcp-card-title">
            <span class="model-status-dot dot-active"></span>
            <strong class="mcp-card-name">${escHtml(currentName || "(unnamed)")}</strong>
          </div>
          <button type="button" class="del-btn mcp-remove">Delete</button>
        </div>
        <div class="mcp-card-body"></div>
      `;
      const body = card.querySelector(".mcp-card-body");
      const nameEl = card.querySelector(".mcp-card-name");

      // mcpField — a labelled single-line text field. Labels are
      // Title-cased (e.g. "Command", not "COMMAND") to match the new
      // section-based layout.
      function mcpField(label, val, onCh, opts = {}) {
        const f = document.createElement("div");
        f.className = "model-field" + (opts.full ? " model-field-full" : "");
        const lbl = document.createElement("label");
        lbl.className = "model-field-label model-field-label--title";
        lbl.textContent = label;
        const inp = document.createElement("input");
        inp.type = "text";
        inp.className = "model-field-input";
        inp.value = val == null ? "" : String(val);
        if (opts.placeholder) inp.placeholder = opts.placeholder;
        inp.addEventListener("input", () => onCh(inp.value));
        f.appendChild(lbl);
        f.appendChild(inp);
        return f;
      }

      // mcpSection wraps a logical block (General Settings, Execution,
      // Arguments, Environment Variables, Headers) with a heading.
      function mcpSection(title) {
        const sec = document.createElement("section");
        sec.className = "mcp-section";
        const h = document.createElement("h4");
        h.className = "mcp-section-title";
        h.textContent = title;
        sec.appendChild(h);
        return sec;
      }

      // mcpAddButton — full-width green "+ Add …" button used at the
      // bottom of each list section.
      function mcpAddButton(label, onClick) {
        const b = document.createElement("button");
        b.type = "button";
        b.className = "mcp-add-full";
        b.innerHTML = `<span class="mcp-add-full-icon">+</span><span>${escHtml(label)}</span>`;
        b.addEventListener("click", onClick);
        return b;
      }

      // mcpTrashBtn — icon-only delete button used on every row.
      function mcpTrashBtn(onClick) {
        const b = document.createElement("button");
        b.type = "button";
        b.className = "mcp-trash";
        b.setAttribute("aria-label", "Remove");
        b.innerHTML = TRASH_ICON_SVG;
        b.addEventListener("click", onClick);
        return b;
      }

      // mcpKVList renders a `<key, value>` editor backed by an object,
      // used for `env` (stdio) and `headers` (http). Values are plain
      // text; a "${input:id}" reference anywhere in the value is
      // resolved interactively at first connect. Returns the wrapper
      // section element.
      function mcpKVList({ title, addLabel, store, keyPlaceholder, valuePlaceholder, addPromptMsg, hint }) {
        const sec = mcpSection(title);
        if (hint) sec.querySelector(".mcp-section-title").title = hint;

        const grid = document.createElement("div");
        grid.className = "mcp-kv-grid";
        const headers = document.createElement("div");
        headers.className = "mcp-kv-headers";
        headers.innerHTML = `
          <span class="model-field-label model-field-label--title">Key</span>
          <span class="model-field-label model-field-label--title">Value</span>
          <span></span>
        `;
        grid.appendChild(headers);
        const rows = document.createElement("div");
        rows.className = "mcp-kv-rows";
        grid.appendChild(rows);
        sec.appendChild(grid);

        const draw = () => {
          rows.innerHTML = "";
          Object.entries(store).forEach(([k, v]) => {
            const r = document.createElement("div");
            r.className = "mcp-kv-row";
            r.innerHTML = `
              <input type="text" class="kv-k" placeholder="${escHtml(keyPlaceholder)}" value="${escHtml(k)}" />
              <input type="text" class="kv-v" placeholder="${escHtml(valuePlaceholder)}" value="${escHtml(v)}" />
            `;
            const kIn = r.querySelector(".kv-k");
            const vIn = r.querySelector(".kv-v");
            let oldKey = k;

            kIn.addEventListener("change", () => {
              const nk = kIn.value.trim();
              if (!nk || nk === oldKey) return;
              const val = store[oldKey];
              delete store[oldKey];
              store[nk] = val;
              oldKey = nk;
              markFormDirty("mcp");
            });
            vIn.addEventListener("input", () => { store[oldKey] = vIn.value; markFormDirty("mcp"); });
            r.appendChild(mcpTrashBtn(() => { delete store[oldKey]; markFormDirty("mcp"); draw(); }));
            rows.appendChild(r);
          });
        };
        draw();
        sec.appendChild(mcpAddButton(addLabel, async () => {
          let nk = await appPrompt(addPromptMsg);
          if (!nk) return;
          nk = nk.trim();
          if (!nk || nk in store) return;
          store[nk] = "";
          markFormDirty("mcp"); draw();
        }));
        return sec;
      }

      // mcpStringList renders an ordered list of strings (used for
      // stdio `args`). Backed by an array. Returns the section element.
      function mcpStringList({ title, addLabel, store }) {
        const sec = mcpSection(title);
        const list = document.createElement("div");
        list.className = "mcp-arg-rows";
        sec.appendChild(list);
        const draw = () => {
          list.innerHTML = "";
          store.forEach((a, ai) => {
            const r = document.createElement("div");
            r.className = "mcp-arg-row";
            const inp = document.createElement("input");
            inp.type = "text";
            inp.value = a;
            inp.addEventListener("input", () => { store[ai] = inp.value; markFormDirty("mcp"); });
            r.appendChild(inp);
            r.appendChild(mcpTrashBtn(() => { store.splice(ai, 1); markFormDirty("mcp"); draw(); }));
            list.appendChild(r);
          });
        };
        draw();
        sec.appendChild(mcpAddButton(addLabel, () => {
          store.push(""); markFormDirty("mcp"); draw();
        }));
        return sec;
      }

      // ── General Settings ────────────────────────────────────────────
      const general = mcpSection("General Settings");
      const generalGrid = document.createElement("div");
      generalGrid.className = "model-field-grid";
      // Name is the map key, not a field on the Server object. We swap
      // the key on rename. Renaming to an existing name is silently
      // refused (no overwrite); empty names are silently refused too.
      generalGrid.appendChild(mcpField("Name", currentName, v => {
        const nv = v.trim();
        nameEl.textContent = nv || "(unnamed)";
        if (!nv || nv === currentName) return;
        if (Object.prototype.hasOwnProperty.call(d.servers, nv)) return;
        delete d.servers[currentName];
        d.servers[nv] = s;
        currentName = nv;
        markFormDirty("mcp");
      }));
      const typeField = document.createElement("div");
      typeField.className = "model-field";
      typeField.innerHTML = `
        <label class="model-field-label model-field-label--title">Type</label>
        <select class="model-field-input">
          <option value="stdio">stdio (local subprocess)</option>
          <option value="http">http (remote server)</option>
        </select>
      `;
      const typeSel = typeField.querySelector("select");
      typeSel.value = mcpTransportKind(s);
      typeSel.addEventListener("change", () => {
        s.type = typeSel.value;
        markFormDirty("mcp");
        renderTransportSection();
      });
      generalGrid.appendChild(typeField);
      general.appendChild(generalGrid);
      body.appendChild(general);

      // Transport-specific sections live in a sub-container so flipping
      // stdio↔http swaps them without rebuilding General Settings.
      const transportSection = document.createElement("div");
      transportSection.className = "mcp-transport-section";
      body.appendChild(transportSection);

      function renderTransportSection() {
        transportSection.innerHTML = "";
        const inputHint = "Embed \"${input:id}\" anywhere in the value to have the user prompted for that input at first use.";
        if (mcpTransportKind(s) === "http") {
          const conn = mcpSection("Connection");
          const urlGrid = document.createElement("div");
          urlGrid.className = "model-field-grid";
          urlGrid.appendChild(mcpField("URL", s.url, v => { s.url = v; markFormDirty("mcp"); }, {
            full: true,
            placeholder: "https://api.githubcopilot.com/mcp/",
          }));
          conn.appendChild(urlGrid);
          transportSection.appendChild(conn);
          transportSection.appendChild(mcpKVList({
            title: "Headers",
            addLabel: "Add Header",
            store: s.headers,
            keyPlaceholder: "Header-Name",
            valuePlaceholder: "value or Bearer ${input:id}",
            addPromptMsg: "Header name (e.g. Authorization):",
            hint: inputHint,
          }));
        } else {
          const exec = mcpSection("Execution");
          const cmdGrid = document.createElement("div");
          cmdGrid.className = "model-field-grid";
          cmdGrid.appendChild(mcpField("Command", s.command, v => { s.command = v; markFormDirty("mcp"); }, { full: true }));
          exec.appendChild(cmdGrid);
          transportSection.appendChild(exec);
          transportSection.appendChild(mcpStringList({
            title: "Arguments",
            addLabel: "Add Argument",
            store: s.args,
          }));
          transportSection.appendChild(mcpKVList({
            title: "Environment Variables",
            addLabel: "Add Variable",
            store: s.env,
            keyPlaceholder: "KEY",
            valuePlaceholder: "value or ${input:id}",
            addPromptMsg: "Env var name:",
            hint: inputHint,
          }));
        }
      }
      renderTransportSection();

      card.querySelector(".mcp-remove").addEventListener("click", async () => {
        if (!await appConfirm(`Remove server "${currentName}"?`)) return;
        delete d.servers[currentName];
        markFormDirty("mcp"); renderMCPList(d);
      });

      grid.appendChild(card);
    });

    // Empty "Add MCP Server" card — new entries default to stdio. We
    // generate a unique name slot so the empty-add never silently
    // clobbers an existing server.
    const emptyCard = document.createElement("div");
    emptyCard.className = "mcp-card mcp-card-empty";
    const emptyBtn = document.createElement("button");
    emptyBtn.type = "button";
    emptyBtn.className = "model-card-empty-btn";
    emptyBtn.innerHTML = `
      <span class="model-card-empty-icon">⊕</span>
      <span class="model-card-empty-label">Add MCP Server</span>
      <span class="model-card-empty-sub">Configure a new Model Context Protocol server</span>
    `;
    emptyBtn.addEventListener("click", () => {
      let base = "new-server";
      let candidate = base;
      let i = 1;
      while (Object.prototype.hasOwnProperty.call(d.servers, candidate)) {
        i++;
        candidate = `${base}-${i}`;
      }
      d.servers[candidate] = { type: "stdio", command: "", args: [], env: {}, url: "", headers: {} };
      markFormDirty("mcp");
      renderMCPList(d);
    });
    emptyCard.appendChild(emptyBtn);
    grid.appendChild(emptyCard);

    el.appendChild(grid);
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

  function appRegistryDialog({ title = "Add Remote Registry", initial = {}, isEdit = false, defaultKind = "skills" } = {}) {
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
      const kindVal = initial.kind || defaultKind;
      const urlPlaceholder = defaultKind === "agents"
        ? "https://github.com/owner/repo/tree/main/agents"
        : defaultKind === "mcp"
        ? "https://github.com/owner/repo/tree/main/mcp-servers"
        : defaultKind === "a2a"
        ? "https://github.com/owner/repo/tree/main/a2a-agents"
        : defaultKind === "squads"
        ? "https://github.com/owner/repo/tree/main/squads"
        : "https://github.com/owner/repo/tree/main/skills";
      const namePlaceholder = defaultKind === "agents"
        ? "My agent registry"
        : defaultKind === "mcp"
        ? "My MCP registry"
        : defaultKind === "a2a"
        ? "My A2A registry"
        : defaultKind === "squads"
        ? "My squad registry"
        : "My skill registry";
      form.innerHTML = `
        <div class="registry-dialog-field">
          <label for="reg-dlg-name">Name <span class="registry-dialog-hint">(optional)</span></label>
          <input type="text" id="reg-dlg-name" autocomplete="off"
            placeholder="${escHtml(namePlaceholder)}"
            value="${escHtml(initial.name || "")}" />
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-url">Repository URL</label>
          <input type="url" id="reg-dlg-url" autocomplete="off"
            placeholder="${escHtml(urlPlaceholder)}"
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
          <label for="reg-dlg-kind">Hosts</label>
          <select id="reg-dlg-kind">
            <option value="skills"${kindVal === "skills" ? " selected" : ""}>Skills</option>
            <option value="agents"${kindVal === "agents" ? " selected" : ""}>Agents</option>
            <option value="both"${kindVal === "both" ? " selected" : ""}>Both (Skills + Agents)</option>
            <option value="mcp"${kindVal === "mcp" ? " selected" : ""}>MCP Servers</option>
            <option value="a2a"${kindVal === "a2a" ? " selected" : ""}>A2A Agents</option>
            <option value="squads"${kindVal === "squads" ? " selected" : ""}>Squads</option>
          </select>
          <span class="registry-dialog-hint">Tab where this registry will appear.</span>
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
          kind:     form.querySelector("#reg-dlg-kind").value,
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
    const r = await fetch(BASE_PATH + `/api${path}`, opts);
    if (r.status === 204) return null;
    const j = await r.json().catch(() => ({}));
    if (!r.ok) throw new SkillsAPIError(j.code || "HTTP_ERROR", j.error || `HTTP ${r.status}`, j.details);
    return j;
  }

  const skillsGet    = path       => skillsAPI("GET",    path, null);
  const skillsPost   = (path, b)  => skillsAPI("POST",   path, b);
  const skillsPut    = (path, b)  => skillsAPI("PUT",    path, b);
  const skillsDel    = path       => skillsAPI("DELETE", path, null);

  // showInstallResult shows a success status and, when the server returned
  // dependency warnings, appends them as a separate warning message.
  function showInstallResult(successMsg, warnings) {
    setStatus(successMsg, "success");
    if (Array.isArray(warnings) && warnings.length > 0) {
      // Overwrite with a warning that includes the success summary.
      setStatus(successMsg + " ⚠ Dependencies: " + warnings.join("; "), "warning");
    }
  }

  // ─── Skills — shared block renderer ───────────────────────────────────

  // Renders skill checkboxes + Enable all / Disable all into container.
  // agentInfo: {name, has_skills_tool, skills:[]}
  // registry: [{name, description, ...}]
  // onChanged: optional callback after a mutation
  function renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange) {
    container.innerHTML = "";

    if (!hasSkillsTool) {
      const warn = document.createElement("p");
      warn.className = "skills-tool-warning";
      warn.textContent = '"Skill" tool not enabled — assignments will be ignored until re-enabled in Agent → Agents.';
      container.appendChild(warn);
    }

    if (!Array.isArray(agent.skills)) agent.skills = [];
    const selected = new Set(agent.skills);

    if (!registry.length) {
      const p = document.createElement("p"); p.className = "empty";
      p.textContent = "No skills installed.";
      container.appendChild(p);
      return;
    }

    const skillIcon = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>`;

    const grid = document.createElement("div");
    grid.className = "agent-tools-grid";

    const sorted = [...registry].sort((a, b) => Number(selected.has(b.name)) - Number(selected.has(a.name)));
    for (const sk of sorted) {
      let isOn = selected.has(sk.name);
      const card = document.createElement("div");
      card.className = "agent-tool-card" + (isOn ? " tool-on" : "");
      card.dataset.skill = sk.name;
      card.innerHTML = `
        <div class="agent-tool-icon">${skillIcon}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${escHtml(sk.name)}</span>
          <span class="agent-tool-desc">${escHtml(sk.description || "")}</span>
        </div>
        <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
      `;
      card.addEventListener("click", () => {
        isOn = !isOn;
        if (isOn) selected.add(sk.name); else selected.delete(sk.name);
        // Preserve registry order for the saved list.
        agent.skills = registry.map(s => s.name).filter(n => selected.has(n));
        card.classList.toggle("tool-on", isOn);
        card.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill " + (isOn ? "pill-on" : "pill-off");
        onChange();
      });
      grid.appendChild(card);
    }
    container.appendChild(grid);

    const actions = document.createElement("div");
    actions.className = "skills-block-actions";

    const enableAllBtn = document.createElement("button");
    enableAllBtn.type = "button"; enableAllBtn.className = "add-btn";
    enableAllBtn.textContent = "Enable all";
    enableAllBtn.addEventListener("click", () => {
      agent.skills = registry.map(s => s.name);
      onChange();
      renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange);
    });

    const disableAllBtn = document.createElement("button");
    disableAllBtn.type = "button"; disableAllBtn.className = "del-btn";
    disableAllBtn.textContent = "Disable all";
    disableAllBtn.addEventListener("click", async () => {
      if (!await appConfirm(`Remove all skills from "${agent.name}"?`)) return;
      agent.skills = [];
      onChange();
      renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange);
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

  // Populates a container with the agent's skill block (fetches registry async).
  async function populateAgentSkillBlock(container, agent, hasSkillsTool, onChange) {
    container.innerHTML = `<p class="settings-hint">Loading skills…</p>`;
    try {
      const regRes = await skillsGet("/skills/registry");
      const registry = regRes.skills || [];
      renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange);
    } catch (e) {
      container.innerHTML = `<p class="settings-error">Skills unavailable: ${escHtml(e.message)}</p>`;
    }
  }

  // ─── MCP — agent picker (mirrors the Skills picker pattern) ──────────
  //
  // Per-agent MCP server selection is stored as `mcp_servers: [name, ...]`
  // directly on the agent entry in agent.json. Unset / empty = no servers
  // (explicit opt-in). Toggling cards mutates the parsed agent doc in
  // memory and calls onChange() so the form goes dirty; saving the Agents
  // tab persists the list back to agent.json. The available server list
  // comes from the parsed mcp_config.json (already shared via state.parsed).
  function renderAgentMCPBlockContent(container, agent, servers, hasMCPTool, onChange) {
    container.innerHTML = "";

    if (!hasMCPTool) {
      const warn = document.createElement("p");
      warn.className = "skills-tool-warning";
      warn.textContent = '"mcp" tool not enabled — selections will be ignored until re-enabled above.';
      container.appendChild(warn);
    }

    if (!servers.length) {
      const p = document.createElement("p");
      p.className = "empty";
      p.textContent = "No MCP servers configured. Add some in the MCP tab.";
      container.appendChild(p);
      return;
    }

    if (!Array.isArray(agent.mcp_servers)) agent.mcp_servers = [];
    const selected = new Set(agent.mcp_servers);

    const mcpIcon = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="12" rx="2"/><line x1="2" y1="20" x2="22" y2="20"/><line x1="8" y1="16" x2="8" y2="20"/><line x1="16" y1="16" x2="16" y2="20"/></svg>`;

    const grid = document.createElement("div");
    grid.className = "agent-tools-grid";

    const sorted = [...servers].sort((a, b) => Number(selected.has(b.name)) - Number(selected.has(a.name)));
    for (const s of sorted) {
      let isOn = selected.has(s.name);
      const card = document.createElement("div");
      card.className = "agent-tool-card" + (isOn ? " tool-on" : "");
      card.dataset.mcp = s.name;
      const descParts = [];
      if (s.command) descParts.push(s.command);
      if (Array.isArray(s.args) && s.args.length) descParts.push(s.args.join(" "));
      const desc = descParts.join(" ");
      card.innerHTML = `
        <div class="agent-tool-icon">${mcpIcon}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${escHtml(s.name)}</span>
          <span class="agent-tool-desc">${escHtml(desc)}</span>
        </div>
        <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
      `;
      card.addEventListener("click", () => {
        isOn = !isOn;
        if (isOn) selected.add(s.name); else selected.delete(s.name);
        // Preserve declaration order from mcp_config.json.
        agent.mcp_servers = servers.map(x => x.name).filter(n => selected.has(n));
        card.classList.toggle("tool-on", isOn);
        card.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill " + (isOn ? "pill-on" : "pill-off");
        onChange();
      });
      grid.appendChild(card);
    }
    container.appendChild(grid);

    const actions = document.createElement("div");
    actions.className = "skills-block-actions";

    const enableAll = document.createElement("button");
    enableAll.type = "button"; enableAll.className = "add-btn"; enableAll.textContent = "Enable all";
    enableAll.addEventListener("click", () => {
      agent.mcp_servers = servers.map(s => s.name);
      onChange();
      renderAgentMCPBlockContent(container, agent, servers, hasMCPTool, onChange);
    });

    const disableAll = document.createElement("button");
    disableAll.type = "button"; disableAll.className = "del-btn"; disableAll.textContent = "Disable all";
    disableAll.addEventListener("click", () => {
      agent.mcp_servers = [];
      onChange();
      renderAgentMCPBlockContent(container, agent, servers, hasMCPTool, onChange);
    });

    actions.appendChild(enableAll);
    actions.appendChild(disableAll);

    const manageLink = document.createElement("button");
    manageLink.type = "button"; manageLink.className = "skills-manage-link";
    manageLink.textContent = "Manage in MCP →";
    manageLink.addEventListener("click", () => { setActiveFile("mcp"); });
    actions.appendChild(manageLink);

    container.appendChild(actions);
  }

  async function populateAgentMCPBlock(container, agent, hasMCPTool, onChange) {
    container.innerHTML = `<p class="settings-hint">Loading MCP servers…</p>`;
    try {
      if (!state.parsed.mcp) await loadParsed("mcp");
      // VS Code's mcp.json schema: servers is a map keyed by name.
      // Flatten to {name, ...} entries the picker expects.
      const raw = state.parsed.mcp.value.servers;
      const servers = (raw && typeof raw === "object" && !Array.isArray(raw))
        ? Object.entries(raw).map(([name, s]) => ({ name, ...s })).filter(s => s.name)
        : [];
      renderAgentMCPBlockContent(container, agent, servers, hasMCPTool, onChange);
    } catch (e) {
      container.innerHTML = `<p class="settings-error">MCP unavailable: ${escHtml(e.message)}</p>`;
    }
  }

  // ─── A2A — agent picker (mirrors the MCP picker pattern) ──────────────
  //
  // Per-agent A2A selection is stored as `a2a_agents: [name, ...]`
  // on the agent entry in agent.json. The available agent list comes
  // from the parsed a2a_config.json (already shared via state.parsed).
  function renderAgentA2ABlockContent(container, agent, a2aAgents, onChange) {
    container.innerHTML = "";

    if (!a2aAgents.length) {
      const p = document.createElement("p");
      p.className = "empty";
      p.textContent = "No A2A agents configured. Add some in the A2A tab.";
      container.appendChild(p);
      return;
    }

    const selected = new Set(Array.isArray(agent.a2a_agents) ? agent.a2a_agents : []);

    const a2aIcon = `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z"/><path d="M12 8v4m0 4h.01"/></svg>`;

    const grid = document.createElement("div");
    grid.className = "agent-tools-grid";

    const sorted = [...a2aAgents].sort((a, b) => Number(selected.has(b.name)) - Number(selected.has(a.name)));
    for (const ag of sorted) {
      let isOn = selected.has(ag.name);
      const card = document.createElement("div");
      card.className = "agent-tool-card" + (isOn ? " tool-on" : "");
      card.dataset.a2a = ag.name;
      const desc = ag.description || ag.url || "";
      card.innerHTML = `
        <div class="agent-tool-icon">${a2aIcon}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${escHtml(ag.name)}</span>
          <span class="agent-tool-desc">${escHtml(desc)}</span>
        </div>
        <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
      `;
      card.addEventListener("click", () => {
        isOn = !isOn;
        if (isOn) selected.add(ag.name); else selected.delete(ag.name);
        agent.a2a_agents = a2aAgents.map(x => x.name).filter(n => selected.has(n));
        card.classList.toggle("tool-on", isOn);
        card.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill " + (isOn ? "pill-on" : "pill-off");
        onChange();
      });
      grid.appendChild(card);
    }
    container.appendChild(grid);

    const actions = document.createElement("div");
    actions.className = "skills-block-actions";

    const enableAll = document.createElement("button");
    enableAll.type = "button"; enableAll.className = "add-btn"; enableAll.textContent = "Enable all";
    enableAll.addEventListener("click", () => {
      agent.a2a_agents = a2aAgents.map(ag => ag.name);
      onChange();
      renderAgentA2ABlockContent(container, agent, a2aAgents, onChange);
    });

    const disableAll = document.createElement("button");
    disableAll.type = "button"; disableAll.className = "del-btn"; disableAll.textContent = "Disable all";
    disableAll.addEventListener("click", () => {
      agent.a2a_agents = [];
      onChange();
      renderAgentA2ABlockContent(container, agent, a2aAgents, onChange);
    });

    actions.appendChild(enableAll);
    actions.appendChild(disableAll);

    const manageLink = document.createElement("button");
    manageLink.type = "button"; manageLink.className = "skills-manage-link";
    manageLink.textContent = "Manage in A2A →";
    manageLink.addEventListener("click", () => { setActiveFile("a2a"); });
    actions.appendChild(manageLink);

    container.appendChild(actions);
  }

  async function populateAgentA2ABlock(container, agent, onChange) {
    container.innerHTML = `<p class="settings-hint">Loading A2A agents…</p>`;
    try {
      if (!state.parsed.a2a) await loadParsed("a2a");
      const raw = state.parsed.a2a.value.agents;
      const agents = (raw && typeof raw === "object" && !Array.isArray(raw))
        ? Object.entries(raw).map(([name, ag]) => ({ name, ...ag })).filter(ag => ag.name)
        : [];
      renderAgentA2ABlockContent(container, agent, agents, onChange);
    } catch (e) {
      container.innerHTML = `<p class="settings-error">A2A unavailable: ${escHtml(e.message)}</p>`;
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

    const sub = state.activeSkillsSubtab;
    bodyEl.innerHTML = `
      <div class="settings-form">
        <div class="settings-subtabs" role="tablist">
          ${SKILLS_SUBTABS.map(t => `
            <button type="button" data-subtab="${t.id}" class="${sub === t.id ? "active" : ""}">${escHtml(t.label)}</button>
          `).join("")}
        </div>
        <div class="settings-subtab-body"></div>
      </div>
    `;

    bodyEl.querySelectorAll(".settings-subtabs button").forEach(b => {
      b.addEventListener("click", () => {
        if (state.activeSkillsSubtab === b.dataset.subtab) {
          if (b.dataset.subtab === "remotes" && state.skills.browsingRemote) {
            state.skills.browsingRemote = null;
            state.skills.viewingRemote = null;
            renderSkills();
          }
          return;
        }
        state.activeSkillsSubtab = b.dataset.subtab;
        renderSkills();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "remotes") {
      await renderRemoteRegistriesSection(host);
    } else {
      await renderSkillsInstalledTab(host);
    }
  }

  async function renderSkillsInstalledTab(host) {
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
        <div id="skills-list"></div>
      </section>
    `;

    renderSkillCards(host.querySelector("#skills-list"), skills);

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
        <p class="settings-hint">Skills live in <code>registry/skills/</code> — commit them yourself to track in git.</p>
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
      const sourceHtml = sk.source === "local"
        ? `<span class="source-badge source-badge-local">local</span>`
        : "";

      card.innerHTML = `
        <div class="skill-mkt-header">
          <span class="skill-mkt-filename">${ICONS.skills}${escHtml(sk.name)}</span>
          ${sourceHtml}
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

  // parseAgentFrontmatter extracts the YAML frontmatter fields used by Claude
  // Code agent files: name, description (plain or folded ">"), skills and
  // mcpServers (list). Returns null when no frontmatter block is found.
  function parseAgentFrontmatter(content) {
    const s = content.trimStart();
    if (!s.startsWith("---")) return null;
    const rest = s.slice(3);
    const idx = rest.indexOf("\n---");
    if (idx < 0) return null;
    const fm = rest.slice(0, idx);
    const result = {};
    const lines = fm.split("\n");
    let i = 0;
    while (i < lines.length) {
      const m = lines[i].match(/^(\w+):\s*(.*)/);
      if (!m) { i++; continue; }
      const key = m[1], val = m[2].trim();
      if (val === ">" || val === "|") {
        let multi = "";
        i++;
        while (i < lines.length && (lines[i].startsWith("  ") || lines[i] === "")) {
          multi += lines[i].trim() + " ";
          i++;
        }
        result[key] = multi.trim();
      } else if (val === "") {
        const items = [];
        i++;
        while (i < lines.length && /^\s+-\s+/.test(lines[i])) {
          items.push(lines[i].replace(/^\s+-\s+/, "").trim());
          i++;
        }
        result[key] = items;
      } else {
        result[key] = val;
        i++;
      }
    }
    return result;
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

  // ─── Agents — remote registries ───────────────────────────────────────
  //
  // Mirrors the skills "Remote registries" section: a CRUD list of remote
  // agent registries on top, with a browse/install marketplace under each
  // one. Backed by /api/agents/remotes/* on the server. The shared
  // remote_registries.json keeps "kind: agents" or "kind: both" entries
  // visible here; "kind: skills" entries don't appear.

  const remoteAgentsCache = {}; // keyed by registry ID → { agents, timestamp }

  // Top-level entry: renders the "Remotes" sub-tab inside the Agents pane.
  // Shared callback used by browse/detail views to re-render only the right
  // panel without rebuilding the full split layout. Set by renderAgentRemotesTab.
  let refreshRemotesRightFn = null;

  // host is the container provided by renderAgentForm.
  async function renderAgentRemotesTab(d, host) {
    if (!state.agentRemotes) state.agentRemotes = { browsing: null, viewing: null };
    if (!state.squadRemotes) state.squadRemotes = { browsing: null };
    if (!state.activeRemoteKind) state.activeRemoteKind = "agents";

    host.innerHTML = `
      <div class="agent-split-layout">
        <div class="agent-fleet-panel">
          <div class="agent-fleet-header">
            <span class="agent-fleet-title">REGISTRIES</span>
          </div>
          <div class="agent-fleet-list" id="remotes-kind-list"></div>
        </div>
        <div class="agent-detail-panel" id="remotes-right-panel"></div>
      </div>
    `;

    const kindList = host.querySelector("#remotes-kind-list");
    const rightEl  = host.querySelector("#remotes-right-panel");

    function renderKindNav() {
      kindList.innerHTML = "";
      for (const k of [{ id: "agents", label: "Agents" }, { id: "squads", label: "Squads" }]) {
        const item = document.createElement("div");
        item.className = "agent-fleet-item" + (state.activeRemoteKind === k.id ? " active" : "");
        item.innerHTML = `<div class="agent-fleet-item-name">${escHtml(k.label)}</div>`;
        item.addEventListener("click", () => {
          if (state.activeRemoteKind === k.id && !state.agentRemotes.browsing && !state.agentRemotes.viewing && !state.squadRemotes.browsing) return;
          state.activeRemoteKind = k.id;
          state.agentRemotes = { browsing: null, viewing: null };
          state.squadRemotes = { browsing: null };
          renderKindNav();
          refreshRight();
        });
        kindList.appendChild(item);
      }
    }

    async function refreshRight() {
      if (state.activeRemoteKind === "agents") {
        if (state.agentRemotes.viewing)  { await renderAgentRemoteDetailView(rightEl); return; }
        if (state.agentRemotes.browsing) { await renderAgentRemoteBrowseView(rightEl); return; }
        await renderAgentRegistryList(rightEl, refreshRight);
      } else {
        if (state.squadRemotes.browsing) { await renderSquadRemoteBrowseView(rightEl); return; }
        await renderSquadRegistryList(rightEl, refreshRight);
      }
    }

    refreshRemotesRightFn = refreshRight;
    renderKindNav();
    await refreshRight();
  }

  async function renderAgentRegistryList(rightEl, onRefresh) {
    rightEl.innerHTML = `
      <div class="remote-kind-list-wrap">
        <div class="remote-kind-list-header">
          <button type="button" class="add-btn" id="agent-remote-add">+ Add</button>
        </div>
        <div id="agent-remote-list"></div>
      </div>
    `;
    const container = rightEl.querySelector("#agent-remote-list");
    await refreshAgentRemoteList(container);
    rightEl.querySelector("#agent-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({ title: "Add Agent Registry", defaultKind: "agents" });
      if (!result) return;
      try {
        await skillsPost("/agents/remotes", result);
        if (onRefresh) await onRefresh(); else await refreshAgentRemoteList(container);
      } catch (e) {
        setStatus("Failed to add registry: " + e.message, "error");
      }
    });
  }

  async function renderSquadRegistryList(rightEl, onRefresh) {
    rightEl.innerHTML = `
      <div class="remote-kind-list-wrap">
        <div class="remote-kind-list-header">
          <button type="button" class="add-btn" id="squad-remote-add">+ Add</button>
        </div>
        <div id="squad-remote-list"></div>
      </div>
    `;
    const container = rightEl.querySelector("#squad-remote-list");
    await refreshSquadRemoteList(container);
    rightEl.querySelector("#squad-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({ title: "Add Squad Registry", defaultKind: "squads" });
      if (!result) return;
      try {
        await skillsPost("/squads-registry/remotes", result);
        if (onRefresh) await onRefresh(); else await refreshSquadRemoteList(container);
      } catch (e) {
        setStatus("Failed to add registry: " + e.message, "error");
      }
    });
  }

  async function refreshAgentRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">Loading…</p>`;
    let remotes;
    try {
      const res = await skillsGet("/agents/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote agent registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install agents.</p>`;
      return;
    }
    container.innerHTML = "";
    for (const r of remotes) {
      const providerLabel = r.provider ? r.provider.charAt(0).toUpperCase() + r.provider.slice(1) : "";
      const kindBadge = r.kind === "both" ? ` <span class="remote-reg-provider">Both</span>` : "";
      const row = document.createElement("div");
      row.className = "remote-reg-row";
      row.innerHTML = `
        <div class="remote-reg-info">
          <span class="remote-reg-name">${escHtml(r.name)}${providerLabel ? ` <span class="remote-reg-provider">${escHtml(providerLabel)}</span>` : ""}${kindBadge}</span>
          <span class="remote-reg-url">${escHtml(r.url)}</span>
        </div>
        <div class="remote-reg-actions">
          <button type="button" class="add-btn remote-browse-btn">Browse</button>
          <button type="button" class="edit-btn remote-edit-btn">Edit</button>
          <button type="button" class="del-btn remote-remove-btn">Remove</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.agentRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: "Edit Agent Registry",
          initial: { name: r.name, url: r.url, provider: r.provider || "", kind: r.kind, hasToken: !!r.has_token },
          isEdit: true,
          defaultKind: "agents",
        });
        if (!result) return;
        try {
          await skillsPut(`/agents/remotes/${r.id}`, result);
          delete remoteAgentsCache[r.id];
          await refreshAgentRemoteList(container);
        } catch (e) {
          setStatus("Failed to update registry: " + e.message, "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        const isBoth = r.kind === "both";
        const msg = isBoth
          ? `Remove "${r.name}" from the Agents tab? It will remain in the Skills tab.`
          : `Remove registry "${r.name}"?`;
        if (!await appConfirm(msg)) return;
        try {
          await skillsDel(`/agents/remotes/${r.id}`);
          delete remoteAgentsCache[r.id];
          await refreshAgentRemoteList(container);
        } catch (e) {
          setStatus("Failed to remove registry: " + e.message, "error");
        }
      });
      container.appendChild(row);
    }
  }

  async function renderAgentRemoteBrowseView(host) {
    const { id, name } = state.agentRemotes.browsing;
    const cached = remoteAgentsCache[id];
    const hasCached = !!(cached && (Date.now() - cached.timestamp < REMOTE_CACHE_TTL));

    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header remote-browse-top">
          <button type="button" class="skill-back-btn">Back to registries</button>
          <span class="remote-browse-refresh-badge"${hasCached ? "" : " hidden"}>Refreshing…</span>
        </div>
        ${!hasCached ? `
          <div class="remote-browse-loading">
            <p class="settings-loading">Browsing <strong>${escHtml(name)}</strong>…</p>
            <p class="settings-hint">Scanning the full repository tree for agent.json files. This may take a moment.</p>
          </div>
        ` : ""}
        <div id="agent-remote-browse-content"></div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.agentRemotes.browsing = null;
      refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
    });

    const contentEl = host.querySelector("#agent-remote-browse-content");

    function populateContent(agents) {
      contentEl.innerHTML = "";

      const truncated = agents.some(a => a.dir_path === "__truncated__");
      const real = agents.filter(a => a.dir_path !== "__truncated__");

      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${real.length} agent${real.length !== 1 ? "s" : ""}${truncated ? " (tree truncated — some agents may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!real.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = "No agents found in this registry.";
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const a of real) {
        const g = a.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(a);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1;
        if (b === "") return 1;
        return a.localeCompare(b);
      });

      function buildAgentCard(a) {
        const card = document.createElement("div");
        card.className = "skill-mkt-card remote-skill-card";

        const tagsHtml = (a.tags && a.tags.length)
          ? `<div class="skill-mkt-tags">${a.tags.map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("")}</div>`
          : "";
        const builtinHtml = a.builtin
          ? `<div class="skill-mkt-author"><span class="skill-mkt-author-icon">◆</span><span class="skill-mkt-author-name">Built-in</span></div>`
          : "";
        const actionHtml = a.installed
          ? `<span class="remote-skill-installed-badge">Installed</span>`
          : `<button type="button" class="add-btn remote-install-btn">Install</button>`;

        card.innerHTML = `
          <div class="skill-mkt-header">
            <span class="skill-mkt-filename">${escHtml(a.name)}</span>
            ${actionHtml}
          </div>
          <div class="skill-mkt-body">
            ${builtinHtml}
            <p class="skill-mkt-desc">${escHtml(a.description || "(no description)")}</p>
            ${tagsHtml}
          </div>
        `;

        if (!a.installed) {
          card.querySelector(".remote-install-btn").addEventListener("click", e => {
            e.stopPropagation();
            doInstallAgent(id, a, card);
          });
        }

        card.addEventListener("click", e => {
          if (e.target.closest(".remote-install-btn")) return;
          state.agentRemotes.viewing = { ...state.agentRemotes.browsing, agent: a };
          refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
        });

        return card;
      }

      for (const group of sortedGroups) {
        const groupAgents = grouped.get(group);
        if (group) {
          const groupHdr = document.createElement("div");
          groupHdr.className = "remote-group-header";
          groupHdr.textContent = group.replace(/\//g, " › ");
          contentEl.appendChild(groupHdr);
        }
        const grid = document.createElement("div");
        grid.className = "skill-marketplace-grid";
        for (const a of groupAgents) grid.appendChild(buildAgentCard(a));
        contentEl.appendChild(grid);
      }
    }

    if (hasCached) populateContent(cached.agents);

    let agents;
    try {
      const res = await skillsGet(`/agents/remotes/${id}/browse`);
      agents = res.agents || [];
    } catch (e) {
      if (!hasCached) {
        const loadEl = host.querySelector(".remote-browse-loading");
        if (loadEl) loadEl.outerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      }
      const badge = host.querySelector(".remote-browse-refresh-badge");
      if (badge) badge.hidden = true;
      return;
    }

    if (!host.contains(contentEl)) return;

    remoteAgentsCache[id] = { agents, timestamp: Date.now() };

    const loadEl = host.querySelector(".remote-browse-loading");
    if (loadEl) loadEl.remove();
    const badge = host.querySelector(".remote-browse-refresh-badge");
    if (badge) badge.hidden = true;

    populateContent(agents);
  }

  async function renderAgentRemoteDetailView(host) {
    const { id, name, agent } = state.agentRemotes.viewing;
    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to ${escHtml(name)}</button>
        </div>
        <div class="skill-frontmatter-card" id="agent-fm-card">
          <p class="settings-loading">Loading…</p>
        </div>
        <div class="skill-content-wrap">
          <div class="skill-md-preview markdown-body" id="agent-json-preview"></div>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${agent.installed
            ? `<span class="remote-skill-installed-badge">Installed</span>`
            : `<button type="button" class="add-btn remote-install-btn">Install</button>`}
        </div>
      </div>
    `;

    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.agentRemotes.viewing = null;
      refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
    });

    const fmCard = host.querySelector("#agent-fm-card");
    const preview = host.querySelector("#agent-json-preview");

    let content;
    try {
      const res = await skillsGet(`/agents/remotes/${id}/agent/${agent.dir_path}`);
      content = res.content;
    } catch (e) {
      fmCard.innerHTML = "";
      preview.textContent = "";
      preview.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }

    const isMarkdown = agent.format === "claude" || agent.dir_path.endsWith(".md");

    if (isMarkdown) {
      // Claude Code markdown format: parse YAML frontmatter for the card,
      // render the body as markdown.
      const fm = parseAgentFrontmatter(content);
      if (fm) {
        const rows = [];
        if (fm.name) rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">name</span><span class="skill-fm-value">${escHtml(fm.name)}</span></div>`);
        if (fm.description) rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">description</span><span class="skill-fm-value">${escHtml(fm.description)}</span></div>`);
        for (const listKey of ["skills", "mcpServers"]) {
          if (Array.isArray(fm[listKey]) && fm[listKey].length) {
            const tags = fm[listKey].map(t => `<span class="skill-mkt-tag">${escHtml(t)}</span>`).join("");
            rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(listKey)}</span><span class="skill-fm-value skill-fm-tags">${tags}</span></div>`);
          }
        }
        fmCard.innerHTML = rows.join("") || "";
      } else {
        fmCard.innerHTML = "";
      }
      preview.innerHTML = marked.parse(stripFrontmatter(content), { breaks: false, gfm: true });
    } else {
      // Native yoke JSON format: populate card from parsed fields, show raw JSON.
      let parsed = null;
      try { parsed = JSON.parse(content); } catch (_) { parsed = null; }
      if (parsed && typeof parsed === "object") {
        const rows = [];
        const keys = ["name", "description", "model_ref", "builtin", "leader"];
        for (const k of keys) {
          if (parsed[k] === undefined) continue;
          rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(k)}</span><span class="skill-fm-value">${escHtml(String(parsed[k]))}</span></div>`);
        }
        if (Array.isArray(parsed.tools) && parsed.tools.length) {
          const tags = parsed.tools.map(t => `<span class="skill-mkt-tag">${escHtml(String(t))}</span>`).join("");
          rows.push(`<div class="skill-fm-row"><span class="skill-fm-key">tools</span><span class="skill-fm-value skill-fm-tags">${tags}</span></div>`);
        }
        fmCard.innerHTML = rows.join("") || "";
      } else {
        fmCard.innerHTML = "";
      }
      preview.innerHTML = `<pre style="white-space:pre-wrap;">${escHtml(content)}</pre>`;
    }

    const installBtn = host.querySelector(".remote-install-btn");
    if (installBtn) {
      installBtn.addEventListener("click", () => {
        doInstallAgent(id, agent, host, installBtn);
      });
    }
  }

  // doInstallAgent prompts for the "Enable" toggle, posts the install, then
  // updates either the card or the detail-view footer. cardOrHost is the
  // surrounding card element (browse view) or the host (detail view);
  // installBtn is supplied only by the detail view so we know which button
  // to swap with the "Installed" badge.
  async function doInstallAgent(registryID, agentInfo, cardOrHost, installBtn) {
    const enable = await appAgentInstallDialog(agentInfo);
    if (enable === null) return; // cancelled

    const btn = installBtn || cardOrHost.querySelector(".remote-install-btn");
    if (btn) { btn.disabled = true; btn.textContent = "Installing…"; }
    try {
      const res = await skillsPost(`/agents/remotes/${registryID}/install/${agentInfo.dir_path}`, { enable });
      agentInfo.installed = true;
      if (btn) btn.outerHTML = `<span class="remote-skill-installed-badge">Installed</span>`;
      if (res.enable_error) {
        setStatus(`Agent "${res.name}" installed, but enabling failed: ${res.enable_error}`, "error");
      } else if (res.enabled) {
        await doReload();
        await loadParsed("agent");
        state.activeAgentSubtab = "agents";
        const newAgents = (state.parsed["agent"].value || {}).agents || [];
        const newIdx = newAgents.findIndex(a => a.name === res.name);
        state.activeAgentIdx = newIdx >= 0 ? newIdx : newAgents.length - 1;
        renderAgentForm();
        showInstallResult(`Agent "${res.name}" installed and enabled.`, res.warnings);
      } else {
        showInstallResult(`Agent "${res.name}" installed.`, res.warnings);
      }
    } catch (e) {
      if (btn) { btn.disabled = false; btn.textContent = "Install"; }
      setStatus("Install failed: " + e.message, "error");
    }
  }

  // appAgentInstallDialog shows the install confirmation with the "Enable"
  // checkbox. Resolves to true/false (enable flag) or null on cancel.
  function appAgentInstallDialog(agentInfo) {
    return new Promise(resolve => {
      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog registry-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");
      box.innerHTML = `
        <p class="app-dialog-msg">Install agent "${escHtml(agentInfo.name)}"?</p>
        <div class="registry-dialog-form">
          <p class="registry-dialog-hint">
            Files will be written to <code>$YOKE_HOME/registry/agents/${escHtml(agentInfo.name)}/</code>.
          </p>
          <label class="registry-dialog-toggle" for="agent-install-enable">
            <span>Enable in <code>config/agents.json</code> after install</span>
            <input type="checkbox" id="agent-install-enable" checked />
          </label>
          <p class="registry-dialog-hint">
            Adds the agent's name to the enabled list so the next reload wires it in.
            Leave unchecked to install on disk only — you can enable later from the Agents tab.
          </p>
        </div>
        <div class="app-dialog-actions">
          <button type="button" id="agent-install-cancel">Cancel</button>
          <button type="button" class="btn-primary" id="agent-install-ok">Install</button>
        </div>
      `;
      overlay.appendChild(box);
      document.body.appendChild(overlay);

      const close = result => { overlay.remove(); resolve(result); };
      box.querySelector("#agent-install-cancel").addEventListener("click", () => close(null));
      box.querySelector("#agent-install-ok").addEventListener("click", () => {
        const enable = box.querySelector("#agent-install-enable").checked;
        close(!!enable);
      });
      overlay.addEventListener("click", e => { if (e.target === overlay) close(null); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(null); }
        if (e.key === "Enter")  { e.stopPropagation(); box.querySelector("#agent-install-ok").click(); }
      });
    });
  }

  // ─── Squad remote registries ───────────────────────────────────────────

  const remoteSquadsCache = {}; // keyed by registry ID → { squads, timestamp }

  async function refreshSquadRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">Loading…</p>`;
    let remotes;
    try {
      const res = await skillsGet("/squads-registry/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote squad registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install squads.</p>`;
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
        state.squadRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: "Edit Squad Registry",
          initial: { name: r.name, url: r.url, provider: r.provider || "", kind: r.kind, hasToken: !!r.has_token },
          isEdit: true,
          defaultKind: "squads",
        });
        if (!result) return;
        try {
          await skillsPut(`/squads-registry/remotes/${r.id}`, result);
          delete remoteSquadsCache[r.id];
          await refreshSquadRemoteList(container);
        } catch (e) {
          setStatus("Failed to update registry: " + e.message, "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(`Remove squad registry "${r.name}"?`)) return;
        try {
          await skillsDel(`/squads-registry/remotes/${r.id}`);
          delete remoteSquadsCache[r.id];
          await refreshSquadRemoteList(container);
        } catch (e) {
          setStatus("Failed to remove registry: " + e.message, "error");
        }
      });
      container.appendChild(row);
    }
  }

  async function renderSquadRemoteBrowseView(host) {
    const { id, name } = state.squadRemotes.browsing;
    const cached = remoteSquadsCache[id];
    const hasCached = !!(cached && (Date.now() - cached.timestamp < REMOTE_CACHE_TTL));

    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header remote-browse-top">
          <button type="button" class="skill-back-btn">Back to registries</button>
          <span class="remote-browse-refresh-badge"${hasCached ? "" : " hidden"}>Refreshing…</span>
        </div>
        ${!hasCached ? `
          <div class="remote-browse-loading">
            <p class="settings-loading">Browsing <strong>${escHtml(name)}</strong>…</p>
            <p class="settings-hint">Scanning the repository tree for squad.json files.</p>
          </div>
        ` : ""}
        <div id="squad-remote-browse-content"></div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.squadRemotes.browsing = null;
      refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
    });

    const contentEl = host.querySelector("#squad-remote-browse-content");

    function populateContent(squads) {
      contentEl.innerHTML = "";

      const truncated = squads.some(s => s.dir_path === "__truncated__");
      const real = squads.filter(s => s.dir_path !== "__truncated__");

      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${real.length} squad${real.length !== 1 ? "s" : ""}${truncated ? " (tree truncated — some squads may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!real.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = "No squads found in this registry. Directories must contain a squad.json file.";
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const s of real) {
        const g = s.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(s);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1; if (b === "") return 1;
        return a.localeCompare(b);
      });

      for (const group of sortedGroups) {
        if (group) {
          const gh = document.createElement("div");
          gh.className = "remote-browse-group";
          gh.textContent = group.replace(/\//g, " › ");
          contentEl.appendChild(gh);
        }
        const grid = document.createElement("div");
        grid.className = "skill-marketplace-grid";

        for (const sq of grouped.get(group)) {
          const membersHtml = (sq.members && sq.members.length)
            ? `<div class="skill-mkt-tags">${sq.members.map(m => `<span class="skill-mkt-tag">${escHtml(m)}</span>`).join("")}</div>`
            : "";
          const leaderHtml = sq.leader
            ? `<div class="skill-mkt-author"><span class="skill-mkt-author-icon">◆</span><span class="skill-mkt-author-name">Leader: ${escHtml(sq.leader)}</span></div>`
            : "";
          const card = document.createElement("div");
          card.className = "skill-mkt-card remote-skill-card" + (sq.installed ? " skill-installed" : "");
          card.innerHTML = `
            <div class="skill-mkt-header">
              <span class="skill-mkt-filename">${escHtml(sq.name)}</span>
              ${sq.installed
                ? `<span class="remote-skill-installed-badge">Installed</span>`
                : `<button type="button" class="add-btn squad-install-btn">Install</button>`}
            </div>
            <div class="skill-mkt-body">
              ${leaderHtml}
              <p class="skill-mkt-desc">${escHtml(sq.description || "(no description)")}</p>
              ${membersHtml}
            </div>
          `;
          const btn = card.querySelector(".squad-install-btn");
          if (btn) {
            btn.addEventListener("click", e => {
              e.stopPropagation();
              doInstallSquad(id, sq, btn);
            });
          }
          grid.appendChild(card);
        }
        contentEl.appendChild(grid);
      }
    }

    if (hasCached) populateContent(cached.squads);

    let squads;
    try {
      const res = await skillsGet(`/squads-registry/remotes/${id}/browse`);
      squads = res.squads || [];
    } catch (e) {
      if (!hasCached) {
        const loadEl = host.querySelector(".remote-browse-loading");
        if (loadEl) loadEl.outerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      }
      const badge = host.querySelector(".remote-browse-refresh-badge");
      if (badge) badge.hidden = true;
      return;
    }

    if (!host.contains(contentEl)) return;

    remoteSquadsCache[id] = { squads, timestamp: Date.now() };

    const loadEl = host.querySelector(".remote-browse-loading");
    if (loadEl) loadEl.remove();
    const badge = host.querySelector(".remote-browse-refresh-badge");
    if (badge) badge.hidden = true;

    populateContent(squads);
  }

  async function doInstallSquad(registryID, squadInfo, btn) {
    if (!await appConfirm(`Install squad "${squadInfo.name}"? It will be merged into config/agents.json and available after the next reload.`)) return;

    btn.disabled = true;
    btn.textContent = "Installing…";
    try {
      const res = await skillsPost(`/squads-registry/remotes/${registryID}/install/${squadInfo.dir_path}`, {});
      squadInfo.installed = true;
      btn.outerHTML = `<span class="remote-skill-installed-badge">Installed</span>`;
      await doReload();
      // Refresh the in-memory agent config so the new squad is visible when
      // the user navigates to the squads sub-tab (no forced redirect).
      await loadParsed("agent");
      showInstallResult(`Squad "${res.name}" ${res.added ? "added" : "updated"} in config/agents.json.`, res.warnings);
      delete remoteSquadsCache[registryID];
    } catch (e) {
      btn.disabled = false;
      btn.textContent = "Install";
      setStatus("Install failed: " + e.message, "error");
    }
  }

  // importAgentDialog shows a paste/file-upload dialog for importing a Claude
  // Code sub-agent (.md or .json). Resolves to {content, enable} or null.
  function importAgentDialog() {
    return new Promise(resolve => {
      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog mcp-import-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");

      const titleEl = document.createElement("p");
      titleEl.className = "app-dialog-msg";
      titleEl.textContent = "Import Claude Code Agent";
      box.appendChild(titleEl);

      const hint = document.createElement("p");
      hint.className = "settings-hint";
      hint.style.margin = "0 0 6px";
      hint.innerHTML = "Paste a <code>.md</code> (YAML frontmatter) or <code>.json</code> agent definition, or load a file.";
      box.appendChild(hint);

      const fileRow = document.createElement("div");
      fileRow.style.cssText = "display:flex;gap:6px;margin-bottom:6px;";
      const fileInput = document.createElement("input");
      fileInput.type = "file";
      fileInput.accept = ".md,.json,text/plain,application/json";
      fileInput.style.display = "none";
      const browseBtn = document.createElement("button");
      browseBtn.type = "button";
      browseBtn.textContent = "Browse…";
      browseBtn.addEventListener("click", () => fileInput.click());
      fileInput.addEventListener("change", () => {
        const f = fileInput.files[0];
        if (!f) return;
        const reader = new FileReader();
        reader.onload = e => { ta.value = e.target.result; ta.focus(); };
        reader.readAsText(f);
      });
      fileRow.appendChild(browseBtn);
      fileRow.appendChild(fileInput);
      box.appendChild(fileRow);

      const ta = document.createElement("textarea");
      ta.className = "mcp-import-textarea";
      ta.spellcheck = false;
      ta.placeholder = `---\nname: my-agent\ndescription: What this agent does\ntools: Read, Grep, Bash\nmodel: sonnet\n---\n\nSystem prompt here…`;
      box.appendChild(ta);

      const enableRow = document.createElement("label");
      enableRow.className = "registry-dialog-toggle";
      enableRow.style.marginTop = "8px";
      const enableCheck = document.createElement("input");
      enableCheck.type = "checkbox";
      enableCheck.id = "agent-import-enable";
      enableCheck.checked = true;
      const enableLabel = document.createElement("span");
      enableLabel.innerHTML = "Enable in <code>config/agents.json</code> after import";
      enableRow.appendChild(enableLabel);
      enableRow.appendChild(enableCheck);
      box.appendChild(enableRow);

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.textContent = "Cancel";
      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = "Import";

      const close = result => { overlay.remove(); resolve(result); };
      cancelBtn.addEventListener("click", () => close(null));
      okBtn.addEventListener("click", () => {
        const v = ta.value.trim();
        if (!v) return;
        close({ content: v, enable: enableCheck.checked });
      });
      overlay.addEventListener("click", e => { if (e.target === overlay) close(null); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(null); }
      });

      actions.appendChild(cancelBtn);
      actions.appendChild(okBtn);
      box.appendChild(actions);
      overlay.appendChild(box);
      document.body.appendChild(overlay);
      ta.focus();
    });
  }

  // ─── MCP — remote registries ──────────────────────────────────────────
  //
  // Mirrors the skills/agents "Remote registries" section. Backed by
  // /api/mcp/remotes/* on the server. The shared remote_registries.json
  // keeps "kind: mcp" entries visible here only.

  const remoteMCPCache = {}; // keyed by registry ID → { tools, timestamp }

  async function renderMCPRemotesSection(host) {
    if (state.mcpRemotes.viewing) {
      await renderMCPRemoteToolView(host);
      return;
    }
    if (state.mcpRemotes.browsing) {
      await renderMCPRemoteBrowseView(host);
      return;
    }

    host.innerHTML = `
      <section class="form-section">
        <h3>Remote MCP registries
          <button type="button" class="add-btn" id="mcp-remote-add">+ Add</button>
        </h3>
        <div id="mcp-remote-list"></div>
      </section>
    `;
    const listEl = host.querySelector("#mcp-remote-list");
    await refreshMCPRemoteList(listEl);

    host.querySelector("#mcp-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({
        title: "Add MCP Registry",
        defaultKind: "mcp",
      });
      if (!result) return;
      try {
        await skillsPost("/mcp/remotes", result);
        await refreshMCPRemoteList(listEl);
      } catch (e) {
        setStatus("Failed to add registry: " + e.message, "error");
      }
    });
  }

  async function refreshMCPRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">Loading…</p>`;
    let remotes;
    try {
      const res = await skillsGet("/mcp/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote MCP registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install MCP servers.</p>`;
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
        state.mcpRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        renderMCPForm();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: "Edit MCP Registry",
          initial: { name: r.name, url: r.url, provider: r.provider || "", kind: r.kind, hasToken: !!r.has_token },
          isEdit: true,
          defaultKind: "mcp",
        });
        if (!result) return;
        try {
          await skillsPut(`/mcp/remotes/${r.id}`, result);
          delete remoteMCPCache[r.id];
          await refreshMCPRemoteList(container);
        } catch (e) {
          setStatus("Failed to update registry: " + e.message, "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(`Remove registry "${r.name}"?`)) return;
        try {
          await skillsDel(`/mcp/remotes/${r.id}`);
          delete remoteMCPCache[r.id];
          await refreshMCPRemoteList(container);
        } catch (e) {
          setStatus("Failed to remove registry: " + e.message, "error");
        }
      });
      container.appendChild(row);
    }
  }

  async function renderMCPRemoteBrowseView(host) {
    const { id, name } = state.mcpRemotes.browsing;
    const cached = remoteMCPCache[id];
    const hasCached = !!(cached && (Date.now() - cached.timestamp < REMOTE_CACHE_TTL));

    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header remote-browse-top">
          <button type="button" class="skill-back-btn">Back to registries</button>
          <span class="remote-browse-refresh-badge"${hasCached ? "" : " hidden"}>Refreshing…</span>
        </div>
        ${!hasCached ? `
          <div class="remote-browse-loading">
            <p class="settings-loading">Browsing <strong>${escHtml(name)}</strong>…</p>
            <p class="settings-hint">Scanning the repository tree for MCP server manifests. This may take a moment.</p>
          </div>
        ` : ""}
        <div id="mcp-remote-browse-content"></div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.mcpRemotes.browsing = null;
      renderMCPForm();
    });

    const contentEl = host.querySelector("#mcp-remote-browse-content");

    function populateContent(tools) {
      contentEl.innerHTML = "";

      const truncated = tools.some(t => t.dir_path === "__truncated__");
      const realTools = tools.filter(t => t.dir_path !== "__truncated__");

      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${realTools.length} server${realTools.length !== 1 ? "s" : ""}${truncated ? " (tree truncated — some entries may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!realTools.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = "No MCP servers found in this registry. Directories must contain a .json manifest file.";
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const t of realTools) {
        const g = t.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(t);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1; if (b === "") return 1;
        return a.localeCompare(b);
      });

      for (const group of sortedGroups) {
        if (group) {
          const gh = document.createElement("div");
          gh.className = "remote-group-header";
          gh.textContent = group.replace(/\//g, " › ");
          contentEl.appendChild(gh);
        }
        const grid = document.createElement("div");
        grid.className = "skill-marketplace-grid";

        for (const tool of grouped.get(group)) {
          const typeLabel = tool.type
            ? `<span class="skill-mkt-tag">${escHtml(tool.type)}</span>`
            : "";
          const actionHtml = tool.installed
            ? `<span class="remote-skill-installed-badge">Installed</span>`
            : `<button type="button" class="add-btn remote-install-btn">Install</button>`;
          const card = document.createElement("div");
          card.className = "skill-mkt-card remote-skill-card";
          if (tool.has_readme) card.style.cursor = "pointer";
          card.innerHTML = `
            <div class="skill-mkt-header">
              <span class="skill-mkt-filename">${ICONS.mcp}${escHtml(tool.name)}</span>
              ${actionHtml}
            </div>
            <div class="skill-mkt-body">
              ${tool.description ? `<p class="skill-mkt-desc">${escHtml(tool.description)}</p>` : ""}
              ${typeLabel ? `<div class="skill-mkt-tags">${typeLabel}</div>` : ""}
            </div>
          `;
          if (tool.has_readme) {
            card.addEventListener("click", e => {
              if (e.target.closest(".remote-install-btn")) return;
              state.mcpRemotes.viewing = { ...state.mcpRemotes.browsing, tool };
              renderMCPForm();
            });
          }
          const btn = card.querySelector(".remote-install-btn");
          if (btn) {
            btn.addEventListener("click", async () => {
              btn.disabled = true;
              btn.textContent = "Installing…";
              try {
                const res = await skillsPost(`/mcp/remotes/${id}/install/${tool.dir_path}`, {});
                tool.installed = true;
                btn.replaceWith(Object.assign(document.createElement("span"), {
                  className: "remote-skill-installed-badge",
                  textContent: "Installed",
                }));
                showInstallResult(`MCP server "${res.name}" added to mcp_config.json. Reload to apply.`, res.warnings);
                showBanner();
                delete remoteMCPCache[id];
                delete state.parsed["mcp"];
              } catch (e) {
                btn.disabled = false;
                btn.textContent = "Install";
                setStatus("Install failed: " + e.message, "error");
              }
            });
          }
          grid.appendChild(card);
        }
        contentEl.appendChild(grid);
      }
    }

    if (hasCached) {
      populateContent(cached.tools);
    }

    try {
      const res = await skillsGet(`/mcp/remotes/${id}/browse`);
      const tools = res.tools || [];
      remoteMCPCache[id] = { tools, timestamp: Date.now() };
      host.querySelector(".remote-browse-refresh-badge")?.setAttribute("hidden", "");
      host.querySelector(".remote-browse-loading")?.remove();
      populateContent(tools);
    } catch (e) {
      if (!hasCached) {
        contentEl.innerHTML = `<p class="settings-error">Failed to browse registry: ${escHtml(e.message)}</p>`;
      } else {
        setStatus("Failed to refresh registry: " + e.message, "error");
      }
    }
  }

  async function renderMCPRemoteToolView(host) {
    const { id, name, tool } = state.mcpRemotes.viewing;

    host.innerHTML = `
      <div class="settings-form skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to ${escHtml(name)}</button>
        </div>
        <div class="skill-frontmatter-card">
          <div class="skill-fm-row"><span class="skill-fm-key">name</span><span class="skill-fm-value">${escHtml(tool.name)}</span></div>
          ${tool.type ? `<div class="skill-fm-row"><span class="skill-fm-key">type</span><span class="skill-fm-value">${escHtml(tool.type)}</span></div>` : ""}
          ${tool.description ? `<div class="skill-fm-row"><span class="skill-fm-key">description</span><span class="skill-fm-value">${escHtml(tool.description)}</span></div>` : ""}
        </div>
        <div class="skill-content-wrap">
          <div class="skill-md-preview markdown-body" id="mcp-tool-readme"><p class="settings-loading">Loading README…</p></div>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${tool.installed
            ? `<span class="remote-skill-installed-badge">Installed</span>`
            : `<button type="button" class="add-btn remote-install-btn">Install</button>`}
        </div>
      </div>
    `;

    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.mcpRemotes.viewing = null;
      renderMCPForm();
    });

    const readmeEl = host.querySelector("#mcp-tool-readme");
    try {
      const res = await skillsGet(`/mcp/remotes/${id}/readme/${tool.dir_path}`);
      if (typeof marked !== "undefined") {
        readmeEl.innerHTML = marked.parse(res.content);
      } else {
        readmeEl.textContent = res.content;
      }
    } catch (e) {
      readmeEl.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
    }

    const installBtn = host.querySelector(".remote-install-btn");
    if (installBtn) {
      installBtn.addEventListener("click", async () => {
        installBtn.disabled = true;
        installBtn.textContent = "Installing…";
        try {
          const res = await skillsPost(`/mcp/remotes/${id}/install/${tool.dir_path}`, {});
          tool.installed = true;
          installBtn.outerHTML = `<span class="remote-skill-installed-badge">Installed</span>`;
          showInstallResult(`MCP server "${res.name}" added to mcp_config.json. Reload to apply.`, res.warnings);
          showBanner();
          delete remoteMCPCache[id];
          delete state.parsed["mcp"];
        } catch (e) {
          installBtn.disabled = false;
          installBtn.textContent = "Install";
          setStatus("Install failed: " + e.message, "error");
        }
      });
    }
  }

  // ─── A2A ──────────────────────────────────────────────────────────────

  const remoteA2ACache = {}; // keyed by registry ID → { agents, timestamp }

  function renderA2AForm() {
    const id = "a2a";
    const d = state.parsed[id].value;
    if (!d.agents || typeof d.agents !== "object" || Array.isArray(d.agents)) d.agents = {};
    if (!Array.isArray(d.inputs)) d.inputs = [];

    const sub = state.activeA2ASubtab;
    bodyEl.innerHTML = `
      <div class="settings-form">
        <div class="settings-subtabs" role="tablist">
          ${A2A_SUBTABS.map(t => `
            <button type="button" data-subtab="${t.id}" class="${sub === t.id ? "active" : ""}">${escHtml(t.label)}</button>
          `).join("")}
        </div>
        <div class="settings-subtab-body"></div>
      </div>
    `;

    bodyEl.querySelectorAll(".settings-subtabs button").forEach(b => {
      b.addEventListener("click", () => {
        if (state.activeA2ASubtab === b.dataset.subtab) {
          if (b.dataset.subtab === "remotes" && state.a2aRemotes.browsing) {
            state.a2aRemotes.browsing = null;
            renderA2AForm();
          }
          return;
        }
        if (b.dataset.subtab === "remotes") {
          state.a2aRemotes.browsing = null;
        }
        state.activeA2ASubtab = b.dataset.subtab;
        renderA2AForm();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "remotes") {
      renderA2ARemotesSection(host);
    } else {
      renderA2AAgentsSubtab(host, d);
    }
  }

  function renderA2AAgentsSubtab(host, d) {
    host.innerHTML = `
      <section class="form-section">
        <h3>Inputs</h3>
        <p class="settings-hint">Declare values the user is prompted for at first use. Reference them from agent fields as <code>\${input:id}</code>.</p>
        <div id="a2a-inputs"></div>
      </section>
      <section class="form-section">
        <h3>A2A Agents</h3>
        <p class="settings-hint">Remote agent endpoints reachable via the <a href="https://google.github.io/A2A" target="_blank" rel="noopener">Agent-to-Agent (A2A)</a> protocol.</p>
        <div id="a2a-list"></div>
      </section>
    `;
    renderA2AInputs(d);
    renderA2AList(d);
    updateFooter();
  }

  function renderA2AInputs(d) {
    const el = bodyEl.querySelector("#a2a-inputs");
    if (!el) return;
    el.innerHTML = "";

    if (!Array.isArray(d.inputs)) d.inputs = [];

    const chips = document.createElement("div");
    chips.className = "mcp-input-chips";

    d.inputs.forEach((inp, idx) => {
      if (!inp.type) inp.type = "promptString";
      if (inp.type === "pickString" && !Array.isArray(inp.options)) inp.options = [];

      const chip = document.createElement("div");
      chip.className = "mcp-input-chip";
      chip.dataset.kind = inp.type === "pickString" ? "pick" : (inp.password ? "secret" : "text");
      chip.tabIndex = 0;
      chip.setAttribute("role", "button");
      chip.title = inp.description || inp.id || "";

      const icon = document.createElement("span");
      icon.className = "mcp-input-chip-icon";
      icon.innerHTML = inp.type === "pickString"
        ? LIST_ICON_SVG
        : (inp.password ? LOCK_ICON_SVG : "");
      if (icon.innerHTML) chip.appendChild(icon);

      const label = document.createElement("span");
      label.className = "mcp-input-chip-label";
      label.textContent = inp.id || "(no id)";
      chip.appendChild(label);

      const close = document.createElement("button");
      close.type = "button";
      close.className = "mcp-input-chip-close";
      close.setAttribute("aria-label", "Remove input");
      close.innerHTML = CLOSE_ICON_SVG;
      close.addEventListener("click", e => {
        e.stopPropagation();
        d.inputs.splice(idx, 1);
        markFormDirty("a2a");
        renderA2AInputs(d);
      });
      chip.appendChild(close);

      const openEditor = async () => {
        const result = await appMCPInputDialog(inp, d.inputs.filter((_, i) => i !== idx));
        if (!result) return;
        d.inputs[idx] = result;
        markFormDirty("a2a");
        renderA2AInputs(d);
      };
      chip.addEventListener("click", openEditor);
      chip.addEventListener("keydown", e => {
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); openEditor(); }
      });

      chips.appendChild(chip);
    });

    const addChip = document.createElement("button");
    addChip.type = "button";
    addChip.className = "mcp-input-chip mcp-input-chip-add";
    addChip.innerHTML = `<span class="mcp-input-chip-icon">+</span><span class="mcp-input-chip-label">Add input</span>`;
    addChip.addEventListener("click", async () => {
      const result = await appMCPInputDialog(
        { id: "", type: "promptString", description: "" },
        d.inputs,
      );
      if (!result) return;
      d.inputs.push(result);
      markFormDirty("a2a");
      renderA2AInputs(d);
    });
    chips.appendChild(addChip);

    el.appendChild(chips);

    if (d.inputs.length === 0) {
      const empty = document.createElement("p");
      empty.className = "settings-hint";
      empty.style.marginTop = "0.5rem";
      empty.textContent = "No inputs declared. Add one if any agent needs a user-supplied value.";
      el.appendChild(empty);
    }
  }

  function renderA2AList(d) {
    const container = bodyEl.querySelector("#a2a-list");
    if (!container) return;
    container.innerHTML = "";

    const agents = (d.agents && typeof d.agents === "object") ? d.agents : {};
    const names = Object.keys(agents).sort();

    const grid = document.createElement("div");
    grid.className = "mcp-cards-grid";

    names.forEach(name => {
      const a = agents[name];
      if (!a.headers || typeof a.headers !== "object") a.headers = {};
      let currentName = name;

      const card = document.createElement("div");
      card.className = "mcp-card";
      card.innerHTML = `
        <div class="mcp-card-hdr">
          <div class="mcp-card-title">
            <span class="model-status-dot dot-active"></span>
            <strong class="mcp-card-name">${escHtml(currentName || "(unnamed)")}</strong>
          </div>
          <button type="button" class="del-btn a2a-remove">Delete</button>
        </div>
        <div class="mcp-card-body"></div>
      `;
      const body = card.querySelector(".mcp-card-body");
      const nameEl = card.querySelector(".mcp-card-name");

      function a2aField(label, val, onCh, opts = {}) {
        const f = document.createElement("div");
        f.className = "model-field" + (opts.full ? " model-field-full" : "");
        const lbl = document.createElement("label");
        lbl.className = "model-field-label model-field-label--title";
        lbl.textContent = label;
        const inp = document.createElement("input");
        inp.type = "text";
        inp.className = "model-field-input";
        inp.value = val == null ? "" : String(val);
        if (opts.placeholder) inp.placeholder = opts.placeholder;
        inp.addEventListener("input", () => onCh(inp.value));
        f.appendChild(lbl);
        f.appendChild(inp);
        return f;
      }

      function a2aSection(title) {
        const sec = document.createElement("section");
        sec.className = "mcp-section";
        const h = document.createElement("h4");
        h.className = "mcp-section-title";
        h.textContent = title;
        sec.appendChild(h);
        return sec;
      }

      function a2aAddButton(label, onClick) {
        const b = document.createElement("button");
        b.type = "button";
        b.className = "mcp-add-full";
        b.innerHTML = `<span class="mcp-add-full-icon">+</span><span>${escHtml(label)}</span>`;
        b.addEventListener("click", onClick);
        return b;
      }

      function a2aTrashBtn(onClick) {
        const b = document.createElement("button");
        b.type = "button";
        b.className = "mcp-trash";
        b.setAttribute("aria-label", "Remove");
        b.innerHTML = TRASH_ICON_SVG;
        b.addEventListener("click", onClick);
        return b;
      }

      // ── General Settings ────────────────────────────────────────────
      const general = a2aSection("General Settings");
      const generalGrid = document.createElement("div");
      generalGrid.className = "model-field-grid";
      generalGrid.appendChild(a2aField("Name", currentName, v => {
        const nv = v.trim();
        nameEl.textContent = nv || "(unnamed)";
        if (!nv || nv === currentName) return;
        if (Object.prototype.hasOwnProperty.call(agents, nv)) return;
        delete agents[currentName];
        agents[nv] = a;
        currentName = nv;
        markFormDirty("a2a");
      }));
      general.appendChild(generalGrid);

      // ── Connection ──────────────────────────────────────────────────
      const conn = a2aSection("Connection");
      const connGrid = document.createElement("div");
      connGrid.className = "model-field-grid";
      connGrid.appendChild(a2aField("URL", a.url, v => { a.url = v; markFormDirty("a2a"); }, {
        full: true,
        placeholder: "https://agent.example.com/",
      }));
      connGrid.appendChild(a2aField("Description", a.description, v => { a.description = v; markFormDirty("a2a"); }, {
        full: true,
        placeholder: "Optional description",
      }));
      conn.appendChild(connGrid);
      body.appendChild(general);
      body.appendChild(conn);

      // ── Routing ─────────────────────────────────────────────────────
      const routing = a2aSection("Routing");
      const routingGrid = document.createElement("div");
      routingGrid.className = "model-field-grid";
      routingGrid.appendChild(a2aField("Squad", a.squad, v => { a.squad = v || undefined; markFormDirty("a2a"); }, {
        placeholder: "default (leave blank)",
      }));
      routingGrid.appendChild(a2aField("Session Name", a.session_name, v => { a.session_name = v || undefined; markFormDirty("a2a"); }, {
        placeholder: "leave blank for ephemeral",
      }));
      routing.appendChild(routingGrid);

      const createRow = document.createElement("div");
      createRow.className = "model-field";
      const createLabel = document.createElement("label");
      createLabel.style.cssText = "display:flex;align-items:center;gap:0.5rem;cursor:pointer;";
      const createChk = document.createElement("input");
      createChk.type = "checkbox";
      createChk.checked = !!a.create;
      createChk.addEventListener("change", () => { a.create = createChk.checked || undefined; markFormDirty("a2a"); });
      const createSpan = document.createElement("span");
      createSpan.className = "model-field-label model-field-label--title";
      createSpan.style.margin = "0";
      createSpan.textContent = "Create session if missing";
      createLabel.appendChild(createChk);
      createLabel.appendChild(createSpan);
      createRow.appendChild(createLabel);
      routing.appendChild(createRow);
      body.appendChild(routing);

      // ── Headers ─────────────────────────────────────────────────────
      const headersSec = a2aSection("Headers");
      const headersGrid = document.createElement("div");
      headersGrid.className = "mcp-kv-grid";
      const headersHdr = document.createElement("div");
      headersHdr.className = "mcp-kv-headers";
      headersHdr.innerHTML = `
        <span class="model-field-label model-field-label--title">Key</span>
        <span class="model-field-label model-field-label--title">Value</span>
        <span></span>
      `;
      headersGrid.appendChild(headersHdr);
      const headersRows = document.createElement("div");
      headersRows.className = "mcp-kv-rows";
      headersGrid.appendChild(headersRows);
      headersSec.appendChild(headersGrid);

      const store = a.headers;
      const drawHeaders = () => {
        headersRows.innerHTML = "";
        Object.entries(store).forEach(([k, v]) => {
          const r = document.createElement("div");
          r.className = "mcp-kv-row";
          r.innerHTML = `
            <input type="text" class="kv-k" placeholder="Header-Name" value="${escHtml(k)}" />
            <input type="text" class="kv-v" placeholder="value or Bearer \${input:id}" value="${escHtml(v)}" />
          `;
          const kIn = r.querySelector(".kv-k");
          const vIn = r.querySelector(".kv-v");
          let oldKey = k;
          kIn.addEventListener("change", () => {
            const nk = kIn.value.trim();
            if (!nk || nk === oldKey) return;
            const val = store[oldKey];
            delete store[oldKey];
            store[nk] = val;
            oldKey = nk;
            markFormDirty("a2a");
          });
          vIn.addEventListener("input", () => { store[oldKey] = vIn.value; markFormDirty("a2a"); });
          r.appendChild(a2aTrashBtn(() => { delete store[oldKey]; markFormDirty("a2a"); drawHeaders(); }));
          headersRows.appendChild(r);
        });
      };
      drawHeaders();
      headersSec.appendChild(a2aAddButton("Add Header", async () => {
        let nk = await appPrompt("Header name (e.g. Authorization):");
        if (!nk) return;
        nk = nk.trim();
        if (!nk || nk in store) return;
        store[nk] = "";
        markFormDirty("a2a"); drawHeaders();
      }));
      body.appendChild(headersSec);

      card.querySelector(".a2a-remove").addEventListener("click", async () => {
        if (!await appConfirm(`Remove agent "${currentName}"?`)) return;
        delete agents[currentName];
        markFormDirty("a2a"); renderA2AList(d);
      });

      grid.appendChild(card);
    });

    // Empty "Add A2A Agent" card
    const emptyCard = document.createElement("div");
    emptyCard.className = "mcp-card mcp-card-empty";
    const emptyBtn = document.createElement("button");
    emptyBtn.type = "button";
    emptyBtn.className = "model-card-empty-btn";
    emptyBtn.innerHTML = `
      <span class="model-card-empty-icon">⊕</span>
      <span class="model-card-empty-label">Add A2A Agent</span>
      <span class="model-card-empty-sub">Configure a remote Agent-to-Agent endpoint</span>
    `;
    emptyBtn.addEventListener("click", () => {
      let base = "new-agent";
      let candidate = base;
      let i = 1;
      while (Object.prototype.hasOwnProperty.call(agents, candidate)) {
        i++;
        candidate = `${base}-${i}`;
      }
      agents[candidate] = { url: "", headers: {} };
      markFormDirty("a2a");
      renderA2AList(d);
    });
    emptyCard.appendChild(emptyBtn);
    grid.appendChild(emptyCard);

    container.appendChild(grid);
  }

  async function renderA2ARemotesSection(host) {
    if (state.a2aRemotes.browsing) {
      await renderA2ARemoteBrowseView(host);
      return;
    }

    host.innerHTML = `
      <section class="form-section">
        <h3>Remote A2A registries
          <button type="button" class="add-btn" id="a2a-remote-add">+ Add</button>
        </h3>
        <div id="a2a-remote-list"></div>
      </section>
    `;
    const listEl = host.querySelector("#a2a-remote-list");
    await refreshA2ARemoteList(listEl);

    host.querySelector("#a2a-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({
        title: "Add A2A Registry",
        defaultKind: "a2a",
      });
      if (!result) return;
      try {
        await skillsPost("/a2a/remotes", result);
        await refreshA2ARemoteList(listEl);
      } catch (e) {
        setStatus("Failed to add registry: " + e.message, "error");
      }
    });
  }

  async function refreshA2ARemoteList(container) {
    container.innerHTML = `<p class="settings-loading">Loading…</p>`;
    let remotes;
    try {
      const res = await skillsGet("/a2a/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote A2A registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install A2A agent endpoints.</p>`;
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
        state.a2aRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        renderA2AForm();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: "Edit A2A Registry",
          initial: { name: r.name, url: r.url, provider: r.provider || "", kind: r.kind, hasToken: !!r.has_token },
          isEdit: true,
          defaultKind: "a2a",
        });
        if (!result) return;
        try {
          await skillsPut(`/a2a/remotes/${r.id}`, result);
          delete remoteA2ACache[r.id];
          await refreshA2ARemoteList(container);
        } catch (e) {
          setStatus("Failed to update registry: " + e.message, "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(`Remove registry "${r.name}"?`)) return;
        try {
          await skillsDel(`/a2a/remotes/${r.id}`);
          delete remoteA2ACache[r.id];
          await refreshA2ARemoteList(container);
        } catch (e) {
          setStatus("Failed to remove registry: " + e.message, "error");
        }
      });
      container.appendChild(row);
    }
  }

  async function renderA2ARemoteBrowseView(host) {
    const { id, name } = state.a2aRemotes.browsing;
    const cached = remoteA2ACache[id];
    const hasCached = !!(cached && (Date.now() - cached.timestamp < REMOTE_CACHE_TTL));

    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header remote-browse-top">
          <button type="button" class="skill-back-btn">Back to registries</button>
          <span class="remote-browse-refresh-badge"${hasCached ? "" : " hidden"}>Refreshing…</span>
        </div>
        ${!hasCached ? `
          <div class="remote-browse-loading">
            <p class="settings-loading">Browsing <strong>${escHtml(name)}</strong>…</p>
            <p class="settings-hint">Scanning the repository tree for a2a.json files. This may take a moment.</p>
          </div>
        ` : ""}
        <div id="a2a-remote-browse-content"></div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.a2aRemotes.browsing = null;
      renderA2AForm();
    });

    const contentEl = host.querySelector("#a2a-remote-browse-content");

    function populateContent(agents) {
      contentEl.innerHTML = "";

      const truncated = agents.some(a => a.dir_path === "__truncated__");
      const realAgents = agents.filter(a => a.dir_path !== "__truncated__");

      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${realAgents.length} agent${realAgents.length !== 1 ? "s" : ""}${truncated ? " (tree truncated — some entries may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!realAgents.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = "No A2A agents found in this registry. Directories must contain an a2a.json file.";
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const a of realAgents) {
        const g = a.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(a);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1; if (b === "") return 1;
        return a.localeCompare(b);
      });

      for (const group of sortedGroups) {
        if (group) {
          const gh = document.createElement("div");
          gh.className = "remote-browse-group";
          gh.textContent = group;
          contentEl.appendChild(gh);
        }
        const grid = document.createElement("div");
        grid.className = "remote-skill-grid";

        for (const agent of grouped.get(group)) {
          let urlTag = "";
          if (agent.url) {
            try { urlTag = `<span class="skill-mkt-tag">${escHtml(new URL(agent.url).hostname)}</span>`; } catch (_) {}
          }
          const card = document.createElement("div");
          card.className = "skill-mkt-card remote-skill-card" + (agent.installed ? " skill-installed" : "");
          card.innerHTML = `
            <div class="skill-mkt-card-body">
              <div class="skill-mkt-name">${escHtml(agent.name)}</div>
              ${agent.description ? `<div class="skill-mkt-desc">${escHtml(agent.description)}</div>` : ""}
              ${urlTag ? `<div class="skill-mkt-tags">${urlTag}</div>` : ""}
            </div>
            <div class="skill-mkt-card-actions">
              ${agent.installed
                ? `<span class="skill-installed-badge">Installed</span>`
                : `<button type="button" class="add-btn a2a-install-btn" data-dir="${escHtml(agent.dir_path)}">Install</button>`}
            </div>
          `;
          const btn = card.querySelector(".a2a-install-btn");
          if (btn) {
            btn.addEventListener("click", async () => {
              btn.disabled = true;
              btn.textContent = "Installing…";
              try {
                const res = await skillsPost(`/a2a/remotes/${id}/install/${agent.dir_path}`, {});
                agent.installed = true;
                card.classList.add("skill-installed");
                btn.replaceWith(Object.assign(document.createElement("span"), {
                  className: "skill-installed-badge",
                  textContent: "Installed",
                }));
                setStatus(`A2A agent "${res.name}" added to a2a_config.json. Reload to apply.`, "success");
                showBanner();
                delete remoteA2ACache[id];
              } catch (e) {
                btn.disabled = false;
                btn.textContent = "Install";
                setStatus("Install failed: " + e.message, "error");
              }
            });
          }
          grid.appendChild(card);
        }
        contentEl.appendChild(grid);
      }
    }

    if (hasCached) {
      populateContent(cached.agents);
    }

    try {
      const res = await skillsGet(`/a2a/remotes/${id}/browse`);
      const agents = res.agents || [];
      remoteA2ACache[id] = { agents, timestamp: Date.now() };
      host.querySelector(".remote-browse-refresh-badge")?.setAttribute("hidden", "");
      host.querySelector(".remote-browse-loading")?.remove();
      populateContent(agents);
    } catch (e) {
      if (!hasCached) {
        contentEl.innerHTML = `<p class="settings-error">Failed to browse registry: ${escHtml(e.message)}</p>`;
      } else {
        setStatus("Failed to refresh registry: " + e.message, "error");
      }
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
        const r = await fetch(BASE_PATH + `/api/config/file/${id}`, {
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
        const r = await fetch(BASE_PATH + `/api/config/parsed/${id}`, {
          method: "PUT",
          headers: authHeaders({ "Content-Type": "application/json" }),
          body: JSON.stringify({ data: prepareForSave(id, p.value), mtime: p.mtime }),
        });
        if (!r.ok) throw new Error(await errText(r));
        const j = await r.json();
        p.data = deepClone(p.value);
        p.mtime = j.mtime;
        p.dirty = false;
        // Invalidate raw cache so the raw view re-fetches the canonical JSON.
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
    refreshGenerationPill();
    syncThemeFromServer();
    const btn = document.getElementById("settings-btn");
    if (btn) btn.addEventListener("click", () => {
      if (isOpen()) close(); else open();
    });
  });
})();
