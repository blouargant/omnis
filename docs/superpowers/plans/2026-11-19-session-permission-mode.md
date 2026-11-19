# Per-session permission mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let each session carry its own permission mode (switchable live, settable at launch), give `auto` a real classifier, and pass the mode to every hook, with the hook/mode precedence the user chose.

**Architecture:** A process-wide `permissions.ModeStore` holds per-session overrides and knows the live config mode; the permission gate and the hook callbacks resolve the effective mode through it. `auto` calls an injected `AutoJudge` (built in `agent/` like the memory provenance verifier) on default-ask calls and on hook asks. Surfaces (server, web, CLI, TUI) only read/write the store and persist the override on the conversation file.

**Tech Stack:** Go 1.26 (ADK v2 `google.golang.org/adk/v2`, gin), vanilla JS web UI, tview TUI.

**Spec:** `docs/superpowers/specs/2026-11-19-session-permission-mode-design.md`

## Global Constraints

- Mode strings are Claude Code's, exactly: `default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions` (existing `modeNames` in `core/permissions/permissions.go`).
- Precedence stays deny → ask → allow; the Bash safety floor is untouched in every mode.
- The classifier only sees default-ask calls (no rule matched) and hook asks; it never overrides an explicit `deny` or `ask` rule.
- Classifier: `eval_model_ref`, else the root's model; non-streamed; `ThinkingBudget: 0`; 20 s timeout; arguments truncated to 4096 bytes.
- Classifier unavailable / error / unparseable ⇒ ask the user; with `askuser.NonInteractive()` ⇒ deny with reason `auto mode could not judge this call`.
- Block message: `[BLOCKED BY AUTO MODE] <tool>: <reason>. Try another approach that stays within the user's request, or ask the user.`
- Escalation: after 3 consecutive or 20 total auto blocks in a session, default-ask calls go to the user's card; reset by an allow and by the user answering a card. Non-interactive: a block stays a block.
- Hook deny blocks in every mode. Hook `ask`: `bypassPermissions` ⇒ allow + clearance; `auto` ⇒ classifier; `dontAsk` ⇒ block (`dontAsk mode: the hook asked for confirmation`); others ⇒ ask the user.
- `permissions.disableBypassPermissionsMode: "disable"` is read **only** from `<paths.SystemConfigDir()>/permissions.json`.
- Router squad: no mode note, no hooks (unchanged).
- Web strings: i18n keys in en/fr/es/de, `make i18n`, bump `?v=` on changed scripts in `web/index.html`.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Mode switched mid-turn** — the next tool call (root or sub-agent) uses the new mode; nothing is cached per turn. Test in Task 2.
2. **Bypass forbidden after a session stored it** — a persisted or config `bypassPermissions` resolves to `default`, and the route refuses to set it. Test in Task 1 and Task 6.
3. **Sub-agent in an injected turn** — the gate keys on the real session (`SessionFunc` = `realSessionID`), so a sub-agent of a mailbox turn uses that session's mode and the injected-text labelling. Test in Task 3.
4. **Huge tool arguments** (a `Write` of a 200 KB file) — the classifier request stays bounded (4096 bytes of args). Test in Task 3.
5. **Plan mode with an existing session grant** — "Allow all Edit this session" does not let an `Edit` through in plan. Test in Task 2.

---

### Task 1: Mode-aware rule evaluation

**Files:**
- Modify: `core/permissions/permissions.go` (ParseMode area ~74; `CheckArgsVerdict` ~303; `checkArgsVerdict` + `checkMultiEdit` take a mode)
- Modify: `core/permissions/convert.go:89-91` (remove the auto warning)
- Create: `core/permissions/mode_store.go`
- Test: `core/permissions/mode_test.go`

