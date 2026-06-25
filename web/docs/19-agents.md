# Agents Settings

The **Settings → Agents** panel is the central place for managing the agent
fleet. Changes are saved to `agents.json` (global list, models, squads,
globals) and to individual files under `registry/agents/<name>/` (per-agent
definitions and instructions).

The panel has five sub-tabs: **Agents**, **Squads**, **Remotes**, **Models**,
and **Global Environment**.

---

## Agents sub-tab

A split view: fleet list on the left, agent detail panel on the right.

### Fleet list

Agents are grouped into two sections:

- **BUILT-IN AGENTS** — shipped with omnis: `omnis`, `leader`,
  `skill_editor`, `helper`, `summariser`, `curator`, `reflector`. Fields
  are read-only where the binary bakes in defaults. The **`omnis`** agent is
  the **router** that runs at the start of every new chat and hands the
  conversation to the best squad (see [Architecture →
  Omnis router](10-architecture.md#omnis-router-default-chat-routing)); it
  is auto-injected when your config doesn't declare it. The **`helper`**
  answers questions about omnis from its bundled documentation (quoting the
  source via `search_docs`) and browses/installs remote registry items.
- **CUSTOM AGENTS** — user-added. All fields are editable; the agent can be
  removed or reordered.

Each row shows a status dot (active / disabled), the agent name, and its
current model reference.

**Adding an agent** — click **+** above the fleet list to create a blank
custom agent.

**Importing a Claude Code agent** — click the **↓** button to paste or
load a `.md` (YAML frontmatter) or `.json` agent definition. The dialog
offers an "Enable in agents.json" checkbox so the imported agent
is wired in immediately on the next hot-reload.

### Agent detail panel

Selecting an agent opens its detail panel. The top bar shows:

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
| `ddg` | Web search via DuckDuckGo. Mutually exclusive with `serpapi`. |
| `serpapi` | Web search via SerpAPI (requires `serpapi_key` in Global Environment). Mutually exclusive with `ddg`. |
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

#### Move / Delete

Custom agents can be reordered with **▲ Move up** / **▼ Move down** (affects
the order in which they appear in squad member lists) or removed with the
**REMOVE** link in the title bar.

---

## Squads sub-tab

A **squad** is a named group `{ leader, members[] }` that a chat session
runs on. Each squad is wired as its own leader + sub-agent tree.

The sub-tab shows a list on the left and a detail panel on the right:

| Field | Description |
|---|---|
| **Name** | Case-insensitive, unique. The `default` squad name is read-only. |
| **Description** | Shown as the tooltip in the New Chat squad picker. |
| **Leader** | Dropdown over enabled agents (excluding `curator` and `reflector`), plus a **`(none — run single agent directly)`** option. Picking `(none)` makes the squad **leaderless**: it must have **exactly one member**, which runs directly with no coordinator. |
| **Members** | Checkbox grid of enabled agents. The current leader is disabled in the grid. `curator` and `reflector` are always excluded (they are process-wide). Leaderless squads switch this to single-select. |

The `default` squad is always present. If `agents.json` doesn't declare one,
the editor synthesises it from all enabled agents the first time the sub-tab
is opened — saving writes it to disk.

Click **Delete squad** (bottom-right) to remove a non-default squad.

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

## Remotes sub-tab

Browse and install agent definitions and squad templates from remote Git
repositories (GitHub, GitLab, Gitea). The left sidebar switches between
**Agents** and **Squads** registry views.

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
**Enable in agents.json** to wire the agent in on the next
hot-reload.

Configure remote registries under **Settings → Skills → Remotes** — registries
marked `kind: agents` or `kind: both` appear here.

---

## Models settings (top-level section)

Models and the providers they connect through have their own settings
section — **Settings → Models**, listed right after **Agents**. The data
lives in `models.json` (separate from `agents.json`) and is picked up by
the same `POST /api/config/reload` flow.

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

## Global Environment sub-tab

Shared settings that apply across the entire agent fleet. Stored at the
top level of `agents.json`.

### CORE DIRECTORIES

| Field | Description |
|---|---|
| `softskills_dir` | Path to the directory where the curator writes distilled soft-skill playbooks. Agents read from here when `load_softskill` is called. |

### OPTIMIZATION

| Toggle | Description |
|---|---|
| `token_optimization` | Enable bash-output filtering to reduce token usage. Filter patterns are read from `.agents/filters/`. |

### RUNTIME CONFIG

| Field | Description |
|---|---|
| `bash_output_filters_dir` | Override the directory containing bash output filter patterns. Default: `.agents/filters/`. |
| `bash_timeout_seconds` | Maximum time in seconds a bash command may run before it is killed. |
| `mcp_config_path` | Override the default `mcp_config.json` path for all agents. |
| `permissions_config_path` | Override the default `permissions.json` path for all agents. |

### EXTERNAL API KEYS

| Field | Description |
|---|---|
| `serpapi_key` | API key for SerpAPI web search. Required when any agent uses the `serpapi` tool. Shown masked. |
