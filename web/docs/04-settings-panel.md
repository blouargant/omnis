# Settings Panel

Click the gear icon at the bottom of the sidebar to open Settings. The panel
replaces the chat surface; the chat resumes when you close Settings.

## Sections

The sidebar lists the available sections. Each maps either to a JSON config
file on disk or to a client-only view:

| Section        | Backing file                  | Purpose |
|---|---|---|
| **Skills**     | `registry/skills/`             | Manage authored playbooks the agent can load on demand. |
| **Agents**     | `config/agents.json` + `registry/agents/` | Roles, model profiles, tool wiring, global env. Per-agent details live in `registry/agents/<name>/`. |
| **Permissions**| `config/permissions.json`     | What the agent may run without asking. |
| **MCP**        | `config/mcp_config.json`      | External tool servers (Model Context Protocol). |
| **A2A**        | `config/a2a_config.json`      | Remote A2A agent endpoints; each entry becomes an `a2a_<name>` tool on the leader. |
| **Commands**   | (client only)                 | Custom slash command templates that expand to a prompt. |
| **Appearance** | (client only)                 | Theme picker. |
| **Documentation** | (client only)              | This page. |

Below the section list a **Raw JSON** entry switches the active JSON section
into a textarea editor. The Form ↔ Raw toggle preserves unsaved edits across
visits within the same session.

## Save flow

JSON sections show a footer with **Discard** and **Save**. Saving runs a
server-side validation pass; on success a non-intrusive banner offers to
**Reload** the agent (no downtime) or **Restart server** (for env/binary
changes).

If you save the same file twice from different tabs, the second save will
fail with a stale-mtime error — reload the panel and retry.

## Editing Agents

The Agents section has four sub-tabs:

- **Agents** — the fleet (leader + sub-agents). The list is split into
  two sections: **Built-in Agents** (shipped with yoke: `leader`,
  `skill_editor`, `registries_crawler`, `summariser`, `curator`) and
  **Custom Agents** (user-added). Pick the agents the leader can call,
  set per-agent system instructions, tool sets, model references, and
  skill links. Each agent's `agent.json` and `instruction.md` live in
  `registry/agents/<name>/`.
- **Squads** — named groups composed from the Agents list (see below).
- **Models** — declare the model profiles referenced by the fleet (provider,
  model ID, base URL, API-key env var, temperature, max-tokens, etc.).
- **Global Environment** — values shared across the fleet: tracing hints,
  search-API keys, etc.

The defaults are baked into the binary; the form highlights any field that
diverges from the built-in baseline.

### Squads sub-tab

A **squad** is a named group `{ leader, members[] }` that a chat session
picks at creation time. Each squad is wired as its own leader +
sub-agent tree; selecting a different squad in the New Chat picker
gives the user a different set of delegable sub-agents while reusing
the same shared agent definitions.

The sub-tab shows a list of squads on the left and a structured detail
panel on the right:

- **Name** — case-insensitive, unique within the file. The `default`
  squad is always present and its name is read-only.
- **Description** — surfaced as the picker tooltip.
- **Leader** — dropdown over the enabled agents (excluding `curator`).
- **Members** — checkbox grid over the enabled agents. The squad's
  current leader is disabled in the grid (a squad can never list its
  own leader as a member). Curator is excluded — it stays
  process-wide.
- **Delete squad** — bottom-right; the `default` squad cannot be
  deleted.

The editor always shows a `default` squad. If your `agent.json` doesn't
declare one, the editor synthesises one from the enabled agents the
first time you open the sub-tab — saving writes it to disk so the
agent.json round-trips cleanly. Saving any change triggers the standard
**Reload** banner.

## Editing Permissions

Permissions are evaluated in three tiers:

1. **always_deny** — the action is rejected without prompting.
2. **always_allow** — the action runs silently.
3. **ask_user** — a confirmation prompt appears in the chat (handled by the
   `ask_user` registry on the server).

A rule matches by tool name plus an optional regex over the call payload.
Skill-contributed permissions appear in a read-only block — they are owned by
the skill file and cannot be edited from this panel.

## Editing MCP

The MCP section lists every external server defined in `mcp_config.json` along
with its command, args, env vars, and resolved tool list (fetched live).

- **Inputs** — declare named secrets the server needs (e.g. an API key). At
  call time the agent emits the `ASK_USER` sentinel; the Web UI prompts you,
  caches the answer for the rest of the session, and coalesces concurrent
  requests for the same input.
- **Import / Export** — paste a snippet from another tool's MCP catalogue;
  duplicates are detected and you choose merge / replace / skip per server.

The MCP subprocess pool deduplicates by `(command, args, env)` hash: two
agent generations that mount the same server share a single child process.
