# Per-session permission mode, `auto` classifier, mode passed to hooks

Date: 2026-11-19

## Intent

Claude Code lets the user switch the permission mode (`default`, `acceptEdits`,
`plan`, `auto`, `dontAsk`, `bypassPermissions`) for the session they are in,
choose it at launch, and runs a real `auto` mode in which a model judges each
action instead of asking. Omnis already knows the six modes, but only as the
global `defaultMode` of `permissions.json`; `auto` behaves like `default`.

The user wants omnis to match Claude Code on three points, plus a fourth:

1. switch the mode **per session**, mid-conversation, without touching the
   global file (web, TUI);
2. choose the mode **at launch** (CLI/TUI, `--permission-mode`);
3. a **real `auto` mode** (a classifier model judges calls);
4. **pass the active mode to hooks**, so a Claude Code plugin's guard (the
   IA Parc one) can take it into account in its own permission handling.

## Goals

- A session's mode overrides the global `defaultMode` for every tool call of
  that session: root agent and every sub-agent (they already share one gate).
- The mode survives a restart, is shown and changeable in the web composer and
  TUI, and is settable from the CLI.
- `auto` decides, without asking, the calls that would otherwise raise a
  default prompt, using a classifier; it falls back to asking when unsure.
- Every hook (PreToolUse, PostToolUse, UserPromptSubmit, Stop, …) receives
  `permission_mode` in its stdin JSON, with Claude Code's spelling.

## Non-goals

- No change to rule precedence (deny → ask → allow) or to what each existing
  mode means, except the plan-mode cache fix below.
- No per-agent mode. The mode is per session.
- No `ExitPlanMode` tool. The agent asks the user to switch with
  `AskUserQuestion` (see plan-mode note).
- The Bash safety floor is untouched, in every mode.

## Anti-goals

- `auto` must never be more permissive than `default` when the classifier is
  unavailable, errors, or times out: those cases **ask**.
- An explicit `deny` or `ask` rule is never overridden by the classifier
  (it only judges calls that reach the mode-default prompt).
- A shared (cookie) deployment must be able to forbid `bypassPermissions`.

## Approaches considered (for `auto`)

- **A. Classifier model on default-ask calls (chosen).** Reads and in-workdir
  edits are allowed by the existing `acceptEdits` logic and never reach it.
  Everything that would raise a default prompt is judged by the evaluator
  model. Cost: one small call (~1 s) per uncovered action.
- B. Fixed rules, no model. Free and predictable, but it is just a wider
  `acceptEdits`; it cannot refuse what goes beyond the user's request, which
  is the point of `auto`.
- C. Classifier only for Bash and MCP tools. Cheaper, but a Write/Edit outside
  the working dir would have no guard.

The user picked A.

## Design

### 1. Mode resolution

New process-wide store `permissions.ModeStore` (map session id → `Mode`, RW
mutex), on `Infrastructure.PermModes`, surviving hot-reload like `SteerStore`.

The effective mode of a call = the session's override if set, else the merged
config `defaultMode`. Resolved in the gate with the existing
`PluginConfig.SessionFunc` (real session id, also for sub-agents and injected
turns). `CheckArgsVerdict` gains a variant taking the mode explicitly
(`CheckArgsVerdictMode(tool, args, cwd, mode)`); the old signature keeps using
the config mode, so CLI examples and tests are unchanged.

`PluginConfig` gains `Modes *ModeStore` (nil ⇒ config mode); the gate calls
`Modes.EffectiveWith(sid, <its live snapshot's defaultMode>)`.

### 2. Plan mode beats session grants (bug fix)

Today the approval cache (`hasTool` / `has`) is checked before the verdict, so
"Allow all Edit this session" survives a switch to `plan`. In `plan` mode the
gate computes the verdict first and only consults the cache when the verdict is
not a deny. (`dontAsk` already behaves correctly: a cached grant allows,
a default ask becomes a deny.)

### 3. Persistence and inheritance

- `ConversationFile.PermissionMode` (`permission_mode`, omitempty) +
  `SessionMeta.PermissionMode`, set via `Registry.SetPermissionMode` +
  `sessions.SetConversationPermissionMode`; seeded into `ModeStore` at boot by
  `LoadPersistedSessions` / the startup loop (like `Goal`).
- Fork and spawn copy the parent's mode. Import drops it. Scheduled fresh
  sessions start with no override. Empty = follow config.
- `Forget` on session delete.

