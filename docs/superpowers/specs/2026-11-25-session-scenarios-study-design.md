# Scenarios from real sessions — a study (design)

Date: 2026-11-25. Status: proposed.

## Why

Tech watch on Perplexity's "Self-improving skills" (2026-11-18). Two of its
ideas could apply to omnis:

1. test skills and instructions on scenarios built from **real sessions that
   went wrong**, instead of hand-written bench tasks only;
2. mark which sessions went wrong, so those sessions can be found.

Before building either into omnis, measure whether they are worth it. This
document describes that measurement only.

**Question to answer:** do scenarios built from real sessions find defects
that our hand-written tasks do not find?

## Goals

- A number for each step of the funnel: turns scanned → turns flagged → real
  failures (checked by a human) → scenarios → failures that reproduce →
  failures no existing suite covers.
- The precision of each way of flagging a turn (cheap signals, LLM judge).
- A list of the signals that were missing from the session files.
- A yes / no / undecided verdict from a rule fixed in advance (below).

## Non-goals

- No change to omnis. No new field, no marker, no route.
- No automatic rewriting of a skill or an instruction. GEPA already does that
  part; this study only asks whether session scenarios would be good input.
- No continuous loop, no scenario-generation service.

## Anti-goals

- Session content (names, paths, hosts, questions as typed) reaching the
  omnis-benches repository, which is on GitHub.
- Scenarios nobody read being counted as evidence.
- A verdict decided after seeing the numbers.

## What the data allows (measured 2026-11-25, one machine)

- 100 sessions, 203 turns. Tool calls are recorded since 2026-10-27 only:
  **50 turns** have them, **45 on the Knowledge squad**, 4 on System, 1 on a
  plugin squad. 13 sessions loaded a skill (`deep-research` 11 times).
- A recorded turn holds the user text, the reply, every tool call with its
  arguments and result (8 KiB each, 300 calls), the sub-agent's own calls, and
  token usage per agent. The session holds the skills loaded and the turn at
  which each was loaded.
- Not recorded: the files' state, Stop, a rewind (it deletes the turns), the
  model's reasoning, tool calls of injected turns. The `error` field of a tool
  record is never filled; errors are in the result text.
- Cheap signals on those 50 turns flag 9: many calls (9), the same call three
  times (4), a tool error (1), a sub-agent that returned nothing (1).

So the study is, in practice, a study of the **Knowledge squad on web
research**. Its scenarios are research questions judged by a rubric, run
against the live web. That is stated in the report's title.

## Approaches considered

- **A. Offline study, omnis untouched (chosen).** A tool in omnis-benches
  reads the session files, flags turns, a human confirms, confirmed failures
  become squad-bench tasks, they are replayed. Cheap, answers the question,
  and tells what a marker would need to record. Limited to what is recorded
  today.
- **B. Add the failure marker to omnis first, study later.** Better data
  (Stop, rewind, corrections), but product code is written before the idea is
  shown to be useful, and the answer comes weeks later.
- **C. The full Perplexity pipeline** (scenario agent, paired replay wired into
  GEPA). That is the integration, not the evaluation.

Chosen by the user on 2026-11-25: A. Its result decides whether B is worth
doing.

## Design

A new tool, `omnis-benches/session-study/`, Python standard library only, one
entry point `study.py` with one subcommand per step. Everything that contains
session content goes to `session-study/out/`, which is added to `.gitignore`.

### 1. `scan`

Reads `conversation_*.json` under a logs directory given as argument (default
`$OMNIS_HOME/logs`, else `~/.omnis/logs`). Hidden sessions are skipped. Writes
`out/turns.jsonl`, one line per turn that has tool calls: session id, turn
index, squad, skills loaded up to that turn, call counts per agent, tokens,
and the signals found:

| Signal | Rule |
|---|---|
| `tool_error` | a result whose text starts with `Error`/`error`, or has a non-empty `error` key |
| `blocked` | `[BLOCKED BY HOOK]`, a permission denial, an auto-mode block |
| `halted` | a budget halt, a per-agent or per-call cap notice |
| `empty_subagent` | "stopped without writing a result" |
| `repeated_call` | same tool and same arguments 3 times or more |
| `many_calls` | call count above the 90th percentile of that squad's turns |
| `reply_cut` / `empty_reply` | the cut notice, or no reply text |
| `user_pushback` | the next user message opens with a correction (a small word list, fr/en) |

### 2. `judge`

