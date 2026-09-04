package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The validator must be enabled and nested under BOTH mutating agents. It is
// deliberately NOT a squad member: the leader's tool list must not grow, and the
// gatherer doctrine puts a specialist's helper on the specialist.
func TestValidatorIsNestedUnderBothMutatingAgents(t *testing.T) {
	for _, name := range []string{"k8s_editor", "k8s_cleaner"} {
		data := readShippedAgentJSON(t, name)
		if !strings.Contains(data, `"k8s_validator"`) {
			t.Fatalf("%s does not declare k8s_validator in its subagents", name)
		}
	}
}

// It must never hold a mutating tool: a reviewer that can change the cluster is
// not a reviewer.
func TestValidatorIsReadOnlyAndCanAttest(t *testing.T) {
	data := readShippedAgentJSON(t, "k8s_validator")
	if !strings.Contains(data, `"attest"`) {
		t.Fatal("k8s_validator must declare the attest tool group, or it cannot record a verdict")
	}
	for _, forbidden := range []string{`"Write"`, `"Edit"`, `"revert"`} {
		if strings.Contains(data, forbidden) {
			t.Fatalf("k8s_validator must not hold %s", forbidden)
		}
	}
}

// validateSubAgentGraph is fatal on an unknown or disabled target, so a
// mistake here breaks the whole config at reload, not just this agent.
func TestShippedFleetResolvesWithTheValidator(t *testing.T) {
	rs, err := loadShippedRuntimeSettings(t)
	if err != nil {
		t.Fatalf("the shipped config must resolve: %v", err)
	}
	found := false
	for _, a := range rs.Agents {
		if a.Name == "k8s_validator" {
			found = true
		}
	}
	if !found {
		t.Fatal("k8s_validator is not enabled in config/agents.json")
	}
}

// loadShippedRuntimeSettings resolves RuntimeSettings against the repo's real
// shipped config/ + registry/ trees, the same way a packaged install lays them
// out under /etc/omnis (config/*.json -> <layer>/*.json, registry/ ->
// <layer>/registry/) — see the "omnis runs from /etc/omnis, not the repo" and
// "OMNIS_CONFIG_DIRS doesn't redirect registry" notes: config/ and registry/
// are SIBLINGS in the checkout, so OMNIS_SYSTEM_CONFIG_DIR must point at a
// directory that has both, not at config/ directly. It builds that layer with
// symlinks in a temp dir rather than copying, and isolates OMNIS_HOME so a
// developer's real $HOME/.omnis never leaks into the resolved settings.
func loadShippedRuntimeSettings(t *testing.T) (RuntimeSettings, error) {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	configDir := filepath.Join(repoRoot, "config")
	registryDir := filepath.Join(repoRoot, "registry")

	sysDir := t.TempDir()
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatalf("read %s: %v", configDir, err)
	}
	for _, e := range entries {
		if err := os.Symlink(filepath.Join(configDir, e.Name()), filepath.Join(sysDir, e.Name())); err != nil {
			t.Fatalf("symlink %s: %v", e.Name(), err)
		}
	}
	if err := os.Symlink(registryDir, filepath.Join(sysDir, "registry")); err != nil {
		t.Fatalf("symlink registry: %v", err)
	}

	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sysDir)
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	return ResolveRuntimeSettings(Options{})
}
