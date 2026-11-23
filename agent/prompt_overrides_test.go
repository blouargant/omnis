package agent

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/genai"

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

func overrides(kv ...string) RuntimeSettings {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return RuntimeSettings{PromptOverrides: m}
}

func TestBlockOverridesKeepConditions(t *testing.T) {
	rt := overrides("block.choice", "CHOICE-RULE", "block.choice.handoff", "HANDOFF-SENTENCE",
		"block.language.delegates", "DELEGATES-RULE")
	if b := choicePolicyBlockFor(rt, "x", false); strings.Contains(b, "HANDOFF-SENTENCE") || !strings.Contains(b, "CHOICE-RULE") {
		t.Errorf("choice without handsBack: %q", b)
	}
	if b := choicePolicyBlockFor(rt, "x", true); !strings.Contains(b, "HANDOFF-SENTENCE") {
		t.Errorf("choice with handsBack lacks the handoff override: %q", b)
	}
	if b := languagePolicyBlockFor(rt, "x", false); strings.Contains(b, "DELEGATES-RULE") {
		t.Errorf("leaderless language block got the delegates override: %q", b)
	}
	if b := languagePolicyBlockFor(rt, "x", true); !strings.Contains(b, "DELEGATES-RULE") {
		t.Errorf("delegating language block lacks the delegates override: %q", b)
	}
}

func TestBlockOverridesReplaceText(t *testing.T) {
	cases := []struct {
		id     string
		render func(RuntimeSettings) string
		def    string
	}{
		{"block.choice", func(r RuntimeSettings) string { return choicePolicyBlockFor(r, "x", true) }, "call `AskUserQuestion`"},
		{"block.choice.handoff", func(r RuntimeSettings) string { return choicePolicyBlockFor(r, "x", true) }, "instead of asking the user what kind"},
		{"block.language", func(r RuntimeSettings) string { return languagePolicyBlockFor(r, "x", true) }, "Reply to the user in the language"},
		{"block.language.delegates", func(r RuntimeSettings) string { return languagePolicyBlockFor(r, "x", true) }, "Write your instructions to sub-agents"},
		{"block.language.evidence", func(r RuntimeSettings) string { return languagePolicyBlockFor(r, "x", true) }, "Do not translate evidence"},
		{"block.steering", func(r RuntimeSettings) string { return steeringAwarenessBlockFor(r, "x") }, "is the user's latest intent"},
		{"block.handoff", func(r RuntimeSettings) string { return routerHandoffProtocolBlockFor(r, "x") }, "The router gave you this conversation"},
		{"block.subagents", func(r RuntimeSettings) string {
			return buildSubAgentCapabilitiesBlockFor([]RuntimeAgentConfig{{Name: "fake", Enabled: true}}, r, "x")
		}, "A call is synchronous and one-shot"},
	}
	for _, c := range cases {
		marker := "MARKER-" + c.id
		got := c.render(overrides(c.id, marker))
		if !strings.Contains(got, marker) || strings.Contains(got, c.def) {
			t.Errorf("%s: override not applied or default kept:\n%s", c.id, got)
		}
		if def := c.render(RuntimeSettings{}); !strings.Contains(def, c.def) {
			t.Errorf("%s: default text missing", c.id)
		}
	}
}

func TestSubAgentBlockKeepsGeneratedList(t *testing.T) {
	cfgs := []RuntimeAgentConfig{{Name: "fake", Enabled: true, Description: "Finds things."}}
	got := buildSubAgentCapabilitiesBlockFor(cfgs, overrides("block.subagents", "NEW RULES"), "x")
	for _, s := range []string{"# Available Sub-Agents", "NEW RULES", "**fake**: Finds things."} {
		if !strings.Contains(got, s) {
			t.Errorf("block lacks %q:\n%s", s, got)
		}
	}
}

func TestBlockReachRecordsUsedParts(t *testing.T) {
	rt := RuntimeSettings{reach: newPromptReach()}
	choicePolicyBlockFor(rt, "solo", false)
	languagePolicyBlockFor(rt, "solo", false)
	got := rt.reach.snapshot()
	if len(got["block.choice.handoff"]) != 0 || len(got["block.language.delegates"]) != 0 {
		t.Errorf("a part not written was recorded: %v", got)
	}
	if !reflect.DeepEqual(got["block.choice"], []string{"solo"}) || !reflect.DeepEqual(got["block.language.evidence"], []string{"solo"}) {
		t.Errorf("reach = %v", got)
	}
}

func TestMemoryReminderOverride(t *testing.T) {
	contents := []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}}
	appendTurnReminder(contents, memoryReminderText(overrides("memory.reminder", "REMEMBER-THIS")))
	if got := contents[0].Parts[len(contents[0].Parts)-1].Text; got != "REMEMBER-THIS" {
		t.Errorf("reminder = %q", got)
	}
	if memoryReminderText(RuntimeSettings{}) != memoryTurnReminder {
		t.Errorf("default reminder changed")
	}
}

func TestMemoryOverridesSkipRouter(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	rt := RuntimeSettings{PromptOverrides: map[string]string{"memory.rules": "MEM-RULES"}, reach: newPromptReach()}
	if got := collectionCtxBlockWith("s", false, memoryRulesFor(rt, "leader")); !strings.Contains(got, "MEM-RULES") {
		t.Errorf("answering block lacks the rules override:\n%s", got)
	}
	if got := collectionCtxBlockWith("s", true, ""); strings.Contains(got, "MEM-RULES") {
		t.Errorf("router got the memory rules")
	}
	if got := rt.reach.snapshot()["memory.rules"]; !reflect.DeepEqual(got, []string{"leader"}) {
		t.Errorf("memory.rules reach = %v", got)
	}
}

// WebSearch's description depends on the search provider (Serper, SerpAPI,
// DuckDuckGo): one override would give every provider the same text, and the
// catalogue default would be whichever agent was built first.
func TestWebSearchIsNotOverridable(t *testing.T) {
	_, lo := promptLayers(t)
	writePromptFile(t, lo, `{"prompts": {"tool.WebSearch": "x"}}`)
	got, warns := resolvePromptOverrides()
	if len(got) != 0 || len(warns) != 1 {
		t.Errorf("tool.WebSearch accepted: %v %v", got, warns)
	}
}
