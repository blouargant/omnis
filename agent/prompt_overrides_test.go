package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"

	"github.com/blouargant/omnis/core/agentkit"
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

// agentInstruction reads the instruction off a BUILT agent (ADK's llmAgent is
// unexported; its embedded State carries it, as agentTools reads Tools).
func agentInstruction(t *testing.T, a adkagent.Agent) string {
	t.Helper()
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	f := v.FieldByName("State").FieldByName("Instruction")
	if !f.IsValid() {
		t.Fatalf("no State.Instruction on %T (ADK layout changed?)", a)
	}
	return f.String()
}

// contractFleet writes a leader + one member sub-agent in lo, routing on.
func contractFleet(t *testing.T) (hi, lo string) {
	t.Helper()
	hi, lo = promptLayers(t)
	writeAgentDef(t, lo, "leader", `{"name": "leader", "enabled": true, "leader": true, "description": "Coordinator.", "tools": []}`)
	writeAgentDef(t, lo, "worker", `{"name": "worker", "enabled": true, "description": "Does work.", "tools": []}`)
	agentsJSON := `{"agents": ["leader", "worker"], "squads": [{"name": "system", "leader": "leader", "members": ["worker"]}]}`
	if err := os.WriteFile(filepath.Join(lo, "agents.json"), []byte(agentsJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return hi, lo
}

func buildTestInstance(t *testing.T) *Instance {
	t.Helper()
	ctx := context.Background()
	opts := Options{DeferModelErrors: true}
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	t.Cleanup(func() { infra.Close() })
	inst, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatalf("instance: %v", err)
	}
	t.Cleanup(func() { inst.Close() })
	return inst
}

func TestContractOverrideReachesRootsAndSubAgents(t *testing.T) {
	_, lo := contractFleet(t)
	writePromptFile(t, lo, `{"prompts": {"contract.root": "ROOT-CONTRACT", "contract.subagent": "SUB-CONTRACT"}}`)
	inst := buildTestInstance(t)
	sq := inst.Squad("system")
	if got := agentInstruction(t, sq.Leader); !strings.HasPrefix(got, "ROOT-CONTRACT\n\n") {
		t.Errorf("root instruction starts %q", firstLine(got))
	}
	if got := agentInstruction(t, sq.SubAgents["worker"]); !strings.HasPrefix(got, "SUB-CONTRACT\n\n") {
		t.Errorf("sub-agent instruction starts %q", firstLine(got))
	}
	router := inst.Squad(inst.RouterName)
	if router == nil {
		t.Fatal("router squad not built")
	}
	if got := agentInstruction(t, router.Leader); strings.Contains(got, "ROOT-CONTRACT") || strings.Contains(got, "SUB-CONTRACT") {
		t.Errorf("router got a contract override")
	}
}

func TestPromptReachRecordsAgents(t *testing.T) {
	contractFleet(t)
	inst := buildTestInstance(t)
	if got := inst.PromptReach["contract.root"]; !reflect.DeepEqual(got, []string{"leader"}) {
		t.Errorf("contract.root reach = %v", got)
	}
	if got := inst.PromptReach["contract.subagent"]; !reflect.DeepEqual(got, []string{"worker"}) {
		t.Errorf("contract.subagent reach = %v", got)
	}
}

func TestReloadDropsOverride(t *testing.T) {
	_, lo := contractFleet(t)
	writePromptFile(t, lo, `{"prompts": {"contract.root": "ROOT-CONTRACT"}}`)
	first := buildTestInstance(t)
	if err := os.Remove(filepath.Join(lo, "prompt_overrides.json")); err != nil {
		t.Fatal(err)
	}
	second := buildTestInstance(t)
	if got := agentInstruction(t, second.Squad("system").Leader); !strings.HasPrefix(got, agentkit.SystemPrompt) {
		t.Errorf("default contract not back after the file was removed")
	}
	if got := agentInstruction(t, first.Squad("system").Leader); !strings.HasPrefix(got, "ROOT-CONTRACT") {
		t.Errorf("the earlier generation's text changed")
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
