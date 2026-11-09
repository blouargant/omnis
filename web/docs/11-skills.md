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
2. `$OMNIS_HOME/registry/skills/` — per-user; written by the Web UI.
3. `/etc/omnis/registry/skills/` — system-wide install.

The Web UI **Skills** section writes new and edited skills to
`$OMNIS_HOME/registry/skills/` by default (override with
`OMNIS_SKILLS_REGISTRY_DIR`). Commit them to git yourself if you want them
tracked.

## Discovery

At agent startup the leader scans every layer for `SKILL.md` files and exposes
them through two tools:

- `list_skills` — returns the registry (`name`, `description`).
- `load_skill name=…` — injects the body into the working context.

The leader is instructed to call `list_skills` before tackling any unfamiliar
task, then `load_skill` for the best match.

In the web UI, loaded skills are listed in the **⚡ N skills** bar above the
composer rather than in the conversation (see *The Composer → Loaded skills*).

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

A skill ships its permission rules in a **`permissions.json`** file next to its
`SKILL.md` (same `permissions.{allow,ask,deny}` shape as the global file).

## Tool dependencies (auto-install)

A skill that drives an external command-line tool can declare it as a
**dependency**, and omnis makes sure it is installed before the skill runs —
rather than leaving it to the model to remember to check. Dependencies are
listed in a **`requires.json`** file next to `SKILL.md`:

```json
{
  "requires": [
    { "command": "lit", "label": "LiteParse", "install": "pipx install liteparse" }
  ]
}
```

- `command` — the program that must be available (checked on your `PATH`).
- `install` — how to install it. Either a single command, or a per-OS object
  so the right one runs on each platform:
  `{ "linux": "apt-get install -y poppler-utils", "darwin": "brew install poppler" }`.
- `label` — optional friendly name shown in the prompt.

**What you'll see.** When the leader loads a skill whose tool is missing, an
**install prompt** appears in the session ("Install LiteParse? — `pip install
liteparse`"). Approve it and omnis runs the install (through the same Bash safety
floor as any command), then continues. The shipped `liteparse` skill works this
way, with the `pdf` skill (`pdftotext`) declared as its fallback.

**If you decline** — or the install fails — omnis does not pretend the tool ran:
it tells the leader the dependency is unavailable so it uses the skill's
documented fallback (or reports that the tool is required). Nothing is installed
without your approval.

> The same `requires` mechanism is available for **MCP servers** — see
> [MCP Servers](12-mcp.md).

## Skills vs. memory

Skills are **authored** playbooks: a human writes the body and installs it.
What the agent learns from you while you work — corrections, preferences,
decisions — goes into the collection's **memory** instead, saved by the agent
in the turn where it learns it. See
[Memory & Recall](20-learning-and-recall.md).
