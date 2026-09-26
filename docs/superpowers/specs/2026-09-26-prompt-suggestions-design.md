# Prompt suggestions — design

**Status:** approved in conversation, 2026-09-26
**Branch:** `feat/prompt-suggestions`
**Scope:** web UI + omnis-server. CLI/TUI untouched.

## 1. Goal

After the agent replies, the web UI composer shows a greyed **suggested next
message** (as Claude Desktop does). Pressing **Tab** in an empty composer fills
the composer with it; the user reviews and sends with Enter. Nothing is ever
sent automatically.

Success criteria:

- A suggestion appears shortly after a turn ends, in the conversation's language.
- It costs one cheap model call per turn that is actually *displayed* — never a
  call for a session nobody is looking at.
- Tab keeps all its existing meanings (slash / `!` / `@` menu navigation, focus
  change) whenever a suggestion is not applicable.
- With the preference off, or the route never called, behaviour is
  byte-identical to today.

## 2. Decisions (from the brainstorming)

| Question | Decision |
|---|---|
| Which model? | The existing **`eval_model_ref`** (the `/goal` "small fast" model), falling back to the session's leader model — via `Manager.evalModel`. No new config key. |
| Default | **On**, user-disableable (Settings → Appearance), stored in `preferences.json`. |
| Display | **Plain placeholder**: visible only while the composer is empty; disappears on first keystroke; comes back if the user clears the field (until a message is sent). No prefix completion. |
| Delivery | **Pull with a server-side cache** (approach B): the client asks for a suggestion when a turn ends in a visible pane; the server generates on demand and caches per session keyed on the turn count. |

Rejected: server push after every turn (pays for undisplayed suggestions, loses
the suggestion on reload), and having the main agent emit a `<suggestion>` tag
(fleet-wide prompt change, pollutes replies, weak models forget it, expensive
model).

## 3. Server

### 3.1 Generation — `agent/prompt_suggest.go`

`func (m *Manager) SuggestNextPrompt(ctx context.Context, sessionID string, turns []Exchange) (string, bool)`

- Same isolated one-off-LLM pattern as `EvaluateGoal` / `GenerateTitle`: resolve
  the session's pinned instance (fallback `Current()`), model via
  `m.evalModel(ctx, inst)`, one non-streamed `GenerateContent`, no runner / tools
  / event bus — so nothing reaches any SSE stream.
- Input: the **last 3 exchanges** (user text + assistant text), rendered as a
  plain transcript, capped at `suggestTranscriptCap` (≈6000 runes), **keeping the
  tail** (the latest reply is what the suggestion answers).
- Request construction is split into a pure `buildSuggestRequest(turns)` so the
  cap and shape are unit-testable without a model.
- System prompt (English, the model answers in the conversation language):
  > You predict the user's next message in a chat with an AI assistant. Write the
  > single most likely next message the USER would send: one sentence, first
  > person, in the same language the user writes in, at most about 15 words. It
  > must move the conversation forward (a follow-up question, a next step, a
  > request to apply or refine). Output only the message — no quotes, no
  > preamble. If there is no natural follow-up, output exactly NONE.
- `cleanSuggestion(raw)` post-processes: trim; drop surrounding quotes/backticks;
  strip a leading label (`Suggestion:`, `User:`, `Next message:` …,
  case-insensitive); keep the first non-empty line. Output `NONE` (any case), empty,
  longer than `suggestMaxLen` (160) runes (dropped, not truncated — a suggestion
  ending in "…" is not sendable), or starting with `/`, `!` or `#` (the composer
  would run it as a slash command, a host shell escape or an AGENT.md write) ⇒
  "no suggestion".
- Returns `ok=false` only for **failures** (no instance, model build error, LLM
  error). A `NONE` answer is `("", true)` — a valid, cacheable "no suggestion".

### 3.2 Route — `server/prompt_suggest.go`

`GET /api/sessions/:id/suggestion` → `200 {"suggestion": string, "turns": int, "busy": bool}`
(auth group; `404` for an unknown session).

Returns `suggestion: ""` **without a model call** when any of:

- preference `prompt_suggestions` is explicitly `false`;
- the session is archived or hidden;
- the session has no turns;
- a turn is in flight (`RunGuard.busy(id)`) — answered with `busy: true`. `Turns`
  is bumped at turn start but the conversation file only gains the turn at the end,
  so generating now would cache a suggestion for the old state under the new key.
  The client's `done` can arrive before the guard is released, so the client
  retries (3 × 1 s) on `busy`.

