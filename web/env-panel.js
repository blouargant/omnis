// Environment panel — the right-hand column of cards (Claude Desktop style)
// that keeps everything auxiliary to the conversation out of the chat: where
// the agent works, context use, loaded skills, connectors, produced files, git
// changes and the file browser. One panel for the window; it shows the
// focused pane's session (app.js calls EnvPanel.setSession from
// setFocusedPanel). Cards are registered by app.js; each renders itself.
//
// A card: { id, title(): string, sessionOnly?: bool,
//           render(body, ctx) } where ctx = { sessionId, open, setValue(html),
//           setVisible(bool), stale() } — an async render must stop when
//           ctx.stale() turns true (the panel moved to another session).
(function () {
  const OPEN_KEY = "agent_env_open";
  const CARDS_KEY = "agent_env_cards";
  const cards = [];
  let session = null;
  let generation = 0;

  function load(key, def) {
    try { const v = localStorage.getItem(key); return v == null ? def : JSON.parse(v); }
    catch (_) { return def; }
  }
  function save(key, v) { try { localStorage.setItem(key, JSON.stringify(v)); } catch (_) {} }

  const root = () => document.getElementById("env-panel");
  const list = () => root() && root().querySelector(".env-cards");
  const isMobile = () => window.matchMedia("(max-width: 820px)").matches;

  function isOpen() {
    return isMobile() ? document.body.classList.contains("env-drawer-open")
                      : document.body.classList.contains("env-open");
  }
  function setOpen(open) {
    if (isMobile()) {
      document.body.classList.toggle("env-drawer-open", open);
    } else {
      document.body.classList.toggle("env-open", open);
      save(OPEN_KEY, open);
    }
    for (const b of document.querySelectorAll(".pane-env-btn")) b.classList.toggle("active", open);
    refresh(); // cards follow the panel (e.g. the file browser stops listening when hidden)
  }
  function toggle() { setOpen(!isOpen()); }

  function cardOpen(id, def) { const m = load(CARDS_KEY, {}); return id in m ? !!m[id] : def; }
  function setCardOpen(id, open) { const m = load(CARDS_KEY, {}); m[id] = open; save(CARDS_KEY, m); }

  function buildCard(card) {
    const el = document.createElement("section");
    el.className = "env-card";
    el.dataset.card = card.id;
    el.innerHTML =
      `<button type="button" class="env-card-head" aria-expanded="false">` +
        `<span class="env-card-title"></span><span class="env-card-chevron" aria-hidden="true">›</span>` +
        `<span class="env-card-value"></span>` +
      `</button><div class="env-card-body"></div>`;
    el.querySelector(".env-card-title").textContent = card.title();
    el.querySelector(".env-card-head").addEventListener("click", () => {
      const open = !el.classList.contains("open");
      setCardOpen(card.id, open);
      renderCard(card, el);
    });
    return el;
  }

  function renderCard(card, el) {
    const open = cardOpen(card.id, card.defaultOpen !== false);
    el.classList.toggle("open", open);
    el.querySelector(".env-card-head").setAttribute("aria-expanded", String(open));
    el.querySelector(".env-card-title").textContent = card.title();
    if (card.sessionOnly && !session) { el.hidden = true; return; }
    el.hidden = false;
    const gen = generation;
    const sid = session;
    const ctx = {
      sessionId: sid,
      open,
      stale: () => gen !== generation || sid !== session,
      setValue: (html) => { if (!ctx.stale()) el.querySelector(".env-card-value").innerHTML = html || ""; },
      setVisible: (v) => { if (!ctx.stale()) el.hidden = !v; },
    };
    try {
      const r = card.render(el.querySelector(".env-card-body"), ctx);
      if (r && typeof r.catch === "function") r.catch((e) => console.error("env card", card.id, e));
    } catch (e) { console.error("env card", card.id, e); }
  }

  function ensureBuilt() {
    const l = list();
    if (!l) return null;
    for (const card of cards) {
      if (!l.querySelector(`[data-card="${card.id}"]`)) l.appendChild(buildCard(card));
    }
    return l;
  }

  // refresh re-renders one card (by id) or all of them, for the current session.
  function refresh(id) {
    const l = ensureBuilt();
    if (!l) return;
    for (const card of cards) {
      if (id && card.id !== id) continue;
      renderCard(card, l.querySelector(`[data-card="${card.id}"]`));
    }
  }

  function setSession(sid) {
    if (sid === session) return;
    session = sid || null;
    generation++;
    refresh();
  }

  function register(card) {
    cards.push(card);
    if (list()) refresh(card.id);
  }

  window.EnvPanel = {
    register, refresh, setSession, toggle, setOpen, isOpen,
    currentSession: () => session,
    // openCard unfolds a card (and the panel) and scrolls it into view.
    openCard(id) {
      setCardOpen(id, true);
      if (!isOpen()) setOpen(true); else refresh(id);
      const el = list() && list().querySelector(`[data-card="${id}"]`);
      if (el) el.scrollIntoView({ block: "nearest" });
    },
    init() {
      if (!isMobile()) document.body.classList.toggle("env-open", load(OPEN_KEY, true));
      for (const b of document.querySelectorAll(".pane-env-btn")) b.classList.toggle("active", isOpen());
      const scrim = document.getElementById("env-scrim");
      if (scrim) scrim.addEventListener("click", () => setOpen(false));
      refresh();
    },
  };
})();
