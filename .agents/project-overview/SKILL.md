---
name: project-overview
description: Bootstrap context for a new development session on the agent-toolkit repository. Use at the start of every session to learn what this codebase is, its design contract, the directory layout, and where each concern lives. Mention triggers - new session, get up to speed, project structure, where is X, what does this repo do.
license: project
metadata:
  audience: dev-agent
  scope: repo-bootstrap
---

# Project overview — agent-toolkit

A **generic, vendor-neutral agent harness** in Go, inspired by Anthropic's
**Claude Code** methodology. Module path: `github.com/blouargant/agent-toolkit`.

## The single most important rule

> The harness is generic and stays generic. The agent's effective
> capability on any run = union of mounted **tools + skills + MCP
> servers**. The same `cmd/full` binary becomes a code reviewer, a K8s
> diagnostician, a DBA, etc., purely by changing what is mounted.
> **Never bake a domain into Go code or a system prompt.**

If a change you are about to make adds domain knowledge to a `.go` file
or to `core/agentkit/agentkit.go`'s `SystemPrompt`, **stop and put it in
a `skills/<name>/SKILL.md` instead.**

## Stack

- Go ≥ 1.25, no third-party LLM SDKs in `go.mod`.
- Built on top of `google.golang.org/adk` (agent loop, runner, session,
  plugins, toolsets, MCP, skill loader).
- Multi-provider LLM dispatcher: gemini / anthropic / openai /
  openai_compat (last three are direct HTTP+SSE adapters in
  `core/llm/`).

## Directory map

```
agent-toolkit/
├── cmd/
│   ├── full/                  # all-in-one launcher (REPL + web). The binary you specialise.
│   └── sNN_<topic>/           # 23 single-component demos, one per article phase
├── core/
│   ├── agentkit/              # central agent constructor + universal SystemPrompt
│   ├── llm/                   # provider dispatcher (llm.go) + anthropic.go, openai.go, convert.go
│   ├── tools/                 # file/bash/grep/glob/revert tools
│   ├── permissions/           # YAML-driven permission plugin
│   ├── events/                # event bus + file logger
│   └── stream/                # streaming helpers
├── internal/
│   ├── todo/  tasks/  bg/     # planning + background work
│   ├── worktree/              # git worktree isolation tools
│   ├── teammates/             # mailbox / FSM inter-agent comms (in-mem + redis backends)
│   ├── compress/  cache/      # plugins
│   ├── skills/  mcp/          # loaders for skills/ and config/mcp_config.yaml
├── skills/                    # specialisation playbooks (SKILL.md per folder)
│   ├── review/                # generic review playbook
│   ├── agent-builder/         # checklist for new specialist agents
│   ├── pdf/                   # narrow tool-bound skill
│   └── k8s-triage/            # example domain specialisation
├── config/
│   ├── permissions.yaml       # safety envelope (always_deny/_allow/ask_user)
│   └── mcp_config.yaml        # MCP servers (filesystem, k8s, postgres, github, …)
├── doc.go                     # package-level overview for go doc
├── docs/                      # full markdown documentation set
└── .agents/                   # ← these bootstrap skills (you are here)
```

## Documentation index

When you need depth on something, read the matching file under
[docs/](../../docs/):

| Topic                                      | File                                |
|--------------------------------------------|-------------------------------------|
| High-level architecture & lifecycle        | `docs/architecture.md`              |
| The 7-step Claude-Code-style methodology   | `docs/methodology.md`               |
| LLM provider configuration                 | `docs/providers.md`                 |
| How to specialise the agent (no Go change) | `docs/specialising.md`              |
| Authoring `skills/<name>/SKILL.md`         | `docs/skills.md`                    |
| `permissions.yaml` + `mcp_config.yaml`     | `docs/configuration.md`             |
| The 23 demo binaries                       | `docs/cmd-catalog.md`               |
| Adding tools / plugins / sub-agents        | `docs/extending.md`                 |

## When in doubt

- Adding behaviour for a new domain → skill (`skills/<name>/SKILL.md`).
- Adding a new tool surface → load via MCP (`config/mcp_config.yaml`).
- Adding a destructive verb → pair it with a `permissions.yaml` rule.
- Adding a generic capability to the agent → new tool in `core/tools` or
  `internal/`, then wired into `cmd/full/main.go`.
- Touching the `SystemPrompt` → ask yourself first: is this about
  *method* (good) or about a *domain* (bad)?

## Where to look first when starting work

1. Read this skill (you just did).
2. Read the [`build-and-test`](../build-and-test/SKILL.md) skill before
   running anything.
3. Read the [`coding-conventions`](../coding-conventions/SKILL.md) skill
   before writing Go.
4. Pick the relevant task-specific skill below and follow it.
