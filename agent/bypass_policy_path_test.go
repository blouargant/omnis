package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestBypassPolicyReadsRelocatedSystemDir: a relocated install (Homebrew, MSI,
// pip) sets OMNIS_SYSTEM_CONFIG_DIR; the bypass-disable policy must be read
// from that directory, not from the /etc/omnis default.
func TestBypassPolicyReadsRelocatedSystemDir(t *testing.T) {
	tmp := t.TempDir()
	sys := filepath.Join(tmp, "sys")
	home := filepath.Join(tmp, "home")
	for _, d := range []string{sys, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pol := `{"permissions":{"disableBypassPermissionsMode":"disable"}}`
	if err := os.WriteFile(filepath.Join(sys, "permissions.json"), []byte(pol), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sys)
	t.Setenv("OMNIS_CONFIG_DIRS", home+":"+sys)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	infra, err := BuildInfrastructure(context.Background(), Options{DeferModelErrors: true})
	if err != nil {
		t.Fatalf("build infra: %v", err)
	}
	defer infra.Close()
	if !infra.PermModes.BypassDisabled() {
		t.Fatal("bypass policy in OMNIS_SYSTEM_CONFIG_DIR was not read")
	}
}
