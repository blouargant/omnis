# Agent lessons: learning from errors, on its own (design, 2026-11-20)

## Intent

When an agent has to try several times before a tool call works, it should do
better the next time, with nobody telling it. This is what soft-skills were
meant to do. They were removed on 2026-11-09 because they were never used:

- the lesson was written after the session by a curator that had not done the work;
- nothing checked that a lesson helped;
- a lesson came back only if the agent chose to list and load soft-skills.

Collection memory does not cover this: it keeps only what the user said.
Agent lessons keep what the agent observed, and only from errors.

Source of the idea: Harness² (google-research/harness2, arXiv 2610.14224). We
take its lesson shape (cue, lesson, evidence from the trajectory) and its rule
that a hook reaches the model where prose can be skipped. We do not take its
replay of the task with candidate harnesses.

## Decisions made with the user

- **Automatic.** A lesson takes effect with no approval. Asking the user would
  duplicate memory.
- **Errors only.** A lesson is written only from an error the host observed.
  This is also the main guard against learning from what the agent merely read.
- **Approach C** (see Approaches): hints attached to the error, plus the most
  frequent lessons in the agent's prompt, with counters that promote and retire.
- **Both parts are built**, hints first.

## Goals

- An agent that failed then succeeded on a tool call gets, the next time the
  same error appears, a hint saying what worked.
- Errors that keep coming back are prevented: their lesson sits in the agent's
  prompt.
- A lesson that does not help is removed by the host, with no human pass.
- Works for squad roots and sub-agents, on server, CLI and TUI.

## Non-goals

- Learning without an error (a wrong answer nobody flagged, a slow strategy).
  The user's correction goes to memory.
- Replaying a task, or probing variants.
- Editing registry files (instructions, skills). Lessons live apart, so package
  updates of an agent keep flowing.
- Sharing lessons between agents.
- A resolution through a different tool (Edit failed, Write used instead).

## Anti-goals

- A sentence from a page, a file or an error message becoming a standing rule.
- A prompt that grows without limit.
- Any card or question to the user.
- A change in behaviour for an install that never hits an error: no lesson
  means no added text and no model call.

## Approaches considered

- **A. Lessons in the agent's prompt.** Prevents the error the first time, but
  is paid on every model call and a weak model may ignore it.
- **B. Hints attached to the error.** Costs nothing until the error returns and
  cannot be skipped, but the agent makes the error once more before being helped.
- **C. Both, with counters.** Hints for everything; the most frequent in the
  prompt; a lesson that does not help is retired. **Picked by the user**: it is
  the only one that checks a lesson is useful.
- Dropped: Harness² offline (Propose–Probe–Compose on bench tasks, producing a
  registry diff). It needs someone to launch and review it.

## Design

### 1. What counts as an error (my call)

Observed by the host, per tool call:

- a tool result that is an error (`EventToolError`, a result string starting
  with `Error:`, `Error <text>`, `error:` or `error <text>` — not `Errors in…`
  nor `ErrorHandler` —, or a result map with a non-empty `error`);
- a `Bash` command that exited non-zero or timed out (the exit status reaches
  the host through a side channel, `fstools.SetBashExitObserver`; the text the
  model sees is unchanged), except exit status 1 from `grep`, `rg`, `egrep`,
  `fgrep`, `diff`, `cmp`, `test` or `[`, which is their "no match" / "differs"
  answer. A Bash call with no reported status is judged on its text;
- a call blocked by a hook (`[BLOCKED BY HOOK]`).

A call is then one of three things: failed, succeeded, or **neither**. Neither:
a user's deny on a permission card, a permission `deny` rule, an auto-mode
block, a budget or cap notice, and a hook block the user declined or that
`dontAsk` or an unattended run refused. These are decisions, not mistakes to
learn around, and the call never ran, so it proves nothing about its
arguments: it opens no episode, closes none, settles no pending hint and is not
counted as an occurrence.

### 2. The episode

An **episode** is one failure followed by a success, for the same agent, the
same tool, in the same run (ADK invocation id), in the same user-facing session
(`realSessionID`). The success must have **started after the failure
completed** (the real start time of the call, not its completion time): ADK
runs the calls of one model response concurrently, and a sibling that started
earlier did not benefit from the failure. It holds: agent name, tool key, the failed call's arguments
and error text, the number of failed attempts, and the arguments of the first
later call of that tool that succeeded. Without a success there is no episode.

