# omnis-agent

**Omnis** is a multi-agent harness. The same binary becomes a code reviewer, a
Kubernetes triage assistant, or a DBA helper purely by mounting different tools,
skills, and MCP servers — no code changes required to retarget the agent.

This package ships two prebuilt Go binaries inside a platform-specific wheel:

| Command | What it is |
|---|---|
| `omnis` | CLI / TUI / REPL |
| `omnis-server` | HTTP API + Web UI server |

## Install

```bash
pip install omnis-agent          # into any Python 3.8+ environment
# or, isolated:
pipx install omnis-agent
```

Prebuilt wheels are published for Linux (x86_64, aarch64), macOS (Intel, Apple
Silicon), and Windows (x64, arm64). The wheel contains the native binary plus the
default config, agent/skill registry, and Web UI assets — no Go toolchain needed.

## First run

On first launch, `omnis` copies the bundled default **config and registry** into
your per-user config home so they are yours to edit:

```
~/.omnis/
├── agents.json  models.json  mcp_config.json  permissions.json
├── preferences.json  remote_registries.json  a2a_config.json  server.yaml
├── filters/                 # bash output filter patterns
└── registry/
    ├── agents/              # built-in agent definitions
    └── skills/              # bundled skill playbooks
```

Existing files are never overwritten, so your edits survive upgrades. To refresh
the pristine defaults at any time:

```bash
omnis-seed --force            # re-copy bundled defaults into ~/.omnis
omnis-seed --home /path/to/x  # seed a different config home
```

The Web UI assets stay inside the installed wheel (read-only) and the launcher
points the binaries at them automatically.

## Quick start

```bash
export ANTHROPIC_API_KEY=sk-...     # or OPENAI_API_KEY / GOOGLE_API_KEY

omnis "explain this repo"            # one-shot CLI
omnis                                # REPL (interactive TTY)
omnis tui                            # full-screen TUI

OMNIS_SERVER_TOKEN=secret omnis-server   # Web UI + API on http://localhost:8080
```

## Configuration layering

omnis resolves configuration through a search chain, highest precedence first:

1. `./.agents/` — project-local (per checkout)
2. `~/.omnis/` — per-user (what this package seeds; override with `$OMNIS_HOME`)
3. the bundled defaults inside the wheel (system layer)

So a value you set in `~/.omnis/models.json` overrides the shipped default, and a
`./.agents/models.json` in a project overrides both.

Useful environment variables:

| Variable | Purpose |
|---|---|
| `OMNIS_HOME` | Per-user state + config root (default `~/.omnis`). |
| `OMNIS_WEB_DIR` | Static Web UI directory (default: the bundled assets). |
| `OMNIS_SYSTEM_CONFIG_DIR` | System config layer (default: the bundled defaults). |
| `OMNIS_SERVER_TOKEN` | Bearer token required by `omnis-server`'s API. |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `GOOGLE_API_KEY` | Provider keys. |

Project home and full documentation: <https://github.com/blouargant/omnis>
