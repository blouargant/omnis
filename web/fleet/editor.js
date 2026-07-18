"use strict";
// FleetEditor — Fleet squad/agent detail-pane editors, extracted verbatim out
// of web/settings.js (Phase 6 Task 8 of the Fleet cutover; see
// .superpowers/sdd/task-8-brief.md). Pure module hygiene: no behaviour change.
// DOM-heavy and has no unit-test file — the store/tree/canvas modules test
// pure logic, but these editors only wire DOM to a FleetStore draft, so this
// is validated by live smoke only (see the task brief).
//
// init(deps) receives the settings.js IIFE-scope helpers these editors call
// (tr/escHtml/appConfirm/state/isBuiltinAgent/renderAgentTeamBlock/
// populateAgent{Skill,MCP,A2A}Block/TOOL_GROUPS/TOOL_MUTEX/TOOL_ICONS/
// TOOL_DISPLAY) once, at settings.js boot, after all of them are defined.
// Dual-export: CommonJS (unused today — no editor.test.js) + browser global
// (window.FleetEditor), matching the sibling store.js/canvas.js modules.
(function (root, factory) {
  const mod = factory();
  if (typeof module !== "undefined" && module.exports) module.exports = mod;
  if (typeof window !== "undefined") window.FleetEditor = mod;
})(this, function () {
  // Deps bag, set once by init(). Left null until then so a call before
  // init() throws immediately (loudly) rather than silently no-op-ing.
  let D = null;
  function init(deps) { D = deps; }

  // Router info (read-only): the Omnis router squad has no editable fields of
  // its own here — it's a leaderless routing entry point, config-driven and
  // auto-managed (see agent/routing.go). Title = the router squad's name,
  // body = the explainer, plus a small "router" tag as the auto-managed cue.
  function renderFleetRouterInfo(host, store, name) {
    host.innerHTML = `
      <div class="fleet-editor-info">
        <h2 class="agent-detail-name">${D.escHtml(name)} <span class="fleet-tag">${D.escHtml(D.tr("fleet.tag.router"))}</span></h2>
        <p>${D.escHtml(D.tr("fleet.editor.routerInfo"))}</p>
      </div>`;
  }

  // Squad editor (Fleet, store-driven). Ported from the classic Squads
  // sub-tab's detail editor (since removed) but sourced from the FleetStore
  // draft instead of the raw parsed config, and with the store owning the
  // leaderless/member-trim rules instead of this function reimplementing
  // them inline.
  function renderFleetSquadEditor(host, store, name, onRename) {
    const sq = store.squad(name);
    if (!sq) {
      host.innerHTML = `<div class="fleet-editor-empty">${D.escHtml(D.tr("fleet.editor.selectPrompt"))}</div>`;
      return;
    }

    const model = store.model();
    // Leader candidates: only agents marked `leader: true` (the agent named
    // "leader" is the canonical default — auto-flagged when the field is
    // absent, matching the server-side resolver).
    const isLeaderAgent = a => !!a.leader || (a.name || "").toLowerCase() === "leader";
    const leaderCandidates = (model.agents || [])
      .filter(a => a && a.name && (a.enabled === undefined || a.enabled) && (a.name || "").toLowerCase() !== "curator" && isLeaderAgent(a))
      .map(a => a.name);
    const memberCandidates = (model.agents || [])
      .filter(a => a && a.name && (a.enabled === undefined || a.enabled) && (a.name || "").toLowerCase() !== "curator");
    const members = Array.isArray(sq.members) ? sq.members : [];
    const isDefault = store.isDefaultSquad(sq.name);
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

    host.innerHTML = `
      <div class="agent-detail-section">
        <div class="agent-detail-field">
          <label class="agent-detail-label">${D.escHtml(D.tr("common.name"))}</label>
          <input type="text" class="agent-detail-input" id="squad-name" value="${D.escHtml(sq.name || "")}" ${isDefault ? "disabled" : ""} />
          ${isDefault ? `<div class="agent-detail-hint">${D.escHtml(D.tr("set.squad.defaultNameHint"))}</div>` : ""}
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">${D.escHtml(D.tr("common.description"))}</label>
          <input type="text" class="agent-detail-input" id="squad-desc" value="${D.escHtml(sq.description || "")}" placeholder="${D.escHtml(D.tr("set.squad.descPlaceholder"))}" />
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">${D.escHtml(D.tr("set.squad.leader"))}</label>
          <select class="agent-detail-input" id="squad-leader">
            ${isDefault ? "" : `<option value="none" ${leaderless ? "selected" : ""}>${D.escHtml(D.tr("set.squad.leaderNone"))}</option>`}
            ${leaderCandidates.map(n => `<option value="${D.escHtml(n)}" ${!leaderless && n === sq.leader ? "selected" : ""}>${D.escHtml(n)}</option>`).join("")}
          </select>
          ${leaderless ? `<div class="agent-detail-hint">${D.escHtml(D.tr("set.squad.leaderlessHint"))}</div>` : ""}
        </div>
        <div class="agent-detail-field">
          <label class="agent-detail-label">${D.escHtml(D.tr("set.squad.members"))}</label>
          <div class="agent-tools-grid" id="squad-members">
            ${sortedMembers.map(a => {
              const isOn = members.includes(a.name);
              const isLeaderRow = a.name === sq.leader;
              const desc = a.description || "";
              return `
              <div class="agent-tool-card${isOn ? " tool-on" : ""}${isLeaderRow ? " tool-disabled" : ""}" data-name="${D.escHtml(a.name)}" data-tip="${D.escHtml(desc)}">
                <div class="agent-tool-icon">${agentIcon}</div>
                <div class="agent-tool-info">
                  <span class="agent-tool-name">${D.escHtml(a.name)}</span>
                  <span class="agent-tool-desc">${D.escHtml(desc)}</span>
                </div>
                <div class="agent-tool-toggle-pill ${isOn ? "pill-on" : "pill-off"}"></div>
              </div>
            `;
            }).join("")}
          </div>
          <div class="agent-detail-hint">${D.escHtml(leaderless ? D.tr("set.squad.membersHintLeaderless") : D.tr("set.squad.membersHint"))}</div>
        </div>
        <div class="agent-detail-field">
          <div class="agent-toggle-row">
            <label class="agent-toggle-switch">
              <input type="checkbox" class="agent-toggle-input" id="squad-hidden" ${sq.hidden ? "checked" : ""} />
              <span class="agent-toggle-slider"></span>
            </label>
            <span class="agent-toggle-text">${D.escHtml(D.tr("fleet.editor.hidden"))}</span>
          </div>
          <div class="agent-detail-hint">${D.escHtml(D.tr("fleet.editor.hiddenHint"))}</div>
        </div>
        ${!isDefault ? `<div class="squad-detail-actions"><button type="button" class="agent-detail-remove" id="squad-remove">${D.escHtml(D.tr("set.squad.deleteBtn"))}</button></div>` : ""}
      </div>
    `;

    const nameInput = host.querySelector("#squad-name");
    if (nameInput && !isDefault) {
      nameInput.addEventListener("input", () => {
        sq.name = nameInput.value;
        // Tell the pane the selection follows the rename BEFORE store.touch()
        // fires the tree/action-bar repaint, so that repaint (and any later
        // post-save re-render) looks the entity up by its current name.
        if (typeof onRename === "function") onRename(sq.name);
        store.touch();
        // Re-render so the tree/actions catch up, then restore focus/caret to
        // the freshly-mounted input.
        renderFleetSquadEditor(host, store, sq.name, onRename);
        const ref = host.querySelector("#squad-name");
        if (ref) { ref.focus(); ref.setSelectionRange(ref.value.length, ref.value.length); }
      });
    }
    host.querySelector("#squad-desc").addEventListener("input", (e) => {
      sq.description = e.target.value;
      store.touch();
    });
    host.querySelector("#squad-leader").addEventListener("change", (e) => {
      store.setLeader(sq.name, e.target.value);
      renderFleetSquadEditor(host, store, sq.name, onRename);
    });
    host.querySelectorAll("#squad-members .agent-tool-card").forEach(card => {
      if (card.classList.contains("tool-disabled")) return;
      card.addEventListener("click", () => {
        store.toggleMember(sq.name, card.dataset.name);
        renderFleetSquadEditor(host, store, sq.name, onRename);
      });
    });
    // Hidden squads (e.g. Session Search) exist but are never offered in the
    // picker or as a routing target. The classic editor never surfaced this —
    // the Fleet editor does, so the flag is editable without dropping to raw JSON.
    host.querySelector("#squad-hidden").addEventListener("change", (e) => {
      store.setHidden(sq.name, e.target.checked);
    });
    if (!isDefault) {
      host.querySelector("#squad-remove").addEventListener("click", async () => {
        if (!await D.appConfirm(D.tr("set.confirm.deleteSquad", { name: sq.name }))) return;
        store.removeSquad(sq.name);
        host.innerHTML = "";
      });
    }
  }

  // Agent editor (Fleet, store-driven). Ported from the classic
  // renderAgentDetail (Agent → Agents sub-tab, ~line 3731) but sourced from
  // the FleetStore draft instead of the raw parsed config: `a` is the live
  // draft agent object (mutate in place), `onChange` is `store.touch()`
  // instead of `markFormDirty("agent")`, and the classic fleet-list DOM pokes
  // (updateFleetModelLine, .agent-fleet-item dot/name updates) are dropped —
  // the Fleet tree repaints itself from store.onChange(). The up/down
  // reorder block is deliberately NOT ported (reorder is a Phase-5
  // composition action, out of scope here).
  function renderFleetAgentEditor(host, store, name, onRename, openModels) {
    const a = store.agent(name);
    if (!a) {
      host.innerHTML = `<div class="fleet-editor-empty">${D.escHtml(D.tr("fleet.editor.selectPrompt"))}</div>`;
      return;
    }

    const model = store.model();
    const draft = store.draft();
    const isLeader = a.name === "leader";
    const isBuiltin = D.isBuiltinAgent(a.name);
    const builtinDefaults = (D.state.builtinAgents && D.state.builtinAgents[a.name]) || null;
    const onChange = () => store.touch();
    const modelOptions = Object.keys(D.state.parsed.models?.value?.models || {});

    // ── Title bar ──
    const titleBar = document.createElement("div");
    titleBar.className = "agent-detail-titlebar";
    const isEnabled = isLeader ? true : a.enabled !== false;
    const detailSourceBadge = a.source === "local"
      ? `<span class="source-badge source-badge-local">local</span>`
      : "";
    titleBar.innerHTML = `
      <div class="agent-detail-title-left">
        <h2 class="agent-detail-name">${D.escHtml(a.name || D.tr("app.askuser.unnamed"))}</h2>
        ${detailSourceBadge}
        <span class="agent-live-badge">LIVE</span>
      </div>
      <div class="agent-detail-title-right">
        <label class="agent-active-toggle-wrap">
          <span class="agent-active-toggle-label">${D.escHtml(D.tr("set.agent.activeState"))}</span>
          <span class="agent-toggle-switch">
            <input type="checkbox" class="agent-toggle-input" ${isEnabled ? "checked" : ""} ${isLeader ? "disabled" : ""}>
            <span class="agent-toggle-slider"></span>
          </span>
        </label>
        ${isBuiltin ? "" : `<button type="button" class="model-remove-link agent-remove-link">${D.escHtml(D.tr("set.agent.removeBtn"))}</button>`}
      </div>
    `;
    titleBar.querySelector(".agent-toggle-input").addEventListener("change", e => {
      a.enabled = e.target.checked;
      onChange();
    });
    if (!isBuiltin) {
      titleBar.querySelector(".agent-remove-link").addEventListener("click", async () => {
        if (!await D.appConfirm(D.tr("set.confirm.removeAgent", { name: a.name }))) return;
        store.removeAgent(a.name);
        host.innerHTML = "";
      });
    }

    const body = document.createElement("div");
    body.className = "agent-detail-body";

    // ── General Settings ──
    const genSection = document.createElement("section");
    genSection.className = "agent-detail-section";
    const genHdr = document.createElement("div");
    genHdr.className = "agent-section-hdr";
    genHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M3 9h18M9 21V9"/></svg><h3>${D.escHtml(D.tr("set.hdr.generalSettings"))}</h3>`;
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
    genGrid.appendChild(genField(D.tr("set.agent.displayName"), f => {
      const inp = document.createElement("input");
      inp.type = "text"; inp.className = "agent-gen-input"; inp.value = a.name || "";
      if (isLeader) inp.disabled = true;
      inp.addEventListener("input", () => {
        a.name = inp.value;
        titleBar.querySelector(".agent-detail-name").textContent = a.name || D.tr("app.askuser.unnamed");
        // Keep the pane's selection following the rename BEFORE onChange()'s
        // store.touch() fires the tree/action-bar repaint — see the matching
        // comment in renderFleetSquadEditor's name-input handler.
        if (typeof onRename === "function") onRename(a.name);
        onChange();
      });
      f.appendChild(inp);
    }));

    // Model Reference
    genGrid.appendChild(genField(D.tr("set.agent.modelRef"), f => {
      const sel = document.createElement("select");
      sel.className = "agent-gen-input";
      for (const o of ["", ...modelOptions]) {
        const opt = document.createElement("option");
        opt.value = o; opt.textContent = o || D.tr("set.none");
        if (o === (a.model_ref || "")) opt.selected = true;
        sel.appendChild(opt);
      }
      sel.addEventListener("change", () => {
        a.model_ref = sel.value;
        onChange();
      });
      f.appendChild(sel);

      if (typeof openModels === "function") {
        const jump = document.createElement("button");
        jump.type = "button";
        jump.className = "fleet-modelref-jump";
        jump.textContent = "↗";
        jump.setAttribute("data-tip", D.tr("fleet.shell.editModelTip"));
        jump.setAttribute("aria-label", D.tr("fleet.shell.editModelTip"));
        jump.addEventListener("click", () => openModels());
        f.appendChild(jump);
      }

      // Surface a frontmatter "model:" hint that the local catalog can't
      // resolve as a recommended model in angle brackets.
      if (a.recommended_model) {
        const hint = document.createElement("span");
        hint.className = "agent-model-recommendation";
        hint.textContent = `<${a.recommended_model}>`;
        hint.setAttribute("data-tip", D.tr("set.agent.recommendedTip"));
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
    toolHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg><h3>${D.escHtml(D.tr("set.hdr.availableTools"))}</h3>`;
    toolSection.appendChild(toolHdr);

    const toolGrid = document.createElement("div");
    toolGrid.className = "agent-tools-grid";
    const effectiveTools = (isLeader && (!a.tools || !a.tools.length)) ? [...D.TOOL_GROUPS] : (a.tools || []);
    const cur = new Set(effectiveTools);
    const btnByTool = {};
    const toolEntries = [];

    // code_search only mounts when a semantic embedder is configured. Mirror
    // the serpapi pattern and grey it out when no embedding model is selected
    // (agents.json override wins, else models.json embed_model_ref). Env-only
    // (OMNIS_EMBED_*) config isn't visible here — same limitation as serpapi_key.
    const embedRef = (draft.embed_model_ref || D.state.parsed.models?.value?.embed_model_ref || "").toString().trim();
    const embedConfigured = !!embedRef;

    for (const t of D.TOOL_GROUPS) {
      const isSerpDisabled = t === "serpapi" && !draft.serpapi_key;
      const isSerperDisabled = t === "serper" && !draft.serper_key;
      const isCodeSearchDisabled = t === "code_search" && !embedConfigured;
      const isDisabledTool = isSerpDisabled || isSerperDisabled || isCodeSearchDisabled;
      const isOn = cur.has(t);
      const btn = document.createElement("div");
      btn.className = "agent-tool-card" + (isOn ? " tool-on" : "") + (isDisabledTool ? " tool-disabled" : "");
      btn.setAttribute("data-tip", D.TOOL_DISPLAY[t] || "");
      btn.innerHTML = `
        <div class="agent-tool-icon">${D.TOOL_ICONS[t] || ""}</div>
        <div class="agent-tool-info">
          <span class="agent-tool-name">${D.escHtml(t)}</span>
          <span class="agent-tool-desc">${D.escHtml(D.TOOL_DISPLAY[t] || "")}</span>
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
            for (const peer of (D.TOOL_MUTEX[t] || [])) {
              if (btnByTool[peer]) {
                cur.delete(peer);
                btnByTool[peer].classList.remove("tool-on");
                btnByTool[peer].querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill pill-off";
              }
            }
            btn.classList.add("tool-on");
            btn.querySelector(".agent-tool-toggle-pill").className = "agent-tool-toggle-pill pill-on";
          }
          a.tools = Array.from(cur);
          // Reveal/hide the Skills / MCP sections Task 6 appends below. Kept
          // null-safe: those sections don't exist yet in this task, so the
          // toggle is a no-op until Task 6 adds `.fleet-skills-section` /
          // `.fleet-mcp-section` to the elements it builds.
          if (t === "Skill") {
            host.querySelector(".fleet-skills-section")?.classList.toggle("section-inactive", !cur.has("Skill"));
          }
          if (t === "mcp") {
            host.querySelector(".fleet-mcp-section")?.classList.toggle("section-inactive", !cur.has("mcp"));
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
        key: "leader", label: "leader", desc: D.tr("set.agent.canLead"),
        icon: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M2 20h20"/><path d="M5 20V8l7-4 7 4v12"/><path d="M9 20v-6h6v6"/></svg>`,
        getValue: () => isLeader ? true : !!a.leader,
        setValue: v => { a.leader = v; onChange(); },
        locked: isLeader,
      },
      {
        key: "allow_file_attachments", label: "files", desc: D.tr("set.agent.fileAttachments"),
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
          <span class="agent-tool-name">${D.escHtml(fc.label)}</span>
          <span class="agent-tool-desc">${D.escHtml(fc.desc)}</span>
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
    toolEntries.sort((x, y) => Number(y.isOn) - Number(x.isOn));
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
      parHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="6" y1="3" x2="6" y2="21"/><line x1="12" y1="3" x2="12" y2="21"/><line x1="18" y1="3" x2="18" y2="21"/></svg><h3>${D.escHtml(D.tr("set.hdr.parallelism"))}</h3>`;
      parSec.appendChild(parHdr);
      const parBody = document.createElement("div");
      parBody.className = "agent-gen-grid";
      const parField = document.createElement("div");
      parField.className = "agent-gen-field";
      const parLbl = document.createElement("label");
      parLbl.className = "agent-gen-label";
      parLbl.textContent = D.tr("set.agent.maxInstances");
      // App tooltip (themed #tip-layer via data-tip), not the native browser
      // title bubble, to match the rest of the settings UI.
      const parInfo = document.createElement("span");
      parInfo.className = "agent-gen-info";
      parInfo.textContent = "?";
      parInfo.setAttribute("data-tip", D.tr("set.agent.maxInstancesTip"));
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
      parDec.setAttribute("aria-label", D.tr("set.agent.decrease"));
      const parInc = document.createElement("button");
      parInc.type = "button";
      parInc.className = "num-stepper-btn";
      parInc.textContent = "+";
      parInc.setAttribute("aria-label", D.tr("set.agent.increase"));
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
      parHint.textContent = D.tr("set.agent.maxInstancesHint");
      parField.appendChild(parLbl);
      parField.appendChild(parWrap);
      parField.appendChild(parHint);
      parBody.appendChild(parField);
      parSec.appendChild(parBody);
      body.appendChild(parSec);

      // ── Sessions (resumable_sessions) ──
      // Durable, re-attachable sub-agent sessions are ON by default (opt-out):
      // each call returns a `session` handle the leader can pass back as
      // resume_session to CONTINUE that exact conversation (keeping its prior
      // context) instead of starting fresh. Toggle OFF to make this sub-agent
      // a stateless pure function (a throwaway session per call). Persist-clean:
      // only the opt-out (false) is written; the default-on case leaves the key
      // absent. Same leader/curator gate as Parallelism (both inert for
      // non-fan-out roots).
      const resSec = document.createElement("section");
      resSec.className = "agent-detail-section";
      const resHdr = document.createElement("div");
      resHdr.className = "agent-section-hdr";
      resHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg><h3>${D.escHtml(D.tr("set.hdr.sessions"))}</h3>`;
      resSec.appendChild(resHdr);
      const resBody = document.createElement("div");
      resBody.className = "agent-gen-grid";
      const resField = document.createElement("div");
      resField.className = "agent-gen-field";
      const resRow = document.createElement("div");
      resRow.className = "agent-toggle-row";
      resRow.setAttribute("data-tip", D.tr("set.agent.resumableTip"));
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
      resText.textContent = D.tr("set.agent.resumable");
      resRow.appendChild(resSwitch);
      resRow.appendChild(resText);
      const resHint = document.createElement("p");
      resHint.className = "agent-gen-hint";
      resHint.textContent = D.tr("set.agent.resumableHint");
      resField.appendChild(resRow);
      resField.appendChild(resHint);
      resBody.appendChild(resField);
      resSec.appendChild(resBody);
      body.appendChild(resSec);
    }

    // ── Team (subagents) ──
    // This agent's OWN delegable team, mounted as tools exactly as a squad leader
    // mounts its members. It is how an expensive specialist pushes bulk retrieval
    // into a cheap gatherer's context instead of accumulating it in its own —
    // retrieval cost is quadratic in ONE agent's tool calls, so who holds the
    // fetched pages decides the bill.
    //
    // Offered for every agent EXCEPT the curator (a process-wide post-session hook,
    // never delegable and never a delegator).
    if ((a.name || "").toLowerCase() !== "curator") {
      const teamSec = document.createElement("section");
      teamSec.className = "agent-detail-section";
      const teamHdr = document.createElement("div");
      teamHdr.className = "agent-section-hdr";
      teamHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M23 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/></svg><h3>${D.escHtml(D.tr("set.hdr.team"))}</h3>`;
      teamSec.appendChild(teamHdr);

      const teamBody = document.createElement("div");
      teamBody.className = "agent-block-body";
      D.renderAgentTeamBlock(teamBody, a, store.draft().agents, () => store.touch());
      teamSec.appendChild(teamBody);
      body.appendChild(teamSec);
    }

    // ── Skills ──
    const skillsSec = document.createElement("section");
    skillsSec.className = "agent-detail-section fleet-skills-section" + (cur.has("Skill") ? "" : " section-inactive");
    const skillsHdr = document.createElement("div");
    skillsHdr.className = "agent-section-hdr";
    skillsHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg><h3>${D.escHtml(D.tr("settings.title.skills"))}</h3>`;
    skillsSec.appendChild(skillsHdr);
    const skillsBody = document.createElement("div");
    skillsBody.className = "skills-agent-body";
    skillsSec.appendChild(skillsBody);
    D.populateAgentSkillBlock(skillsBody, a, cur.has("Skill"), () => store.touch());
    body.appendChild(skillsSec);

    // ── MCP Servers ──
    const mcpSec = document.createElement("section");
    mcpSec.className = "agent-detail-section fleet-mcp-section" + (cur.has("mcp") ? "" : " section-inactive");
    const mcpHdr = document.createElement("div");
    mcpHdr.className = "agent-section-hdr";
    mcpHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="12" rx="2"/><line x1="2" y1="20" x2="22" y2="20"/><line x1="8" y1="16" x2="8" y2="20"/><line x1="16" y1="16" x2="16" y2="20"/></svg><h3>${D.escHtml(D.tr("settings.title.mcp"))}</h3>`;
    mcpSec.appendChild(mcpHdr);
    const mcpBody = document.createElement("div");
    mcpBody.className = "skills-agent-body";
    mcpSec.appendChild(mcpBody);
    D.populateAgentMCPBlock(mcpBody, a, cur.has("mcp"), () => store.touch());
    body.appendChild(mcpSec);

    // ── A2A Agents ──
    const a2aSec = document.createElement("section");
    a2aSec.className = "agent-detail-section";
    const a2aHdr = document.createElement("div");
    a2aHdr.className = "agent-section-hdr";
    a2aHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z"/></svg><h3>${D.escHtml(D.tr("settings.title.a2a"))}</h3>`;
    a2aSec.appendChild(a2aHdr);
    const a2aBody = document.createElement("div");
    a2aBody.className = "skills-agent-body";
    a2aSec.appendChild(a2aBody);
    D.populateAgentA2ABlock(a2aBody, a, () => store.touch());
    body.appendChild(a2aSec);

    // ── Instruction Set ──
    const instrSection = document.createElement("section");
    instrSection.className = "agent-detail-section";
    const instrHdr = document.createElement("div");
    instrHdr.className = "agent-section-hdr";
    instrHdr.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg><h3>${D.escHtml(D.tr("set.hdr.instructionSet"))}</h3>`;
    instrSection.appendChild(instrHdr);

    const instrBody = document.createElement("div");
    instrBody.className = "agent-instr-body";

    // Public Description
    const descF = document.createElement("div");
    descF.className = "agent-instr-field";
    const descLbl = document.createElement("label");
    descLbl.className = "agent-instr-label";
    descLbl.textContent = D.tr("set.agent.publicDesc");
    if (isBuiltin) {
      const tag = document.createElement("span");
      tag.className = "agent-builtin-tag";
      tag.textContent = D.tr("set.agent.builtin");
      descLbl.appendChild(tag);
    }
    const descInp = document.createElement("input");
    descInp.type = "text"; descInp.className = "agent-gen-input";
    descInp.placeholder = D.tr("set.agent.descPlaceholder");
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
    sysLbl.textContent = D.tr("set.agent.systemInstructions");
    if (isBuiltin) {
      const tag = document.createElement("span");
      tag.className = "agent-builtin-tag";
      tag.textContent = D.tr("set.agent.builtin");
      sysLbl.appendChild(tag);
    }
    const sysCount = document.createElement("span");
    sysCount.className = "agent-instr-count";
    const instrVal = isBuiltin && builtinDefaults ? (builtinDefaults.instruction || "") : (a.instruction || "");
    sysCount.textContent = D.tr("set.agent.tokensUsed", { count: Math.round(instrVal.length / 4) });
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
        sysCount.textContent = D.tr("set.agent.tokensUsed", { count: Math.round(ta.value.length / 4) });
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
    adv.innerHTML = `<summary class="agent-advanced-summary">${D.escHtml(D.tr("set.agent.advancedPaths"))}</summary>`;
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
      if (isLeader && !a[key]) inp.placeholder = D.tr("set.default");
      inp.addEventListener("input", () => { a[key] = inp.value; onChange(); });
      f.appendChild(lbl); f.appendChild(inp); advGrid.appendChild(f);
    }
    adv.appendChild(advGrid);
    body.appendChild(adv);

    host.innerHTML = "";
    host.appendChild(titleBar);
    host.appendChild(body);
  }

  return {
    init,
    renderSquadEditor: renderFleetSquadEditor,
    renderAgentEditor: renderFleetAgentEditor,
    renderRouterInfo: renderFleetRouterInfo,
  };
});
