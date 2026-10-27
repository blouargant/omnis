# Omnis web UI — Claude Desktop look & feel

Date: 2026-10-27 · Status: approved in conversation, awaiting builder check

## Intent

Make the Omnis web UI look and feel like Claude Desktop: its cream /
terracotta colours, its typography, its centred chat with a rounded composer,
and above all its **sobriety**. The structural work (collection tree, account
menu, settings dialog — see
`2026-10-27-sidebar-tree-settings-modal-design.md`) already landed; what is left
is the visual layer, which today still reads as "busier" than Claude Desktop:
a two-tone sidebar/chat split with a shadow seam, a big orange New Chat pill,
the user prompt in a peach shadowed box, the assistant answer in a framed
bubble spanning ~90 % of the pane, and a composer with a row of bordered icon
buttons plus text Stop/Send buttons.

The reference is the user's own Claude Desktop screenshots (Code tab) and the
approved mockup, variant **A** (`.superpowers/brainstorm/…/look-v1.html`).

## Goals

1. **Palette (light)** — the default theme `terracotta` takes Claude Desktop's
   light palette: one cream family for sidebar and chat (main ≈ `#faf9f5`,
   sidebar a hair darker ≈ `#f5f4ed`), separated only by a 1px warm hairline;
   warm dark-grey text (≈ `#3d3d3a`, muted ≈ `#73726c`); terracotta accent
   ≈ `#d97757` used **sparingly** (active tab underline, send button, focus
   rings, links). The theme id stays `terracotta` so persisted preferences keep
   working; its picker swatch is updated.
2. **Palette (dark)** — a new theme `terracotta-dark` ("Terracotta Dark",
   principal tier, Dark tone): Claude Desktop's dark mode — warm anthracite
   surfaces (main ≈ `#262624`, sidebar ≈ `#1f1e1d`), light warm text, same
   terracotta accent. Registered in `THEMES` (`web/settings.js`) and linked in
   `index.html` like the other theme files.
3. **Typography** — system sans-serif everywhere, assistant answers included
   (variant A; no serif). No downloaded/web font: Omnis must work offline. A
   slightly larger body size and ~1.6 line-height for answers.
4. **Chat layout (all themes)**
   - Centred reading column of fixed max width (≈ 680–720 px), replacing the
     `--col-w: 90%` proportional column on desktop. Messages, tool blocks,
     ask-user card and composer share it.
   - **User message**: a small rounded bubble, right-aligned, neutral fill
     (theme token), no border, no shadow.
   - **Assistant message**: plain text on the page — no bubble, frame, border
     or shadow; no extra left recess.
   - The floating pinned-prompt header keeps working but adopts the same
     quiet bubble look (no heavy peach box + shadow).
5. **Composer (all themes)** — one rounded white (light) / raised (dark) box,
   generous radius (≈ 18 px), very light shadow, border hairline:
   - textarea on top, a single row of controls below;
   - a **"＋" button** on the left opening a menu with: upload a file, add
     context, toggle Enter mode, slash commands (replaces the attach / T / `/`
     icon buttons; the `/` key in the textarea still opens the slash menu);
   - small discreet **pills** stay visible for live indicators: skills dock,
     context ring, goal chip, subtle-ask "?";
   - one **round send button** on the right (arrow icon). While a turn runs it
     becomes **Stop**; the existing mid-turn **Steer** behaviour is kept as a
     state of that same button (when the textarea has text during a run, the
     button steers; empty, it stops);
   - prompt suggestion (Tab) unchanged.
