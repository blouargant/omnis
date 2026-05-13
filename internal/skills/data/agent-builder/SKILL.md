---
name: agent-builder
description: Scaffold a new specialised agent and wire it into the harness. Use when the user says "create an agent", "add a specialist", "build a new agent", or wants to extend the harness for a new domain. Covers tool composition, skill authoring, MCP server wiring, permissions, and coordinator registration.
compatibility: Designed for the yoke harness (github.com/blouargant/yoke).
---

# Agent Builder

Every specialisation in the harness is a combination of:
**tools + skills + MCP servers + permissions + a focused system instruction.**

Nothing is hand-rolled from scratch — compose from existing packages and encode domain knowledge as skills.

## Step 1 — Interview

Before writing any files, gather answers to these four questions:

- [ ] **Domain & goal.** One sentence: what does this agent do?
- [ ] **Required capabilities.** Which existing tools are needed (`fs`, `bash`, `bg`, `todo`, `tasks`, `worktree`, `teammate`)? Any new MCP servers?
- [ ] **Inputs / outputs.** What does the caller hand the agent, and what shape should the response take?
- [ ] **Safety envelope.** Which actions must always be denied? Which require explicit user approval?

## Step 2 — Create the skill file

If the domain procedure is repeatable, encode it first as a skill — skills are the primary unit of specialisation.

1. Create `skills/<name>/SKILL.md` with proper frontmatter (`name`, `description`, optional `compatibility`).
2. Write the body as step-by-step instructions. Add a `## Gotchas` section for non-obvious domain facts the agent would get wrong without guidance.
3. Keep `SKILL.md` under 500 lines. Move detailed reference material to `skills/<name>/references/` and tell the agent exactly when to load each file.

## Step 3 — Wire tools and configuration

1. Compose tools from existing packages — do not hand-roll new tools unless the capability genuinely does not exist.
2. Add or extend `config/permissions.yaml` for any new tool surface introduced by this agent.
3. If new MCP servers are required, add them to `config/mcp_config.yaml`.

## Step 4 — Register with the coordinator

If the agent should be callable from the lead coordinator:

1. Register it via `agenttool.New` in the root `main.go` multi-loader.
2. Give it a short, keyword-rich description — the coordinator uses this to decide when to delegate.

## Step 5 — Observability (optional)

Subscribe a domain-specific event handler via `events.Bus` to emit structured events for monitoring and replay.

## Deliverable

Produce the following and nothing more:
1. `skills/<name>/SKILL.md` — the skill file.
2. Diffs for `config/permissions.yaml` and `config/mcp_config.yaml` if changed.
3. Diff for root `main.go` coordinator registration if the agent is to be reachable.
4. One sample prompt that exercises the new agent end to end.

## Gotchas

- Do not create `examples/<name>/main.go`. Example binaries require a Go compiler and have no utility outside of a development environment; production deployments use the pre-built coordinator binary.
- Do not hand-roll tools that duplicate existing packages (`fs`, `todo`, `tasks`, `worktree`, `bg`, `mailbox`). Check `core/tools/` and `internal/` before writing new code.
- `config/permissions.yaml` is enforced at runtime — omitting a required permission will silently deny the tool call, not produce a compile error.
- Skills are loaded by `name` matching the directory name exactly. A mismatch causes silent non-activation.