One model call per turn in `turns.jsonl` — all of them, not only the flagged
ones, so that what the cheap signals miss is measured too. Input: the user
text, an outline of the calls (name, arguments and result cut to 300
characters, nested for sub-agents), the reply, the next user message. Output,
JSON: `verdict` (`failed` / `wasteful` / `ok`), `kind` (one short label),
`reason` (one sentence), `calls` (ids that show it). Session text is fenced
and the prompt says it is data.

The judge runs through the gateway of `.env`, like `judge_modelcost.py`. Model
given on the command line; default `premium`.

A research answer's factual errors cannot be seen from the session alone. The
judge is told to report what the transcript shows (errors, loops, an answer
that does not address the question, the user pushing back), not to fact-check.

### 3. `review`

Writes `out/review.md`: one card per candidate (any signal, or a judge verdict
other than `ok`), plus 8 turns drawn at random among the others, mixed in and
not labelled, to estimate what both methods miss. Each card: the question, the
call outline, the reply's first and last 600 characters, the next user
message. Signals and the judge's verdict are **not** on the card, so the human
verdict is independent.

Writes `out/verdicts.json` with one entry per card for the user to fill:
`failure` (`yes` / `no` / `unsure`), `kind`, `note`. About 20 to 25 cards.

### 4. `draft`

For each turn the user marked `yes`, one model call drafts a scenario:

```json
{
  "id": "ss-<n>",
  "squad": "knowledge",
  "kind": "web",
  "prompts": ["<earlier user messages, if the failure needs them>", "<the request>"],
  "rubric": {
    "pass": ["<what a good answer contains or does>"],
    "failure": "<the specific failure seen in the session>"
  },
  "files": {"<name>": "<synthetic content>"}
}
```

`prompts` is squad-bench's existing multi-turn key (one turn per entry; a
single request uses `prompt`). The request is rewritten, not copied: same task, no real name, path, host or
figure from the session. `files` is filled only when the turn depended on
local files; their content is made up. The drafts go to
`out/scenarios-draft.json`.

Then two control scenarios are drafted the same way from turns the user marked
`no`, to check that the replay does not fail everything.

The user and the builder read every draft. `study.py check` lists every token
of the source turn (capitalised words, paths, host names, numbers of 4 digits
or more) that still appears in a draft. Only drafts that pass, and that were
read, are copied to `session-study/tasks-sessions.json`, the one file with
scenario text that is committed. The link from a scenario to its source
session stays in `out/`.

### 5. `replay`

Runs each scenario 5 times with `squad-bench/bench.py` against a server started
by `squad-bench/setup-ab.sh` with `MODEL=tiered` (production tiering, the
shipped config of the omnis checkout). If `bench.py` cannot take a `files`
map or a rubric today, the study writes the files into a temporary directory
and passes it as `cwd`, and judges afterwards from the JSONL records; it does
not change `bench.py`'s existing behaviour.

Each run is then judged against its rubric (same gateway, a model of another
family than the one that answered): `passed` (yes/no) and
`failure_seen` (yes/no — the scenario's specific failure, not any failure).

A failure **reproduces** when `failure_seen` is yes in at least 2 of 5 runs.
A control scenario must pass in at least 4 of 5.

Spending stops at a ceiling given on the command line (default 30 USD, counted
from the records' token usage and the models' prices).

### 6. `report`

Writes `reports/session-scenarios-knowledge-<date>.md`:

- the funnel, with counts;
- precision of each signal and of the judge against the user's verdicts, and
  how many of the 8 random turns were failures nobody flagged;
- one line per scenario: the failure, runs reproduced, cost;
- for each failure that reproduces, whether an existing tasks file exercises
  the same behaviour (checked by hand, the file named);
- the signals that would have been needed and are not recorded;
- the verdict.

The report contains scenario text (already sanitised), never session text.

## Decision rule (fixed now)

- **Go on to B** (failure marker in omnis): 3 or more distinct failures
  reproduce and no existing suite covers them.
- **Stop:** none does. The report is kept, like the agent-lessons one.
- **Undecided:** 1 or 2. The report says so; the user decides, knowing the
  sample was 50 turns of one squad.

Two failures are distinct when fixing one would not fix the other (judged by
hand, argued in the report).

## Known limits

- One user, one machine, four weeks of recorded tool calls, one squad.
- Live web: a scenario's result can change with the web, not only with omnis.
- Stop and rewind, the two clearest signs of an unhappy user, are invisible.
- The same model family may write the scenarios and judge the turns.
- A reproduced failure shows the scenario is a usable test; it does not show
  that fixing it improves real sessions.

## Left to the builder

The judge and draft prompts, the pushback word list, the outline's exact
format, tests (no network: fixtures of fake conversation files).
