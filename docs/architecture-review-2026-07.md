# Architecture & Code Review — 2026-07-02

Branch: `review/architecture-audit`. Scope: architecture, security, potential bugs,
doc accuracy, and hygiene. Method: direct inspection + code-graph analysis, with
parallel focused sub-reviews (security, concurrency, doc-accuracy).

Baseline health (this branch): `go build ./...` ✅, `go vet ./...` ✅,
`go test ./...` ✅, and `go test -race` on the concurrency-critical packages
(`agent`, `server`, `internal/{steer,goal,bg,scheduler}`) ✅. Nothing is currently
broken; the findings below are latent issues, doc drift, and hygiene.

## TL;DR — top findings

- **[High · C1] Cross-session leak on the interactive stream.** Concurrent turns on a
  multi-user server leak each other's sub-agent tool frames (names + args) to the wrong
  browser and corrupt persisted per-agent token/cost accounting — the shared event bus
  is fanned to every SSE subscriber with no session filter. The contamination CLAUDE.md
  says the design avoids was only fixed for background turns, not interactive ones.
- **[High · S1] The "hard safety floor" is trivially bypassable.** It's a
  `strings.Contains` over 3 literals and gates the agent's own LLM-driven Bash tool;
  `rm -fr /`, `rm -r -f /`, extra whitespace, or a base64-pipe all sail through.
- **[Medium · C2] A panic in any turn crashes the whole server.** The disconnect-survival
  refactor runs turns in a bare goroutine with no `recover()`, so one bad turn kills every
  session's in-flight work (a fault-isolation regression vs. the old handler).
- **[Medium] Secrets in URLs (S2/S3), unbounded folder download/delete (S4), reload
  ordering can drop the newest config (C3).**
- **[Docs] The squad topology drifted** (a removed `research` squad still documented in
  5 files; 5 new squads undocumented) plus a bogus `make release` target, a stale
  go-turbovec `replace` note, and 5 undocumented env vars.

The manager reload-race crash fix, background-turn usage accounting, and every
watcher/poller's shutdown were reviewed and found **sound**.

> **Status update:** every finding below — S1–S4, C1–C5, and all the doc/hygiene
> items — has now been **fixed on this branch**, with tests. See §7 for the
> per-finding changelog. `go build`, `go vet`, `go test ./...` (48 pkgs), and
> `go test -race` on the concurrency-critical packages all pass.

---

## 1. Repo hygiene (confirmed)

| # | Severity | Finding | Evidence |
|---|---|---|---|
| H1 | High | **A 48 MB compiled ELF binary `yoke` is committed to git.** It bloats the repo permanently (git history keeps every version forever) and is the only >1 MB non-vendored tracked file. It looks like a stray build artifact. | `git ls-files` → `yoke`; `file yoke` → ELF 64-bit Go executable, 48 MB |
| H2 | Low | Several personal essay/planning docs are committed at repo root and add clutter: `AGENT05.md`, `article.md`, `ace-plan.md`, `agent_harnesses_essay.md`, `simple-endpoint-latency-investigation.md`. | `git ls-files` root listing |

**Recommendation:** `git rm --cached yoke`, add `yoke` (or a broader binary rule) to
`.gitignore`. If `yoke` has ever been committed in prior history and repo size
matters, consider a history rewrite (`git filter-repo`) — otherwise at minimum stop
tracking it going forward. Move the root essays under `docs/notes/` or drop them.

`.env` is correctly **not** committed, and committed config (`config/models.json`)
correctly stores env-var *names* (`OPENAI_BASE_URL`, `OPENAI_API_KEY`) rather than
secret values — no hardcoded credentials found.

---

## 2. Documentation accuracy (confirmed)

The user's guidance was to verify the markdown guides are accurate. They have drifted
from the code in several concrete ways:

