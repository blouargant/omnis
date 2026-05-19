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
      "name": "default",
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
			{Name: "default", Leader: "leader"},
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
