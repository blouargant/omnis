# Permissions

Every tool call the agent makes — file edit, shell command, MCP invocation —
is filtered through the permissions engine. The engine reads
`permissions.json` and applies rules in three tiers, **stopping at the
first match**:

1. **always_deny** — the call is rejected, the agent receives an error, no
   prompt is shown to you.
2. **always_allow** — the call runs silently.
3. **ask_user** — the call is held; you see a confirmation prompt in the chat
   with the exact tool name and arguments. You may approve **once**, approve
   **for the rest of the session**, or deny.

Anything not matched by any rule falls through to **ask_user** by default —
i.e. the safe default is to confirm.

## Rule shape

A rule matches by:

- **Tool name** (exact match, or wildcard like `mcp__github__*`).
- An optional **regex** over the JSON-encoded call arguments.

For shell commands a built-in **safety floor** runs *before* the rules: even
an `always_allow` rule cannot authorise commands that look like `rm -rf /`
or that try to disable hooks (`--no-verify`). Those are unconditionally
rejected and a one-line explanation is returned to the agent.

## The `!` shell-escape

The permissions engine governs commands the **agent** decides to run. When
*you* run a command directly by prefixing a message with `!` (see **The
Composer → Shell commands**), the `ask_user` tier is **bypassed** — you already
authorised it by typing it. The same hard **safety floor** still applies, so
`rm -rf /` and friends are refused regardless.

## Skill-contributed permissions

When a skill is loaded, any permission rules it declares are merged into the
active set **read-only**. They appear in their own block in the Permissions
panel so you can audit them but cannot edit them in place; to change them,
edit the skill file.

## Editing from the panel

The **Settings → Permissions** view renders one card per tool group with
sliders for the three tiers and a regex field. The form covers the common
cases; complex multi-rule scenarios are easier to write directly in the
**Raw JSON** view.

A save warns you that ambiguity in regex patterns can cause `always_deny` to
shadow `always_allow` — the engine evaluates tiers top-down for safety, so a
deny that matches first wins even if a more specific allow exists later.
