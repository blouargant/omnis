# Permissions

Every tool call the agent makes — file edit, shell command, MCP invocation —
is filtered through the permissions engine. yoke uses **Claude Code's
permission nomenclature** as its native format. `permissions.json` holds a
`permissions` object with three rule tiers plus a `defaultMode`:

```json
{
  "permissions": {
    "defaultMode": "default",
    "allow": ["Bash(npm run *)", "Read"],
    "ask":   ["Bash(git push *)"],
    "deny":  ["Bash(rm -rf /*)", "Read(.env)"]
  }
}
```

Rules are evaluated **deny → ask → allow** — the first match wins, so a deny
always takes precedence. Anything matching no rule falls through to the mode
default (in `default` mode that means **ask** — the safe default is to confirm).

## Rule syntax

A rule is a `Tool(specifier)` string:

| Form | Matches |
|---|---|
| `Bash` | every Bash command |
| `Bash(npm run *)` | commands starting `npm run` (`*` spans any text; a trailing ` *` / `:*` enforces a word boundary, so `ls *` matches `ls -la` but not `lsof`) |
| `Read(.env)` | a `.env` file at any depth under the working dir (gitignore semantics) |
| `Edit(/src/**)` | edits under `<project root>/src/` (`//abs`, `~/home`, `/project-root`, `./cwd` anchors) |
| `mcp__server` / `mcp__server__tool` | an MCP server's tools / one exact tool |
| `Agent(Explore)` | a specific sub-agent |

`Read` rules also cover `Grep`/`Glob`; `Edit` rules also cover `Write`/`revert` —
matching Claude Code's tool fan-out.

**Bash parity:** compound commands are split on `&&`, `||`, `;`, `|` and each
part is matched independently (a deny on any part denies the whole); process
wrappers (`timeout`, `nice`, `xargs`, …) are stripped before matching; and
built-in read-only commands (`ls`, `cat`, `grep`, `git status`, …) run without a
prompt unless an `ask`/`deny` rule says otherwise.

### yoke extensions

- **Object form** — `{ "rule": "Bash(...)", "reason": "...", "cwd": "..." }`
  attaches a prompt reason and a project-scoping `cwd` (rules with a `cwd` only
  apply inside that directory tree; used by "Allow in this project" grants).
- **Regex escape hatch** — `/pattern/` (or `{ "regex": "...", "tools": ["Bash"] }`)
  matches a raw Go regexp against `toolName <json args>`. This is what the
  shipped safety floor uses for catastrophic patterns the glob syntax can't
  express.

## Permission modes (`defaultMode`)

| Mode | Behaviour for unmatched calls |
|---|---|
| `default` | prompt (ask) |
| `acceptEdits` | auto-allow edits in the working dir + common fs commands (`mkdir`, `mv`, …) |
| `plan` | reads allowed, edits and non-read-only commands denied |
| `dontAsk` | deny unless explicitly allowed |
| `bypassPermissions` | allow everything except the hard safety floor |
| `auto` | treated like `default` (no background classifier) |

## Safety floor

A built-in **safety floor** in the Bash tool runs independently of the rules:
`rm -rf /`, `mkfs`, fork bombs and similar are unconditionally refused even in
`bypassPermissions` mode and via the `!` shell-escape.

## Upgrading old configs & importing Claude Code settings

Old-format files (top-level `always_deny` / `always_allow` / `ask_user`) are
**auto-converted on load** — the file is rewritten in the new nomenclature with
a `.bak` backup kept alongside. The conversion is lossless: each old regex rule
becomes a regex-escape-hatch rule, so behaviour is unchanged.

Two CLI helpers:

```bash
yoke permissions convert -w permissions.json        # upgrade an old yoke file in place
yoke permissions import  -o permissions.json settings.json   # convert a Claude Code settings.json
```

`import` reads a Claude Code `settings.json` (or just its `permissions` block)
and prints any rules it can't map (e.g. `WebFetch(domain:…)`, which has no gated
yoke tool today).

## The `!` shell-escape

The engine governs commands the **agent** runs. When *you* run a command by
prefixing a message with `!`, the `ask` tier is **bypassed** — you already
authorised it. The hard safety floor still applies.

## Skill-contributed permissions

When a skill is loaded, the permission rules it declares are merged in
**read-only**. They appear in their own block in the Permissions panel so you
can audit them; edit the skill file to change them.

## Editing from the panel

**Settings → Permissions** renders the `defaultMode` selector and one editable
list per tier (deny / ask / allow). Each rule is a `Tool(specifier)` string with
an optional reason; complex regex rules are easier to write in the **Raw JSON**
view.
