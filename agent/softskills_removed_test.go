package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// softSkillToolNames are the tools the removed soft-skill pipeline mounted.
var softSkillToolNames = []string{
	"list_softskills", "load_softskill", "load_softskill_resource", "recall_softskills",
	"recall_precedents", "curate_session", "record_session_feedback",
}

// Soft-skills were removed, but the "softskills" key survives in older
// agent.json overlays: it must be accepted and mount nothing.
func TestSoftskillsToolGroupIsIgnored(t *testing.T) {
	tools, toolsets, _, _ := toolsForAgentConfig(
		context.Background(),
		RuntimeAgentConfig{Name: "probe", Tools: []string{"fs", "softskills"}},
		RuntimeSettings{},
		nil, nil, nil, nil, nil, nil, nil, nil, true, nil,
	)
	if len(toolsets) != 0 {
		t.Fatalf("the softskills key mounted %d toolset(s), want none", len(toolsets))
	}
	names := toolNames(tools)
	if !has(names, "Read") {
		t.Fatalf("fs tools missing: %v", names)
	}
	for _, n := range softSkillToolNames {
		if has(names, n) {
			t.Errorf("removed soft-skill tool %q is mounted: %v", n, names)
		}
	}
}

// A coordinating leader no longer gets the session-lifecycle tools
// (curate_session, record_session_feedback), and an agent.json still carrying
// "softskills_dir" and the "softskills" tool key keeps loading.
func TestLeaderHasNoSessionLifecycleTools(t *testing.T) {
	layer := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	writeAgentDef(t, layer, "leader", `{
		"name": "leader", "enabled": true, "leader": true,
		"description": "Coordinator.", "tools": ["fs", "softskills"],
		"softskills_dir": ""
	}`)
	writeAgentDef(t, layer, "investigator", `{
		"name": "investigator", "enabled": true,
		"description": "Reads things.", "tools": ["fs", "softskills"]
	}`)
	agentsJSON := `{
		"agents": ["leader", "investigator"],
		"softskills_dir": "",
		"router_squad": "none",
		"squads": [{"name": "default", "leader": "leader", "members": ["investigator"]}]
	}`
	if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(agentsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	opts := Options{DeferModelErrors: true}
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatalf("build infra: %v", err)
	}
	defer infra.Close()
	inst, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatalf("build instance: %v", err)
	}
	sq := inst.Squad("default")
	if sq == nil {
		t.Fatal("default squad not built")
	}
	got := agentToolNames(t, sq.Leader)
	if !has(got, "investigator") {
		t.Fatalf("leader tools = %v, want the investigator delegation tool", got)
	}
	for _, n := range softSkillToolNames {
		if has(got, n) {
			t.Errorf("leader still has removed tool %q: %v", n, got)
		}
	}
}
