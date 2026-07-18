# Agents Settings

The **Settings → Agents** panel is the central place for managing the agent
fleet. Changes are saved to `agents.json` (global list, models, squads,
globals) and to individual files under `registry/agents/<name>/` (per-agent
definitions and instructions).

Settings → Agents opens directly onto the **Fleet** — a single tree view of
the whole agent topology. It is the only Agents/Squads editor in the Web UI;
there is no separate Agents/Squads/Remotes sub-tab strip to switch between.
**Models** and **Global configuration** remain reachable from the same screen
as header peers (see below).

### Fleet

The Fleet renders the whole agent tree in one place — router → squads →
leader → members → nested sub-agents — with an **Unused agents** section for
agents no squad references, and a `⌂N` badge on any agent shared by more than
one mount point.

The Fleet is **editable**: select a squad or agent in the tree to open its
editor on the right. Editing a squad covers its name, description, leader,
members, and the hidden flag; editing an agent covers its identity, model,
tools, team (sub-agents), skills, MCP servers, A2A peers, instruction, and the
parallelism/session settings. A **Save / Discard** bar appears once you make a
change; **Save** writes the change and hot-reloads the fleet so it takes
effect immediately, **Discard** reverts to the last-saved state.

Beneath the tree, a collapsible **Delegation map** strip shows the same
topology as a small, read-only diagram (router/squad/leader/sub-agent nodes
connected by delegation edges); it stays collapsed until you open it and
repaints whenever the tree changes.

A header above the tree carries four peers — **Fleet · Models · Registry ·
Global** — plus a `⟳` reload button. Fleet is where you already are; clicking
**Models**, **Registry**, or **Global** opens that panel as a slide-over on
top of the fleet instead of navigating away, so you rarely have to leave the
tree to make a related change. **Global** edits the same draft as the fleet
tree, so they're picked up by the fleet's own Save/Discard bar; **Models**
carries its own Save button (it edits `models.json`, a separate file, and
still shows the embedder-restart notice when that applies); **Registry**
mounts the same registries hub you'd get from Settings → Registries, and
closing it re-syncs the fleet tree in case an install changed an agent. In
the agent editor, the Model Reference field has a small **`↗`** button that
jumps straight to the Models slide-over.

Opening **Models** or **Registries** requires the Fleet draft to be saved or
discarded first — both reload the underlying config when you save/install,
which would otherwise silently drop any unsaved composition edits still
sitting in the tree. **Global** shares the same draft as the Fleet tree, so
it stays available while dirty. Installing an agent or squad from the
**Registries** slide-over repaints the Fleet tree in place — the slide-over
stays open and you are not ejected to a different view.

### Editing the Fleet

The tree supports creating, reorganising, and removing nodes directly:

- **`＋` create** — the button above the tree opens a menu: **New squad…**
  and **New agent…** each prompt for a name, select the new node, and open
  the Save/Discard bar; **Import agent from file…** opens a dialog to
  paste or load a Claude Code-style `.md` (YAML frontmatter) or `.json`
  agent definition, with an "Enable in agents.json" checkbox to wire it in
  on the next hot-reload.
