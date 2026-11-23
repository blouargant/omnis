package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// promptLayers points the config chain at two temp layers (hi above lo) and
// returns them.
func promptLayers(t *testing.T) (hi, lo string) {
	t.Helper()
	hi, lo = t.TempDir(), t.TempDir()
	t.Setenv("OMNIS_HOME", hi)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", lo)
	t.Setenv("OMNIS_CONFIG_DIRS", hi+string(os.PathListSeparator)+lo)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	return hi, lo
}

func writePromptFile(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "prompt_overrides.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolvePromptOverridesMergesLayers(t *testing.T) {
	hi, lo := promptLayers(t)
	writePromptFile(t, lo, `{"prompts": {"block.choice": "lo choice", "block.steering": "lo steer"}}`)
	writePromptFile(t, hi, `{"prompts": {"contract.root": "hi root"}, "prompts_removed": ["block.choice"]}`)
	got, warns := resolvePromptOverrides()
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	want := map[string]string{"contract.root": "hi root", "block.steering": "lo steer"}
	if len(got) != len(want) || got["contract.root"] != "hi root" || got["block.steering"] != "lo steer" {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUnknownPromptIDIsAWarning(t *testing.T) {
	_, lo := promptLayers(t)
	writePromptFile(t, lo, `{"prompts": {"block.nope": "x", "tool.lsp_hover": "y", "block.choice": "  "}}`)
	got, warns := resolvePromptOverrides()
	if len(got) != 0 {
		t.Fatalf("overrides = %v, want none", got)
	}
	if len(warns) != 3 {
		t.Fatalf("warnings = %v, want 3", warns)
	}
	joined := strings.Join(warns, "\n")
	for _, s := range []string{`"block.nope": unknown id`, `"tool.lsp_hover": unknown id`, `"block.choice": empty text`} {
		if !strings.Contains(joined, s) {
			t.Errorf("warnings %q lack %q", joined, s)
		}
	}
}

func TestPromptOverridesWarningsReachConfigWarnings(t *testing.T) {
	_, lo := promptLayers(t)
	writeAgentDef(t, lo, "leader", `{"name": "leader", "enabled": true, "leader": true, "tools": []}`)
	if err := os.WriteFile(filepath.Join(lo, "agents.json"), []byte(`{"agents": ["leader"], "router_squad": "none"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writePromptFile(t, lo, `{"prompts": {"block.nope": "x", "block.choice": "mine"}}`)
	rt, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rt.PromptOverrides["block.choice"] != "mine" {
		t.Errorf("PromptOverrides = %v", rt.PromptOverrides)
	}
	if !strings.Contains(strings.Join(rt.ConfigWarnings, "\n"), "block.nope") {
		t.Errorf("ConfigWarnings = %v", rt.ConfigWarnings)
	}
}

func TestPromptTextFallsBackToDefault(t *testing.T) {
	rt := RuntimeSettings{PromptOverrides: map[string]string{"block.choice": "over"}}
	if got := promptText(rt, "block.choice", "def"); got != "over" {
		t.Errorf("got %q", got)
	}
	if got := promptText(rt, "block.steering", "def"); got != "def" {
		t.Errorf("got %q", got)
	}
	if got := promptText(RuntimeSettings{}, "block.choice", "def"); got != "def" {
		t.Errorf("got %q", got)
	}
}
