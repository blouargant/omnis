package agent

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/adk/tool"
)

// webSearchTools filters a tool slice down to the ones named "WebSearch" —
// exactly what a caller building an ADK agent sees, no internals.
func webSearchTools(tools []tool.Tool) []tool.Tool {
	var out []tool.Tool
	for _, t := range tools {
		if t.Name() == "WebSearch" {
			out = append(out, t)
		}
	}
	return out
}

// TestToolsForAgentConfig_WebSearchProviderPrecedence locks in the fix for the
// shipped web_agent (registry/agents/web_agent/agent.json declares BOTH the
// "serper" and "ddg" tool groups: ["Skill","serper","ddg","web","softskills"]).
// core/tools/serper.go and core/tools/ddg.go both register a tool named
// "WebSearch"; ADK rejects two tools sharing a name
// (internal/toolinternal/toolutils/toolutils.go: "duplicate tool: %q"). Today
// this only "works" because NewSerperTools returns nil on an empty key — the
// moment a serper_key is configured, web_agent's tool list carries two
// "WebSearch" entries. An agent declaring both providers means "prefer Serper
// when configured, otherwise fall back to DuckDuckGo" — exactly one WebSearch
// tool must ever be built, chosen by that precedence.
func TestToolsForAgentConfig_WebSearchProviderPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		serperKey  string
		wantSubstr string // substring expected in the resolved tool's Description()
	}{
		{
			name:       "serper key configured prefers Serper",
			serperKey:  "test-serper-key",
			wantSubstr: "Serper.dev",
		},
		{
			name:       "no key falls back to DuckDuckGo",
			serperKey:  "",
			wantSubstr: "DuckDuckGo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := RuntimeAgentConfig{
				Name:  "web_agent",
				Tools: []string{"serper", "ddg"}, // shipped web_agent declares both
			}
			runtime := RuntimeSettings{SerperKey: tc.serperKey}

			tools, _, _, _ := toolsForAgentConfig(
				context.Background(), cfg, runtime,
				nil, nil, // skillTS, softSkillTS
				nil, nil, // leaderMCPHandles, pool
				nil, nil, nil, nil, // codeIdx, regIdx, docIdx, sessIdx
				true, // asLeader
				nil,  // emb
			)

			ws := webSearchTools(tools)
			if len(ws) != 1 {
				descs := make([]string, len(ws))
				for i, wt := range ws {
					descs[i] = wt.Description()
				}
				t.Fatalf("want exactly 1 WebSearch tool for an agent declaring both "+
					"\"serper\" and \"ddg\", got %d (descriptions: %v) — ADK rejects "+
					"duplicate tool names, so building this agent for real would fail "+
					"the moment serper_key is configured", len(ws), descs)
			}
			if !strings.Contains(ws[0].Description(), tc.wantSubstr) {
				t.Fatalf("WebSearch tool description = %q, want it to mention %q",
					ws[0].Description(), tc.wantSubstr)
			}
		})
	}
}

// TestResolveWebSearchTools_AllThreeProviders exercises resolveWebSearchTools
// directly (not just the shipped serper+ddg pairing) to lock in the full
// three-way precedence — Serper > SerpAPI > DDG — and the "declaring a keyed
// provider alone with no key configured yields nothing" behavior inherited
// unchanged from NewSerpAPITools/NewSerperTools.
func TestResolveWebSearchTools_AllThreeProviders(t *testing.T) {
	cases := []struct {
		name       string
		keys       []string
		serpAPIKey string
		serperKey  string
		wantCount  int
		wantSubstr string
	}{
		{
			name:       "all three declared, both keys set: Serper wins",
			keys:       []string{"serper", "serpapi", "ddg"},
			serpAPIKey: "test-serpapi-key",
			serperKey:  "test-serper-key",
			wantCount:  1,
			wantSubstr: "Serper.dev",
		},
		{
			name:       "serpapi + ddg, only serpapi key set: SerpAPI wins over DDG fallback",
			keys:       []string{"serpapi", "ddg"},
			serpAPIKey: "test-serpapi-key",
			wantCount:  1,
			wantSubstr: "SerpAPI",
		},
		{
			name:      "serper + serpapi, neither key set, no ddg declared: no fallback",
			keys:      []string{"serper", "serpapi"},
			wantCount: 0,
		},
		{
			name:      "serpapi declared alone with no key: unchanged nil-on-empty-key behavior",
			keys:      []string{"serpapi"},
			wantCount: 0,
		},
		{
			name:      "no web-search group declared at all",
			keys:      []string{"fs", "calc"},
			serperKey: "test-serper-key",
			wantCount: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveWebSearchTools(tc.keys, tc.serpAPIKey, tc.serperKey)
			if len(got) != tc.wantCount {
				t.Fatalf("resolveWebSearchTools(%v) returned %d tools, want %d",
					tc.keys, len(got), tc.wantCount)
			}
			if tc.wantCount == 0 {
				return
			}
			if !strings.Contains(got[0].Description(), tc.wantSubstr) {
				t.Fatalf("resolved tool description = %q, want it to mention %q",
					got[0].Description(), tc.wantSubstr)
			}
		})
	}
}
