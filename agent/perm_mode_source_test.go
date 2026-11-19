package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/blouargant/omnis/core/permissions"
)

// The ModeStore's config source must follow the PROMOTED generation: building
// another generation that is never installed (a failed or rejected reload) must
// not leave the store reading that generation's torn-down reloader.
func TestPermModeConfigSourceFollowsPromotedGeneration(t *testing.T) {
	layer := t.TempDir()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	writeAgentDef(t, layer, "leader", `{"name":"leader","enabled":true,"leader":true,"description":"c","tools":[]}`)
	if err := os.WriteFile(filepath.Join(layer, "agents.json"), []byte(`{"agents":["leader"],"router_squad":"none","squads":[{"name":"default","leader":"leader","members":[]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	permPath := filepath.Join(layer, "permissions.json")
	if err := os.WriteFile(permPath, []byte(`{"permissions":{"defaultMode":"default"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	opts := Options{DeferModelErrors: true}
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer infra.Close()
	first, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(infra, first)
	_ = m

	// A second generation is built but never promoted, then torn down.
	second, err := BuildInstance(ctx, infra, opts, 2)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Close()

	if err := os.WriteFile(permPath, []byte(`{"permissions":{"defaultMode":"plan"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src, ok := first.Squads[first.DefaultName].PermSource.(*permissions.Reloader)
	if !ok {
		t.Fatalf("default squad has no permission reloader: %T", first.Squads[first.DefaultName].PermSource)
	}
	if err := src.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := infra.PermModes.ConfigMode(); got != permissions.ModePlan {
		t.Fatalf("ConfigMode = %v, want plan (store follows an unpromoted generation)", got)
	}
}
