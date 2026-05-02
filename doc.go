// Package agenttoolkit is a generic, vendor-neutral agent harness
// inspired by Anthropic's Claude Code.
//
// # Design contract
//
// The harness is intentionally domain-agnostic. The agent loop, the
// system prompt, and the coordinator sub-agents (investigator,
// summariser) describe a *method* — restate, plan, investigate, act,
// report, respect permissions, escalate — and never a domain.
//
// A run's effective capability is the union of three orthogonal things
// mounted from the outside:
//
//   - Tools     — Go functions exposed via google.golang.org/adk/tool
//   - Skills    — Markdown playbooks under skills/<name>/SKILL.md, loaded
//     lazily via the load_skill tool
//   - MCP       — external servers wired in via config/mcp_config.yaml
//
// The same root binary becomes a code reviewer, a Kubernetes triage
// assistant, a DBA helper, or a release engineer purely by changing what
// is mounted. No code change is required to retarget the agent at a new
// domain.
//
// # Specialisation recipe
//
// To turn the OOTB harness into, say, a Kubernetes diagnostician:
//
//  1. Drop a procedure in skills/k8s-triage/SKILL.md (already shipped as
//     an example).
//  2. Add a Kubernetes MCP server entry in config/mcp_config.yaml (or
//     rely on a kubectl bash tool).
//  3. Tighten config/permissions.yaml: read-only kubectl auto-allowed,
//     mutating verbs gated by ask_user.
//
// The lead agent will discover the new skill at startup and follow it
// when the user asks a Kubernetes question. No Go code needs editing.
//
// # Provider neutrality
//
// Model selection is controlled at runtime by GOAGENT_PROVIDER, which
// accepts gemini, anthropic, openai, and openai_compat. See core/llm.
package main