**Interfaces:**
- Produces:
  - `func ParseModeStrict(s string) (Mode, bool)` — false for unknown strings (empty included).
  - `func (c *Config) CheckArgsVerdictMode(toolName string, args map[string]any, cwd string, mode Mode) Verdict`; `CheckArgsVerdict` = `CheckArgsVerdictMode(…, c.Permissions.DefaultMode)`.
  - `func ReadBypassDisabled(path string) bool` — true iff the file's `permissions.disableBypassPermissionsMode == "disable"`; missing/unparseable ⇒ false.
  - `type ModeStore struct` with `NewModeStore() *ModeStore`, `Set(sid string, m Mode)`, `Clear(sid string)`, `Get(sid string) (Mode, bool)`, `Forget(sid string)`, `SetConfigSource(src Source)`, `ConfigMode() Mode` (src nil ⇒ `ModeDefault`), `SetBypassDisabled(bool)`, `BypassDisabled() bool`, `Effective(sid string) Mode` (override else config; `ModeBypass` ⇒ `ModeDefault` when disabled, logged once per session), and the auto counters `AutoBlocked(sid string) (escalate bool)`, `AutoAllowed(sid string)`, `AutoEscalated(sid string) bool`, `ResetAuto(sid string)`. A nil `*ModeStore` is safe (Effective ⇒ `ModeDefault`, counters no-op).

- [ ] **Step 1: Write the failing tests** in `mode_test.go`:
  - `TestCheckArgsVerdictModeOverridesConfig`: config `defaultMode: default`, an unmatched `Bash(make deploy)` → `CheckArgsVerdictMode(..., ModeDontAsk)` is `DecisionDeny`; with `ModeBypass` is `DecisionAllow`; `CheckArgsVerdict` is a default ask.
  - `TestAutoAppliesAcceptEditsStep`: `Edit` of `<cwd>/a.go` with `ModeAuto` → allow, reason `acceptEdits mode`; `Edit` of `/etc/x` with `ModeAuto` → `DecisionAsk` with `DefaultAsk == true`.
  - `TestMultiEditUsesGivenMode`: a MultiEdit of two in-cwd files in `ModePlan` → deny.
  - `TestReadBypassDisabled`: file with `{"permissions":{"disableBypassPermissionsMode":"disable"}}` → true; other value / missing file → false.
  - `TestModeStoreEffective`: no override ⇒ config mode; override wins; `Clear` restores config; bypass disabled turns an override `ModeBypass` into `ModeDefault`; nil store ⇒ `ModeDefault`.
  - `TestModeStoreAutoEscalation`: 3 consecutive `AutoBlocked` ⇒ third returns true and `AutoEscalated` true; `AutoAllowed` resets consecutive but not total; 20 total (interleaved allows) ⇒ escalated; `ResetAuto` clears both.
  - `TestConvertNoAutoWarning`: converting a config with `defaultMode: auto` yields no warning mentioning `auto`.
- [ ] **Step 2: Run** `go test ./core/permissions -run 'TestCheckArgsVerdictMode|TestAutoApplies|TestMultiEditUsesGivenMode|TestReadBypassDisabled|TestModeStore|TestConvertNoAutoWarning'` — expect FAIL (undefined symbols).
- [ ] **Step 3: Implement.** Thread `mode Mode` through `checkArgsVerdict`/`checkMultiEdit` (replace `mode := ps.DefaultMode`); step 6 condition becomes `mode == ModeAcceptEdits || mode == ModeAuto`; delete the convert.go auto warning; write `mode_store.go` (RWMutex maps, `atomic.Bool` for bypass, `sync.Map` once-log).
- [ ] **Step 4: Run** `go test ./core/permissions/...` — expect PASS (including existing `TestShippedConfigParity`).
- [ ] **Step 5: Commit** `feat(permissions): mode-aware verdicts and a per-session mode store`.

### Task 2: Gate uses the session mode; plan beats session grants; auto judge

**Files:**
- Modify: `core/permissions/permissions.go` (`PluginConfig` ~852, `NewGate` callback ~950)
- Create: `core/permissions/auto.go`
- Test: `core/permissions/gate_mode_test.go`

