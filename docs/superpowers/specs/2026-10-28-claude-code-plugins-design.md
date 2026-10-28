# Omnis — Claude Code plugins from `/etc/agentskills/claude/`

Date: 2026-10-28 · Status: approved in conversation; builder check done and
folded in (§3 delegation, §4 stale sessions, §6 aliases, §7 ask-rule
precedence)

## Intent

iapcli (≥ 0.36) installs a **Claude Code plugin** at
`/etc/agentskills/claude/`: a squad of IA Parc operators (`lead` + 10
specialists), the `iaparc` / `iaparc-docs` skills, and a `PreToolUse` guard hook
(`iapcli agent guard`) that asks before every write and refuses what an agent
must never run. Today omnis uses only the two skills (via
`/etc/agentskills/skills/`); the agents and the guard are not read at all.

IA Parc users must be able to ask omnis, in plain language, things like
*"deploy Qwen from Hugging Face and publish it in the LLM Gateway"*, *"where is
my LLM Gateway budget"*, or *"launch my KFP run"*, and have the iaparc squad do
it with `iapcli`, under the plugin's own safety rules.

omnis's design contract is that the same binary is retargeted by mounting
config, not by changing code. So omnis learns to read **any** Claude Code plugin
placed under `/etc/agentskills/claude/`. iaparc is the first one, not a special
case.

### Use (decided with the user)

- **Who / where**: users of the shared IA Parc omnis server (cookie identity
  mode), where every `iapcli` an agent runs already carries the user's token
  (`IAPCLI_TOKEN`, planted by `fstools.WithShellEnv`). The agent acts with that
  user's rights, no more. A single-user desktop works the same way with the
  local iapcli context.
- **Entry**: the Omnis router sends IA Parc requests to an `iaparc` squad led by
  `lead`, which hands each one to the right specialist (`productions`,
  `gateway`, `kfp`…). A dedicated IA Parc deployment may make it the
  `start_squad`.
- **Safety**: reads run without a confirmation. Writes go through the plugin's
  own flow: `lead` asks the user in plain words (`AskUserQuestion`), then the
  guard shows the exact command (`ask`). That double confirmation is the
  plugin's design (`lead.md`: *"Claude Code asks the user once more before the
  command runs … tell the user to expect that second confirmation"*). omnis adds
  no third one. The guard refuses every write from the read-only agents
  (`lead`, `prod-support`, `reviewer`).
- **Freshness**: omnis reads the plugin **in place**. An iapcli upgrade reaches
  omnis at the next config reload, with no copy under `~/.omnis` to go stale.

## What is in a plugin (as installed by iapcli 0.36.1)

```
/etc/agentskills/claude/
├── .claude-plugin/marketplace.json     # {"name":"iaparc","plugins":[{"name":"iaparc","source":"./iaparc",…}]}
└── iaparc/
    ├── .claude-plugin/plugin.json      # name, version 0.36.0, description, …
    ├── agents/<name>.md                # 11 agents: YAML frontmatter + markdown body
    ├── skills/{iaparc,iaparc-docs}/    # byte-identical to /etc/agentskills/skills/*
    └── hooks/hooks.json                # PreToolUse, matcher Bash|Read|Grep|Glob|Write|Edit|MultiEdit|NotebookEdit → "iapcli agent guard", timeout 10
```

Agent frontmatter uses `name`, `description`, `model` (`haiku` for nine
specialists, `sonnet` for `lead`, `opus` for `reviewer`), `tools` (e.g.
`[Bash, Read, Grep, Glob, Write, Edit]`; `lead` has
`[Bash, Read, Grep, Glob, Agent, AskUserQuestion, SendMessage]`), `maxTurns`,
`color`. No agent declares `skills:`, but the bodies refer to skill `iaparc`
dozens of times. Bodies use `${CLAUDE_PLUGIN_ROOT}/skills/<skill>/…` to locate
skill files. They name each other `iaparc:<agent>` (≈ 350 mentions) and mention
`aiforge:automation` / `aiforge:reviewer`, from a plugin that is not installed.

## Goals

### 1. Discovery

