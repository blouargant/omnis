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
| `softskills/`                                 | Curator-distilled procedures. |

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
`curator`); the Web UI displays them under a **Built-in Agents**
section, separate from user-added **Custom Agents**.

The registry directory follows the same three-layer lookup described below:
`.agents/registry/agents`, `$HOME/.yoke/registry/agents`, then
`/etc/yoke/registry/agents`.

## Read root (config search chain)

Config files are resolved through a **three-layer chain**, high → low
precedence. **Whichever layer has a given file wins for that whole file** —
this is a file-level override, not a deep merge.

1. `.agents/` — project-local directory (CWD-relative, highest priority).
2. `$HOME/.yoke/` — per-user state root.
3. `/etc/yoke/registry/` — system-wide install (lowest priority).

Override the chain wholesale with `YOKE_CONFIG_DIRS` (colon-separated). Use
`YOKE_CONFIG_PATH` to bypass the chain entirely for `agents.json`.

Skills follow the same three-layer lookup against `registry/skills/` subdirectories.

## Write root (state)

Everything mutable lands under `$HOME/.yoke/` (override with `YOKE_HOME`).
Config files written by the Web UI editor also land here — a first edit on a
lower-precedence file forks a per-user override that subsequent reads pick up:

```
$HOME/.yoke/
├── agents.json          # editor writes — user overrides of agents/models/squads
├── permissions.json     # editor writes — user permission overrides
├── mcp_config.json      # editor writes — user MCP server overrides
├── logs/                # agent_tasks_*, agent_todo_*, agent_memory_*,
│   │                    #   agent_statelog_*, agent_events_*,
│   │                    #   conversation_*.json (turns + title + squad + harvested)
│   └── uploads/         # web UI file uploads (per-session)
├── mailboxes/           # JSONL inter-agent mailboxes
├── softskills/          # curator-distilled procedures (read AND write)
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
