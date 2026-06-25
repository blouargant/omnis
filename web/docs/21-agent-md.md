# Project Memory (`AGENT.md`)

`AGENT.md` is omnis's project-memory file — the equivalent of Claude Code's
`CLAUDE.md`. Any `AGENT.md` visible from a session's working directory is
loaded automatically and prepended to the agent's system instruction on every
turn, so it acts as persistent, always-on guidance rather than something you
have to paste into the chat.

## How it's loaded

At each turn omnis resolves `AGENT.md` against the **session's working
directory** (the same cwd the Folders panel and the `!cd` shell-escape use), so
each session picks up the project it is actually working in — even when several
sessions are rooted in different folders.

Files from every layer are **concatenated**, lowest-precedence first (the most
specific guidance, closest to your cwd, comes last):

1. **System** — `/etc/omnis/AGENT.md`
2. **User (global)** — `$OMNIS_HOME/AGENT.md` (applies to every project)
3. **`.agents/` layer** — `AGENT.md` inside the project-local `.agents/`
   (or `agents/`) config directory
4. **Project** — `AGENT.md` from the repository root down to the working
   directory (so a nested `AGENT.md` adds to, and refines, the root one)

When no `AGENT.md` exists anywhere, nothing is injected and behaviour is
unchanged.

## `/init` — bootstrap a starter file

Run `/init` (web UI, TUI, or CLI) to have the agent inspect the repository and
write a starter `AGENT.md` at the project root, documenting the build/test
commands, architecture, key packages, conventions, and gotchas it finds. Review
and edit the result like any other file. The generated document opens with a
**self-maintenance rule** — a short instruction telling future agents to keep
`AGENT.md` in sync as the project evolves (update it in the same change that
renames a command, adds a component, or changes a convention), so it doesn't
drift out of date.

After writing the file, the leader delegates a **fresh-eyes review** to the
`agentmd_reviewer` sub-agent (a member of the Default squad): it reads the new
`AGENT.md` as a newcomer would, follows its instructions against the real
project, and answers the one question that matters — *"if I follow this
document, can I actually work on this project, or will it mislead me?"* It
reports blockers (claims that are wrong or misleading), should-fixes (inaccurate,
incomplete, or stale content), and nits, each with the offending phrase, the
evidence, and a concrete correction. The leader then applies those
recommendations and, if the review found substantive defects, sends the
corrected document back for another pass until the reviewer reports no blockers.
The reviewer is read-only — it never edits the file; the leader (its author)
makes the corrections. When no reviewer sub-agent is available, the leader runs
the verification pass itself.

## `#` — quick one-line memory

Start a composer line with `#` to append a single note to the project
`AGENT.md` without sending anything to the agent (symmetric with the `!`
shell-escape):

```
#always run `make fmt` before committing
```

The line is appended under a `## Notes` section in the project's `AGENT.md`
(created at the repository root if the file doesn't exist yet). In the CLI this
works in both the REPL and one-shot mode; in the TUI and web UI just type it in
the composer.

## Editing it

`AGENT.md` is a plain Markdown file — edit it directly, double-click it in the
Folders panel to open it in the editor, or keep appending with `#`. Changes are
picked up on the next turn automatically (no reload needed).
