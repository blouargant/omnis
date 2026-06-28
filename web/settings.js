// Settings panel — omnis configuration editor.
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
    hooks: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M18 16.98h-5.99c-1.1 0-1.95.94-2.48 1.9A4 4 0 0 1 2 17c.01-.7.2-1.4.57-2"/><path d="m6 17 3.13-5.78c.53-.97.1-2.18-.5-3.1a4 4 0 1 1 6.89-4.06"/><path d="m12 6 3.13 5.73C15.66 12.7 16.9 13 18 13a4 4 0 0 1 0 8"/></svg>`,
    skills: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>`,
    appearance: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18c1.5 0 2-1 2-2 0-1.5 1-2 2-2h2a3 3 0 0 0 3-3 9 9 0 0 0-9-9z"/><circle cx="7.5" cy="10.5" r="1"/><circle cx="12" cy="7.5" r="1"/><circle cx="16.5" cy="10.5" r="1"/></svg>`,
    documentation: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z"/><path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z"/></svg>`,
    "user-commands": `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="3"/><line x1="15" y1="7" x2="9" y2="17"/></svg>`,
    registries: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></svg>`,
    automation: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><polyline points="12 7 12 12 15 14"/></svg>`,
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
    { id: "hooks",       label: "Hooks",       form: "hooks" },
  ];

  // Sidebar menu entries (JSON configs + client-only views like Appearance).
  // `title` is the human-readable section name shown in the breadcrumb header.
  const APPEARANCE_ID = "appearance";
  const DOCUMENTATION_ID = "documentation";
  const USER_COMMANDS_ID = "user-commands";
  const REGISTRIES_ID = "registries";
  const AUTOMATION_ID = "automation";
  const MENU_ITEMS = [
    { id: "skills",        label: "Skills",      title: "Skills",                    kind: "client" },
    { id: "agent",         label: "Agents",      title: "Agent Configuration",       kind: "json" },
    { id: "models",        label: "Models",      title: "Models & Providers",        kind: "json" },
    { id: "permissions",   label: "Permissions", title: "Permissions",               kind: "json" },
    { id: "mcp",           label: "MCP",         title: "MCP Servers",               kind: "json" },
    { id: "a2a",           label: "A2A",         title: "A2A Agents",                kind: "json" },
    { id: "hooks",         label: "Hooks",       title: "Lifecycle Hooks",           kind: "json" },
    { id: USER_COMMANDS_ID,label: "Commands",    title: "Slash Commands",            kind: "client" },
    { id: AUTOMATION_ID,   label: "Automation",  title: "Automation",                kind: "client" },
    { id: REGISTRIES_ID,   label: "Registries",  title: "Remote Registries",         kind: "client" },
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
    { id: "learning-recall", file: "20-learning-and-recall.md", label: "Learning & Recall", group: "Core Concepts" },
    { id: "agents-settings", file: "19-agents.md",          label: "Agents Settings",      group: "Core Concepts" },
    { id: "mcp-concept",     file: "12-mcp.md",             label: "MCP Servers",     group: "Core Concepts" },
    { id: "a2a",             file: "17-a2a.md",             label: "A2A Agents",          group: "Core Concepts" },
    { id: "commands",        file: "18-commands.md",        label: "Slash Commands",       group: "Core Concepts" },
    { id: "agent-md",        file: "21-agent-md.md",        label: "Project Memory (AGENT.md)", group: "Core Concepts" },
    { id: "permissions-concept", file: "13-permissions.md", label: "Permissions",     group: "Core Concepts" },
    { id: "hooks-concept",   file: "22-hooks.md",           label: "Lifecycle Hooks", group: "Core Concepts" },
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
  // Default skin applied when the user has never picked a theme (no localStorage
  // entry and no server-side preference). VS Code Dark remains selectable as the
  // empty-id :root palette; this only governs the first-run fallback.
  const DEFAULT_THEME = "vscode-light";
  // localStorage cache for the unified desktop-notification preference. The
  // durable source of truth is the server preferences.json (user home); this
  // cache is what the synchronous fire path in app.js reads.
  const NOTIFY_STORAGE_KEY = "agent_toolkit_os_notify";
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
    // null = never chosen → fall back to the default skin; "" = explicit VS Code Dark.
    const v = localStorage.getItem(THEME_STORAGE_KEY);
    return v === null ? DEFAULT_THEME : v;
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

  // prefsReady resolves once the boot sync below has loaded the server-side
  // preferences (or to null on failure, so awaiters never hang). The first-run
  // notification opt-in in app.js awaits this to decide whether to prompt.
  let _resolvePrefs;
  const prefsReady = new Promise((resolve) => { _resolvePrefs = resolve; });

  // saveNotifications records the unified desktop-notification choice: the
  // localStorage cache the fire path reads, plus the durable server file
  // (merged server-side, so it doesn't clobber the theme). Returns the PUT
  // promise so callers can await persistence.
  function saveNotifications(enabled) {
    localStorage.setItem(NOTIFY_STORAGE_KEY, enabled ? "1" : "0");
    return fetch(BASE_PATH + "/api/preferences", {
      method: "PUT",
      headers: authHeaders({ "Content-Type": "application/json" }),
      body: JSON.stringify({ notifications: !!enabled }),
    }).catch(() => { /* offline / unauthenticated — local cache wins */ });
  }

  // notifyBlockedHelp explains how to unblock notifications in the browser when
  // the site-level permission is blocking them (a website can't grant it itself).
  // Reuses app.js's themed modal when available, falling back to a plain alert.
  function notifyBlockedHelp() {
    if (typeof window.showNotificationBlockedHelp === "function") { window.showNotificationBlockedHelp(); return; }
    alert(tr("set.appearance.notifyBlockedFallback"));
  }

  // Pull the server-side preferences once on boot and reconcile with the local
  // caches (which the inline <head> script applied synchronously for the theme).
  async function syncThemeFromServer() {
    let prefs = null;
    try {
      const r = await fetch(BASE_PATH + "/api/preferences", { headers: authHeaders() });
      if (r.ok) {
        prefs = await r.json();
        // An explicit server choice (any string, including "" = VS Code Dark)
        // wins. Its absence (first run) leaves whatever the inline <head> script
        // applied — the user's local choice, or the DEFAULT_THEME default skin.
        if (prefs && typeof prefs.theme === "string" && prefs.theme !== getActiveTheme()) {
          applyTheme(prefs.theme, { persist: false });
        }
        // Seed the notification cache from the server when a choice exists; an
        // absent value (first run) is left untouched so the opt-in can fire.
        if (prefs && typeof prefs.notifications === "boolean") {
          localStorage.setItem(NOTIFY_STORAGE_KEY, prefs.notifications ? "1" : "0");
        }
        // Reconcile the UI language: an explicit server choice that differs from
        // what this browser resolved adopts it and reloads once (guarded), so a
        // second device/browser converges to the saved locale. First run (no
        // server locale) leaves whatever i18n.js already resolved.
        if (prefs && typeof prefs.locale === "string" && window.I18N) {
          I18N.reconcileServerLocale(prefs.locale); // may reload — returns early below
        }
      }
    } catch (_) { /* ignore */ }
    finally { _resolvePrefs(prefs); }
  }

  const RESTART_FLAG = "agent_toolkit_needs_restart";
  // Sticky flag: a pending config change touches the embedder, which is built
  // once on Infrastructure and survives hot-reload — only a full server
  // restart applies it. Set at save time by detecting an embedder-identity
  // change in models.json; cleared only by an actual restart.
  const RESTART_REQUIRED_FLAG = "agent_toolkit_restart_required";
  const BANNER_DISMISS_FLAG = "agent_toolkit_restart_dismissed";
  const ACTIVE_AGENT_KEY = "agent_toolkit_active_agent";
  const TOOL_GROUPS = ["Bash", "Read", "Write", "Edit", "Grep", "Glob", "revert", "mime", "mcp", "Skill", "softskills", "calc", "ddg", "serpapi", "web", "registries", "code_search", "docs"];
  const TOOL_DESCRIPTIONS = {
    Bash:       tr("tool.desc.Bash"),
    Read:       tr("tool.desc.Read"),
    Write:      tr("tool.desc.Write"),
    Edit:       tr("tool.desc.Edit"),
    Grep:       tr("tool.desc.Grep"),
    Glob:       tr("tool.desc.Glob"),
    revert:     tr("tool.desc.revert"),
    mime:       tr("tool.desc.mime"),
    mcp:        tr("tool.desc.mcp"),
    Skill:      tr("tool.desc.Skill"),
    softskills: tr("tool.desc.softskills"),
    calc:       tr("tool.desc.calc"),
    ddg:        tr("tool.desc.ddg"),
    serpapi:    tr("tool.desc.serpapi"),
    web:        tr("tool.desc.web"),
    registries: tr("tool.desc.registries"),
    code_search: tr("tool.desc.code_search"),
    docs: tr("tool.desc.docs"),
  };
  // Tools that are mutually exclusive: selecting one auto-deselects the other.
  const TOOL_MUTEX = { ddg: "serpapi", serpapi: "ddg" };

  const AGENT_SUBTABS = [
    { id: "agents",  label: tr("subtab.agents")  },
    { id: "squads",  label: tr("subtab.squads")  },
    { id: "remotes", label: tr("subtab.remotes") },
    { id: "globals", label: tr("subtab.globalEnv") },
  ];

  const MODELS_SUBTABS = [
    { id: "providers", label: tr("subtab.providers") },
    { id: "models",    label: tr("subtab.models")    },
  ];

  const SKILLS_SUBTABS = [
    { id: "installed", label: tr("subtab.installed") },
    { id: "remotes",   label: tr("subtab.remotes")   },
  ];

  const MCP_SUBTABS = [
    { id: "servers", label: tr("subtab.servers") },
    { id: "remotes", label: tr("subtab.remotes") },
  ];

  const A2A_SUBTABS = [
    { id: "agents",  label: tr("subtab.agents") },
    { id: "remotes", label: tr("subtab.remotes") },
  ];

  const COMMANDS_SUBTABS = [
    { id: "user",    label: tr("subtab.userCommands") },
    { id: "remotes", label: tr("subtab.remotes")       },
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
    activeCommandsSubtab: "user",  // only used when activeFile === USER_COMMANDS_ID
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
    commandsRemotes: { browsing: null, viewing: null }, // Commands remotes panel state
    permissionsRemotes: { browsing: null, viewing: null }, // Permissions remotes panel state
    activeRemoteKind: "agents",       // Selected kind in the Agents→Remotes split panel
    activeRegistryKind: "skills",     // Selected kind in the consolidated Registries hub
    docs: { activePage: "getting-started", cache: {} }, // documentation viewer state
  };

  // ─── DOM refs ──────────────────────────────────────────────────────────
  let panelEl, tabsEl, viewToggleEl, bodyEl, footerEl, statusEl;
  let sidebarMenuEl, sidebarMenuListEl; // in-sidebar settings categories

  // Navigation-context for the consolidated Registries hub. When non-null, the
  // per-kind remote flows (skills / mcp / a2a / commands) re-render into the
  // hub's right panel instead of jumping back to their own settings form. It is
  // set by renderRegistriesHub and cleared at the top of each parent form
  // renderer (renderSkills / renderMCPForm / renderA2AForm / renderUserCommands)
  // so the standalone per-kind "Remotes" sub-tabs behave exactly as before.
  // (Agents / Squads use the separate refreshRemotesRightFn indirection.)
  let registriesHubRefresh = null;

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
  // Two mutually-exclusive modes, decided at save time:
  //   • "reload"  — the change is hot-reloadable; offer Reload only (no
  //                 downtime, no SSE interruption — in-flight sessions stay
  //                 on their current generation, new ones get the new config).
  //   • "restart" — the change touches the embedder, which hot-reload cannot
  //                 swap (it is process-wide on Infrastructure); offer Restart
  //                 only, since Reload would silently leave it unapplied.
  // The restart option is therefore proposed *only* when an embedder change
  // is pending — every other edit shows Reload.
  function bannerMode() {
    return localStorage.getItem(RESTART_REQUIRED_FLAG) === "1" ? "restart" : "reload";
  }

  function ensureBanner() {
    let b = document.getElementById("restart-banner");
    if (b) return b;
    b = document.createElement("div");
    b.id = "restart-banner";
    b.hidden = true;
    // Insert into #chat-area (the flex column) above #chat (the pane row), so
    // the banner is a full-width top bar — not a flex item inside the pane row
    // (where it became a phantom column that survived into the chat view).
    const area = document.getElementById("chat-area") || document.getElementById("chat");
    area.insertBefore(b, area.firstChild);
    return b;
  }

  // renderBannerContent (re)builds the banner's text + buttons for the given
  // mode and (re)binds their handlers. Called on every show so a save that
  // flips the mode updates the controls in place.
  function renderBannerContent(b, mode) {
    if (mode === "restart") {
      b.innerHTML = `
        <span class="restart-banner-text">
          ${escHtml(tr("set.banner.restartText"))}
        </span>
        <button type="button" id="restart-banner-btn" class="reload-primary">${escHtml(tr("set.banner.restartBtn"))}</button>
        <button type="button" id="restart-banner-dismiss" data-tip="${escHtml(tr("set.banner.dismiss"))}">×</button>
      `;
      b.querySelector("#restart-banner-btn").addEventListener("click", () => doRestart());
    } else {
      b.innerHTML = `
        <span class="restart-banner-text">
          ${escHtml(tr("set.banner.reloadText"))}
        </span>
        <button type="button" id="restart-banner-reload" class="reload-primary">${escHtml(tr("set.banner.reloadBtn"))}</button>
        <button type="button" id="restart-banner-dismiss" data-tip="${escHtml(tr("set.banner.dismiss"))}">×</button>
      `;
      b.querySelector("#restart-banner-reload").addEventListener("click", () => doReload());
    }
    b.querySelector("#restart-banner-dismiss").addEventListener("click", () => {
      // Persistent dismissal until the next successful save re-arms the banner.
      localStorage.setItem(BANNER_DISMISS_FLAG, "1");
      b.hidden = true;
    });
  }

  // showBanner arms the banner after a save. restartRequired sets the sticky
  // embedder flag (it is never cleared here — a later hot-reloadable save must
  // not downgrade a still-pending embedder restart back to Reload; only an
  // actual restart clears it).
  function showBanner(restartRequired) {
    localStorage.setItem(RESTART_FLAG, "1");
    if (restartRequired) localStorage.setItem(RESTART_REQUIRED_FLAG, "1");
    // Re-arm visibility: a fresh save invalidates any earlier dismissal.
    localStorage.removeItem(BANNER_DISMISS_FLAG);
    const b = ensureBanner();
    renderBannerContent(b, bannerMode());
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
    renderBannerContent(b, bannerMode());
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
    el.querySelector("#restart-overlay-msg").textContent = msg || tr("set.overlay.restarting");
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
          ? `<span class="reload-spinner" aria-hidden="true"></span>${escHtml(tr("set.reload.reloading"))}`
          : origReloadHtml;
      }
      if (restartBtn) restartBtn.disabled = !!on;
    };

    if (hasAnyUnsaved()) {
      if (!await appConfirm(tr("set.reload.unsavedConfirm"))) return;
      await saveActive();
      if (hasUnsavedActive()) return;
    }

    setLoading(true);
    setStatus(tr("set.status.reloadingAgent"));
    try {
      const r = await fetch(BASE_PATH + "/api/config/reload", { method: "POST", headers: authHeaders() });
      if (!r.ok) {
        const j = await r.json().catch(() => ({}));
        throw new Error(j.error || `HTTP ${r.status}`);
      }
      const body = await r.json().catch(() => ({}));
      localStorage.removeItem(RESTART_FLAG);
      localStorage.removeItem(RESTART_REQUIRED_FLAG);
      localStorage.removeItem(BANNER_DISMISS_FLAG);

      const draining = body.draining_sessions || 0;
      const summary = draining > 0
        ? tr("set.reload.summaryDraining", { generation: body.generation, count: draining })
        : tr("set.reload.summary", { generation: body.generation });
      setStatus(summary);

      // Animate the banner out before hiding it. The is-fading-out class
      // stays on the element while hidden so the reverse transition can
      // never play; showBanner clears the class on re-show.
      if (banner) {
        if (textEl) textEl.textContent = tr("set.banner.reloaded");
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
      // Invalidate every parsed-config cache so the next render re-fetches
      // the on-disk state the runtime just reloaded. Without this, an
      // editor panel keeps its in-memory copy and the user may see the
      // pre-save value of fields that weren't directly touched (e.g. the
      // providers URL after a Save+Reload). Unsaved changes were already
      // either persisted or discarded by the hasAnyUnsaved check above.
      for (const k of Object.keys(state.parsed)) delete state.parsed[k];
      for (const k of Object.keys(state.raw)) delete state.raw[k];
      if (state.activeFile) renderBody();
      // Notify the main app shell so it can refresh anything cached from the
      // server side (squad picker, etc.) without a full page reload.
      window.dispatchEvent(new CustomEvent("omnis:config-reloaded", { detail: { generation: body.generation } }));
    } catch (e) {
      setLoading(false);
      if (textEl) {
        textEl.textContent = tr("set.reload.failedBanner", { error: e.message });
      }
      setStatus(tr("set.reload.failed", { error: e.message }));
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
        pill.textContent = tr("set.pill.gen", { generation: body.generation, count: draining });
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
    if (!await appConfirm(tr("set.restart.confirm"))) return;
    setStatus(tr("set.restart.restarting"));
    showRestartingOverlay(tr("set.overlay.restartingFull"));
    try {
      const r = await fetch(BASE_PATH + "/api/server/restart", { method: "POST", headers: authHeaders() });
      if (!r.ok) {
        const j = await r.json().catch(() => ({}));
        throw new Error(j.error || `HTTP ${r.status}`);
      }
      localStorage.removeItem(RESTART_FLAG);
      localStorage.removeItem(RESTART_REQUIRED_FLAG);
      localStorage.removeItem(BANNER_DISMISS_FLAG);
      const b = document.getElementById("restart-banner");
      if (b) b.hidden = true;
      setStatus(tr("set.restart.pageReload"));
      showRestartingOverlay(tr("set.overlay.restartingFull"));
      // Poll /api/health until reachable, then reload.
      const start = Date.now();
      const tick = async () => {
        try {
          const h = await fetch(BASE_PATH + "/api/health");
          if (h.ok) { window.location.reload(); return; }
        } catch (_) { /* not yet up */ }
        if (Date.now() - start > 30000) {
          hideRestartingOverlay();
          setStatus(tr("set.restart.notBack"));
          return;
        }
        setTimeout(tick, 750);
      };
      setTimeout(tick, 1000);
    } catch (e) {
      hideRestartingOverlay();
      setStatus(tr("set.restart.failed", { error: e.message }));
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
          <span class="settings-breadcrumb-root">${escHtml(tr("settings.label"))}</span>
          <svg class="settings-breadcrumb-sep" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="9 18 15 12 9 6"/></svg>
          <span class="settings-breadcrumb-current"></span>
        </nav>
        <div class="settings-tabs" role="tablist"></div>
      </header>
      <div class="settings-body">
        <div class="settings-body-toolbar">
          <div class="settings-content-inner">
            <div class="settings-view-toggle" role="tablist">
              <button type="button" data-view="form" class="active">${escHtml(tr("settings.viewForm"))}</button>
              <button type="button" data-view="raw">${escHtml(tr("settings.viewRaw"))}</button>
            </div>
          </div>
        </div>
        <div class="settings-body-content"></div>
      </div>
      <footer class="settings-footer">
        <span class="settings-status"></span>
        <button type="button" class="btn-discard">${escHtml(tr("settings.discard"))}</button>
        <button type="button" class="btn-save">${escHtml(tr("common.save"))}</button>
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
      b.textContent = tr("settings.menu." + f.id);
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
      li.innerHTML = `${ICONS[m.id] || ""}<span>${escHtml(tr("settings.menu." + m.id))}</span>`;
      li.addEventListener("click", () => setActiveFile(m.id));
      sidebarMenuListEl.appendChild(li);
    }
    // Raw JSON is appended last so new section entries inserted into
    // MENU_ITEMS always render above it.
    const raw = document.createElement("li");
    raw.dataset.file = RAW_VIEW_ID;
    raw.className = "settings-menu-raw";
    raw.innerHTML = `${ICONS.raw}<span>${escHtml(tr("settings.viewRaw"))}</span>`;
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
    const base = item ? tr("settings.title." + item.id) : "";
    el.textContent = (state.activeView === "raw" && !isClientOnly(id))
      ? `${base} › ${tr("settings.viewRaw")}`
      : base;
  }

  async function setActiveFile(id) {
    if (state.activeFile !== id && hasUnsavedActive() &&
        !await appConfirm(tr("set.confirm.discardTab"))) {
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
    if (id === USER_COMMANDS_ID) {
      state.commandsRemotes.browsing = null;
      state.commandsRemotes.viewing = null;
    }
    // Entering the consolidated Registries hub always starts at the per-kind
    // list — clear every kind's browse/detail navigation state.
    if (id === REGISTRIES_ID) {
      state.skills.browsingRemote = null;
      state.skills.viewingRemote = null;
      state.agentRemotes = { browsing: null, viewing: null };
      state.squadRemotes = { browsing: null };
      state.mcpRemotes = { browsing: null, viewing: null };
      state.a2aRemotes = { browsing: null };
      state.commandsRemotes = { browsing: null, viewing: null };
    }
    syncActiveHighlight(id);
    renderBody();
  }

  async function setActiveView(v) {
    if (state.activeView === v) return;
    if (hasUnsavedActive() && !await appConfirm(tr("set.confirm.discardView"))) return;
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
    return id === APPEARANCE_ID || id === "skills" || id === DOCUMENTATION_ID || id === USER_COMMANDS_ID || id === REGISTRIES_ID || id === AUTOMATION_ID;
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
    if (id === "permissions") return { permissions: { defaultMode: "default", allow: [], ask: [], deny: [] } };
    if (id === "mcp") return { servers: {}, inputs: [] };
    if (id === "a2a") return { agents: {}, inputs: [] };
    if (id === "hooks") return { hooks: {} };
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
    bodyEl.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
    setStatus("");
    applyClientOnlyChrome();
    const id = state.activeFile;
    if (isClientOnly(id)) {
      if (id === APPEARANCE_ID) renderAppearance();
      else if (id === "skills") renderSkills();
      else if (id === DOCUMENTATION_ID) renderDocumentation();
      else if (id === USER_COMMANDS_ID) renderUserCommands();
      else if (id === REGISTRIES_ID) renderRegistriesHub();
      else if (id === AUTOMATION_ID) renderAutomation();
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

  // ─── Registries hub ────────────────────────────────────────────────────
  //
  // A consolidated, top-level view of every remote registry, grouped by kind
  // in a left nav (Skills / Agents / Squads / MCP / A2A / Commands). It reuses
  // the same per-kind list / browse / install renderers that back the
  // standalone "Remotes" sub-tabs — it does not duplicate them. Two nav-context
  // indirections let those renderers re-render into this hub's right panel:
  //   • registriesHubRefresh — used by the form-based kinds (skills/mcp/a2a/
  //     commands), whose browse/back buttons would otherwise re-render their
  //     own settings form.
  //   • refreshRemotesRightFn — already used by the agents/squads dispatch.
  // Both are reset by the per-kind form renderers, so the standalone tabs are
  // unchanged. A single "Reindex" button rebuilds the semantic registry index
  // (skills + agents metadata) via POST /api/registries/reindex.
  const REGISTRY_KINDS = [
    { id: "skills",   label: tr("settings.title.skills")   },
    { id: "agents",   label: tr("settings.menu.agent")   },
    { id: "squads",   label: tr("subtab.squads")   },
    { id: "mcp",      label: tr("settings.menu.mcp")      },
    { id: "a2a",      label: tr("settings.menu.a2a")      },
    { id: "commands", label: tr("settings.menu.user-commands") },
    { id: "permissions", label: tr("settings.menu.permissions") },
  ];

  async function renderRegistriesHub(host = bodyEl) {
    if (!state.activeRegistryKind) state.activeRegistryKind = "skills";
    if (!state.agentRemotes) state.agentRemotes = { browsing: null, viewing: null };
    if (!state.squadRemotes) state.squadRemotes = { browsing: null };

    host.innerHTML = `
      <div class="agent-split-layout">
        <div class="agent-fleet-panel">
          <div class="agent-fleet-header">
            <span class="agent-fleet-title">REGISTRIES</span>
            <button type="button" class="add-btn" id="registries-reindex-btn"
              data-tip="${escHtml(tr("set.reg.reindexTip"))}">${escHtml(tr("set.reg.reindexBtn"))}</button>
          </div>
          <div class="agent-fleet-list" id="registries-kind-list"></div>
        </div>
        <div class="agent-detail-panel" id="registries-right-panel"></div>
      </div>
    `;

    const kindList = host.querySelector("#registries-kind-list");
    const rightEl  = host.querySelector("#registries-right-panel");

    function resetKindNav() {
      state.skills.browsingRemote = null;
      state.skills.viewingRemote = null;
      state.agentRemotes = { browsing: null, viewing: null };
      state.squadRemotes = { browsing: null };
      state.mcpRemotes = { browsing: null, viewing: null };
      state.a2aRemotes = { browsing: null };
      state.commandsRemotes = { browsing: null, viewing: null };
      state.permissionsRemotes = { browsing: null, viewing: null };
    }

    function renderKindNav() {
      kindList.innerHTML = "";
      for (const k of REGISTRY_KINDS) {
        const item = document.createElement("div");
        item.className = "agent-fleet-item" + (state.activeRegistryKind === k.id ? " active" : "");
        item.innerHTML = `<div class="agent-fleet-item-name">${escHtml(k.label)}</div>`;
        item.addEventListener("click", () => {
          if (state.activeRegistryKind === k.id) return;
          state.activeRegistryKind = k.id;
          resetKindNav();
          renderKindNav();
          refreshRegistriesRight();
        });
        kindList.appendChild(item);
      }
    }

    async function refreshRegistriesRight() {
      // Keep the nav-context pointers live so the reused per-kind renderers
      // re-render into this panel for every interaction.
      registriesHubRefresh = refreshRegistriesRight;
      refreshRemotesRightFn = refreshRegistriesRight;
      rightEl.innerHTML = "";
      switch (state.activeRegistryKind) {
        case "skills":
          if (state.skills.viewingRemote)  { await renderRemoteSkillDetailView(rightEl); return; }
          if (state.skills.browsingRemote) { await renderRemoteBrowseView(rightEl); return; }
          await renderRemoteRegistriesSection(rightEl);
          return;
        case "agents":
          if (state.agentRemotes.viewing)  { await renderAgentRemoteDetailView(rightEl); return; }
          if (state.agentRemotes.browsing) { await renderAgentRemoteBrowseView(rightEl); return; }
          await renderAgentRegistryList(rightEl, refreshRegistriesRight);
          return;
        case "squads":
          if (state.squadRemotes.browsing) { await renderSquadRemoteBrowseView(rightEl); return; }
          await renderSquadRegistryList(rightEl, refreshRegistriesRight);
          return;
        case "mcp":      await renderMCPRemotesSection(rightEl); return;
        case "a2a":      await renderA2ARemotesSection(rightEl); return;
        case "commands": await renderCommandsRemotesSection(rightEl); return;
        case "permissions": await renderPermissionsRemotesSection(rightEl); return;
      }
    }

    host.querySelector("#registries-reindex-btn").addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      const original = btn.textContent;
      btn.disabled = true;
      btn.textContent = tr("set.reg.reindexBtnBusy");
      setStatus(tr("set.status.reindexing"));
      try {
        const res = await skillsPost("/registries/reindex", {});
        const n = res && typeof res.indexed === "number" ? res.indexed : 0;
        setStatus(`Reindexed ${n} registry item${n === 1 ? "" : "s"}.`, "success");
      } catch (err) {
        setStatus(tr("set.status.reindexFailed", { error: err.message }), "error");
      } finally {
        btn.disabled = false;
        btn.textContent = original;
      }
    });

    registriesHubRefresh = refreshRegistriesRight;
    refreshRemotesRightFn = refreshRegistriesRight;
    renderKindNav();
    await refreshRegistriesRight();
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
          <h3>${escHtml(tr("theme.tier." + tier.id))}</h3>
          ${Object.entries(byTone).map(([tone, list]) => `
            <div class="theme-group-label">${escHtml(tr("theme.tone." + tone))}</div>
            <div class="theme-grid">${list.map(cardHTML).join("")}</div>
          `).join("")}
        </section>
      `;
    }).join("");

    // Language picker — reuses the theme-card grid (no swatch, just a label).
    const activeLocale = (window.I18N && I18N.locale) || "en";
    const langCards = (window.I18N ? I18N.LOCALES : [])
      .map(l => `
        <button type="button" class="theme-card lang-card ${activeLocale === l.id ? "active" : ""}" data-locale-id="${escHtml(l.id)}">
          <span class="theme-card-label">${escHtml(l.label)}</span>
          <span class="theme-card-check" aria-hidden="true">✓</span>
        </button>
      `).join("");
    const languageSection = `
      <section class="form-section">
        <h3>${escHtml(tr("appearance.language"))}</h3>
        <p class="settings-hint" style="margin:0 0 0.5rem;">${escHtml(tr("appearance.languageHint"))}</p>
        <div class="theme-grid">${langCards}</div>
      </section>
    `;

    const osNotify = localStorage.getItem(NOTIFY_STORAGE_KEY) === "1";
    bodyEl.innerHTML = `
      <div class="settings-form">
        ${languageSection}
        <section class="form-section">
          <h3>${escHtml(tr("appearance.notifications"))}</h3>
          <label class="settings-checkrow">
            <input type="checkbox" id="os-notify-toggle" ${osNotify ? "checked" : ""} />
            <span>${escHtml(tr("appearance.notifyLabel"))}</span>
          </label>
          <p class="settings-hint" style="margin:0;">
            ${escHtml(tr("appearance.notifyHint"))}
          </p>
        </section>
        <p class="settings-hint" style="margin:0;">
          ${escHtml(tr("appearance.themeHint"))}
        </p>
        ${sections}
      </div>
    `;

    // Language cards — persist + reload via the i18n runtime.
    bodyEl.querySelectorAll(".lang-card").forEach(btn => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.localeId;
        if (window.I18N && id) I18N.setLocale(id);
      });
    });

    bodyEl.querySelectorAll(".theme-card").forEach(btn => {
      btn.addEventListener("click", () => {
        const id = btn.dataset.themeId;
        applyTheme(id);
        bodyEl.querySelectorAll(".theme-card").forEach(b => {
          b.classList.toggle("active", b.dataset.themeId === id);
        });
      });
    });

    const osToggle = bodyEl.querySelector("#os-notify-toggle");
    if (osToggle) {
      osToggle.addEventListener("change", async () => {
        if (!osToggle.checked) {
          saveNotifications(false);
          return;
        }
        if (!("Notification" in window)) {
          osToggle.checked = false;
          saveNotifications(false);
          alert(tr("set.appearance.notifyUnsupported"));
          return;
        }
        const before = Notification.permission;
        let after = before;
        if (before === "default") { try { after = await Notification.requestPermission(); } catch (_) {} }
        if (after === "granted") { saveNotifications(true); return; }
        if (before === "default" && after === "denied") {
          // User actively chose "Block" in the native prompt — respect it.
          osToggle.checked = false;
          saveNotifications(false);
          return;
        }
        // Opted in but the browser is still blocking. Keep the toggle on (the
        // browser permission is a separate gate checked when a notification
        // fires, so it works once unblocked) and explain how to allow it.
        saveNotifications(true);
        notifyBlockedHelp();
      });
    }
  }

  // ─── User commands editor ──────────────────────────────────────────────
  // Lists the built-in slash commands as read-only context, then a
  // CRUD view of the user-defined commands persisted via
  // /api/user-commands. Editing reuses the inline modal defined in
  // app.js (window.UserCommands.openModal).
  // ─── Automation page (loops & schedules) ──────────────────────────────
  // Full management surface for /loop and /schedule jobs: two grouped lists
  // (durable Schedules + active Loops), each row with run-now / inline-edit
  // (spec+prompt) / enable-disable / delete, an expandable run history (with
  // links to the session each run produced), and an add-routine form. Backed by
  // the /api/schedules routes.
  let _schedSpec = "", _schedPrompt = "";

  // fmtTime renders an ISO timestamp, treating Go's zero time as "never".
  function fmtSchedTime(t) {
    if (!t || String(t).startsWith("0001")) return tr("set.sched.never");
    return new Date(t).toLocaleString(I18N.locale);
  }
  function schedOneLine(s) {
    const t = (s || "").split("\n").map(x => x.trim()).find(Boolean) || "";
    return t.length > 90 ? t.slice(0, 90) + "…" : t;
  }
  function schedHistoryHTML(j) {
    const recs = (j.history || []).slice().reverse(); // newest first
    if (!recs.length) return `<p class="settings-hint">${escHtml(tr("set.sched.noRuns"))}</p>`;
    return recs.map(r => {
      const ok = r.status !== "error";
      const when = r.at ? new Date(r.at).toLocaleString(I18N.locale) : "";
      const link = r.session_id
        ? `<a class="sched-link" data-open="${escHtml(r.session_id)}">${escHtml(tr("set.sched.open"))}</a>`
        : "";
      const note = r.note ? `<span class="sched-run-note">${escHtml(r.note)}</span>` : "";
      return `<div class="sched-run"><span class="sched-run-dot ${ok ? "ok" : "err"}"></span>` +
        `<span class="sched-run-when">${escHtml(when)}</span>${link}${note}</div>`;
    }).join("");
  }
  function schedRowHTML(j) {
    const histN = (j.history || []).length;
    return `
      <div class="sched-row" data-id="${escHtml(j.id)}">
        <div class="sched-row-head">
          <span class="sched-kind sched-kind-${escHtml(j.kind)}">${escHtml(j.kind)}</span>
          <code class="sched-spec">${escHtml(j.spec)}</code>
          <span class="sched-prompt" title="${escHtml(j.prompt || "")}">${escHtml(schedOneLine(j.prompt))}</span>
          <span class="sched-state ${j.enabled ? "on" : "off"}">${escHtml(j.enabled ? tr("set.sched.active") : tr("set.sched.paused"))}</span>
        </div>
        <div class="sched-row-meta">
          <span>${escHtml(tr("set.sched.next"))}: ${escHtml(fmtSchedTime(j.next_run))}</span>
          <span>${escHtml(tr("set.sched.last"))}: ${escHtml(fmtSchedTime(j.last_run))}</span>
          <span>${escHtml(tr("set.sched.runs"))}: ${j.runs || 0}</span>
          ${histN ? `<button type="button" class="sched-link" data-act="history">${escHtml(tr("set.sched.viewRuns"))}</button>` : ""}
        </div>
        <div class="sched-history" hidden>${schedHistoryHTML(j)}</div>
        <div class="sched-edit" hidden>
          <label>${escHtml(tr("set.sched.spec"))}
            <input type="text" class="sched-edit-spec" value="${escHtml(j.spec)}" placeholder="${escHtml(tr("set.sched.specHint"))}">
          </label>
          <label>${escHtml(tr("set.sched.prompt"))}
            <textarea class="sched-edit-prompt" rows="2">${escHtml(j.prompt || "")}</textarea>
          </label>
          <div class="sched-edit-actions">
            <button type="button" class="btn-small" data-act="save">${escHtml(tr("common.save"))}</button>
            <button type="button" class="btn-small" data-act="cancel">${escHtml(tr("common.cancel"))}</button>
          </div>
        </div>
        <div class="sched-actions">
          <button type="button" class="btn-small" data-act="run">${escHtml(tr("set.sched.run"))}</button>
          <button type="button" class="btn-small" data-act="edit">${escHtml(tr("set.sched.edit"))}</button>
          <button type="button" class="btn-small" data-act="toggle">${j.enabled ? escHtml(tr("set.sched.disable")) : escHtml(tr("set.sched.enable"))}</button>
          <button type="button" class="btn-small btn-danger" data-act="delete">${escHtml(tr("set.sched.delete"))}</button>
        </div>
      </div>`;
  }

  async function renderAutomation() {
    registriesHubRefresh = null;
    bodyEl.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
    let jobs = [];
    try {
      const r = await fetch(BASE_PATH + "/api/schedules", { headers: authHeaders() });
      const j = await r.json();
      jobs = Array.isArray(j.jobs) ? j.jobs : [];
    } catch (e) {
      bodyEl.innerHTML = `<p class="settings-error">${escHtml(String(e))}</p>`;
      return;
    }
    const schedules = jobs.filter(j => j.kind === "schedule");
    const loops = jobs.filter(j => j.kind === "loop");

    bodyEl.innerHTML = `
      <div class="settings-form sched-panel">
        <h2>${escHtml(tr("set.sched.heading"))}</h2>
        <p class="settings-hint">${escHtml(tr("set.sched.intro"))}</p>

        <div class="sched-section">
          <h3>${escHtml(tr("set.sched.schedulesH"))} <span class="sched-count">${schedules.length}</span></h3>
          <div class="sched-list">${schedules.map(schedRowHTML).join("") || `<p class="settings-hint">${escHtml(tr("set.sched.emptySchedules"))}</p>`}</div>
          <div class="sched-add">
            <h4>${escHtml(tr("set.sched.addTitle"))}</h4>
            <label>${escHtml(tr("set.sched.spec"))}
              <input type="text" id="sched-spec" placeholder="${escHtml(tr("set.sched.specHint"))}" value="${escHtml(_schedSpec)}">
            </label>
            <label>${escHtml(tr("set.sched.prompt"))}
              <textarea id="sched-prompt" rows="2">${escHtml(_schedPrompt)}</textarea>
            </label>
            <button type="button" class="add-btn" id="sched-add-btn">${escHtml(tr("set.sched.add"))}</button>
          </div>
        </div>

        <div class="sched-section">
          <h3>${escHtml(tr("set.sched.loopsH"))} <span class="sched-count">${loops.length}</span></h3>
          <div class="sched-list">${loops.map(schedRowHTML).join("") || `<p class="settings-hint">${escHtml(tr("set.sched.emptyLoops"))}</p>`}</div>
        </div>
      </div>`;

    const api = (path, opts) => fetch(
      BASE_PATH + "/api/schedules" + path,
      Object.assign({ headers: authHeaders({ "Content-Type": "application/json" }) }, opts)
    );

    // One delegated handler for every row action + run-history "open" link.
    bodyEl.querySelector(".sched-panel").addEventListener("click", async (e) => {
      const openEl = e.target.closest("[data-open]");
      if (openEl) {
        if (typeof selectSession === "function") selectSession(openEl.dataset.open);
        return;
      }
      const btn = e.target.closest("[data-act]");
      if (!btn) return;
      const row = btn.closest(".sched-row");
      if (!row) return;
      const id = row.dataset.id;
      const job = jobs.find(j => j.id === id);
      const act = btn.dataset.act;
      try {
        if (act === "history") { const h = row.querySelector(".sched-history"); h.hidden = !h.hidden; return; }
        if (act === "edit") { const ed = row.querySelector(".sched-edit"); ed.hidden = !ed.hidden; return; }
        if (act === "cancel") { row.querySelector(".sched-edit").hidden = true; return; }
        if (act === "run") { await api(`/${encodeURIComponent(id)}/run`, { method: "POST" }); }
        else if (act === "toggle") { await api(`/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify({ enabled: !(job && job.enabled) }) }); }
        else if (act === "delete") { await api(`/${encodeURIComponent(id)}`, { method: "DELETE" }); }
        else if (act === "save") {
          const spec = row.querySelector(".sched-edit-spec").value.trim();
          const prompt = row.querySelector(".sched-edit-prompt").value.trim();
          const r = await api(`/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify({ spec, prompt }) });
          if (!r.ok) { const jj = await r.json().catch(() => ({})); setStatus(jj.error || ("error " + r.status), "error"); return; }
        } else return;
        renderAutomation();
      } catch (err) { setStatus(String(err), "error"); }
    });

    // Add-routine form.
    const specEl = bodyEl.querySelector("#sched-spec");
    const promptEl = bodyEl.querySelector("#sched-prompt");
    if (specEl) specEl.addEventListener("input", () => { _schedSpec = specEl.value; });
    if (promptEl) promptEl.addEventListener("input", () => { _schedPrompt = promptEl.value; });
    const addBtn = bodyEl.querySelector("#sched-add-btn");
    if (addBtn) addBtn.addEventListener("click", async () => {
      const spec = specEl.value.trim(), prompt = promptEl.value.trim();
      if (!spec || !prompt) { setStatus(tr("set.sched.needBoth"), "error"); return; }
      try {
        const r = await api("", { method: "POST", body: JSON.stringify({ kind: "schedule", spec, prompt }) });
        if (!r.ok) { const j = await r.json().catch(() => ({})); setStatus(j.error || ("error " + r.status), "error"); return; }
        _schedSpec = ""; _schedPrompt = "";
        renderAutomation();
      } catch (e) { setStatus(String(e), "error"); }
    });
  }

  // refreshSchedules re-renders the Automation panel when it is the active
  // section (driven by the schedule_changed SSE event from app.js).
  function refreshSchedules() {
    if (isOpen() && state.activeFile === AUTOMATION_ID) renderAutomation();
  }

  async function renderUserCommands() {
    registriesHubRefresh = null;
    const UC = window.UserCommands;
    if (!UC) {
      bodyEl.innerHTML = `<p class="settings-error">${escHtml(tr("set.cmd.apiUnavailable"))}</p>`;
      return;
    }
    // Render the sub-tab shell once; each tab body paints into the inner host.
    const sub = state.activeCommandsSubtab;
    bodyEl.innerHTML = `
      <div class="settings-form">
        <div class="settings-subtabs" role="tablist">
          ${COMMANDS_SUBTABS.map(t => `
            <button type="button" data-subtab="${t.id}" class="${sub === t.id ? "active" : ""}">${escHtml(t.label)}</button>
          `).join("")}
        </div>
        <div class="settings-subtab-body"></div>
      </div>
    `;
    bodyEl.querySelectorAll(".settings-subtabs button").forEach(b => {
      b.addEventListener("click", () => {
        if (state.activeCommandsSubtab === b.dataset.subtab) {
          if (b.dataset.subtab === "remotes" && state.commandsRemotes.browsing) {
            state.commandsRemotes.browsing = null;
            state.commandsRemotes.viewing = null;
            renderUserCommands();
          }
          return;
        }
        state.activeCommandsSubtab = b.dataset.subtab;
        renderUserCommands();
      });
    });

    const host = bodyEl.querySelector(".settings-subtab-body");
    if (sub === "remotes") {
      await renderCommandsRemotesSection(host);
      return;
    }

    await UC.refresh();
    paintUserCommands(UC, host);
    // Repaint when the underlying list changes (modal save, delete, etc.),
    // but only while this section is still the active view. The listener
    // accumulates across navigations; the guard makes that harmless.
    if (!state._userCmdListenerWired) {
      state._userCmdListenerWired = true;
      UC.onChanged(() => {
        if (state.activeFile === USER_COMMANDS_ID && state.open &&
            state.activeCommandsSubtab === "user") {
          const h = bodyEl.querySelector(".settings-subtab-body");
          if (h) paintUserCommands(UC, h);
        }
      });
    }
  }

  function paintUserCommands(UC, host) {
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
      ? `<tr><td colspan="4" class="cmd-empty">${escHtml(tr("set.cmd.empty"))}</td></tr>`
      : commands.map(c => `
        <tr data-name="${escHtml(c.name)}">
          <td class="cmd-name">/${escHtml(c.name)}</td>
          <td class="cmd-args">${escHtml(c.args || "")}</td>
          <td class="cmd-desc">${escHtml(c.description || "")}</td>
          <td class="cmd-actions">
            <button type="button" class="btn-edit" data-name="${escHtml(c.name)}">${escHtml(tr("common.edit"))}</button>
            <button type="button" class="btn-del" data-name="${escHtml(c.name)}">${escHtml(tr("common.delete"))}</button>
          </td>
        </tr>
      `).join("");

    host.innerHTML = `
      <div class="user-cmd-settings">
        <p class="settings-hint" style="margin:0;">
          ${tr("set.cmd.hint")}
        </p>

        <section class="form-section">
          <h3>${escHtml(tr("set.cmd.builtinHeader"))}</h3>
          <table class="cmd-table">
            <thead><tr><th>${escHtml(tr("set.cmd.colCommand"))}</th><th>${escHtml(tr("set.cmd.colArgs"))}</th><th>${escHtml(tr("set.cmd.colDescription"))}</th></tr></thead>
            <tbody>${builtinRows}</tbody>
          </table>
        </section>

        <section class="form-section">
          <div class="cmd-section-header">
            <h3 style="margin:0;">${escHtml(tr("set.cmd.userHeader"))}</h3>
            <button type="button" id="user-cmd-add-btn" class="primary">${escHtml(tr("set.cmd.addBtn"))}</button>
          </div>
          <table class="cmd-table">
            <thead><tr><th>${escHtml(tr("set.cmd.colCommand"))}</th><th>${escHtml(tr("set.cmd.colArgs"))}</th><th>${escHtml(tr("set.cmd.colDescription"))}</th><th></th></tr></thead>
            <tbody>${userRows}</tbody>
          </table>
        </section>
      </div>
    `;

    host.querySelector("#user-cmd-add-btn")?.addEventListener("click", () => {
      UC.openModal(null);
    });
    host.querySelectorAll(".btn-edit").forEach(btn => {
      btn.addEventListener("click", () => {
        const name = btn.dataset.name;
        const cmd = UC.list().find(c => c.name === name);
        if (cmd) UC.openModal(cmd);
      });
    });
    host.querySelectorAll(".btn-del").forEach(btn => {
      btn.addEventListener("click", async () => {
        const name = btn.dataset.name;
        if (!await appConfirm(tr("set.cmd.removeConfirm", { name }))) return;
        try { await UC.remove(name); }
        catch (e) { await appConfirm(tr("set.cmd.deleteFailed", { error: e.message })); }
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
        <aside class="docs-toc" aria-label="${escHtml(tr("set.docs.tocAria"))}">
          ${tocHTML}
        </aside>
        <article class="docs-article" tabindex="-1">
          <div class="docs-article-body">
            <p class="settings-loading">${escHtml(tr("set.loading"))}</p>
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
        host.innerHTML = `<p class="settings-error">${escHtml(tr("set.docs.loadFailed", { error: e.message }))}</p>`;
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
    if (id === "hooks") return renderHooksForm();
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
              <span class="agent-fleet-title">${escHtml(tr("set.fleet.squadsTitle"))}</span>
              <button type="button" class="agent-fleet-add" id="add-squad" data-tip="${escHtml(tr("set.fleet.addSquad"))}">+</button>
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
              <span class="agent-fleet-title">${escHtml(tr("set.fleet.activeFleet"))}</span>
              <button type="button" class="agent-fleet-import" id="import-agent" data-tip="${escHtml(tr("set.fleet.importAgent"))}">&#8595;</button>
              <button type="button" class="agent-fleet-add" id="add-agent" data-tip="${escHtml(tr("set.fleet.addAgent"))}">+</button>
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
            setStatus(tr("set.agent.imported", { names }), "success");
          }
        } catch (e) {
          setStatus(tr("set.status.importFailed", { error: e.message }), "error");
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
        <div class="agent-fleet-item-name">${escHtml(sq.name || tr("app.askuser.unnamed"))} ${isDefault ? '<span class="squad-default-tag">default</span>' : ""}</div>
        <div class="agent-fleet-item-meta">${escHtml(trN("set.squad.memberCount", memberCount))}</div>
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
      panel.innerHTML = `<div class="agent-detail-empty">${escHtml(tr("set.squad.empty"))}</div>`;
      return;
    }
    const sq = d.squads[idx];
    if (!sq) {
      panel.innerHTML = `<div class="agent-detail-empty">${escHtml(tr("set.squad.selectPrompt"))}</div>`;
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
    // A leaderless squad (leader "" or "none") runs a single member agent
    // directly, with no coordinator. The default squad always needs a leader.
    const leaderless = !isDefault && (!sq.leader || (sq.leader || "").toLowerCase() === "none");

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
          <label class="agent-detail-label">${escHtml(tr("common.name"))}</label>
          <input type="text" class="agent-detail-input" id="squad-name" value="${escHtml(sq.name || "")}" ${isDefault ? "disabled" : ""} />
          ${isDefault ? `<div class="agent-detail-hint">${escHtml(tr("set.squad.defaultNameHint"))}</div>` : ""}
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">${escHtml(tr("common.description"))}</label>
          <input type="text" class="agent-detail-input" id="squad-desc" value="${escHtml(sq.description || "")}" placeholder="${escHtml(tr("set.squad.descPlaceholder"))}" />
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">${escHtml(tr("set.squad.leader"))}</label>
          <select class="agent-detail-input" id="squad-leader">
            ${isDefault ? "" : `<option value="none" ${leaderless ? "selected" : ""}>${escHtml(tr("set.squad.leaderNone"))}</option>`}
            ${leaderCandidates.map(n => `<option value="${escHtml(n)}" ${!leaderless && n === sq.leader ? "selected" : ""}>${escHtml(n)}</option>`).join("")}
          </select>
          ${leaderless ? `<div class="agent-detail-hint">${escHtml(tr("set.squad.leaderlessHint"))}</div>` : ""}
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">${escHtml(tr("set.squad.members"))}</label>
          <div class="agent-tools-grid" id="squad-members">
            ${sortedMembers.map(a => {
              const isOn = members.includes(a.name);
              const isLeaderRow = a.name === sq.leader;
              const desc = a.description || "";
              return `
              <div class="agent-tool-card${isOn ? " tool-on" : ""}${isLeaderRow ? " tool-disabled" : ""}" data-name="${escHtml(a.name)}" data-tip="${escHtml(desc)}">
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
          <div class="agent-detail-hint">${escHtml(leaderless ? tr("set.squad.membersHintLeaderless") : tr("set.squad.membersHint"))}</div>
        </div>
        ${!isDefault ? `<div class="squad-detail-actions"><button type="button" class="agent-detail-remove" id="squad-remove">${escHtml(tr("set.squad.deleteBtn"))}</button></div>` : ""}
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
      if ((sq.leader || "").toLowerCase() === "none") {
        // Leaderless: keep at most one member so the single agent runs directly.
        if (Array.isArray(sq.members) && sq.members.length > 1) {
          sq.members = [sq.members[0]];
        }
      } else if (Array.isArray(sq.members)) {
        // Drop the new leader from the members list (a squad cannot list its
        // own leader as a member).
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
        if (leaderless) {
          // Single-agent selection: clicking an agent makes it the sole member;
          // clicking the selected one clears it.
          sq.members = sq.members.includes(name) ? [] : [name];
        } else if (sq.members.includes(name)) {
          sq.members = sq.members.filter(m => m !== name);
        } else {
          sq.members.push(name);
        }
        // Reflect the new selection in place instead of re-rendering the whole
        // grid. A full renderAgentSquads(d) re-sorts members (selected first),
        // so the just-clicked chip would jump to the front of the list. Keeping
        // each card where it is until the next real render (page/server reload)
        // is far less jarring while toggling several members.
        const paint = (el, on) => {
          el.classList.toggle("tool-on", on);
          const pill = el.querySelector(".agent-tool-toggle-pill");
          if (pill) { pill.classList.toggle("pill-on", on); pill.classList.toggle("pill-off", !on); }
        };
        paint(card, sq.members.includes(name));
        if (leaderless) {
          // Single-select: clear the visual state of every other member card.
          panel.querySelectorAll("#squad-members .agent-tool-card").forEach(other => {
            if (other === card || other.classList.contains("tool-disabled")) return;
            paint(other, false);
          });
        }
        onChange();
      });
    });
    if (!isDefault) {
      panel.querySelector("#squad-remove").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.confirm.deleteSquad", { name: sq.name }))) return;
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
    el.appendChild(envSection(tr("set.env.coreDirectories"), tr("set.env.softskillsDesc"), body => {
      const g = document.createElement("div");
      g.className = "env-grid-2";
      g.appendChild(envText("softskills_dir"));
      body.appendChild(g);
    }));

    // OPTIMIZATION
    el.appendChild(envSection(tr("set.env.optimization"), null, body => {
      const isOn = !!d.token_optimization;
      const chip = document.createElement("div");
      chip.className = "agent-tool-card env-opt-chip" + (isOn ? " tool-on" : "");
      chip.innerHTML = `
        <div class="agent-tool-icon"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/></svg></div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">token_optimization</span>
          <span class="agent-tool-desc">${escHtml(tr("set.env.reduceTokens"))}</span>
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
    el.appendChild(envSection(tr("set.env.runtimeConfig"), null, body => {
      const g = document.createElement("div");
      g.className = "env-grid-2";
      g.appendChild(envText("bash_output_filters_dir"));
      g.appendChild(envNum("bash_timeout_seconds"));
      g.appendChild(envText("mcp_config_path"));
      g.appendChild(envText("permissions_config_path"));
      body.appendChild(g);
    }));

    // EXTERNAL API KEYS
    el.appendChild(envSection(tr("set.env.externalApiKeys"), null, body => {
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
      eye.setAttribute("data-tip", tr("set.env.showHide"));
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

    // The model "in use" dot is driven by whether any agent references the
    // model via model_ref, so make sure the agent config is loaded first.
    if (!state.parsed["agent"]) { try { await loadParsed("agent"); } catch (_) {} }

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
          <h2 class="model-panel-title">${escHtml(tr("set.model.configuredProviders"))}</h2>
          <p class="model-panel-desc">${escHtml(tr("set.model.providersDesc"))}</p>
        </div>
        <button type="button" class="add-btn model-add-btn" id="add-provider">${escHtml(tr("set.model.addProvider"))}</button>
      </div>
      <div id="providers-grid"></div>
    `;
    host.querySelector("#add-provider").addEventListener("click", async () => {
      let name = await appPrompt(tr("set.confirm.newProviderName"));
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
            <button type="button" class="model-test-link" data-tip="${escHtml(tr("set.model.testTip"))}">${escHtml(tr("set.model.test"))}</button>
            <button type="button" class="model-remove-link">${escHtml(tr("set.agent.removeBtn"))}</button>
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
      kindLbl.textContent = tr("set.model.kind");
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
          ? tr("set.model.removeProviderRefs", { name, refs: refs.join(", ") })
          : tr("set.model.removeProvider", { name });
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
          setStatus(tr("set.model.testingName", { name }));
          const r = await fetch(BASE_PATH + `/api/providers/models?${params}`, { headers: authHeaders() });
          const j = await r.json();
          if (!r.ok) throw new Error(j.error || r.statusText);
          setStatus(tr("set.model.reachable", { name, count: j.models?.length || 0 }), "success");
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
          <h2 class="model-panel-title">${escHtml(tr("set.model.configuredModels"))}</h2>
          <p class="model-panel-desc">${tr("set.model.modelsDesc")}</p>
        </div>
        <button type="button" class="add-btn model-add-btn" id="add-model">${escHtml(tr("set.model.addModel"))}</button>
      </div>
      <div id="general-select-row"></div>
      <div id="models-grid"></div>
    `;
    renderGeneralSelectors(d, host.querySelector("#general-select-row"));
    host.querySelector("#add-model").addEventListener("click", async () => {
      const providerNames = Object.keys(d.providers || {});
      if (!providerNames.length) {
        setStatus(tr("set.status.addProviderFirst"), "error");
        return;
      }
      const result = await appModelDialog(d);
      if (!result) return;
      d.models[result.name] = result.model;
      markFormDirty("models");
      renderModelCards(d, host.querySelector("#models-grid"));
      renderGeneralSelectors(d, host.querySelector("#general-select-row"));
    });
    renderModelCards(d, host.querySelector("#models-grid"));
  }

  // renderGeneralSelectors renders the "General" card grouping the two internal
  // model-role selectors — the /goal evaluator model (on top) and the internal
  // embedding model — into a single card above the model grid. Both persist into
  // models.json (`eval_model_ref` / `embed_model_ref`) and are re-rendered
  // together whenever a model's embedding flag or combo metadata changes.
  function renderGeneralSelectors(d, el) {
    if (!el) return;
    el.innerHTML = "";

    // Mirror the standard model-card structure (header bar + padded body) so
    // this selector sits flush with the model grid below it.
    const wrap = document.createElement("div");
    wrap.className = "embed-select-card model-card";

    const hdr = document.createElement("div");
    hdr.className = "model-card-hdr";
    const title = document.createElement("div");
    title.className = "model-card-title";
    const strong = document.createElement("strong");
    strong.textContent = tr("set.model.general");
    title.appendChild(strong);
    hdr.appendChild(title);

    const body = document.createElement("div");
    body.className = "model-card-body";
    // /goal evaluator first (on top), then the internal embedding model.
    body.appendChild(buildEvalField(d));
    body.appendChild(buildEmbedField(d));

    wrap.appendChild(hdr);
    wrap.appendChild(body);
    el.appendChild(wrap);
  }

  // buildEvalField returns the "/goal evaluator model" field — a dropdown of the
  // chat (non-embedding) models. The selection is persisted as models.json
  // `eval_model_ref` and is used by the /goal completion judge after each turn.
  // When unset, the judge falls back to the session's leader model; applies on
  // the next config reload (no restart needed).
  function buildEvalField(d) {
    const chatModels = Object.keys(d.models || {}).filter(n => d.models[n] && !d.models[n].embedding);
    const fld = document.createElement("div");
    fld.className = "model-field model-field-full";

    const lbl = document.createElement("label");
    lbl.className = "model-field-label";
    lbl.textContent = tr("set.model.evalModel");

    const sel = document.createElement("select");
    sel.className = "model-field-input";
    const none = document.createElement("option");
    none.value = "";
    none.textContent = tr("set.model.evalDefault");
    sel.appendChild(none);
    for (const n of chatModels) {
      const opt = document.createElement("option");
      opt.value = n; opt.textContent = n;
      if ((d.eval_model_ref || "").toLowerCase() === n.toLowerCase()) opt.selected = true;
      sel.appendChild(opt);
    }
    sel.disabled = !chatModels.length;
    sel.addEventListener("change", () => {
      if (sel.value) d.eval_model_ref = sel.value; else delete d.eval_model_ref;
      markFormDirty("models");
    });
    const desc = document.createElement("p");
    desc.className = "model-panel-desc";
    desc.textContent = tr("set.model.evalDesc");

    fld.appendChild(lbl); fld.appendChild(sel); fld.appendChild(desc);
    return fld;
  }

  // buildEmbedField returns the "internal embedding model" field — a dropdown of
  // the models flagged `embedding: true`. The selection is persisted as
  // models.json `embed_model_ref` and drives semantic recall (soft-skills,
  // precedents, codebase). When unset, recall is disabled and the agent uses its
  // glob/grep fallbacks.
  function buildEmbedField(d) {
    const embedModels = Object.keys(d.models || {}).filter(n => d.models[n] && d.models[n].embedding);
    const fld = document.createElement("div");
    fld.className = "model-field model-field-full";

    const lbl = document.createElement("label");
    lbl.className = "model-field-label";
    lbl.textContent = tr("set.model.internalEmbedding");

    const sel = document.createElement("select");
    sel.className = "model-field-input";
    const none = document.createElement("option");
    none.value = "";
    none.textContent = embedModels.length
      ? tr("set.model.embedDisabled")
      : tr("set.model.noEmbedModels");
    sel.appendChild(none);
    for (const n of embedModels) {
      const opt = document.createElement("option");
      opt.value = n; opt.textContent = n;
      if ((d.embed_model_ref || "").toLowerCase() === n.toLowerCase()) opt.selected = true;
      sel.appendChild(opt);
    }
    sel.disabled = !embedModels.length;
    sel.addEventListener("change", () => {
      if (sel.value) d.embed_model_ref = sel.value; else delete d.embed_model_ref;
      markFormDirty("models");
    });
    const desc = document.createElement("p");
    desc.className = "model-panel-desc";
    desc.textContent = tr("set.model.embedDesc");

    fld.appendChild(lbl); fld.appendChild(sel); fld.appendChild(desc);
    return fld;
  }

  function renderModelCards(d, el) {
    el.innerHTML = "";
    const grid = document.createElement("div");
    grid.className = "model-cards-grid";
    const providerNames = Object.keys(d.providers || {});

    // Models referenced by at least one agent via model_ref are "in use" and
    // get a live (green) dot; the rest are unused (grey). Keep the referencing
    // agent names so the hover label can name the consumer.
    const usedBy = new Map();
    for (const a of ((state.parsed["agent"]?.value?.agents) || [])) {
      if (!a || !a.model_ref) continue;
      const k = String(a.model_ref).toLowerCase();
      if (!usedBy.has(k)) usedBy.set(k, []);
      usedBy.get(k).push(a.name || tr("app.askuser.unnamed"));
    }

    Object.keys(d.models).forEach(name => {
      const m = d.models[name] || {};
      const onChange = () => markFormDirty("models");
      const agents = usedBy.get(name.toLowerCase()) || [];
      const inUse = agents.length > 0;
      const dotClass = inUse ? "dot-active" : "dot-standby";
      const MAX_TIP_AGENTS = 4;
      const dotTip = inUse
        ? tr("set.model.usedBy", { agents: `${agents.slice(0, MAX_TIP_AGENTS).join(", ")}${agents.length > MAX_TIP_AGENTS ? ", …" : ""}` })
        : tr("set.model.unused");
      const card = document.createElement("div");
      card.className = "model-card";
      card.innerHTML = `
        <div class="model-card-hdr">
          <div class="model-card-title">
            <span class="model-status-dot ${dotClass}" data-tip="${escHtml(dotTip)}"></span>
            <strong>${escHtml(name.toUpperCase())}</strong>
          </div>
          <button type="button" class="model-remove-link">${escHtml(tr("set.agent.removeBtn"))}</button>
        </div>
        <div class="model-card-body"></div>
      `;
      const body = card.querySelector(".model-card-body");

      // On model-combo selection we re-render the whole card grid (and the
      // General selectors) so prefilled metadata — context length, prices, dim,
      // embedding flag — becomes visible. Both the embedding and /goal evaluator
      // selectors depend on the embedding-flag set, so re-render the whole card.
      const refreshEmbed = () => {
        const parent = el.parentElement;
        const row = parent && parent.querySelector("#general-select-row");
        if (row) renderGeneralSelectors(d, row);
      };
      const rerender = () => { renderModelCards(d, el); refreshEmbed(); };
      const { streamWrap, cacheWrap, fg } = buildModelConfigFields(d, m, {
        onChange, rerender, refreshEmbedSelector: refreshEmbed, name,
      });
      const titleEl = card.querySelector(".model-card-title");
      titleEl.appendChild(streamWrap);
      titleEl.appendChild(cacheWrap);
      body.appendChild(fg);

      card.querySelector(".model-remove-link").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.confirm.removeModel", { name }))) return;
        delete d.models[name];
        markFormDirty("models");
        renderModelCards(d, el);
      });
      grid.appendChild(card);
    });

    el.appendChild(grid);
  }

  // buildModelConfigFields builds the shared model-configuration controls used
  // both by an in-place model card and by the "Add model" dialog. It returns
  // the header STREAMING toggle (`streamWrap`) and the field grid (`fg`:
  // provider, model combo, prices, separator, embedding flag, dim) bound to the
  // model object `m`. `onChange` fires after every edit; `rerender` rebuilds the
  // host so model-combo prefills become visible; `refreshEmbedSelector`
  // re-renders the embed dropdown when the EMBEDDING flag toggles; `name` is the
  // model's key, used to clear `embed_model_ref` when a model stops being an
  // embedder (pass "" for a not-yet-named new model).
  function buildModelConfigFields(d, m, { onChange, rerender, refreshEmbedSelector, name, prefillOverwrites } = {}) {
    onChange = onChange || (() => {});
    rerender = rerender || (() => {});
    refreshEmbedSelector = refreshEmbedSelector || (() => {});
    name = name || "";
    const providerNames = Object.keys(d.providers || {});

    // STREAMING toggle — meant to live in the card header, left of the model
    // name. ON when the model streams (the default), OFF when it falls back to
    // the non-streaming endpoint (persisted as disable_streaming). Use OFF for
    // backends whose streamed output misbehaves (e.g. a quantised model behind
    // vLLM/LiteLLM that runs away only when streamed).
    const streamWrap = document.createElement("span");
    streamWrap.className = "model-stream-wrap";
    streamWrap.setAttribute("data-tip", tr("set.model.streamTip"));
    const streamText = document.createElement("span");
    streamText.className = "model-stream-label";
    streamText.textContent = tr("set.model.streaming");
    const streamSwitch = document.createElement("label");
    streamSwitch.className = "agent-toggle-switch model-stream-toggle";
    const streamCb = document.createElement("input");
    streamCb.type = "checkbox";
    streamCb.className = "agent-toggle-input";
    streamCb.checked = !m.disable_streaming;
    streamCb.addEventListener("change", () => {
      if (streamCb.checked) delete m.disable_streaming; else m.disable_streaming = true;
      onChange();
    });
    const streamSlider = document.createElement("span");
    streamSlider.className = "agent-toggle-slider";
    streamSwitch.appendChild(streamCb); streamSwitch.appendChild(streamSlider);
    streamWrap.appendChild(streamSwitch); streamWrap.appendChild(streamText);

    // PROMPT CACHE toggle — OFF by default (persisted as prompt_cache). When ON
    // the OpenAI-compat adapter adds Anthropic `cache_control` breakpoints to the
    // long-lived prefix so an upstream LiteLLM proxy caches it against the backing
    // Anthropic model. Leave OFF for a plain OpenAI endpoint (it caches
    // automatically and may reject the annotation).
    const cacheWrap = document.createElement("span");
    cacheWrap.className = "model-stream-wrap";
    cacheWrap.setAttribute("data-tip", tr("set.model.cacheTip"));
    const cacheText = document.createElement("span");
    cacheText.className = "model-stream-label";
    cacheText.textContent = tr("set.model.promptCache");
    const cacheSwitch = document.createElement("label");
    cacheSwitch.className = "agent-toggle-switch model-stream-toggle";
    const cacheCb = document.createElement("input");
    cacheCb.type = "checkbox";
    cacheCb.className = "agent-toggle-input";
    cacheCb.checked = !!m.prompt_cache;
    cacheCb.addEventListener("change", () => {
      if (cacheCb.checked) m.prompt_cache = true; else delete m.prompt_cache;
      onChange();
    });
    const cacheSlider = document.createElement("span");
    cacheSlider.className = "agent-toggle-slider";
    cacheSwitch.appendChild(cacheCb); cacheSwitch.appendChild(cacheSlider);
    cacheWrap.appendChild(cacheSwitch); cacheWrap.appendChild(cacheText);

    const fg = document.createElement("div");
    fg.className = "model-field-grid";

    // PROVIDER (dropdown sourced from d.providers)
    const provF = document.createElement("div");
    provF.className = "model-field";
    const provLbl = document.createElement("label");
    provLbl.className = "model-field-label";
    provLbl.textContent = tr("set.model.provider");
    const provSel = document.createElement("select");
    provSel.className = "model-field-input";
    if (!providerNames.length) {
      const opt = document.createElement("option");
      opt.value = ""; opt.textContent = tr("set.model.noProviders");
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

    // MODEL (combobox, sourced via provider_ref).
    const combo = modelComboField(m, onChange, n => d.providers[n], rerender, { prefillOverwrites });
    combo.className = "model-field model-field-combo";
    const comboSpan = combo.querySelector("span");
    if (comboSpan) { comboSpan.className = "model-field-label"; comboSpan.textContent = tr("set.model.model"); }
    fg.appendChild(combo);

    fg.appendChild(numField("context_length", m.context_length, v => { m.context_length = v; onChange(); }));
    fg.appendChild(numField("input_token_price_per_million", m.input_token_price_per_million, v => { m.input_token_price_per_million = v; onChange(); }));
    fg.appendChild(numField("cached_input_token_price_per_million", m.cached_input_token_price_per_million, v => { m.cached_input_token_price_per_million = v; onChange(); }, tr("set.model.cachedInputTip")));
    fg.appendChild(numField("cache_creation_token_price_per_million", m.cache_creation_token_price_per_million, v => { m.cache_creation_token_price_per_million = v; onChange(); }, tr("set.model.cacheCreationTip")));
    fg.appendChild(numField("output_token_price_per_million", m.output_token_price_per_million, v => { m.output_token_price_per_million = v; onChange(); }));

    // Thin separator setting the embedder-specific fields (EMBEDDING MODEL +
    // DIM) apart from the general model configuration above.
    const sep = document.createElement("div");
    sep.className = "model-field-sep";
    fg.appendChild(sep);

    // EMBEDDING flag — marks this entry as an embeddings model so it appears in
    // the "internal embedding model" selector. Uses the same pill switch as the
    // agent "Active State" toggle; the explanatory text is a tooltip.
    const embF = document.createElement("div");
    embF.className = "model-field";
    embF.setAttribute("data-tip", tr("set.model.embeddingUsable"));
    const embLbl = document.createElement("label");
    embLbl.className = "model-field-label";
    embLbl.textContent = tr("set.model.embeddingModel");
    const embSwitch = document.createElement("label");
    embSwitch.className = "agent-toggle-switch";
    embSwitch.setAttribute("data-tip", tr("set.model.embeddingUsable"));
    const cb = document.createElement("input");
    cb.type = "checkbox";
    cb.className = "agent-toggle-input";
    cb.checked = !!m.embedding;
    cb.addEventListener("change", () => {
      if (cb.checked) m.embedding = true; else delete m.embedding;
      // If this model was the active embedder and is no longer embedding, clear
      // the selection so the saved config stays consistent.
      if (!cb.checked && name && (d.embed_model_ref || "").toLowerCase() === name.toLowerCase()) delete d.embed_model_ref;
      onChange();
      refreshEmbedSelector();
    });
    const embSlider = document.createElement("span");
    embSlider.className = "agent-toggle-slider";
    embSwitch.appendChild(cb); embSwitch.appendChild(embSlider);
    embF.appendChild(embLbl); embF.appendChild(embSwitch);
    fg.appendChild(embF);

    // DIM — embedding output dimension (e.g. 1536, 768). Blank = learn it from
    // the first response. The ⟳ button probes the embeddings endpoint.
    fg.appendChild(dimField(m, onChange));

    return { streamWrap, cacheWrap, fg };
  }

  // appModelDialog presents a full model-configuration popup for adding a new
  // model: a NAME field plus the shared streaming/provider/model/price/embedding
  // controls. Resolves to { name, model } on Save, or null on Discard/Escape.
  function appModelDialog(d) {
    return new Promise(resolve => {
      const providerNames = Object.keys(d.providers || {});
      const m = { provider_ref: providerNames[0] || "", model: "" };

      const overlay = document.createElement("div");
      overlay.className = "app-dialog-overlay";

      const box = document.createElement("div");
      box.className = "app-dialog model-dialog";
      box.setAttribute("role", "dialog");
      box.setAttribute("aria-modal", "true");

      const titleEl = document.createElement("p");
      titleEl.className = "app-dialog-msg";
      titleEl.textContent = tr("set.model.addModelTitle");
      box.appendChild(titleEl);

      // NAME — kept outside the re-rendered config area so its value survives a
      // model-combo prefill re-render.
      const nameField = document.createElement("div");
      nameField.className = "model-field model-field-full";
      const nameLbl = document.createElement("label");
      nameLbl.className = "model-field-label";
      nameLbl.textContent = tr("set.model.name");
      const nameInp = document.createElement("input");
      nameInp.type = "text";
      nameInp.className = "model-field-input";
      nameInp.setAttribute("autocomplete", "off");
      nameInp.placeholder = tr("set.model.namePlaceholder");
      const nameErr = document.createElement("span");
      nameErr.className = "model-dialog-err";
      nameInp.addEventListener("input", () => { nameErr.textContent = ""; });
      nameField.appendChild(nameLbl);
      nameField.appendChild(nameInp);
      nameField.appendChild(nameErr);
      box.appendChild(nameField);

      const configHost = document.createElement("div");
      box.appendChild(configHost);

      function renderConfig() {
        configHost.innerHTML = "";
        const { streamWrap, cacheWrap, fg } = buildModelConfigFields(d, m, {
          onChange: () => {},
          rerender: renderConfig,
          refreshEmbedSelector: () => {},
          name: "",
          prefillOverwrites: true,
        });
        const streamRow = document.createElement("div");
        streamRow.className = "model-dialog-stream";
        streamRow.appendChild(streamWrap);
        streamRow.appendChild(cacheWrap);
        configHost.appendChild(streamRow);
        configHost.appendChild(fg);
      }
      renderConfig();

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";

      const discardBtn = document.createElement("button");
      discardBtn.type = "button";
      discardBtn.textContent = tr("settings.discard");

      const saveBtn = document.createElement("button");
      saveBtn.type = "button";
      saveBtn.className = "btn-primary";
      saveBtn.textContent = tr("common.save");

      const close = result => { overlay.remove(); resolve(result); };
      discardBtn.addEventListener("click", () => close(null));
      saveBtn.addEventListener("click", () => {
        const name = nameInp.value.trim().toLowerCase();
        if (!name) { nameErr.textContent = tr("set.model.nameRequired"); nameInp.focus(); return; }
        if (d.models[name]) { nameErr.textContent = `Model "${name}" already exists.`; nameInp.focus(); return; }
        close({ name, model: m });
      });

      overlay.addEventListener("click", e => { if (e.target === overlay) close(null); });
      box.addEventListener("keydown", e => {
        if (e.key === "Escape") { e.stopPropagation(); close(null); }
      });

      actions.appendChild(discardBtn);
      actions.appendChild(saveBtn);
      box.appendChild(actions);
      overlay.appendChild(box);
      document.body.appendChild(overlay);
      nameInp.focus();
    });
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

  function numField(key, val, onCh, tip) {
    const f = document.createElement("div");
    f.className = "model-field";
    if (tip) f.setAttribute("data-tip", tip);
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

  // dimField builds the embedding DIM row: a number input plus a ⟳ button that
  // probes the provider's embeddings endpoint (GET /api/providers/embedding-dim)
  // with the configured model and fills the detected vector length. Detection
  // needs both a provider (provider_ref, or legacy inline provider) and a model
  // id; it errors clearly when either is missing or the model isn't an
  // embeddings model.
  function dimField(m, onCh) {
    const f = document.createElement("div");
    f.className = "model-field model-field-dim";
    const lbl = document.createElement("label");
    lbl.className = "model-field-label";
    lbl.textContent = tr("set.model.dim");
    const wrap = document.createElement("div");
    wrap.className = "combo-wrap";
    const inp = document.createElement("input");
    inp.type = "number";
    inp.className = "model-field-input";
    inp.value = m.dim == null ? "" : m.dim;
    inp.addEventListener("input", () => {
      const n = inp.value === "" ? undefined : Number(inp.value);
      if (Number.isFinite(n) && n) m.dim = n; else delete m.dim;
      onCh();
    });
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "combo-fetch-btn";
    btn.setAttribute("data-tip", tr("set.model.detectDimTip"));
    btn.textContent = "⟳";
    btn.addEventListener("click", async () => {
      const model = (m.model || "").trim();
      if (!model) { setStatus(tr("set.status.setModelFirst")); return; }
      const providerRef = (m.provider_ref || "").trim();
      const params = new URLSearchParams();
      if (providerRef) {
        params.set("provider_ref", providerRef);
      } else {
        const provider = (m.provider || "").trim();
        if (!provider) { setStatus(tr("set.status.setProviderFirst")); return; }
        params.set("provider", provider);
        if (m.api_key) params.set("api_key", m.api_key);
        if (m.base_url) params.set("base_url", m.base_url);
      }
      params.set("model", model);
      btn.disabled = true;
      btn.textContent = "…";
      setStatus(tr("set.status.detectingDim"));
      try {
        const r = await fetch(BASE_PATH + `/api/providers/embedding-dim?${params}`, { headers: authHeaders() });
        const j = await r.json();
        if (!r.ok) throw new Error(j.error || r.statusText);
        m.dim = j.dim;
        inp.value = j.dim;
        onCh();
        setStatus(`Detected dimension: ${j.dim}.`);
      } catch (e) {
        setStatus(tr("set.status.detectDimFailed", { error: e.message }));
      } finally {
        btn.disabled = false;
        btn.textContent = "⟳";
      }
    });
    wrap.appendChild(inp);
    wrap.appendChild(btn);
    f.appendChild(lbl);
    f.appendChild(wrap);
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
    eye.setAttribute("data-tip", tr("set.env.showHide"));
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
  //
  // opts.prefillOverwrites — when true (the add-model dialog), picking a model
  // from the dropdown does a clean refresh of the prefillable metadata: each
  // field is replaced by the selected model's value, and cleared when the model
  // doesn't expose one. This prevents a first pick's context length / prices
  // from sticking when the user then chooses a different model. When false (the
  // default, used for an already-configured card) prefill only fills fields the
  // user left blank, so it never clobbers values they typed.
  function modelComboField(m, onChange, resolveProvider, onPrefill, opts) {
    const prefillOverwrites = !!(opts && opts.prefillOverwrites);
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
        li.textContent = q ? tr("set.model.noMatch") : tr("set.model.noModelsLoaded");
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
          // Prefill any metadata the provider exposed (LiteLLM /model/info). In
          // overwrite mode (add-model dialog) set the field from the selected
          // model and clear it when absent — a clean refresh per selection.
          // Otherwise only fill fields the user left blank.
          const setMeta = (key, val) => {
            if (prefillOverwrites) { if (val) m[key] = val; else delete m[key]; }
            else if (val && !m[key]) m[key] = val;
          };
          setMeta("context_length", mdl.context_length);
          setMeta("input_token_price_per_million", mdl.input_token_price_per_million);
          setMeta("cached_input_token_price_per_million", mdl.cached_input_token_price_per_million);
          setMeta("cache_creation_token_price_per_million", mdl.cache_creation_token_price_per_million);
          setMeta("output_token_price_per_million", mdl.output_token_price_per_million);
          setMeta("dim", mdl.dim);
          // An embedding-mode model selected here is, by definition, an embedder.
          if (mdl.embedding) m.embedding = true;
          else if (prefillOverwrites) delete m.embedding;
          onChange();
          panel.hidden = true;
          // Re-render so the prefilled fields (which are static inputs built at
          // render time) reflect the new values; falls back to no-op when the
          // caller didn't supply a re-render hook.
          if (typeof onPrefill === "function") onPrefill();
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
    fetchBtn.setAttribute("data-tip", tr("set.model.loadModelsTip"));
    fetchBtn.textContent = "⟳";

    fetchBtn.addEventListener("click", async () => {
      const providerRef = (m.provider_ref || "").trim();
      let params;
      let sourceLabel;
      if (providerRef && typeof resolveProvider === "function") {
        params = new URLSearchParams({ provider_ref: providerRef });
        sourceLabel = providerRef;
      } else {
        const provider = (m.provider || "").trim();
        if (!provider) { setStatus(tr("set.status.setProviderFirst")); return; }
        params = new URLSearchParams({ provider });
        if (m.api_key) params.set("api_key", m.api_key);
        if (m.base_url) params.set("base_url", m.base_url);
        sourceLabel = provider;
      }
      fetchBtn.disabled = true;
      fetchBtn.textContent = "…";
      setStatus(tr("set.status.fetchingModels"));
      try {
        const r = await fetch(BASE_PATH + `/api/providers/models?${params}`, { headers: authHeaders() });
        const j = await r.json();
        if (!r.ok) throw new Error(j.error || r.statusText);
        allModels = (j.models || []).slice().sort((a, b) =>
          (a.id || "").localeCompare(b.id || "", undefined, { sensitivity: "base" })
        );
        // Show all models unfiltered; typing will narrow the list.
        panel.hidden = false;
        renderList("");
        input.focus();
        setStatus(`Loaded ${allModels.length} model(s) from ${sourceLabel}.`);
      } catch (e) {
        // Show error inside the panel so it's visible even if the status bar is offscreen.
        allModels = [];
        list.innerHTML = "";
        const li = document.createElement("li");
        li.className = "combo-empty combo-error";
        li.textContent = e.message;
        list.appendChild(li);
        panel.hidden = false;
        setStatus(tr("set.status.loadModelsFailed", { error: e.message }));
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
    code_search: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="10" cy="10" r="7"/><line x1="21" y1="21" x2="15" y2="15"/><polyline points="8 8 6 10 8 12"/><polyline points="12 8 14 10 12 12"/></svg>`,
    docs: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20"/><path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z"/></svg>`,
  };
  const TOOL_DISPLAY = {
    Bash: tr("tool.display.Bash"), Read: tr("tool.display.Read"), Write: tr("tool.display.Write"), Edit: tr("tool.display.Edit"),
    Grep: tr("tool.display.Grep"), Glob: tr("tool.display.Glob"), revert: tr("tool.display.revert"), mime: tr("tool.display.mime"),
    mcp: tr("tool.display.mcp"), Skill: tr("tool.display.Skill"),
    softskills: tr("tool.display.softskills"), calc: tr("tool.display.calc"), ddg: tr("tool.display.ddg"),
    serpapi: tr("tool.display.serpapi"), web: tr("tool.display.web"), registries: tr("tool.display.registries"),
    code_search: tr("tool.display.code_search"), docs: tr("tool.display.docs"),
  };

  // updateFleetModelLine syncs the model display under a fleet-list item with
  // the agent's current model_ref / recommended_model. Handles three states:
  // a resolved model_ref, a recommended (angle-bracketed) fallback, or empty.
  function updateFleetModelLine(fleetItem, a) {
    const info = fleetItem.querySelector(".agent-fleet-info");
    if (!info) return;
    let modelEl = info.querySelector(".agent-fleet-model");
    const desired = a.model_ref
      ? { text: a.model_ref, recommended: false }
      : (a.recommended_model ? { text: `<${a.recommended_model}>`, recommended: true } : null);
    if (!desired) {
      if (modelEl) modelEl.remove();
      return;
    }
    if (!modelEl) {
      modelEl = document.createElement("span");
      modelEl.className = "agent-fleet-model";
      info.appendChild(modelEl);
    }
    modelEl.textContent = desired.text;
    modelEl.classList.toggle("agent-fleet-model-recommended", desired.recommended);
  }

  function renderAgentAgents(d) {
    const fleetList = bodyEl.querySelector("#agent-fleet-list");
    const detailPanel = bodyEl.querySelector("#agent-detail-panel");
    if (!fleetList || !detailPanel) return;

    if (!d.agents.length) {
      fleetList.innerHTML = `<p class="empty" style="padding:1rem">${escHtml(tr("set.fleet.noAgents"))}</p>`;
      detailPanel.innerHTML = "";
      return;
    }

    if (!state.activeAgentInitialized) {
      state.activeAgentInitialized = true;
      const savedName = localStorage.getItem(ACTIVE_AGENT_KEY);
      let idx = savedName ? d.agents.findIndex(a => a.name === savedName) : -1;
      // No valid prior selection: default to the leader if one exists,
      // otherwise the first agent in the list.
      if (idx < 0) {
        const isLeader = a => !!a.leader || (a.name || "").toLowerCase() === "leader";
        idx = d.agents.findIndex(isLeader);
      }
      state.activeAgentIdx = idx >= 0 ? idx : 0;
    }

    if (state.activeAgentIdx >= d.agents.length) state.activeAgentIdx = d.agents.length - 1;
    if (state.activeAgentIdx < 0) state.activeAgentIdx = 0;

    const activeName = d.agents[state.activeAgentIdx]?.name;
    if (activeName) localStorage.setItem(ACTIVE_AGENT_KEY, activeName);

    // Fleet list
    fleetList.innerHTML = "";

    // Separate agents into built-in and custom strictly by the on-disk
    // `builtin` flag — the source of truth. (The read-only / undeletable
    // treatment is a separate axis driven by isBuiltinAgent(): agents wired
    // into the binary. So a shipped-but-customizable agent can sit in the
    // built-in group yet stay editable.)
    const isBuiltinFlag = (a) => a.builtin === true;
    const byName = (x, y) => (x.name || "").localeCompare(y.name || "", undefined, { sensitivity: "base" });
    const builtinAgents = d.agents.filter(isBuiltinFlag).sort(byName);
    const customAgents = d.agents.filter(a => !isBuiltinFlag(a)).sort(byName);

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
        let modelHtml = "";
        if (a.model_ref) {
          modelHtml = `<span class="agent-fleet-model">${escHtml(a.model_ref)}</span>`;
        } else if (a.recommended_model) {
          // Frontmatter declared a model the local catalog doesn't ship —
          // render it greyed in angle brackets to flag the recommendation.
          modelHtml = `<span class="agent-fleet-model agent-fleet-model-recommended">&lt;${escHtml(a.recommended_model)}&gt;</span>`;
        }
        item.innerHTML = `
          <span class="agent-fleet-dot ${a.enabled !== false ? "dot-live" : "dot-off"}"></span>
          <div class="agent-fleet-info">
            <span class="agent-fleet-name">${escHtml(a.name || tr("app.askuser.unnamed"))} ${agentSourceBadge}</span>
            ${modelHtml}
          </div>
        `;
        item.addEventListener("click", () => { state.activeAgentIdx = originalIdx; renderAgentAgents(d); });
        fleetList.appendChild(item);
      });
    };

    // Render custom agents first (labelled simply "AGENTS"), then built-in
    renderAgentGroup(customAgents, tr("set.fleet.agentsLabel"));
    renderAgentGroup(builtinAgents, tr("set.fleet.builtinAgentsLabel"));

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
        <h2 class="agent-detail-name">${escHtml(a.name || tr("app.askuser.unnamed"))}</h2>
        ${detailSourceBadge}
        <span class="agent-live-badge">LIVE</span>
      </div>
      <div class="agent-detail-title-right">
        <label class="agent-active-toggle-wrap">
          <span class="agent-active-toggle-label">${escHtml(tr("set.agent.activeState"))}</span>
          <span class="agent-toggle-switch">
            <input type="checkbox" class="agent-toggle-input" ${isEnabled ? "checked" : ""} ${isLeader ? "disabled" : ""}>
            <span class="agent-toggle-slider"></span>
          </span>
        </label>
        ${isBuiltin ? "" : `<button type="button" class="model-remove-link agent-remove-link">${escHtml(tr("set.agent.removeBtn"))}</button>`}
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
        if (!await appConfirm(tr("set.confirm.removeAgent", { name: a.name }))) return;
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
    genHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg><h3>${escHtml(tr("set.hdr.generalSettings"))}</h3>`;
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
    genGrid.appendChild(genField(tr("set.agent.displayName"), f => {
      const inp = document.createElement("input");
      inp.type = "text"; inp.className = "agent-gen-input"; inp.value = a.name || "";
      if (isLeader) inp.disabled = true;
      inp.addEventListener("input", () => {
        a.name = inp.value;
        detailPanel.querySelector(".agent-detail-name").textContent = a.name || tr("app.askuser.unnamed");
        const nameEl = bodyEl.querySelectorAll(".agent-fleet-item")[idx]?.querySelector(".agent-fleet-name");
        if (nameEl) nameEl.textContent = a.name || tr("app.askuser.unnamed");
        onChange();
      });
      f.appendChild(inp);
    }));

    // Model Reference
    genGrid.appendChild(genField(tr("set.agent.modelRef"), f => {
      const sel = document.createElement("select");
      sel.className = "agent-gen-input";
      for (const o of ["", ...modelOptions]) {
        const opt = document.createElement("option");
        opt.value = o; opt.textContent = o || tr("set.none");
        if (o === (a.model_ref || "")) opt.selected = true;
        sel.appendChild(opt);
      }
      sel.addEventListener("change", () => {
        a.model_ref = sel.value;
        const fleetItem = bodyEl.querySelectorAll(".agent-fleet-item")[idx];
        if (fleetItem) updateFleetModelLine(fleetItem, a);
        onChange();
      });
      f.appendChild(sel);

      // Surface a frontmatter "model:" hint that the local catalog can't
      // resolve as a recommended model in angle brackets.
      if (a.recommended_model) {
        const hint = document.createElement("span");
        hint.className = "agent-model-recommendation";
        hint.textContent = `<${a.recommended_model}>`;
        hint.setAttribute("data-tip", tr("set.agent.recommendedTip"));
        f.appendChild(hint);
      }
    }));

    genSection.appendChild(genGrid);
    body.appendChild(genSection);

    // ── Available Tools ──
    const toolSection = document.createElement("section");
    toolSection.className = "agent-detail-section";
    const toolHdr = document.createElement("div");
    toolHdr.className = "agent-section-hdr";
    toolHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg><h3>${escHtml(tr("set.hdr.availableTools"))}</h3>`;
    toolSection.appendChild(toolHdr);

    const toolGrid = document.createElement("div");
    toolGrid.className = "agent-tools-grid";
    const effectiveTools = (isLeader && (!a.tools || !a.tools.length)) ? [...TOOL_GROUPS] : (a.tools || []);
    const cur = new Set(effectiveTools);
    const btnByTool = {};
    const toolEntries = [];

    // code_search only mounts when a semantic embedder is configured. Mirror
    // the serpapi pattern and grey it out when no embedding model is selected
    // (agents.json override wins, else models.json embed_model_ref). Env-only
    // (OMNIS_EMBED_*) config isn't visible here — same limitation as serpapi_key.
    const embedRef = (d.embed_model_ref || state.parsed.models?.value?.embed_model_ref || "").toString().trim();
    const embedConfigured = !!embedRef;

    for (const t of TOOL_GROUPS) {
      const isSerpDisabled = t === "serpapi" && !d.serpapi_key;
      const isCodeSearchDisabled = t === "code_search" && !embedConfigured;
      const isDisabledTool = isSerpDisabled || isCodeSearchDisabled;
      const isOn = cur.has(t);
      const btn = document.createElement("div");
      btn.className = "agent-tool-card" + (isOn ? " tool-on" : "") + (isDisabledTool ? " tool-disabled" : "");
      btn.setAttribute("data-tip", TOOL_DISPLAY[t] || "");
      btn.innerHTML = `
        <div class="agent-tool-icon">${TOOL_ICONS[t] || ""}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${escHtml(t)}</span>
          <span class="agent-tool-desc">${escHtml(TOOL_DISPLAY[t] || "")}</span>
        </div>
        <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
      `;
      if (!isDisabledTool) {
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
        key: "leader", label: "leader", desc: tr("set.agent.canLead"),
        icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M2 20h20"/><path d="M5 20V8l7-4 7 4v12"/><path d="M9 20v-6h6v6"/></svg>`,
        getValue: () => isLeader ? true : !!a.leader,
        setValue: v => { a.leader = v; onChange(); },
        locked: isLeader,
      },
      {
        key: "allow_file_attachments", label: "files", desc: tr("set.agent.fileAttachments"),
        icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21.44 11.05l-9.19 9.19a6 6 0 0 1-8.49-8.49l9.19-9.19a4 4 0 0 1 5.66 5.66l-9.2 9.19a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>`,
        getValue: () => !!a.allow_file_attachments,
        setValue: v => { a.allow_file_attachments = v; onChange(); },
      },
    ];
    for (const fc of featureCards) {
      let fcOn = fc.getValue();
      const fcBtn = document.createElement("div");
      fcBtn.className = "agent-tool-card" + (fcOn ? " tool-on" : "") + (fc.locked ? " tool-disabled" : "");
      fcBtn.setAttribute("data-tip", fc.desc || "");
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

    // ── Parallelism (max_instances) ──
    // Only meaningful for sub-agents: it caps how many invocations the leader
    // may fan out in a single tool call. The leader is never fanned out and the
    // curator is a process-wide hook (both excluded by buildSubAgents), so the
    // setting is inert for them — hide the control.
    if (!isLeader && (a.name || "").toLowerCase() !== "curator") {
      const parSec = document.createElement("section");
      parSec.className = "agent-detail-section";
      const parHdr = document.createElement("div");
      parHdr.className = "agent-section-hdr";
      parHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="6" y1="3" x2="6" y2="21"/><line x1="12" y1="3" x2="12" y2="21"/><line x1="18" y1="3" x2="18" y2="21"/></svg><h3>${escHtml(tr("set.hdr.parallelism"))}</h3>`;
      parSec.appendChild(parHdr);
      const parBody = document.createElement("div");
      parBody.className = "agent-gen-grid";
      const parField = document.createElement("div");
      parField.className = "agent-gen-field";
      const parLbl = document.createElement("label");
      parLbl.className = "agent-gen-label";
      parLbl.textContent = tr("set.agent.maxInstances");
      // App tooltip (themed #tip-layer via data-tip), not the native browser
      // title bubble, to match the rest of the settings UI.
      const parInfo = document.createElement("span");
      parInfo.className = "agent-gen-info";
      parInfo.textContent = "?";
      parInfo.setAttribute("data-tip", tr("set.agent.maxInstancesTip"));
      parLbl.appendChild(parInfo);
      // Custom stepper so the +/- controls match the app look & feel instead of
      // the browser's native number spinner.
      const parWrap = document.createElement("div");
      parWrap.className = "num-stepper";
      const parInp = document.createElement("input");
      parInp.type = "number"; parInp.min = "1"; parInp.step = "1";
      parInp.className = "agent-gen-input num-stepper-input";
      parInp.value = String(Math.max(1, parseInt(a.max_instances, 10) || 1));
      const parDec = document.createElement("button");
      parDec.type = "button";
      parDec.className = "num-stepper-btn";
      parDec.textContent = "−";
      parDec.setAttribute("aria-label", tr("set.agent.decrease"));
      const parInc = document.createElement("button");
      parInc.type = "button";
      parInc.className = "num-stepper-btn";
      parInc.textContent = "+";
      parInc.setAttribute("aria-label", tr("set.agent.increase"));
      const applyMax = () => {
        let n = parseInt(parInp.value, 10);
        if (!Number.isFinite(n) || n < 1) n = 1;
        parInp.value = String(n);
        parDec.disabled = n <= 1;
        // Keep the file clean: only persist when it opts into parallelism.
        if (n > 1) a.max_instances = n; else delete a.max_instances;
        onChange();
      };
      const bump = (delta) => {
        parInp.value = String((parseInt(parInp.value, 10) || 1) + delta);
        applyMax();
      };
      parDec.addEventListener("click", () => bump(-1));
      parInc.addEventListener("click", () => bump(1));
      parInp.addEventListener("input", applyMax);
      parInp.addEventListener("change", applyMax);
      parDec.disabled = (parseInt(parInp.value, 10) || 1) <= 1;
      parWrap.appendChild(parDec);
      parWrap.appendChild(parInp);
      parWrap.appendChild(parInc);
      const parHint = document.createElement("p");
      parHint.className = "agent-gen-hint";
      parHint.textContent = tr("set.agent.maxInstancesHint");
      parField.appendChild(parLbl);
      parField.appendChild(parWrap);
      parField.appendChild(parHint);
      parBody.appendChild(parField);
      parSec.appendChild(parBody);
      body.appendChild(parSec);

      // ── Sessions (resumable_sessions) ──
      // Durable, re-attachable sub-agent sessions are ON by default (opt-out):
      // each call returns a `session` handle the leader can pass back as
      // resume_session to CONTINUE that exact conversation instead of starting
      // fresh. Toggle OFF to make this sub-agent a stateless pure function (a
      // throwaway session per call). Persist-clean: only the opt-out (false) is
      // written; the default-on case leaves the key absent. Same leader/curator
      // gate as Parallelism (both inert for non-fan-out roots).
      const resSec = document.createElement("section");
      resSec.className = "agent-detail-section";
      const resHdr = document.createElement("div");
      resHdr.className = "agent-section-hdr";
      resHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg><h3>${escHtml(tr("set.hdr.sessions"))}</h3>`;
      resSec.appendChild(resHdr);
      const resBody = document.createElement("div");
      resBody.className = "agent-gen-grid";
      const resField = document.createElement("div");
      resField.className = "agent-gen-field";
      const resRow = document.createElement("div");
      resRow.className = "agent-toggle-row";
      resRow.setAttribute("data-tip", tr("set.agent.resumableTip"));
      const resSwitch = document.createElement("label");
      resSwitch.className = "agent-toggle-switch";
      const resCb = document.createElement("input");
      resCb.type = "checkbox";
      resCb.className = "agent-toggle-input";
      // Opt-out default: checked unless explicitly disabled (resumable_sessions === false).
      resCb.checked = a.resumable_sessions !== false;
      resCb.addEventListener("change", () => {
        if (resCb.checked) delete a.resumable_sessions; else a.resumable_sessions = false;
        onChange();
      });
      const resSlider = document.createElement("span");
      resSlider.className = "agent-toggle-slider";
      resSwitch.appendChild(resCb);
      resSwitch.appendChild(resSlider);
      const resText = document.createElement("span");
      resText.className = "agent-toggle-text";
      resText.textContent = tr("set.agent.resumable");
      resRow.appendChild(resSwitch);
      resRow.appendChild(resText);
      const resHint = document.createElement("p");
      resHint.className = "agent-gen-hint";
      resHint.textContent = tr("set.agent.resumableHint");
      resField.appendChild(resRow);
      resField.appendChild(resHint);
      resBody.appendChild(resField);
      resSec.appendChild(resBody);
      body.appendChild(resSec);
    }

    // ── Skills ──
    const skillsSec = document.createElement("section");
    skillsSec.className = "agent-detail-section" + (cur.has("Skill") ? "" : " section-inactive");
    const skillsHdr = document.createElement("div");
    skillsHdr.className = "agent-section-hdr";
    skillsHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg><h3>${escHtml(tr("settings.title.skills"))}</h3>`;
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
    mcpHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="12" rx="2"/><line x1="2" y1="20" x2="22" y2="20"/><line x1="8" y1="16" x2="8" y2="20"/><line x1="16" y1="16" x2="16" y2="20"/></svg><h3>${escHtml(tr("settings.title.mcp"))}</h3>`;
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
    a2aHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z"/></svg><h3>${escHtml(tr("settings.title.a2a"))}</h3>`;
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
    instrHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg><h3>${escHtml(tr("set.hdr.instructionSet"))}</h3>`;
    instrSection.appendChild(instrHdr);

    const instrBody = document.createElement("div");
    instrBody.className = "agent-instr-body";

    // Public Description
    const descF = document.createElement("div");
    descF.className = "agent-instr-field";
    const descLbl = document.createElement("label");
    descLbl.className = "agent-instr-label";
    descLbl.textContent = tr("set.agent.publicDesc");
    if (isBuiltin) {
      const tag = document.createElement("span");
      tag.className = "agent-builtin-tag";
      tag.textContent = tr("set.agent.builtin");
      descLbl.appendChild(tag);
    }
    const descInp = document.createElement("input");
    descInp.type = "text"; descInp.className = "agent-gen-input";
    descInp.placeholder = tr("set.agent.descPlaceholder");
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
    sysLbl.textContent = tr("set.agent.systemInstructions");
    if (isBuiltin) {
      const tag = document.createElement("span");
      tag.className = "agent-builtin-tag";
      tag.textContent = tr("set.agent.builtin");
      sysLbl.appendChild(tag);
    }
    const sysCount = document.createElement("span");
    sysCount.className = "agent-instr-count";
    const instrVal = isBuiltin && builtinDefaults ? (builtinDefaults.instruction || "") : (a.instruction || "");
    sysCount.textContent = tr("set.agent.tokensUsed", { count: Math.round(instrVal.length / 4) });
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
        sysCount.textContent = tr("set.agent.tokensUsed", { count: Math.round(ta.value.length / 4) });
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
    adv.innerHTML = `<summary class="agent-advanced-summary">${escHtml(tr("set.agent.advancedPaths"))}</summary>`;
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
      if (isLeader && !a[key]) inp.placeholder = tr("set.default");
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
      upBtn.type = "button"; upBtn.className = "up-btn"; upBtn.textContent = tr("set.agent.moveUp");
      if (!upOk) upBtn.disabled = true;
      const dnBtn = document.createElement("button");
      dnBtn.type = "button"; dnBtn.className = "down-btn"; dnBtn.textContent = tr("set.agent.moveDown");
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

  // ── permissions.json form (Claude Code nomenclature) ──
  // Tiers are deny → ask → allow (precedence order). Each rule is a
  // Tool(specifier) string, e.g. Bash(npm run *), Read(.env), mcp__srv__tool,
  // Agent(Explore) — or a /regex/ escape hatch. Object rules carry an optional
  // reason and a project-scoping cwd (omnis extensions).
  const PERM_TIERS = ["deny", "ask", "allow"];
  const PERM_MODES = ["default", "acceptEdits", "plan", "auto", "dontAsk", "bypassPermissions"];

  function permData() {
    const d = state.parsed["permissions"].value;
    if (!d.permissions || typeof d.permissions !== "object") d.permissions = {};
    const p = d.permissions;
    for (const k of PERM_TIERS) if (!Array.isArray(p[k])) p[k] = [];
    if (!p.defaultMode) p.defaultMode = "default";
    return p;
  }

  function renderPermissionsForm() {
    const id = "permissions";
    const p = permData();
    bodyEl.innerHTML = `
      <div class="settings-form">
        <section class="form-section">
          <h3>${escHtml(tr("set.hdr.defaultMode"))}</h3>
          <div class="form-card" style="margin-bottom:0">
            <select class="perm-mode">
              ${PERM_MODES.map(m => `<option value="${m}"${p.defaultMode === m ? " selected" : ""}>${m}</option>`).join("")}
            </select>
            <p class="empty" style="margin:.4rem 0 0">${escHtml(tr("set.perm.modeHint"))}</p>
          </div>
        </section>
        ${PERM_TIERS.map(k => `
          <section class="form-section">
            <h3>${escHtml(tr("set.perm.tier." + k))} <button type="button" class="add-btn" data-list="${k}">${escHtml(tr("set.perm.addRule"))}</button></h3>
            <div class="form-card" style="margin-bottom:0">
              <div class="rule-list" data-list="${k}"></div>
            </div>
          </section>
        `).join("")}
        <section class="form-section" id="skill-perms-section" style="display:none">
          <h3>${escHtml(tr("set.hdr.fromSkills"))}</h3>
          <div id="skill-perms-list"></div>
        </section>
      </div>
    `;
    const modeSel = bodyEl.querySelector(".perm-mode");
    modeSel.addEventListener("change", () => { p.defaultMode = modeSel.value; markFormDirty(id); });
    bodyEl.querySelectorAll(".add-btn").forEach(btn => {
      btn.addEventListener("click", () => {
        const k = btn.dataset.list;
        p[k].push("");
        markFormDirty(id);
        renderPermRule(p, k);
      });
    });
    for (const k of PERM_TIERS) renderPermRule(p, k);
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
        const tiers = ["deny", "ask", "allow"];
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
                <span class="skill-perm-tier-badge">${escHtml(tr("set.perm.tier." + tier))}</span>
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

  // ruleDisplay renders a rule object/string into the single text field. A
  // {regex} rule shows as /pattern/; a {rule} or bare string shows verbatim.
  function ruleDisplay(rule) {
    if (rule && typeof rule === "object") {
      if (rule.regex) return "/" + rule.regex + "/";
      return rule.rule || "";
    }
    return String(rule || "");
  }

  function renderPermRule(d, key) {
    const el = bodyEl.querySelector(`.rule-list[data-list="${key}"]`);
    el.innerHTML = "";
    if (!d[key].length) { el.innerHTML = `<p class="empty">${escHtml(tr("set.perm.noRules"))}</p>`; return; }
    d[key].forEach((rule, idx) => {
      const isObj = rule && typeof rule === "object";
      const row = document.createElement("div");
      row.className = "rule-row";
      row.innerHTML = `
        <select class="rule-kind">
          <option value="string" ${!isObj ? "selected" : ""}>${escHtml(tr("set.perm.optRule"))}</option>
          <option value="object" ${isObj ? "selected" : ""}>${escHtml(tr("set.perm.optRuleReason"))}</option>
        </select>
        <input type="text" class="rule-pattern" placeholder="Bash(npm run *) · Read(.env) · mcp__srv · /regex/" />
        <input type="text" class="rule-reason" placeholder="${escHtml(tr("set.perm.reasonPlaceholder"))}" />
        <button type="button" class="del-btn">${escHtml(tr("common.remove"))}</button>
      `;
      const kindSel = row.querySelector(".rule-kind");
      const patIn = row.querySelector(".rule-pattern");
      const reaIn = row.querySelector(".rule-reason");
      patIn.value = ruleDisplay(rule);
      reaIn.value = isObj ? (rule.reason || "") : "";
      reaIn.style.display = isObj ? "" : "none";

      const commit = () => {
        const val = patIn.value;
        const isRegex = val.length > 1 && val.startsWith("/") && val.endsWith("/");
        if (kindSel.value === "object") {
          // Preserve fields the form doesn't expose (cwd, tools) so editing a
          // rule never silently drops its tool scope. Route the text into the
          // regex field when wrapped in /…/, otherwise the rule field.
          const prev = (d[key][idx] && typeof d[key][idx] === "object") ? d[key][idx] : {};
          const obj = { ...prev, reason: reaIn.value };
          if (isRegex) { obj.regex = val.slice(1, -1); delete obj.rule; }
          else { obj.rule = val; delete obj.regex; }
          d[key][idx] = obj;
        } else {
          d[key][idx] = val;
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

  // ── hooks.json form (Claude Code hooks schema) ───────────────────
  // On-disk shape:
  //   { "hooks": { "<Event>": [ { "matcher": "<regex>",
  //       "hooks": [ { "type": "command", "command": "...", "timeout": N } ] } ] } }
  // Events map to lifecycle moments; `matcher` is a tool-name regexp for the
  // tool events (ignored for the others). See CLAUDE.md "Lifecycle hooks".
  const HOOK_EVENTS = [
    { id: "PreToolUse",       hint: tr("hook.hint.PreToolUse") },
    { id: "PostToolUse",      hint: tr("hook.hint.PostToolUse") },
    { id: "UserPromptSubmit", hint: tr("hook.hint.UserPromptSubmit") },
    { id: "Stop",             hint: tr("hook.hint.Stop") },
    { id: "SubagentStop",     hint: tr("hook.hint.SubagentStop") },
    { id: "SessionStart",     hint: tr("hook.hint.SessionStart") },
    { id: "SessionEnd",       hint: tr("hook.hint.SessionEnd") },
    { id: "PreCompact",       hint: tr("hook.hint.PreCompact") },
    { id: "Notification",     hint: tr("hook.hint.Notification") },
  ];

  function hooksData() {
    const d = state.parsed["hooks"].value;
    if (!d.hooks || typeof d.hooks !== "object") d.hooks = {};
    return d.hooks;
  }

  function renderHooksForm() {
    const id = "hooks";
    if (!state.parsed[id]) {
      bodyEl.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
      loadParsed(id).then(() => renderHooksForm()).catch(e => {
        bodyEl.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      });
      return;
    }
    const h = hooksData();
    bodyEl.innerHTML = `
      <div class="settings-form">
        <p class="empty" style="margin:0 0 .6rem">
          ${escHtml(tr("set.hook.intro"))}
        </p>
        ${HOOK_EVENTS.map(ev => `
          <section class="form-section">
            <h3>${ev.id}
              <button type="button" class="add-btn" data-event="${ev.id}">${escHtml(tr("set.hook.addMatcher"))}</button>
            </h3>
            <div class="form-card" style="margin-bottom:0">
              <p class="empty" style="margin:0 0 .4rem">${escHtml(ev.hint)}</p>
              <div class="hook-matchers" data-event="${ev.id}"></div>
            </div>
          </section>
        `).join("")}
      </div>
    `;
    bodyEl.querySelectorAll(".add-btn[data-event]").forEach(btn => {
      btn.addEventListener("click", () => {
        const ev = btn.dataset.event;
        if (!Array.isArray(h[ev])) h[ev] = [];
        h[ev].push({ matcher: "", hooks: [{ type: "command", command: "" }] });
        markFormDirty(id);
        renderHookMatchers(h, ev);
      });
    });
    for (const ev of HOOK_EVENTS) renderHookMatchers(h, ev.id);
    updateFooter();
  }

  function renderHookMatchers(h, event) {
    const el = bodyEl.querySelector(`.hook-matchers[data-event="${event}"]`);
    if (!el) return;
    const list = Array.isArray(h[event]) ? h[event] : [];
    el.innerHTML = "";
    if (!list.length) { el.innerHTML = `<p class="empty">${escHtml(tr("set.hook.noHooks"))}</p>`; return; }
    list.forEach((matcher, mIdx) => {
      if (!Array.isArray(matcher.hooks)) matcher.hooks = [];
      const card = document.createElement("div");
      card.className = "hook-matcher-card";
      card.innerHTML = `
        <div class="hook-matcher-row">
          <input type="text" class="hook-matcher" placeholder="${escHtml(tr("set.hook.matcherPlaceholder"))}" />
          <button type="button" class="add-btn hook-add-cmd">${escHtml(tr("set.hook.addCommand"))}</button>
          <button type="button" class="del-btn hook-del-matcher">${escHtml(tr("set.hook.removeMatcher"))}</button>
        </div>
        <div class="hook-commands"></div>
      `;
      const matchIn = card.querySelector(".hook-matcher");
      matchIn.value = matcher.matcher || "";
      matchIn.addEventListener("input", () => { matcher.matcher = matchIn.value; markFormDirty("hooks"); });
      card.querySelector(".hook-del-matcher").addEventListener("click", () => {
        list.splice(mIdx, 1);
        markFormDirty("hooks");
        renderHookMatchers(h, event);
      });
      const cmdWrap = card.querySelector(".hook-commands");
      const paintCmds = () => {
        cmdWrap.innerHTML = "";
        if (!matcher.hooks.length) { cmdWrap.innerHTML = `<p class="empty">${escHtml(tr("set.hook.noCommands"))}</p>`; return; }
        matcher.hooks.forEach((cmd, cIdx) => {
          const row = document.createElement("div");
          row.className = "hook-cmd-row";
          row.innerHTML = `
            <input type="text" class="hook-cmd" placeholder="${escHtml(tr("set.hook.cmdPlaceholder"))}" />
            <input type="number" class="hook-timeout" min="0" placeholder="${escHtml(tr("set.hook.timeoutPlaceholder"))}" />
            <button type="button" class="del-btn">${escHtml(tr("common.remove"))}</button>
          `;
          const cmdIn = row.querySelector(".hook-cmd");
          const toIn = row.querySelector(".hook-timeout");
          cmdIn.value = cmd.command || "";
          if (cmd.timeout) toIn.value = cmd.timeout;
          cmdIn.addEventListener("input", () => {
            cmd.type = "command";
            cmd.command = cmdIn.value;
            markFormDirty("hooks");
          });
          toIn.addEventListener("input", () => {
            const n = parseInt(toIn.value, 10);
            if (Number.isFinite(n) && n > 0) cmd.timeout = n; else delete cmd.timeout;
            markFormDirty("hooks");
          });
          row.querySelector(".del-btn").addEventListener("click", () => {
            matcher.hooks.splice(cIdx, 1);
            markFormDirty("hooks");
            paintCmds();
          });
          cmdWrap.appendChild(row);
        });
      };
      card.querySelector(".hook-add-cmd").addEventListener("click", () => {
        matcher.hooks.push({ type: "command", command: "" });
        markFormDirty("hooks");
        paintCmds();
      });
      paintCmds();
      el.appendChild(card);
    });
  }

  // ── mcp_config.json form (VS Code mcp.json schema) ────────────────
  // On-disk shape:
  //   { "servers": { <name>: { type, command/url, args, env, headers } },
  //     "inputs":  [ { id, type, description, password, options, default } ] }
  // Server string fields may embed "${input:id}" references; those are
  // resolved interactively at first connect by the backend.
  function renderMCPForm() {
    registriesHubRefresh = null;
    const id = "mcp";
    if (!state.parsed[id]) {
      bodyEl.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
        <span class="settings-hint">${tr("set.mcp.importMergeHint")}</span>
      </div>
      <section class="form-section">
        <h3>${escHtml(tr("set.hdr.inputs"))}</h3>
        <p class="settings-hint">${tr("set.mcp.inputsHint")}</p>
        <div id="mcp-inputs"></div>
      </section>
      <section class="form-section">
        <h3>${escHtml(tr("settings.title.mcp"))}</h3>
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
    catch (e) { await appConfirm(tr("set.confirm.invalidJson", { error: e.message })); return; }

    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      await appConfirm(tr("set.confirm.expectedServersInputs"));
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
        renamed.push(tr("set.mcp.renamedServer", { from: name, to: target }));
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
        renamed.push(tr("set.mcp.renamedInput", { from: input.id, to: target }));
      }
      const cloned = JSON.parse(JSON.stringify(input));
      cloned.id = target;
      d.inputs.push(cloned);
      existingInputIds.add(target);
      addedInputs.push(target);
    }

    if (addedServers.length === 0 && addedInputs.length === 0) {
      await appConfirm(tr("set.confirm.nothingToImport"));
      return;
    }

    markFormDirty("mcp");
    renderMCPInputs(d);
    renderMCPList(d);

    const parts = [];
    if (addedServers.length) parts.push(trN("set.mcp.serverCount", addedServers.length));
    if (addedInputs.length)  parts.push(trN("set.mcp.inputCount", addedInputs.length));
    let msg = tr("set.mcp.importedSummary", { parts: parts.join(` ${tr("common.and")} `) });
    if (renamed.length) msg += `\n\n${tr("set.mcp.renamedConflicts")}\n• ${renamed.join("\n• ")}`;
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
      titleEl.textContent = tr("set.mcp.importTitle");
      box.appendChild(titleEl);

      const hint = document.createElement("p");
      hint.className = "settings-hint";
      hint.style.margin = "0";
      hint.textContent = tr("set.mcp.importDialogHint");
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
      cancelBtn.textContent = tr("common.cancel");
      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = tr("common.import");

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
      chip.setAttribute("data-tip", inp.description || inp.id || "");

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
      close.setAttribute("aria-label", tr("set.mcp.removeInput"));
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
    addChip.innerHTML = `<span class="mcp-input-chip-icon">+</span><span class="mcp-input-chip-label">${escHtml(tr("set.mcp.addInputChip"))}</span>`;
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
      empty.textContent = tr("set.mcp.noInputs");
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
      titleEl.textContent = initial && initial.id ? tr("set.mcp.editInput", { id: initial.id }) : tr("set.mcp.addInput");
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
        <label class="model-field-label model-field-label--title">${escHtml(tr("set.mcp.id"))}</label>
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
        <label class="model-field-label model-field-label--title">${escHtml(tr("set.mcp.type"))}</label>
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
        <label class="model-field-label model-field-label--title">${escHtml(tr("common.description"))}</label>
        <input type="text" class="model-field-input" placeholder="${escHtml(tr("set.mcp.descPlaceholder"))}" />
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
        <label class="model-field-label model-field-label--title">${escHtml(tr("set.mcp.defaultOptional"))}</label>
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
          pw.innerHTML = `<input type="checkbox" /><span>${escHtml(tr("set.mcp.treatAsPassword"))}</span>`;
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
          sec.innerHTML = `<h4 class="mcp-section-title">${escHtml(tr("set.hdr.options"))}</h4>`;
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
              const delBtn = document.createElement("button");
              delBtn.type = "button";
              delBtn.className = "mcp-trash";
              delBtn.innerHTML = TRASH_ICON_SVG;
              delBtn.addEventListener("click", () => { draft.options.splice(oi, 1); drawOpts(); });
              r.appendChild(delBtn);
              rows.appendChild(r);
            });
          };
          drawOpts();
          const addBtn = document.createElement("button");
          addBtn.type = "button";
          addBtn.className = "mcp-add-full";
          addBtn.innerHTML = `<span class="mcp-add-full-icon">+</span><span>${escHtml(tr("set.mcp.addOption"))}</span>`;
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
      cancelBtn.textContent = tr("common.cancel");
      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = tr("common.save");

      const close = result => { overlay.remove(); resolve(result); };
      cancelBtn.addEventListener("click", () => close(null));
      okBtn.addEventListener("click", () => {
        const id = (draft.id || "").trim();
        if (!id) { showErr(tr("set.mcp.idRequired")); idInp.focus(); return; }
        if (siblings.some(s => s && s.id === id)) {
          showErr(tr("set.mcp.idInUse", { id }));
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
          <button type="button" class="del-btn mcp-remove">${escHtml(tr("common.delete"))}</button>
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
        b.setAttribute("aria-label", tr("common.remove"));
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
        if (hint) sec.querySelector(".mcp-section-title").setAttribute("data-tip", hint);

        const grid = document.createElement("div");
        grid.className = "mcp-kv-grid";
        const headers = document.createElement("div");
        headers.className = "mcp-kv-headers";
        headers.innerHTML = `
          <span class="model-field-label model-field-label--title">${escHtml(tr("set.kv.key"))}</span>
          <span class="model-field-label model-field-label--title">${escHtml(tr("set.kv.value"))}</span>
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
      const general = mcpSection(tr("set.hdr.generalSettings"));
      const generalGrid = document.createElement("div");
      generalGrid.className = "model-field-grid";
      // Name is the map key, not a field on the Server object. We swap
      // the key on rename. Renaming to an existing name is silently
      // refused (no overwrite); empty names are silently refused too.
      generalGrid.appendChild(mcpField(tr("common.name"), currentName, v => {
        const nv = v.trim();
        nameEl.textContent = nv || tr("app.askuser.unnamed");
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
        <label class="model-field-label model-field-label--title">${escHtml(tr("set.mcp.type"))}</label>
        <select class="model-field-input">
          <option value="stdio">${escHtml(tr("set.mcp.typeStdio"))}</option>
          <option value="http">${escHtml(tr("set.mcp.typeHttp"))}</option>
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
        const inputHint = tr("set.mcp.inputEmbedHint");
        if (mcpTransportKind(s) === "http") {
          const conn = mcpSection(tr("set.mcp.connection"));
          const urlGrid = document.createElement("div");
          urlGrid.className = "model-field-grid";
          urlGrid.appendChild(mcpField(tr("set.mcp.url"), s.url, v => { s.url = v; markFormDirty("mcp"); }, {
            full: true,
            placeholder: "https://api.githubcopilot.com/mcp/",
          }));
          conn.appendChild(urlGrid);
          transportSection.appendChild(conn);
          transportSection.appendChild(mcpKVList({
            title: tr("set.mcp.headers"),
            addLabel: tr("set.mcp.addHeader"),
            store: s.headers,
            keyPlaceholder: "Header-Name",
            valuePlaceholder: "value or Bearer ${input:id}",
            addPromptMsg: tr("set.confirm.headerNamePrompt"),
            hint: inputHint,
          }));
        } else {
          const exec = mcpSection(tr("set.mcp.execution"));
          const cmdGrid = document.createElement("div");
          cmdGrid.className = "model-field-grid";
          cmdGrid.appendChild(mcpField(tr("set.mcp.command"), s.command, v => { s.command = v; markFormDirty("mcp"); }, { full: true }));
          exec.appendChild(cmdGrid);
          transportSection.appendChild(exec);
          transportSection.appendChild(mcpStringList({
            title: tr("set.mcp.arguments"),
            addLabel: tr("set.mcp.addArgument"),
            store: s.args,
          }));
          transportSection.appendChild(mcpKVList({
            title: tr("set.mcp.envVars"),
            addLabel: tr("set.mcp.addVariable"),
            store: s.env,
            keyPlaceholder: "KEY",
            valuePlaceholder: "value or ${input:id}",
            addPromptMsg: tr("set.mcp.envVarPrompt"),
            hint: inputHint,
          }));
        }
      }
      renderTransportSection();

      card.querySelector(".mcp-remove").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.confirm.removeServer", { name: currentName }))) return;
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
      <span class="model-card-empty-label">${escHtml(tr("set.mcp.addServer"))}</span>
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
      cancelBtn.textContent = tr("common.cancel");

      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = withInput ? tr("common.ok") : tr("common.confirm");

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

  function appRegistryDialog({ title = tr("set.reg.addTitle"), initial = {}, isEdit = false, defaultKind = "skills" } = {}) {
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
        ? tr("set.reg.tokenKeepHint")
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
        : defaultKind === "commands"
        ? "https://github.com/owner/repo/tree/main/commands"
        : "https://github.com/owner/repo/tree/main/skills";
      const npKind = ["agents", "mcp", "a2a", "squads", "commands"].includes(defaultKind) ? defaultKind : "skills";
      const namePlaceholder = tr("set.reg.namePlaceholder." + npKind);
      form.innerHTML = `
        <div class="registry-dialog-field">
          <label for="reg-dlg-name">${escHtml(tr("set.reg.nameLabel"))} <span class="registry-dialog-hint">${escHtml(tr("common.optional"))}</span></label>
          <input type="text" id="reg-dlg-name" autocomplete="off"
            placeholder="${escHtml(namePlaceholder)}"
            value="${escHtml(initial.name || "")}" />
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-url">${escHtml(tr("set.reg.repoUrl"))}</label>
          <input type="url" id="reg-dlg-url" autocomplete="off"
            placeholder="${escHtml(urlPlaceholder)}"
            value="${escHtml(initial.url || "")}" />
          <span class="registry-dialog-hint">${escHtml(tr("set.reg.urlHint"))}</span>
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-provider">${escHtml(tr("set.reg.provider"))}</label>
          <select id="reg-dlg-provider">
            <option value="">${escHtml(tr("set.reg.autoDetect"))}</option>
            <option value="github"${initial.provider === "github" ? " selected" : ""}>GitHub</option>
            <option value="gitlab"${initial.provider === "gitlab" ? " selected" : ""}>GitLab</option>
            <option value="gitea"${initial.provider === "gitea" ? " selected" : ""}>Gitea</option>
          </select>
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-kind">${escHtml(tr("set.reg.hosts"))}</label>
          <select id="reg-dlg-kind">
            <option value="skills"${kindVal === "skills" ? " selected" : ""}>${escHtml(tr("settings.title.skills"))}</option>
            <option value="agents"${kindVal === "agents" ? " selected" : ""}>${escHtml(tr("settings.menu.agent"))}</option>
            <option value="both"${kindVal === "both" ? " selected" : ""}>${escHtml(tr("set.reg.kindBoth"))}</option>
            <option value="mcp"${kindVal === "mcp" ? " selected" : ""}>${escHtml(tr("settings.title.mcp"))}</option>
            <option value="a2a"${kindVal === "a2a" ? " selected" : ""}>${escHtml(tr("settings.title.a2a"))}</option>
            <option value="squads"${kindVal === "squads" ? " selected" : ""}>${escHtml(tr("subtab.squads"))}</option>
            <option value="commands"${kindVal === "commands" ? " selected" : ""}>${escHtml(tr("settings.title.user-commands"))}</option>
            <option value="permissions"${kindVal === "permissions" ? " selected" : ""}>${escHtml(tr("settings.title.permissions"))}</option>
          </select>
          <span class="registry-dialog-hint">${escHtml(tr("set.reg.kindHint"))}</span>
        </div>
        <div class="registry-dialog-field">
          <label for="reg-dlg-token">${escHtml(tr("set.reg.accessToken"))} <span class="registry-dialog-hint">${escHtml(tr("set.reg.tokenOptional"))}</span></label>
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
      cancelBtn.textContent = tr("common.cancel");

      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = isEdit ? tr("common.save") : tr("common.add");

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
        lab.setAttribute("data-tip", tr("set.tool.serpapiDisabled"));
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
      warn.textContent = tr("set.block.skillToolWarn");
      container.appendChild(warn);
    }

    if (!Array.isArray(agent.skills)) agent.skills = [];
    const selected = new Set(agent.skills);

    if (!registry.length) {
      const p = document.createElement("p"); p.className = "empty";
      p.textContent = tr("set.block.noSkills");
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
    enableAllBtn.textContent = tr("set.block.enableAll");
    enableAllBtn.addEventListener("click", () => {
      agent.skills = registry.map(s => s.name);
      onChange();
      renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange);
    });

    const disableAllBtn = document.createElement("button");
    disableAllBtn.type = "button"; disableAllBtn.className = "del-btn";
    disableAllBtn.textContent = tr("set.block.disableAll");
    disableAllBtn.addEventListener("click", async () => {
      if (!await appConfirm(tr("set.confirm.removeAllSkills", { name: agent.name }))) return;
      agent.skills = [];
      onChange();
      renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange);
    });

    actions.appendChild(enableAllBtn);
    actions.appendChild(disableAllBtn);

    const manageLink = document.createElement("button");
    manageLink.type = "button"; manageLink.className = "skills-manage-link";
    manageLink.textContent = tr("set.block.manageSkills");
    manageLink.addEventListener("click", () => {
      state.skills.editing = null;
      setActiveFile("skills");
    });
    actions.appendChild(manageLink);
    container.appendChild(actions);
  }

  // Populates a container with the agent's skill block (fetches registry async).
  async function populateAgentSkillBlock(container, agent, hasSkillsTool, onChange) {
    container.innerHTML = `<p class="settings-hint">${escHtml(tr("set.block.loadingSkills"))}</p>`;
    try {
      const regRes = await skillsGet("/skills/registry");
      const registry = regRes.skills || [];
      renderSkillBlockContent(container, agent, registry, hasSkillsTool, onChange);
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(tr("set.block.skillsUnavailable", { error: e.message }))}</p>`;
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
      warn.textContent = tr("set.block.mcpToolWarn");
      container.appendChild(warn);
    }

    if (!servers.length) {
      const p = document.createElement("p");
      p.className = "empty";
      p.textContent = tr("set.block.noMcp");
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
    enableAll.type = "button"; enableAll.className = "add-btn"; enableAll.textContent = tr("set.block.enableAll");
    enableAll.addEventListener("click", () => {
      agent.mcp_servers = servers.map(s => s.name);
      onChange();
      renderAgentMCPBlockContent(container, agent, servers, hasMCPTool, onChange);
    });

    const disableAll = document.createElement("button");
    disableAll.type = "button"; disableAll.className = "del-btn"; disableAll.textContent = tr("set.block.disableAll");
    disableAll.addEventListener("click", () => {
      agent.mcp_servers = [];
      onChange();
      renderAgentMCPBlockContent(container, agent, servers, hasMCPTool, onChange);
    });

    actions.appendChild(enableAll);
    actions.appendChild(disableAll);

    const manageLink = document.createElement("button");
    manageLink.type = "button"; manageLink.className = "skills-manage-link";
    manageLink.textContent = tr("set.block.manageMcp");
    manageLink.addEventListener("click", () => { setActiveFile("mcp"); });
    actions.appendChild(manageLink);

    container.appendChild(actions);
  }

  async function populateAgentMCPBlock(container, agent, hasMCPTool, onChange) {
    container.innerHTML = `<p class="settings-hint">${escHtml(tr("set.block.loadingMcp"))}</p>`;
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
      container.innerHTML = `<p class="settings-error">${escHtml(tr("set.block.mcpUnavailable", { error: e.message }))}</p>`;
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
      p.textContent = tr("set.block.noA2a");
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
    enableAll.type = "button"; enableAll.className = "add-btn"; enableAll.textContent = tr("set.block.enableAll");
    enableAll.addEventListener("click", () => {
      agent.a2a_agents = a2aAgents.map(ag => ag.name);
      onChange();
      renderAgentA2ABlockContent(container, agent, a2aAgents, onChange);
    });

    const disableAll = document.createElement("button");
    disableAll.type = "button"; disableAll.className = "del-btn"; disableAll.textContent = tr("set.block.disableAll");
    disableAll.addEventListener("click", () => {
      agent.a2a_agents = [];
      onChange();
      renderAgentA2ABlockContent(container, agent, a2aAgents, onChange);
    });

    actions.appendChild(enableAll);
    actions.appendChild(disableAll);

    const manageLink = document.createElement("button");
    manageLink.type = "button"; manageLink.className = "skills-manage-link";
    manageLink.textContent = tr("set.block.manageA2a");
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
    registriesHubRefresh = null;
    bodyEl.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
        <h3>${escHtml(tr("set.hdr.installedSkills"))}
          <button type="button" class="add-btn" id="skill-new">${escHtml(tr("set.skill.newBtn"))}</button>
          <label class="add-btn skill-upload-label" id="skill-upload-label" style="cursor:pointer">
            ${escHtml(tr("set.skill.uploadArchive"))}
            <input type="file" id="skill-upload-input" accept=".zip,.tar.gz,.tgz" style="display:none">
          </label>
        </h3>
        <div id="skills-list"></div>
      </section>
    `;

    renderSkillCards(host.querySelector("#skills-list"), skills);

    host.querySelector("#skill-new").addEventListener("click", async () => {
      const name = await appPrompt(tr("set.confirm.skillNamePrompt"), "my-skill");
      if (!name) return;
      const n = name.trim().toLowerCase();
      try {
        await skillsPost("/skills/registry", { name: n });
        state.skills.editing = { name: n };
        renderSkills();
      } catch (e) {
        setStatus(tr("set.status.createFailed", { error: e.message }), "error");
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
        : `<span class="skill-mkt-unlinked">${escHtml(tr("set.skill.notLinked"))}</span>`;
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
    const lines = rest.slice(0, idx).split("\n");
    let section = null;
    let i = 0;
    while (i < lines.length) {
      const line = lines[i];
      if (!line.trim()) { i++; continue; }
      const indented = line.startsWith("  ") || line.startsWith("\t");
      const col = line.indexOf(":");
      if (col < 0) { i++; continue; }
      const key = line.slice(0, col).trim();
      const val = line.slice(col + 1).trim();
      if (indented && section) {
        if (val.startsWith("[") && val.endsWith("]")) {
          result[section][key] = val.slice(1, -1).split(",").map(t => t.trim().replace(/^["']|["']$/g, "")).filter(Boolean);
        } else {
          result[section][key] = val;
        }
        i++;
      } else if (!indented) {
        if (val === ">" || val === "|") {
          let multi = "";
          i++;
          while (i < lines.length && (lines[i].startsWith("  ") || lines[i] === "")) {
            multi += lines[i].trim() + " ";
            i++;
          }
          section = null;
          result[key] = multi.trim();
        } else if (val === "") {
          section = key;
          result[key] = {};
          i++;
        } else {
          section = null;
          result[key] = val;
          i++;
        }
      } else {
        i++;
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
    bodyEl.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
          <button type="button" class="del-btn skill-del-btn">${escHtml(tr("common.delete"))}</button>
          <span class="skill-save-status"></span>
          <button type="button" class="add-btn skill-edit-btn">${escHtml(tr("common.edit"))}</button>
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
          <button type="button" class="btn-discard skill-cancel-btn">${escHtml(tr("settings.discard"))}</button>
          <span class="skill-save-status"></span>
          <button type="button" class="btn-save skill-save-btn">${escHtml(tr("common.save"))}</button>
        `;
        footer.querySelector(".skill-cancel-btn").addEventListener("click", () => setEditMode(false));
        footer.querySelector(".skill-save-btn").addEventListener("click", async () => {
          const saveBtn = footer.querySelector(".skill-save-btn");
          const status  = footer.querySelector(".skill-save-status");
          saveBtn.disabled = true; status.textContent = tr("set.skill.saving"); status.className = "skill-save-status";
          try {
            currentContent = ta.value;
            const res = await skillsPut(`/skills/registry/${name}`, { content: currentContent, mtime: currentMtime });
            currentMtime = res.mtime;
            renderFrontmatterCard(currentContent);
            status.textContent = tr("set.skill.saved"); status.className = "skill-save-status success";
            setTimeout(() => setEditMode(false), 800);
          } catch (e) {
            status.textContent = tr("set.skill.saveFailed", { error: e.message });
            status.className = "skill-save-status error";
          } finally { saveBtn.disabled = false; }
        });
      } else {
        ta.hidden = true;
        preview.hidden = false;
        renderPreview(currentContent);
        footer.innerHTML = `
          <button type="button" class="del-btn skill-del-btn">${escHtml(tr("common.delete"))}</button>
          <span class="skill-save-status"></span>
          <button type="button" class="btn-save skill-edit-btn">${escHtml(tr("common.edit"))}</button>
        `;
        footer.querySelector(".skill-edit-btn").addEventListener("click", () => setEditMode(true));
        footer.querySelector(".skill-del-btn").addEventListener("click", async () => {
          if (!await appConfirm(tr("set.confirm.deleteSkill", { name }))) return;
          try {
            await skillsDel(`/skills/registry/${name}`);
            state.skills.editing = null;
            renderSkills();
          } catch (e) {
            if (e.code === "LINKED_IN_AGENTS") {
              const agents = (e.details && e.details.agents || []).join(", ");
              if (!await appConfirm(tr("set.confirm.skillStillUsed", { name, agents }))) return;
              try { await skillsDel(`/skills/registry/${name}?force=1`); state.skills.editing = null; renderSkills(); }
              catch (e2) { setStatus(tr("set.status.deleteFailed", { error: e2.message }), "error"); }
            } else {
              setStatus(tr("set.status.deleteFailed", { error: e.message }), "error");
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
      <h3>${escHtml(tr("set.hdr.remoteRegistries"))}
        <button type="button" class="add-btn" id="remote-reg-add">${escHtml(tr("set.reg.add"))}</button>
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
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
      }
    });
  }

  async function refreshRemoteRegList(container) {
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.skills.browsingRemote = { id: r.id, name: r.name, url: r.url };
        (registriesHubRefresh || renderSkills)();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editRegistry"),
          initial: { name: r.name, url: r.url, provider: r.provider || "", hasToken: !!r.has_token },
          isEdit: true,
        });
        if (!result) return;
        try {
          await skillsPut(`/skills/remotes/${r.id}`, result);
          delete remoteSkillsCache[r.id];
          await refreshRemoteRegList(container);
        } catch (e) {
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.reg.removeConfirm", { name: r.name }))) return;
        try {
          await skillsDel(`/skills/remotes/${r.id}`);
          delete remoteSkillsCache[r.id];
          await refreshRemoteRegList(container);
        } catch (e) {
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
        }
      });
      container.appendChild(row);
    }
  }

  const remoteSkillsCache = {}; // keyed by registry ID → { skills, timestamp }
  const REMOTE_CACHE_TTL = 90 * 60 * 1000; // 90 minutes

  // Renders the grouped grid for any remote-browse view. Each non-empty
  // group is wrapped in a foldable section with an item count on the right.
  // When the view has many sections (>= COLLAPSE_GROUPS_THRESHOLD) or many
  // total items (>= COLLAPSE_ITEMS_THRESHOLD), groups start collapsed so
  // users can scan the section list before drilling in.
  const COLLAPSE_GROUPS_THRESHOLD = 3;
  const COLLAPSE_ITEMS_THRESHOLD = 20;

  function renderGroupedRemoteList({ contentEl, sortedGroups, grouped, gridClass = "skill-marketplace-grid", buildCard }) {
    const totalItems = sortedGroups.reduce((n, g) => n + grouped.get(g).length, 0);
    const namedGroups = sortedGroups.filter(g => g !== "").length;
    const startCollapsed = namedGroups >= COLLAPSE_GROUPS_THRESHOLD || totalItems >= COLLAPSE_ITEMS_THRESHOLD;

    for (const group of sortedGroups) {
      const items = grouped.get(group);
      if (!group) {
        const grid = document.createElement("div");
        grid.className = gridClass;
        for (const it of items) grid.appendChild(buildCard(it));
        contentEl.appendChild(grid);
        continue;
      }

      const section = document.createElement("section");
      section.className = "remote-group-section" + (startCollapsed ? " collapsed" : "");

      const header = document.createElement("button");
      header.type = "button";
      header.className = "remote-group-header foldable";
      header.setAttribute("aria-expanded", startCollapsed ? "false" : "true");
      header.innerHTML = `
        <span class="remote-group-caret" aria-hidden="true">▾</span>
        <span class="remote-group-label">${escHtml(group.replace(/\//g, " › "))}</span>
        <span class="remote-group-count">${items.length}</span>
      `;

      const grid = document.createElement("div");
      grid.className = gridClass + " remote-group-body";
      for (const it of items) grid.appendChild(buildCard(it));

      header.addEventListener("click", () => {
        const collapsed = section.classList.toggle("collapsed");
        header.setAttribute("aria-expanded", collapsed ? "false" : "true");
      });

      section.appendChild(header);
      section.appendChild(grid);
      contentEl.appendChild(section);
    }
  }

  async function renderRemoteBrowseView(host = bodyEl) {
    // Render into `host` (the full settings body by default, or the Registries
    // hub's right panel when mounted there). Shadowing keeps the existing
    // bodyEl-based DOM queries below working against the chosen container.
    const bodyEl = host;
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
      (registriesHubRefresh || renderSkills)();
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
        empty.textContent = tr("set.reg.empty.skills");
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
          ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
          : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`;

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
            installBtn.textContent = tr("set.reg.installing");
            try {
              const res = await skillsPost(`/skills/remotes/${id}/install/${sk.dir_path}`, {});
              installBtn.outerHTML = `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`;
              sk.installed = true;
              setStatus(`Skill "${res.name}" installed successfully.`, "success");
            } catch (e) {
              installBtn.disabled = false;
              installBtn.textContent = tr("common.install");
              setStatus(tr("set.reg.installFailed", { error: e.message }), "error");
            }
          });
        }

        card.addEventListener("click", e => {
          if (e.target.closest(".remote-install-btn")) return;
          state.skills.viewingRemote = { ...state.skills.browsingRemote, skill: sk };
          (registriesHubRefresh || renderSkills)();
        });

        return card;
      }

      renderGroupedRemoteList({
        contentEl,
        sortedGroups,
        grouped,
        buildCard: buildSkillCard,
      });
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

  async function renderRemoteSkillDetailView(host = bodyEl) {
    // See renderRemoteBrowseView: shadow bodyEl so the detail view can render
    // into the Registries hub's right panel as well as the full settings body.
    const bodyEl = host;
    const { id, name, skill } = state.skills.viewingRemote;
    bodyEl.innerHTML = `
      <div class="settings-form skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to ${escHtml(name)}</button>
        </div>
        <div class="skill-frontmatter-card" id="skill-fm-card">
          <p class="settings-loading">${escHtml(tr("set.loading"))}</p>
        </div>
        <div class="skill-content-wrap">
          <div class="skill-md-preview markdown-body"></div>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${skill.installed
            ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
            : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`}
        </div>
      </div>
    `;

    bodyEl.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.skills.viewingRemote = null;
      (registriesHubRefresh || renderSkills)();
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
        installBtn.textContent = tr("set.reg.installing");
        const statusEl = bodyEl.querySelector(".skill-save-status");
        try {
          const res = await skillsPost(`/skills/remotes/${id}/install/${skill.dir_path}`, {});
          installBtn.outerHTML = `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`;
          skill.installed = true;
          setStatus(`Skill "${res.name}" installed successfully.`, "success");
        } catch (e) {
          installBtn.disabled = false;
          installBtn.textContent = tr("common.install");
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
      for (const k of [{ id: "agents", label: tr("settings.menu.agent") }, { id: "squads", label: tr("subtab.squads") }]) {
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
      const result = await appRegistryDialog({ title: tr("set.reg.title.addAgent"), defaultKind: "agents" });
      if (!result) return;
      try {
        await skillsPost("/agents/remotes", result);
        if (onRefresh) await onRefresh(); else await refreshAgentRemoteList(container);
      } catch (e) {
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
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
      const result = await appRegistryDialog({ title: tr("set.reg.title.addSquad"), defaultKind: "squads" });
      if (!result) return;
      try {
        await skillsPost("/squads-registry/remotes", result);
        if (onRefresh) await onRefresh(); else await refreshSquadRemoteList(container);
      } catch (e) {
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
      }
    });
  }

  async function refreshAgentRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
      const kindBadge = r.kind === "both" ? ` <span class="remote-reg-provider">${escHtml(tr("set.reg.both"))}</span>` : "";
      const row = document.createElement("div");
      row.className = "remote-reg-row";
      row.innerHTML = `
        <div class="remote-reg-info">
          <span class="remote-reg-name">${escHtml(r.name)}${providerLabel ? ` <span class="remote-reg-provider">${escHtml(providerLabel)}</span>` : ""}${kindBadge}</span>
          <span class="remote-reg-url">${escHtml(r.url)}</span>
        </div>
        <div class="remote-reg-actions">
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.agentRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editAgent"),
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
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
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
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
        }
      });
      container.appendChild(row);
    }
  }

  // remoteAgentMetaRowsHtml renders tools / skills / mcp_servers / model
  // declared in the agent's manifest as small chip rows beneath the
  // description. Each row is omitted when its list is empty.
  function remoteAgentMetaRowsHtml(a) {
    function chipRow(label, items, extraClass = "") {
      if (!Array.isArray(items) || !items.length) return "";
      const chips = items.map(t => `<span class="remote-agent-chip ${extraClass}">${escHtml(String(t))}</span>`).join("");
      return `<div class="remote-agent-meta-row"><span class="remote-agent-meta-label">${escHtml(label)}</span><span class="remote-agent-meta-chips">${chips}</span></div>`;
    }
    let html = "";
    html += chipRow("tools", a.tools);
    html += chipRow("skills", a.skills);
    html += chipRow("mcp", a.mcp_servers, "remote-agent-chip-mcp");
    if (a.model) {
      html += `<div class="remote-agent-meta-row"><span class="remote-agent-meta-label">model</span><span class="remote-agent-meta-chips"><span class="remote-agent-chip remote-agent-chip-model">${escHtml(String(a.model))}</span></span></div>`;
    }
    return html;
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
        empty.textContent = tr("set.reg.empty.agents");
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
          ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
          : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`;
        const metaRows = remoteAgentMetaRowsHtml(a);

        card.innerHTML = `
          <div class="skill-mkt-header">
            <span class="skill-mkt-filename">${escHtml(a.name)}</span>
            ${actionHtml}
          </div>
          <div class="skill-mkt-body">
            ${builtinHtml}
            <p class="skill-mkt-desc">${escHtml(a.description || "(no description)")}</p>
            ${metaRows}
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

      renderGroupedRemoteList({
        contentEl,
        sortedGroups,
        grouped,
        buildCard: buildAgentCard,
      });
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
          <p class="settings-loading">${escHtml(tr("set.loading"))}</p>
        </div>
        <div class="skill-content-wrap">
          <div class="skill-md-preview markdown-body" id="agent-json-preview"></div>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${agent.installed
            ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
            : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`}
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

    function fmScalarRow(key, val) {
      return `<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(key)}</span><span class="skill-fm-value">${escHtml(String(val))}</span></div>`;
    }
    function fmTagRow(key, items) {
      const tags = items.map(t => `<span class="skill-mkt-tag">${escHtml(String(t))}</span>`).join("");
      return `<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(key)}</span><span class="skill-fm-value skill-fm-tags">${tags}</span></div>`;
    }
    // fmToList accepts either an array, a comma-separated string ("Read, Write"),
    // or a single scalar and normalises everything to a trimmed []string. This
    // matters for Claude Code–style frontmatter where `tools:` is typically a
    // comma list, while `skills:`/`mcpServers:` are usually YAML sequences.
    function fmToList(val) {
      if (Array.isArray(val)) return val.map(v => String(v).trim()).filter(Boolean);
      if (typeof val === "string") {
        return val.split(",").map(s => s.trim()).filter(Boolean);
      }
      return [];
    }

    if (isMarkdown) {
      // Claude Code markdown format: parse YAML frontmatter for the card,
      // render the body as markdown.
      const fm = parseAgentFrontmatter(content);
      if (fm) {
        const rows = [];
        if (fm.name) rows.push(fmScalarRow("name", fm.name));
        if (fm.description) rows.push(fmScalarRow("description", fm.description));
        if (fm.model) rows.push(fmScalarRow("model", fm.model));
        for (const listKey of ["tools", "skills", "mcpServers"]) {
          const items = fmToList(fm[listKey]);
          if (items.length) rows.push(fmTagRow(listKey, items));
        }
        fmCard.innerHTML = rows.join("") || "";
      } else {
        fmCard.innerHTML = "";
      }
      preview.innerHTML = marked.parse(stripFrontmatter(content), { breaks: false, gfm: true });
    } else {
      // Native omnis JSON format: populate card from parsed fields, show raw JSON.
      let parsed = null;
      try { parsed = JSON.parse(content); } catch (_) { parsed = null; }
      if (parsed && typeof parsed === "object") {
        const rows = [];
        for (const k of ["name", "description", "model_ref", "model", "builtin", "leader"]) {
          if (parsed[k] === undefined) continue;
          rows.push(fmScalarRow(k, parsed[k]));
        }
        for (const [label, jsonKey] of [["tools", "tools"], ["skills", "skills"], ["mcp_servers", "mcp_servers"]]) {
          const items = fmToList(parsed[jsonKey]);
          if (items.length) rows.push(fmTagRow(label, items));
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
    if (btn) { btn.disabled = true; btn.textContent = tr("set.reg.installing"); }
    try {
      const res = await skillsPost(`/agents/remotes/${registryID}/install/${agentInfo.dir_path}`, { enable });
      agentInfo.installed = true;
      if (btn) btn.outerHTML = `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`;
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
      if (btn) { btn.disabled = false; btn.textContent = tr("common.install"); }
      setStatus(tr("set.reg.installFailed", { error: e.message }), "error");
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
            Files will be written to <code>$OMNIS_HOME/registry/agents/${escHtml(agentInfo.name)}/</code>.
          </p>
          <label class="registry-dialog-toggle" for="agent-install-enable">
            <span>${tr("set.agent.enableInConfig")}</span>
            <input type="checkbox" id="agent-install-enable" checked />
          </label>
          <p class="registry-dialog-hint">
            Adds the agent's name to the enabled list so the next reload wires it in.
            Leave unchecked to install on disk only — you can enable later from the Agents tab.
          </p>
        </div>
        <div class="app-dialog-actions">
          <button type="button" id="agent-install-cancel">${escHtml(tr("common.cancel"))}</button>
          <button type="button" class="btn-primary" id="agent-install-ok">${escHtml(tr("common.install"))}</button>
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
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.squadRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        refreshRemotesRightFn ? refreshRemotesRightFn() : renderAgentForm();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editSquad"),
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
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.confirm.removeSquadRegistry", { name: r.name }))) return;
        try {
          await skillsDel(`/squads-registry/remotes/${r.id}`);
          delete remoteSquadsCache[r.id];
          await refreshSquadRemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
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
        empty.textContent = tr("set.reg.empty.squads");
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

      function buildSquadCard(sq) {
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
              ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
              : `<button type="button" class="add-btn squad-install-btn">${escHtml(tr("common.install"))}</button>`}
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
        return card;
      }

      renderGroupedRemoteList({
        contentEl,
        sortedGroups,
        grouped,
        buildCard: buildSquadCard,
      });
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
    if (!await appConfirm(tr("set.confirm.installSquad", { name: squadInfo.name }))) return;

    btn.disabled = true;
    btn.textContent = tr("set.reg.installing");
    try {
      const res = await skillsPost(`/squads-registry/remotes/${registryID}/install/${squadInfo.dir_path}`, {});
      squadInfo.installed = true;
      btn.outerHTML = `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`;
      await doReload();
      // Refresh the in-memory agent config so the new squad is visible when
      // the user navigates to the squads sub-tab (no forced redirect).
      await loadParsed("agent");
      showInstallResult(`Squad "${res.name}" ${res.added ? "added" : "updated"} in config/agents.json.`, res.warnings);
      delete remoteSquadsCache[registryID];
    } catch (e) {
      btn.disabled = false;
      btn.textContent = tr("common.install");
      setStatus(tr("set.reg.installFailed", { error: e.message }), "error");
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
      titleEl.textContent = tr("set.agent.importTitle");
      box.appendChild(titleEl);

      const hint = document.createElement("p");
      hint.className = "settings-hint";
      hint.style.margin = "0 0 6px";
      hint.innerHTML = tr("set.agent.importHint");
      box.appendChild(hint);

      const fileRow = document.createElement("div");
      fileRow.style.cssText = "display:flex;gap:6px;margin-bottom:6px;";
      const fileInput = document.createElement("input");
      fileInput.type = "file";
      fileInput.accept = ".md,.json,text/plain,application/json";
      fileInput.style.display = "none";
      const browseBtn = document.createElement("button");
      browseBtn.type = "button";
      browseBtn.textContent = tr("set.agent.browse");
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
      enableLabel.innerHTML = tr("set.agent.enableInConfigImport");
      enableRow.appendChild(enableLabel);
      enableRow.appendChild(enableCheck);
      box.appendChild(enableRow);

      const actions = document.createElement("div");
      actions.className = "app-dialog-actions";
      const cancelBtn = document.createElement("button");
      cancelBtn.type = "button";
      cancelBtn.textContent = tr("common.cancel");
      const okBtn = document.createElement("button");
      okBtn.type = "button";
      okBtn.className = "btn-primary";
      okBtn.textContent = tr("common.import");

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
        <h3>${escHtml(tr("set.hdr.remoteMcpRegistries"))}
          <button type="button" class="add-btn" id="mcp-remote-add">${escHtml(tr("set.reg.add"))}</button>
        </h3>
        <div id="mcp-remote-list"></div>
      </section>
    `;
    const listEl = host.querySelector("#mcp-remote-list");
    await refreshMCPRemoteList(listEl);

    host.querySelector("#mcp-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({
        title: tr("set.reg.title.addMcp"),
        defaultKind: "mcp",
      });
      if (!result) return;
      try {
        await skillsPost("/mcp/remotes", result);
        await refreshMCPRemoteList(listEl);
      } catch (e) {
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
      }
    });
  }

  async function refreshMCPRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.mcpRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        (registriesHubRefresh || renderMCPForm)();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editMcp"),
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
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.reg.removeConfirm", { name: r.name }))) return;
        try {
          await skillsDel(`/mcp/remotes/${r.id}`);
          delete remoteMCPCache[r.id];
          await refreshMCPRemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
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
      (registriesHubRefresh || renderMCPForm)();
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
        empty.textContent = tr("set.reg.empty.mcp");
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

      function buildMCPCard(tool) {
        const typeLabel = tool.type
          ? `<span class="skill-mkt-tag">${escHtml(tool.type)}</span>`
          : "";
        const actionHtml = tool.installed
          ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
          : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`;
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
            (registriesHubRefresh || renderMCPForm)();
          });
        }
        const btn = card.querySelector(".remote-install-btn");
        if (btn) {
          btn.addEventListener("click", async () => {
            btn.disabled = true;
            btn.textContent = tr("set.reg.installing");
            try {
              const res = await skillsPost(`/mcp/remotes/${id}/install/${tool.dir_path}`, {});
              tool.installed = true;
              btn.replaceWith(Object.assign(document.createElement("span"), {
                className: "remote-skill-installed-badge",
                textContent: tr("set.reg.installed"),
              }));
              showInstallResult(`MCP server "${res.name}" added to mcp_config.json. Reload to apply.`, res.warnings);
              showBanner();
              delete remoteMCPCache[id];
              delete state.parsed["mcp"];
            } catch (e) {
              btn.disabled = false;
              btn.textContent = tr("common.install");
              setStatus(tr("set.reg.installFailed", { error: e.message }), "error");
            }
          });
        }
        return card;
      }

      renderGroupedRemoteList({
        contentEl,
        sortedGroups,
        grouped,
        buildCard: buildMCPCard,
      });
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
        setStatus(tr("set.reg.refreshFailed", { error: e.message }), "error");
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
            ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
            : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`}
        </div>
      </div>
    `;

    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.mcpRemotes.viewing = null;
      (registriesHubRefresh || renderMCPForm)();
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
        installBtn.textContent = tr("set.reg.installing");
        try {
          const res = await skillsPost(`/mcp/remotes/${id}/install/${tool.dir_path}`, {});
          tool.installed = true;
          installBtn.outerHTML = `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`;
          showInstallResult(`MCP server "${res.name}" added to mcp_config.json. Reload to apply.`, res.warnings);
          showBanner();
          delete remoteMCPCache[id];
          delete state.parsed["mcp"];
        } catch (e) {
          installBtn.disabled = false;
          installBtn.textContent = tr("common.install");
          setStatus(tr("set.reg.installFailed", { error: e.message }), "error");
        }
      });
    }
  }

  // ─── A2A ──────────────────────────────────────────────────────────────

  const remoteA2ACache = {}; // keyed by registry ID → { agents, timestamp }

  function renderA2AForm() {
    registriesHubRefresh = null;
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
        <h3>${escHtml(tr("set.hdr.inputs"))}</h3>
        <p class="settings-hint">${tr("set.a2a.inputsHint")}</p>
        <div id="a2a-inputs"></div>
      </section>
      <section class="form-section">
        <h3>${escHtml(tr("settings.title.a2a"))}</h3>
        <p class="settings-hint">${tr("set.a2a.endpointsHint")}</p>
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
      chip.setAttribute("data-tip", inp.description || inp.id || "");

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
      close.setAttribute("aria-label", tr("set.mcp.removeInput"));
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
    addChip.innerHTML = `<span class="mcp-input-chip-icon">+</span><span class="mcp-input-chip-label">${escHtml(tr("set.mcp.addInputChip"))}</span>`;
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
      empty.textContent = tr("set.a2a.noInputs");
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
          <button type="button" class="del-btn a2a-remove">${escHtml(tr("common.delete"))}</button>
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
        b.setAttribute("aria-label", tr("common.remove"));
        b.innerHTML = TRASH_ICON_SVG;
        b.addEventListener("click", onClick);
        return b;
      }

      // ── General Settings ────────────────────────────────────────────
      const general = a2aSection(tr("set.hdr.generalSettings"));
      const generalGrid = document.createElement("div");
      generalGrid.className = "model-field-grid";
      generalGrid.appendChild(a2aField(tr("common.name"), currentName, v => {
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
      const conn = a2aSection(tr("set.mcp.connection"));
      const connGrid = document.createElement("div");
      connGrid.className = "model-field-grid";
      connGrid.appendChild(a2aField(tr("set.mcp.url"), a.url, v => { a.url = v; markFormDirty("a2a"); }, {
        full: true,
        placeholder: "https://agent.example.com/",
      }));
      connGrid.appendChild(a2aField(tr("common.description"), a.description, v => { a.description = v; markFormDirty("a2a"); }, {
        full: true,
        placeholder: tr("set.a2a.optionalDescription"),
      }));
      conn.appendChild(connGrid);
      body.appendChild(general);
      body.appendChild(conn);

      // ── Routing ─────────────────────────────────────────────────────
      const routing = a2aSection(tr("set.a2a.routing"));
      const routingGrid = document.createElement("div");
      routingGrid.className = "model-field-grid";
      routingGrid.appendChild(a2aField("Squad", a.squad, v => { a.squad = v || undefined; markFormDirty("a2a"); }, {
        placeholder: tr("set.a2a.squadPlaceholder"),
      }));
      routingGrid.appendChild(a2aField(tr("set.a2a.sessionName"), a.session_name, v => { a.session_name = v || undefined; markFormDirty("a2a"); }, {
        placeholder: tr("set.a2a.sessionPlaceholder"),
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
      createSpan.textContent = tr("set.a2a.createSession");
      createLabel.appendChild(createChk);
      createLabel.appendChild(createSpan);
      createRow.appendChild(createLabel);
      routing.appendChild(createRow);
      body.appendChild(routing);

      // ── Headers ─────────────────────────────────────────────────────
      const headersSec = a2aSection(tr("set.mcp.headers"));
      const headersGrid = document.createElement("div");
      headersGrid.className = "mcp-kv-grid";
      const headersHdr = document.createElement("div");
      headersHdr.className = "mcp-kv-headers";
      headersHdr.innerHTML = `
        <span class="model-field-label model-field-label--title">${escHtml(tr("set.kv.key"))}</span>
        <span class="model-field-label model-field-label--title">${escHtml(tr("set.kv.value"))}</span>
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
      headersSec.appendChild(a2aAddButton(tr("set.mcp.addHeader"), async () => {
        let nk = await appPrompt(tr("set.confirm.headerNamePrompt"));
        if (!nk) return;
        nk = nk.trim();
        if (!nk || nk in store) return;
        store[nk] = "";
        markFormDirty("a2a"); drawHeaders();
      }));
      body.appendChild(headersSec);

      card.querySelector(".a2a-remove").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.confirm.removeAgent", { name: currentName }))) return;
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
      <span class="model-card-empty-label">${escHtml(tr("set.a2a.addAgent"))}</span>
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
        <h3>${escHtml(tr("set.hdr.remoteA2aRegistries"))}
          <button type="button" class="add-btn" id="a2a-remote-add">${escHtml(tr("set.reg.add"))}</button>
        </h3>
        <div id="a2a-remote-list"></div>
      </section>
    `;
    const listEl = host.querySelector("#a2a-remote-list");
    await refreshA2ARemoteList(listEl);

    host.querySelector("#a2a-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({
        title: tr("set.reg.title.addA2a"),
        defaultKind: "a2a",
      });
      if (!result) return;
      try {
        await skillsPost("/a2a/remotes", result);
        await refreshA2ARemoteList(listEl);
      } catch (e) {
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
      }
    });
  }

  async function refreshA2ARemoteList(container) {
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
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
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.a2aRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        (registriesHubRefresh || renderA2AForm)();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editA2a"),
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
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.reg.removeConfirm", { name: r.name }))) return;
        try {
          await skillsDel(`/a2a/remotes/${r.id}`);
          delete remoteA2ACache[r.id];
          await refreshA2ARemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
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
      (registriesHubRefresh || renderA2AForm)();
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
        empty.textContent = tr("set.reg.empty.a2a");
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

      function buildA2ACard(agent) {
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
              ? `<span class="skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
              : `<button type="button" class="add-btn a2a-install-btn" data-dir="${escHtml(agent.dir_path)}">${escHtml(tr("common.install"))}</button>`}
          </div>
        `;
        const btn = card.querySelector(".a2a-install-btn");
        if (btn) {
          btn.addEventListener("click", async () => {
            btn.disabled = true;
            btn.textContent = tr("set.reg.installing");
            try {
              const res = await skillsPost(`/a2a/remotes/${id}/install/${agent.dir_path}`, {});
              agent.installed = true;
              card.classList.add("skill-installed");
              btn.replaceWith(Object.assign(document.createElement("span"), {
                className: "skill-installed-badge",
                textContent: tr("set.reg.installed"),
              }));
              setStatus(`A2A agent "${res.name}" added to a2a_config.json. Reload to apply.`, "success");
              showBanner();
              delete remoteA2ACache[id];
            } catch (e) {
              btn.disabled = false;
              btn.textContent = tr("common.install");
              setStatus(tr("set.reg.installFailed", { error: e.message }), "error");
            }
          });
        }
        return card;
      }

      renderGroupedRemoteList({
        contentEl,
        sortedGroups,
        grouped,
        gridClass: "remote-skill-grid",
        buildCard: buildA2ACard,
      });
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
        setStatus(tr("set.reg.refreshFailed", { error: e.message }), "error");
      }
    }
  }

  // ─── Commands — remote registries ─────────────────────────────────────
  //
  // Mirrors the MCP / A2A "Remote registries" section. Backed by
  // /api/commands/remotes/* on the server. Each entry in a remote
  // registry is a Claude Code-style slash-command markdown file
  // (one .md per command). Installing merges it into user_commands.json.

  const remoteCommandsCache = {}; // keyed by registry ID → { commands, timestamp }

  async function renderCommandsRemotesSection(host) {
    if (state.commandsRemotes.viewing) {
      await renderCommandsRemoteCommandView(host);
      return;
    }
    if (state.commandsRemotes.browsing) {
      await renderCommandsRemoteBrowseView(host);
      return;
    }

    host.innerHTML = `
      <section class="form-section">
        <h3>${escHtml(tr("set.hdr.remoteCommandRegistries"))}
          <button type="button" class="add-btn" id="cmd-remote-add">${escHtml(tr("set.reg.add"))}</button>
        </h3>
        <p class="settings-hint">
          ${tr("set.cmd.remoteHint")}
        </p>
        <div id="cmd-remote-list"></div>
      </section>
    `;
    const listEl = host.querySelector("#cmd-remote-list");
    await refreshCommandsRemoteList(listEl);

    host.querySelector("#cmd-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({
        title: tr("set.reg.title.addCommand"),
        defaultKind: "commands",
      });
      if (!result) return;
      try {
        await skillsPost("/commands/remotes", result);
        await refreshCommandsRemoteList(listEl);
      } catch (e) {
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
      }
    });
  }

  async function refreshCommandsRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
    let remotes;
    try {
      const res = await skillsGet("/commands/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote command registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install slash commands.</p>`;
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
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.commandsRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        (registriesHubRefresh || renderUserCommands)();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editCommand"),
          initial: { name: r.name, url: r.url, provider: r.provider || "", kind: r.kind, hasToken: !!r.has_token },
          isEdit: true,
          defaultKind: "commands",
        });
        if (!result) return;
        try {
          await skillsPut(`/commands/remotes/${r.id}`, result);
          delete remoteCommandsCache[r.id];
          await refreshCommandsRemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.reg.removeConfirm", { name: r.name }))) return;
        try {
          await skillsDel(`/commands/remotes/${r.id}`);
          delete remoteCommandsCache[r.id];
          await refreshCommandsRemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
        }
      });
      container.appendChild(row);
    }
  }

  async function renderCommandsRemoteBrowseView(host) {
    const { id, name } = state.commandsRemotes.browsing;
    const cached = remoteCommandsCache[id];
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
            <p class="settings-hint">Scanning the repository tree for command markdown files. This may take a moment.</p>
          </div>
        ` : ""}
        <div id="cmd-remote-browse-content"></div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.commandsRemotes.browsing = null;
      (registriesHubRefresh || renderUserCommands)();
    });

    const contentEl = host.querySelector("#cmd-remote-browse-content");

    function populateContent(commands) {
      contentEl.innerHTML = "";

      const truncated = commands.some(c => c.dir_path === "__truncated__");
      const real = commands.filter(c => c.dir_path !== "__truncated__");

      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${real.length} command${real.length !== 1 ? "s" : ""}${truncated ? " (tree truncated — some entries may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!real.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = tr("set.reg.empty.commands");
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const c of real) {
        const g = c.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(c);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1; if (b === "") return 1;
        return a.localeCompare(b);
      });

      function buildCommandCard(cmd) {
        const card = document.createElement("div");
        card.className = "skill-mkt-card remote-skill-card";

        const actionHtml = cmd.installed
          ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
          : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`;
        const argHintRow = cmd.argument_hint
          ? `<div class="remote-agent-meta-row"><span class="remote-agent-meta-label">args</span><span class="remote-agent-meta-chips"><span class="remote-agent-chip">${escHtml(cmd.argument_hint)}</span></span></div>`
          : "";

        card.innerHTML = `
          <div class="skill-mkt-header">
            <span class="skill-mkt-filename">/${escHtml(cmd.name)}</span>
            ${actionHtml}
          </div>
          <div class="skill-mkt-body">
            <p class="skill-mkt-desc">${escHtml(cmd.description || "(no description)")}</p>
            ${argHintRow}
          </div>
        `;

        if (!cmd.installed) {
          card.querySelector(".remote-install-btn").addEventListener("click", e => {
            e.stopPropagation();
            doInstallCommand(id, cmd, card);
          });
        }

        card.addEventListener("click", e => {
          if (e.target.closest(".remote-install-btn")) return;
          state.commandsRemotes.viewing = { ...state.commandsRemotes.browsing, command: cmd };
          (registriesHubRefresh || renderUserCommands)();
        });

        return card;
      }

      renderGroupedRemoteList({
        contentEl,
        sortedGroups,
        grouped,
        buildCard: buildCommandCard,
      });
    }

    if (hasCached) {
      populateContent(cached.commands);
    }

    try {
      const res = await skillsGet(`/commands/remotes/${id}/browse`);
      const commands = res.commands || [];
      remoteCommandsCache[id] = { commands, timestamp: Date.now() };
      host.querySelector(".remote-browse-refresh-badge")?.setAttribute("hidden", "");
      host.querySelector(".remote-browse-loading")?.remove();
      populateContent(commands);
    } catch (e) {
      if (!hasCached) {
        contentEl.innerHTML = `<p class="settings-error">Failed to browse registry: ${escHtml(e.message)}</p>`;
      } else {
        setStatus(tr("set.reg.refreshFailed", { error: e.message }), "error");
      }
    }
  }

  async function renderCommandsRemoteCommandView(host) {
    const { id, name, command } = state.commandsRemotes.viewing;
    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to ${escHtml(name)}</button>
        </div>
        <div class="skill-frontmatter-card" id="cmd-fm-card">
          <p class="settings-loading">${escHtml(tr("set.loading"))}</p>
        </div>
        <div class="skill-content-wrap">
          <div class="skill-md-preview markdown-body" id="cmd-md-preview"></div>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${command.installed
            ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
            : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`}
        </div>
      </div>
    `;

    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.commandsRemotes.viewing = null;
      (registriesHubRefresh || renderUserCommands)();
    });

    const fmCard = host.querySelector("#cmd-fm-card");
    const preview = host.querySelector("#cmd-md-preview");

    let content;
    try {
      const res = await skillsGet(`/commands/remotes/${id}/command/${command.dir_path}`);
      content = res.content || "";
    } catch (e) {
      fmCard.innerHTML = "";
      preview.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }

    // Render frontmatter as scalar rows (name / description / argument-hint),
    // mirroring the agent detail view's parseAgentFrontmatter + fmScalarRow pattern.
    function scalarRow(key, val) {
      return `<div class="skill-fm-row"><span class="skill-fm-key">${escHtml(key)}</span><span class="skill-fm-value">${escHtml(String(val))}</span></div>`;
    }
    const fm = parseAgentFrontmatter(content) || {};
    const displayName = fm.name || command.name;
    const rows = [];
    rows.push(scalarRow("name", "/" + displayName));
    if (fm.description) rows.push(scalarRow("description", fm.description));
    else if (command.description) rows.push(scalarRow("description", command.description));
    const argHint = fm["argument-hint"] || fm.argument_hint || command.argument_hint;
    if (argHint) rows.push(scalarRow("argument-hint", argHint));
    fmCard.innerHTML = rows.join("");

    // The body (after frontmatter) is the prompt template — render as markdown.
    const body = stripFrontmatter(content);
    if (body.trim()) {
      preview.innerHTML = marked.parse(body, { breaks: false, gfm: true });
    } else {
      preview.innerHTML = `<p class="empty">(empty prompt body)</p>`;
    }

    const installBtn = host.querySelector(".remote-install-btn");
    if (installBtn) {
      installBtn.addEventListener("click", () => {
        doInstallCommand(id, command, host, installBtn);
      });
    }
  }

  // doInstallCommand posts the install and swaps the button/card to the
  // "Installed" state in place. cardOrHost is the surrounding card element
  // (browse view) or the detail-view host; installBtn is supplied only by
  // the detail view so we know which button to replace.
  async function doInstallCommand(registryID, command, cardOrHost, installBtn) {
    const btn = installBtn || cardOrHost.querySelector(".remote-install-btn");
    if (btn) { btn.disabled = true; btn.textContent = tr("set.reg.installing"); }
    try {
      const res = await skillsPost(`/commands/remotes/${registryID}/install/${command.dir_path}`, {});
      command.installed = true;
      if (btn) {
        btn.replaceWith(Object.assign(document.createElement("span"), {
          className: "remote-skill-installed-badge",
          textContent: tr("set.reg.installed"),
        }));
      }
      const verb = res.added ? "added" : "updated";
      setStatus(`Command "/${res.name}" ${verb}.`, "success");
      // Keep the in-memory UserCommands cache in sync so the composer's
      // slash menu and the User tab pick the new command up immediately.
      if (window.UserCommands && typeof window.UserCommands.refresh === "function") {
        window.UserCommands.refresh();
      }
      delete remoteCommandsCache[registryID];
    } catch (err) {
      if (btn) {
        btn.disabled = false;
        btn.textContent = tr("common.install");
      }
      setStatus(tr("set.reg.installFailed", { error: err.message }), "error");
    }
  }

  // ─── Permissions remotes ───────────────────────────────────────────────
  // Mirrors the commands remotes section but for permission rule-sets, backed
  // by /api/permissions-registry/remotes/* on the server. Each entry in a
  // remote registry is a directory containing a permissions.json; installing
  // merges its rules into the user's permissions.json (deduped by pattern).

  const remotePermissionsCache = {}; // keyed by registry ID → { permissions, timestamp }

  async function renderPermissionsRemotesSection(host) {
    if (state.permissionsRemotes.viewing) {
      await renderPermissionsRemoteDetailView(host);
      return;
    }
    if (state.permissionsRemotes.browsing) {
      await renderPermissionsRemoteBrowseView(host);
      return;
    }

    host.innerHTML = `
      <section class="form-section">
        <h3>${escHtml(tr("set.hdr.remotePermissionRegistries"))}
          <button type="button" class="add-btn" id="perm-remote-add">${escHtml(tr("set.reg.add"))}</button>
        </h3>
        <p class="settings-hint">
          ${tr("set.perm.remoteHint")}
        </p>
        <div id="perm-remote-list"></div>
      </section>
    `;
    const listEl = host.querySelector("#perm-remote-list");
    await refreshPermissionsRemoteList(listEl);

    host.querySelector("#perm-remote-add").addEventListener("click", async () => {
      const result = await appRegistryDialog({
        title: tr("set.reg.title.addPermission"),
        defaultKind: "permissions",
      });
      if (!result) return;
      try {
        await skillsPost("/permissions-registry/remotes", result);
        await refreshPermissionsRemoteList(listEl);
      } catch (e) {
        setStatus(tr("set.reg.addFailed", { error: e.message }), "error");
      }
    });
  }

  async function refreshPermissionsRemoteList(container) {
    container.innerHTML = `<p class="settings-loading">${escHtml(tr("set.loading"))}</p>`;
    let remotes;
    try {
      const res = await skillsGet("/permissions-registry/remotes");
      remotes = res.remotes || [];
    } catch (e) {
      container.innerHTML = `<p class="settings-error">${escHtml(e.message)}</p>`;
      return;
    }
    if (!remotes.length) {
      container.innerHTML = `<p class="empty">No remote permission registries configured. Add a GitHub, GitLab, or Gitea repository to browse and install permission rule-sets.</p>`;
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
          <button type="button" class="add-btn remote-browse-btn">${escHtml(tr("set.reg.browse"))}</button>
          <button type="button" class="edit-btn remote-edit-btn">${escHtml(tr("common.edit"))}</button>
          <button type="button" class="del-btn remote-remove-btn">${escHtml(tr("common.remove"))}</button>
        </div>
      `;
      row.querySelector(".remote-browse-btn").addEventListener("click", () => {
        state.permissionsRemotes.browsing = { id: r.id, name: r.name, url: r.url };
        (registriesHubRefresh || (() => renderPermissionsRemotesSection(container.parentElement)))();
      });
      row.querySelector(".remote-edit-btn").addEventListener("click", async () => {
        const result = await appRegistryDialog({
          title: tr("set.reg.title.editPermission"),
          initial: { name: r.name, url: r.url, provider: r.provider || "", kind: r.kind, hasToken: !!r.has_token },
          isEdit: true,
          defaultKind: "permissions",
        });
        if (!result) return;
        try {
          await skillsPut(`/permissions-registry/remotes/${r.id}`, result);
          delete remotePermissionsCache[r.id];
          await refreshPermissionsRemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.updateFailed", { error: e.message }), "error");
        }
      });
      row.querySelector(".remote-remove-btn").addEventListener("click", async () => {
        if (!await appConfirm(tr("set.reg.removeConfirm", { name: r.name }))) return;
        try {
          await skillsDel(`/permissions-registry/remotes/${r.id}`);
          delete remotePermissionsCache[r.id];
          await refreshPermissionsRemoteList(container);
        } catch (e) {
          setStatus(tr("set.reg.removeFailed", { error: e.message }), "error");
        }
      });
      container.appendChild(row);
    }
  }

  async function renderPermissionsRemoteBrowseView(host) {
    const { id, name } = state.permissionsRemotes.browsing;
    const cached = remotePermissionsCache[id];
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
            <p class="settings-hint">Scanning the repository tree for permission rule-sets. This may take a moment.</p>
          </div>
        ` : ""}
        <div id="perm-remote-browse-content"></div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.permissionsRemotes.browsing = null;
      (registriesHubRefresh || (() => renderPermissionsRemotesSection(host)))();
    });

    const contentEl = host.querySelector("#perm-remote-browse-content");

    function populateContent(perms) {
      contentEl.innerHTML = "";
      const truncated = perms.some(p => p.dir_path === "__truncated__");
      const real = perms.filter(p => p.dir_path !== "__truncated__");

      const hdr = document.createElement("div");
      hdr.className = "remote-browse-header";
      hdr.innerHTML = `
        <span class="remote-browse-title">${escHtml(name)}</span>
        <span class="remote-browse-count">${real.length} rule-set${real.length !== 1 ? "s" : ""}${truncated ? " (tree truncated — some entries may be missing)" : ""}</span>
      `;
      contentEl.appendChild(hdr);

      if (!real.length) {
        const empty = document.createElement("p");
        empty.className = "empty";
        empty.textContent = tr("set.reg.empty.permissions");
        contentEl.appendChild(empty);
        return;
      }

      const grouped = new Map();
      for (const p of real) {
        const g = p.group || "";
        if (!grouped.has(g)) grouped.set(g, []);
        grouped.get(g).push(p);
      }
      const sortedGroups = [...grouped.keys()].sort((a, b) => {
        if (a === "") return -1; if (b === "") return 1;
        return a.localeCompare(b);
      });

      function buildPermCard(perm) {
        const card = document.createElement("div");
        card.className = "skill-mkt-card remote-skill-card";
        const actionHtml = perm.installed
          ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
          : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`;
        const rulesRow = perm.rules
          ? `<div class="remote-agent-meta-row"><span class="remote-agent-meta-label">rules</span><span class="remote-agent-meta-chips"><span class="remote-agent-chip">${perm.rules}</span></span></div>`
          : "";
        card.innerHTML = `
          <div class="skill-mkt-header">
            <span class="skill-mkt-filename">${escHtml(perm.name)}</span>
            ${actionHtml}
          </div>
          <div class="skill-mkt-body">
            <p class="skill-mkt-desc">${escHtml(perm.description || "(permission rule-set)")}</p>
            ${rulesRow}
          </div>
        `;
        if (!perm.installed) {
          card.querySelector(".remote-install-btn").addEventListener("click", e => {
            e.stopPropagation();
            doInstallPermission(id, perm, card);
          });
        }
        card.addEventListener("click", e => {
          if (e.target.closest(".remote-install-btn")) return;
          state.permissionsRemotes.viewing = { ...state.permissionsRemotes.browsing, perm };
          (registriesHubRefresh || (() => renderPermissionsRemotesSection(host)))();
        });
        return card;
      }

      renderGroupedRemoteList({ contentEl, sortedGroups, grouped, buildCard: buildPermCard });
    }

    if (hasCached) populateContent(cached.permissions);

    try {
      const res = await skillsGet(`/permissions-registry/remotes/${id}/browse`);
      const perms = res.permissions || [];
      remotePermissionsCache[id] = { permissions: perms, timestamp: Date.now() };
      host.querySelector(".remote-browse-refresh-badge")?.setAttribute("hidden", "");
      host.querySelector(".remote-browse-loading")?.remove();
      populateContent(perms);
    } catch (e) {
      if (!hasCached) {
        contentEl.innerHTML = `<p class="settings-error">Failed to browse registry: ${escHtml(e.message)}</p>`;
      } else {
        setStatus(tr("set.reg.refreshFailed", { error: e.message }), "error");
      }
    }
  }

  async function renderPermissionsRemoteDetailView(host) {
    const { id, name, perm } = state.permissionsRemotes.viewing;
    host.innerHTML = `
      <div class="skill-detail-view">
        <div class="skill-detail-header">
          <button type="button" class="skill-back-btn">Back to ${escHtml(name)}</button>
        </div>
        <div class="skill-content-wrap">
          <pre class="skill-md-preview" id="perm-json-preview"><code>${escHtml(tr("set.loading"))}</code></pre>
        </div>
        <div class="skill-detail-footer">
          <span></span>
          <span class="skill-save-status"></span>
          ${perm.installed
            ? `<span class="remote-skill-installed-badge">${escHtml(tr("set.reg.installed"))}</span>`
            : `<button type="button" class="add-btn remote-install-btn">${escHtml(tr("common.install"))}</button>`}
        </div>
      </div>
    `;
    host.querySelector(".skill-back-btn").addEventListener("click", () => {
      state.permissionsRemotes.viewing = null;
      (registriesHubRefresh || (() => renderPermissionsRemotesSection(host)))();
    });

    const preview = host.querySelector("#perm-json-preview code");
    try {
      const res = await skillsGet(`/permissions-registry/remotes/${id}/permission/${perm.dir_path}`);
      let pretty = res.content || "";
      try { pretty = JSON.stringify(JSON.parse(pretty), null, 2); } catch { /* leave raw */ }
      preview.textContent = pretty;
    } catch (e) {
      preview.textContent = e.message;
    }

    const installBtn = host.querySelector(".remote-install-btn");
    if (installBtn) {
      installBtn.addEventListener("click", () => doInstallPermission(id, perm, host, installBtn));
    }
  }

  // doInstallPermission posts the install and swaps the button to "Installed".
  async function doInstallPermission(registryID, perm, cardOrHost, installBtn) {
    const btn = installBtn || cardOrHost.querySelector(".remote-install-btn");
    if (btn) { btn.disabled = true; btn.textContent = tr("set.reg.installing"); }
    try {
      const res = await skillsPost(`/permissions-registry/remotes/${registryID}/install/${perm.dir_path}`, {});
      perm.installed = true;
      if (btn) {
        btn.replaceWith(Object.assign(document.createElement("span"), {
          className: "remote-skill-installed-badge",
          textContent: tr("set.reg.installed"),
        }));
      }
      const n = res.rules || 0;
      setStatus(`Permission set "${res.name}" merged (${n} new rule${n !== 1 ? "s" : ""}). Reload to apply.`, "success");
      delete remotePermissionsCache[registryID];
    } catch (err) {
      if (btn) { btn.disabled = false; btn.textContent = tr("common.install"); }
      setStatus(tr("set.reg.installFailed", { error: err.message }), "error");
    }
  }

  // ─── Skills — upload helpers ───────────────────────────────────────────

  async function doSkillUpload(host, file, overwrite) {
    const fd = new FormData(); fd.append("file", file);
    const url = `/api/skills/registry/upload${overwrite ? "?overwrite=1" : ""}`;
    setStatus(tr("set.status.uploading"));
    try {
      const r = await fetch(url, { method: "POST", headers: authHeaders(), body: fd });
      const j = await r.json().catch(() => ({}));
      if (!r.ok) {
        if (r.status === 409 && j.code === "NAME_TAKEN") {
          // Extract skill name from error message if possible.
          const m = j.error && j.error.match(/"([^"]+)"/);
          const sname = m ? m[1] : "existing skill";
          if (await appConfirm(tr("set.confirm.overwriteExists", { name: sname }))) {
            await doSkillUpload(host, file, true);
          }
          return;
        }
        throw new Error(j.error || `HTTP ${r.status}`);
      }
      setStatus(`Skill "${j.name}" uploaded successfully.`, "success");
      renderSkills();
    } catch (e) { setStatus(tr("set.status.uploadFailed", { error: e.message }), "error"); }
  }

  function setupSkillDropZone(el, onFile) {
    el.addEventListener("dragover", e => { e.preventDefault(); el.classList.add("drop-active"); });
    el.addEventListener("dragleave", e => { if (!el.contains(e.relatedTarget)) el.classList.remove("drop-active"); });
    el.addEventListener("drop", e => {
      e.preventDefault(); el.classList.remove("drop-active");
      const file = e.dataTransfer.files[0]; if (!file) return;
      if (!/\.(zip|tar\.gz|tgz)$/i.test(file.name)) {
        setStatus(tr("set.status.archiveOnly"), "error"); return;
      }
      onFile(file);
    });
  }

  // ─── Save / Discard ────────────────────────────────────────────────────

  // embedderFingerprint reduces a parsed models.json object to a stable string
  // capturing everything that determines the internal embedder's identity:
  // the embed_model_ref, the referenced model's id/dim, and the connection
  // fields (kind/base_url/api_key) resolved through its provider_ref. A change
  // to any of these means hot-reload can't apply it — a restart is required.
  // Returns "" when no embedder is configured (embed_model_ref absent).
  function embedderFingerprint(models) {
    if (!models || typeof models !== "object") return "";
    const ref = String(models.embed_model_ref || "").trim().toLowerCase();
    if (!ref) return "";
    const ci = (obj, key) => {
      if (!obj || typeof obj !== "object") return null;
      if (obj[key]) return obj[key];
      const hit = Object.keys(obj).find(k => k.toLowerCase() === key);
      return hit ? obj[hit] : null;
    };
    const m = ci(models.models, ref);
    if (!m) return `ref:${ref}|<missing>`;
    const prov = ci(models.providers, String(m.provider_ref || "").trim().toLowerCase());
    const kind = String(m.provider || (prov && prov.kind) || "").trim();
    const baseURL = String(m.base_url || (prov && prov.base_url) || "").trim();
    const apiKey = String(m.api_key || (prov && prov.api_key) || "").trim();
    return [ref, kind, baseURL, apiKey, String(m.model || "").trim(), String(m.dim || "")].join("|");
  }

  // embedderChangedOnSave compares the pre-save and post-save models.json for
  // an embedder-identity change. Only meaningful for the "models" file; every
  // other file returns false (hot-reloadable). `oldVal`/`newVal` are parsed
  // objects (form view) or JSON strings (raw view).
  function embedderChangedOnSave(id, oldVal, newVal) {
    if (id !== "models") return false;
    const parse = (v) => {
      if (typeof v === "string") { try { return JSON.parse(v); } catch (_) { return null; } }
      return v;
    };
    return embedderFingerprint(parse(oldVal)) !== embedderFingerprint(parse(newVal));
  }

  async function saveActive() {
    const id = state.activeFile;
    setStatus(tr("set.status.saving"));
    try {
      let restartRequired = false;
      if (state.activeView === "raw") {
        const s = state.raw[id];
        const prevContent = s.content;
        const r = await fetch(BASE_PATH + `/api/config/file/${id}`, {
          method: "PUT",
          headers: authHeaders({ "Content-Type": "application/json" }),
          body: JSON.stringify({ content: s.value, mtime: s.mtime }),
        });
        if (!r.ok) throw new Error(await errText(r));
        const j = await r.json();
        restartRequired = embedderChangedOnSave(id, prevContent, j.content);
        s.content = j.content; s.mtime = j.mtime; s.dirty = false;
        // Invalidate parsed cache so the form view re-fetches.
        delete state.parsed[id];
      } else {
        const p = state.parsed[id];
        const prevData = p.data;
        const r = await fetch(BASE_PATH + `/api/config/parsed/${id}`, {
          method: "PUT",
          headers: authHeaders({ "Content-Type": "application/json" }),
          body: JSON.stringify({ data: prepareForSave(id, p.value), mtime: p.mtime }),
        });
        if (!r.ok) throw new Error(await errText(r));
        const j = await r.json();
        restartRequired = embedderChangedOnSave(id, prevData, p.value);
        p.data = deepClone(p.value);
        p.mtime = j.mtime;
        p.dirty = false;
        // Invalidate raw cache so the raw view re-fetches the canonical JSON.
        delete state.raw[id];
      }
      setStatus(restartRequired
        ? "Saved. Restart the server to apply the embedder change."
        : "Saved. Reload the agent to apply.", "success");
      showBanner(restartRequired);
      renderBody();
    } catch (e) {
      setStatus(tr("set.status.saveFailed", { error: e.message }), "error");
    }
  }

  async function discardActive() {
    if (!hasUnsavedActive()) return;
    if (!await appConfirm(tr("set.confirm.discard"))) return;
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
  window.Settings = { open, close, isOpen, prefsReady, saveNotifications, refreshSchedules };

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
