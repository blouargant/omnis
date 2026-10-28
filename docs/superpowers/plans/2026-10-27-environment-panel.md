# Environment panel + quiet tool calls — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move everything auxiliary out of the chat into a Claude Desktop-style right panel of cards (Environnement, Contexte, Skills, Connecteurs, Résultats, Modifications, Fichiers) and render tool runs as one quiet summary line that unfolds into a card of per-call rows.

**Architecture:** Client: a new `#env-panel` column inside `#chat-area` (one per window, following the focused pane) built by a new `web/env-panel.js` module of card renderers; pure helpers (tool kind + summary sentence) in `web/tool-summary.js`, node-tested. The existing Folders browser, context-ring popup and skills dock are re-parented into cards rather than rewritten. Server: three read-only, session-scoped routes — produced files (persisted like `ConversationFile.Skills`), git status of the session cwd, MCP status of the session's squad (needs a status tracker in `internal/mcp.Pool`).

**Tech Stack:** Go (gin) server, vanilla JS + CSS web UI, `node --test` for pure JS, `go test` for routes.

**Spec:** `docs/superpowers/specs/2026-10-27-environment-panel-design.md`

## Global Constraints

- Same tokens/look as the merged Claude-style UI (`--radius-md` cards, hairline `--border`, `--bg-input` card fill, no shadow).
- Every visible string i18n'd (en/fr/es/de) + `make i18n`; bump `?v=` of changed scripts/stylesheets in `web/index.html`; new CSS partial ⇒ barrel import + `web/css/CLAUDE.md` row.
- New routes live under `auth.GET("/sessions/:id/…")` so `ownerGuard` covers them; read-only; never start an MCP server; git commands bounded by a timeout (5 s) and run with `git -C <cwd>`.
- Panel refresh triggers: turn end, focus change, card unfold, `skills_changed`/`produced_changed` events, `file_changed`. No interval polling.
- Phone (< 820 px): panel is a right drawer opened by the toggle.
- Keep ids JS relies on (`#folders-panel` and its children, `#ctx-ring-wrap`/`#ctx-popup` element ids, `.skills-dock`) or update every reference.

## Review Focus

1. **Switching tabs/panes** — the panel must never show the previous session's skills/results/git; pin with a session-id guard on every async render (`env-panel.js` drops a response whose session ≠ current).
2. **Draft / editor / terminal tab active** — session cards hidden, Environnement shows the global folder, Fichiers works (it already handles "no session").
3. **Folder not a git repo, or git missing** — Modifications card hidden, branch omitted, no error shown; route test covers both.
4. **MCP server never connected yet** — shown "not started", not red; route test covers it.
5. **Rewind / fork / import** — produced files from dropped turns disappear; Go test mirrors the skills ones.

---

### Task 1: Tool kind + summary sentence (pure, tested)

**Files:** Create `web/tool-summary.js`, `web/tests/tool_summary.test.mjs`; modify `web/index.html` (script before `app.js`).

**Interfaces — Produces** (window globals + `module.exports`, like `composer-state.js`):
- `toolKind(name: string) → "command"|"read"|"search"|"write"|"edit"|"web"|"delegate"|"skill"|"mcp"|"other"`. Mapping: `bash*`/`bash_background`/`monitor` → command; `read`/`mime`/`read_doc`/`read_session`/`lsp_read_symbol`/`lsp_hover`/`lsp_document_symbols` → read; `grep`/`glob`/`search_*`/`recall_*`/`list_*`/`lsp_*` (other) → search; `write` → write; `edit`/`multiedit`/`revert`/`lsp_rename`/`ast_grep_rewrite` → edit; `websearch`/`webfetch`/`html_to_markdown` → web; `mcp__*` → mcp; `load_skill`/`load_softskill` → skill; a name in the caller-supplied `subAgents` set → delegate; else other.
- `toolSummary(calls: Array<{kind, status: "pending"|"done"|"error"}>, t: (key, n) => string) → string` — counts per kind in first-appearance order, joins `t("tool.sum.<kind>", n)` parts with ", ", appends `t("tool.sum.failed", nFailed)` when any error; when any call is pending returns `t("tool.run.<kindOfLastPending>", 1)`.