- The plugin root is `<agent-skills dir>/claude/`, so
  `OMNIS_AGENTSKILLS_DIR` relocates it and `OMNIS_AGENTSKILLS_DIR=""` disables
  it, like the rest of that layer.
- Read `.claude-plugin/marketplace.json`. For each `plugins[]` entry with a
  **local relative** `source`, read that directory. Ignore any other source
  (git URL, object) with a log line.
- In each plugin, read `.claude-plugin/plugin.json` (name, version,
  description), `agents/*.md`, `skills/*/SKILL.md` and `hooks/hooks.json`, the
  default Claude Code component locations.
- Re-read on every config resolution, so `POST /api/config/reload` picks up an
  upgrade. omnis never writes there.
- **A malformed plugin, agent or hook is skipped with a log line. It never fails
  config resolution.** A third-party package must not be able to brick omnis.
  This is unlike the fatal `validateSubAgentGraph`.

### 2. Agents → omnis agents

Each `agents/<file>.md` becomes an omnis agent (reuse `internal/claudeformat`
for parsing; its `InstallAgent` writing path is not used):

| Claude Code | omnis |
|---|---|
| `name` | `<plugin>_<name>`, lower-cased, `-`→`_` (`iaparc_prod_support`). Keep the qualified name `<plugin>:<name>` (`iaparc:prod-support`) for hook input (§6). If the omnis name collides with an agent omnis already defines, omnis's agent wins and the plugin agent is skipped with a log line. |
| `description` | verbatim |
| body | `instruction`, with `${CLAUDE_PLUGIN_ROOT}` replaced by the plugin's absolute directory, followed by the translation note (§4) |
| `tools` | `Bash`, `Read`, `Grep`, `Glob`, `Write`, `Edit`, `MultiEdit` → the same omnis tools. `Agent` → delegation through the squad (§3), not a tool key. `AskUserQuestion` → omnis's always-on tool on a squad root (same name). `SendMessage` → dropped (resume, §4). Other names → dropped with a debug log. **`Skill` is always added.** No `tools` field (Claude Code: inherit all) → `Bash, Read, Grep, Glob, Write, Edit, MultiEdit, Skill`. |
| `skills` | the frontmatter list when present, otherwise every skill of the plugin |
| `model` | the alias table (§5) |
| `maxTurns`, `color`, `permissionMode`, frontmatter `hooks`, `memory`, `isolation`, … | ignored in v1 |

Resumable sessions stay on (the omnis default), because §4 relies on them.
Plugin agents are enabled without being listed in `agents.json`.

**Settings**: plugin agents appear in Settings → Agents with a
"plugin: iaparc" source badge and behave like system-layer agents: read-only
until edited, and an edit forks a per-user overlay
(`~/.omnis/registry/agents/<name>/agent.json`) **deep-merged over the converted
plugin agent**, the plugin being the lowest layer. A user's model change
therefore survives an iapcli upgrade, while untouched fields keep following the
plugin.

### 3. One squad per plugin

- An agent whose `tools` include `Agent` is the plugin's **leader**
  (`leader: true`). If several do, prefer the one named `lead`, else the first
  by name, with a log line. If none does, the plugin gets no squad: its agents
  exist and can be added to a squad by hand in Settings.
- Squad name = plugin name (`iaparc`). Members = all the other plugin agents.
  Routable, not hidden.
