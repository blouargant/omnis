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

- `/init` — analyze the repository and write a starter **AGENT.md** project
  memory file (see **Project Memory (AGENT.md)**).
- `/review` — review the pending changes on the current branch.
- `/security-review` — run a focused security audit.

The slash registry is populated from the agent's loaded skills; see the
**Skills** section for how to add your own.

## Shell commands (`!`)

Start a message with `!` to run the rest of the line **directly on the host**,
without involving the agent — a shell-escape, like `!` in `vim` or `psql`. For
example:

```
!ls -hal /tmp
!git status
!cd src && go build ./...
```

The command runs on the machine the server is running on, and its output is
shown in a green terminal-style block right in the chat.

- **Permissions are bypassed.** Because you typed the command yourself, it is
  *not* routed through the permission prompts (the `ask` tier). The hard **safety
  floor** still applies, so commands like `rm -rf /` are refused (see the
  **Permissions** section).
- **The working directory persists per session.** `!cd somedir` affects later
  `!` commands in the same session; the directory is shown under each block.
  (Only the directory carries over — environment variables and shell functions
  do not, because each command runs in a fresh shell.)
- **Not part of the conversation.** Shell output is a live convenience: it is
  shown in the transcript but is *not* sent to the model and does *not* survive
  a page reload.

## Project memory (`#`)

Start a message with `#` to append the rest of the line as a one-line note to
the project **AGENT.md** instead of sending it to the agent:

```
#always run `make fmt` before committing
```

The note is saved under a `## Notes` section of the repository's `AGENT.md`
(created if missing) and is loaded into the agent's context on every later turn.
See **Project Memory (AGENT.md)** for the full picture.

### Tab completion

As you type after `!`, a completion menu appears, just like a terminal:

- The **first word** completes against executable command names on your `$PATH`.
- **Later words** (and anything containing a `/`) complete against files and
  directories, relative to the session's current directory. Directories get a
  trailing `/` so you can keep completing into them.

Use **↑/↓** to move through the suggestions, **Tab** or **Enter** to accept the
highlighted one, and **Esc** to dismiss the menu. Pressing **Enter** with
nothing highlighted runs the command.

> The same `!` shell-escape and completion are also available in the terminal
> UI (`yoke tui`), where it uses the input field's autocomplete dropdown.

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