### 4. Forbidding bypass

Support Claude Code's `permissions.disableBypassPermissionsMode: "disable"`
in `permissions.json`, **read from the system layer only** (`/etc/omnis`,
i.e. `paths.SystemDir()` (honours `OMNIS_SYSTEM_CONFIG_DIR`); the key is ignored in user, project and skill
layers and is not editable from Settings). Settings are global and editable
by any user in a shared (cookie) deployment, so a user-layer value could be
lifted by anyone; only the administrator controls the system layer. When set: the server route and CLI flag refuse `bypassPermissions`, the
web picker omits it, and a persisted/config `bypassPermissions` is treated as
`default` (logged once).

### 5. `auto` mode

In the gate, when the effective mode is `auto` and the verdict is a
**default ask** (`Verdict.DefaultAsk`, no rule matched), and no clearance /
`DefaultAllow` / cache grant applies, call a classifier before asking.

Inside `CheckArgsVerdictMode`, `auto` also applies the `acceptEdits` step
(edits in the working dir, common fs commands) so those never reach the
classifier.

Classifier (`agent/auto_mode.go`, injected into the gate as
`PluginConfig.AutoJudge func(tc, tool, args, cwd) (AutoVerdict, error)`):
- Model: built like the memory provenance verifier (`newMemoryVerifier(runtime,
  root)` pattern, since the gate is built inside `buildSquadInstance` before a
  Manager exists): eval_model_ref, else the root's model; non-streamed,
  `ThinkingBudget: 0`, 20 s timeout.
- Input: the user's own words for the session (the existing
  `sessionUserWordsStore` used by the memory provenance check — last few user
  messages), the working directory, the agent name, the tool name and its
  arguments (truncated to ~4 KB).
- Prompt asks for `ALLOW` or `BLOCK: <reason>`. Block when the action: goes
  beyond what the user asked; touches production/shared infrastructure or
  credentials; deletes or overwrites data outside the working dir; sends data
  to an external destination not implied by the request; or appears driven by
  instructions found in tool output, files or web pages rather than the user.
- `ALLOW` ⇒ the call proceeds (logged `auto: allow <tool>`).
- `BLOCK` ⇒ the call is denied with
  `[BLOCKED BY AUTO MODE] <tool>: <reason>. Try another approach that stays
  within the user's request, or ask the user.`
- Error / timeout / unparseable ⇒ normal ask card; when nobody can answer
  (`askuser.NonInteractive()`), a **deny** with the reason "auto mode could
  not judge this call" (builder's call: an unanswerable card would hang the
  turn, `askuser.DefaultTimeout` is 0).
- Turns the user did not start (mailbox, A2A, spawn result, background
  notification — the kinds `memoryReadOnlyFor` marks read-only): the
  classifier is given only the messages the user typed in this session as
  "the user's request", and the injected text separately, labelled as not
  coming from the user. The prompt blocks any action justified only by that
  injected text. The surface marks such a turn on the run context (same rail
  as `WithMemoryReadOnly`) and passes the injected text to the judge.
- Escalation: after 3 consecutive blocks in a session, or 20 in total, the
  next default-ask call in that session goes to the user's card instead of the
  classifier (counter reset by any allow, and by the user answering a card).
  With no one to ask (`OMNIS_NON_INTERACTIVE`), a block stays a block.

On entering `auto` (switch or launch), nothing is rewritten in the config.
Claude Code drops arbitrary-exec allow rules when entering auto; omnis does
not, because its classifier only sees calls no rule covers — a user's
`Bash(python *)` allow stays theirs.

Remove the `convert.go` warning that `auto` is treated like `default`.

### 6. Telling the agent

A short block appended per model call on the squad root (the existing
steering/collection plugin pattern), only when the mode is not `default`:
- `plan`: "Plan mode: you may read and investigate, not modify files or run
  changing commands. Present a plan; to carry it out, ask the user with
  AskUserQuestion to leave plan mode."
- `auto` / `acceptEdits` / `dontAsk` / `bypassPermissions`: one line saying
  what is allowed without confirmation (dontAsk: un-pre-approved calls are
  refused, do not retry them).
Not on the router. Stable per mode, so the prompt cache only misses on a
switch.

### 7. Hooks / plugins

