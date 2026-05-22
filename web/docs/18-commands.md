# Slash Commands

The **Settings → Commands** panel lets you define custom slash commands
that expand to prompt templates. Type `/` in the chat composer to see the
full command list.

## Built-in commands

Built-in commands are shipped with the Web UI and cannot be edited or
removed:

| Command | Description |
|---|---|
| `/help` | Show the command list. |
| `/compress` | Compress the current session context. |
| `/create-skill` | Start a new skill from the current conversation. |
| `/update-skill` | Update an existing skill. |
| `/status` | Show session and agent status. |
| `/learn` | Queue a soft-skill harvest for this session. |
| `/learn-now` | Run the curator immediately on this session. |

## User commands

User commands are stored in `$YOKE_HOME/config/user_commands.json` and
managed via the CRUD table in the panel.

Each command has four fields:

| Field | Required | Description |
|---|---|---|
| **Name** | yes | Command name without the leading `/`. 1–40 chars: letters, digits, `-`, `_`. Case-insensitive. Cannot shadow a built-in name. |
| **Description** | no | Short label shown in the `/` suggestion popup. |
| **Args** | no | Human-readable hint for expected arguments (shown as placeholder text). |
| **Prompt** | yes | Template body sent to the agent when the command is invoked. |

## Prompt templates

Positional substitutions in the prompt body:

| Placeholder | Expands to |
|---|---|
| `$1`, `$2`, … | The first, second, … argument typed after the command. |
| `$*` | All arguments joined as a single string. |

**Example** — a command `/review` that asks for a review of a named file:

- Name: `review`
- Args: `<file>`
- Prompt: `Review $1 for correctness, style, and potential bugs. Summarise findings in a brief list.`

Invoking `/review main.go` sends:

```
Review main.go for correctness, style, and potential bugs. Summarise findings in a brief list.
```

## Persistence

User commands are written to `$YOKE_HOME/config/user_commands.json` on
save and read back on every page load. They are not part of the
hot-reload flow — changes take effect immediately without a reload.
