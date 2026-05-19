package mcp

import "strings"

// loaderProtocolTemplate is injected into the system prompt of any agent
// that has at least one MCP server mounted. `{{servers}}` is replaced with
// the comma-separated names of the servers that are actually attached to
// the agent (i.e. after the per-agent `mcp_servers` whitelist has been
// applied), so the model never advertises a server that isn't reachable.
//
// The rule is intentionally framed as a tool-selection preference rather
// than an absolute prohibition: shell commands are still the correct
// choice when no MCP server covers the task (build scripts, local
// processes, etc.). The protocol focuses the model on "is one of the
// mounted servers the right tool for this domain?" before reaching for
// bash.
const loaderProtocolTemplate = `MCP TOOL PREFERENCE — when a task touches the domain of a mounted MCP server, use that server's tools instead of running shell commands.
- Mounted MCP servers: {{servers}}.
- Before any bash call, ask: does the name or domain of one of these servers cover this task? If yes, call that server's tools first.
- Examples of matching: a "github" server covers repos / issues / PRs / code search (never use 'gh' or 'git' via bash for those); a "kubernetes" server covers pods / namespaces / logs / triage (never 'kubectl'); a "postgres" server covers queries / schema introspection (never 'psql'); a "filesystem" server covers find / grep / read when mounted.
- Fall back to bash only after confirming no mounted MCP server covers the domain (build scripts, local-only processes, or work wholly unrelated to any mounted server).
- Reaching for bash when a mounted MCP server is available is a tool-selection violation. Pick the MCP tool first.
`

// BuildLoaderProtocol returns the dynamic MCP-preference instruction for
// an agent whose mounted MCP servers are the given names. Returns the
// empty string when no servers are mounted, so callers can unconditionally
// concatenate the result without producing a dangling header.
func BuildLoaderProtocol(serverNames []string) string {
	if len(serverNames) == 0 {
		return ""
	}
	return strings.Replace(loaderProtocolTemplate, "{{servers}}", strings.Join(serverNames, ", "), 1)
}
