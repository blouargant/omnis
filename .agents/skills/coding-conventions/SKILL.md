---
name: coding-conventions
description: Go coding conventions, architectural rules and anti-patterns for the omnis repository. Use whenever writing or reviewing Go code in this repo. Mention triggers - write Go code, refactor, code review, conventions, style, architecture rule, where should this go.
---

# Coding conventions

## Architectural rules (non-negotiable)

1. **No domain knowledge in Go.** Domain knowledge belongs in
   `skills/<name>/SKILL.md`, MCP servers, or `permissions.json`. If you
   are tempted to write `if topic == "kubernetes"` or to mention
   `kubectl` / `psql` / `aws` in a Go string literal under `core/` or
   `./`, **stop**. Move it to a skill.

2. **The system prompt describes a method, not a domain.** When editing
   `core/agentkit/agentkit.go`'s `SystemPrompt`, only add things that
   apply to *every* possible task (planning, evidence-gathering,
   reporting, escalation). Domain rules go in skills.

3. **Sub-agents are role-based, not domain-based.** The two shipped
   sub-agents (`investigator`, `summariser`) are roles ("gather
   evidence", "compress output"). When adding a new one, give it a
   *role* (validator, critic, planner, …) — never a *domain* (k8s-bot,
   pg-bot, …).

4. **Every mutating tool needs a permission rule.** When adding any
   tool that writes state, also add a matching rule to
   `config/permissions.json` under the `ask` tier (or `deny` for
   destructive ones).

5. **Use `agentkit.New`, never `llmagent.New` directly.** That guarantees
   every agent inherits the universal `SystemPrompt`.

6. **No new LLM SDKs in `go.mod`.** Use the in-tree `core/llm/` adapters
   (HTTP + SSE). If a provider is OpenAI-compatible, just point
   `OPENAI_BASE_URL` — don't write code.

## Layout rules

- `core/` — generic, reusable building blocks. Stable surface.
- `internal/` — components specific to this harness; no public API
  promise.
- `cmd/sNN_<topic>/` — single-component demos. Each one isolates one
  feature; keep them ≤ ~150 lines.
- `./` — the wiring binary. It assembles components but
  contains no logic.
- `skills/` — specialisation playbooks (Markdown, not Go).
- `.agents/` — bootstrap skills for *the development agent itself* (the
  one writing code in this repo).

## Go style

- Standard `gofmt`. No custom formatter.
- Errors: `fmt.Errorf("pkg.Func: %w", err)` — always wrap with the
  caller's identity.
- Tool definitions go in their own `.go` file named after the tool
  (`grep.go`, `revert.go`, …).
- Constructors return `(T, error)`, not `T` with a panic.
- Tests live next to code as `*_test.go`. Currently sparse — add tests
  when you change risky paths.

## Avoid these patterns

| ❌ Anti-pattern                                              | ✅ Do instead                                                  |
|--------------------------------------------------------------|----------------------------------------------------------------|
| Hard-coding domain in a sub-agent's `Instruction`            | Encode the procedure in `skills/<name>/SKILL.md`               |
| One sub-agent per domain                                     | One generic role-based sub-agent + many skills                 |
| Calling `llmagent.New` directly                              | Call `agentkit.New(AgentConfig{...})`                          |
| Adding a third-party LLM SDK to `go.mod`                     | Extend `core/llm/` with a small HTTP+SSE adapter               |
| Mutating tool without a permission rule                      | Pair it with an `ask`-tier rule in `permissions.json`          |
| Documenting domain rules in a Go comment                     | Move them into the relevant `SKILL.md`                         |
| Refactoring code unrelated to the requested change           | Make the smallest change that satisfies the request            |
| Adding docstrings/comments to code you didn't change         | Don't                                                          |
| Catching an error scenario that cannot happen                | Validate only at system boundaries                             |

## When the codebase pushes back

If you find yourself fighting the architecture (e.g. "I need a
domain-specific sub-agent because skills aren't expressive enough"),
that is a smell. Re-read [project-overview](../project-overview/SKILL.md)
and [the methodology doc](../../docs/methodology.md) before continuing.
