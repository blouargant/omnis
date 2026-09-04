package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shipped hook must parse, must be fail_closed, and must point at a script
// that exists. A guard whose script is missing stops guarding silently, which
// internal/hooks' fail_closed turns into a block — but only if the declaration
// is right in the first place.
func TestShippedHooksConfigIsWellFormed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "hooks.json"))
	if err != nil {
		t.Fatalf("read config/hooks.json: %v", err)
	}
	var f struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command    string `json:"command"`
				Timeout    int    `json:"timeout"`
				FailClosed bool   `json:"fail_closed"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("config/hooks.json does not parse: %v", err)
	}
	pre := f.Hooks["PreToolUse"]
	if len(pre) == 0 || len(pre[0].Hooks) == 0 {
		t.Fatal("no PreToolUse hook declared")
	}
	h := pre[0].Hooks[0]
	if !h.FailClosed {
		t.Fatal("the validation hook must be fail_closed, or a broken script silently stops validating")
	}
	if h.Timeout <= 0 {
		t.Fatal("the validation hook must declare a timeout (a cluster dry-run takes seconds)")
	}
	if !strings.Contains(h.Command, "OMNIS_SYSTEM_CONFIG_DIR") {
		t.Fatal("the command must resolve through OMNIS_SYSTEM_CONFIG_DIR so the non-FHS packages (brew/MSI/pip) find it")
	}
	if _, err := os.Stat(filepath.Join("..", "config", "hooks", "k8s-validate.py")); err != nil {
		t.Fatalf("the declared script does not exist: %v", err)
	}
}

// Every packaging channel must ship the hook, or the guarantee is absent on that
// channel. This is the same class of defect as the profile script that bypassed
// the config layers (see profile_test.go).
func TestEveryPackagingChannelShipsTheHook(t *testing.T) {
	for _, f := range []struct{ path, needle string }{
		{filepath.Join("..", ".goreleaser.yaml"), "config/hooks/k8s-validate.py"},
		{filepath.Join("windows", "omnis.wxs"), "k8s-validate.py"},
		{filepath.Join("..", "scripts", "build_wheels.py"), "hooks"},
	} {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("read %s: %v", f.path, err)
		}
		if !strings.Contains(string(data), f.needle) {
			t.Fatalf("%s does not ship the validation hook (looking for %q)", f.path, f.needle)
		}
	}
}