| # | Severity | Finding | Evidence |
|---|---|---|---|
| D1 | Medium | **`make release` is documented but does not exist.** The Makefile has only a `package` target. Anyone copying the command gets `make: release: No such file or directory`. | Documented at `CLAUDE.md:133`, `CLAUDE.md:167`, `README.md:382`; `grep -nE '^(release\|package):' Makefile` → only `package:` |
| D2 | Medium | **Stale `replace` claim for go-turbovec.** CLAUDE.md says the shared-matrix optimisation "requires go-turbovec ≥ the memoised build; omnis currently pins it via a local `replace` in `go.mod` — publish + bump for release." `go.mod` has **no** `replace`; go-turbovec is a plain published require `v0.1.1`. Either the note is stale (published — update the doc) or the memoised build isn't actually in `v0.1.1` (a real correctness risk for the recall matrices). Verify which. | `CLAUDE.md:720`; `go.mod:9` → `github.com/blouargant/go-turbovec v0.1.1`, zero replaces |
| D3 | High | **Squad topology is out of date across the whole doc set.** CLAUDE.md's Agent-topology diagram documents a `research` squad that no longer exists and omits five real squads: `Kubernetes`, `Knowledge`, `Skill Editor`, `Helper`, `Coding`. The `Default` squad's real membership (`investigator, summariser, image_generator, helper, agentmd_reviewer`) also differs from the documented one (no `web_agent`; adds `image_generator`/`helper`). The dead `research` squad is *also* referenced in `docs/configuration.md`, `docs/architecture.md`, `docs/extending.md`, and `web/docs/10-architecture.md`. | `config/agents.json` squads vs. CLAUDE.md topology; `grep -rl '"research"' docs/ web/docs/ CLAUDE.md` |
| D4 | Low | **Five env vars read by the code are undocumented** in the CLAUDE.md env-var table: `OMNIS_APP_NAME`, `OMNIS_SERVER_BASE_PATH`, `OMNIS_SESSION_REBIND_IDLE`, `OMNIS_SOFTSKILLS_DIR` (all `server/main.go` / `server/idle_rebind.go`). | `grep -rn 'OMNIS_' --include=*.go server/` vs. table |

Everything else spot-checked in the docs is accurate: ~30 file/function references,
the HTTP route list, SSE event names, the documented constants
(`routerMaxHops=4`, `maxSpawnsPerSession=8`, `maxBufferBytes=8 MiB`,
`shaperMaxChars=32000`), env-var defaults (`OMNIS_GOAL_MAX_TURNS=30`,
stall `10m`, http `15m`, update `6h`), and the vendored library versions
(Monaco `0.55.1`, xterm `5.3.0`, fit `0.8.0`).

CLAUDE.md carries an explicit "Self-Maintenance Rule" requiring the topology tables be
kept current; D3 is exactly the drift that rule exists to prevent, so it is worth a
dedicated fix pass.

---

## 3. Verified-clean areas (no issue found)

