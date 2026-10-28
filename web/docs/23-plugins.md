# Claude Code Plugins

omnis can run **Claude Code plugins** — the bundles of agents, skills and hooks
that Claude Code users install. A tool such as `iapcli` installs a plugin under
`/etc/agentskills/claude/`; omnis finds it at startup and on every
[reload](14-config.md), with nothing to configure.

## What gets loaded

For every installed plugin omnis loads:

- **Agents** — each becomes an ordinary omnis agent named
  `<plugin>_<agent>` (for example `iaparc_prod_support`). They appear in
  **Settings → Agents** under the plugin's name, with a `plugin` badge.
- **Skills** — available to the plugin's agents like any other skill.
- **Hooks** — the plugin's guard scripts run as [lifecycle hooks](22-hooks.md),
  the way Claude Code runs them.

Not loaded: plugin slash commands, MCP and LSP declarations, output styles and
per-agent hooks written inside an agent file.

## The plugin squad

If one of the plugin's agents can delegate to the others (it holds Claude's
`Agent` tool), omnis creates a **squad** named after the plugin with that agent
as its lead and the rest as specialists. Pick it like any other squad, or let
the Omnis router send matching requests to it. A plugin whose agents cannot
delegate still adds its agents, just without a squad.

## Confirmations

Plugin agents are written to ask before doing anything that changes things, and
omnis keeps both safeguards:

- the **lead asks you** in the conversation before handing the action to a
  specialist;
- the plugin's **guard** then shows its own confirmation card with the exact
  command before it runs.

Calls between agents of the same plugin do not raise a permission card.
Your own [permission rules](13-permissions.md) always win.

If a specialist's session has expired (after long idle time, a reload or a
restart), the lead may start a fresh specialist and tell it the action was
confirmed. The guard's card still shows the exact command first, so nothing runs
without you seeing it.

## Model mapping

Claude agents name a model by alias. omnis maps the aliases to its own
catalogue:

| Claude alias | omnis model |
|---|---|
| `haiku` | `hosted` |
| `sonnet` | `high` |
| `opus` | `premium` |

An agent with no model (or `inherit`) uses its lead's model. To change the
mapping, add a `claude_model_map` object to `models.json`, for example
`{"claude_model_map": {"sonnet": "balanced"}}`; entries you leave out keep their
default. You can also set a plugin agent's model directly in Settings.

## Turning a plugin off

**Settings → Agents → Plugins** lists the installed plugins with a toggle. Turning
one off removes its agents, squad, skills and hooks after a reload; turning it
back on restores them, with your edits to its agents intact. If a reload error
occurs, Settings shows it next to the toggle. If the Agents form has unsaved
edits, Settings asks you to confirm first, since the toggle discards them.

## Editing a plugin agent

Plugin agents are editable like any other agent: change the model, the
instruction, the tools or the description. Your changes are stored as your own
overrides, so untouched fields keep following the plugin when it is updated.
Plugin agents cannot be deleted — turn the plugin off instead.
