package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// deferred_tools must survive a Settings save: a key missing from the PUT
// allowlist is dropped, which would silently put every deferred schema back
// on every call of the agent.
func TestParsedAgentRoundTripKeepsDeferredTools(t *testing.T) {
	sys, home := agentLayersSetup(t)
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"tools":["Read"],"deferred_tools":["lsp_*","TaskCreate"]}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"),
		[]byte(`{"agents":["leader"],"squads":[{"name":"System","leader":"leader","members":[]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	agents, _ := data["agents"].([]any)
	var leader map[string]any
	for _, raw := range agents {
		if am, _ := raw.(map[string]any); am["name"] == "leader" {
			leader = am
		}
	}
	if got, _ := leader["deferred_tools"].([]any); len(got) != 2 {
		t.Fatalf("GET did not surface deferred_tools: %v", leader["deferred_tools"])
	}
	// An edit of an unrelated field must not drop the list.
	leader["description"] = "Leader (edited)"
	w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(home, "registry/agents/leader/agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, present := m["deferred_tools"]; present {
		t.Fatalf("unchanged deferred_tools forked into the user overlay: %s", b)
	}
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	data = parsedAgentData(t, w.Body.String())
	for _, raw := range data["agents"].([]any) {
		if am, _ := raw.(map[string]any); am["name"] == "leader" {
			if got, _ := am["deferred_tools"].([]any); len(got) != 2 {
				t.Fatalf("deferred_tools lost after save: %v", am["deferred_tools"])
			}
			return
		}
	}
	t.Fatal("leader missing after save")
}
