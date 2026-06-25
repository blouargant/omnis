# Environment Variables

A reference of every environment variable Omnis recognises. Set them in your
shell before launching `make run-server` (or pass through a `.env` file).

## Provider / model

| Variable               | Purpose |
|---|---|
| `OMNIS_PROVIDER`        | `anthropic` / `openai` / `gemini` / `openai_compat` (default). |
| `OMNIS_MODEL`           | Provider-specific model ID. |
| `OMNIS_BASE_URL`        | API endpoint. |
| `OMNIS_API_KEY`         | Provider API key. |
| `ANTHROPIC_API_KEY`    | Claude key (alternative to `OMNIS_API_KEY`). |
| `OPENAI_API_KEY`       | OpenAI key. |
| `GOOGLE_API_KEY`       | Gemini key. |

## Routing

| Variable             | Purpose |
|---|---|
| `OMNIS_ROUTER_SQUAD`  | Overrides the `router_squad` key in `agents.json` — names the **Omnis** router squad that new chats start on. Absent ⇒ defaults to `omnis` (auto-injected if your config doesn't declare it). Set to `"none"` to disable routing, so new chats start directly on the picked squad (or `default`). See [Architecture → Omnis router](10-architecture.md#omnis-router-default-chat-routing). |

## Curator

| Variable                          | Purpose |
|---|---|
| `OMNIS_CURATOR_ENABLED`            | `true` / `false`. |
| `OMNIS_CURATOR_IDLE_TIMEOUT`       | Duration (e.g. `30m`) before an idle Web UI session is auto-harvested. `0` disables. |
| `OMNIS_CURATOR_MIN_TURNS`          | Minimum model-response count before non-forced curation runs. Default `3`. |
| `OMNIS_CURATOR_MIN_SUB_AGENT_CALLS`| Minimum sub-agent invocations required when no explicit decision is recorded. Default `2`. |

## Embedding / semantic recall

These select the internal embedding model that powers all semantic-recall
features (precedents, soft-skill recall, code and registry search — see
[Learning & Recall](20-learning-and-recall.md)). When none resolves, recall is
disabled and every path falls back to glob/grep. The embedder is built once per
process, so changes take effect on a **server restart**, not a hot-reload.

| Variable                | Purpose |
|---|---|
| `OMNIS_EMBED_MODEL_REF`  | Overrides `embed_model_ref` from `models.json` — names the catalogue model used as the internal embedder. |
| `OMNIS_EMBED_PROVIDER`   | Embedder provider. Default: `OMNIS_PROVIDER`, else `openai_compat`. `anthropic` is unsupported — use Voyage/OpenAI via `openai_compat`. |
| `OMNIS_EMBED_MODEL`      | Embedding model id. Default `text-embedding-3-small`. |
| `OMNIS_EMBED_BASE_URL`   | Embeddings endpoint. Default `OMNIS_BASE_URL` / `OPENAI_BASE_URL`. |
| `OMNIS_EMBED_API_KEY`    | Embedder API key. Default `OMNIS_API_KEY` / provider key. |
| `OMNIS_EMBED_DIM`        | Expected embedding dimension. Default `1536`, or learned from the first response. |
| `OMNIS_DOCS_DIRS`        | Colon-separated documentation roots for the Helper's `search_docs` / `list_docs`. Replaces the auto-discovered set (`web/docs`, `/usr/share/omnis/web/docs`, `docs`, `/usr/share/doc/omnis/docs`). |

## Server

| Variable                  | Purpose |
|---|---|
| `OMNIS_SERVER_TOKEN`       | Bearer token required to start the HTTP server. |
| `OMNIS_SERVER_ADDR`        | Listen address. Default `:8080`. |
| `OMNIS_SERVER_GC_INTERVAL` | Period between orphan-file sweeps in `$OMNIS_HOME/logs`. Default `1h`. `0` disables. |

## Filesystem

| Variable                    | Purpose |
|---|---|
| `OMNIS_HOME`                 | Per-user state root. Default `$HOME/.omnis`. |
| `OMNIS_CONFIG_DIRS`          | Colon-separated config search chain, high → low. Replaces the default (`.agents:$HOME/.omnis:/etc/omnis`). |
| `OMNIS_CONFIG_PATH`          | Explicit `agents.json` path; bypasses the chain. |
| `OMNIS_SKILLS_REGISTRY_DIR`  | Where the Web UI installs imported skills. Default `$OMNIS_HOME/registry/skills`. |
| `OMNIS_AGENTS_REGISTRY_DIR`  | Where the Web UI installs imported agents. Default `$OMNIS_HOME/registry/agents`. |
| `OMNIS_WEB_DIR`              | Directory containing the static Web UI files. Default `web` (relative to CWD). |

## Debug

| Variable      | Purpose |
|---|---|
| `OMNIS_DEBUG`  | Log full conversation/event payloads + per-stream SSE timing line. |

## Web UI debug

Two flags are read on the browser side rather than the server:

- URL query `?debug=1` — enable the in-page debug overlay for the current
  tab.
- `localStorage.agent_toolkit_debug = "1"` — persist the overlay across
  reloads.

The overlay shows live per-turn metrics: client-side TTFB / chunks/s / render
cost, and the matching server-side counters emitted via the `debug_timing`
SSE event.