- Squad description = the **leader agent's description**. It is the
  routing-oriented one (*"Use it for 'réserve des GPU', 'ma prod ne répond
  plus' …"*), and the router's catalogue uses squad descriptions.
- A user overlay entry for the same squad name in `agents.json` deep-merges as
  usual (by `name`), and `start_squad` may name it.
- **Delegation is not prompted.** In omnis, calling a sub-agent is the
  leader's own tool call and goes through the permission gate. The shipped
  `permissions.json` allows built-in sub-agents by exact name
  (`Agent(investigator)`…), but plugin agent names are only known at runtime,
  so as things stand every `lead` → specialist call would raise a card. That
  includes reads and the resume call that carries the user's confirmation.
  Claude Code does not prompt for `Agent` calls, and the plugin's flow assumes
  it doesn't. So a call from a plugin agent to an agent **of the same plugin**
  is allowed by default, at the **lowest precedence**: a user rule naming that
  agent (`Agent(iaparc_admin)` in `deny` or `ask`) still wins. This bypasses
  nothing inside the specialist, whose own tool calls are gated per call as
  usual (guard + permissions). The mechanism is the builder's choice: a
  generated lowest-tier allow rule, or a check in the gate.

### 4. Translation note (appended to every plugin agent's instruction)

Short, generated, English. It tells the agent:

- its sibling agents' names here: `iaparc:access` is `iaparc_access`, and so on,
  as a table built from the plugin's agents;
- *"call an agent with the Agent tool"* means calling that agent's tool;
- *"SendMessage to an agent"* (continue the same agent later) means calling the
  same agent again with `resume_session` set to the `session` handle its
  previous call returned. This is how `lead` sends `confirmed by the user: <exact
  command>` back to the specialist that proposed it, and it must reach the
  **same** specialist session;
- agents of other plugins (`aiforge:automation`, `aiforge:reviewer`, …) are not
  available here: say so to the user rather than try to call them.

Without the note, the leader would call tools that do not exist.

**Known limit: a lost specialist session.** Resumable sessions expire after
30 minutes idle and are lost on reload or restart, while `AskUserQuestion` has
no timeout and survives a restart. A slow answer can therefore resume a handle
that no longer exists. The wrapper then silently starts a fresh specialist
session, and that specialist will run `confirmed by the user: <exact command>`
(its own procedure only requires that phrase). This is accepted in v1: the
command is the exact one the user approved, and the guard still shows it in
its own card before it runs, so nothing runs without the user seeing the
command. What is lost is the specialist's context for its follow-up
verification. Making the fallback visible (a notice in the resumed call's
result) would help but touches every agent; it is optional and left to the
builder.

### 5. Models

A top-level `claude_model_map` in `models.json` maps Claude Code aliases to
catalogue refs. Built-in default: `{"haiku": "hosted", "sonnet": "high",
"opus": "premium"}`. It deep-merges across layers like the rest of the file.
Resolution of a plugin agent's `model`:

1. `inherit` or absent → empty `model_ref` (inherits the leader's model, the
   omnis default);
2. an alias in the map → that ref;
3. a value that is itself a catalogue ref → used as is;
4. anything else, or a ref missing from the catalogue → empty `model_ref`, with
   a log line.

For iaparc this gives nine specialists on `hosted`, `lead` on `high`, and
`reviewer` on `premium`. No dedicated Settings UI in v1 (Raw JSON edits it);
per-agent overrides go through the normal agent editor (§2).

### 6. Plugin hooks (the guard)

- A plugin's `hooks/hooks.json` is added to the hook engine as an extra,
  additive layer **while the plugin is enabled**. It applies **fleet-wide**,
  like a plugin hook in Claude Code: any agent that runs `iapcli` through
  `Bash`, `bash_background` or `monitor` (see the aliases below) is guarded.
  No agent can reach `~/.kube/`, `.iapcli.yaml`, `~/.config/aiforge/` or
  `/etc/iaparc/` through the Claude-named file tools (`Read`, `Grep`, `Glob`,
  `Write`, `Edit`, `MultiEdit`). Verified with the live guard; `kubectl`
  itself is unaffected. omnis-only file tools (`mime`, `revert`, `lsp_*`,
  `ast_grep_*`) are not matched by a Claude Code matcher and stay governed by
  the permission layer only; the plugin's own agents never get them.
- Installing, upgrading or disabling a plugin changes its hooks at the next
  config reload. Note that the engine is memoised on `Infrastructure`
  (`Infrastructure.Hooks`), so this needs explicit handling.
- When running a plugin hook command: expand `${CLAUDE_PLUGIN_ROOT}` in the
  command and export `CLAUDE_PLUGIN_ROOT` in its environment (Claude Code
  semantics). `fail_closed` is not set: Claude Code semantics apply, and the
  guard itself exits 2 (block) on input it cannot read.
- **Hook input compatibility** (the guard was written for Claude Code's input;
  verified against `iapcli agent guard`):
  - **`agent_type`**: when the calling agent is a plugin agent, send its
    qualified name (`"agent_type": "iaparc:prod-support"`). The guard keys its
    read-only rule on this field. With only omnis's `agent_name` it answers
    `ask` instead of `deny` for a write by `prod-support`. Not sent for
    omnis-native agents, so a guard's names (`reviewer`, `lead`) never match
    omnis's own agents. Add it to `hooks.Input` (additive).
  - **`MultiEdit`**: omnis's `MultiEdit` takes `{"files": [{"file_path", "edits"}…]}`,
    while Claude Code's takes `{"file_path", "edits"}`. The guard refuses
    omnis's shape with exit 2 (*"a MultiEdit call with no file path — the call
    is refused"*), which would **block every `MultiEdit` in the fleet** (the
    Coding squad included). For **plugin hooks only**, a `MultiEdit` call runs
    the hook once per file, each time with Claude Code's shape, and aggregates
    the outcomes the way the engine already does (Block > Ask > Allow >
    Proceed). omnis-native hooks keep receiving omnis's shape.
  - **`bash_background` / `monitor`**: omnis-only shell tools, both taking
    `command`, so a Claude Code matcher (`Bash|…`) never sees them. Verified:
    the guard has no opinion on `bash_background` running `iapcli … stop`. For
    **plugin hooks only**, they are matched and presented as `Bash` with
    `{"command": …}`, so an `iapcli` command cannot dodge the guard by running
    in the background. Native agents with the `bg` group (`leader`, `coder`)
    have them; the plugin's agents don't.
  - Every other tool's arguments were checked and are read correctly: `Read`
    (`file_path`, `start_line`/`end_line`), `Grep` (`pattern`, `path`,
    `recursive`), `Glob`, `Write`, `Edit`, and `Bash` (`timeout`). The extra
    omnis fields (`attempt`, `consecutive`, `attestations`, `agent_name`) are
    ignored by the guard.

