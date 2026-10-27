# Claude Desktop look & feel — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restyle the Omnis web UI to Claude Desktop's look: cream/terracotta palette (light + dark), system sans-serif, centred narrow chat with flat answers, one rounded composer with a "＋" menu and a single send/stop/steer button, a sober sidebar.

**Architecture:** Layout and shape changes go through new design tokens in `web/css/features/common.css` (with defaults so every theme keeps working) and the feature partials that consume them. Colours live in `web/css/themes/terracotta.css` (re-coloured) and a new `web/css/themes/terracotta-dark.css`. The composer gets a small HTML restructure in the `#chat-pane-tpl` template plus one pure state function (`composerButtonMode`) in a new `web/composer-state.js`, unit-tested with `node --test`.

**Tech Stack:** Plain CSS (no build), vanilla JS (`web/app.js`, `web/settings.js`), Node ≥ 18 built-in test runner for the two new JS/CSS guard tests.

**Spec:** `docs/superpowers/specs/2026-10-27-claude-desktop-look-design.md`

## Global Constraints

- Plain CSS; shared values are tokens in `web/css/features/common.css`; theme files override tokens only (`web/css/CLAUDE.md`).
- Every new token has a default in `:root`; no existing theme file needs editing to render correctly.
- System sans-serif stack only; no web font, no font file (offline).
- Theme id `terracotta` kept (persisted prefs); new theme id `terracotta-dark`, label "Terracotta Dark", tier `principal`, tone `Dark`.
- Target colours (tune for ≥ 4.5:1 body text): light main `#faf9f5`, sidebar `#f5f4ed`, text `#3d3d3a`, muted `#73726c`, accent `#d97757`; dark main `#262624`, sidebar `#1f1e1d`, same accent.
- Chat column max width `--chat-max-w: 720px` on desktop; phone band (`< 820px`) keeps 100 %.
- Radii: `--radius-sm: 8px`, `--radius-md: 12px`, `--radius-composer: 18px`.
- No behaviour removed: attach, add context, Enter mode, `/` menu, Stop, Steer, Tab suggestion, skills dock, context ring popup, goal chip, subtle "?" all reachable.
- Keep JS-referenced ids: `#send`, `#cancel`, `#attach-btn`, `#attach-menu`, `#attach-computer`, `#attach-context`, `#edit-mode-btn`, `#slash-btn`, `#new-chat`, `#squad-toggle`.
- New visible strings → `web/i18n/{en,fr,es,de}.json` + `make i18n`; bump `?v=` of every changed script/stylesheet link in `web/index.html`.
- No Anthropic/Claude branding in the UI.

## Review Focus

1. **A third-party dark theme (e.g. Dracula) with the new layout** — user bubble and composer must stay readable; pinned by `tokens.test.mjs` (every new token defined in `:root`) and the Task 5 visual pass.
2. **A background/remote turn (busy, not local)** — the single button must not show an active Stop it cannot honour; pinned in `composer_state.test.mjs`.
3. **Archived session** — button disabled in every mode; pinned in `composer_state.test.mjs`.
4. **Typing during a local turn, then clearing the text** — button switches Steer → Stop live (needs an `input` listener); pinned by test + Task 3 manual step.
5. **Phone width (390 px)** — composer "＋" menu and send button stay ≥ 34 px touch targets and the menu fits on screen; Task 5 visual pass.

## Dev loop (all tasks)

Serve the branch's web assets live (no rebuild needed for web files):

```bash
OMNIS_WEB_DIR=$(pwd)/web OMNIS_SERVER_TOKEN= go run ./server
```

Open `http://localhost:8080/` in the in-app browser. Hard-reload after each CSS edit.

---

### Task 1: Layout tokens, centred column, flat messages

**Files:**
- Modify: `web/css/features/common.css` (`:root` tokens, body font)
- Modify: `web/css/features/messages.css` (`.bubble-user`, `#prompt-header`, `.msg-row`, `.msg-row.assistant`, `.bubble-assistant`)
- Modify: `web/css/features/composer.css:573` and `web/css/features/dialogs.css:820` (width rules using `--col-w`)
- Modify: `web/css/features/responsive.css` (phone band sets `--chat-max-w: 100%`)
- Create: `web/tests/tokens.test.mjs`
- Modify: `Makefile` (add `web-test` target)

