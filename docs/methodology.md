# Operating methodology

The harness follows the methodology proven by Anthropic's **Claude
Code**. It is intentionally compact and intentionally domain-free: it
describes *how* an agent should approach any task, not *what* the task
is about.

The full text lives in [`core/agentkit/agentkit.go`](../core/agentkit/agentkit.go)
as the `SystemPrompt` constant and is prepended to every agent
constructed via `agentkit.New()`.

## The 7 steps

| #  | Step          | Why it exists                                                                                  |
|----|---------------|------------------------------------------------------------------------------------------------|
| 1  | **RESTATE**   | Forces shared understanding before anything irreversible runs.                                 |
| 2  | **PLAN**      | Breaks multi-step work into individually verifiable units (`todo_write` / `task_create`).      |
| 3  | **INVESTIGATE** | Replaces assumptions with evidence — call read-only tools, not memory.                       |
| 4  | **ACT**       | Small reversible steps. Prefer dedicated tools over raw shell. Prefer dry-runs over mutations. |
| 5  | **REPORT**    | After every action: what was done, what was observed, what is next.                            |
| 6  | **RESPECT**   | If a tool call is denied, *do not retry* — surface to the user.                                |
| 7  | **ESCALATE**  | When ambiguity remains after one round of evidence gathering, ask. Don't guess.                |

## Tool selection rules

- Prefer a `load_skill` call when one matches the task: skills encode
  proven, domain-specific procedures.
- Use `bash_background` for any command expected to take more than a
  couple of seconds; check the queue between turns.
- Use `teammate_ask` / `teammate_tell` for inter-agent coordination,
  not free text.

## Graceful degradation

The last sentence of the system prompt is critical:

> *If a step in this protocol references a tool you do not have, skip
> it silently rather than refusing the task.*

This is what makes the same prompt usable in a stripped-down agent
(e.g. the `summariser` sub-agent has no tools at all) and in a
fully-loaded specialist with MCP servers attached.

## Why "method, not domain"?

A method-only prompt:

- Is **stable** — it does not need editing when you target a new
  domain.
- Is **composable** — every sub-agent inherits the same operating
  contract, so they are predictable from the lead's point of view.
- Is **safe** — RESPECT and ESCALATE create natural circuit breakers.
- Is **portable** — works across providers; no provider-specific
  hand-holding bakes in.

Domain knowledge belongs in [skills](skills.md), tools and MCP servers.
That is the contract.