### 7. Hook decisions and the permission layer (applies to ALL hooks)

Today omnis ignores a hook's `allow` (`hooks.DecisionAllow` is dead code), and
an `ask` the user approves is followed by a second card from the permission
layer. With the guard, that means a card for every iapcli read (no `iapcli`
rule, `defaultMode: default`) and an extra card for every write. New behaviour,
**exactly Claude Code's** (its permissions page, "Precedence among rules,
hooks, and mods": *"PreToolUse hook decisions don't bypass permission rules …
a matching deny rule blocks the call, and a matching ask rule still prompts
even when the hook returned `allow` or `ask`"*):

- hook **`allow`** → the call skips the permission layer's **default** prompt,
  the one an unmatched call gets in `default` mode;
- hook **`ask`**, approved by the user → same: the default prompt is skipped;
- a matching **`ask` rule still prompts** and a matching **`deny` rule still
  blocks**, whatever the hook said. A plugin can therefore never override a
  rule the user wrote, for example a user who adds `Bash(iapcli *)` to the ask
  tier;
- **plan mode**: a hook's `allow` has no effect on a call that isn't read-only
  (same as Claude Code);
- the Bash safety floor still applies;
- **per call only**: never cached into the session approval cache, never
  persisted.

This needs the permission check to tell "an ask rule matched" apart from "no
rule matched, the mode's default is ask". `CheckArgs` does not expose that
today; how to surface it is the builder's choice.

Effect on the shipped Kubernetes hook: `config/hooks/k8s-validate.py` never
emits `allow` (verified: only `deny`, `ask`, or no opinion), and the mutations
it escalates match the shipped `kubectl … apply|delete|…` ask rules, which
still prompt. So in practice nothing changes for it.

Rejected alternative: adding a `Bash(iapcli *)` allow rule while the plugin is
active. Writes would then be protected by the guard alone, and a failing guard
(missing binary, hook error, non-blocking in Claude Code semantics) would let a
write run with no confirmation at all. With the chosen design, a silent guard
does not say `allow`, so the permission card still shows: the default errs on
the safe side.

### 8. Enable / disable