- **Tool key**: the tool name; for `Bash`, `Bash:<first word of the command>`
  after wrapper stripping (`kubectl`, `git`, …).
- **Signature**: tool key + the error text normalised (lower case; quoted
  strings, timestamps, paths, UUIDs, hex ids and numbers replaced by
  placeholders, timestamps before hex and digit runs; whitespace collapsed;
  first 200 chars), hashed. A path is absolute or `./`/`../`, relative with a
  directory, or a file name with an extension, after any non-path character
  (`(`, `=`, `:`…), so the same mistake on two files gives one signature. Lessons are keyed by (agent,
  signature); the lesson id is the first 12 hex characters.

No episode is recorded in a turn where memory is read-only (`memoryReadOnly`):
a hidden session, a mailbox or A2A turn, a spawned session's result delivered to
its parent, a background-task notification. The request there does not come
from the user. Scheduled turns and a spawned session's own task do record. A
hint is still delivered in those turns, but no counter, outcome or episode is
recorded.

### 3. Writing a lesson

At the moment an episode closes, it is queued (bounded queue, dropped when
full). A process-wide worker on `Infrastructure` handles it, so no surface code
is involved:

- If a lesson already exists for (agent, signature): stop. The after-tool
  callback is the single owner of the counters (`seen` counts every occurrence,
  recovered or not); the worker never touches them.
