package permissions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingRulesReturnsEmptySet(t *testing.T) {
	t.Parallel()

	rules, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rules == nil {
		t.Fatal("Load() returned nil rules")
	}
	if decision, _ := rules.Check("bash", "ls"); decision != DecisionAllow {
		t.Fatalf("Check() = %v, want allow", decision)
	}
}

func TestRulesCheckHonorsDenyAllowAndAsk(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "permissions.yaml")
	content := "always_deny:\n  - pattern: rm\\s+-rf\n    reason: destructive\nalways_allow:\n  - ls\nask_user:\n  - pattern: kubectl\n    reason: cluster access\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rules, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if decision, reason := rules.Check("bash", `{"command":"rm -rf /tmp/demo"}`); decision != DecisionDeny || reason != "destructive" {
		t.Fatalf("deny Check() = (%v, %q)", decision, reason)
	}
	if decision, _ := rules.Check("bash", `{"command":"ls -la"}`); decision != DecisionAllow {
		t.Fatalf("allow Check() = %v", decision)
	}
	if decision, reason := rules.Check("bash", `{"command":"kubectl get pods"}`); decision != DecisionAsk || reason != "cluster access" {
		t.Fatalf("ask Check() = (%v, %q)", decision, reason)
	}
}
