---
name: root-wiring
description: Understand and modify the wiring of main.go (root) — how tools, sub-agents, plugins and toolsets are assembled into the lead agent. Use when adding a sub-agent, swapping a plugin, changing the toolset list, or debugging why a feature isn't reaching the agent. Mention triggers - root binary, main.go, wiring, lead agent, agenttool, NewMultiLoader, plugins.
---

# `main.go (root)` wiring

This is the only file in the project that **assembles** components.
It contains no logic of its own — every change here is "I want
component X to be part of the running agent".

## Mental model

```
            ┌──────────────┐
            │ agentkit.NewModel(ctx)  ← reads YOKE_PROVIDER
            └──────┬───────┘
                   │ llm
                   ▼
   ┌────────────────────────────────────────────────────────┐
   │ leadTools = []tool.Tool{                                │
   │   fstools.New()...,           // file/bash/grep/glob   │
   │   store.Tools()...,            // todo                  │
   │   g.Tools()...,                // tasks                 │
   │   worktree.Tools(repo)...,     // git worktrees         │
   │   q.Tool(),                    // bash_background       │
   │   leadMailbox.Tools()...,      // teammate comms        │
   │   agenttool.New(investigator), // sub-agent as tool     │
   │   agenttool.New(summariser),   // sub-agent as tool     │
   │ }                                                       │
   │                                                         │
   │ toolsets = [skills.Toolset, mcp.Toolsets...]            │
   └────────────────────────────────────────────────────────┘
                   │
                   ▼
            agentkit.New({Tools, Toolsets}) ──► lead
                   │
                   ▼
            adkagent.NewMultiLoader(lead, investigator, summariser)
                   │
                   ▼
            full.NewLauncher().Execute(ctx, cfg, args)
```

## Common changes

### Add a new tool

```go
leadTools = append(leadTools, mypkg.New()...)
```

Place it **before** the lead's `agentkit.New(...)` call.

### Add a sub-agent

> **Important:** do NOT pass sub-agents to `SubAgents` in the lead's
> `agentkit.AgentConfig`. When `SubAgents` is non-empty, ADK injects a
> `transfer_to_agent` function into the lead. That function permanently
> hands off control — the lead never resumes after the call. Use
> `agenttool.New` instead: it wraps the sub-agent as a regular tool that
> returns its output to the caller. In the root agent, also wrap the
> AgentTool with `newNonConcurrentTool` so duplicate calls to the same
> sub-agent in one model turn fail fast instead of blocking the turn.

```go
critic, err := agentkit.New(agentkit.AgentConfig{
    Name:        "critic",
    Description: "Pokes holes in a proposed plan.",
    Model:       llm,
    Instruction: "You are an adversarial reviewer. ...",   // role, not domain
})
if err != nil { return err }

// Register as a tool so control always returns to the leader.
wrappedCritic, ok := agenttool.New(critic, &agenttool.Config{}).(runnableTool)
if !ok { return fmt.Errorf("agenttool for critic is not runnable") }
leadTools = append(leadTools, newNonConcurrentTool(wrappedCritic))

// Add to the multi-loader so the launcher can address it:
loader, err := adkagent.NewMultiLoader(lead, investigator, summariser, critic)
```

### Add a plugin

```go
if p, err := mypkg.NewPlugin("mine"); err == nil {
    plugins = append(plugins, p)
}
```

Convention: never `return err` from a plugin constructor inside `run()`
— treat plugin failure as best-effort (log via the events plugin if
needed). Permission plugin is the exception (it's safety-critical).

### Add an MCP server

Don't touch this file. Edit `config/mcp_config.json` instead — it's
already loaded via `mcpcfg.Load("config/mcp_config.json")`.

### Add a skill

Don't touch this file. Drop a `skills/<name>/SKILL.md` — the
`skills.Toolset(ctx, "skills")` call already discovers it.

## Things to leave alone

- The `SystemPrompt` / lead `Instruction` text. They are intentionally
  domain-neutral. If you want to change agent behaviour by domain, do
  it via a skill, not here.
- The investigator / summariser sub-agents. They are the harness's
  generic "roles". Add new sub-agents alongside; don't repurpose these.
- The mailbox backend choice (`teammates.ChooseBackend()` reads
  `REDIS_URL`). Don't hard-code a backend.

## Order matters

1. `agentkit.NewModel` — fails fast on missing API key.
2. Toolsets (skills, MCP) — loaded best-effort; failures are logged.
3. Mailbox backend — `defer be.Close()` must come before the lead is
   built so it survives the function.
4. Sub-agents — built before the lead, then added to `leadTools` via
   `agenttool.New`.
5. Lead — built last, with the full `leadTools` slice.
6. `NewMultiLoader(lead, ...subAgents)` — must list every agent
   addressable by the launcher.
7. Plugins — assembled into `runner.PluginConfig`.
8. `full.NewLauncher().Execute(ctx, cfg, args)` — the only blocking
   call.

## Verify after a change

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go vet ./... && echo OK

PATH=$HOME/.local/go/bin:$PATH go run . console
> what tools and skills do you have?
```

The agent should enumerate every tool/skill you wired.
