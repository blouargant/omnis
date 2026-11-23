package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// turn_reminder must survive a Settings save: a key missing from the PUT
// allowlist is dropped, which would silently remove the reminder.
func TestParsedAgentRoundTripKeepsTurnReminder(t *testing.T) {
	sys, home := agentLayersSetup(t)
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"tools":["Read"],"turn_reminder":"[note]"}`)
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
	if got, _ := leader["turn_reminder"].(string); got != "[note]" {
		t.Fatalf("GET did not surface turn_reminder: %v", leader["turn_reminder"])
	}
	// An edit of an unrelated field must not drop the reminder.
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
	if _, present := m["turn_reminder"]; present {
		t.Fatalf("unchanged turn_reminder forked into the user overlay: %s", b)
	}
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	data = parsedAgentData(t, w.Body.String())
	for _, raw := range data["agents"].([]any) {
		if am, _ := raw.(map[string]any); am["name"] == "leader" {
			if got, _ := am["turn_reminder"].(string); got != "[note]" {
				t.Fatalf("turn_reminder lost after save: %v", am["turn_reminder"])
			}
			return
		}
	}
	t.Fatal("leader missing after save")
}
