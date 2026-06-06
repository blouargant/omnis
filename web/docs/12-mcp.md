# MCP Servers

The **Model Context Protocol** (MCP) is an open standard for exposing tools
to LLM agents over a subprocess or remote transport. Yoke can mount any MCP
server alongside its built-in tools — the agent treats them as ordinary
function calls.

## Configuring servers

Servers are defined in `mcp_config.json`:

```json
{
  "servers": [
    {
      "name": "github",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "GITHUB_TOKEN" }
    }
  ]
}
```

Each `env` value is **resolved as an environment-variable name first**: if the
named env var exists and is non-empty, its value is used; otherwise the literal
string is passed through. This lets you keep secrets out of the config file.

## Inputs and `ASK_USER`

For interactive secrets — values you would rather type once per session than
export globally — declare an **input** in the MCP config and reference it with
the literal sentinel `ASK_USER` in an `env` slot. At call time the agent emits
an `ask_user` event; the Web UI surfaces a prompt in the chat, caches the
answer for the rest of the session, and coalesces concurrent requests for the
same input. The answer is never persisted to disk.

## Subprocess pool

MCP servers are deduplicated by `(command, args, env)` hash in
`internal/mcp/pool.go`. Two agent generations that mount the same server
share one child process. A configuration reload that only changes one server
restarts just that server.

The pool exposes a generation refcount so the server-status UI can report
which servers are draining on the previous generation.

## Importing from other tools

The MCP panel accepts JSON snippets from other MCP catalogues (Claude Desktop,
Cursor, etc.). When you paste:

- Duplicates are detected by `name`.
- For each duplicate you choose **Merge** (keep both), **Replace**, or **Skip**.
- Inputs defined in the snippet are imported into the **Inputs** section, with
  the same conflict resolution.

## Tool naming

The agent sees MCP tools under their server-qualified name (e.g.
`mcp__github__list_repos`). The **Permissions** panel uses the same naming, so
you can scope rules per-server (`mcp__github`), per-tool
(`mcp__github__list_repos`), or across a server's traffic (`mcp__github__*`) in
any of the `allow` / `ask` / `deny` tiers.
