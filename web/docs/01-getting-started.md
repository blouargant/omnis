# Getting Started

Omnis is a configurable software-engineering agent. The same binary can act as
a code reviewer, a Kubernetes triage assistant, or a DBA helper — what changes
its behavior are the **tools**, **skills**, and **MCP servers** mounted on it,
not the code.

This Web UI is one of three ways to drive the agent (alongside the CLI and the
TUI). It exposes:

- A chat surface (left sidebar: sessions / main pane: transcript and composer).
- A settings panel for editing every configuration file the agent reads.
- A documentation viewer (this page).

## Starting a conversation

1. Open the app. A first session is created automatically.
2. Type a request in the composer at the bottom and press **Enter** (or click
   **Send**).
3. The agent streams its response token-by-token in the transcript.

The agent has access to the file system rooted at the working directory the
server was launched from. It can read, grep, glob, write, and run shell
commands — subject to the permission rules described later.

> **Tip:** prefix a message with `/` to run a command, or with `!` to run a
> shell command directly on the host (e.g. `!ls -hal`). See **The Composer**.

## Switching sessions

Each chat in the **Sessions** sidebar is an isolated conversation: its
transcript, task graph, todo list, memory, and mailbox namespace are all
scoped to that session. Switching sessions never mixes state.

- **+** (top of sidebar) — start a new session.
- Click a session row — open it.
- Hover a session row — rename / delete buttons appear.

## How a new chat is routed (Omnis)

A **squad** is a named group of agents (one leader plus the sub-agents
it can delegate to). By default you don't pick a squad at all — every new
chat starts on the **Omnis router**, which reads your first request, picks
the squad best able to handle it, and hands the conversation over. That
squad's leader then answers you directly; a small **routing chip** in the
transcript shows which squad took over.

If you later switch to a topic the active squad can't handle, it quietly
hands control back to Omnis, which re-routes — each squad keeps its own
history within the session, so coming back to an earlier topic resumes
where it left off. When no squad fits, Omnis asks you a clarifying question
instead of guessing.

You can still **force** a starting squad: when `agents.json` declares more
than one squad, a compact dropdown appears next to the **+** button to pin
the next session to a specific squad (bypassing the router for that chat).
With a single-squad configuration the dropdown stays hidden. The sidebar
shows a small badge next to sessions running on a non-default squad. See
the **Sessions** documentation page for the full lifecycle, and
**Architecture** for how routing works.

## Authentication

The Web UI is gated by a bearer token. The server requires `OMNIS_SERVER_TOKEN`
at startup; the browser stores it locally. If the token changes server-side,
you will be prompted to enter the new value before any API call succeeds.
