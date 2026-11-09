package agent

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	mcpcfg "github.com/blouargant/omnis/internal/mcp"
)

type namedToolset struct{ name string }

func (n namedToolset) Name() string                                     { return n.name }
func (n namedToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) { return nil, nil }

// A read-only agent mounts only MCP servers the operator declared read_only.
func TestReadOnlyAgentMountsOnlyReadOnlyMCPServers(t *testing.T) {
	handles := []*mcpcfg.Handle{
		{Name: "github", Toolset: namedToolset{"github"}},
		{Name: "docs", Toolset: namedToolset{"docs"}, ReadOnly: true},
	}
	names := func(cfg RuntimeAgentConfig) map[string]bool {
		_, ts, _, _ := toolsForAgentConfig(context.Background(), cfg, RuntimeSettings{}, nil, handles, nil, nil, nil, nil, nil, nil, false, nil)
		out := map[string]bool{}
		for _, s := range ts {
			out[s.Name()] = true
		}
		return out
	}
	base := RuntimeAgentConfig{Name: "investigator", Tools: []string{"mcp"}, MCPServers: []string{"github", "docs"}}
	if got := names(base); !got["github"] || !got["docs"] {
		t.Fatalf("a normal agent must mount both: %v", got)
	}
	base.ReadOnly = true
	if got := names(base); got["github"] || !got["docs"] {
		t.Fatalf("a read-only agent must mount only the read_only server: %v", got)
	}
}
