package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSquadsExplicit verifies that explicit squad declarations in agent.json
// are parsed, validated against the agent catalogue, and surfaced on
// RuntimeSettings.
func TestSquadsExplicit(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
		{Name: "investigator"},
		{Name: "web_agent"},
		{Name: "summariser"},
	})

	cfgPath := filepath.Join(dir, "agent.json")
	mustWrite(t, cfgPath, []byte(`{
  "agents": ["leader", "investigator", "web_agent", "summariser"],
  "squads": [
    {
      "name": "system",
      "description": "Full team",
      "leader": "leader",
      "members": ["investigator", "web_agent", "summariser"]
    },
    {
      "name": "research",
      "description": "Web focus",
      "leader": "leader",
      "members": ["web_agent", "summariser"]
    }
  ]
}`))

	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}

	if got := len(runtime.Squads); got != 2 {
		t.Fatalf("len(Squads) = %d, want 2", got)
	}
	def, ok := runtime.DefaultSquad()
	if !ok {
		t.Fatal("default squad missing after resolution")
	}
	if def.Leader != "leader" {
		t.Fatalf("default squad leader = %q, want leader", def.Leader)
	}
	wantMembers := []string{"investigator", "web_agent", "summariser"}
	if !equalStringSlice(def.Members, wantMembers) {
		t.Fatalf("default squad members = %v, want %v", def.Members, wantMembers)
	}

	research, ok := runtime.Squad("research")
	if !ok {
		t.Fatal("research squad missing")
	}
	if !equalStringSlice(research.Members, []string{"web_agent", "summariser"}) {
		t.Fatalf("research members = %v", research.Members)
	}
}

// TestSquadsDefaultSynthesized verifies that omitting the `squads:` block
// produces a synthesized `default` squad from the enabled agents.
func TestSquadsDefaultSynthesized(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
		{Name: "investigator"},
		{Name: "curator", Enabled: ptrBool(true)},
	})

	cfgPath := filepath.Join(dir, "agent.json")
	mustWrite(t, cfgPath, []byte(`{
  "agents": ["leader", "investigator", "curator"]
}`))

	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}

	if got := len(runtime.Squads); got != 1 {
		t.Fatalf("len(Squads) = %d, want 1 (synthesized default)", got)
	}
	def, ok := runtime.DefaultSquad()
	if !ok {
		t.Fatal("default squad missing")
	}
	if def.Leader != "leader" {
		t.Fatalf("default squad leader = %q, want leader", def.Leader)
	}
	// Curator must NOT be in the synthesized members list.
	for _, m := range def.Members {
		if m == "curator" {
			t.Fatal("default squad must not include curator")
		}
		if m == "leader" {
			t.Fatal("default squad must not include leader as a member")
		}
	}
	if !containsString(def.Members, "investigator") {
		t.Fatalf("default squad members should include investigator, got %v", def.Members)
	}
}

// TestSquadsLeaderless verifies that a squad with leader "none" (or empty)
// resolves to a leaderless squad: Leader normalised to "" and exactly one
// member, which need not be marked leader:true.
func TestSquadsLeaderless(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
		{Name: "helper"},
	})

	cfgPath := filepath.Join(dir, "agent.json")
	mustWrite(t, cfgPath, []byte(`{
  "agents": ["leader", "helper"],
  "squads": [
    {"name": "default", "leader": "leader", "members": ["helper"]},
    {"name": "Helper", "leader": "none", "members": ["helper"]}
  ]
}`))

	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	sq, ok := runtime.Squad("helper")
	if !ok {
		t.Fatal("helper squad missing")
	}
	if sq.Leader != "" {
		t.Fatalf("leaderless squad Leader = %q, want empty", sq.Leader)
	}
	if !equalStringSlice(sq.Members, []string{"helper"}) {
		t.Fatalf("leaderless squad members = %v, want [helper]", sq.Members)
	}
}

