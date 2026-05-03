---
name: how-the-library-works
description: Explains the soft-skills system to the lead agent — when to consult learned procedures, how they differ from authored skills, and how new ones get created.
category: meta
---

# How the soft-skills library works

## What soft-skills are

Soft-skills are short, actionable procedures the curator agent has distilled
from successful past sessions. Each one captures a sequence of steps that
worked, plus the constraints that made it work.

They are **not** authoritative — they are hints. If a soft-skill conflicts
with an authored skill (`list_skills`), with a tool's documentation, or with
the user's instructions, prefer the authoritative source and mention the
conflict in your reply.

## When to consult them

- At the start of any non-trivial task: call `list_softskills` once to scan
  the available descriptions. The list is cheap (frontmatter only).
- Whenever the user's request matches a soft-skill description, call
  `load_softskill` with `name="<SOFTSKILL_NAME>"` before planning.
- Skip the lookup for trivial requests (single-tool answers, conversational
  replies).

## When new soft-skills are created

The curator runs **after** a session ends (event hook) or on demand
(`agent-toolkit curate` / the `curate_session` tool). It reads the
session's compress audit and state log, identifies a successful multi-step
procedure, checks for redundancy against existing skills and soft-skills,
and only then creates a new entry.

## What lives in a soft-skill

Each `softskills/<name>/SKILL.md` follows the same shape as an authored
skill:

- YAML frontmatter: `name`, `description`, optional `category`.
- Body: **Context** (why it exists), **Steps** (numbered, concrete),
  **Constraints** (what to avoid), **Validation** (how to know it worked).

## What you should NOT do

- Do not modify soft-skills directly. The curator is the only writer.
- Do not treat a soft-skill as authority over an explicit user instruction.