- [ ] Step 1: tests — `toolKind("Bash")==="command"`, `toolKind("mcp__github__list")==="mcp"`, `toolKind("web_agent", new Set(["web_agent"]))==="delegate"`, `toolKind("frobnicate")==="other"`; `toolSummary([{kind:"command",status:"done"},{kind:"other",status:"done"},{kind:"other",status:"done"}], fakeT)` → `"command:1, other:2"` (fakeT = `(k,n)=>k.split(".").pop()+":"+n`); with one `error` → ends `", failed:1"`; with a pending `web` last → `"web:1"` from `tool.run.web`.
- [ ] Step 2: `node --test web/tests/*.test.mjs` → FAIL (module missing).
- [ ] Step 3: implement; Step 4: PASS; Step 5: commit `feat(web): tool kind and summary sentence helpers`.

### Task 2: Quiet tool runs in the chat

**Files:** `web/app.js` (`buildToolBlock`, `buildToolGroup`, `refreshToolGroup`, `appendToolCall`, `resolveToolCall`, `appendNestedToolCall`), `web/css/features/tools.css`, i18n.

**Interfaces — Consumes:** Task 1. **Produces:** `.tool-group` always used (even for one call) = `.tool-summary-line` (grey text + chevron, no frame) + `.tool-card` (rounded card, hidden until unfolded) of `.tool-row`s; each row = title line (`toolRowTitle(name,args)`, e.g. « A exécuté une commande », « A modifié main.go », « A délégué à web_agent ») + `.tool-row-detail` (args as `key: value` lines with muted monospace keys, then the existing output slot). Sub-agent nested calls render as rows inside the delegating row's detail (keep `appendNestedToolCall`).

