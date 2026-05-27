---
name: wrap-session
description: One-shot wrap-up question for explicit user feedback before the post-session reflector runs. Use only when work is complete on an interactive surface (TUI / Web UI).
---

# Wrap Session

## Context

The post-session reflector tags loaded soft-skills as helpful / harmful / neutral by reading implicit signals (StateLog open issues, the tone of the final user message, tool errors). Implicit signals are noisy. A single closing question, asked at the right moment, gives the reflector a far more reliable verdict — but only when there is a human present to answer.

Load this skill ONLY when:
- the runtime is interactive (TUI or Web UI),
- all user-stated tasks for the turn are complete or blocked on input the user must supply,
- you have not already loaded this skill in this session.

NEVER load this skill on:
- CLI one-shot invocations (no human is sitting at the terminal),
- A2A inbound calls (the caller is another agent),
- scheduled / cron-triggered runs (no human is reachable),
- any turn where you still have outstanding work to do for the user.

## Steps

1. Call `AskUserQuestion` with `kind="text"` and the prompt: "Anything off, or are we good to wrap?". Set `timeout_secs` to 120 (two minutes is plenty; the user may be checking a result).

2. If the user supplies an answer with at least one non-whitespace character, immediately call `record_session_feedback` with:
   - `question`: the exact question text you asked in step 1.
   - `answer`: the answer string, trimmed.
   The tool persists the record to `logs/agent_feedback_<session-suffix>.json`. The post-session reflector reads it as the dominant verdict signal.

3. If the user lets the question time out, declines, or returns an empty answer, do NOT call `record_session_feedback`. The reflector will fall back on implicit signals — which is the pre-wrap-up behaviour.

4. Acknowledge the user's answer in one short sentence ("Noted." / "Thanks, I'll flag that.") and end the turn. Do not start new work based on the wrap-up answer in the same turn; the user can open a new turn if they want follow-up.

## Constraints

- Fire AT MOST ONCE per session. The soft-skill loader records per-session loads, so re-loading this skill in the same session is itself a violation.
- Never ask a multiple-choice variant; the free-text answer is what the reflector parses.
- Never substitute `record_session_feedback` with a generic file write — the tool name is the contract with the reflector.
- If `record_session_feedback` returns an error, do NOT retry the same call; report the error and continue.

## Validation

- After step 2, `logs/agent_feedback_<session-suffix>.json` exists and contains `{question, answer, timestamp}`.
- The post-session reflector reports `explicit_feedback:positive` or `explicit_feedback:negative` in its signals.
