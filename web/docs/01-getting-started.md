# Getting Started

Yoke is a configurable software-engineering agent. The same binary can act as
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

## Picking a squad for a new chat

A **squad** is a named group of agents (one leader plus the sub-agents
it can delegate to). When `agent.json` declares more than one squad, a
compact dropdown appears next to the **+** button: select the squad
you want the next session to use. With a single-squad configuration
the dropdown stays hidden and every new chat uses the default.

A session's squad is recorded for the life of the conversation; the
sidebar shows a small badge next to sessions running on a non-default
squad. To change which squad a chat uses, start a new one. See the
**Sessions** documentation page for the full lifecycle.

## Authentication

The Web UI is gated by a bearer token. The server requires `YOKE_SERVER_TOKEN`
at startup; the browser stores it locally. If the token changes server-side,
you will be prompted to enter the new value before any API call succeeds.
