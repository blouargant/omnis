# `.agents/` — bootstrap skills for development sessions

This directory holds [Agent Skills](https://agentskills.io/specification)
for agents (or humans) **working on this repository's source code**.
They are distinct from `skills/` at the repo root, which holds
specialisation skills for the *running* harness agent.

| Directory   | Audience                  | When loaded                          |
|-------------|---------------------------|--------------------------------------|
| `.agents/`  | The development agent     | At the start of a coding session     |
| `skills/`   | The runtime harness agent | When a user prompt matches at runtime|

## Bootstrap order

A new development session should load these in order:

1. [`project-overview`](project-overview/SKILL.md) — what this codebase
   is, the design contract, the directory layout, the doc index.
2. [`build-and-test`](build-and-test/SKILL.md) — environment, build
   command, provider env vars, common failures.
3. [`coding-conventions`](coding-conventions/SKILL.md) — Go conventions,
   architectural rules, anti-patterns.

Then, depending on the task:

- Adding a tool → [`add-tool`](add-tool/SKILL.md)
- Adding a skill → [`add-skill`](add-skill/SKILL.md)
- Adding a provider → [`add-llm-provider`](add-llm-provider/SKILL.md)
- Wiring changes → [`cmd-full-wiring`](cmd-full-wiring/SKILL.md)
- Closing the loop → [`change-checklist`](change-checklist/SKILL.md)

## Format

Every file follows the [Agent Skills specification]: a folder with
`SKILL.md` containing YAML frontmatter (`name` + `description` required)
and Markdown body. Compatible with Claude Code, GitHub Copilot, Cursor,
Gemini CLI, and any other Agent-Skills-aware client.

[Agent Skills specification]: https://agentskills.io/specification
