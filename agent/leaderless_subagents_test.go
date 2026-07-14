package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeAgentDef drops a registry agent definition into a config layer.
func writeAgentDef(t *testing.T, layer, name, json string) {
	t.Helper()
	dir := filepath.Join(layer, "registry", "agents", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.json"), []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("You are "+name+"."), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A LEADERLESS squad root must still build and mount its OWN `subagents`.
//
// It didn't: buildSquadInstance skipped sub-agent building entirely when
// leaderless ("no members to coordinate ⇒ no team"), which conflated "is a
// coordinator" with "may delegate" — the exact conflation `subagents` exists to
// undo. The Helper squad is leaderless, so its session_search specialist was
// built into the catalogue and then never mounted on anything: the Helper simply
// had no way to search past sessions, and the failure was silent (the model says
// it cannot do it and moves on).
func TestLeaderlessRootMountsItsOwnSubAgents(t *testing.T) {
	layer := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	// OMNIS_CONFIG_DIRS alone does NOT redirect the registry search chain (it
	// still resolves agent definitions from /etc/omnis), so point the system layer
	// at the temp dir too or this test reads the real shipped fleet.
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	writeAgentDef(t, layer, "helper", `{
		"name": "helper", "enabled": true, "leader": true,
		"description": "Docs + settings steward.",
		"tools": [], "subagents": ["session_search"]
	}`)
	writeAgentDef(t, layer, "session_search", `{
		"name": "session_search", "enabled": true, "leader": false,
		"description": "Finds past chat sessions.",
		"tools": ["sessions"]
	}`)
	writeAgentDef(t, layer, "leader", `{
		"name": "leader", "enabled": true, "leader": true,
		"description": "Coordinator.", "tools": []
	}`)

	agentsJSON := `{
		"agents": ["leader", "helper", "session_search"],
		"router_squad": "none",
		"squads": [
			{"name": "default", "leader": "leader", "members": []},
			{"name": "Helper", "leader": "none", "members": ["helper"]}
		]
	}`
	if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(agentsJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	opts := Options{DeferModelErrors: true} // no live model creds needed to assert wiring
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatalf("build infra: %v", err)
	}
	defer infra.Close()
	inst, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatalf("build instance: %v", err)
	}

	sq := inst.Squad("helper")
	if sq == nil {
		t.Fatal("Helper squad not built")
	}

	// The specialist must be reachable AS A TOOL on the leaderless root — being
	// merely present in the catalogue is what the old code already did, and it
	// left the Helper unable to delegate.
	if got := agentToolNames(t, sq.Leader); !has(got, "session_search") {
		t.Fatalf("leaderless root's tools = %v, want its declared subagent session_search mounted", got)
	}
	if _, ok := sq.SubAgents["session_search"]; !ok {
		t.Errorf("session_search missing from the squad's sub-agent map: %v", sq.SubAgents)
	}

	// A leaderless root is still NOT a session coordinator: the session-lifecycle
	// tools stay off it even now that it has a team.
	if got := agentToolNames(t, sq.Leader); has(got, "curate_session") {
		t.Errorf("leaderless root gained the coordinator-only curate_session tool: %v", got)
	}
}

// A leaderless root that declares NO subagents must build exactly as before —
// one agent, no delegation tools.
func TestLeaderlessRootWithoutSubAgentsIsUnchanged(t *testing.T) {
	layer := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	// OMNIS_CONFIG_DIRS alone does NOT redirect the registry search chain (it
	// still resolves agent definitions from /etc/omnis), so point the system layer
	// at the temp dir too or this test reads the real shipped fleet.
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	writeAgentDef(t, layer, "helper", `{
		"name": "helper", "enabled": true, "leader": true,
		"description": "Docs steward.", "tools": []
	}`)
	writeAgentDef(t, layer, "leader", `{
		"name": "leader", "enabled": true, "leader": true,
		"description": "Coordinator.", "tools": []
	}`)

	agentsJSON := `{
		"agents": ["leader", "helper"],
		"router_squad": "none",
		"squads": [
			{"name": "default", "leader": "leader", "members": []},
			{"name": "Helper", "leader": "none", "members": ["helper"]}
		]
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

	sq := inst.Squad("helper")
	if sq == nil {
		t.Fatal("Helper squad not built")
	}
	if len(sq.SubAgents) != 0 {
		t.Errorf("leaderless root without subagents built a team: %v", sq.SubAgents)
	}
}
