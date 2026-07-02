# Slash Commands

The **Settings → Commands** panel lets you define custom slash commands
that expand to prompt templates. Type `/` in the chat composer to see the
full command list.

> Slash commands (`/…`) expand into prompts **for the agent**. To run a shell
> command **directly on the host** instead, prefix the line with `!` — see
> **The Composer → Shell commands**.

## Built-in commands

Built-in commands are shipped with the Web UI and cannot be edited or
removed:

| Command | Description |
|---|---|
| `/help` | Show the command list. |
| `/compress` | Compress the current session context. |
| `/create-skill` | Start a new skill from the current conversation. |
| `/update-skill` | Update an existing skill. |
| `/status` | Show session and agent status. |
| `/init` | Analyse the repository and write a starter `AGENT.md`. |
| `/learn` | Queue a soft-skill harvest for this session (reflectors run first; curator follows). |
| `/learn-now` | Run the curator immediately on this session. The LLM reflector is skipped on this path — only the curator-gate's threshold + the existing audit/statelog are used. |
| `/btw <question>` | Ask a quick **side question** about the session. The answer uses the conversation as context but is **not** saved to the transcript or the model's memory — a read-only aside (one off-stream LLM call). |
| `/recap` | Summarise the current session so far (a few Markdown bullet points). Read-only aside, not saved. |
| `/fork` | Branch a brand-new session that inherits this conversation's **full** context, then switch to it. The original session is left untouched, so you can explore a different direction without losing it (mirrors Claude Code's `/fork`). |
| `/spawn <name> [@squad]: <task>` | Start a **fresh** session (empty context — *not* a copy of this conversation) rooted in this session's working directory. Grammar: everything **before the `:`** is the header — an `@squad` token in it names the squad, the remaining words are the (multi-word) **session name**; everything **after the `:`** is an optional initial **task** the new session starts working on **in the background**. When that task finishes, its **result is delivered back into this session** (the one you launched `/spawn` from) as a message your leader reacts to — so you get the findings in your main thread without switching sessions. No `:` → an idle session (no task, nothing delivered back). **Omit `@squad`** to let the Omnis router pick the best-suited squad. E.g. `/spawn parser tests @coding: add unit tests for the tokenizer`, or `/spawn market research: size the EV market` (router picks). A squad **leader** can do the same on its own via the `spawn_session` tool (disable it by removing the `spawn` tool from the leader in Settings → Agent). |
| `/plan [task]` | Send a templated prompt asking the agent to research and propose a step-by-step plan **without making changes**. This is prompt-level guidance, not a permission-enforced read-only mode. |
| `/export [filename]` | Download the conversation as a Markdown file. Defaults to `omnis-<session>.md`. |
| `/usage` (alias `/cost`) | Show this session's token usage (context-window fill, cumulative prompt/output) and estimated cost, with a per-agent breakdown. |
| `/rollback [N\|all]` | Undo recent **settings** changes (made via the chat settings tools or the Settings panel), restoring the previous config and hot-reloading. No argument undoes the most recent change; `N` undoes the last *N*; `all` reverts everything to the initial state. Reverts config-file edits only (not files downloaded by a registry install) and cannot itself be redone. The Helper also does this from natural language ("revert that", "go back to how it was"). |
| `/loop <spec> <prompt>` | Re-run `<prompt>` in **this** session on a timer. `/loop stop` cancels it; bare `/loop` (or `/loop list`) lists the session's loops. Loops are in-memory — they stop when the session is archived/deleted or the server restarts. |
| `/schedule <spec> <prompt>` | Create a **durable** routine that fires `<prompt>` on a cron/interval. Under the server each run spins up a **fresh, auto-archived session** (visible read-only in the sidebar). `/schedule list`, `/schedule remove <id>`, and `/schedule run <id>` manage routines. Routines survive restarts (stored in `schedules.json`). |
| `/goal <condition>` | Set a **completion condition** and keep working across turns until it's met. After each turn a small fast evaluator model judges the condition against the transcript; if not met, the agent takes another turn on its own. Bare `/goal` shows status (condition, turns, latest reason); `/goal clear` (aliases `stop`/`off`/`reset`/`none`/`cancel`) stops it. A "◎ Goal" chip above the composer shows it's active — click it to stop. One goal per session; it resumes after a restart. |

### Goals (`/goal`)

`/goal` keeps the agent working toward a **verifiable end state** without you
prompting each step — the same idea as [`/loop`](#built-in-commands) but the
*stop* condition is decided by a model, not a timer:

- Write a condition the agent's own output can demonstrate (the evaluator only
  reads the transcript — it can't run commands or open files). "All tests in
  `internal/foo` pass" works because the agent runs the tests and the result
  lands in the conversation.
- After each turn the **evaluator model** (the `eval_model_ref` model in
  `models.json`, falling back to the session's leader model — point it at a cheap
  fast model) returns met / not-met plus a one-line reason. "Not met" feeds the
  reason back as guidance and starts another turn.
- A hard turn cap (`OMNIS_GOAL_MAX_TURNS`, default 30) bounds runaway loops; you
  can also bound it in the condition itself ("…or stop after 20 turns").
- A plain message while a goal is active **steers** the running work (it doesn't
  cancel the goal); use `/goal clear` (or the Stop button) to stop entirely.

### Schedule specs

`/loop` and `/schedule` accept the same `<spec>`, which is the **first token** of
the argument (quote it if it contains spaces):

| Spec | Meaning |
|---|---|
| `30m`, `2h`, `every 2h` | Recurring interval (30s minimum). |
| `in 90m` | One-shot, 90 minutes from now. |
| `at 09:00`, `at 2026-06-29T09:00` | One-shot at a clock time (next occurrence) or an absolute time. |
| `"0 9 * * 1-5"` | A 5-field cron expression (quote it — it has spaces). |

Manage every loop and routine visually under **Settings → Automation**.

## User commands

User commands are stored in `$OMNIS_HOME/user_commands.json` and
managed via the CRUD table in the panel.

Each command has four fields:

| Field | Required | Description |
|---|---|---|
| **Name** | yes | Command name without the leading `/`. 1–40 chars: letters, digits, `-`, `_`. Case-insensitive. Cannot shadow a built-in name. |
| **Description** | no | Short label shown in the `/` suggestion popup. |
| **Args** | no | Human-readable hint for expected arguments (shown as placeholder text). |
| **Prompt** | yes | Template body sent to the agent when the command is invoked. |

## Prompt templates

Positional substitutions in the prompt body:

| Placeholder | Expands to |
|---|---|
| `$1`, `$2`, … | The first, second, … argument typed after the command. |
| `$*` | All arguments joined as a single string. |

**Example** — a command `/review` that asks for a review of a named file:

- Name: `review`
- Args: `<file>`
- Prompt: `Review $1 for correctness, style, and potential bugs. Summarise findings in a brief list.`

Invoking `/review main.go` sends:

```
Review main.go for correctness, style, and potential bugs. Summarise findings in a brief list.
```

## Persistence

User commands are written to `$OMNIS_HOME/user_commands.json` on
save and read back on every page load. They are not part of the
hot-reload flow — changes take effect immediately without a reload.