Session fields are read through a new `Registry.Snapshot(id)` (a copy taken under
the registry lock) — reading `Archived`/`Hidden`/`Turns` through the pointer `Get`
returns would race their setters.

Otherwise:

- **Cache**: in-memory `suggestCache` on `serverDeps`:
  `map[sessionID]{turns int, text string}` + mutex. If the cached `turns` equals
  the session's current turn count, return it.
- On a miss: load `conversation_<id>.json`, map the last 3 turns to
  `agent.Exchange`, call the generator with a 20 s timeout derived from
  `d.rootCtx` (not the request context — a client that navigates away must not
  waste a half-finished call, and the result still warms the cache).
- **Single-flight per session**: concurrent requests for the same session share
  one generation (a small `map[sessionID]*call` with a done channel).
- A **failure** is not cached (a later display retries). A `NONE` result **is**
  cached as `""`.
- The generator is an injectable field (`serverDeps.suggestFn`, defaulting to
  `d.Manager.SuggestNextPrompt`) so the route is testable without a model.
- Cache entries are dropped in `deleteSession`, and explicitly by
  `handleRewind` (`d.Suggest.forget(id)`, right after `SetTurns`): the cache is
  keyed only on turn count, so relying on "the turn count changes" is not
  enough — rewinding from N turns back to N-1 and then resending (bringing the
  count back to N) could otherwise replay the discarded reply's cached
  suggestion. A fork is a new session id, so it needs nothing.
- `SuggestNextPrompt` resolves the session's instance via `Manager.Peek`, a
  non-pinning counterpart to `Lookup` added for this path: `Lookup` auto-pins
  an unpinned session to the current generation, and this read-only path has
  no matching `Release`, so pinning here would leak the generation's refcount
  forever (reachable when a session is archived/deleted between the route's
  `Snapshot` and this call). `Peek` returns the pinned instance or `nil` with
  no side effect; the existing `Current()` fallback is unchanged.

### 3.3 Preference

`preferences.PromptSuggestions *bool` (`json:"prompt_suggestions,omitempty"`) in
[server/preferences.go](../../../server/preferences.go). Absent ⇒ enabled;
explicit `false` ⇒ disabled. Merged by the existing `PUT /api/preferences`
(load → bind → save), so it does not clobber theme/locale/notifications. The
route reads it on each request (the store is a tiny file behind a mutex) so a
stale tab or a direct call cannot spend model calls while the user has it off.

## 4. Web client — [web/app.js](../../../web/app.js)

### 4.1 State and fetching

- `sessionSuggestion`: `Map<sessionId, text>`, cleared in
  `forgetSession`. Per session, not per pane, so it survives tab switches.
- `fetchSuggestion(sessionId)`: no-op unless the preference is on (localStorage
  read-cache) **and** some pane shows the session as its active tab
  (`panelsForSession(id).length > 0`). A per-session sequence number drops stale
  responses. On success it stores the result and repaints affected panes.
- Called:
  1. at the end of a local turn (send-path `finally`, outcome `done` or `reload`);
  2. at the end of an observed remote/injected turn (`endRemoteBusy`);
  3. on session open / tab reactivation (`activateTab`) — always; the server
     cache makes a repeat free, so the client keeps no turn-count bookkeeping;
  4. explicitly from the `subscribeGlobalEvents` `mailbox_push` /
     `task_notification` / `schedule_run` handlers. Mailbox delivery,
     background-task active wake, and `/loop` all inject a turn into an
     already-viewed session with no `turn_started` broadcast, so `remoteBusy`
     is never armed for it and `endRemoteBusy`'s own `fetchSuggestion` call
     (item 2) returns early (`if (!remoteBusy.has(sid)) return;`) — these three
     handlers call `clearSuggestion`/`fetchSuggestion` themselves so the
     composer doesn't keep offering the previous reply's suggestion. A
     redundant fetch alongside `endRemoteBusy`'s (when `remoteBusy` happened to
     be armed) is harmless: `fetchSuggestion`'s sequence guard and the server
     cache/single-flight make the extra call a no-op.
- Cleared (and panes repainted) the moment a send or steer starts
  (`sendMessage`, `steerMessage`), and on the `session_rewound` event.

