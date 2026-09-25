# Durable agent questions (`AskUserQuestion` survives a server restart)

**Status:** approved design, 2026-09-25. Branch `feat/durable-ask-user`
(stacked on `chore/adk-v2-migration`).

## Problem

When an agent asks the user a question with `AskUserQuestion`, the question
lives only in process memory (`internal/askuser.Registry`). A server restart —
an upgrade, a crash, `omnis-server stop/start` — loses it. The user comes back
to a session that shows no question, while the task it belonged to has stopped
with no explanation.

A graceful shutdown makes it worse: the run context is cancelled, so the
question is *resolved as cancelled* and removed from the registry before the
process even exits.

## Goal and non-goals

**Goal.** A question asked by an agent (`AskUserQuestion`) survives a server
restart. After the restart it reappears in the web UI. Answering it starts a
new turn that continues the task from that answer.

**Not in scope:**

- **Exact native resumption** of the interrupted turn. ADK v2 has native
  human-in-the-loop (`RequestConfirmation`, workflow `RequestInput`), but it
  does not fit omnis today: `agenttool` runs sub-agents in a throwaway
  in-memory session and does not propagate an interrupt to the caller, and
  omnis's squad sessions are in memory, so there is nothing to resume from
  after a restart. The work done inside the interrupted turn is lost; the
  question is not.
- **Technical cards** — permission prompts, hook escalations, the per-turn
  budget card, settings confirmations, dependency installs, MCP `${input:…}`
  prompts. Each one gates one specific tool call that no longer exists after a
  restart, and the agent asks again if it redoes the action. They keep today's
  behaviour: lost on restart.
- **CLI and TUI.** They install no persister, so they behave exactly as today.

## Design

### 1. Durable questions

`askuser.Question` gains two fields:

- `Durable bool` — this question must survive a restart.
- `Agent string` — the agent that asked it (for the resume prompt).

`AskUserQuestion` (`core/tools/ask_user.go`) sets both. No other caller
changes, so every other card stays non-durable. A third field, `Resumed bool`,
is set only by `Restore` (section 4) so the UI can label a restored question.

`AskUserQuestion` is mounted only on squad roots (`agent/squad.go`), so the
tool's `SessionID()` is always the user-facing session.

**Bug fix folded in.** `AskUserQuestion` currently waits on
`context.Background()`, so the Stop button does not release a pending
question: the turn stays blocked until the user answers. It must wait on the
tool context (the run context), exactly like the permission asker already
does. That is also what lets us tell Stop (abandon) from shutdown (keep).

### 2. A persister hook on the registry

```go
// Persister stores durable questions. Implementations must be safe for
// concurrent use. Only questions with Durable=true are passed to it.
type Persister interface {
    Save(q Question)                  // q is now pending
    Remove(q Question, answered bool) // q was answered, or abandoned
}

func (r *Registry) SetPersister(p Persister)
```

- `Ask` calls `Save` after registering a durable question.
- `resolveInternal` calls `Remove(q, answered)`: `answered` is true for a
  genuine answer and false for a cancellation (context cancelled, timeout,
  `Cancelled` answer).
- `Restore(q Question, onAnswer func(Question, Answer))` re-registers a
  question with no waiting tool call (an **orphan**). It appears in
  `Pending` like any other question. When `Resolve` reaches it, the registry
  calls **only** `onAnswer`, not `Remove`: the owner of the orphan decides what
  happens to the stored entry (section 5).
- `CancelSession(sessionID)` resolves every pending question of a session as
  cancelled. It is used when a session is archived.

With no persister set, the registry behaves exactly as today.

### 3. The server persister

Implemented in `server/`, installed on the registry at boot. Each durable
question is stored in the session's conversation file, in a new field:

```go
// ConversationFile
PendingQuestions []PendingQuestion `json:"pending_questions,omitempty"`

type PendingQuestion struct {
    Question  askuser.Question `json:"question"`
    TurnID    string           `json:"turn_id"`    // identifies the interrupted turn
    Prompt    string           `json:"prompt"`     // the user request that turn was answering
    Squad     string           `json:"squad"`      // squad answering at ask time
    TurnCount int              `json:"turn_count"` // persisted turns when asked
    AskedAt   time.Time        `json:"asked_at"`
    Answer    *askuser.Answer  `json:"answer,omitempty"` // set once a restored orphan is answered
}
```

`LoadPersistedSessions` currently skips a conversation file with no turns. It
must also load a file that has no turns but has pending questions — the first
turn of a new session can be the one interrupted — using the oldest
`AskedAt` as the session's created/last-used time. Otherwise the session is
not restored and the GC deletes its file.

Removing an entry must **not recreate a deleted conversation file**: when the
file is gone (session deleted), removal is a no-op.

- `Prompt` and `TurnID` come from the session's live turn
  (`LiveTurns.get(sessionID)`). `liveTurn` gains an `id` field, a UUID set in
  `newLiveTurn`. The prompt is the only record of the request, because a turn
  is persisted only when it ends.
- **Injected turns have no live turn** (mailbox delivery, scheduled routines,
  spawned tasks, background notifications). For a question asked there,
  `Prompt` is empty and `TurnID` is the question's own ID, so the question
  resumes on its own. The resume prompt then omits the "Original request"
  block.
- Writes go through the existing conversation lock, like
  `SetConversationGoal`.

`internal/sessions` imports `internal/askuser` directly; `askuser` imports
only stdlib + uuid, so this cannot form a cycle.

