// Settings panel — agent-toolkit configuration editor.
// Loaded after app.js. Uses the same `token` and `authHeaders` defined there.
// Exposes Settings.open() / Settings.close() / Settings.isOpen().

(function () {
  const FILES = [
    { id: "agent",       label: "Agent",       form: "agent" },
    { id: "permissions", label: "Permissions", form: "permissions" },
    { id: "mcp",         label: "MCP",         form: "mcp" },
  ];

  const RESTART_FLAG = "agent_toolkit_needs_restart";
  const BANNER_DISMISS_FLAG = "agent_toolkit_restart_dismissed";
  const TOOL_GROUPS = ["fs", "mcp", "skills", "softskills"];

  const AGENT_SUBTABS = [
    { id: "globals", label: "Globals" },
    { id: "models",  label: "Models"  },
    { id: "agents",  label: "Agents"  },
  ];

  const state = {
    activeFile: "agent",
    activeView: "form", // 'form' | 'raw'
    activeAgentSubtab: "globals", // only used when activeFile === 'agent'
    raw: {}, // id → { content, mtime, dirty, value }
    parsed: {}, // id → { data, mtime, dirty, value }
    open: false,
  };

  // ─── DOM refs ──────────────────────────────────────────────────────────
  let panelEl, tabsEl, viewToggleEl, bodyEl, footerEl, statusEl;

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

  async function doRestart() {
    if (!await appConfirm("Restart the agent-toolkit server now? Active streams will be interrupted.")) return;
    setStatus("Restarting…");
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
      // Poll /api/health until reachable, then reload.
      const start = Date.now();
      const tick = async () => {
        try {
          const h = await fetch("/api/health");
          if (h.ok) { window.location.reload(); return; }
        } catch (_) { /* not yet up */ }
        if (Date.now() - start > 30000) {
          setStatus("Server did not come back within 30s. Reload manually.");
          return;
        }
        setTimeout(tick, 750);
      };
      setTimeout(tick, 1000);
    } catch (e) {
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
        <h2>Configuration</h2>
        <p class="settings-hint">
          Edits are saved to disk. The running agent keeps the previously-loaded
          configuration until the server is restarted.
        </p>
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

  async function setActiveFile(id) {
    if (state.activeFile !== id && hasUnsavedActive() &&
        !await appConfirm("Discard unsaved changes in the current tab?")) {
      return;
    }
    state.activeFile = id;
    tabsEl.querySelectorAll("button").forEach(b => {
      b.classList.toggle("active", b.dataset.file === id);
    });
    renderBody();
  }

  async function setActiveView(v) {
    if (state.activeView === v) return;
    if (hasUnsavedActive() && !await appConfirm("Discard unsaved changes in this view?")) return;
    state.activeView = v;
    viewToggleEl.querySelectorAll("button").forEach(b => {
      b.classList.toggle("active", b.dataset.view === v);
    });
    renderBody();
  }

  function hasUnsavedActive() {
    if (state.activeView === "raw") {
      const r = state.raw[state.activeFile];
      return r && r.dirty;
    }
    const p = state.parsed[state.activeFile];
    return p && p.dirty;
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
    const id = state.activeFile;
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
      const fields = [
        ["provider", "string"], ["model", "string"], ["base_url", "string"], ["api_key", "string"],
        ["context_length", "number"],
        ["input_token_price_per_million", "number"],
        ["output_token_price_per_million", "number"],
        ["cached_input_token_price_per_million", "number"],
        ["cache_creation_token_price_per_million", "number"],
      ];
      for (const [k, kind] of fields) {
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

      // Tools default to all available for leader when not explicitly set.
      const effectiveTools = (isLeader && (!a.tools || !a.tools.length)) ? [...TOOL_GROUPS] : a.tools;
      grid.appendChild(toolsField("tools", effectiveTools, v => { a.tools = v; onChange(); }));

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

  function toolsField(label, val, onChange) {
    const row = document.createElement("div");
    row.className = "form-row form-row-tools";
    const span = document.createElement("span");
    span.textContent = label;
    row.appendChild(span);
    const wrap = document.createElement("div");
    wrap.className = "tools-checks";
    const cur = new Set(Array.isArray(val) ? val : []);
    for (const t of TOOL_GROUPS) {
      const lab = document.createElement("label");
      lab.className = "tools-check";
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.checked = cur.has(t);
      cb.addEventListener("change", () => {
        if (cb.checked) cur.add(t); else cur.delete(t);
        onChange(Array.from(cur));
      });
      lab.appendChild(cb);
      lab.appendChild(document.createTextNode(" " + t));
      wrap.appendChild(lab);
    }
    row.appendChild(wrap);
    return row;
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
    if (!tabsEl.querySelector("button.active")) {
      tabsEl.querySelector(`button[data-file="${state.activeFile}"]`).classList.add("active");
    }
    renderBody();
  }

  function close() {
    if (!state.open) return;
    state.open = false;
    if (panelEl) panelEl.hidden = true;
    document.getElementById("chat").classList.remove("chat--settings");
    const sb = document.getElementById("settings-btn");
    if (sb) sb.classList.remove("active");
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
    const btn = document.getElementById("settings-btn");
    if (btn) btn.addEventListener("click", () => {
      if (isOpen()) close(); else open();
    });
  });
})();
