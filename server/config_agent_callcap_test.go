package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// max_tool_calls_per_call must survive a Settings save: a key missing from the
// PUT allowlist is dropped, which would silently uncap web_agent's fan-out.
func TestParsedAgentRoundTripKeepsPerCallCap(t *testing.T) {
	sys, _ := agentLayersSetup(t)
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"tools":["Read"]}`)
	seedSysAgentEntry(t, sys, "web_agent", `{"name":"web_agent","description":"Web","enabled":true,"leader":false,"builtin":false,"allow_file_attachments":false,"tools":["Read"],"max_instances":10,"max_tool_calls_per_call":15}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"),
		[]byte(`{"agents":["leader","web_agent"],"squads":[{"name":"System","leader":"leader","members":["web_agent"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestEngine(t, editorFiles())

	capOf := func() any {
		w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
		}
		data := parsedAgentData(t, w.Body.String())
		for _, raw := range data["agents"].([]any) {
			if am := raw.(map[string]any); am["name"] == "web_agent" {
				return am["max_tool_calls_per_call"]
			}
		}
		t.Fatal("web_agent missing from GET")
		return nil
	}
	if got := capOf(); got != float64(15) {
		t.Fatalf("GET does not surface the per-call cap: %v", got)
	}

	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	data := parsedAgentData(t, w.Body.String())
	if w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data}); w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	if got := capOf(); got != float64(15) {
		t.Fatalf("per-call cap lost on a no-op save: %v", got)
	}
}
