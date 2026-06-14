# Lifecycle hooks

Hooks are **shell commands you configure to run at fixed points in the agent
loop** — before or after a tool runs, when a prompt is submitted, when the agent
stops, on session start/end, before context compaction, and on notifications.
They let you enforce policy *in code* (block edits to protected files, run a
formatter after every write, inject standing context into every prompt) instead
of hoping the model follows an instruction — the same guarantee
[Permissions](13-permissions.md) give for tool gating.

yoke's hooks are a faithful port of **[Claude Code hooks](https://code.claude.com/docs/en/hooks-guide)**:
the `hooks.json` format and the stdin/stdout protocol match, so an existing
Claude Code hooks block is portable.

## Config: `hooks.json`

A `hooks.json` file resolved through the usual [config search chain](14-config.md).
Its top-level `hooks` object maps each **event** to a list of **matchers**, each
holding one or more **commands**:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Write|Edit",
        "hooks": [
          { "type": "command", "command": "./scripts/guard.sh", "timeout": 30 }
        ]
      }
    ],
    "PostToolUse": [
      { "matcher": "Edit", "hooks": [ { "command": "gofmt -w ." } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "command": "echo 'House style: be terse.'" } ] }
    ]
  }
}
```

- **`matcher`** is a Go regexp matched against the **tool name** for
  `PreToolUse` / `PostToolUse` (e.g. `Write|Edit`, `Bash`, `mcp__.*`). It is the
  **trigger** for `PreCompact` and the **sub-agent name** for `SubagentStop`, and
  is **ignored** for the events that have no subject. An empty matcher (or `"*"`)
  matches everything. An invalid regexp falls back to exact string equality.
- **`command`** is the shell line to run. `type` defaults to `"command"` (the
  only kind). `timeout` is in seconds (default 60).

## Events

| Event | Fires | `matcher` matches | Can block? |
|---|---|---|---|
| `PreToolUse` | before a tool runs | tool name | **yes** — blocks the tool |
| `PostToolUse` | after a tool runs | tool name | feeds a reason back to the model |
| `UserPromptSubmit` | when you submit a prompt | — | **yes** — aborts the turn; can inject context |
| `Stop` | when the agent finishes a turn | — | no (notification) |
| `SubagentStop` | when a sub-agent invocation finishes | sub-agent name | no (notification) |
| `SessionStart` | when a session is created | — | injects context |
| `SessionEnd` | when a session ends | — | no (notification) |
| `PreCompact` | before context compaction | trigger (`manual`/`auto`/`hard`) | no (notification) |
| `Notification` | on a notification (e.g. a permission/ask-user prompt) | — | no (notification) |

## What a hook receives (stdin)

Each command is run with a JSON object on **stdin** describing the event — the
same schema Claude Code uses:

```json
{
  "session_id": "teaching-kite",
  "cwd": "/home/you/project",
  "hook_event_name": "PreToolUse",
  "tool_name": "Write",
  "tool_input": { "file_path": "config/.env", "content": "…" }
}
```

Common fields: `session_id`, `cwd`, `hook_event_name`. Per-event extras:
`tool_name` + `tool_input` (`PreToolUse`), plus `tool_response` (`PostToolUse`),
`prompt` (`UserPromptSubmit`), `message` (`Notification`), `trigger`
(`PreCompact`).

## What a hook returns (exit code + stdout)

| Signal | Effect |
|---|---|
| **exit 0** | proceed. For `UserPromptSubmit` / `SessionStart`, plain stdout is **injected as context** for the model. |
| **exit 2** | **block** — stderr is the reason fed back (the tool is refused, or the prompt is aborted). |
| any other non-zero | non-blocking error — stderr is logged, the action proceeds. |

For finer control, print a **JSON object** on stdout:

```json
{
  "decision": "block",
  "reason": "Editing .env is not allowed",
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "deny",
    "additionalContext": "…"
  }
}
```

- `hookSpecificOutput.permissionDecision` — `deny` blocks the tool, `allow`
  runs it without a permission prompt, `ask` defers to the permission rules
  (`PreToolUse`).
- `decision: "block"` + `reason` — block (`PreToolUse` / `UserPromptSubmit`) or
  surface the reason to the model (`PostToolUse`).
- `additionalContext` — extra text added to the turn (`UserPromptSubmit` /
  `SessionStart`).
- `systemMessage` — a message shown to you; `continue: false` requests the
  agent stop.

## Examples

**Block edits to secrets (PreToolUse):**

```bash
#!/usr/bin/env bash
# ./scripts/guard.sh — refuse writes to .env files
path=$(jq -r '.tool_input.file_path // empty')
case "$path" in
  *.env|*/.env) echo "Editing $path is blocked by policy" >&2; exit 2 ;;
esac
```

**Format Go files after every edit (PostToolUse):**

```json
{ "hooks": { "PostToolUse": [ { "matcher": "Edit|Write", "hooks": [ { "command": "gofmt -w ." } ] } ] } }
```

**Inject standing context on every prompt (UserPromptSubmit):**

```json
{ "hooks": { "UserPromptSubmit": [ { "hooks": [ { "command": "cat .agents/house-style.txt" } ] } ] } }
```

## Editing from the panel

**Settings → Hooks** lists every event with an editor of **matcher cards** —
each has a matcher field and a list of `command` + `timeout` rows. Saves write
`hooks.json` to your user (or project) config layer. The **Raw JSON** view is
handy for bulk edits.

## Hot reload & trust model

- **Hot reload** — `hooks.json` is polled, so edits take effect within a few
  seconds with no restart.
- **Layered & additive** — a user-layer `hooks.json` *adds* its commands to a
  project/system one (they don't shadow each other).
- **Trust** — like the `!` shell-escape, hooks run **outside the permission
  layer** (you authored them) but the hard **[safety floor](13-permissions.md#safety-floor)**
  (`rm -rf /`, `mkfs`, fork bombs) still applies. Hooks run with your full user
  permissions — review any hook you didn't write.

## Limitations

- `Stop` / `SubagentStop` hooks fire as notifications but cannot force the agent
  to keep going; `PreCompact` cannot rewrite the compaction.
- `PreToolUse` / `SubagentStop` do not fire for a sub-agent's **internal** tool
  calls (sub-agents run in a private runner) — `SubagentStop` covers their
  completion.
- An absent or empty `hooks.json` is a complete no-op.
