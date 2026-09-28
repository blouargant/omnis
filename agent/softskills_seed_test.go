package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// BuildInstance must seed the packaged built-in soft-skills from the system
// config layer into the per-user soft-skill directory: the leaders are told to
// `load_softskill wrap-session`, which reads only $OMNIS_HOME/softskills, and
// before seeding existed that call failed with "skill not found" on every
// install.
func TestBuildInstanceSeedsBuiltinSoftSkills(t *testing.T) {
	layer, home := t.TempDir(), t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	writeAgentDef(t, layer, "leader", `{
		"name": "leader", "enabled": true, "leader": true,
		"description": "Coordinator.", "tools": []
	}`)
	agentsJSON := `{
		"agents": ["leader"],
		"router_squad": "none",
		"squads": [{"name": "default", "leader": "leader", "members": []}]
	}`
	if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(agentsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(layer, "softskills", "wrap-session")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: wrap-session\ndescription: test\n---\n# Wrap\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
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
	defer inst.Close()

	got, err := os.ReadFile(filepath.Join(home, "softskills", "wrap-session", "SKILL.md"))
	if err != nil {
		t.Fatalf("wrap-session not seeded into $OMNIS_HOME/softskills: %v", err)
	}
	if string(got) != body {
		t.Fatalf("seeded content mismatch: %q", got)
	}
}
