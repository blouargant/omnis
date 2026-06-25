package permissions

import "strings"

// mcpMatch reports whether an mcp spec matches a omnis MCP tool name. Forms:
//
//	mcp__server            → any tool from that server (mcp__server__*)
//	mcp__server__*         → same (explicit wildcard)
//	mcp__server__tool      → that exact tool
func mcpMatch(specArg, omnisTool string) bool {
	specArg = strings.TrimSpace(specArg)
	if specArg == "" {
		return false
	}
	if strings.HasSuffix(specArg, "__*") {
		prefix := strings.TrimSuffix(specArg, "*") // keep trailing "__"
		return strings.HasPrefix(omnisTool, prefix)
	}
	if specArg == omnisTool {
		return true
	}
	// Server-only form: matches every tool under that server.
	return strings.HasPrefix(omnisTool, specArg+"__")
}

// agentMatch reports whether an Agent(Name) spec matches a sub-agent tool name.
func agentMatch(specArg, omnisTool string) bool {
	return strings.EqualFold(strings.TrimSpace(specArg), omnisTool)
}

// domainMatch reports whether a WebFetch domain spec matches a URL's host.
// omnis has no built-in WebFetch tool today (web fetch lives in the web_agent
// sub-agent), so this is dormant — kept so the syntax parses and is ready if
// a gated fetch tool is added.
func domainMatch(specArg, url string) bool {
	const p = "domain:"
	if !strings.HasPrefix(specArg, p) {
		return false
	}
	want := strings.ToLower(strings.TrimPrefix(specArg, p))
	host := strings.ToLower(hostOf(url))
	return host == want || strings.HasSuffix(host, "."+want)
}

func hostOf(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '@'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return s
}
