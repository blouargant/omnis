# LLM providers

The harness is provider-agnostic. Provider selection happens at startup
inside [`core/llm`](../core/llm/llm.go) based on environment variables.
There is no provider-specific SDK in `go.mod` for Anthropic or OpenAI:
both adapters speak HTTP + SSE directly.

The root binary can also load provider/model settings from
[`agents.json`](configuration.md), via a reusable `models`
catalog and per-agent `model_ref` selection.

## Selection

| Variable           | Default   | Meaning                                                              |
|--------------------|-----------|----------------------------------------------------------------------|
| `YOKE_PROVIDER` | `openai_compat`  | One of `gemini`, `anthropic`, `openai`, `openai_compat`              |
| `YOKE_MODEL`    | per below | Provider-specific model id; overrides the default                    |
| `YOKE_BASE_URL` | provider/env specific | Override API base URL used by the selected provider             |
| `YOKE_API_KEY`  | provider/env specific | Override API key used by the selected provider                  |

CLI global overrides:

- `--provider <name>`
- `--model <id>`
- `--base-url <url>`
- `--api-key <value>`

Precedence is: CLI > env > `agents.json` > built-in defaults.

Per-provider defaults:

| Provider        | Default model        |
|-----------------|----------------------|
| `gemini`        | `gemini-2.5-flash`   |
| `anthropic`     | `claude-sonnet-4-5`  |
| `openai`        | `gpt-4o-mini`        |
| `openai_compat` | `gpt-4o-mini`        |

## Auth

| Provider        | Required env                                               |
|-----------------|------------------------------------------------------------|
| `gemini`        | `GOOGLE_API_KEY` *or* `GEMINI_API_KEY`                     |
| `anthropic`     | `ANTHROPIC_API_KEY`                                        |
| `openai`        | `OPENAI_API_KEY`                                           |
| `openai_compat` | `OPENAI_BASE_URL` (required); `OPENAI_API_KEY` if needed   |

## Examples

### OpenAI-compatible (default)

```bash
export OPENAI_BASE_URL=http://localhost:11434/v1
go run . console
```

### Gemini

```bash
export GOOGLE_API_KEY=AIza…
go run . console
```

### Anthropic

```bash
export YOKE_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-…
export YOKE_MODEL=claude-opus-4-5    # optional
```

### OpenAI

```bash
export YOKE_PROVIDER=openai
export OPENAI_API_KEY=sk-…
export YOKE_MODEL=gpt-4o
```

### OpenAI-compatible — local Ollama

```bash
export YOKE_PROVIDER=openai_compat
export OPENAI_BASE_URL=http://localhost:11434/v1
export YOKE_MODEL=llama3.1:70b
```

### OpenAI-compatible — Groq

```bash
export YOKE_PROVIDER=openai_compat
export OPENAI_BASE_URL=https://api.groq.com/openai/v1
export OPENAI_API_KEY=gsk_…
export YOKE_MODEL=llama-3.3-70b-versatile
```

### OpenAI-compatible — vLLM / Together / Mistral / DeepInfra

Same pattern: set `OPENAI_BASE_URL` to the provider's `/v1` endpoint and
`OPENAI_API_KEY` to your token.

## What the adapters translate

ADK uses Google `genai` types end-to-end (`*genai.Content`, `*genai.Part`
with `Text`, `FunctionCall`, `FunctionResponse`, `*genai.GenerateContentConfig`
with `SystemInstruction`). The non-Gemini adapters in
[`core/llm/convert.go`](../core/llm/convert.go),
[`core/llm/anthropic.go`](../core/llm/anthropic.go) and
[`core/llm/openai.go`](../core/llm/openai.go) translate:

| genai concept                  | Anthropic                            | OpenAI                              |
|--------------------------------|--------------------------------------|-------------------------------------|
| `SystemInstruction` text       | `system` top-level field             | `messages[0]` with role `system`    |
| `Content` (role: user / model) | `messages[].role: user / assistant`  | `messages[].role: user / assistant` |
| `Part.Text`                    | content block `type: text`           | message `content`                   |
| `Part.FunctionCall`            | content block `type: tool_use`       | `tool_calls[]`                      |
| `Part.FunctionResponse`        | content block `type: tool_result`    | role `tool` message                 |
| `Tools` (function declarations)| `tools[]` with `input_schema`        | `tools[].function`                  |

Both adapters stream tokens via SSE so the agent loop can react
incrementally.

## Adding a new provider

If the new provider exposes an OpenAI-compatible API, just point
`OPENAI_BASE_URL` at it. Otherwise:

1. Implement `model.LLM` (`Name() string` + `GenerateContent(ctx, *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error]`).
2. Register it in `core/llm/llm.go`'s `New()` switch.
3. Add a default model id to `defaultModel`.
4. Document its env vars at the top of `llm.go`.

No callers need to change.