**Interfaces:**
- Consumes: Task 1 `ModeStore`, `CheckArgsVerdictMode`.
- Produces:
  - `type AutoVerdict struct { Allow bool; Reason string }`
  - `type AutoJudge func(tc adk.ToolContext, toolName string, args map[string]any, cwd, why string) (AutoVerdict, error)` — `why` is the gate's or hook's reason.
  - `PluginConfig.Modes *ModeStore` (nil ⇒ config mode, today's behaviour) and `PluginConfig.AutoJudge AutoJudge` (nil ⇒ auto behaves like default: ask).
  - `func AutoBlockMessage(tool, reason string) string` (the exact Global Constraints text).
  - `func JudgeOrAsk(…)` is NOT exported; the hook side calls the judge directly (Task 4).

- [ ] **Step 1: Write failing tests** (fake `Asker` counting calls, fake `AutoJudge`, `Static` source, `Modes` with overrides):
  - `TestGateUsesSessionOverride`: config `default`, session override `dontAsk` ⇒ unmatched Bash returns `[DENIED]` without asking; switching the override to `bypassPermissions` before the next call ⇒ allowed (Review Focus 1).
  - `TestGatePlanIgnoresToolGrant`: user answers `OutcomeAllowToolSession` for `Edit` in `default`, then override `plan` ⇒ next `Edit` returns `[DENIED] Edit: plan mode forbids edits` (Review Focus 5). In `default`, the grant still allows.
  - `TestGateAutoAllow`: override `auto`, judge returns Allow ⇒ no ask, nil result.
  - `TestGateAutoBlock`: judge returns `{false,"deletes outside the workdir"}` ⇒ output equals `AutoBlockMessage("Bash","deletes outside the workdir")`.
  - `TestGateAutoErrorAsks`: judge returns error ⇒ asker called once; with `OMNIS_NON_INTERACTIVE=1` ⇒ `[DENIED] Bash: auto mode could not judge this call`, asker not called.
  - `TestGateAutoEscalates`: 3 blocks ⇒ 4th default-ask call goes to the asker, judge not called; the user's answer resets (`ResetAuto`) so the 5th call is judged again. Non-interactive: 4th call is judged and blocked.
  - `TestGateAutoNeverOverridesAskRule`: explicit `ask` rule matches ⇒ asker called, judge not called.
  - `TestGateClearanceBeforeJudge`: a granted clearance on a default ask ⇒ allowed, judge not called.
- [ ] **Step 2: Run** `go test ./core/permissions -run TestGate` — expect FAIL.
- [ ] **Step 3: Implement** in the gate callback: `mode := cfg.Modes.Effective(sid)` when `Modes != nil` else snapshot mode; when `mode == ModePlan` compute the verdict first and return on deny before the cache checks; otherwise keep cache-first. Verdict via `CheckArgsVerdictMode`. After the clearance/DefaultAllow check: if `decision == DecisionAsk && v.DefaultAsk && mode == ModeAuto && cfg.AutoJudge != nil && !(cfg.Modes.AutoEscalated(sid) && !askuser-noninteractive)` call the judge (allow ⇒ `AutoAllowed`, return nil; block ⇒ `AutoBlocked`, return message; error ⇒ non-interactive deny, else fall through to ask). Any user answer to the card calls `cfg.Modes.ResetAuto(sid)`. Read non-interactive through `askuser.NonInteractive()` (core/permissions may import `internal/askuser`? it must not — pass `NonInteractive func() bool` in `PluginConfig`, defaulting to `func() bool { return false }`).
- [ ] **Step 4: Run** `go test ./core/permissions/...` — PASS.
- [ ] **Step 5: Commit** `feat(permissions): gate follows the session mode and judges auto calls`.

### Task 3: The classifier and its wiring

**Files:**
- Create: `agent/auto_mode.go`, `agent/injected_turn.go`
- Modify: `agent/infrastructure.go` (`PermModes *permissions.ModeStore` field, built in `BuildInfrastructure`, `SetBypassDisabled(permissions.ReadBypassDisabled(filepath.Join(paths.SystemConfigDir(),"permissions.json")))`), `agent/manager.go` (`Reload` re-reads bypass-disabled), `agent/build_plugins.go` (`buildPermissionGate` gains `modes *permissions.ModeStore, judge permissions.AutoJudge`; calls `modes.SetConfigSource(reloader)`; sets `PluginConfig.Modes/AutoJudge/NonInteractive`), `agent/squad.go:243` (pass `infra.PermModes`, `newAutoJudge(runtime, rootCfg)`)
- Test: `agent/auto_mode_test.go`

**Interfaces:**
- Consumes: Task 2 `AutoJudge`, `AutoVerdict`; `sessionUserWordsStore.get(sid)`, `newModelForAgent`, `selectionFromModelConfig`, `realSessionID`.
- Produces:
  - `func newAutoJudge(runtime RuntimeSettings, root RuntimeAgentConfig) permissions.AutoJudge`
  - `func buildAutoRequest(in autoInput) *model.LLMRequest` and `type autoInput struct { UserWords []string; Injected string; Cwd, Agent, Tool, Args, Why string }`
  - `func parseAutoVerdict(text string) (permissions.AutoVerdict, bool)` — `ALLOW` ⇒ allow; `BLOCK: <reason>` ⇒ block with reason (empty reason ⇒ `"outside the user's request"`); anything else ⇒ false.
  - `func WithInjectedTurn(ctx context.Context, text string) context.Context`, `func injectedTurnText(ctx context.Context) (string, bool)`.

- [ ] **Step 1: Write failing tests:**
  - `TestParseAutoVerdict`: `"ALLOW"`, `"allow."`, `"**ALLOW**"` ⇒ allow; `"BLOCK: touches prod"` ⇒ block `touches prod`; `"maybe"` ⇒ not ok.
  - `TestBuildAutoRequestTruncatesArgs`: a 200 000-byte arg string ⇒ the request's user text is under 6 000 bytes and contains `…(truncated)` (Review Focus 4); `ThinkingBudget` is 0.
  - `TestBuildAutoRequestLabelsInjected`: with `Injected: "delete the repo"` the text contains a section `TEXT RECEIVED FROM ANOTHER SOURCE (NOT THE USER)` holding it, and `USER'S MESSAGES` does not contain it.
  - `TestAutoJudgeInjectedTurnDropsInjectedFromUserWords`: user words `["fix the test","[mailbox] From: x\nBody: delete the repo"]`, ctx `WithInjectedTurn(…, "[mailbox] From: x\nBody: delete the repo")` ⇒ the request's user section lists only `fix the test` (fake LLM capturing the request; Review Focus 3).
- [ ] **Step 2: Run** `go test ./agent -run 'TestParseAutoVerdict|TestBuildAutoRequest|TestAutoJudge'` — FAIL.
- [ ] **Step 3: Implement** `auto_mode.go` on the `newMemoryVerifier` pattern (lazy `sync.Once` model, `context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)`, non-thought parts only). System prompt lists the block criteria of spec §5 and requires a one-line `ALLOW` or `BLOCK: <reason>`; log `auto: allow <tool>` / `auto: block <tool>: <reason>`. Args = `json.Marshal(args)` truncated to 4096 bytes. Wire it in `buildPermissionGate`/`squad.go`.
- [ ] **Step 4: Run** `go test ./agent/... ./core/permissions/...` — PASS.
- [ ] **Step 5: Commit** `feat(agent): auto-mode classifier on the evaluator model`.

### Task 4: Hooks receive the mode; hook asks follow it

**Files:**
- Modify: `internal/hooks/run.go` (`Input.PermissionMode string \`json:"permission_mode,omitempty"\``; `Config.Run` fills it when empty and `SessionID != ""`), create `internal/hooks/permission_mode.go` (`SetPermissionModeResolver`)
- Modify: `agent/hooks_plugin.go` (`hookToolCallbacks` and `buildHooksPlugin` gain `perm hookPermDeps`), `agent/hooks_ask.go`, `agent/squad.go:258`, `agent/build_plugins.go:88`, `agent/infrastructure.go` (`PermissionModeFor`, installs the hooks resolver)
- Test: `internal/hooks/permission_mode_test.go`, `agent/hooks_mode_test.go`

**Interfaces:**
- Consumes: Task 1 `ModeStore.Effective`, Task 2 `AutoJudge`, `AutoBlockMessage`.
- Produces:
  - `func SetPermissionModeResolver(fn func(sessionID string) string)` (package `hooks`, process-wide, nil ⇒ field left empty).
  - `func (i *Infrastructure) PermissionModeFor(sessionID string) string` = `i.PermModes.Effective(sid).String()`.
  - `type hookPermDeps struct { Modes *permissions.ModeStore; Judge permissions.AutoJudge }`.

- [ ] **Step 1: Write failing tests:**
  - `TestRunFillsPermissionMode`: resolver returns `"plan"`; a hook command `cat > $OUT` receives JSON with `"permission_mode":"plan"`; empty session id ⇒ field absent.
  - `agent/hooks_mode_test.go` with a PreToolUse hook script that prints `{"hookSpecificOutput":{"permissionDecision":"ask"}}`:
    - `TestHookAskBypassAllows`: override bypass ⇒ tool runs, no question raised (fake registry counts asks), clearance granted.
    - `TestHookAskAutoJudged`: override auto, judge block ⇒ output is `AutoBlockMessage`; judge allow ⇒ runs + clearance.
    - `TestHookAskDontAskBlocks`: ⇒ `[BLOCKED BY HOOK] Bash: dontAsk mode: the hook asked for confirmation`.
    - `TestHookAskDefaultAsks`: ⇒ one question raised.
    - `TestHookDenyBlocksInBypass`: a script exiting 2 ⇒ `[BLOCKED BY HOOK]` under bypass.
- [ ] **Step 2: Run** `go test ./internal/hooks ./agent -run 'TestRunFillsPermissionMode|TestHook'` — FAIL.
- [ ] **Step 3: Implement.** In `beforeTool`'s `out.Asks()` branch, switch on `perm.Modes.Effective(sid)` before `canEscalate`: bypass ⇒ `RecordOutcome(false)`, `grant()`, nil; dontAsk ⇒ `RecordOutcome(true)`, block text; auto with judge ⇒ judge(tc, t.Name(), args, cwd, reason): allow ⇒ grant, block ⇒ message, error ⇒ fall through to the existing ask path (which already denies when non-interactive). Install the resolver in `BuildInfrastructure`.
- [ ] **Step 4: Run** `go test ./internal/hooks/... ./agent/... ./packaging/...` — PASS (k8s guard tests unchanged).
- [ ] **Step 5: Commit** `feat(hooks): pass permission_mode and apply the session mode to hook asks`.

### Task 5: Tell the agent its mode

**Files:**
- Create: `agent/perm_mode_note.go`
- Modify: `agent/build_plugins.go` (mount on non-router roots, beside the steering plugin)
- Test: `agent/perm_mode_note_test.go`

**Interfaces:**
- Produces: `func permModeNote(m permissions.Mode) string` (`""` for `ModeDefault`), `func permModePlugin(modes *permissions.ModeStore) (*plugin.Plugin, error)` — BeforeModelCallback appending the note to `req.Config.SystemInstruction` keyed on `ctx.SessionID()`.

- [ ] **Step 1: Failing test** `TestPermModeNote`: default ⇒ `""`; plan contains `AskUserQuestion` and `leave plan mode`; dontAsk contains `do not retry`; each non-default mode returns a distinct one-line-ish text; `TestPermModePluginAppends` checks the instruction gains the note for an override `plan` and is unchanged for `default`.
- [ ] **Step 2: Run** `go test ./agent -run TestPermMode` — FAIL.
- [ ] **Step 3: Implement** with the plan wording from spec §6.
- [ ] **Step 4: Run** `go test ./agent/...` — PASS.
- [ ] **Step 5: Commit** `feat(agent): tell the root agent its permission mode`.

### Task 6: Persistence and server routes

**Files:**
- Modify: `internal/sessions/history.go` (`ConversationFile.PermissionMode \`json:"permission_mode,omitempty"\``, `SetConversationPermissionMode`, `ForkConversation` copies it, `LoadPersistedSessions` maps it), `internal/sessions/sessions.go` (`SessionMeta.PermissionMode`, `Registry.SetPermissionMode(id, mode string) bool`)
- Create: `server/permission_mode.go`
- Modify: `server/server.go` (routes; `POST /sessions` accepts `permission_mode`; delete ⇒ `PermModes.Forget`), `server/main.go` (boot seed next to `GoalStore.Restore`), `server/fork_rewind.go` (store copy), `server/spawn.go` (`materializeSession` copies parent's override), `server/export_import.go` (drop), `server/mailbox_push.go:641` + `server/a2a_server.go:576` (`WithInjectedTurn(ctx, answerPrompt)` when `o.MemoryReadOnly`; a2a with the message text)
- Test: `server/permission_mode_test.go`

**Interfaces:**
- Consumes: Task 1 `ModeStore`, `ParseModeStrict`; Task 3 `WithInjectedTurn`.
- Produces: `GET /api/sessions/:id/permission-mode` → `{"mode":"","effective":"acceptEdits","config_mode":"acceptEdits","bypass_disabled":false}`; `PUT` `{"mode":"plan"}` → same shape; `""` clears; unknown ⇒ 400; `bypassPermissions` while disabled ⇒ 403; archived session ⇒ 409. Event `permission_mode_changed` with `Data {mode, effective}` via `broadcastData`. `GET /api/sessions` rows carry `permission_mode`.

- [ ] **Step 1: Failing tests** (drive the real router like `TestSearchSessionsRoute`):
  - `TestPermissionModeRoundTrip`: PUT plan → GET effective plan → conversation file holds `"permission_mode":"plan"` → PUT `""` clears it.
  - `TestPermissionModeRejects`: `"yolo"` ⇒ 400; bypass with `SetBypassDisabled(true)` ⇒ 403 (Review Focus 2).
  - `TestPermissionModeSurvivesReload`: write a conversation file with `permission_mode: auto`, `LoadPersistedSessions` + seed ⇒ `PermModes.Get` = auto.
  - `TestForkInheritsPermissionMode`, `TestImportDropsPermissionMode`, `TestCreateSessionWithPermissionMode`.
  - `TestPermissionModeChangedEvent`: `/api/events` receives `permission_mode_changed` for the session.
- [ ] **Step 2: Run** `go test ./server ./internal/sessions -run 'PermissionMode|Fork|Import'` — FAIL.
- [ ] **Step 3: Implement** the route file, persistence, seeds and copies. `TestOwnerGuardCoversEveryIDRoute` covers cookie scoping automatically.
- [ ] **Step 4: Run** `go test ./server/... ./internal/sessions/...` — PASS.
- [ ] **Step 5: Commit** `feat(server): per-session permission mode routes and persistence`.

### Task 7: CLI and TUI

**Files:**
- Modify: `main.go` (`newFlagSet`: `--permission-mode`), `internal/cli/cli.go` (`Config.PermModes *permissions.ModeStore`, `Config.PermissionMode string`; set override for `cfg.SessionID`; `/mode [name]` in `runRepl`), `internal/tui/tui.go` (same config fields; `/mode`; `KeyBacktab` at :1891 cycles the mode when the input field has focus; status line shows `mode: <name>` when not default), `internal/usercommands/usercommands.go` (`"mode"` in `ReservedNames`)
- Create: `internal/cli/permmode.go` with `func cycleMode(cur permissions.Mode, bypassAllowed bool) permissions.Mode` (default → acceptEdits → plan → auto → bypass? → default) and `func applyModeCommand(store *permissions.ModeStore, sid, arg string) (string, error)` (shared by TUI via import)
- Test: `internal/cli/permmode_test.go`

- [ ] **Step 1: Failing tests:** `TestCycleMode` (with and without bypass; dontAsk → default); `TestApplyModeCommand` (no arg ⇒ reports current; `plan` sets it; `bypassPermissions` refused when disabled; unknown ⇒ error listing the six names); `TestFlagRejectsUnknownMode` via `newFlagSet` + validation helper.
- [ ] **Step 2: Run** `go test ./internal/cli ./ -run 'Mode'` — FAIL.
- [ ] **Step 3: Implement**; the flag value is validated with `ParseModeStrict` and the bypass check before the session starts (error + exit 2).
- [ ] **Step 4: Run** `go test ./internal/cli/... ./internal/tui/... .` — PASS; manual: `go run . --permission-mode plan` then `/mode` prints `plan`.
- [ ] **Step 5: Commit** `feat(cli,tui): --permission-mode flag, /mode and Shift+Tab`.

### Task 8: Web UI

**Files:**
- Create: `web/perm-mode.js` (pure helpers `cycleMode(mode, bypassAllowed)`, `modeLabelKey(mode)`), `web/tests/perm_mode.test.mjs`
- Modify: `web/index.html` (pastille `.perm-mode-btn` in `.composer-actions` left of the context ring; script tag; `?v=` bumps), `web/app.js` (per-session cache `sessionPermMode`; fetch on `mountInPanel`; menu via `showFolderCtxMenu`; Shift+Tab in `onPromptKeydown` before the suggestion Tab handling, only when no slash/`!`/`@` menu is open; `/mode` in `BUILTIN_SLASH_COMMANDS` kind `session`; `permission_mode_changed` in `subscribeGlobalEvents`; drafts keep `draftPermModes[key]` and send `permission_mode` in `newChat`), `web/css/features/composer.css` (pastille; `pointer-events: auto; position: relative; z-index: 1`), `web/i18n/{en,fr,es,de}.json` + `make i18n`, `web/settings.js` (Permissions hint: sessions can override)

- [ ] **Step 1: Failing test** `perm_mode.test.mjs`: cycle order with/without bypass; dontAsk → default; every mode has a label key present in `web/i18n/en.json`.
- [ ] **Step 2: Run** `make web-test` — FAIL.
- [ ] **Step 3: Implement.** Mode names stay untranslated identifiers in the menu, with a translated one-line description each.
- [ ] **Step 4: Verify:** `make web-test` PASS; start the server with `OMNIS_WEB_DIR=$(pwd)/web` (launch.json), open a chat, Shift+Tab cycles the pastille, a second tab shows the change after `permission_mode_changed`, a draft started in `plan` keeps it after the first message; screenshot.
- [ ] **Step 5: Commit** `feat(web): permission-mode pastille, menu, Shift+Tab and /mode`.

### Task 9: Docs and live check

**Files:**
- Modify: `CLAUDE.md` (permission section: session mode, auto classifier, hook precedence table, `permission_mode` hook input, `disableBypassPermissionsMode`; hooks section: new `Input` field), `web/docs/` permissions page, `internal/features/FEATURES.md` (bullet under the in-development section)
- Bench (in `../omnis-benches`): a squad-bench pair `auto` vs `default` with `OMNIS_NON_INTERACTIVE=1`

- [ ] **Step 1:** Write the docs; run `go test ./internal/features/...` — PASS (FEATURES.md parses).
- [ ] **Step 2:** Run the bench pair on 5 coding tasks; record classifier calls, cost and wrong blocks in `omnis-benches/reports/permission-mode-auto-2026-11-19.md`.
- [ ] **Step 3:** `make test` — PASS.
- [ ] **Step 4: Commit** both repos: `docs: per-session permission mode` / `bench: auto vs default permission mode`.
