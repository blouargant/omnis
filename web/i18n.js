// i18n runtime — omnis Web UI string localisation.
//
// Loaded (deferred) BEFORE app.js / settings.js, so `tr()` exists by the time
// those scripts render. The catalogue data lives in the synchronously-loaded
// `web/i18n/locales.js` bundle (window.OMNIS_I18N = { en:{…}, fr:{…}, … }),
// generated from web/i18n/<locale>.json by `make i18n`.
//
// These are classic (non-module) scripts, so everything assigned to `window`
// here is visible to app.js / settings.js, and conversely this file may call
// their globals (authHeaders, BASE_PATH) — but only from `setLocale`, which
// runs at click time, long after app.js has loaded.
(function () {
  "use strict";

  const STORAGE_KEY = "agent_toolkit_locale";
  // Guard so a cross-device reconcile reload (server locale ≠ local cache) can
  // only fire once per tab session — mirrors the notification-resync guard.
  const RESYNC_FLAG = "agent_toolkit_locale_resynced";

  // Available UI languages. `id` must match a key in window.OMNIS_I18N and a
  // <locale>.json catalogue file. English is the base/fallback.
  const LOCALES = [
    { id: "en", label: "English" },
    { id: "fr", label: "Français" },
    { id: "es", label: "Español" },
    { id: "de", label: "Deutsch" },
  ];
  const DEFAULT_LOCALE = "en";
  const ids = LOCALES.map((l) => l.id);

  const catalogs = (window.OMNIS_I18N && typeof window.OMNIS_I18N === "object")
    ? window.OMNIS_I18N
    : {};

  // Resolve the active locale: explicit local choice → browser language (first
  // run only) → English. The server-side preference is reconciled later by
  // settings.js (syncThemeFromServer), which reloads once on a mismatch.
  function resolveLocale() {
    let v = null;
    try { v = localStorage.getItem(STORAGE_KEY); } catch (_) { /* ignore */ }
    if (v && ids.includes(v)) return v;
    if (v === null) {
      // First run: try to honour the browser's preferred language.
      const navs = (navigator.languages && navigator.languages.length)
        ? navigator.languages
        : [navigator.language || ""];
      for (const tag of navs) {
        const prefix = String(tag).toLowerCase().split("-")[0];
        if (ids.includes(prefix)) return prefix;
      }
    }
    return DEFAULT_LOCALE;
  }

  let locale = resolveLocale();
  try { document.documentElement.lang = locale; } catch (_) { /* ignore */ }

  // Look up a key in the active locale, falling back to English, then to the
  // key itself (so a missing string degrades to a visible, greppable id rather
  // than `undefined`).
  function lookup(key) {
    const active = catalogs[locale];
    if (active && typeof active[key] === "string") return active[key];
    const en = catalogs[DEFAULT_LOCALE];
    if (en && typeof en[key] === "string") return en[key];
    return key;
  }

  // Replace {name} placeholders from `vars`. Missing vars are left as-is.
  function interpolate(str, vars) {
    if (!vars) return str;
    return str.replace(/\{(\w+)\}/g, (m, name) =>
      Object.prototype.hasOwnProperty.call(vars, name) ? String(vars[name]) : m
    );
  }

  // tr("area.key", { name: "x" }) → localised, interpolated string.
  function tr(key, vars) {
    return interpolate(lookup(key), vars);
  }

  // Plural form: catalogues store "<key>.one" / "<key>.other" (and "few"/"many"
  // /"two"/"zero" where a locale needs them); `count` is auto-added to vars as
  // {count}. Falls back to "<key>.other", then the bare key.
  let pluralRules;
  function plural(n) {
    try {
      if (!pluralRules) pluralRules = new Intl.PluralRules(locale);
      return pluralRules.select(n);
    } catch (_) { return n === 1 ? "one" : "other"; }
  }
  function trN(key, count, vars) {
    const cat = plural(count);
    const active = catalogs[locale] || {};
    const en = catalogs[DEFAULT_LOCALE] || {};
    let str =
      active[key + "." + cat] ?? active[key + ".other"] ??
      en[key + "." + cat] ?? en[key + ".other"] ?? key;
    return interpolate(str, Object.assign({ count: count }, vars || {}));
  }

  // translateDom(root): apply catalogue strings to static markup. Elements opt
  // in via data-i18n (textContent) and data-i18n-{tip,placeholder,aria-label,
  // title,value} (the matching attribute). The English text stays in the markup
  // as a no-JS fallback; this overwrites it with the active locale. Safe to call
  // on a DocumentFragment (cloned pane template) before it is inserted.
  const ATTR_MAP = {
    "data-i18n-tip": "data-tip",
    "data-i18n-placeholder": "placeholder",
    "data-i18n-aria-label": "aria-label",
    "data-i18n-title": "title",
    "data-i18n-value": "value",
  };
  function translateDom(root) {
    if (!root || !root.querySelectorAll) return;
    root.querySelectorAll("[data-i18n]").forEach((el) => {
      el.textContent = tr(el.getAttribute("data-i18n"));
    });
    // data-i18n-html: the catalogue value carries inline markup (e.g. <code>$1</code>).
    // Trusted, authored content — never user input — so innerHTML is safe here.
    root.querySelectorAll("[data-i18n-html]").forEach((el) => {
      el.innerHTML = tr(el.getAttribute("data-i18n-html"));
    });
    for (const [dataAttr, target] of Object.entries(ATTR_MAP)) {
      root.querySelectorAll("[" + dataAttr + "]").forEach((el) => {
        el.setAttribute(target, tr(el.getAttribute(dataAttr)));
      });
    }
    // The root element itself may carry annotations (querySelectorAll excludes it).
    if (root.nodeType === 1) {
      if (root.hasAttribute && root.hasAttribute("data-i18n")) {
        root.textContent = tr(root.getAttribute("data-i18n"));
      }
      if (root.hasAttribute && root.hasAttribute("data-i18n-html")) {
        root.innerHTML = tr(root.getAttribute("data-i18n-html"));
      }
      for (const [dataAttr, target] of Object.entries(ATTR_MAP)) {
        if (root.hasAttribute && root.hasAttribute(dataAttr)) {
          root.setAttribute(target, tr(root.getAttribute(dataAttr)));
        }
      }
    }
  }

  // Persist the chosen locale (local cache + server preferences) and reload so
  // every already-rendered string is rebuilt in the new language.
  function setLocale(id) {
    if (!ids.includes(id)) return;
    try { localStorage.setItem(STORAGE_KEY, id); } catch (_) { /* ignore */ }
    // Best-effort server persistence; reload regardless so the choice applies.
    try {
      const headers = (typeof authHeaders === "function")
        ? authHeaders({ "Content-Type": "application/json" })
        : { "Content-Type": "application/json" };
      fetch((window.BASE_PATH || "") + "/api/preferences", {
        method: "PUT",
        headers: headers,
        body: JSON.stringify({ locale: id }),
      }).catch(() => { /* offline / unauthenticated — local cache wins */ });
    } catch (_) { /* ignore */ }
    // Clear the resync guard so the next boot doesn't think it already reconciled.
    try { sessionStorage.removeItem(RESYNC_FLAG); } catch (_) { /* ignore */ }
    location.reload();
  }

  // reconcileServerLocale(serverLocale): called from settings.js after it loads
  // the server preferences. If the server has a different locale than the local
  // cache, adopt it and reload once (guarded) so a second browser/device
  // converges to the saved choice. Returns true if it triggered a reload.
  function reconcileServerLocale(serverLocale) {
    if (!serverLocale || !ids.includes(serverLocale)) return false;
    if (serverLocale === locale) return false;
    let already = null;
    try { already = sessionStorage.getItem(RESYNC_FLAG); } catch (_) { /* ignore */ }
    if (already) return false;
    try {
      sessionStorage.setItem(RESYNC_FLAG, "1");
      localStorage.setItem(STORAGE_KEY, serverLocale);
    } catch (_) { /* ignore */ }
    location.reload();
    return true;
  }

  window.tr = tr;
  window.trN = trN;
  window.I18N = {
    get locale() { return locale; },
    LOCALES: LOCALES,
    DEFAULT_LOCALE: DEFAULT_LOCALE,
    t: tr,
    trN: trN,
    setLocale: setLocale,
    translateDom: translateDom,
    reconcileServerLocale: reconcileServerLocale,
  };

  // Translate the static markup now. Deferred execution guarantees the document
  // is fully parsed, so document.body exists.
  translateDom(document.body);
})();
