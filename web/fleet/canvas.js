"use strict";
// FleetCanvas — pure delegation-graph layout + SVG renderer for the Fleet minimap.
// Dual-export: CommonJS (node --test) + browser global (window.FleetCanvas).
(function (root, factory) {
  const store = (typeof module !== "undefined" && module.exports)
    ? require("./store.js")
    : (typeof window !== "undefined" ? window.FleetStore : null);
  const mod = factory(store);
  if (typeof module !== "undefined" && module.exports) module.exports = mod;
  if (typeof window !== "undefined") window.FleetCanvas = mod;
})(this, function (FleetStore) {
  // Geometry (compact "strip" — squads laid out left→right; nodes stack down a column).
  const COL_W = 150, ROW_H = 26, INDENT = 16, PAD = 12, NODE_R = 5;

  function layout(model, opts) {
    const o = opts || {};
    const colW = o.colW || COL_W, rowH = o.rowH || ROW_H, indent = o.indent || INDENT, pad = o.pad || PAD;
    const nodes = FleetStore.treeNodes(model)
      .filter(n => n.kind !== "unused-header" && n.kind !== "unused");
    const cells = [];
    const edges = [];
    const columns = [];
    let col = -1, colX = pad, row = 0;
    // parents[d] = the last cell seen at depth d in the current column (for edge wiring).
    let parents = [];
    for (const n of nodes) {
      if (n.depth === 0) {                 // a squad row starts a new column
        col++; colX = pad + col * colW; row = 0; parents = [];
        columns.push({ name: n.name, kind: n.kind, x: colX });
      }
      const x = colX + n.depth * indent;
      const y = pad + row * rowH;
      const cell = {
        name: n.name, kind: n.kind, depth: n.depth, col,
        x, y,
        model_ref: (n.agent && n.agent.model_ref) || "",
        shared: n.shared || 0,
      };
      cells.push(cell);
      parents[n.depth] = cell;
      if (n.depth >= 1 && parents[n.depth - 1]) {
        const p = parents[n.depth - 1];
        edges.push({ x1: p.x, y1: p.y, x2: x, y2: y });
      }
      row++;
    }
    // Dimensions: widest column extent + a row budget tall enough for the tallest column.
    const maxRowsByCol = {};
    cells.forEach(c => { maxRowsByCol[c.col] = Math.max(maxRowsByCol[c.col] || 0, (c.y - pad) / rowH + 1); });
    const tallest = Object.values(maxRowsByCol).reduce((a, b) => Math.max(a, b), 0);
    const width = columns.length ? pad * 2 + columns.length * colW : 0;
    const height = cells.length ? pad * 2 + tallest * rowH : 0;
    return { cells, edges, width, height, columns };
  }

  const GLYPH = { router: "◇", squad: "⬢", leaderless: "⬡", leader: "★",
    member: "•", sole: "◆", subagent: "↳" };

  function esc(s) { return (typeof window !== "undefined" && window.escHtml)
    ? window.escHtml(s)
    : String(s == null ? "" : s).replace(/[&<>"]/g, c => ({ "&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;" }[c])); }
  function t(k, v) { return (typeof window !== "undefined" && window.tr) ? window.tr(k, v) : k; }

  function render(container, model, opts) {
    const lay = layout(model, opts);
    if (!lay.cells.length) {
      container.innerHTML = `<div class="fleet-cv-empty">${esc(t("fleet.canvas.empty"))}</div>`;
      return;
    }
    const edges = lay.edges.map(e =>
      `<line class="fleet-cv-edge" x1="${e.x1}" y1="${e.y1}" x2="${e.x2}" y2="${e.y2}"/>`).join("");
    const nodes = lay.cells.map(c => {
      const label = c.name.length > 14 ? c.name.slice(0, 13) + "…" : c.name;
      const tip = c.model_ref ? `${c.name} · ${c.model_ref}` : c.name;
      return `<g class="fleet-cv-cell kind-${esc(c.kind)}" data-tip="${esc(tip)}" transform="translate(${c.x},${c.y})">
        <circle class="fleet-cv-node" r="5" cx="0" cy="0"/>
        <text class="fleet-cv-glyph" x="10" y="4">${esc(GLYPH[c.kind] || "")}</text>
        <text class="fleet-cv-label" x="22" y="4">${esc(label)}</text>
      </g>`;
    }).join("");
    container.innerHTML =
      `<svg class="fleet-cv-svg" width="${lay.width}" height="${lay.height}" viewBox="0 0 ${lay.width} ${lay.height}" role="img" aria-label="${esc(t("fleet.canvas.title"))}">${edges}${nodes}</svg>`;
  }

  return { layout, render, GLYPH };
});
