# CLAUDE.md — web/css

Guidance for AI sessions (and humans) working on the omnis web UI stylesheets.
This directory holds **all** the CSS for the web UI. It is organised as two
**barrels** of feature **partials** plus a folder of **theme** palettes.

## Layout

```
web/css/
├── styles.css        ← barrel: @imports features/*.css (chat surface)
├── settings.css      ← barrel: @imports settings/*.css (Settings panel)
├── features/         ← chat-surface partials (one feature per file)
├── settings/         ← Settings-panel partials (one panel/feature per file)
└── themes/           ← per-theme palette overrides (one file per theme)
```

- **`styles.css` and `settings.css` are barrels** — they contain only a header
  comment and a list of `@import "<dir>/<file>.css";` lines. **Do not put rules
  in them.** Add/edit rules in the partial that owns the feature.
- **Import order = cascade order.** The `@import` order inside each barrel is
  the order the browser applies the rules. `features/common.css` must stay
  **first** in `styles.css` (it defines the design tokens everything else
  consumes). When two partials could style the same element, the later import
  wins on equal specificity — preserve order when moving things.
- Both barrels are linked from [`../index.html`](../index.html) with a
  cache-busting `?v=N` query. **Bump that `?v=` when you change the import list**
  (so the new barrel is refetched). The partials themselves revalidate via
  `Last-Modified` and don't carry a version.
- Themes load **after** both barrels in `index.html`; each `themes/*.css`
  overrides the `:root` / `[data-theme="…"]` tokens defined in
  `features/common.css`.

## Design tokens (the shared "common" file)

The single source of shared data is **`features/common.css`**: the CSS reset,
the `:root` colour/size **design tokens** (`--bg`, `--bg-panel`, `--border`,
`--text`, `--accent`, `--danger-fg`, `--scroll-thumb`, …), the `html`/`body`
base, and the global scrollbar. **Both** barrels rely on it — `styles.css`
imports it first, and `settings.css`'s partials use the same `var(--…)` tokens
(it loads after `styles.css` in `index.html`, so the tokens are already defined).

**If a value needs to be shared across features, add it as a token in
`features/common.css`** and reference it with `var(--…)` — do not hard-code the
same colour/size in multiple partials.

## How to find the right CSS

1. **Know the class/id?** Grep for it — that's the fastest route:
   `grep -rn "\.my-class" web/css/`. Selectors are not duplicated across
   partials, so the one hit is the file to edit.
2. **Know the feature but not the selector?** Use the maps below.
3. **Adding something brand new?** Put it in the partial for the closest
   existing feature; only create a new partial for a genuinely new feature
   area (see "Adding a new partial").

### `features/` — chat surface (barrel: `styles.css`)

| File | Owns |
|---|---|
| `common.css` | **Shared:** reset, theme-palette tokens (`:root` vars), `html`/`body` base, global scrollbar |
| `sidebar.css` | Left sidebar, hover tooltip (`#tip-layer`), collapsed icon rail, New-Chat split-button, session rows, archived-sessions panel, sidebar resize handle |
| `folders.css` | Folders browser panel + its right-click context menu |
| `panes.css` | Chat-pane layout, pane dividers, per-pane tab bar / tabs, empty-pane picker |
| `editor.css` | Monaco file-editor tabs (`.pane-editor`, `.monaco-*`) |
| `terminal.css` | xterm.js terminal tabs (`.pane-terminal`, `.xterm-*`) |
| `messages.css` | User bubble / pinned prompt, transcript, message rows, assistant markdown, copy button, error, inline images, `@file` ref links |
| `tools.css` | Tool-call blocks, todo plan widget, bash `!`-escape block, badge colour variants |
| `composer.css` | Composer input, attach button, attachment chips, attach popup menu, slash-command menu |
| `context.css` | Context ring, context popup, context browser modal |
| `dialogs.css` | User-command modal, push-notification banner, ask-user wizard card |
| `notifications.css` | Background-task / monitor toast notifications (`#task-toast-layer`, `.task-toast`) |

### `settings/` — Settings panel (barrel: `settings.css`)

| File | Owns |
|---|---|
| `core.css` | **Settings shell:** `#settings-panel`, header, breadcrumb, hint, tabs, view-toggle, body, footer, raw editor, forms, sub-tabs, combobox |
| `permissions.css` | Permission-rule editor + skill-contributed (read-only) permissions |
| `hooks.css` | Lifecycle hooks editor (Settings → Hooks): matcher cards + command rows |
| `status.css` | Restart-required banner, reload spinner, generation pill, full-page restart overlay |
| `sidebar.css` | Settings **sidebar chrome**: the gear button, section labels, the in-sidebar settings menu that expands when Settings is open |
| `theme.css` | Theme picker |
| `skills.css` | Skills panel |
| `registries.css` | Remote registries split-panel list + browse view + cards |
| `environment.css` | Global Environment panel |
| `models.css` | Models panel, model cards, add-model dialog, embedding-model selector |
| `mcp.css` | MCP server cards + the key/value list editor (`.kv-list`) |
| `agents.css` | Agents panel (master-detail split: fleet list + detail) |
| `docs.css` | Documentation viewer |
| `squads.css` | Squads sub-tab (Agent → Squads) |
| `commands.css` | User Commands settings |

> **Two `sidebar.css` files:** `features/sidebar.css` is the **main app**
> sidebar (session list, folders/archived panels). `settings/sidebar.css` is the
> **Settings**-specific sidebar chrome. Don't confuse them.

## Adding a new partial

1. Create `features/<name>.css` (or `settings/<name>.css`) with a one-line
   header comment describing what it owns.
2. Add an `@import "<dir>/<name>.css";` line to the barrel (`styles.css` /
   `settings.css`) **in the correct cascade position** (group it with related
   imports; keep `common.css` first in `styles.css` and `core.css` first in
   `settings.css`).
3. Bump the barrel's `?v=N` in [`../index.html`](../index.html).
4. Add a row to the matching table in this file (see self-maintenance below).

A partial must be **self-contained and brace-balanced** — never split a single
rule block across two files.

## Self-maintenance rule

**Keep this file in sync with the directory. After any structural change, update
it in the same change** — specifically when you:

- **add, remove, or rename a partial** → update the matching file-map table and,
  if relevant, the `Layout` tree;
- **move a feature's rules between partials** → fix the "Owns" description so the
  grep-or-map lookup still points to the right file;
- **change the import/cascade order** in a barrel in a way worth knowing (e.g. a
  new override dependency) → note it;
- **add a new shared design token** to `features/common.css` → mention it in the
  "Design tokens" section if it's broadly used;
- **add a new barrel or a new top-level folder** under `web/css/` → add it to
  the `Layout` tree and explain its role.

If you change the structure and *don't* see how to describe it here, that's a
sign the split is unclear — prefer a layout that's easy to document. This file
is the single source of truth for where web-UI CSS lives.