**Interfaces:**
- Produces tokens (all with `:root` defaults): `--chat-max-w`, `--radius-sm`, `--radius-md`, `--radius-composer`, `--user-bubble-bg` (default `var(--bubble-user-bg)`), `--user-bubble-fg` (default `var(--text)`), `--font-ui` (system stack), `--answer-font-size` (`15px`), `--answer-line-height` (`1.6`).
- Produces `make web-test` = `node --test web/tests/`.

- [ ] **Step 1: Write the failing test** `web/tests/tokens.test.mjs` — reads `web/css/features/common.css`, extracts the first `:root { … }` block, and for each name in `["--chat-max-w","--radius-sm","--radius-md","--radius-composer","--user-bubble-bg","--user-bubble-fg","--font-ui","--answer-font-size","--answer-line-height"]` asserts `new RegExp(name + "\\s*:")` matches. Second test: every `var(--x)` used in `web/css/features/messages.css` and `composer.css` names a token defined somewhere in `common.css` (allowing fallbacks `var(--x, …)`).
- [ ] **Step 2: Run** `node --test web/tests/` — Expected: FAIL listing the missing tokens.
- [ ] **Step 3: Add the tokens** to `:root` in `common.css`; set `body { font-family: var(--font-ui) }` with `--font-ui: system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`. Add the `web-test` Makefile target.
- [ ] **Step 4: Restyle messages.** Column: every `min(var(--col-w), 100%)`-style width becomes `min(var(--chat-max-w), 100%)`, centred (`margin-inline: auto`). User row: right-aligned, `.bubble-user` = `--user-bubble-bg`, `--radius-md`, no border, no shadow, max-width 80 %. Assistant: `.msg-row.assistant` loses its 56 px recess; `.bubble-assistant` loses background/border/shadow/padding-inline, uses `--answer-font-size`/`--answer-line-height`. `#prompt-header` (shares the `.bubble-user` rule at line 5–6) gets the same quiet look but keeps its sticky behaviour. Phone band in `responsive.css`: `--chat-max-w: 100%`.
- [ ] **Step 5: Run** `node --test web/tests/` → PASS; then visual check in the browser on a session with a table + code block (column centred ≤ 720 px, answer unframed, question right-aligned).
- [ ] **Step 6: Commit** `feat(web): centred chat column and flat messages` (bump `styles.css?v=`).

### Task 2: Terracotta light palette + Terracotta Dark theme

**Files:**
- Modify: `web/css/themes/terracotta.css` (token values; delete the scoped `#sidebar` shadow and `#sidebar-resize` gradient rules)
- Create: `web/css/themes/terracotta-dark.css`
- Modify: `web/index.html` (link the new theme after `terracotta.css`)
- Modify: `web/settings.js:128` (`THEMES` entry + swatch update)
- Modify: `web/tests/tokens.test.mjs`

**Interfaces:**
- Consumes: Task 1 tokens.
- Produces: `[data-theme="terracotta-dark"]` with `color-scheme: dark`.

- [ ] **Step 1: Extend the test** — for both `terracotta.css` and `terracotta-dark.css`: file exists, declares `color-scheme`, and defines `--bg`, `--bg-panel`, `--text`, `--text-muted`, `--accent`, `--border`, `--user-bubble-bg`, `--bg-input`. Assert `settings.js` contains `id: "terracotta-dark"` and `index.html` links `themes/terracotta-dark.css`.
- [ ] **Step 2: Run** `node --test web/tests/` → FAIL (dark theme missing).
- [ ] **Step 3: Re-colour `terracotta.css`** with the Global-Constraints targets: `--bg #faf9f5`, `--bg-panel #f5f4ed`, `--bg-input #ffffff`, `--border` warm hairline ≈ `#e8e6dc`, `--text #3d3d3a`, `--text-strong #1f1e1b`, `--text-muted #73726c`, `--accent #d97757` (+ hover/strong/soft variants), `--user-bubble-bg #f0eee6`, `--bg-active #e8e6dc` (selected rows). Remove the sidebar shadow + resize-gradient rules so only the 1 px border separates sidebar and chat. Update its swatch in `THEMES` to `["#faf9f5","#f5f4ed","#d97757","#3d3d3a"]`.
- [ ] **Step 4: Create `terracotta-dark.css`** mirroring the same token list: `--bg #262624`, `--bg-panel #1f1e1d`, `--bg-input #30302e`, `--border #3a3936`, `--text #e5e3dc`, `--text-muted #a3a199`, `--accent #d97757`, `--user-bubble-bg #141413`, `--bg-active #33322f`, danger/warn variants readable on dark. Add the `THEMES` entry `{ id: "terracotta-dark", label: "Terracotta Dark", tier: "principal", tone: "Dark", swatch: ["#262624","#1f1e1d","#d97757","#e5e3dc"] }` and the `<link>`.
- [ ] **Step 5: Run** tests → PASS; switch both themes in Settings → Appearance and check: no seam shadow, no flash on reload, contrast OK.
- [ ] **Step 6: Commit** `feat(web): Claude-style Terracotta palette and Terracotta Dark theme` (bump `settings.js?v=`).

