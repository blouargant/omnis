# Appearance & Themes

Omnis ships with a curated set of color themes, applied instantly and persisted
both client-side (`localStorage`) and server-side (`preferences.json`).

## Picking a theme

Open **Settings → Appearance**, click a swatch. The theme is applied to the
running tab immediately and synced to the server so it survives reloads and
follows you across browsers signed in with the same token.

## Tiers

- **Principal themes** — curated, default-quality palettes (VS Code Dark/Light,
  GitHub Dark/Light, One Dark). Tested across every panel and chrome state.
- **Secondary themes** — well-known community palettes (Dracula, Nord, Tokyo
  Night, Solarized Dark/Light, Monokai, Gruvbox Dark). Maintained but less
  exhaustively tuned.

Each tier is grouped by tone (Dark / Light) in the picker.

## Adding a custom theme

Themes are plain CSS files under `web/css/themes/<id>.css` keyed by a
`[data-theme="<id>"]` selector. To add one:

1. Copy an existing file (e.g. `one-dark.css`) and rename it.
2. Override the CSS custom properties (`--bg`, `--text`, `--accent`, etc.).
3. Add a `<link>` tag in `web/index.html` and an entry in the `THEMES` array
   in `web/settings.js` with a swatch quad for the preview card.

A page reload picks it up.
