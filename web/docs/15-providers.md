# Providers & Models

The agent is provider-agnostic. A single dispatcher in `core/llm/` routes
calls to one of four backends:

| Provider         | Use for |
|---|---|
| `anthropic`      | Claude (native Anthropic API). |
| `openai`         | OpenAI's first-party API. |
| `gemini`         | Google AI Studio / Vertex Gemini. |
| `openai_compat`  | Any OpenAI-compatible endpoint (vLLM, Together, Groq, …). The default. |

## Selecting a model

Models are declared in `agent.json` under `models[]` and referenced by name
from each `agent[].model_ref`. A model entry carries:

- `provider` — one of the four above.
- `model` — the provider-specific model ID (e.g. `claude-opus-4-7`).
- `base_url` / `api_key` — endpoint and credential. Resolved as env-var names
  first (see Configuration).
- `temperature`, `max_tokens`, optional `thinking` budget, etc.

Override at runtime with `YOKE_PROVIDER`, `YOKE_MODEL`, `YOKE_BASE_URL`,
`YOKE_API_KEY`. The provider-specific env vars `ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, `GOOGLE_API_KEY` are also recognised.

## Per-agent models

Different sub-agents can use different models. A common configuration:

- **leader** — top-tier reasoning model (Opus, GPT-5, Gemini 2.x Pro).
- **investigator / summariser** — smaller, faster model for evidence
  gathering and text condensation.
- **curator** — runs offline at session end; usually a mid-tier model.

The **Models** sub-tab of the Agents panel exposes every field of the model
entries with inline validation.

## Prompt caching

When the provider supports prompt caching (Anthropic, OpenAI's beta endpoint),
the agent automatically marks the long-lived prefix (system prompt + skill
bodies + tool catalogue) as cacheable. The cache-stats plugin reports the hit
rate in the context popup; a healthy run should show >80% hit rates after the
first turn.

## Web UI provider-model picker

The **Settings → Agents → Models** form lists known model IDs per provider so
you can pick from a dropdown instead of typing. The list is fetched from
`/api/provider-models` on the server, which caches per-provider catalogues for
a few minutes.