- [ ] Step 1: i18n keys `tool.sum.{command,read,search,write,edit,web,delegate,skill,mcp,other}` (`.one`/`.other`, fr: « a exécuté {n} commande(s) », « a lu {n} fichier(s) », « a fait {n} recherche(s) », « a écrit {n} fichier(s) », « a modifié {n} fichier(s) », « a consulté le web {n} fois », « a délégué {n} tâche(s) », « a chargé {n} skill(s) », « a utilisé {n} connecteur(s) », « a utilisé {n} outil(s) »), `tool.sum.failed` (« {n} échec(s) »), `tool.run.<kind>` (« En train d'exécuter une commande… » …), `tool.row.<kind>` titles; first letter of the joined sentence capitalised. `make i18n`.
- [ ] Step 2: rewrite the render functions per Interfaces; the summary line text comes from `toolSummary` with `trN`; error rows carry `.is-error` (red dot); remove badge chips and colour rails.
- [ ] Step 3: verify live in the browser: a run of 3 calls → one grey line; unfold → card with 3 titled rows; unfold a row → args then result; a failing call → « …, 1 échec »; a delegated sub-agent shows its inner calls inside its row; todo widget and ask-user unchanged. `make web-test` PASS.
- [ ] Step 4: commit `feat(web): tool runs as one quiet summary line`.

### Task 2b: Persist tool runs and replay them on reload

**Files:** `internal/sessions/history.go` (+ test), create `server/tool_record.go` (+ `_test.go`), `server/sse.go` (`handleMessages` wraps `sink`), `web/app.js` (history render).

**Interfaces — Produces:**
- `type ToolCallRecord struct { CallID, Name string; Args json.RawMessage; Result json.RawMessage; Error string; DurationMs int64; Offset int; Agent string; Children []ToolCallRecord }` with JSON tags `call_id,name,args,result,error,duration_ms,offset,agent,children` (omitempty where sensible). `Offset` = UTF-16 length of the visible reply text streamed before the call (so JS can `slice` with it).
- `ConversationTurn.Tools []ToolCallRecord \`json:"tools,omitempty"\``; `sessions.AppendConversationTurnRecord(sessionID, userText, assistantText string, usage map[string]TokenUsage, durationMs int64, tools []ToolCallRecord) error` (`AppendConversationTurnFull` delegates with nil).
- `type toolRecorder` (server): `wrap(sink func(string, []byte)) func(string, []byte)` observes frames — `token`/`message` advance the offset; `tool_call` appends a top-level record; `tool_result`/`tool_error` resolve it; `agent_tool_call`/`_result`/`_error` attach to the most recent top-level call (as the live UI does); `take() []ToolCallRecord` returns and resets. Caps: args and result 8 KiB each (truncated JSON string with an ellipsis marker), 300 top-level calls, 100 children per call.
- Web: `renderHistoryTurn(turn, container, index)` replaces the three duplicated loops (initial load, `rerenderSessionFromHistory`, `appendNewPushTurns`); splits `assistant_text` at each run's offset (calls with the same offset = one run) into text segments interleaved with tool groups built by Task 2's functions, already resolved. Applies the live filters (successful skill loads and todo tools are not shown).

- [ ] Step 1: Go tests — `TestToolRecorderOffsetsAndResults` (frames: token "Hello " → tool_call A → tool_result A → token "done" → tool_call B → agent_tool_call B1 → agent_tool_result B1 → tool_result B; expect A.Offset 6, B.Offset 10, B.Children len 1), `TestToolRecorderCaps`, `TestAppendTurnPersistsTools`, `TestTruncateAndForkKeepToolsWithTurns` (tools ride on the turn, so truncate/fork need no extra code — the test pins it). FAIL → implement → PASS.
- [ ] Step 2: wire the recorder in `handleMessages` per loop iteration (reset with `usageAccum`), persist via `AppendConversationTurnRecord`. Injected turns (mailbox/background/scheduled) are out of scope for v1 — ledger it.
- [ ] Step 3: web replay; verify: run a turn with tools, reload → same grey lines between the same paragraphs, unfold → args/result; legacy session renders as before. `make web-test && go test ./server ./internal/sessions`.
- [ ] Step 4: commit `feat: persist tool runs with each turn and replay them`.

### Task 3: Panel shell + Fichiers card

**Files:** Create `web/env-panel.js`, `web/css/features/env-panel.css`; modify `web/index.html` (`#env-panel` after `#chat` inside `#chat-area`; toggle `.pane-env-btn` at the right of each pane tab bar; move `#folders-panel` markup into the Fichiers card), `web/app.js` (`setFocusedPanel` → `EnvPanel.setSession(id|null, panel)`; folders code unchanged apart from removing its sidebar collapse logic and the `foldersHeightCap` reservation), `web/css/styles.css` barrel, `web/css/features/responsive.css`, `web/css/CLAUDE.md`, i18n.

**Interfaces — Produces:** `window.EnvPanel = { setSession(sessionId|null), refresh(cardId?), register(card: {id, title, sessionOnly, render(body, ctx) , headerValue?(ctx)}) , isOpen() }` where `ctx = { sessionId, cwd, signal }`; each async render must bail when `ctx.sessionId !== EnvPanel.currentSession()`. Card fold state in `localStorage["agent_env_cards"]` (`{id: open}`), panel visibility in `localStorage["agent_env_open"]` (default open).

- [ ] Step 1: build the shell + card component + toggle; register a Fichiers card that hosts the existing folders panel DOM (expanded inside the card body; header value = `foldersDir` basename).
- [ ] Step 2: verify: panel at right, cards fold/unfold and persist across reload, toggle hides/shows and persists, Fichiers navigation / context menu / drop upload still work, sidebar no longer has Files, phone width opens as right drawer + scrim.
- [ ] Step 3: commit `feat(web): environment panel with the file browser`.

### Task 4: Environnement, Contexte, Skills cards

**Files:** `web/env-panel.js`, `web/app.js` (ctx ring/popup + skills dock call sites), `web/index.html` (remove `#ctx-ring-wrap` and `.skills-dock` from the composer), CSS, i18n.

- [ ] Step 1: Environnement card: squad (from session meta), folder (`bashCwd` via existing `GET /api/sessions/:id/folder` `dir`; click → unfold Fichiers), collection name + colour; branch filled in Task 6.
- [ ] Step 2: Contexte card: header = `NN %` + bar from `sessionCtxUsage`; body = the existing ctx popup content rendered inline (reuse `renderCtxPopup` logic with the card body as target; keep **Compress now**). Remove the ring from the composer; `renderCtxRing` call sites → `EnvPanel.refresh("context")`.
- [ ] Step 3: Skills card: header = count; body = the skills list (reuse `renderSkillsDock` row building with the card body as target). Remove the composer dock; `skills_changed` → `EnvPanel.refresh("skills")`.
- [ ] Step 4: verify: switching sessions updates all three; draft tab hides Contexte/Skills; Compress now works; skill body unfolds. `make web-test` PASS. Commit `feat(web): environment, context and skills cards`.

### Task 5: Résultats — produced files (server + card)

**Files:** Create `internal/sessions/produced_files.go` (+ `_test.go`), `server/produced_files.go` (+ `_test.go`); modify `internal/sessions/history.go` (`ConversationFile.Produced []ProducedFile \`json:"produced,omitempty"\``, truncate/fork/import keep `Turn < keep`), `server/sse.go` (`emitFileChanged` for `tool=="write"` → `recordProducedFile`), `server/server.go` (route), `server/main.go` (`producedChangedHook` → `pushEvents.broadcast("produced_changed", sid)`), web.

**Interfaces — Produces:** `type ProducedFile struct { Path string \`json:"path"\`; Turn int \`json:"turn"\`; At time.Time \`json:"at"\` }`; `sessions.RecordProducedFile(sessionID, path string) error` (dedup by path: update `At`, keep first `Turn`); `sessions.LoadProducedFiles(sessionID) ([]ProducedFile, error)`; `GET /api/sessions/:id/produced` → `{files:[{path,name,turn,at,exists}]}` (`exists` = `os.Stat` regular file).

- [ ] Step 1: Go tests — `TestRecordProducedFileDedups`, `TestTruncateKeepsProducedBeforeTurn`, `TestForkKeepsProducedBeforeTurn`, `TestProducedRouteListsFiles` (real router, includes `exists:false` for a deleted file). Run → FAIL.
- [ ] Step 2: implement; tests PASS; `go test ./internal/sessions/ ./server/`.
- [ ] Step 3: Résultats card (newest first, file icon, name, download button via `downloadHostFile`, greyed when `!exists`); delete `appendFileDownloadCard` and its call; `produced_changed` + `file_changed(write)` → `EnvPanel.refresh("results")`.
- [ ] Step 4: verify live; commit `feat: produced files listed in the environment panel`.

### Task 6: Modifications + branch — git status route

**Files:** Create `server/git_status.go` (+ `_test.go`), route in `server/server.go`, web.

**Interfaces — Produces:** `gitStatus(ctx, cwd string) (GitStatus, error)`; `type GitStatus struct { Repo bool; Branch string; Files []GitFile; Added, Removed int }`; `type GitFile struct { Path string; Added, Removed int; Status string /* M A D R ? */ }`. Uses `git -C cwd rev-parse --is-inside-work-tree`, `git branch --show-current` (detached ⇒ short SHA), `git diff --numstat HEAD` (falls back to `--cached` + working tree when no HEAD), `git status --porcelain` for untracked (`?`, counted as added lines = 0). 5 s context timeout. `GET /api/sessions/:id/git` → `GitStatus` JSON; not a repo / git missing ⇒ `{repo:false}` with 200.

- [ ] Step 1: Go tests with `t.TempDir()` + `git init` (skip when git absent): `TestGitStatusNotARepo`, `TestGitStatusCountsChanges` (commit a file, modify +2/−1, add untracked), `TestGitStatusRouteOwnerScoped` (cookie-mode owner guard → 404 for another owner, reuse existing identity test helpers). Run → FAIL.
- [ ] Step 2: implement; PASS.
- [ ] Step 3: Modifications card (hidden when `!repo`; header `+A −R`; rows path + `+a −r`; click → `openFileInEditor(abs)`); Environnement shows branch. Refresh on turn end, focus change, unfold, `file_changed`.
- [ ] Step 4: verify in this repo and in `/tmp`; commit `feat: git changes in the environment panel`.

### Task 7: Connecteurs — MCP status

**Files:** `internal/mcp/pool.go` (+ `pool_status_test.go`), `agent/` accessor for "MCP servers of a squad", `server/mcp_status.go` (+ test), route, web.

**Interfaces — Produces:** `Pool` wraps every transport in a status recorder: `type ServerState string` (`"idle"` never connected, `"connected"`, `"error"`); `(*Pool).Status() map[string]ServerStatus` keyed by server name, `ServerStatus{State, Err string}`. `(*Manager).SquadMCPServers(sessionID string) []string` (union of the session's squad members' `mcp_servers`). `GET /api/sessions/:id/mcp` → `{servers:[{name,state,error}]}` (servers not in the pool ⇒ `idle`). Never triggers a connect.

- [ ] Step 1: tests — `TestPoolStatusIdleUntilConnect`, `TestPoolStatusRecordsError` (fake transport), `TestMCPRouteNeverConnects`. FAIL → implement → PASS.
- [ ] Step 2: Connecteurs card (dot green/grey/red, error text on unfold; header `N` connected + red count); hidden when the squad has no MCP server.
- [ ] Step 3: verify; commit `feat: MCP connector status in the environment panel`.

### Task 8: Docs + whole check

- [ ] `web/docs` (composer, sessions/Files, new "Environment panel" section), `CLAUDE.md` (Folders panel, skills dock, context ring, tool blocks, download card sections → point to the panel), `FEATURES.md` bullet: `- **Environment panel** — a right-hand panel lists the session's squad, folder and git branch, context use, loaded skills, connectors, produced files, git changes and the file browser; tool calls shrink to one quiet line that unfolds on demand.`
- [ ] `make web-test && make test` PASS; browser sweep per the spec's Verification; commit `docs: environment panel`.
