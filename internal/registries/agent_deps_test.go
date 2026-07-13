package registries

import (
	"reflect"
	"testing"
)

func TestParseAgentDeps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		raw       string
		wantSkill []string
		wantMCP   []string
		wantSubs  []string
	}{
		{
			name:      "snake_case",
			raw:       `{"name":"fluxcd","skills":["a","b"],"mcp_servers":["flux-operator-mcp"]}`,
			wantSkill: []string{"a", "b"},
			wantMCP:   []string{"flux-operator-mcp"},
		},
		{
			name:      "camelCase alias merged",
			raw:       `{"skills":["a"],"mcpServers":["x"],"mcp_servers":["y"]}`,
			wantSkill: []string{"a"},
			wantMCP:   []string{"y", "x"},
		},
		{
			name:      "none",
			raw:       `{"name":"plain"}`,
			wantSkill: nil,
			wantMCP:   nil,
		},
		{
			name:      "claude markdown without deps",
			raw:       "---\nname: x\n---\nbody",
			wantSkill: nil,
			wantMCP:   nil,
		},
		{
			// A Claude-format markdown agent declares its dependencies in YAML
			// frontmatter; the cascade must read them (regression: previously the
			// JSON-only parse failed and silently dropped the deps).
			name:      "claude markdown with deps",
			raw:       "---\nname: fluxcd\nskills: [gitops-knowledge, gitops-repo-audit]\nmcpServers: [flux-operator-mcp]\n---\nbody",
			wantSkill: []string{"gitops-knowledge", "gitops-repo-audit"},
			wantMCP:   []string{"flux-operator-mcp"},
		},
		{
			// The agent's own delegable team. This is the one dependency whose
			// absence is FATAL rather than degrading — a dangling `subagents` edge
			// makes the whole runtime config fail to resolve — so the cascade must
			// see it.
			name:     "subagents",
			raw:      `{"name":"research_critic","subagents":["web_fetcher"]}`,
			wantSubs: []string{"web_fetcher"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skills, mcp, subs := parseAgentDeps([]byte(tc.raw))
			if !reflect.DeepEqual(skills, tc.wantSkill) {
				t.Errorf("skills = %v, want %v", skills, tc.wantSkill)
			}
			if !reflect.DeepEqual(mcp, tc.wantMCP) {
				t.Errorf("mcp = %v, want %v", mcp, tc.wantMCP)
			}
			if !reflect.DeepEqual(subs, tc.wantSubs) {
				t.Errorf("subagents = %v, want %v", subs, tc.wantSubs)
			}
		})
	}
}

func TestRequestReloadNilHook(t *testing.T) {
	t.Parallel()
	var d Deps // RequestReload nil
	if d.requestReload() {
		t.Error("requestReload() with nil hook = true, want false")
	}
	fired := false
	d.RequestReload = func() bool { fired = true; return true }
	if !d.requestReload() || !fired {
		t.Error("requestReload() did not invoke the wired hook")
	}
}
