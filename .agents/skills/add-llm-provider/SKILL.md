---
name: add-llm-provider
description: Add support for a new LLM provider in core/llm. Use when the target provider is NOT OpenAI-compatible (if it is, just set OPENAI_BASE_URL — no code change). Mention triggers - new LLM provider, add Anthropic-style backend, model.LLM, GenerateContent, integrate provider X.
---

# Add a new LLM provider

## First, check the cheap path

If the provider exposes an OpenAI-compatible API (Ollama, Groq, Together,
vLLM, Mistral, DeepInfra, Fireworks, Azure OpenAI, …), **do not write
code**. Just set:

```bash
export OMNIS_PROVIDER=openai_compat
export OPENAI_BASE_URL=https://api.example.com/v1
export OPENAI_API_KEY=sk-…
export OMNIS_MODEL=their-model-id
```

Done. Test with `go run . console`.

If the API is genuinely different, continue.

## Implement `model.LLM`

Add a new file under [`core/llm/`](../../core/llm/) modelled on
[`anthropic.go`](../../core/llm/anthropic.go) or
[`openai.go`](../../core/llm/openai.go). Both are ~200 lines, no SDK.

The interface is:

```go
type LLM interface {
    Name() string
    GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool)
        iter.Seq2[*model.LLMResponse, error]
}
```

Translation primitives are already in
[`core/llm/convert.go`](../../core/llm/convert.go) — reuse
`systemTextFromReq`, content-part conversions, etc. Don't duplicate.

## Register

Edit [`core/llm/llm.go`](../../core/llm/llm.go):

1. Add a default model id to the `defaultModel` map.
2. Add a `case` in the `New()` switch.
3. Document the auth env var(s) at the top of the file.

## Test

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go vet ./... && echo OK

export OMNIS_PROVIDER=newprov
export NEWPROV_API_KEY=…
PATH=$HOME/.local/go/bin:$PATH go run . console
> hello, who are you?
```

The model's `Name()` should appear in `.agent_events.log`'s
`BeforeModel` entries.

## Update the docs

When the provider works:

1. Add a row to the table in [`docs/providers.md`](../../docs/providers.md)
   and the README.
2. If env vars differ from the existing pattern, add a note in
   [`.agents/build-and-test/SKILL.md`](../build-and-test/SKILL.md).

## Don'ts

- ❌ Don't import a third-party SDK. Use `net/http` + SSE.
- ❌ Don't change the `model.LLM` interface — it belongs to ADK.
- ❌ Don't add provider-specific behaviour above the dispatcher; keep
  the rest of the codebase provider-agnostic.
- ❌ Don't drop streaming support; `GenerateContent` must honour the
  `stream` flag.
