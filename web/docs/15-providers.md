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

Models live in their own `models.json` file (separate from `agents.json`)
with two top-level sections:

- `providers` — credentials + endpoint for each upstream API. A provider
  picks a `kind` (the four above), a `base_url`, and an `api_key`.
- `models` — model profiles. Each one points at a provider via
  `provider_ref` and inherits its credentials. Optional inline
  `provider` / `base_url` / `api_key` fields still override the inherited
  values when set.

Each agent references a model by its profile name via `model_ref` in
`registry/agents/<name>/agent.json`.

`base_url` and `api_key` are resolved as env-var names first (see
Configuration). Override leader credentials at runtime with
`OMNIS_PROVIDER`, `OMNIS_MODEL`, `OMNIS_BASE_URL`, `OMNIS_API_KEY`. The
provider-specific env vars `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`,
`GOOGLE_API_KEY` are also recognised.

A startup-time check rejects configs that still declare `models` inline
in `agents.json` — move the block to `models.json` (the loader points at
the expected path in the error message).

## Per-agent models

Different sub-agents can use different models. A common configuration:

- **leader** — top-tier reasoning model (Opus, GPT-5, Gemini 2.x Pro).
- **investigator / summariser** — smaller, faster model for evidence
  gathering and text condensation.
- **curator** — runs offline at session end; usually a mid-tier model.

The **Models** section in Settings (right after Agents) exposes every
field with inline validation. It has two sub-tabs — Providers and Models
— so credentials can be defined once and shared across model entries.

## Prompt caching

When the provider supports prompt caching (Anthropic, OpenAI's beta endpoint),
the agent automatically marks the long-lived prefix (system prompt + skill
bodies + tool catalogue) as cacheable. The cache-stats plugin reports the hit
rate in the context popup; a healthy run should show >80% hit rates after the
first turn.

## Web UI provider-model picker

The **Settings → Models → Models** sub-tab lists known model IDs per provider
so you can pick from a dropdown instead of typing. The ⟳ button calls
`/api/providers/models?provider_ref=<name>` on the server, which resolves the
provider's credentials and base URL from `models.json` before contacting the
upstream API — secrets never cross the wire to the browser. If the provider
returns context-length or pricing alongside each model, those fields are
auto-filled on the model entry.
