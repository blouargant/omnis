# Permissions

Every tool call the agent makes — file edit, shell command, MCP invocation —
is filtered through the permissions engine. omnis uses **Claude Code's
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

The engine covers **every** agent's tool calls, not just the leader you chat
with: when the leader delegates to a sub-agent, that sub-agent's own file edits,
shell commands, and MCP calls are gated by the same rules (and share your
session's "allow this session / project" grants). A prompt you don't answer
**waits** for you — it is not auto-denied on a timer — until you respond or stop
the turn.

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
built-in read-only commands (`ls`, `cat`, `grep`, `git status`, `which`,
`command -v`/`command -V`, …) run without a prompt unless an `ask`/`deny` rule
says otherwise. (`command` is only treated as read-only with a `-v`/`-V` lookup
flag — `command rm …` still goes through the rules, since it *executes* its
argument.)

### omnis extensions

- **Web downloads run without asking** — the shipped `allow` tier includes
  `Bash(curl *)`, `Bash(wget *)`, `Bash(pdftotext *)` and `Bash(pdfinfo *)`, and
  `cd /tmp` no longer asks, so a research agent that downloads a page or a PDF
  never stops on a card. A `curl`/`wget` still asks when it sends data (`-d`,
  `-F`, `-T`, `--data…`, `--json`, `-K`, a `POST`/`PUT`/`PATCH`/`DELETE`),
  writes outside `/tmp` and the working directory (`-o`/`-O`/`-P`/`>` to an
  absolute, `~`, `$…`, hidden or `..` path; `/dev/…` is fine), or carries a
  command substitution or a secret-looking variable (`$GITHUB_TOKEN`). Most
  research needs no shell at all: `WebFetch` reads HTML, PDF (with
  `pages="3-4"`), Word, OpenDocument, EPUB, RTF, JSON and CSV.
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
| `acceptEdits` (shipped default) | auto-allow edits in the session's working folder + common fs commands (`mkdir`, `mv`, `cp`, …) whose paths are all inside it; everything else prompts |
| `plan` | reads allowed, edits and non-read-only commands denied |
| `dontAsk` | deny unless explicitly allowed |
| `bypassPermissions` | allow everything except the hard safety floor |
| `auto` | a small model judges each call that would otherwise prompt (see below) |

`acceptEdits` covers the session's working folder only (the folder shown in
the Environment card), never:

- a hidden file or folder inside it (`.bashrc`, `.git/…`, `.omnis/…`) —
  sessions often run in your home folder, where those hold your shell setup and
  omnis's own configuration;
- a path that leaves it, including through a symbolic link;
- anything when the working folder is `/`.

Those writes, and `mv`/`cp`/`ln`… with a path outside the folder, a `$VAR`, a
`~` or a wildcard, show the usual card. Set `defaultMode` to `default` to be
asked for every write.

## Choosing a mode per chat

`defaultMode` is only the starting point: every chat can have its own mode,
which applies to the agent and all its sub-agents, survives a restart and is
copied by a fork or a spawned session.

- **Pastille** in the composer, left of the context ring: click it to pick a mode.
- **Shift+Tab** in the composer cycles `default` → `acceptEdits` → `plan` →
  `auto` (→ `bypassPermissions` when allowed). `dontAsk` is in the menu only.
- **`/mode [name]`** shows or sets the mode (`/mode` alone shows it).
- **New chat**: the start picker has a Permission mode row; the choice is used
  when the chat is created.
- **Terminal**: `omnis --permission-mode <mode>`, `/mode <name>`, and Shift+Tab
  in the TUI input field.

In `plan` mode a "Allow all `Edit` this session" grant does not apply: the
agent can read and investigate but not change anything. The agent is told which
mode it is in; in `plan` it presents a plan and asks you to leave plan mode.

### How `auto` decides

Edits in the working folder and common file commands are allowed as in
`acceptEdits`. For anything else that would show a card and that no rule
covers, a small model (the `/goal` evaluator model, else the chat's model)
judges the call against what you asked for. It blocks actions that go beyond
your request, touch production or credentials, delete data outside the working
folder, send data to an unrequested destination, or look driven by text found
in a file, web page or message from another session. A blocked call is refused
with the reason and the agent tries another way. If the judge fails you get the
usual card; after 3 blocks in a row (or 20 in total) the next call is asked to
you as well. Your own `allow`, `ask` and `deny` rules always win over the judge.

### Hooks and modes

Hooks receive the mode as `permission_mode`. A hook that **denies** blocks in
every mode, `bypassPermissions` included. A hook that **asks** follows the mode:
`bypassPermissions` allows it, `auto` has the judge decide, `dontAsk` refuses it,
the other modes show you the card. Note that a guard escalating a repeated
refusal to a question therefore lets that call through in `bypassPermissions`.

### Forbidding `bypassPermissions`

An administrator can set `"disableBypassPermissionsMode": "disable"` under
`permissions` in the **system** `permissions.json` (`/etc/omnis`). It is ignored
in any other layer and cannot be changed from Settings. Bypass then disappears
from the menu, the flag and the API, and a chat saved in bypass runs in `default`.

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
omnis permissions convert -w permissions.json        # upgrade an old omnis file in place
omnis permissions import  -o permissions.json settings.json   # convert a Claude Code settings.json
```

`import` reads a Claude Code `settings.json` (or just its `permissions` block)
and prints any rules it can't map (e.g. `WebFetch(domain:…)`, which has no gated
omnis tool today).

## The `!` shell-escape

The engine governs commands the **agent** runs. When *you* run a command by
prefixing a message with `!`, the `ask` tier is **bypassed** — you already
authorised it. The hard safety floor still applies.

## Skill-contributed permissions

When a skill is loaded, the permission rules it declares (in a `permissions.json`
file next to its `SKILL.md`, same `permissions.{allow,ask,deny}` shape) are
merged in **read-only**. They appear in their own block in the Permissions panel
so you can audit them; edit the skill's `permissions.json` to change them. The
shipped `liteparse` / `pdf` skills use this to pre-allow their `lit` / `pdftotext`
commands — see [Skills](11-skills.md).

## Editing from the panel

**Settings → Permissions** renders the `defaultMode` selector and one editable
list per tier (deny / ask / allow). Each rule is a `Tool(specifier)` string with
an optional reason; complex regex rules are easier to write in the **Raw JSON**
view.

**Plugin guards.** A [Claude Code plugin](23-plugins.md)'s hook may answer
`allow` for a command it has checked. That only skips the *default* prompt for
that single call (it expires after a few minutes): a rule of yours in `ask` still
asks, one in `deny` still blocks, and Plan mode ignores it. Calls between agents
of the same plugin do not prompt either, unless you wrote an `Agent(...)` rule.
