# Skills

A **skill** is an authored playbook — a markdown file with YAML front matter —
that the leader can load on demand to specialise its behavior for a task.

## Anatomy

```yaml
---
name: my-skill
description: One-line description shown in list_skills output
---
# Skill content as markdown instructions
```

- The `name` is the identifier passed to `load_skill`.
- The `description` is what the leader sees when it inspects available skills
  with `list_skills` — it must be specific enough for the model to choose
  correctly without reading the body.
- The body is appended to the leader's system prompt when the skill is loaded.

## Where skills live

Skills are stored in `registry/skills/` directories and follow the same
three-layer lookup as config files. The first directory in this list that
contains the skill wins:

1. `.agents/registry/skills/` — project-local (highest priority).
2. `$YOKE_HOME/registry/skills/` — per-user; written by the Web UI.
3. `/etc/yoke/registry/skills/` — system-wide install.

The Web UI **Skills** section writes new and edited skills to
`$YOKE_HOME/registry/skills/` by default (override with
`YOKE_SKILLS_REGISTRY_DIR`). Commit them to git yourself if you want them
tracked.

## Discovery

At agent startup the leader scans every layer for `SKILL.md` files and exposes
them through two tools:

- `list_skills` — returns the registry (`name`, `description`).
- `load_skill name=…` — injects the body into the working context.

The leader is instructed to call `list_skills` before tackling any unfamiliar
task, then `load_skill` for the best match.

## Managing skills from the Web UI

The **Skills** section behaves like a small marketplace:

- **Installed skills** — cards for each skill in the registry, with an inline
  editor for name, description, body, and any skill-contributed permissions.
- **Remote registries** — connect to external skill catalogues; browse and
  install one-click into the local registry.
- **Upload archive** — drop a `.zip` / `.tar.gz` into the panel to install a
  skill bundle.

## Skill-contributed permissions

A skill may declare permission rules it needs (e.g. "allow `bash` for `git
status`"). These rules are merged read-only into the active permissions when
the skill is loaded and unmerged when the session ends. The **Permissions**
panel shows them in a separate, read-only block so they cannot be silently
overwritten.

## Skills vs. soft-skills

Both are markdown playbooks. The difference is provenance:

- **Skills** are **authored**: a human writes the body, commits it.
- **Soft-skills** are **distilled**: the curator agent extracts procedures
  from past session transcripts and writes them to
  `$YOKE_HOME/softskills/`. The leader loads them via the analogous
  `list_softskills` / `load_softskill` tools.

Soft-skills make the agent better at recurring tasks over time without
human authoring.
