# Environment Variables

A reference of every environment variable Yoke recognises. Set them in your
shell before launching `make run-server` (or pass through a `.env` file).

## Provider / model

| Variable               | Purpose |
|---|---|
| `YOKE_PROVIDER`        | `anthropic` / `openai` / `gemini` / `openai_compat` (default). |
| `YOKE_MODEL`           | Provider-specific model ID. |
| `YOKE_BASE_URL`        | API endpoint. |
| `YOKE_API_KEY`         | Provider API key. |
| `ANTHROPIC_API_KEY`    | Claude key (alternative to `YOKE_API_KEY`). |
| `OPENAI_API_KEY`       | OpenAI key. |
| `GOOGLE_API_KEY`       | Gemini key. |

## Curator

| Variable                          | Purpose |
|---|---|
| `YOKE_CURATOR_ENABLED`            | `true` / `false`. |
| `YOKE_CURATOR_IDLE_TIMEOUT`       | Duration (e.g. `30m`) before an idle Web UI session is auto-harvested. `0` disables. |
| `YOKE_CURATOR_MIN_TURNS`          | Minimum model-response count before non-forced curation runs. Default `3`. |
| `YOKE_CURATOR_MIN_SUB_AGENT_CALLS`| Minimum sub-agent invocations required when no explicit decision is recorded. Default `2`. |

## Server

| Variable                  | Purpose |
|---|---|
| `YOKE_SERVER_TOKEN`       | Bearer token required to start the HTTP server. |
| `YOKE_SERVER_ADDR`        | Listen address. Default `:8080`. |
| `YOKE_SERVER_GC_INTERVAL` | Period between orphan-file sweeps in `$YOKE_HOME/logs`. Default `1h`. `0` disables. |

## Filesystem

| Variable                    | Purpose |
|---|---|
| `YOKE_HOME`                 | Per-user state root. Default `$HOME/.yoke`. |
| `YOKE_CONFIG_DIRS`          | Colon-separated config search chain, high → low. Replaces the default (`.agents:$HOME/.yoke:/etc/yoke/registry`). |
| `YOKE_CONFIG_PATH`          | Explicit `agents.json` path; bypasses the chain. |
| `YOKE_SKILLS_REGISTRY_DIR`  | Where the Web UI installs imported skills. Default `$YOKE_HOME/registry/skills`. |
| `YOKE_AGENTS_REGISTRY_DIR`  | Where the Web UI installs imported agents. Default `$YOKE_HOME/registry/agents`. |
| `YOKE_WEB_DIR`              | Directory containing the static Web UI files. Default `web` (relative to CWD). |

## Debug

| Variable      | Purpose |
|---|---|
| `YOKE_DEBUG`  | Log full conversation/event payloads + per-stream SSE timing line. |

## Web UI debug

Two flags are read on the browser side rather than the server:

- URL query `?debug=1` — enable the in-page debug overlay for the current
  tab.
- `localStorage.agent_toolkit_debug = "1"` — persist the overlay across
  reloads.

The overlay shows live per-turn metrics: client-side TTFB / chunks/s / render
cost, and the matching server-side counters emitted via the `debug_timing`
SSE event.
