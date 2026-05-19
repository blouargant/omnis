# The Composer

The composer at the bottom of the chat is the single entry point for every
message you send to the agent.

## Sending a message

- **Enter** sends. **Shift+Enter** inserts a new line.
- The **edit-mode** button (notepad icon) toggles this behavior: when active,
  Enter inserts a new line and **Cmd/Ctrl+Enter** sends. Useful when drafting
  longer prompts.
- The **Stop** button cancels a streaming response.

## Attachments

Click the paperclip to attach files in two ways:

- **Upload from computer** — multipart-uploads files to the server; they are
  stored under `$YOKE_HOME/logs/uploads/<session>/` and the agent receives
  paths it can read with the `read` tool.
- **Add context** — opens a file browser anchored at the server's working
  directory. The selected paths are pinned as a "context" header above the
  prompt and read by the agent on the first turn.

Uploads are session-scoped and garbage-collected when the session is deleted.

## Slash commands

Type `/` (or click the `/` button) to open the slash menu. Slash commands
are short prompts the agent expands into a full instruction — for example:

- `/init` — write or refresh a CLAUDE.md file describing the codebase.
- `/review` — review the pending changes on the current branch.
- `/security-review` — run a focused security audit.

The slash registry is populated from the agent's loaded skills; see the
**Skills** section for how to add your own.

## Context ring

The small ring next to the Stop/Send buttons visualises how much of the
model's context window the current session is consuming. Click it to see:

- Tokens used vs. the model's maximum context.
- Percentage and remaining budget.
- A per-sub-agent breakdown when multiple agents have contributed.
- A **Compress Now** button — invokes the compression plugin, which
  summarises older turns into `agent_memory_*.md` and shortens the live
  history without losing decisions.

The ring fills red as you approach the budget. Compression is also triggered
automatically at high watermarks.