- A plugin is **enabled when present**.
- Disabled per plugin from Settings: a toggle on the plugin's squad (or agent
  group) in Settings → Agents & squads, persisted as `disabled_plugins:
  ["iaparc"]` in `agents.json` (layered like the rest; re-enabling must work
  from the UI). A disabled plugin contributes **no** agents, squad, skills or
  hooks at the next reload.
- `OMNIS_AGENTSKILLS_DIR=""` disables the whole `/etc/agentskills` layer,
  plugins included (existing behaviour, extended).

### 9. Skills

- Each plugin's `skills/` joins the skill search chain **below**
  `<agent-skills dir>/skills`. Names stay bare (`iaparc`): omnis skill names
  cannot contain `:`. On a duplicate name the first wins, so
  `/etc/agentskills/skills/iaparc` is served, which is identical for iaparc.
- Plugin agents get the `Skill` tool plus their plugin's skills (§2). They can
  also Read the plugin's copy through the substituted `${CLAUDE_PLUGIN_ROOT}`
  path their instructions give.

## Non-goals

- Installing, updating or removing plugins. That is iapcli's job; omnis only
  reads what is there. No remote marketplaces, no `claude plugin install`
  semantics.
- Plugin `commands/`, `.mcp.json` / MCP servers, LSP servers, output styles,
  agent-scoped frontmatter hooks, `maxTurns`, custom component paths in
  `plugin.json`. Ignored in v1 with a log line.
- Reading the user's own Claude Code plugins (`~/.claude/plugins`). Only
  `<agent-skills dir>/claude/`.
- Changing iapcli or the guard.
- Claude Code agent teams beyond "SendMessage ⇒ resume the same session".
- A Settings UI for `claude_model_map`.

## Anti-goals (it fails even if it "works")

- A broken or unexpected plugin makes omnis's config fail to load.
- A plugin agent shadows an omnis agent, or is silently shadowed by one.
- Copies of plugin content under `~/.omnis` that go stale after an upgrade.
- **A no-op Settings save persists plugin agents or the plugin squad into the
  user's `agents.json`.** Same family as the squads/`model`/empty-list
  round-trip bugs in CLAUDE.md ("a GET must return the AUTHORED config"). The
  synthesized agents and squad must not ride back through `PUT
  /api/config/parsed/agent`.