- **Tool-wrapper dispatch invariant ("pack itself").** Every leader-facing sub-agent
  wrapper — `nonConcurrentTool`, `parallelAgentTool`, and the `gatedLoadTool`/
  softskills `renamedTool` gates — packs *itself* into `req.Tools` in `ProcessRequest`,
  so ADK dispatches to the wrapper (preserving the mutex / fan-out / dep-gate) rather
  than the inner tool. `resumableAgentTool` is the one wrapper that does **not** override
  `ProcessRequest` (it inherits the embedded `runnableTool`'s), but `build_subagents.go`
  always re-wraps it in `newNonConcurrentTool`/`newParallelAgentTool`, so its inherited
  method is never the top-level dispatch. The invariant holds end-to-end.
- **Squad→agent config integrity.** All seven squads reference enabled agents; the
  `Skill Editor` squad lists its leader `skill_editor` as a member too, but the resolver
  dedups the leader from members (`runtime_config.go:666`), so it's harmless.

---

## 4. Security review (host-trust routes, auth, path traversal)

Context: the server *by design* exposes several routes that trust the API-token
holder with host filesystem/shell access (the `!` escape, the Monaco save route, the
interactive terminal). Those are the intended trust model and are **not** flagged as
new vulnerabilities. The findings below are places where the implementation is weaker
than the surrounding code intends, or where the blast radius is larger than necessary.

| # | Severity | Finding | Evidence |
|---|---|---|---|
| S1 | **High** | **The "hard safety floor" is a naive 3-literal substring match and is trivially bypassed** — and it also gates the agent's own LLM-driven `Bash` tool, not just the human `!` escape. `alwaysBlock = {"rm -rf /", ":(){:|:&};:", "mkfs"}` matched by `strings.Contains`. Bypasses that execute unblocked: `rm -fr /`, `rm -r -f /`, `rm --recursive --force /`, `rm -rf⇥/` (extra whitespace), the canonical spaced fork-bomb `:(){ :\|:& };:`, and `echo <b64> \| base64 -d \| bash`. Nothing outside the 3 literals is covered at all (`dd if=/dev/zero of=/dev/sda`, `chmod -R 000 /`, `find / -delete`). A prompt-injected model (e.g. from fetched web content) that reorders flags or base64-encodes defeats the one guarantee the code advertises as unconditional. | `core/tools/bash.go:20,27-34`; enforced at `bash.go:187` (`RunBash`), `:233` (`RunBashInteractive`), `:291` (`RunShellCaptured`/hooks), `internal/bg/bg.go:109`, `internal/bg/monitor.go:33` |
| S2 | Medium | **Terminal WebSocket passes the master bearer token in the URL query string.** The compare is correctly constant-time and omnis's own request logger drops the query string, but URL-embedded secrets leak via browser history, upstream reverse-proxy/ingress access logs (which log the full request line by default), and DevTools/screen-shares. Because it is the *same* token as full API control, leaking the terminal URL leaks the whole server. | `server/terminal.go:69-74` (`handleTerminal`), client `web/app.js:1839` |
| S3 | Medium | **A not-yet-saved provider API key is sent via GET query string**, directly contradicting the design decision made for the sibling `POST /providers/test` route (whose comment says "A POST body … keeps a typed key out of access logs"). Reachable from the Settings → Models "test connection" / "auto-fill dim" buttons while the user types a real key. | `web/settings.js:2267,2821,3002` (`params.set("api_key", …)` on a GET); `server/provider_models.go:250` reads `c.Query("api_key")` for `/providers/models` + `/providers/embedding-dim` |
| S4 | Medium | **Folder ops have no containment/size guard.** `resolveAgainstCwd` (unlike the correct `safeJoinUnder` used for uploads) accepts absolute paths as-is and lets `..` walk out. `doFolderDelete` `os.RemoveAll`s any path (only refuses the exact cwd or exact `"/"`; root check is POSIX-only). `doFolderDownload` zip-streams *any* directory with `WalkDir` and **no size/file-count/depth cap** — `GET …/folder/download?path=/` tries to zip the whole reachable filesystem, an easy accidental resource-exhaustion primitive. Not a new privilege under the trust model, but a much bigger blast radius than a "download my open folder" click. | `server/uploads.go:259-268` (`resolveAgainstCwd`); `server/folder_ops.go:52-105` (download), `:121-148` (delete) |
| S5 | Low (defense-in-depth) | **No SSRF guard + no request timeout on outbound registry fetches.** `rawGet` uses `http.DefaultClient.Do` with no per-request timeout and no scheme/host allowlist. The agent-facing tools only ever fetch config-sourced URLs (never free-form chat input — verified), so a prompt-injected model *cannot* pivot to `169.254.169.254`; but a slow/malicious registry host can hang a browse call indefinitely, and a tampered config file has no code-level guard against internal-service access. | `internal/registries/http.go:9-24`; `internal/a2a/client.go:87,123` |

**Verified secure (explicitly checked, no finding):** `authMiddleware` uses
`subtle.ConstantTimeCompare` correctly, with a correct empty-token unauthenticated
mode (`server/auth.go`); `safeJoinUnder` correctly rejects absolute paths and `..`
escapes and is used for all upload destinations; `GET/PUT /api/file` write path is
existing-regular-file-only and preserves mode (`server/fileref.go`); the self-update
sudo password is only piped to `sudo -S` stdin, never logged/persisted/returned;
provider health/test routes never serialise `api_key` (only `has_api_key`);
`get_settings` `redact()` recurses to any depth and masks nested credentials (e.g. an
A2A peer's `Authorization` header). Every host-touching route is behind
`authMiddleware` except the one necessary exception (`GET /api/terminal/ws`), which
self-validates the same token constant-time before upgrading.

**Suggested priorities:** S1 first (replace the substring floor with real shell-word
tokenisation — detect `rm` + recursive + force targeting shallow paths regardless of
flag form/whitespace, normalise the fork-bomb check, and flag decode-then-exec
chains; widen coverage beyond 3 patterns, or at minimum make the doc comment honest
that it's a minimal best-effort backstop). Then S3 (route the key through the
existing POST) and S2 (mint a short-lived terminal-scoped token). S4/S5 are hardening.

---

## 5. Concurrency & architecture (reload race, lifecycle, goroutine leaks)

The headline CLAUDE.md claims mostly hold: the manager "empty-map / nil `Current()`"
crash is genuinely fixed, the **background/`injectTurn`** usage path really is
session-scoped and contamination-free, and every watcher/poller binds to a cancelable
`rootCtx` and stops cleanly on session delete/archive and server shutdown. But the
review found one confirmed high-impact cross-session bug on the **interactive** stream
and a fault-isolation regression from the disconnect-survival refactor.

| # | Severity | Finding | Evidence |
|---|---|---|---|
| C1 | **High** | **The interactive SSE stream leaks sub-agent frames + token usage across concurrent sessions.** The interactive turn producer subscribes to the *process-wide* event broadcaster, whose only filter is dropping `agent == "leader"`. `emitBusEvent` has **no session filter** — its only dedup is `agentName == rootAgent`. So while sessions A and B both run turns that delegate to sub-agents: (a) B's sub-agent `EventBeforeTool`/`EventAfterTool` are emitted as `agent_tool_call`/`agent_tool_result` frames to **A's browser**, leaking B's tool names *and args* (file paths, grep queries) — a privacy issue on a multi-user server; and (b) B's sub-agent `EventAfterModel` usage is added into **A's** `usageAccum` and persisted onto A's turn, corrupting per-agent cost. This is exactly the contamination CLAUDE.md says the design avoids — but that guarantee was only implemented for the background path (`recordInjectedUsage` reads the session-scoped ADK stream), never the interactive path. Not trivially fixable by filtering on payload `session_id`: for a sub-agent in agenttool's private runner, `tctx.SessionID()` is the *ephemeral* agenttool session, not the web-UI session. **Fix:** propagate the real session id / leader `run_id` into sub-agent runs (the `WithCwd`/`WithSteerSession` context path already reaches sub-agents), stamp it on the bus payload, and drop any event whose correlation id ≠ this producer's. | `server/sse.go:800-894` (`emitBusEvent`, filter at `:809`); subscription `server/sse.go:210`; broadcaster `server/server.go:124-146` (only filters `agent=="leader"`); payload session id is ephemeral for sub-agents `core/events/events.go:225,244` |
| C2 | Medium | **Detached turn goroutines have no panic recovery (crash regression).** The disconnect-survival refactor moved the agent run into a bare `go func(){…}()`. `gin.Recovery()` only wraps the request handler, not spawned goroutines, and there is **zero** `recover()` in `sse.go`. A panic during a turn (nil deref in a plugin `AfterToolCallback`, a malformed tool response, an ADK edge case) — previously a contained 500 — now propagates out of the unrecovered goroutine and **crashes the whole process, killing every other session's in-flight turn.** Same gap in the mailbox/scheduler/spawn injection goroutines. **Fix:** wrap each detached turn-driver body in `defer func(){ if r:=recover();r!=nil { log + emit "error" frame + lt.finish() } }()` before the existing cleanup defers. | `server/sse.go:202` (`go func`), cleanup defers present but no `recover()`; `grep -c recover() server/sse.go` → 0 |
| C3 | Low–Med | **Overlapping reloads can silently discard the newest generation** (ordering, not crash). The `genSeq` fix is correct for the crash it targets, but `Reload`'s final section sets `m.currentGen = nextGen` *unconditionally*. If R1 (`nextGen=2`) and R2 (`nextGen=3`) both build and R2 installs first (currentGen=3), then R1 finishing last sets currentGen=2 and, via `oldGen(3)!=nextGen(2)` with gen3 refcount 0, **deletes gen3 — the newer config** — leaving the process on the older build. "Last reload wins" becomes "last-to-finish wins". Can occur when a manual `POST /api/config/reload` overlaps a settings-tool `RequestReload`. **Fix:** only promote when `nextGen > m.currentGen`; otherwise `Close()` the just-built older instance instead of installing it. | `agent/manager.go:300-316` (`m.currentGen = nextGen` unconditional) |
| C4 | Low | **`steer.Store` leaks one empty map entry per session that ever steered.** `Forget` is called only on session *delete*, not archive/abandonment, and `TakeConsumed`/`TakePending` null the slices but never `delete` the key. Unbounded over a long-lived multi-session server. (`goal.Store`/`RouteRegistry`/`SpawnRegistry` don't have this — they self-clear.) **Fix:** `delete(s.m, sid)` when both slices empty, and/or `Forget` on archive. | `internal/steer/steer.go:41-48,82-107,121-125`; delete-only call at `server/server.go:400` |
| C5 | Low | **Route/Spawn directives aren't cleared on session delete.** All map access is correctly locked and directives are normally transient (Taken/Drained each turn), but a directive `Set` during a run torn down mid-flight leaves a stale entry until the id is reused. Hygiene-only. **Fix:** add `Forget(sid)` to both registries alongside `SteerStore.Forget`/`GoalStore.Forget` in the delete handler. | `agent/routing.go:57-126`, `agent/spawn.go:45-82` |

**Plausible (not confirmed triggerable):** (P1) a reconnect carrying a `from` seq from a
*previous* turn can attach to a *new* turn's `liveTurn` and silently skip its first
frames — requires two clients on one session (`server/live_turn.go:93-118,166-174`);
(P2) the mailbox watcher's `acquire` does a bare `sem <- struct{}{}` with no `select`
on ctx, so it can't be cancelled promptly while parked behind a long turn (not a
permanent leak — `server/mailbox_push.go:32-37`).

**Verified sound (no action):** the manager empty-map/nil-`Current()` crash fix is
correct and complete and refcount inc/dec is balanced (one pin per session);
background `injectTurn` usage accounting is genuinely session-scoped; all
watchers/pollers (mailbox, background, idle-indexer, idle-curator, GC, self-update,
docs-indexer, scheduler) bind to a cancelable ctx and exit cleanly, with `PushMgr.Stop`
+ `Manager.Release` wired on both delete and archive; the scheduler `fire` closure
re-resolves the squad each run (no stale-generation capture); `liveTurn`
producer/consumer locking is correct and its 60s GC only deletes a still-registered
turn; all four directive stores guard every map access and return value copies.

---

## 6. Suggested priority order

1. **C1 (High)** — cross-session leak of sub-agent frames + cost on the interactive
   stream. Highest impact on any multi-user deployment (privacy + billing integrity).
2. **S1 (High)** — replace the trivially-bypassable substring safety floor; it gates
   the agent's own Bash tool, so it's a real guard, not just UX.
3. **C2 (Medium)** — add panic recovery to the detached turn goroutines so one bad
   turn can't crash every session.
4. **S3, S2 (Medium)** — stop sending typed API keys in GET query strings; give the
   terminal WS a short-lived scoped token instead of the master token.
5. **D3 (docs)** — bring the squad topology in CLAUDE.md and the sibling guides back in
   sync (partially done on this branch — see §7).
6. **C3 (reload ordering), S4/S5, C4/C5, H1/H2 (hygiene), D1/D2/D4** — lower-risk
   correctness and cleanup.

## 7. Fixes applied on this branch (changelog)

**Security**

- **S1** — `core/tools/bash.go`: replaced the 3-literal `strings.Contains` safety floor
  with structural detection that survives flag reorder/merge/long-form and whitespace
  (`rm -fr /`, `rm -r -f /`, `rm --recursive --force /`), normalises the fork bomb
  (whitespace-insensitive, function-name agnostic), and widens coverage to `mkfs.*`,
  `dd` / redirect to a block device, recursive `chmod`/`chown` of `/`, and `find / -delete`.
  Conservative (ordinary dev commands still pass) and covered by `TestSafetyFloorStructural`.
- **S2** — the terminal WebSocket no longer rides the master token in the URL. A new
  authenticated `POST /api/terminal/token` mints a 256-bit, 30 s TTL, single-use token
  (`termTokens` store in `server/terminal.go`); the client mints one immediately before
  the handshake and passes only that. Covered by `TestTerminalTokenSingleUse`;
  empty-server-token (unauthenticated) mode preserved.
- **S3** — `POST` (was `GET`) for `/api/providers/models` + `/api/providers/embedding-dim`,
  so a typed, not-yet-saved `api_key` travels in the body, never the query string
  (`server/provider_models.go` + the three `web/settings.js` call sites).
- **S4** — the folder zip-download (`server/folder_ops.go`) is now bounded — max
  20 000 entries, 2 GiB, depth 64, with a per-file `CopyN` cap — so `?path=/` can't try
  to stream the whole filesystem; it stops cleanly and appends a `_TRUNCATED.txt` note.

**Concurrency**

- **C1** — cross-session leak fixed. Every run tags its bus events with the real session
  id via `events.WithRootSession` (propagates into sub-agents like `WithCwd`), each bus
  payload carries `root_session_id`, and `emitBusEvent` drops any event not belonging to
  the subscriber's session — closing both the frame leak and the usage cross-contamination.
  Tagged on all three entry points (interactive, injected, A2A). `TestRootSessionContextRoundTrip`.
- **C2** — panic recovery added to every detached turn goroutine: the `streamEvents`
  ADK-stream goroutine (where tool/plugin panics surface), the `handleMessages` producer,
  and `injectTurnRouted` (mailbox/scheduler/spawn). A panicking turn now fails in isolation
  with a logged stack + error frame instead of crashing the process.
- **C3** — `Manager.Reload` (`agent/manager.go`) now only promotes when `nextGen >
  currentGen` and discards a superseded older build, so an out-of-order reload completion
  can't demote/delete the newer generation; teardown sweeps all idle sub-current
  generations. Passes the existing `-race` reload test.
- **C4** — `internal/steer/steer.go` self-prunes its map entry once a session's notes are
  fully drained, so the store no longer grows one empty entry per steered session
  (`TestStorePrunesEmptyEntries`).
- **C5** — added `RouteRegistry.Forget` / `SpawnRegistry.Forget` and a
  `forgetSessionState` helper wired into both the delete and archive handlers, so transient
  route/spawn/steer state is cleared when a session ends (goal kept across archive — it is
  persisted and resumes on unarchive).

**Docs & hygiene**

- Untracked the 48 MB `yoke` binary (`git rm --cached`) and gitignored it (H1).
- `make release` → `make package` in CLAUDE.md (×2) and README (D1); corrected the stale
  go-turbovec `replace` note (D2); rewrote the squad topology in CLAUDE.md +
  `docs/architecture.md` + `web/docs/10-architecture.md` to the real seven squads (D3,
  illustrative JSON how-to snippets elsewhere still use a `research` example name);
  documented five env vars (D4); and updated the provider-route, terminal-auth, and
  cross-session-bus sections of CLAUDE.md to match the code changes above.

**Not changed (deliberately):** P1 (reconnect-from-stale-turn frame skip) and P2 (mailbox
watcher `acquire` not ctx-aware) were left as documented low-severity edge cases; and the
illustrative `research`-squad JSON snippets in `docs/configuration.md` / `docs/extending.md`
are valid teaching examples, so they were kept.
