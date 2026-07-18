"use strict";
// FleetTree — read-only topology renderer for the Fleet settings view (Phase 1).
(function () {
  const esc = (s) => (typeof window !== "undefined" && window.escHtml)
    ? window.escHtml(s)
    : String(s == null ? "" : s).replace(/[&<>"]/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;" }[c]));
  const t = (k, v) => (typeof window !== "undefined" && window.tr) ? window.tr(k, v) : k;

  const GLYPH = {
    router: "◇", squad: "⬢", leaderless: "⬡", leader: "★",
    member: "•", sole: "◆", subagent: "↳", unused: "·",
  };

  let _selRef = null;
  let _drag = null;
  const lc2 = s => String(s || "").toLowerCase();

  function clearDropCues(container) {
    container.querySelectorAll(".drop-target").forEach(el => el.classList.remove("drop-target"));
  }

  function legendHTML() {
    const items = [
      ["◇", t("fleet.legend.router")], ["⬢", t("fleet.legend.squad")],
      ["⬡", t("fleet.legend.leaderless")], ["★", t("fleet.legend.leader")],
      ["•", t("fleet.legend.member")], ["↳", t("fleet.legend.subagent")],
      ["⌂N", t("fleet.legend.shared")],
    ];
    return `<div class="fleet-legend">${items.map(([g, label]) =>
      `<span><code>${esc(g)}</code> ${esc(label)}</span>`).join("")}</div>`;
  }

  function rowHTML(n, i, dndEnabled) {
    if (n.kind === "unused-header") {
      return `<div class="fleet-unused-header">${esc(t("fleet.unusedAgents"))}</div>`;
    }
    const glyph = GLYPH[n.kind] || "";
    const indent = 8 + n.depth * 16;
    const model = n.agent && n.agent.model_ref ? `<span class="fleet-sub">${esc(n.agent.model_ref)}</span>` : "";
    const badge = n.shared && n.shared > 1 ? `<span class="fleet-badge" data-tip="${esc(t("fleet.sharedTip", { n: n.shared }))}">⌂${n.shared}</span>` : "";
    let tag = "";
    if (n.kind === "router") tag = `<span class="fleet-tag">${esc(t("fleet.tag.router"))}</span>`;
    else if (n.kind === "leaderless") tag = `<span class="fleet-tag">${esc(t("fleet.tag.leaderless"))}</span>`;
    if (n.squad && n.squad.hidden) tag += `<span class="fleet-tag">${esc(t("fleet.tag.hidden"))}</span>`;
    const ref = n.kind === "router" ? { type: "router", name: n.name }
      : (n.kind === "squad" || n.kind === "leaderless") ? { type: "squad", name: n.name }
      : (n.kind === "leader" || n.kind === "member" || n.kind === "sole"
         || n.kind === "subagent" || n.kind === "unused") ? { type: "agent", name: n.name }
      : { type: "none", name: "" };
    const sel = _selRef && _selRef.type === ref.type && lc2(_selRef.name) === lc2(ref.name)
      && ref.type !== "none" ? " selected" : "";
    // Kebab for everything except the router (auto-managed, read-only).
    const kebab = (ref.type !== "none" && n.kind !== "router")
      ? `<button type="button" class="fleet-kebab" aria-label="${esc(t("fleet.menu.open"))}" data-tip="${esc(t("fleet.menu.open"))}">⋯</button>` : "";
    const dragAttrs = (dndEnabled && (n.kind === "member" || n.kind === "unused"))
      ? ` draggable="true" data-drag-name="${esc(n.name)}"${n.kind === "member" ? ` data-drag-squad="${esc(n.squadName || "")}" data-member-idx="${n.memberIdx}"` : ""}` : "";
    return `<div class="fleet-row kind-${esc(n.kind)}${sel}" data-node-idx="${i}" data-ref-type="${esc(ref.type)}" data-ref-name="${esc(ref.name)}" style="padding-left:${indent}px"${dragAttrs}>
      <span class="fleet-glyph">${esc(glyph)}</span>
      <span class="fleet-name">${esc(n.name)}</span>${badge}${model}${tag}${kebab}
    </div>`;
  }

  function render(container, model, opts) {
    _selRef = (opts && opts.selectedRef) || null;
    const nodes = window.FleetStore.treeNodes(model);
    const dndEnabled = !!(opts && (typeof opts.onReorder === "function" || typeof opts.onDropMember === "function"));
    const hasSquads = model.squads.some(s => s.kind !== "router");
    const body = hasSquads
      ? `<div class="fleet-tree">${nodes.map((n, i) => rowHTML(n, i, dndEnabled)).join("")}</div>`
      : `<div class="fleet-empty">${esc(t("fleet.empty"))}</div>`;
    container.innerHTML = `<div class="fleet-view">${legendHTML()}${body}</div>`;
    const onSelect = opts && opts.onSelect;
    const onMenu = opts && opts.onMenu;
    container.querySelectorAll(".fleet-row[data-ref-type]").forEach(row => {
      const type = row.dataset.refType;
      if (type === "none") return;
      const node = nodes[Number(row.dataset.nodeIdx)];
      const ref = { type, name: row.dataset.refName };
      if (typeof onSelect === "function") {
        row.classList.add("fleet-row-click");
        row.addEventListener("click", (e) => {
          if (e.target.closest(".fleet-kebab")) return; // kebab handles itself
          onSelect(ref);
        });
      }
      if (typeof onMenu === "function") {
        // Only rows with a kebab are menu-actionable; the router row has none, so
        // it must keep its native context menu (no preventDefault, no handler).
        const kebab = row.querySelector(".fleet-kebab");
        if (kebab) {
          row.addEventListener("contextmenu", (e) => { e.preventDefault(); onMenu(ref, e, node); });
          kebab.addEventListener("click", (e) => { e.stopPropagation(); onMenu(ref, e, node); });
        }
      }
    });
    const onReorder = opts && opts.onReorder;
    const onDropMember = opts && opts.onDropMember;
    if (typeof onReorder === "function" || typeof onDropMember === "function") {
      container.querySelectorAll(".fleet-row[draggable=true]").forEach(row => {
        row.addEventListener("dragstart", (e) => {
          _drag = { name: row.dataset.dragName, squadName: row.dataset.dragSquad || "",
                    kind: row.classList.contains("kind-member") ? "member" : "unused",
                    idx: row.dataset.memberIdx != null ? Number(row.dataset.memberIdx) : -1 };
          row.classList.add("dragging");
          e.dataTransfer.effectAllowed = "move";
          try { e.dataTransfer.setData("text/plain", _drag.name); } catch (_) {}
        });
        row.addEventListener("dragend", () => { row.classList.remove("dragging"); clearDropCues(container); _drag = null; });
      });
      // Drop targets: squad rows (add member) and member rows (reorder within
      // the same squad, else add to that member's squad). Router rows excluded.
      container.querySelectorAll(".fleet-row.kind-squad, .fleet-row.kind-leaderless, .fleet-row.kind-member, .fleet-row.kind-leader, .fleet-row.kind-sole").forEach(row => {
        row.addEventListener("dragover", (e) => {
          if (!_drag) return;
          e.preventDefault(); e.dataTransfer.dropEffect = "move";
          clearDropCues(container); row.classList.add("drop-target");
        });
        row.addEventListener("dragleave", () => row.classList.remove("drop-target"));
        row.addEventListener("drop", (e) => {
          e.preventDefault();
          if (!_drag) return;
          // Resolve the target squad name: a squad row's ref-name IS the squad;
          // a leader/member/sole row carries squadName in the node — read from
          // the nodes array by data-node-idx.
          const node = nodes[Number(row.dataset.nodeIdx)];
          const sqName = (row.classList.contains("kind-squad") || row.classList.contains("kind-leaderless"))
            ? row.dataset.refName : (node && node.squadName);
          if (!sqName) { clearDropCues(container); _drag = null; return; }
          // Never mutate the auto-managed router squad (its member renders as a
          // .kind-sole row, so it is otherwise a valid drop target).
          const tsq = model.squads.find(s => s.name.toLowerCase() === sqName.toLowerCase());
          if (!tsq || tsq.kind === "router") { clearDropCues(container); _drag = null; return; }
          const sameSquadReorder = _drag.kind === "member" && _drag.squadName
            && sqName.toLowerCase() === _drag.squadName.toLowerCase()
            && node && node.kind === "member" && typeof onReorder === "function";
          if (sameSquadReorder) onReorder(sqName, _drag.idx, node.memberIdx);
          else if (typeof onDropMember === "function") onDropMember(sqName, _drag.name);
          clearDropCues(container); _drag = null;
        });
      });
    }
  }

  window.FleetTree = { render };
})();
