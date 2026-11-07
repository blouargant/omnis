package agent

import (
	"encoding/json"
	"testing"
)

// Fetched pages stay in the context and are re-sent on every later call, so an
// agent that holds the web tools needs a host-side cap on its tool calls: a
// written page budget alone was ignored (2026-10-31 call-volume A/B). Since
// 2026-11-07 the premium knowledge_leader fetches pages itself.
func TestShippedWebToolHoldersAreCapped(t *testing.T) {
	webGroups := map[string]bool{"web": true, "serper": true, "serpapi": true, "ddg": true}
	for _, name := range shippedAgentNames(t) {
		var a struct {
			Tools        []string `json:"tools"`
			MaxCalls     int      `json:"max_tool_calls"`
			MaxCallsCall int      `json:"max_tool_calls_per_call"`
		}
		if err := json.Unmarshal([]byte(readShippedAgentJSON(t, name)), &a); err != nil {
			continue // default.md-only dirs and the like
		}
		holds := false
		for _, g := range a.Tools {
			holds = holds || webGroups[g]
		}
		if holds && a.MaxCalls <= 0 && a.MaxCallsCall <= 0 {
			t.Errorf("%s holds web tools but declares neither max_tool_calls nor max_tool_calls_per_call", name)
		}
	}
}
