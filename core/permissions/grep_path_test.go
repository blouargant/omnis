package permissions

import "testing"

// Grep is in the Read class but names its target `path`: a Read(.env) deny
// must stop a Grep of .env.
func TestReadDenyAppliesToGrepPath(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"deny":["Read(.env)"]}}`)
	dir := t.TempDir()
	if d, _ := cfg.CheckArgs("Grep", map[string]any{"pattern": "KEY", "path": ".env"}, dir); d != DecisionDeny {
		t.Fatalf("Grep of .env: %v, want deny", d)
	}
	if d, _ := cfg.CheckArgs("Grep", map[string]any{"pattern": "KEY", "path": "src"}, dir); d == DecisionDeny {
		t.Fatal("Grep of src denied")
	}
}
