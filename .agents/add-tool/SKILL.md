---
name: add-tool
description: Add a new ADK tool to the harness in Go. Use when the user asks for a new capability that doesn't exist in core/tools or internal/* and that cannot be supplied by an MCP server or a skill. Mention triggers - add a tool, new tool, expose a function, tool.Tool, FuncTool.
---

# Add a new tool

Before writing code, check the cheaper alternatives:

1. **Can a skill cover it?** If the capability is a *procedure* over
   existing tools, write a `skills/<name>/SKILL.md` instead.
2. **Can an MCP server cover it?** If a community MCP server exists
   (filesystem, github, postgres, kubernetes, …), add it to
   `config/mcp_config.yaml`. No Go required.
3. **Otherwise, write a tool.** Continue below.

## Anatomy

A tool is a Go function exposed via ADK's `tool.Tool`. Reference
example: [`core/tools/grep.go`](../../core/tools/grep.go).

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
        "my_tool",                              // model-visible name (snake_case)
        "One-sentence description for the model.",
        &genai.Schema{
            Type: genai.TypeObject,
            Properties: map[string]*genai.Schema{
                "pattern": {Type: genai.TypeString, Description: "what to look for"},
            },
            Required: []string{"pattern"},
        },
        func(ctx context.Context, a Args) (any, error) {
            // do work; return JSON-serialisable result
            return map[string]any{"matches": []string{}}, nil
        },
    )
}
```

## Where to put it

| Tool kind                        | Package                |
|----------------------------------|------------------------|
| Filesystem / shell / search      | `core/tools/`          |
| Planning / scratch state         | `internal/todo/` `internal/tasks/` |
| Background / async work          | `internal/bg/`         |
| Inter-agent comms                | `internal/teammates/`  |
| Anything brand-new and specific  | new package under `internal/<name>/` |

Naming: tool name is `snake_case`, package and exported `New()` are
idiomatic Go.

## Wire it in

Edit [`cmd/full/main.go`](../../cmd/full/main.go). Append to `leadTools`
**before** the `agentkit.New(...)` call for the lead:

```go
leadTools = append(leadTools, mytool.New())
```

If the tool should also be available to a sub-agent (e.g. the
investigator), pass it via that sub-agent's `Tools:` field too.

## Permissions

If the tool **mutates state** (writes a file, calls a remote API with
side effects, runs a destructive shell command), pair it with a rule in
`config/permissions.yaml`:

```yaml
ask_user:
  - "^my_tool"            # tool-name based pattern (future)
  - "^<bash pattern>"     # if the tool shells out
```

Currently the permission plugin matches the **bash command string**, so
shell-based tools need bash patterns. Pure Go tools that mutate state
should still be documented in `permissions.yaml` under a comment so the
intent is visible.

## Verify

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go vet ./... && echo OK
```

Then run `cmd/full` and ask the model to use the new tool.

## Don'ts

- ❌ Don't make the tool domain-specific. `kubectl_get` is wrong;
  `bash` + a `k8s-triage` skill is right.
- ❌ Don't return giant strings — return structured JSON; the model can
  consume it more efficiently.
- ❌ Don't bypass `agentkit.New` to attach the tool to a hand-rolled
  agent.
