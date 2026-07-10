# About Omnis

**Version:** `{{OMNIS_VERSION}}`

Omnis is a **configurable, multi-agent software-engineering assistant**. The
same binary can act as a code reviewer, a Kubernetes triage assistant, a DBA
helper, or a general coding partner — what changes its behaviour are the
**tools**, **skills**, and **MCP servers** mounted on it, not the code. No
rebuild is needed to retarget the agent.

You are looking at the **Web UI**, one of three ways to drive Omnis alongside a
command-line REPL (CLI) and a terminal interface (TUI). All three share the same
agents, configuration, and session store.

## What Omnis is

- **Multi-agent by design.** Work is organised into **squads** — a named group
  of a leader agent plus the specialists it can delegate to (a code scout, a web
  researcher, a reviewer, and so on). Each session runs on one squad.
- **Router-first.** Every new chat starts on the **Omnis router**, which reads
  your request, picks the squad best suited to it, and hands over. Change topic
  and control quietly returns to the router, which re-routes. When nothing fits,
  Omnis asks a clarifying question instead of guessing.
- **Provider-agnostic.** Omnis talks to Anthropic, OpenAI, Gemini, and any
  OpenAI-compatible endpoint (including local models and LiteLLM gateways).
  Different agents can run on different models — expensive reasoning for design
  and edits, cheap fast models for high-volume search.

## What Omnis can do

- **Work with your files and shell.** Read, grep, glob, edit, and write files,
  and run shell commands — all rooted in the session's working directory and
  gated by permission rules you control.
- **Follow playbooks (Skills).** Curated, reusable instruction sets the agent
  loads on demand for repeatable procedures.
- **Extend through MCP and A2A.** Mount external **MCP servers** for extra
  tools, and connect to remote **A2A** agents as delegable teammates.
- **Enforce policy in code.** **Permissions** gate every tool call
  (allow / ask / deny), and **lifecycle hooks** run your own shell commands at
  key moments (before/after a tool, on prompt submit, on session end).
- **Run itself.** Set a recurring prompt with `/loop`, a durable cron routine
  with `/schedule`, or a self-driving completion goal with `/goal`.
- **Remember and learn.** Project memory (`AGENT.md`) is injected on every turn,
  and a post-session curator distils reusable soft-skills, with semantic recall
  across past sessions, your codebase, and the docs.
- **Edit its own configuration.** Change models, agents, permissions, themes,
  and more from the Settings panel — or just ask the in-app assistant to do it.

## Getting oriented

- New here? Start with **Getting Started**.
- Want the full picture of squads and routing? See **Architecture**.
- Configuring the agent? See **Settings Panel**, **Providers & Models**,
  **Permissions**, and **Configuration & Filesystem**.

> **Tip:** Omnis periodically checks for a newer stable release. When one is
> available an **Update** button appears next to the "Omnis" title in the
> sidebar; the version shown above is the build you are running.