### Task 3: Composer — "＋" menu and single send/stop/steer button

**Files:**
- Create: `web/composer-state.js`
- Create: `web/tests/composer_state.test.mjs`
- Modify: `web/index.html` (composer markup in `#chat-pane-tpl`; `<script>` for `composer-state.js` before `app.js`)
- Modify: `web/app.js` (`setSendButtonMode` → driven by `composerButtonMode`; `applySessionUI` ~line 1749; `attachPaneHandlers` attach/edit/slash wiring ~lines 1222–1310; outside-click close ~line 11680)
- Modify: `web/css/features/composer.css`
- Modify: `web/i18n/{en,fr,es,de}.json`, then `make i18n`

**Interfaces:**
- Produces `composerButtonMode({ hasText: boolean, localActive: boolean, busy: boolean, archived: boolean }) → "send" | "steer" | "stop" | "stop-disabled"`, exposed as `window.composerButtonMode` and `module.exports` (guarded `typeof module`).
- Consumes: existing `steerMessage`, `sendMessage`, Stop handler on `#cancel`.

- [ ] **Step 1: Write the failing test** `composer_state.test.mjs` (`createRequire` to load the file):
  - idle, any text → `"send"`
  - `busy && localActive && hasText` → `"steer"`
  - `busy && localActive && !hasText` → `"stop"`
  - `busy && !localActive && hasText` → `"steer"` (remote turn, steering allowed)
  - `busy && !localActive && !hasText` → `"stop-disabled"`
  - `composerButtonMode({archived:true, busy:true, localActive:true, hasText:false}) === "send"` (archived short-circuits; the caller disables the button).
- [ ] **Step 2: Run** `node --test web/tests/` → FAIL (module not found).
- [ ] **Step 3: Implement `composerButtonMode`** in `web/composer-state.js` (archived short-circuits to `"send"`).
- [ ] **Step 4: Restructure the markup.** In `.composer-actions`: `#attach-wrap` becomes the "＋" control — `#attach-btn` shows a plus icon, `#attach-menu` gains two more items, `#attach-editmode` (label = current Enter mode) and `#attach-slash` ("Slash commands"); `#edit-mode-btn` and `#slash-btn` stay in the DOM but `hidden` (their click handlers remain the single implementation; the menu items call `.click()` on them). Indicator pills (`#ctx-ring-wrap`, `.subtle-ask-btn`) follow, then `#status` as small muted text, then one round `#send` (arrow icon, `aria-label` from i18n). `#cancel` stays in the DOM, `hidden`, and is triggered by `#send` in stop mode.
- [ ] **Step 5: Wire it in `app.js`.** `setSendButtonMode(panel)` reads `hasText = panel.els.prompt.value.trim() !== ""`, the session's `localActive/busy/archived`, calls `composerButtonMode`, sets `data-mode` on `#send`, its icon (arrow / square / steer arrow), `aria-label` + `data-tip`, and `disabled` for `"stop-disabled"` and archived. A click on `#send` in `stop` mode calls `panel.els.cancel.click()` and does not submit. Add a `prompt` `input` listener calling `setSendButtonMode(panel)`. Remove the separate Stop/Send text labels.
- [ ] **Step 6: Style.** `#composer` = `--composer-bg`, 1 px `--border`, `--radius-composer`, shadow `0 1px 6px rgba(0,0,0,.04)` (no peach, no accent ring at rest; focus = border `--accent-border-soft`). `#send` 32 px circle `--accent`, white icon; `stop` mode = `--text` fill. Pills: 11–12 px muted, `--bg-panel` fill, full radius. Skills dock and goal chip keep their position above the box but adopt the pill style.
- [ ] **Step 7: i18n.** Add `composer.plus` ("More actions"), `composer.slashItem` ("Slash commands"), `composer.enterSends` ("Enter sends"), `composer.enterNewline` ("Enter adds a new line"), `composer.sendAria` ("Send"), `composer.stopAria` ("Stop") in en/fr/es/de; `make i18n`.
- [ ] **Step 8: Run** `node --test web/tests/` → PASS; `make i18n` prints no missing-key warning; manual: attach a file, add context, toggle Enter mode via menu, `/` from menu and from typing, send, Stop mid-turn, type during a turn → button becomes Steer and steers, clear text → back to Stop, Tab suggestion, context ring popup.
- [ ] **Step 9: Commit** `feat(web): Claude-style composer with plus menu and single send button` (bump `app.js?v=`, `i18n/locales.js?v=`, `styles.css?v=`).