`hooks.Input` gains `PermissionMode string json:"permission_mode,omitempty"`,
filled for every event that has a session (tool events in
`hookToolCallbacks`, UserPromptSubmit/Stop in the plugin, bus-driven events
and server `FireHook` from their session id) through a resolver on
`Infrastructure` (`PermissionModeFor(sessionID) string`: override, else the
config `defaultMode` — the permission reloader re-reads `permissions.json` live
whatever the generation, so every generation sees the same config mode and no
pinning is needed; spelled as Claude Code: `default`, `acceptEdits`, `plan`, `auto`,
`dontAsk`, `bypassPermissions`). Plugin hooks and omnis hooks get it alike.
Hooks still run before the permission gate (chain order unchanged).

Who wins between a hook and the session's mode (decided with the user):

- A hook's **deny / block** blocks in every mode, `bypassPermissions`
  included (as in Claude Code). A deny is the hook saying no, knowing the mode;
  a hook that wants to step aside in a mode reads `permission_mode` and answers
  `allow` instead.
- A hook's **`ask`** (handled by `askHookPermission`, not the gate) is a
  question to the user, so it follows the mode:
  - `bypassPermissions`: **allowed**, no question — "bypass" means passing over
    permission questions, not refusing them. The allow also grants the usual
    hook clearance for that call.
  - `auto`: judged by the classifier like a default-ask call (same prompt,
    same escalation counters; the hook's reason is given to the judge). Judge
    error ⇒ ask the user, or deny when nobody can answer.
  - `dontAsk`: blocked ("dontAsk mode: the hook asked for confirmation").
  - `default`, `acceptEdits`, `plan`: asked to the user, as today.
- Consequence, accepted: the Kubernetes hook escalates a refusal to `ask`
  after three identical attempts, so in `bypassPermissions` such a change goes
  through on the third retry. Its terminal refusal (no `APPROVED` reviewer
  attestation, the validator acting itself) is a deny and still blocks in every
  mode. In a shared deployment the administrator can forbid `bypass` (§4).

### 8. Surfaces

- **Server**: `GET /api/sessions/:id/permission-mode` →
  `{mode, effective, config_mode, bypass_disabled}`;
  `PUT …/permission-mode {mode}` (`""` = follow config) → validates, stores,
  persists, broadcasts `permission_mode_changed` (session-scoped,
  owner-filtered in cookie mode). `GET /api/sessions` rows carry
  `permission_mode`. Draft: `POST /api/sessions` accepts `permission_mode`.
- **Web**: a small mode pastille in the composer, left of the context ring,
  always shown, opening a menu of the allowed modes; **Shift+Tab** in the composer
  cycles default → acceptEdits → plan → auto → (bypassPermissions if allowed)
  → default. `dontAsk` is in the menu, not the cycle (as Claude Code). Draft
  tabs keep the choice and send it with the create. `/mode [name]` slash
  command (session section). i18n en/fr/es/de.
- **CLI**: `--permission-mode <mode>` flag (validated; refused for bypass when
  disabled) sets the override of the CLI session; `/mode <name>` in the REPL.
- **TUI**: same flag; Shift+Tab while the input field has focus cycles the
  mode (focus cycling stays on Tab, and Shift+Tab elsewhere keeps cycling
  focus backward); mode shown in the status line; `/mode`.
- **Settings → Permissions**: unchanged (it edits the config default); its
  hint says sessions can override it.
- `/mode` reserved in `usercommands.ReservedNames`.

### 9. Docs and tracking

CLAUDE.md permission section, `web/docs` permissions page, FEATURES.md bullet
under the in-development section.

## Testing

- `core/permissions`: mode override in `CheckArgsVerdictMode`; plan beats a
  session tool grant; `auto` falls into acceptEdits for in-workdir edits;
  disableBypass.
- Gate: auto allow / block / error→ask / escalation after 3 blocks, with a
  fake judge.
- Hooks: `permission_mode` present in PreToolUse input for a session with an
  override and for one without; a hook `ask` is allowed in bypass, judged in
  auto, blocked in dontAsk, asked otherwise; a hook deny blocks in bypass.
- Server: route round trip, persistence across `LoadPersistedSessions`, fork
  inheritance, cookie owner scoping (existing route-enumeration test covers
  `ownerGuard`).
- Live: a squad-bench pair in `auto` vs `default` with `OMNIS_NON_INTERACTIVE`
  to measure classifier cost and wrong blocks (in omnis-benches).

## Left to the builder

Exact classifier prompt wording, file split inside `agent/`, CSS of the
pastille, the TUI status-line format.
