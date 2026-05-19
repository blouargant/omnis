package permissions

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingRulesReturnsEmptySet(t *testing.T) {
	t.Parallel()

	rules, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rules == nil {
		t.Fatal("Load() returned nil rules")
	}
	if decision, _ := rules.Check("Bash", "ls"); decision != DecisionAllow {
		t.Fatalf("Check() = %v, want allow", decision)
	}
}

func TestRulesCheckHonorsDenyAllowAndAsk(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "permissions.json")
	content := `{
  "always_deny": [
    {"pattern": "rm\\s+-rf", "reason": "destructive"}
  ],
  "always_allow": ["ls"],
  "ask_user": [
    {"pattern": "kubectl", "reason": "cluster access"}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	rules, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if decision, reason := rules.Check("Bash", `{"command":"rm -rf /tmp/demo"}`); decision != DecisionDeny || reason != "destructive" {
		t.Fatalf("deny Check() = (%v, %q)", decision, reason)
	}
	if decision, _ := rules.Check("Bash", `{"command":"ls -la"}`); decision != DecisionAllow {
		t.Fatalf("allow Check() = %v", decision)
	}
	if decision, reason := rules.Check("Bash", `{"command":"kubectl get pods"}`); decision != DecisionAsk || reason != "cluster access" {
		t.Fatalf("ask Check() = (%v, %q)", decision, reason)
	}
}
