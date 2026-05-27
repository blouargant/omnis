# Configuration & Filesystem Layout

Yoke separates **config** (read-only, lookup chain) from **state** (writable,
single root).

## Config files

| File                                          | Purpose |
|---|---|
| `agents.json`                                 | Global settings, model profiles, **squad composition**, list of enabled agent names. |
| `registry/agents/<name>/agent.json`           | Per-agent definition (model_ref, tools, builtin flag, etc.). |
| `registry/agents/<name>/instruction.md`       | Per-agent system instruction (falls back to `registry/agents/default.md`). |
| `permissions.json`                            | Permission rules. |
| `mcp_config.json`                             | MCP server definitions. |
| `a2a_config.json`                             | Remote A2A agent endpoints — each entry becomes an `a2a_<name>` tool on the leader. |
| `filters/`                                    | Bash output filter patterns (token optimization). |
| `registry/skills/<name>/SKILL.md`             | Authored skill playbooks. |
| `softskills/`                                 | Curator-distilled procedures + the `wrap-session` built-in. |
| `softskills/_stats.json`                      | Sidecar: per-skill `loaded_count` + `helpful` / `harmful` / `neutral` counters. Maintained by the reflection pipeline. |
| `logs/agent_feedback_<key>.json`              | Wrap-session sidecar: one `{question, answer, timestamp}` record per session, consumed by the reflectors. |

`agents.json` carries three top-level lists:

- `models` — reusable model profiles referenced by each agent's
  `model_ref`.
- `agents` — list of enabled agent **names** (strings). Each name must
  match a directory under `registry/agents/<name>/`.
- `squads` — named groups `{ leader, members[] }` composed from
  `agents:` and picked per chat session. A squad named `default` is
  always present; the resolver synthesises one when missing. Edit
  squads through the **Squads** sub-tab under Settings → Agents.

Each agent's definition lives in its own directory under
`registry/agents/<name>/`, mirroring the skills layout. An `agent.json`
holds the structured fields (`description`, `model_ref`, `tools`,
`enabled`, `leader`, `builtin`, ...), and an optional `instruction.md`
provides the system prompt. Agents marked `"builtin": true` ship with
yoke (`leader`, `skill_editor`, `registries_crawler`, `summariser`,
`curator`, `reflector`); the Web UI displays them under a **Built-in
Agents** section, separate from user-added **Custom Agents**.

The registry directory follows the same lookup described below:
`.agents/registry/agents` (and `agents/registry/agents` when present),
`$HOME/.yoke/registry/agents`, then `/etc/yoke/registry/agents`.

## Read root (config search chain)

Config files are resolved through a **three-layer chain**, high → low
precedence. **Whichever layer has a given file wins for that whole file** —
this is a file-level override, not a deep merge.

1. `.agents/` (canonical) and/or `agents/` (dotless alias) — project-local
   directories (CWD-relative, highest priority). Both are accepted; when
   both exist, `.agents/` wins and `agents/` is searched immediately after.
2. `$HOME/.yoke/` — per-user state root.
3. `/etc/yoke/` — system-wide install (lowest priority). Agent and skill
   registries live one level deeper, at `/etc/yoke/registry/agents` and
   `/etc/yoke/registry/skills`.

Override the chain wholesale with `YOKE_CONFIG_DIRS` (colon-separated). Use
`YOKE_CONFIG_PATH` to bypass the chain entirely for `agents.json`.

Skills follow the same lookup against `registry/skills/` subdirectories.

## Write root (state)

Runtime state — `logs/`, `uploads/`, `mailboxes/`, `softskills/`, and any
remote-registry-driven installs into `registry/skills` or `registry/agents` —
always lands under `$HOME/.yoke/` (override with `YOKE_HOME`).

For user-edited config (Web UI editor + auto-install helpers), writes are
**layer-aware**: a file that already lives in the project-local layer
(`.agents/` or `agents/`) stays there on save. `agents.json` is additionally
**promoted to the local layer** when any of its referenced agents or skills
only resolves in a local directory, so the post-save config never points at
files that don't exist outside `.agents/`. Files originally in
`/etc/yoke/` still fork into `$HOME/.yoke/` (the system layer is read-only).

```
$HOME/.yoke/
├── agents.json          # editor writes — user overrides of agents/models/squads
├── permissions.json     # editor writes — user permission overrides
├── mcp_config.json      # editor writes — user MCP server overrides
├── logs/                # agent_tasks_*, agent_todo_*, agent_memory_*,
│   │                    #   agent_statelog_*, agent_events_*,
│   │                    #   agent_feedback_*.json (wrap-session answers),
│   │                    #   conversation_*.json (turns + title + squad + harvested)
│   └── uploads/         # web UI file uploads (per-session)
├── mailboxes/           # JSONL inter-agent mailboxes
├── softskills/          # curator-distilled procedures (read AND write)
│   └── _stats.json      # per-skill load/helpful/harmful/neutral counters
└── registry/
    ├── skills/          # web UI installed skills (override: YOKE_SKILLS_REGISTRY_DIR)
    └── agents/          # web UI installed agents (override: YOKE_AGENTS_REGISTRY_DIR)
```

## Precedence

```
defaults → agents.json → ENV → Options (struct/flags)
```

`api_key` and `base_url` values in the config file are resolved as
**environment-variable names first**: if an env var with that name exists and
is non-empty, its value is used; otherwise the literal value is kept. This
keeps secrets out of the config file even when many parts of the system want
to reference the same key.

## Garbage collection

The server periodically sweeps `$YOKE_HOME/logs` and `$YOKE_HOME/logs/uploads`
for orphan files (uploads with no conversation, conversation files whose
session is gone, etc.). Interval is `YOKE_SERVER_GC_INTERVAL` (default `1h`,
`0` disables). A one-shot sweep can be triggered with `POST /api/admin/gc`.