### Task 4: Sober sidebar

**Files:**
- Modify: `web/css/features/sidebar.css` (`#new-chat-wrap`, `#new-chat`, `#squad-toggle`, `.sidebar-action`, brand header, account button)
- Modify: `web/css/features/collections.css` (row hover/selected, group header, counts, section labels)
- Modify: `web/index.html` only if `#new-chat` needs a class change (keep ids)

**Interfaces:**
- Consumes: Task 1/2 tokens (`--bg-active`, `--radius-sm`).

- [ ] **Step 1: New Chat row.** `#new-chat` styled exactly like `.sidebar-action` (transparent, `--text`, plus icon, `--radius-sm` hover fill `--bg-raised-hover`), placed first in that list; `#squad-toggle` becomes a small chevron at the row's right end, visible on hover/focus and always on touch (`pointer: coarse`), `#squad-menu` unchanged.
- [ ] **Step 2: Rows.** Selected session row = `--bg-active` fill + `--radius-sm`, the left rail replaced by a 6 px dot in `--col-accent` (folder glyph tint unchanged); hover = `--bg-raised-hover`. Section labels (`.sidebar-section-label`) 12 px, `--text-muted`, normal case, no letter-spacing. Counts `--text-faint`. Brand logo 20 px, title weight 600, no uppercase.
- [ ] **Step 3: Verify** in browser (light, dark, Dracula): New chat, squad chooser, collapsed rail, bulk select, phone drawer.
- [ ] **Step 4: Commit** `feat(web): sober Claude-style sidebar`.

### Task 5: Flatten the rest, phone pass, docs

**Files:**
- Modify: `web/css/features/tools.css`, `panes.css`, `dialogs.css`, `notifications.css`, `web/css/settings/*.css` (shadows → none except popovers/modals; radii → tokens)
- Modify: `web/css/features/responsive.css` (touch sizes for `#attach-btn`, `#send`)
- Modify: `internal/features/FEATURES.md` (2.1 in-development bullet)
- Modify: `web/docs/` page describing the composer (grep `Stop` / `attach` in `web/docs/*.md`)
- Modify: `CLAUDE.md` (Desktop notifications/Mid-turn steering mentions of the Send/Steer button; theme default note) and `web/css/CLAUDE.md` (new tokens)

- [ ] **Step 1: Flatten.** Tool blocks, code blocks, tables, `.ui-modal*`, settings cards, pane tab bar: drop `box-shadow` (keep it on `#account-menu`, `#slash-menu`, `#attach-menu`, `.ui-modal-*` dialog, `#settings-modal` dialog, toasts), use `--radius-sm`/`--radius-md`, hairline `--border`. Active pane tab = text `--text` + 2 px bottom border in `var(--col-accent, var(--accent))`, no filled tab box.
- [ ] **Step 2: Phone pass** at 390 × 844 (`resize_window` mobile preset): composer box, "＋" menu fits on screen, `#send` ≥ 34 px, drawer, single pane, settings dialog.
- [ ] **Step 3: Theme sweep:** Terracotta, Terracotta Dark, Dracula, GitHub Light, VS Code Dark — screenshot each on the same long answer; nothing unreadable.
- [ ] **Step 4: Docs.** FEATURES.md bullet: `- **Claude-style look** — a calmer cream and terracotta design (light and dark), a centred chat column, and a single rounded composer whose ＋ menu holds attachments, context and commands.` Update the composer section of the user doc and the CLAUDE.md lines above.
- [ ] **Step 5: Run** `make web-test && make test` → PASS.
- [ ] **Step 6: Commit** `feat(web): flatter blocks and dialogs; docs for the Claude-style look`.