// TestSquadsFleetConductorResolves verifies that a led squad (Fleet, leader
// conductor) with an EMPTY members list resolves cleanly — only a leaderless
// squad requires exactly one member; a squad with a real leader:true agent
// delegates to spawned sessions (fleet_dispatch), not squad members, so zero
// declared members must be accepted. It also checks the conductor agent
// resolves as an enabled leader whose tools include fleet, fleet_dispatch,
// and planning.
func TestSquadsFleetConductorResolves(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
		{
			Name:     "conductor",
			Leader:   ptrBool(true),
			ModelRef: "",
			Tools:    []string{"fleet", "fleet_dispatch", "planning"},
		},
	})

	cfgPath := filepath.Join(dir, "agent.json")
	mustWrite(t, cfgPath, []byte(`{
  "agents": ["leader", "conductor"],
  "squads": [
    {"name": "system", "leader": "leader", "members": []},
    {
      "name": "Fleet",
      "description": "Multi-project coordinator.",
      "leader": "conductor",
      "members": []
    }
  ]
}`))

	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}

	fleet, ok := runtime.Squad("fleet")
	if !ok {
		t.Fatal("Fleet squad missing after resolution")
	}
	if fleet.Leader != "conductor" {
		t.Fatalf("Fleet squad leader = %q, want conductor", fleet.Leader)
	}
	if len(fleet.Members) != 0 {
		t.Fatalf("Fleet squad members = %v, want empty", fleet.Members)
	}

	var conductor RuntimeAgentConfig
	found := false
	for _, a := range runtime.Agents {
		if a.Name == "conductor" {
			conductor = a
			found = true
			break
		}
	}
	if !found {
		t.Fatal("conductor agent missing from resolved runtime agents")
	}
	if !conductor.Enabled {
		t.Fatal("conductor agent should be enabled")
	}
	if !conductor.Leader {
		t.Fatal("conductor agent should be marked as leader")
	}
	for _, want := range []string{"fleet", "fleet_dispatch", "planning"} {
		if !containsString(conductor.Tools, want) {
			t.Fatalf("conductor tools = %v, want to include %q", conductor.Tools, want)
		}
	}
}

// TestSquadsValidation covers the rejection paths.
func TestSquadsValidation(t *testing.T) {
	cases := []struct {
		name      string
		agents    []AgentEntry
		config    string
		wantError string
	}{
		{
			name:   "unknown leader",
			agents: []AgentEntry{{Name: "leader"}},
			config: `{
  "agents": ["leader"],
  "squads": [{"name": "default", "leader": "ghost", "members": []}]
}`,
			wantError: `leader "ghost"`,
		},
		{
			name:   "unknown member",
			agents: []AgentEntry{{Name: "leader"}, {Name: "investigator"}},
			config: `{
  "agents": ["leader", "investigator"],
  "squads": [{"name": "default", "leader": "leader", "members": ["ghost"]}]
}`,
			wantError: `member "ghost"`,
		},
		{
			name:   "curator as member",
			agents: []AgentEntry{{Name: "leader"}, {Name: "curator"}},
			config: `{
  "agents": ["leader", "curator"],
  "squads": [{"name": "default", "leader": "leader", "members": ["curator"]}]
}`,
			wantError: "cannot include the curator agent",
		},
		{
			name:   "duplicate squad name",
			agents: []AgentEntry{{Name: "leader"}},
			config: `{
  "agents": ["leader"],
  "squads": [
    {"name": "default", "leader": "leader", "members": []},
    {"name": "Default", "leader": "leader", "members": []}
  ]
}`,
			wantError: "duplicate squad name",
		},
		{
			name:   "non-leader agent as squad leader",
			agents: []AgentEntry{{Name: "leader"}, {Name: "investigator"}},
			config: `{
  "agents": ["leader", "investigator"],
  "squads": [
    {"name": "default", "leader": "leader", "members": []},
    {"name": "rogue", "leader": "investigator", "members": []}
  ]
}`,
			wantError: `not marked as leader: true`,
		},
		{
			name:   "leaderless with no member",
			agents: []AgentEntry{{Name: "leader"}, {Name: "helper"}},
			config: `{
  "agents": ["leader", "helper"],
  "squads": [
    {"name": "default", "leader": "leader", "members": ["helper"]},
    {"name": "solo", "leader": "none", "members": []}
  ]
}`,
			wantError: "exactly one member",
		},
		{
			name:   "leaderless with multiple members",
			agents: []AgentEntry{{Name: "leader"}, {Name: "helper"}, {Name: "web_agent"}},
			config: `{
  "agents": ["leader", "helper", "web_agent"],
  "squads": [
    {"name": "default", "leader": "leader", "members": ["helper", "web_agent"]},
    {"name": "solo", "leader": "none", "members": ["helper", "web_agent"]}
  ]
}`,
			wantError: "exactly one member",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			setupAgentsRegistry(t, dir, tc.agents)
			cfgPath := filepath.Join(dir, "agent.json")
			mustWrite(t, cfgPath, []byte(tc.config))
			_, err := ResolveRuntimeSettings(Options{
				ConfigPath:       cfgPath,
				ConfigPathStrict: true,
			})
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantError)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got %v", tc.wantError, err)
			}
		})
	}
}

