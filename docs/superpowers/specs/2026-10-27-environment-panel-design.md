# Omnis web UI — Environment panel and quiet tool calls

Date: 2026-10-27 · Status: approved in conversation, awaiting builder check

## Intent

Keep the chat as light as Claude Desktop's: everything auxiliary to the
conversation (where the agent works, context use, loaded skills, connectors,
produced files, git changes, the file browser) moves out of the transcript and
the composer into a **right-hand "Environnement" panel**, and tool calls shrink
to one quiet summary line per run. The user asked for **the same behaviour and
the same look & feel as Claude Desktop** (their screenshots of Claude Desktop's
Code tab: the right column of cards — Environnement, Contexte, Connecteurs,
Résultats, Modifications — and the in-chat "Exécuté 1 commande, a utilisé
2 outils ▸" lines). Approved mockup:
`.superpowers/brainstorm/740054-1793117017/content/env-panel-v2.html`.

This builds on the Claude Desktop look already merged
(`2026-10-27-claude-desktop-look-design.md`): same tokens, palette, radii.

## Goals

### 1. The right panel

- A column on the right of the chat area, ≈ 270–300 px, `--bg` background,
  1 px `--border` on its left, holding a vertical stack of **cards** (white /
  `--bg-input` fill, hairline border, `--radius-md`, no shadow). Each card has a
  header (title + optional value on the right + ▸/▾) and folds/unfolds; the
  fold state is remembered per browser.
- **One panel for the window**, showing the **focused pane's** session (it
  follows `setFocusedPanel`). A draft / editor / terminal tab shows the cards
  that still make sense (Environnement with the folder, Fichiers) and hides the
  session-only ones.
- A **toggle button** at the right end of the pane tab bar shows/hides the
  panel; the choice is remembered per browser. Default: **shown** on desktop.
- The **"Files" panel leaves the left sidebar**: the sidebar keeps New chat,
  Search, Import, Collections, Archived and the account button.

### 2. The cards (top to bottom)

1. **Environnement** — the active **squad**, the session's **working folder**
   (clicking it opens the Fichiers card), the **git branch** when that folder is
   inside a git repository, and the **collection** (with its colour).
2. **Contexte** — header value = percentage + a small bar (what the composer's
   context ring shows today). Unfolded: today's ring popup content (tokens used,
   max, budget, per-agent detail, **Compress now**). The context ring **leaves
   the composer**.
3. **Skills** — the skills loaded in the session (today's composer skills dock:
   name, and unfolding a skill shows its SKILL.md body). Header value = count.
   The `⚡ N skills` bar above the composer **disappears**.
4. **Connecteurs** — the MCP servers available to the session's squad, each
   with a status dot (connected / not started / error) and the error text on
   hover or unfold. Header value = connected count, plus an error count when
   any. Needs a new server route (see Constraints).
5. **Résultats** — files the agent produced in this session (today's
   generated-file download cards, i.e. `Write` of a new file), each with a
   download button. Persisted per session so they survive a reload (today's
   cards are live-only). The download cards **leave the transcript**. The inline
   download buttons next to file paths in replies stay (they are part of the
   reply text).
6. **Modifications** — when the working folder is a git repository: the files
   changed versus `HEAD` (working tree + index), each with `+N −M`, header
   value = total `+N −M`. Clicking a file opens it in the Monaco editor tab.
   Hidden when the folder is not a repository.
7. **Fichiers** — today's Folders/Files browser, unchanged in behaviour
   (navigation, context menus, upload by drop/paste, auto-refresh), now inside
   this card. Header value = the current folder.

### 3. Tool calls in the chat

