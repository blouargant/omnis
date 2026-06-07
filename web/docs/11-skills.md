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

A skill ships its permission rules in a **`permissions.json`** file next to its
`SKILL.md` (same `permissions.{allow,ask,deny}` shape as the global file).

## Tool dependencies (auto-install)

A skill that drives an external command-line tool can declare it as a
**dependency**, and yoke makes sure it is installed before the skill runs —
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
liteparse`"). Approve it and yoke runs the install (through the same Bash safety
floor as any command), then continues. The shipped `liteparse` skill works this
way, with the `pdf` skill (`pdftotext`) declared as its fallback.

**If you decline** — or the install fails — yoke does not pretend the tool ran:
it tells the leader the dependency is unavailable so it uses the skill's
documented fallback (or reports that the tool is required). Nothing is installed
without your approval.

> The same `requires` mechanism is available for **MCP servers** — see
> [MCP Servers](12-mcp.md).

## Skills vs. soft-skills

Both are markdown playbooks. The difference is provenance:

- **Skills** are **authored**: a human writes the body, commits it.
- **Soft-skills** are **distilled**: the curator agent extracts procedures
  from past session transcripts and writes them to
  `$YOKE_HOME/softskills/`. The leader loads them via the analogous
  `list_softskills` / `load_softskill` tools.

Soft-skills make the agent better at recurring tasks over time without
human authoring. They are also recalled semantically — see
[Learning & Recall](20-learning-and-recall.md) for the StateLog, the
cross-session precedents index, and the embedder that powers recall.

## Post-session reflection

The curator no longer judges sessions alone. At every session end yoke
runs a two-stage reflection pipeline that informs the curator's
decisions:

1. **Heuristic reflector** (always on, no LLM) scans the StateLog
   (open issues, decisions, tools), the last few user messages, tool
   errors, and any explicit wrap-up feedback to produce a verdict
   (`positive` / `negative` / `ambiguous` / `unknown`) and one
   `helpful` / `harmful` / `neutral` tag per loaded soft-skill.
2. **LLM reflector** (the `reflector` agent — optional) refines the
   heuristic with reasons and extracts a single `key_insight` worth
   distilling. The LLM wins on overlap; the heuristic fills the gaps.

Tag counts live in `$YOKE_HOME/softskills/_stats.json` (keyed by
`<agent>/<name>` for sub-agent skills, bare `<name>` for leader
skills). The curator consults the counts plus the reflector's reasons
to apply concrete gating rules:

| Action | When |
|---|---|
| **create** | `success == positive` AND `key_insight` is non-empty AND no near-duplicate exists |
| **update** | The `key_insight` cleanly extends an existing skill |
| **delete** | `(stats.harmful >= 3 && stats.harmful > stats.helpful)` OR the reflector tagged the skill harmful with a reason mentioning "wrong assumptions" or "superseded" |
| **skip** | Default — none of the above is satisfied |

### Per-sub-agent micro-reflection

The same pipeline runs at `EventRunEnd` (every user turn, not just
session end) for the sub-agents that the leader called during the
turn. For each invocation:

- If the leader retried the sub-agent later in the same turn → the
  first call's loaded skills are tagged `harmful`.
- If the sub-agent's reply started with `Error:` or was effectively
  empty → tagged `harmful`.
- If the leader's text cited the sub-agent approvingly ("per
  investigator", "investigator reported …") → tagged `helpful`.
- Otherwise → `neutral`.

This is a lexical scan; over-counting from false-positive citations is
the trade-off vs. running a dedicated micro-classifier per turn.

### Explicit wrap-up feedback

A built-in `wrap-session` soft-skill ships in the default
`softskills/` library. On interactive surfaces (TUI / Web UI) the
leader loads it once per session when the user's work is complete; it
asks "Anything off, or are we good to wrap?" and persists the answer
via the `record_session_feedback` tool to
`logs/agent_feedback_<session-suffix>.json`. Both reflectors treat the
answer as the dominant verdict signal.

Delete `softskills/wrap-session/` (or the directory under
`$YOKE_HOME/softskills/wrap-session/` if you forked it) to disable the
wrap-up question globally.