- More confirmations than the plugin designed (a third card, a card on a
  `lead` → specialist delegation), or fewer (a write that runs without the
  guard's ask).
- A plugin hook overriding a permission rule the user wrote.
- The guard blocks an omnis tool it cannot parse (`MultiEdit`) across the
  fleet.
- A hook's `allow` overrides a permission deny rule or the Bash safety floor.
- The guard (or any hook) receives the user's IA Parc token. Hooks are
  deliberately not given `WithShellEnv`.

## Constraints

- ADK v2 seams stay in `core/adk`; follow the existing patterns:
  `ensureRouterSquad` for synthesized agents/squads, `loadAgentFromRegistry`'s
  deep-merge for overlays, `hooks.Reloader` layering, the permission gate's
  shared `Callback` for sub-agents.
- The same behaviour on server, TUI and CLI, as far as each surface has the
  pieces. The router and the permission/hook chain are shared.
- No-op contract: with no `<agent-skills dir>/claude/`, omnis behaves
  byte-identically to today, except §7, which changes the hook/permission
  interplay for every hook.
- CLAUDE.md self-maintenance: new section, plus updates to the "Before-tool
  chain order" bullet and the Kubernetes "permission card does NOT carry the
  diff" text (they state that `allow` is dead code), the env/config tables
  (`claude_model_map`, `disabled_plugins`), and the filesystem layout. Add a
  FEATURES.md bullet under the in-development minor and a `web/docs` page.

## Decided by the user

Use cases and deployment; router → `iaparc` squad led by `lead`; reads without
a confirmation and writes confirmed through the guard; generic support for any
plugin under `/etc/agentskills/claude/`; read in place with no copy; one squad
per plugin with the `Agent`-holder as leader; prefixed names; `Skill` and the
plugin's skills for every agent; model alias table haiku→hosted,
sonnet→high, opus→premium, overridable per agent; enabled by default and
disableable in Settings; the guard fleet-wide with `agent_type` sent; Claude
Code semantics for hook `allow` / approved `ask` versus permissions, with deny
rules and the safety floor still winning. (The builder check showed that Claude
Code also keeps explicit `ask` rules prompting and ignores a hook's `allow` in
plan mode; §7 now follows that exactly, as decided.)

## My calls (for the user to check)

- Name scheme `<plugin>_<agent>` with `-`→`_`; on collision, omnis's agent wins.
- Squad description taken from the leader agent, not from `plugin.json`.
- Leader tie-break (`lead` first, then by name); no `Agent` holder ⇒ no squad.
- Default tool set when `tools` is absent; `Skill` always added; unknown tool
  names dropped.
- The `${CLAUDE_PLUGIN_ROOT}` substitution in instructions and hook commands.
- The translation note, especially SendMessage ⇒ `resume_session`.
- `agent_type` sent only for plugin agents.
- The per-file `MultiEdit` split, and `bash_background`/`monitor` presented as
  `Bash`, for plugin hooks only.
- Same-plugin delegations allowed by default at the lowest precedence (§3).
- A lost specialist session falls back to a fresh one; the guard's card is the
  safeguard (§4, known limit).
- Model resolution fallbacks (§5, steps 3–4).
- Skipping malformed items with a log line instead of failing.
- `disabled_plugins` as the persisted form of the toggle.
- `maxTurns` ignored in v1 (the per-turn spend budget still bounds a runaway).

## Left to the builder

- Where synthesis happens (`ResolveRuntimeSettings` versus the build path) and
  how Settings sees plugin agents without persisting them (§2, anti-goals).
- How the hook engine re-evaluates plugin layers on reload despite being
  memoised.
- The per-call signal from the hook callback to the permission gate (§7). It
  must work for the root's runner plugin **and** the sub-agent callback chain,
  must not touch the session approval cache, and needs the gate to tell a
  rule-matched ask from the mode's default ask.
- The mechanism for default-allowed same-plugin delegation (§3).
- Exact Settings placement and wording of the plugin badge and toggle
  (en/fr/es/de).
- The exact wording of the translation note.
- Test layout. Unit tests should use a fixture plugin tree, not
  `/etc/agentskills`.

## Acceptance checks

With iapcli 0.36.x installed (the guard needs no platform; end-to-end runs on
the IA Parc test deployment):

1. Settings shows squad `iaparc` and 11 `iaparc_*` agents with the plugin
   badge; `iaparc_reviewer` and omnis's `reviewer` both exist.
2. The router sends *"où en est mon budget LLM Gateway"* to `iaparc`.
3. A read request goes `lead` → specialist → `iapcli --redact … get` with
   **no** card, the delegation included.
4. A write: `lead` asks with `AskUserQuestion`, the guard shows **one** card,
   and there is **no** permission card after it, nor on the delegation calls.
   The confirmation reaches the same specialist session (resume).
5. `iaparc_prod_support` attempting a write is **denied** by the guard.
6. With the plugin installed, a Coding-squad `MultiEdit` is **not** blocked,
   and a `MultiEdit` touching `~/.kube/config` **is**. A `bash_background`
   running an `iapcli` write raises the guard's card.
7. `disabled_plugins: ["iaparc"]` (via the Settings toggle) or
   `OMNIS_AGENTSKILLS_DIR=""`: no iaparc agents, squad or hook after reload.
8. Editing a file under the plugin, then reloading: the change is visible, and
   nothing was written under `~/.omnis`.
9. A no-op Settings save leaves the user's `agents.json` and registry without
   any plugin agent or squad.
10. A malformed `agents/*.md` is skipped with a log line; the other agents load.
11. Precedence: a `deny` rule beats a hook `allow`; an `ask` rule (e.g. a user's
    `Bash(iapcli *)`, or the shipped `kubectl … apply` rule after an approved
    Kubernetes escalation) still prompts after a hook `allow`/approved `ask`;
    in plan mode a hook `allow` does not let a write through. A user `deny` or
    `ask` rule on `Agent(iaparc_admin)` beats the default-allowed delegation.
    The Kubernetes hook's behaviour is unchanged.