`Remove(q, answered=false)` **does nothing when the server's root context is
done.** That is the shutdown case: the question must stay on disk. In every
other case, `Remove` deletes the entry.

### 4. Restore at boot

`LoadPersistedSessions` reads `PendingQuestions` into `SessionMeta`. The
startup loop in `server/main.go`, next to the goal restore, calls
`Registry.Restore` for each entry, marking the question `Resumed` for the UI.
The existing `/api/events` replay then shows it in every browser, with no
change to the replay code.

### 5. Resume on answer

`onAnswer` receives each answered orphan and **records the answer on its
stored entry** (`PendingQuestion.Answer`, persisted), rather than deleting it.
That way a restart between two answers of the same turn loses nothing.

- When **every** entry of a `TurnID` has an answer, one resume turn starts.
  Its entries are removed from disk just before it runs (at most once: a crash
  during the resume turn does not replay it).
- A **dismissed** card (cancelled answer) counts as answered, and the resume
  prompt says the user dismissed that question. If **every** question of the
  turn was dismissed, the entries are removed and nothing starts.
- **At boot**, a turn whose entries are all already answered (the server died
  after the last answer but before the resume ran) starts its resume right
  away instead of being restored as questions.

The resume turn goes through `pushManager.injectTurnRouted`, in a goroutine on
the server root context. It broadcasts `turn_started` first, then emits
`mailbox_push` on completion, so open tabs show a processing state and then
the persisted turn.

**Model prompt** (built by a pure function, `buildResumePrompt`):

```
[Resumed after a server restart] Your previous turn was interrupted while you
were waiting for the user's answer.

Original request:
<prompt>

You asked: <question 1>
The user answered: <answer 1>
(… one pair per question of that turn …)

Continue the task from where you left off. Work done during the interrupted
turn may be lost, so re-check anything you relied on before continuing.
```

**Persisted user text** (what the chat history shows):

- If the interrupted turn was already persisted — the conversation now has more
  turns than `TurnCount`, which happens on a graceful shutdown because the
  streamed text is saved — show only
  `↪ Answer to "<question>": <answer>` (one line per question).
- Otherwise (crash), prefix it with the original request, when there is one.

**Context reseed on every injected turn.** `injectTurnRouted` gains the same
lazy reseed that `handleMessages` has: if the session has persisted turns but
its squad has no in-memory context (`!Manager.HasSessionContext`), rebuild it
from the conversation file before running. This fixes the gap for mailbox,
scheduled and spawned turns too.

### 6. When an entry is removed

| Event | In memory | On disk |
|---|---|---|
| User answers (live turn) | resolved | removed |
| Stop button | cancelled (tool now waits on the run context) | removed |
| Graceful shutdown | cancelled | **kept** (root context done) |
| Crash / `kill -9` | lost | **kept** |
| Session deleted | — | gone with the file |
| Session archived | cancelled (`CancelSession`) | removed |
| Fork / export / import | — | not copied |

### 7. Edge cases

- **Second restart before an answer**: the entry is still on disk and is
  restored again.
- **The session is busy when the answer arrives**: `injectTurnRouted` waits
  on the session run guard, so the resume runs after the current turn.
- **The user types a new message instead of answering**: the card stays; the
  question stays pending. No guessing.
- **A question asked by the Omnis router**: the stored squad is the router, so
  the resume turn routes again. Intended.
- **Hidden sessions** (Settings assistant, session search): same behaviour.
- **The session's squad no longer exists after a config change**:
  `injectTurnRouted` already returns without running when
  `LookupSquad` fails. The entry is removed, and the failure is logged.

## Web UI

- `QuestionToPayload` includes `resumed: true` for a restored question.
- The ask-user wizard shows one info line above such a question: "Asked before
  a server restart — your answer will resume the task." New i18n key
  `app.askwizard.resumed` (en/fr/es/de), then `make i18n`, and bump the
  `?v=` of `app.js`, `i18n.js` and `locales.js` in `web/index.html`.
- No new event type: the resume turn reuses `turn_started` and `mailbox_push`.

## Documentation

- CLAUDE.md: a "Durable agent questions" paragraph in the ask-user section;
  note the `context.Background()` fix; mark the injected-turn reseed gap as
  fixed.
- `internal/features/FEATURES.md`: one user-facing bullet in the in-development
  section.

## Testing

- **`internal/askuser`**:
  - only durable questions reach the persister;
  - `Remove` reports `answered` correctly for an answer, a cancellation and a
    timeout;
  - a restored orphan appears in `Pending`, and `Resolve` calls `onAnswer`;
  - `CancelSession` cancels only that session's questions.
- **`core/tools`**: cancelling the run context releases `AskUserQuestion` and
  returns `Cancelled`. This is a regression test for Stop and fails on the
  current code.
- **`internal/sessions`**:
  - `PendingQuestions` round-trips through the conversation file, and
    `LoadPersistedSessions` reads it;
  - fork and export/import drop it.
- **`server`**:
  - the persister keeps an entry when the root context is done and removes it
    otherwise;
  - the resume starts only once every question of a turn is answered, and a
    cancelled answer starts nothing;
  - `buildResumePrompt` and the persisted text are right, with and without an
    already-persisted partial turn;
  - `injectTurnRouted` reseeds a session with no in-memory context.
- **Live end-to-end**, against a real server and model gateway:
  1. an agent asks a question;
  2. stop the server with SIGTERM, restart it, check the question is back,
     answer, and check the resume turn continues the task with that answer;
  3. repeat with `kill -9`.
