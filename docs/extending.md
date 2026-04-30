# Extending the harness with code

Most specialisation is configuration (see [specialising.md](specialising.md)).
You only need Go when you want a **brand-new tool**, **plugin** or
**sub-agent** that doesn't yet exist.

## Add a new tool

A tool is a Go function adapted to ADK's `tool.Tool` interface. The
simplest path is to mirror an existing one — `core/tools/grep.go` is a
small, complete reference.

Skeleton:

```go
package mytool

import (
    "context"

    "google.golang.org/adk/tool"
    "google.golang.org/genai"
)

type Args struct {
    Pattern string `json:"pattern"`
}

func New() tool.Tool {
    return tool.NewFuncTool(
        "my_tool",
        "One-sentence description visible to the model.",
        &genai.Schema{
            Type: genai.TypeObject,
            Properties: map[string]*genai.Schema{
                "pattern": {Type: genai.TypeString},
            },
            Required: []string{"pattern"},
        },
        func(ctx context.Context, a Args) (any, error) {
            // do the work
            return map[string]any{"matches": []string{}}, nil
        },
    )
}
```

Then register it in the root `main.go`:

```go
leadTools = append(leadTools, mytool.New())
```

If the tool is mutating, **also add a `permissions.yaml` rule** so the
gating plugin can prompt the user.

## Add a new plugin

Plugins observe and mutate the agent loop via the `plugin.Plugin`
interface. Look at `core/permissions/permissions.go` for a complete
example that intercepts `BeforeTool`.

Skeleton:

```go
func MyPlugin(name string) (*plugin.Plugin, error) {
    return plugin.New(plugin.Config{
        Name: name,
        BeforeTool: func(ctx context.Context, in *plugin.BeforeToolInput) (*plugin.BeforeToolOutput, error) {
            // inspect / mutate / short-circuit
            return nil, nil
        },
    })
}
```

Wire it into the root `main.go`:

```go
if p, err := mypkg.MyPlugin("mine"); err == nil {
    plugins = append(plugins, p)
}
```

## Add a new sub-agent

Sub-agents are constructed with `agentkit.New` like the lead is. Keep
their **instruction domain-neutral** — describe their *role* (e.g.
"validate inputs", "estimate cost") not their *domain*. Domain belongs
in skills.

```go
critic, err := agentkit.New(agentkit.AgentConfig{
    Name:        "critic",
    Description: "Pokes holes in a proposed plan.",
    Model:       llm,
    Instruction: "You are an adversarial reviewer. For each step in the proposed plan, list the most likely failure mode in one sentence. Cite evidence when possible.",
})
```

Expose it to the lead as a tool:

```go
leadTools = append(leadTools, agenttool.New(critic, &agenttool.Config{}))
```

Add it to the multi-loader so the launcher can address it:

```go
loader, err := adkagent.NewMultiLoader(lead, investigator, summariser, critic)
```

## Add a new LLM provider

If the provider exposes an OpenAI-compatible API, **don't** add code —
just point `OPENAI_BASE_URL` at it. Otherwise:

1. Implement `model.LLM` (`Name() string` + `GenerateContent(ctx, *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error]`).
2. Register it in [`core/llm/llm.go`](../core/llm/llm.go)'s `New()` switch.
3. Add a default model id to `defaultModel`.
4. Document its env vars at the top of `llm.go`.

Look at [`core/llm/anthropic.go`](../core/llm/anthropic.go) and
[`core/llm/openai.go`](../core/llm/openai.go) — both are ~200 lines
each, no third-party SDK.

## Conventions

- **Stay domain-neutral** in the harness. Domains live in `skills/`,
  `config/mcp_config.yaml` and `config/permissions.yaml`.
- **One package per component.** Mirror the existing `core/` and
  `internal/` layout.
- **Expose tools through `tool.Tool`**, never as bare Go functions
  called by the launcher.
- **Use `agentkit.New`** so every agent inherits the universal system
  prompt.
- **Pair every mutating tool with a permission rule.**
- **Build & vet** before committing: `go build ./... && go vet ./...`.