// TestSquadsAutoPrependDefault verifies that when the user's agent.json
// declares squads but none is named "default", the resolver silently
// prepends a synthesised default rather than failing — this is the
// editor-friendly UX (user adds a research squad → resolver still has
// the default available for sessions that don't specify one).
func TestSquadsAutoPrependDefault(t *testing.T) {
	dir := t.TempDir()
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
		{Name: "investigator"},
	})

	cfgPath := filepath.Join(dir, "agent.json")
	mustWrite(t, cfgPath, []byte(`{
  "agents": ["leader", "investigator"],
  "squads": [
    {"name": "research", "leader": "leader", "members": ["investigator"]}
  ]
}`))

	runtime, err := ResolveRuntimeSettings(Options{
		ConfigPath:       cfgPath,
		ConfigPathStrict: true,
	})
	if err != nil {
		t.Fatalf("ResolveRuntimeSettings() error = %v", err)
	}
	if got := len(runtime.Squads); got != 2 {
		t.Fatalf("len(Squads) = %d, want 2 (research + synthesised default)", got)
	}
	if runtime.Squads[0].Name != DefaultSquadName {
		t.Fatalf("first squad = %q, want %q (default prepended)", runtime.Squads[0].Name, DefaultSquadName)
	}
	if _, ok := runtime.Squad("research"); !ok {
		t.Fatal("research squad missing after auto-synthesis")
	}
}

// TestSquadAccessors exercises the lookup helpers.
func TestSquadAccessors(t *testing.T) {
	runtime := RuntimeSettings{
		Squads: []RuntimeSquadConfig{
			{Name: "system", Leader: "leader"},
			{Name: "research", Leader: "leader"},
		},
	}
	if _, ok := runtime.Squad(""); ok {
		t.Fatal("Squad(\"\") returned ok")
	}
	if _, ok := runtime.Squad("MISSING"); ok {
		t.Fatal("Squad(MISSING) returned ok")
	}
	if sq, ok := runtime.Squad("RESEARCH"); !ok || sq.Name != "research" {
		t.Fatalf("Squad(RESEARCH) = %+v, ok=%v", sq, ok)
	}
	if sq, ok := runtime.DefaultSquad(); !ok || sq.Name != DefaultSquadName {
		t.Fatalf("DefaultSquad() = %+v, ok=%v", sq, ok)
	}
}

// TestSquadLeaderInstructionIsPerAgent guards the squad-leader wiring bug where
// a non-default squad leader (e.g. the "helper" agent leading the "Helper"
// squad) was given the generic coordinator instruction instead of its own.
// buildSquadInstance resolves an empty inline Instruction via
// defaultAgentInstruction(leaderCfg.Name) — NOT a hardcoded "leader" — so the
// chosen prompt must depend on the leader agent's name. This asserts the
// resolver the fix relies on is genuinely name-keyed and that the helper's and
// leader's shipped instructions are materially different.
func TestSquadLeaderInstructionIsPerAgent(t *testing.T) {
	dir := t.TempDir()
	// setupAgentsRegistry copies each agent's real instruction.md from the
	// repo's registry/agents/<name>/ into the temp registry.
	setupAgentsRegistry(t, dir, []AgentEntry{
		{Name: "leader"},
		{Name: "helper"},
	})

	leaderInstr := defaultAgentInstruction("leader")
	helperInstr := defaultAgentInstruction("helper")

	if leaderInstr == "" || helperInstr == "" {
		t.Fatalf("instructions missing: leader=%d bytes helper=%d bytes",
			len(leaderInstr), len(helperInstr))
	}
	if leaderInstr == helperInstr {
		t.Fatal("helper and leader resolve to the same instruction; a squad led " +
			"by the helper would get the coordinator prompt (the bug this guards)")
	}
	// Spot-check the two prompts are the ones we expect, so the test fails
	// loudly if the helper is ever pointed at the coordinator instruction.
	if !strings.Contains(leaderInstr, "coordinator") {
		t.Errorf("leader instruction missing %q marker", "coordinator")
	}
	if !strings.Contains(helperInstr, "registry steward") {
		t.Errorf("helper instruction missing %q marker", "registry steward")
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
