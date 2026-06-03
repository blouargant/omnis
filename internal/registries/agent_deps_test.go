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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			skills, mcp := parseAgentDeps([]byte(tc.raw))
			if !reflect.DeepEqual(skills, tc.wantSkill) {
				t.Errorf("skills = %v, want %v", skills, tc.wantSkill)
			}
			if !reflect.DeepEqual(mcp, tc.wantMCP) {
				t.Errorf("mcp = %v, want %v", mcp, tc.wantMCP)
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
