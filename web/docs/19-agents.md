# Agents Settings

The **Settings → Agents** panel is the central place for managing the agent
fleet. Changes are saved to `config/agents.json` (global list, models, squads,
globals) and to individual files under `registry/agents/<name>/` (per-agent
definitions and instructions).

The panel has five sub-tabs: **Agents**, **Squads**, **Remotes**, **Models**,
and **Global Environment**.

---

## Agents sub-tab

A split view: fleet list on the left, agent detail panel on the right.

### Fleet list

Agents are grouped into two sections:

- **BUILT-IN AGENTS** — shipped with yoke: `leader`, `skill_editor`,
  `registries_crawler`, `summariser`, `curator`. Fields are read-only where
  the binary bakes in defaults.
- **CUSTOM AGENTS** — user-added. All fields are editable; the agent can be
  removed or reordered.

Each row shows a status dot (active / disabled), the agent name, and its
current model reference.

**Adding an agent** — click **+** above the fleet list to create a blank
custom agent.

**Importing a Claude Code agent** — click the **↓** button to paste or
load a `.md` (YAML frontmatter) or `.json` agent definition. The dialog
offers an "Enable in config/agents.json" checkbox so the imported agent
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
picks at creation time. Each squad is wired as its own leader + sub-agent
tree.

The sub-tab shows a list on the left and a detail panel on the right:

| Field | Description |
|---|---|
| **Name** | Case-insensitive, unique. The `default` squad name is read-only. |
| **Description** | Shown as the tooltip in the New Chat squad picker. |
| **Leader** | Dropdown over enabled agents (excluding `curator`). |
| **Members** | Checkbox grid of enabled agents. The current leader is disabled in the grid. `curator` is always excluded (it is process-wide). |

The `default` squad is always present. If `agents.json` doesn't declare one,
the editor synthesises it from all enabled agents the first time the sub-tab
is opened — saving writes it to disk.

Click **Delete squad** (bottom-right) to remove a non-default squad.

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
`$YOKE_HOME/registry/agents/<name>/`. The install dialog offers
**Enable in config/agents.json** to wire the agent in on the next
hot-reload.

Configure remote registries under **Settings → Skills → Remotes** — registries
marked `kind: agents` or `kind: both` appear here.

---

## Models sub-tab

Declares the model profiles that agents reference via `model_ref`. Each
profile is a card with the following fields:

| Field | Description |
|---|---|
| **Provider** | One of `anthropic`, `openai`, `gemini`, `openai_compat`. |
| **Model** | Provider-specific model ID (e.g. `claude-opus-4-7`). Combobox with known IDs fetched from `/api/provider-models`. |
| **Base URL** | API endpoint. Leave blank for the provider default. Resolved as an env-var name first. |
| **API Key** | Credential. Shown masked; eye button to reveal. Resolved as an env-var name first. |
| **Context Length** | Maximum context window for this model (tokens). |
| **Input / Cached Input / Output token price per million** | Cost tracking fields. Optional. |

Click **⊕ Add model** at the end of the card grid to create a new profile.
Profile names are case-insensitive keys referenced by `model_ref` in each
agent's General Settings.

---

## Global Environment sub-tab

Shared settings that apply across the entire agent fleet. Stored at the
top level of `config/agents.json`.

### CORE DIRECTORIES

| Field | Description |
|---|---|
| `softskills_dir` | Path to the directory where the curator writes distilled soft-skill playbooks. Agents read from here when `load_softskill` is called. |

### OPTIMIZATION

| Toggle | Description |
|---|---|
| `token_optimization` | Enable bash-output filtering to reduce token usage. Filter patterns are read from `config/filters/`. |

### RUNTIME CONFIG

| Field | Description |
|---|---|
| `bash_output_filters_dir` | Override the directory containing bash output filter patterns. Default: `config/filters/`. |
| `bash_timeout_seconds` | Maximum time in seconds a bash command may run before it is killed. |
| `mcp_config_path` | Override the default `config/mcp_config.json` path for all agents. |
| `permissions_config_path` | Override the default `config/permissions.json` path for all agents. |

### EXTERNAL API KEYS

| Field | Description |
|---|---|
| `serpapi_key` | API key for SerpAPI web search. Required when any agent uses the `serpapi` tool. Shown masked. |