- Else one non-streamed model call (`eval_model_ref`, else the leader's model,
  as `/goal` does;
  reasoning off; 20 s). Input, fenced as data: tool, failed arguments, error
  text, working arguments. Output: `NONE` (transient or not the agent's doing),
  or a `cue` (when this applies) and a `lesson` (what to do), one line each,
  ≤ 300 chars.
- Limits: 3 writer calls per run (a `NONE` or failed call counts), 200 lessons
  per agent (a new one then replaces the least seen retired, else hint, lesson;
  none is written when all are prompt lessons).

A failure of the call, or `NONE`, writes nothing. `NONE` is remembered for the
signature for 24 h so the call is not repeated.

### 4. Guard against injected text

Limiting to errors narrows the risk but an error message is still outside text.
So:

- The "what worked" shown to the agent is the **arguments of the call it really
  made and that succeeded** (already through permissions and hooks), capped at
  500 chars. It is not prose taken from a tool output.
- The writer is told the fenced text is data and that a lesson may only say how
  to call this tool. Its output is rejected unless it is one line per field
  within the size limit.
- An episode whose failed or working arguments, or error text, match a secret
  pattern is dropped before any model call: password/passwd/secret/token/
  api key/authorization followed by `=` or `:` (JSON-shaped and escaped quotes
  included), `--password`/`--passwd`/`--secret`/`--token`/`--api-key`/`--apikey`
  followed by a value, an environment-style name ending in `_SECRET`, `_KEY`,
  `_TOKEN` or `_PASSWORD` followed by `=`, `user:password@host` in a URL,
  `bearer <token>`, `sk-…` keys, `ghp_`/`gho_`/`github_pat_` tokens, a PEM
  private key header. Fence
  markers inside the data sent to the writer are neutralised so it cannot close
  its own fence.
- A lesson reaches only the agent that made the error, on that tool. Every call
  it leads to still goes through hooks, permissions and the Bash safety floor.
- Lesson text sits in a tool result note or in the system instruction, never in
  a user turn, so memory's provenance check and the auto-mode judge do not read
  it as the user's words.

Residual risk, accepted: a crafted error message can still bias the one-line
lesson. It cannot grant a permission. The secret patterns are not exhaustive
(a bare positional password or a short flag such as `-p x` is not matched).

### 5. Storage

`internal/lessons` (stdlib only, like `internal/collmem`): one file per lesson
under `<root>/lessons/<agent>/<id>.md`, front matter + body, per-directory lock,
atomic write. `<root>` is `$OMNIS_HOME`, or the session owner's
`users/<login>/` in cookie mode (arguments may hold a user's paths). In cookie
mode, a session whose owner cannot be resolved gets no lesson behaviour at all
for that call (no read, no write, no hint) rather than the shared root.

Fields: `agent`, `tool`, `signature`, `cue`, `lesson`, `failed` (excerpt),
`worked` (the arguments), `state` (`hint` | `prompt` | `retired`), `created`,
`last_seen`, and counters:

- `seen`: every error occurrence with this signature for this agent, whether or
  not it ended in a success;
- `sessions`: distinct sessions among them;
- `hinted`, `recovered`, `repeated`: see Delivery;
- `seen_in_prompt`: occurrences while the lesson was in state `prompt`.

Also kept: the last 8 distinct session ids, the last 5 hint outcomes,
`promoted_seen` (the value of `seen` when the lesson last entered `prompt`) and
`prompt_failed` (set when a prompt lesson was demoted because it did not prevent
the error).

The per-user root is resolved from the session the way the collection plugin
does it (`collectionRoot(sessionID)`), and from the caller on the routes
(`userRoot`). When several calls of one model response run in parallel, a call counts as
"the next call" only if it started after the failure completed (for an
outcome: after the hint was delivered).

### 6. Delivery

**Hint (every non-retired lesson).** An after-tool callback, on squad roots
(runner plugin) and on every sub-agent (composed with the existing shaper via
`chainAfterTool`, since ADK stops at the first callback that returns a result).
When a tool result is an error whose signature has a lesson for this agent, a
`lesson` key is added to the result after shaping, so it is never cut:
the lesson line and the arguments that worked. `hinted` is incremented. The next
call of that tool by that agent in the run that **started after the hint**
decides `recovered` (success) or `repeated` (same signature again); a different
error settles neither. The hint is added after the events callback, so the audit
log does not show it.

**Prompt (lessons in state `prompt`).** A before-model callback appends an
`<agent-lessons>` block to the agent's system instruction: one line per lesson
(`cue` → `lesson`), headed as notes learned from this agent's earlier tool
errors. Budget 1,500 chars; lessons are ordered by `promoted_seen` (the `seen`
count frozen at promotion), then id, and the list stops at the first one that
does not fit (it stays a prompt lesson on disk). Roots (a runner plugin) and
sub-agents; never the router.

Known limit: on a squad root, a `PostToolUse` hook that rewrites a tool result
ends the plugin chain, so that result gets no hint. Sub-agents are not affected.
The block changes only when a lesson enters or leaves the prompt state, so the
prompt cache holds: this is why the order is frozen at promotion and not the
live `seen`, which would reorder the block on every error.

### 7. Promotion and retirement

Constants (the values shipped; the bench in omnis-benches,
`squad-bench/tasks-lessons.json`, is where to revisit them):

- **Promote** `hint` → `prompt`: `seen` ≥ 3 across ≥ 2 sessions. The agent keeps
  walking into it.
- **Retire**: `repeated` ≥ 3 among the last 5 hints. The hint does not lead to a
  working call. A retired lesson keeps its file (so it is not rewritten) and is
  never delivered.
- **Demote** `prompt` → `hint`: `seen_in_prompt` ≥ 3 (the text does not prevent
  it; the hint stays and the lesson is never promoted again), or no occurrence
  for 60 days (the cost is no longer justified; one new occurrence promotes it
  again).

### 8. Switches and visibility

- `lessons: {enabled}` in `agents.json`, `OMNIS_LESSONS=false` (overrides it).
  Default on. Read at call time from the current generation, so a reload that
  turns it off also silences sessions draining on an older one.
- Per agent: `"lessons": false` in `agent.json`.
- Settings → Agent: a read-only **Lessons** list with state and counters, and a
  delete button per lesson, and the per-agent switch. `GET /api/agents/:name/lessons`,
  `DELETE /api/agents/:name/lessons/:id`. No approval step.
- One log line per lesson written, promoted, demoted, retired.

### 9. Measuring

In `omnis-benches` (never in this repo): a squad-bench task set whose tasks
trigger reproducible tool errors, run twice on the same `OMNIS_HOME`. Per task:
tool errors, tool calls, cost, correctness, first run vs second. Success: fewer
errors and calls on the second run at equal correctness, and no regression of
the existing benches with lessons on.

## Build order

1. Episode detection, store, writer, hint on error. Bench.
2. Counters, prompt block, promotion, demotion, retirement. Bench.
3. Settings list and routes, FEATURES.md, CLAUDE.md, user docs.

## Left to the builder

- The exact normalisation of the error text, and where `Bash`'s first word is
  extracted (reuse the permissions wrapper stripping if it is exported).
- The file format details and the id scheme.
- Where the root's callbacks sit relative to the coding-efficiency plugin, as
  long as the hint is added after the shaper. (Built: the hint rides in that
  plugin; the start-time recorder is its own plugin mounted first, ahead of the
  hooks, permission and budget plugins, as it is in the events slot on
  sub-agents.)
- The wording of the writer prompt and of the block header.