6. **Sidebar sobriety (all themes)** — "New Chat" becomes a plain row
   ("＋ New chat", same style as Search / Import) instead of the orange pill;
   the squad chooser moves next to it as a discreet control or into the draft
   picker (builder's call, must stay reachable). Selected rows = soft rounded
   neutral fill, not an accent rail + tint; section labels small, muted,
   sentence-case is acceptable; counts muted. Logo stays but smaller/quieter.
7. **Everything else flattens** — tool blocks, tables, code blocks, menus,
   dialogs, the settings dialog, toasts: no drop shadows except on floating
   popovers/modals, consistent radii (≈ 8 px small, 12 px cards, 18 px
   composer), hairline borders from theme tokens.

## Non-goals

- No behaviour change: tabs, split panes, collections, files, terminal, editor,
  routing, steering, ask-user flows all work as today.
- No serif font, no bundled font files.
- No "follow the OS" theme: Terracotta Dark is just one more theme to pick
  (user's decision); the default stays `terracotta`.
- No status line under the composer (the mockup's "Squad · /commands ·
  @files" line is not built).
- Other themes (Nord, Dracula, GitHub…) are not re-coloured; they only inherit
  the new layout through the shared tokens.
- Not a copy of Anthropic branding: no Claude logo, no "Claude" name — Omnis
  keeps its own identity.

## Anti-goals (failure even if it "works")

- Losing a function because a button disappeared (attach, `/`, Enter mode,
  Stop, Steer, context ring popup, skills dock must all stay reachable).
- A theme other than terracotta looking broken (unreadable bubble contrast,
  invisible borders) because the layout now relies on a token it doesn't define.
- Breaking the phone layout (drawer, single pane, touch targets).
- Re-introducing a flash to dark/light on load (keep `color-scheme` per theme).

## Constraints

- Plain CSS, no build step; follow `web/css/CLAUDE.md`: shared values become
  tokens in `features/common.css`, theme files only override tokens (plus the
  few scoped rules they already carry — terracotta's sidebar shadow/seam rules
  go away).
- Any new structural token (e.g. `--chat-max-w`, `--user-bubble-bg`,
  `--radius-*`, `--composer-radius`) must get a sensible default in `:root` so
  every existing theme renders correctly without edits.
- Composer HTML lives in the `#chat-pane-tpl` template (`web/index.html`) and is
  cloned per pane; JS handlers are per-pane in `web/app.js`
  (`attachPaneHandlers`). Keep the existing element ids/classes that JS relies
  on where possible (`#send`, `#cancel`, `#attach-btn`, `#slash-btn`,
  `#edit-mode-btn`, `setSendButtonMode`) or update every reference.
- i18n: any new visible string goes into `web/i18n/{en,fr,es,de}.json` +
  `make i18n`; bump `?v=` on changed scripts/stylesheets in `index.html`.
- Add a "What's new" bullet in `internal/features/FEATURES.md` (current minor,
  in-development section).
- Update the user doc (`web/docs`) where it describes the composer buttons, and
  the CLAUDE.md sections that describe the composer / theme defaults.

## Decided in conversation

- Everything: colours, typography, centred chat, rounded composer, sobriety.
- Variant A (sans-serif answers).
- Layout changes apply to **all** themes.
- The default `terracotta` theme is re-coloured to Claude Desktop's light
  palette (name kept); a dark counterpart is added.
- My calls, accepted by the user: "＋" menu content, indicator pills, single
  send/stop/steer button, flatter blocks/dialogs, system font, phone layout
  kept working.

## Settled after the builder check (my calls)

- **Status text** (`#status`: streaming…, working… (Ns), running <tool>…,
  reconnecting…, editor saved) stays: small muted text in the control row,
  just left of the send button. It is the only liveness signal.
- **Collection colours stay**: the folder glyph keeps its tint, and the active
  pane tab's underline uses the chat's collection colour (`--col-accent`,
  falling back to the terracotta accent). The selected session row uses the
  neutral fill, plus a small dot (not a rail) in the collection colour.
- **Indicator placement**: the skills dock and goal chip stay **above** the
  composer box (restyled as quiet pills, behaviour unchanged). The context
  ring and the subtle "?" sit in the control row.

## Left to the builder

- Exact hex values (the ones above are targets, tune against the screenshots
  for contrast — body text ≥ 4.5:1).
- Whether `ivory` (the other warm theme) is kept as is, aligned, or retired —
  default: keep it untouched.
- Exact placement of the squad chooser once the New Chat pill is gone.
- Icon set for the "＋" menu and the send/stop button (inline SVG, matching the
  existing stroke style).
- Mobile tuning of the new column width (the phone band already sets the
  column to 100 %).

## Verification

- Visual check in the in-app browser of: empty draft pane, a long answer with a
  table + code block + tool calls, ask-user card, settings dialog, phone width,
  both terracotta themes and at least one dark and one light third-party theme.
- Composer: attach, add context, Enter mode, `/` menu, send, stop mid-turn,
  steer mid-turn, Tab suggestion, skills dock, context ring popup.
- `make test` stays green (Go side unaffected; i18n generator warns on no
  missing keys).
