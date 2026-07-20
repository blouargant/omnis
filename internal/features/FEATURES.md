# Omnis — What's New

<!--
  MAINTAINED FILE — see "What's-new tracking (FEATURES.md)" in CLAUDE.md.

  This is the source for the web UI's "What's new" modal. Rules:
    * One `## A.B — <one-line summary>` section per MINOR version, newest first.
    * The summary after the em dash is shown on its own when a section is
      compacted (old versions in a long span); keep it to a single line.
    * Under each section, `- **Title** — description.` bullets, one per
      user-facing feature. User-facing only — no refactors/internal plumbing.
    * The patch digit C in A.B.C is bug-fixes only; NEVER add a `## A.B.C`
      section. Fold nothing patch-level here.
    * Features still in development go under a `## A.B (in development)`
      section whose version is ABOVE the latest release; the modal filters it
      out until that minor is actually tagged, so it never shows unreleased work.
    * English only (release notes follow the same policy as the docs).
-->

## 1.9 (in development) — Collection memory management

- **Collection memory size** — choose a Small / Medium / Large memory budget per collection; a live word counter shows how close you are.
- **Automatic memory updates** — opt a collection into keeping its memory current from recent chats, with one-click revert.

## 1.8 — Collection context & session search

- **Per-collection context** — a collection can carry persistent instructions + memory that follow a workstream across repos, plus a seeded starting squad and working directory.
- **Assisted collection memory** — generate a collection's memory from its recent chats, and a drafting assistant that helps you write the instructions.
- **Agent instruction assistant** — a drafting assistant on the agent Settings panel that helps you write an agent's system instruction and public description, grounded in the agent's own tools, skills, and model.
- **Squad graph view** — a Graph toggle in Settings → Agents → Squads visualises a squad's delegation tree (leader → members → nested sub-agents); for the Omnis router it shows the squads it can route to. Click a node to jump to that agent or squad.
- **Find a past chat** — search every past conversation (including archived) from the new-tab landing page or a chat.
- **More web-search providers** — added Serper.dev as the recommended provider, with a "Test" button for web-search API keys in Settings.

## 1.7 — Nested sub-agents, spend budgets & a responsive UI

- **Session Collections** — a three-pane, email-style layout that files chats into thematic folders, each with its own colour.
- **Nested sub-agents** — any agent can now delegate to its own cheap "gatherer" team, keeping expensive models off raw retrieval.
- **Per-turn spend budget** — a runaway turn pauses and asks you before it keeps spending, instead of burning tokens unattended.
- **Deep-research tier** — a dedicated research reviewer checks findings for accuracy before they're delivered.
- **Responsive small-screen layout** — an off-canvas navigation drawer makes Omnis usable on a phone.
- **Copy button on code blocks**, **import a chat from a file**, and replies now come back in your own language.

## 1.6 — Coding squad, session spawning & multi-kind registries

- **Coding squad with language-server (LSP) integration** — symbol lookups, diagnostics, safe renames, and quick-fix code actions for token-efficient coding.
- **Session spawning** — an agent (or you, via /spawn) can start a fresh background session and get the result delivered back.
- **Multi-kind remote registries** — one registry can serve skills, agents, MCP servers, A2A peers, squads, commands, and permission rule-sets.
- **Layered configuration** — per-user edits keep tracking package updates instead of freezing the whole config.
- **Per-session working directory** — a chat (and its forks) remembers where it works, surviving restarts.
- **New themes** — Terracotta, Ivory, and claude-light, plus a restyled composer and editor.
- **Independent Kubernetes compliance auditor** with sharper auditing skills.

## 1.5 — Automation, goals & resumable sub-agents

- **Automation** — /loop and /schedule run a prompt on a recurring timer, managed from Settings → Automation.
- **/goal** — Omnis keeps taking turns on its own until a small evaluator model judges your goal met.
- **Resumable sub-agent sessions** — the leader can continue a sub-agent's exact prior work instead of restarting it.
- **Prompt caching for Anthropic models** for faster, cheaper multi-turn chats.
- **Light-theme polish** — color-scheme support and a friendly default theme for first-time visitors.

## 1.4 — Sidebar refresh

- **Refreshed sidebar** — app logo and styling adjustments.

## 1.2 — Self-update, fork/rewind & background server

- **In-app self-update** — Omnis detects a newer release and installs it in one click for your install method.
- **Fork & rewind conversations** — branch a chat from any earlier turn, or roll it back to edit and resend.
- **Background server** — `omnis-server start / stop / status` runs the server detached so it frees your terminal.
- **Auto-generated chat titles** — a new chat gets a topic-bearing name from your first message.
- **Desktop notifications** — get pinged when a background task finishes or a reply lands while you're away.
- **Background command monitoring** — watch a long-running command and react to matching output.
- **Draft preservation across tabs** and a **shared Agent-Skills registry**.

## 1.1 — The Omnis router, resilient streaming & health checks

- **Omnis router** — every new chat is routed to the squad best able to answer it, and re-routed when the topic changes.
- **Lifecycle hooks** — run your own shell commands on tool/prompt/session events via hooks.json.
- **Provider health checks** — a warning banner and fix-it modal when a model provider is unreachable.
- **Resilient turn streaming** — a turn survives a dropped connection and reconnects mid-work.
- **Custom slash commands** — create and edit your own /commands.
- **Cross-browser session sync** and **per-agent token usage & cost breakdown**.
- **Kubernetes and Knowledge squads**, plus **pip install** (`omnis-agent`).