- Each run of consecutive tool calls (today's `.tool-group`) renders as **one
  grey line without a frame**: a sentence summarising the actions **by kind**,
  e.g. « A exécuté 1 commande, a utilisé 2 outils ▸ », « A délégué 1 tâche, a
  écrit 1 fichier ▸ » — localised (en/fr/es/de), counts pluralised.
- Clicking the line unfolds a **rounded card** (hairline border, `--radius-md`)
  with **one row per call**, each titled in plain language (« A exécuté une
  commande », « A lu un fichier », « A lu une page web », « A délégué à
  web_agent », « A modifié main.go » …). A row unfolds to show the arguments
  (`key: value` lines, keys in muted monospace) then the result.
- While a run is in flight the line shows the action in progress (e.g. « En
  train d'exécuter une commande… ») instead of the final sentence.
- A failed call stays visible: the summary line mentions it (« 1 échec ») and
  the failing row is marked.
- **Tool runs are persisted** (user's decision: "like Claude Desktop, even if
  it makes the project bigger"): each persisted turn records its tool calls
  (name, arguments, result, error, duration, nested sub-agent calls) and where
  each sat in the reply text, so a reload / switching back / a pushed turn
  re-renders the same summary lines between the same paragraphs. Rewind, fork
  and import keep them with their turn. Arguments and results are capped in
  size; legacy turns without the record render as today.
- Todo-plan widget, ask-user wizard, spawned-session chips and mailbox blocks
  keep their current rendering (they are conversation content, not auxiliary).

### 4. Phone / narrow widths

- Below 820 px the panel is not shown inline: the toggle opens it as a
  full-height right-side drawer over the chat (mirror of the left drawer).

## Non-goals

- No change to what the agent does or to the streamed SSE frames; the panel
  re-presents information that is already streamed or persisted, plus the
  new read-only server routes (git, MCP status, produced files) and the
  persisted tool-call record on each turn.
- No git actions (commit, PR, discard) from the panel — Modifications is
  read-only.
- No change to the CLI/TUI.
- No "follow the OS" theme, no new theme.

## Anti-goals

- Losing a function: Compress now, the skill bodies, file browsing / upload /
  context menus, downloading a produced file, seeing a tool's arguments and
  result must all stay reachable.
- A panel that shows another session's data after switching tabs or panes.
- Polling that hammers the server: git/MCP refresh on turn end, focus change
  and card unfold, not on a short timer.
- Breaking the phone layout.

## Constraints

- Plain CSS + vanilla JS (`web/app.js`, partials under `web/css/features/`),
  tokens from `features/common.css`; follow `web/css/CLAUDE.md` (new partial ⇒
  barrel import + `?v=` bump + table row).
- i18n for every visible string (`web/i18n/{en,fr,es,de}.json` + `make i18n`).
- **New server routes** (Go, `server/`), read-only, session-scoped, under the
  existing `/api/sessions/:id/…` group so `ownerGuard` covers them in cookie
  mode:
  - git status of the session's working folder (`bashCwd`): branch + changed
    files with added/removed line counts. Must not hang on a huge repo
    (timeout) and must answer "not a repository" cleanly.
  - MCP status for the session's squad: server name, state, last error. The
    MCP pool connects lazily, so "not started" is a normal state, not an error;
    the route must not start servers.
  - produced files: persisted per session (like the loaded-skills record,
    `ConversationFile.Skills`), with rewind/fork/import keeping only entries
    from kept turns.
- Existing ids that JS relies on keep working or every reference is updated
  (`#folders-panel`, `#ctx-ring-wrap`, `.skills-dock`, `.tool-group`).
- Update the user docs (`web/docs`), `CLAUDE.md` sections that describe the
  Folders panel, skills dock, context ring, tool blocks and download cards, and
  add a `FEATURES.md` bullet.

## Decided in conversation

- Same behaviour and look & feel as Claude Desktop: right panel of cards; tool
  calls summarised by kind, unfolding into a card of per-call rows that unfold
  to arguments + result.
- Files move from the left column into the panel.
- A Modifications (git) card: yes.
- Auxiliary items listed in the panel to keep the chat light: skills, context,
  produced files, connectors, environment.

## My calls (not asked)

- Panel shown by default on desktop, toggle remembered per browser; one panel
  following the focused pane.
- Card order as listed above; fold state remembered per browser.
- Phone: right drawer.
- Inline download buttons next to file paths in replies stay in the reply.

## Settled after the builder check (my calls)

- **Sub-agent calls**: two levels — a delegation row (« A délégué à
  web_agent ») unfolds to its prompt, then the sub-agent's own calls as nested
  rows, then its answer. The summary sentence counts **top-level** calls only;
  a failure inside a sub-agent marks the nested row, not the summary.
- **Connecteurs**: the MCP servers declared by the agents of the squad the
  session is **currently pinned to** (its last routed squad), including nested
  `subagents`; a router-pinned new chat therefore shows none and the card is
  hidden. "Connected" is the shared pool's state for the whole process (the
  pool is process-wide; a server another session started shows connected).
- **Résultats**: only files that **did not exist** when the `Write` call was
  issued (the server stats the path on the `tool_call` event and records it on
  a successful result); deduplicated by path. Files created through Bash are
  not listed (no event carries them).
- **Modifications**: untracked files are included (status `?`, added = their
  line count, capped at 10 000; binary ⇒ 0). A deleted file is listed but not
  clickable.

## Left to the builder

- Exact sentence templates per tool kind and the kind mapping (Bash → command,
  Read/Grep/Glob → read/search, Write/Edit → file, WebFetch/WebSearch → web,
  sub-agents → delegation, MCP → its server's name…), with a generic fallback.
- Icons (inline SVG, existing stroke style).
- Refresh cadence details within the anti-goal above.
- Whether Modifications counts come from `git diff --numstat` (+ `--cached`)
  or equivalent.

## Verification

- Visual check in the in-app browser: a session with tool runs (one failed),
  a delegated sub-agent, loaded skills, a produced file, in a git folder and in
  a non-git folder; switching tabs and panes updates the panel; draft tab;
  phone width; Terracotta and Terracotta Dark.
- Go tests for the new routes (not-a-repo, timeout, no MCP servers, owner
  scoping in cookie mode).
- `make web-test` gains tests for the pure pieces (summary sentence from a list
  of tool kinds, produced-files bookkeeping if client-side).
- `make test` stays green.