### 4.2 Display — the native placeholder

The composer textarea's text is transparent (the `@file` highlight backdrop
draws it), but `::placeholder` has its own colour, so the suggestion is shown by
swapping the textarea's `placeholder` text — no change to the backdrop layer, and
the browser already implements "hide on first keystroke, show again when empty".

- **Single source of truth** `composerPlaceholder(panel)`: archived message if
  the session is archived; else the suggestion if one exists and no turn is in
  flight; else the default placeholder. It replaces the two duplicated hard-coded
  strings in `setComposerReadOnly` and `updateEditModeBtn`, which now use the
  i18n keys (`composer.placeholder`, new `composer.placeholderCtrl`,
  `composer.archivedPlaceholder`) instead of English literals.
- `applyComposerPlaceholder(panel)` sets the attribute and toggles
  `.has-suggestion` on the composer wrapper. Called from `applySessionUI` and from
  the suggestion store's repaint.
- CSS ([web/css/features/composer.css](../../../web/css/features/composer.css)):
  `.has-suggestion #prompt::placeholder` in italics; a small `Tab ⇥` badge
  (`.prompt-suggest-hint`, part of the pane template) shown only when
  `.has-suggestion` is set **and** the textarea is empty (`#prompt:placeholder-shown`
  sibling selector), so it disappears with the placeholder.

### 4.3 Tab to accept

In `onPromptKeydown`, **after** the slash / `!` / `@` menu block (which keeps
priority), accept when **all** hold: key is `Tab` without Shift/Ctrl/Alt/Meta;
the menu is hidden; the textarea value is empty; not composing (IME); the pane's
session has a suggestion and no turn is in flight. Then: `preventDefault`, set the
value, caret to the end, dispatch `input` (auto-grow + highlight). **Never send.**
In every other case Tab keeps its default behaviour.

### 4.4 Preference toggle

Settings → Appearance (`renderAppearance` in
[web/settings.js](../../../web/settings.js)) gains a "Reply suggestions" toggle:
localStorage read-cache (`agent_toolkit_prompt_suggestions`), persisted via
`PUT /api/preferences {prompt_suggestions}`, seeded from the server prefs in
`syncThemeFromServer` — mirroring `saveNotifications`. Turning it off clears every
visible suggestion immediately; turning it on fetches for the visible sessions.

### 4.5 Out of scope

The Settings assistant, collection-context assistant and agent-instruction
assistant (own composers), archived sessions, CLI and TUI.

## 5. i18n

New keys (en/fr/es/de): `composer.placeholderCtrl`, `composer.archivedPlaceholder`,
`composer.suggestHint`, `appearance.suggestions`, `appearance.suggestionsLabel`,
`appearance.suggestionsHint` (the Appearance panel's existing namespace). `make i18n`, bump the `?v=` query strings in
`web/index.html` for `app.js`, `settings.js`, `i18n/locales.js`.

## 6. Testing

- **Go, `agent/`**: `cleanSuggestion` (quotes, labels, multi-line, `NONE`,
  length cap); `buildSuggestRequest` (last 3 turns only, tail kept under the cap).
- **Go, `server/`** (real gin router, fake `suggestFn` counting calls): cache hit
  on unchanged turn count (1 call for 2 requests); new turn ⇒ new call; failure
  not cached; `NONE` cached; preference `false` / archived / no turns ⇒ `""` and
  zero calls; concurrent requests single-flight to one call; delete drops the
  entry.
- **Web**: Playwright smoke against a branch server (`OMNIS_WEB_DIR=$(pwd)/web`):
  after a turn the placeholder shows the suggestion; Tab fills the composer; with
  the `/` menu open Tab still navigates the menu; toggle off ⇒ default placeholder.

## 7. Documentation

- `internal/features/FEATURES.md`: bullet under `## 1.10 (in development)`.
- `CLAUDE.md`: a "Prompt suggestions (Web UI)" section (route, cache key,
  placeholder mechanism, Tab precedence, preference, no-op contract).
- `web/docs/02-composer.md`: user-facing note.

## 8. No-op contract

Preference off, or the client never calling the route ⇒ no model call, the
default placeholder, Tab unchanged — byte-identical to before. The turn-execution
path (`handleMessages`, `injectTurnRouted`) is not modified.