- **Node menus** — right-click a node, or use its **`⋯`** button, to open a
  context menu. A squad offers **Add member…** / **Set member…** (leaderless
  squads), **Set/Make leader…**, **Duplicate**, and **Delete squad** (hidden
  for the default squad). An agent offers **Add to team…** (its `subagents`,
  excluding anything that would create a cycle), **Add to squad…**, **Make
  leader** (when it's a member of a real, non-leaderless squad), **Enable** /
  **Disable** (disabled when the agent is the squad's leader), **Duplicate**,
  **Remove from squad**, and **Delete agent** (Delete is hidden for built-in agents).
- **Drag-and-drop** — drag a member within its squad to reorder it; drag a
  member or an unused agent onto a different squad to add it there as a
  **shared reference** (the agent stays in its original squad too — this is
  not a move). The router is never a drag source or a drop target.

Every change here goes through the same Save/Discard bar as the rest of the
Fleet editor.

---

## Agent editor

Selecting an agent node in the **Fleet** tree — a squad's leader or member,
or an entry under **Unused agents** — opens its editor on the right.

### Agent detail panel

The editor's top bar shows:

- **Agent Display Name** — the name used to reference this agent everywhere.
  Locked to `leader` for the leader agent.
- **Active State** toggle — disabled agents are excluded from squads and
  not offered as delegable tools. The leader cannot be disabled.
- **REMOVE** link — removes the agent from the runtime config (custom agents
  only). The agent's files under `registry/agents/<name>/` are not deleted.

The detail panel is organised into sections:

#### General Settings

| Field | Description |
|---|---|
| **Agent Display Name** | Unique name identifying this agent in config and tool calls. |
| **Model Reference** | Which model profile from the Models sub-tab this agent uses. Dropdown over the profiles declared in the same `agents.json`. |

#### Available Tools

Toggle grid of all tool groups. Active tools appear first. Toggling a tool
adds or removes it from the agent's `tools` list in `agent.json`.

| Tool | Description |
|---|---|
| `Bash` | Run shell commands in the working directory. |
| `Read` | File read. |
| `Write` | File write. |
| `Edit` | Inline file edit (replaces specific strings). |
| `Grep` | Grep across files. |
| `Glob` | List files matching a glob pattern. |
| `revert` | Revert a file to its last committed state. |
| `mime` | Detect MIME type of a file. |
| `mcp` | Mount configured MCP servers as tools. |
| `Skill` | Load authored skill playbooks (`load_skill`, `list_skills`). |
| `softskills` | Load curator-distilled soft skills (`load_softskill`, `list_softskills`). |
| `calc` | Math / expression evaluator. |
| `ddg` | Web search via DuckDuckGo (no API key). Mutually exclusive with `serper` and `serpapi`. |
| `serper` | Web search via Serper.dev (Google) — the recommended, cheaper provider (requires `serper_key` in Global Environment). Mutually exclusive with `ddg` and `serpapi`. |
| `serpapi` | Web search via SerpAPI (requires `serpapi_key` in Global Environment). Mutually exclusive with `ddg` and `serper`. |
| `web` | Browser tool (fetch and parse web pages). |
| `registries` | Browse and install skills and agents from remote registries. |
| `code_search` | Semantic code search (`search_code`, `reindex_code`). Mounted only when an embedding model is configured; otherwise falls back to grep/read. |

Two feature toggles also appear in the grid:

| Toggle | Description |
|---|---|
| `leader` | Marks this agent as eligible to lead a squad. The canonical `leader` agent has this locked on. |
| `files` | Allow file attachments from the Web UI composer for this agent. |

#### Skills

Visible and editable only when the `Skill` tool is active. Lists all skills
the agent has access to. Toggle individual skills on/off; click
**Manage in Skills →** to jump to the Skills panel.

#### MCP Servers

Visible and editable only when the `mcp` tool is active. Checkbox grid of
configured MCP servers. Click **Manage in MCP →** to jump to the MCP panel.

#### A2A Agents

Checkbox grid of configured A2A peers. Only peers enabled here appear as
`a2a_<name>` tools on this agent. Click **Manage in A2A →** to jump to
the A2A panel.

#### Instruction Set

| Field | Description |
|---|---|
| **Public Description** | One-sentence description surfaced in the tool catalogue that the leader sees when deciding whether to delegate. Read-only for built-in agents. |
| **System Instructions** | Full system prompt for this agent. Stored in `registry/agents/<name>/instruction.md`. A token-usage estimate is shown in real time. Read-only for built-in agents (the binary's baked-in instruction is displayed). |

#### Advanced path overrides

Collapsible section. Override per-agent paths that otherwise fall back to
the global values:

| Field | Description |
|---|---|
| `softskills_dir` | Path to a custom soft-skills directory for this agent. |
| `mcp_config_path` | Path to a custom `mcp_config.json` for this agent. |
| `permissions_config_path` | Path to a custom `permissions.json` for this agent. |

#### Reorder, enable, and remove

There are no Move up/down buttons — reordering a member within a squad is
drag-and-drop on the Fleet tree (see "Drag-and-drop" under [Editing the
Fleet](#editing-the-fleet) above). **Enable** / **Disable**, **Duplicate**,
**Remove from squad**, and **Delete agent** (hidden for built-in agents) are
all on the agent node's context menu — right-click it, or use its **`⋯`**
button (or the **REMOVE** link in the editor's title bar).

---

## Squad editor

A **squad** is a named group `{ leader, members[] }` that a chat session
runs on. Each squad is wired as its own leader + sub-agent tree.

Selecting a squad node in the **Fleet** tree opens its editor on the right:

| Field | Description |
|---|---|
| **Name** | Case-insensitive, unique. The `default` squad name is read-only. |
| **Description** | Shown as the tooltip in the New Chat squad picker. |
| **Leader** | Dropdown over enabled agents (excluding `curator` and `reflector`), plus a **`(none — run single agent directly)`** option. Picking `(none)` makes the squad **leaderless**: it must have **exactly one member**, which runs directly with no coordinator. |
| **Members** | Checkbox grid of enabled agents. The current leader is disabled in the grid. `curator` and `reflector` are always excluded (they are process-wide). Leaderless squads switch this to single-select. |

The `default` squad is always present. If `agents.json` doesn't declare one,
the editor synthesises it from all enabled agents the first time the Fleet
is opened — saving writes it to disk.

Remove a non-default squad from its node's context menu (**Delete squad**,
hidden for the default squad — see [Editing the Fleet](#editing-the-fleet)
above).

### The Omnis router squad

By default every new chat starts on the **Omnis router** — a leaderless
squad (single member: the `omnis` agent) that routes each request to the
best-suited squad and hands over control (see [Architecture →
Omnis router](10-architecture.md#omnis-router-default-chat-routing)). It is
**not** offered in the New Chat squad picker (it is the entry point, not a
destination) and is injected automatically when your config doesn't declare
it. To which squad new chats route is decided by the model at runtime; to
**disable** routing entirely, set the top-level `router_squad` to `"none"`
in `agents.json` (or `OMNIS_ROUTER_SQUAD=none`) — new chats then start on the
`default` squad.

---

## Installing remote agents and squads

Browsing and installing agent definitions and squad templates from remote
Git repositories (GitHub, GitLab, Gitea) is no longer a sub-tab under
Agents. It lives in the top-level **Settings → Registries** section (a
consolidated hub covering every kind — Skills, Agents, Squads, MCP, A2A,
Commands, Permissions), and is also reachable without leaving the tree via
the Fleet's **Registry** header peer / slide-over (see [Fleet](#fleet)
above).

Remote agent repositories follow this layout:

```
repo/path/
├── leader/
│   ├── agent.json
│   └── instruction.md   (optional)
└── investigator/
    └── agent.json
```

Installing an agent downloads all files in the matched directory to
`$OMNIS_HOME/registry/agents/<name>/`. The install dialog offers
**Enable in agents.json** to wire the agent in on the next hot-reload.
Installing from the Fleet's Registry slide-over repaints the Fleet tree in
place, so a newly-installed agent or squad shows up immediately without
leaving the tree.

Configure remote registries in **Settings → Registries** (or any of the
per-kind Remotes tabs elsewhere in Settings, e.g. Skills → Remotes) — tick
**Agents** and/or **Squads** under a registry's **Content types** for it to
appear in these browse views.

---

## Models settings (top-level section)

Models and the providers they connect through have their own settings
section — **Settings → Models**, listed right after **Agents**. The data
lives in `models.json` (separate from `agents.json`) and is picked up by
the same `POST /api/config/reload` flow. It's also reachable without
leaving the Fleet tree, via its **Models** header peer / slide-over (see
[Fleet](#fleet) above).

### Providers sub-tab

A **provider** groups credentials and an endpoint so multiple models can
share them. Each provider card has:

| Field | Description |
|---|---|
| **Kind** | One of `anthropic`, `openai`, `gemini`, `openai_compat`. Picks the upstream API shape. |
| **Base URL** | API endpoint. Resolved as an env-var name first (the literal `OPENAI_BASE_URL` becomes the value of the `OPENAI_BASE_URL` env var when one is set). |
| **API Key** | Credential. Shown masked; eye button to reveal. Resolved as an env-var name first. |

A **⟳ Test** button on each card calls
`GET /api/providers/models?provider=<kind>&…` to confirm the credentials
reach the upstream API.

### Models sub-tab

Each model is a card that references a provider via `provider_ref`. Adding
a model requires at least one provider to exist.

| Field | Description |
|---|---|
| **Provider** | Dropdown sourced from the Providers sub-tab. Inherits `kind` (as `provider`), `base_url`, `api_key`. |
| **Model** | Provider-specific model ID. The ⟳ button calls `/api/providers/models?provider_ref=<name>` — credentials never cross the wire — and lists the available models. Picking one prefills any `context_length` / pricing the provider returns. |
| **Context Length** | Maximum context window for this model (tokens). |
| **Input / Cached Input / Output token price per million** | Cost-tracking fields. Optional. |

Profile names are case-insensitive keys; agents reference them via
`model_ref` in their General Settings.

---

## Global configuration

Shared settings that apply across the entire agent fleet, stored at the top
level of `agents.json`. Reached via the Fleet's **Global** header peer (see
[Fleet](#fleet) above), which opens as a slide-over on top of the tree —
edits there are folded into the same draft as the Fleet tree and saved by
its own Save/Discard bar (there is no separate Save button for Global).

### OPTIMIZATION

| Toggle | Description |
|---|---|
| `token_optimization` | Enable bash-output filtering to reduce token usage. Filter patterns are read from `.agents/filters/`. |

### EXTERNAL API KEYS

Each key has a **Test** button that verifies access against the provider with one
minimal search (the key is checked on the server, never in the browser), reporting
"Access OK" or the provider's error.

| Field | Description |
|---|---|
| `serper_key` | API key for [Serper.dev](https://serper.dev/) web search — the recommended, cheaper provider. Required when any agent uses the `serper` tool. Resolved as an environment-variable name first (falls back to the literal value). Shown masked. |
| `serpapi_key` | API key for SerpAPI web search. Required when any agent uses the `serpapi` tool. Resolved as an environment-variable name first. Shown masked. |

### CORE DIRECTORIES

| Field | Description |
|---|---|
| `softskills_dir` | Path to the directory where the curator writes distilled soft-skill playbooks. Agents read from here when `load_softskill` is called. |

### RUNTIME CONFIG

| Field | Description |
|---|---|
| `bash_output_filters_dir` | Override the directory containing bash output filter patterns. Default: `.agents/filters/`. |
| `bash_timeout_seconds` | Maximum time in seconds a bash command may run before it is killed. |
| `mcp_config_path` | Override the default `mcp_config.json` path for all agents. |
| `permissions_config_path` | Override the default `permissions.json` path for all agents. |
