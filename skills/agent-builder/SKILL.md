---
name: agent-builder
description: Help the user scaffold a new specialised agent and wire it into the harness. Use when the user says "create an agent", "add a specialist", or wants to extend the harness for a new domain.
---

# Agent Builder

The harness is generic on purpose: every specialisation is a combination
of (tools + skills + MCP servers + permissions + a focused instruction).
When the user wants a new specialist agent, walk this checklist:

## Interview

1. **Domain & goal.** One sentence: what does this agent do?
2. **Required capabilities.** Which existing tools are needed (fs, bash,
   bg, todo, tasks, worktree, teammate)? Which new MCP servers, if any?
3. **Inputs / outputs.** What does the user hand the agent, and what
   shape should the answer take?
4. **Safety envelope.** Which actions must always be denied? Which
   require user approval?

## Scaffolding

1. Create `cmd/<name>/main.go` modelled on `cmd/s01_loop/main.go`:
   reuse `agentkit.NewModel`, `agentkit.New`, `agentkit.Runner`,
   `agentkit.RunOnce`.
2. Compose tools from existing packages — do not hand-roll new tools
   unless the capability genuinely doesn't exist.
3. If a domain-specific procedure is repeatable, encode it as a
   **skill** under `skills/<name>/SKILL.md` rather than as prose in the
   instruction. Skills are the unit of specialisation.
4. If the agent should be reachable from the lead coordinator, register
   it via `agenttool.New` and add it to the multi-loader in
   `cmd/full/main.go`.
5. Add or extend `config/permissions.yaml` for the new tool surface.
6. (Optional) Subscribe a domain-specific event handler via `events.Bus`
   for observability.

## Deliverable

Give the user the diff (or new files) plus one sample prompt that
exercises the new agent end to end.
