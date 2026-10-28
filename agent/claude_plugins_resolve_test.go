package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/paths"
)

// pluginResolveEnv isolates the 3-layer config chain for a plugin resolution
// test: a temp system layer holding agents.json (+ the `leader` agent) and
// models.json, an empty $OMNIS_HOME, the demo fixture as the Agent-Skills root,
// and no OMNIS_CONFIG_PATH (the developer's shell exports it; it bypasses the
// merge). It returns the system layer and the home dir.
func pluginResolveEnv(t *testing.T, agentsJSON, modelsJSON string) (sys, home string) {
	t.Helper()
	captureLog(t)
	t.Setenv("OMNIS_CONFIG_PATH", "")
	os.Unsetenv("OMNIS_CONFIG_PATH")
	fixture, err := filepath.Abs("../internal/claudeplugins/testdata/agentskills")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir()) // no .agents/ local layer
	sys, home = t.TempDir(), t.TempDir()
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_CONFIG_DIRS", "")
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sys)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", fixture)
	t.Cleanup(func() { paths.SetPluginSkillDirs(nil); claudeCatalog.Store(nil) })

	if agentsJSON == "" {
		agentsJSON = `{"agents": ["leader"], "router_squad": "none"}`
	}
	if modelsJSON == "" {
		modelsJSON = `{"providers": {"p": {"kind": "openai_compat", "base_url": "http://x", "api_key": "k"}},
			"models": {"hosted": {"provider_ref": "p", "model": "m1"}, "high": {"provider_ref": "p", "model": "m2"},
			"premium": {"provider_ref": "p", "model": "m3"}}}`
	}
	writeFile(t, filepath.Join(sys, "agents.json"), agentsJSON)
	writeFile(t, filepath.Join(sys, "models.json"), modelsJSON)
	writeFile(t, filepath.Join(sys, "registry", "agents", "leader", "agent.json"),
		`{"name":"leader","description":"Coordinator.","leader":true,"model_ref":"high"}`)
	return sys, home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resolvedAgent(t *testing.T, s RuntimeSettings, name string) (RuntimeAgentConfig, bool) {
	t.Helper()
	for _, a := range s.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return RuntimeAgentConfig{}, false
}

func TestResolveIncludesPluginAgentsAndSquad(t *testing.T) {
	pluginResolveEnv(t, "", "")
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct{ ref, typ string }{
		"demo_lead":   {"high", "demo:lead"},
		"demo_worker": {"hosted", "demo:worker"},
		"demo_reader": {"premium", "demo:reader"},
	}
	for name, w := range want {
		a, ok := resolvedAgent(t, s, name)
		if !ok {
			t.Fatalf("agent %s missing", name)
		}
		if a.Plugin != "demo" || a.PluginType != w.typ || a.ModelRef != w.ref || !a.Enabled {
			t.Errorf("%s = plugin %q type %q ref %q enabled %v; want demo %q %q", name, a.Plugin, a.PluginType, a.ModelRef, a.Enabled, w.typ, w.ref)
		}
	}
	if a, _ := resolvedAgent(t, s, "leader"); a.Plugin != "" {
		t.Errorf("an ordinary agent must carry no plugin, got %q", a.Plugin)
	}
	var found bool
	for _, sq := range s.Squads {
		if sq.Name == "demo" {
			found = true
			if sq.Leader != "demo_lead" || sq.Plugin != "demo" {
				t.Errorf("demo squad = %+v", sq)
			}
		} else if sq.Plugin != "" {
			t.Errorf("squad %s must carry no plugin", sq.Name)
		}
	}
	if !found {
		t.Fatalf("demo squad missing: %+v", s.Squads)
	}
	infos := ClaudePlugins()
	if len(infos) != 1 || infos[0].Name != "demo" || !infos[0].Enabled || infos[0].Squad != "demo" || len(infos[0].Agents) != 3 {
		t.Errorf("ClaudePlugins() = %+v", infos)
	}
}

func TestResolveHonoursDisabledPlugins(t *testing.T) {
	pluginResolveEnv(t, `{"agents": ["leader"], "router_squad": "none", "disabled_plugins": ["demo"]}`, "")
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range s.Agents {
		if strings.HasPrefix(a.Name, "demo_") {
			t.Errorf("disabled plugin agent %s resolved", a.Name)
		}
	}
	for _, sq := range s.Squads {
		if sq.Name == "demo" {
			t.Error("disabled plugin squad resolved")
		}
	}
	if len(s.PluginHookLayers) != 0 {
		t.Errorf("disabled plugin contributed hooks: %+v", s.PluginHookLayers)
	}
	if !IsPluginAgent("demo_lead") {
		t.Error("IsPluginAgent must stay true for a disabled plugin's agent")
	}
	if got := PluginAgentType("demo_lead"); got != "" {
		t.Errorf("PluginAgentType of a disabled plugin agent = %q, want empty", got)
	}
	infos := ClaudePlugins()
	if len(infos) != 1 || infos[0].Enabled {
		t.Errorf("ClaudePlugins() = %+v, want demo listed as disabled", infos)
	}
}

func TestResolveClaudeModelMapOverride(t *testing.T) {
	pluginResolveEnv(t, "", `{"providers": {"p": {"kind": "openai_compat", "base_url": "http://x", "api_key": "k"}},
		"models": {"hosted": {"provider_ref": "p", "model": "m1"}, "high": {"provider_ref": "p", "model": "m2"},
		"premium": {"provider_ref": "p", "model": "m3"}},
		"claude_model_map": {"HAIKU": "premium"}}`)
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := resolvedAgent(t, s, "demo_worker"); a.ModelRef != "premium" {
		t.Errorf("demo_worker ModelRef = %q, want premium", a.ModelRef)
	}
	if a, _ := resolvedAgent(t, s, "demo_lead"); a.ModelRef != "high" {
		t.Errorf("unmapped aliases keep their default: demo_lead = %q", a.ModelRef)
	}
}

func TestResolveUserOverlayOverPluginAgent(t *testing.T) {
	_, home := pluginResolveEnv(t, "", "")
	writeFile(t, filepath.Join(home, "registry", "agents", "demo_worker", "agent.json"), `{"model_ref":"high"}`)
	writeFile(t, filepath.Join(home, "registry", "agents", "demo_worker", "instruction.md"), "Custom worker.\n")
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := resolvedAgent(t, s, "demo_worker")
	if !ok {
		t.Fatal("demo_worker missing")
	}
	if a.ModelRef != "high" {
		t.Errorf("overlay model_ref lost: %q", a.ModelRef)
	}
	if a.Description != "Makes the changes the lead hands over." {
		t.Errorf("description must come from the plugin, got %q", a.Description)
	}
	if a.Instruction != "Custom worker." {
		t.Errorf("the overlay instruction.md must win, got %q", a.Instruction)
	}
	if got := ReadAgentInstruction("demo_worker"); got != "Custom worker." {
		t.Errorf("ReadAgentInstruction = %q", got)
	}
	if a.Plugin != "demo" {
		t.Errorf("an overlaid plugin agent is still a plugin agent, got %q", a.Plugin)
	}
}

func TestResolvePluginNameCollisionKeepsOmnisAgent(t *testing.T) {
	sys, _ := pluginResolveEnv(t, `{"agents": ["leader", "demo_worker"], "router_squad": "none"}`, "")
	writeFile(t, filepath.Join(sys, "registry", "agents", "demo_worker", "agent.json"),
		`{"name":"demo_worker","description":"Mine.","model_ref":"high"}`)
	buf := captureLog(t)
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatalf("a name collision must not fail the config: %v", err)
	}
	n := 0
	for _, a := range s.Agents {
		if a.Name == "demo_worker" {
			n++
			if a.Description != "Mine." || a.Plugin != "" {
				t.Errorf("the omnis agent must win, got %+v", a)
			}
		}
	}
	if n != 1 {
		t.Errorf("demo_worker present %d times", n)
	}
	if !strings.Contains(buf.String(), "demo_worker") {
		t.Errorf("the skip must be logged, got %q", buf.String())
	}
}

func TestResolveSetsPluginSkillDirsAndHookLayers(t *testing.T) {
	pluginResolveEnv(t, "", "")
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(filepath.Join(os.Getenv("OMNIS_AGENTSKILLS_DIR"), "claude", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	dirs := paths.SkillsAllSearchDirs()
	if last := dirs[len(dirs)-1]; last != filepath.Join(dir, "skills") {
		t.Errorf("SkillsAllSearchDirs must end with the plugin skills dir, got %q", last)
	}
	want := []hooks.PluginLayer{{Name: "demo", Root: dir, Path: filepath.Join(dir, "hooks", "hooks.json")}}
	if !reflect.DeepEqual(s.PluginHookLayers, want) {
		t.Errorf("PluginHookLayers = %+v, want %+v", s.PluginHookLayers, want)
	}
}

func TestReadAgentInstructionFallsBackToPlugin(t *testing.T) {
	pluginResolveEnv(t, "", "")
	if _, err := ResolveRuntimeSettings(Options{}); err != nil {
		t.Fatal(err)
	}
	got := ReadAgentInstruction("demo_worker")
	if !strings.HasPrefix(got, "You make the change you are asked for") || !strings.Contains(got, "Running inside omnis") {
		t.Errorf("ReadAgentInstruction must return the converted instruction, got %q", got)
	}
	if below := ReadAgentInstructionBelowLayer("demo_worker", "user"); below != got {
		t.Errorf("below the user layer the plugin instruction is the baseline, got %q", below)
	}
}

func TestResolveMergesPluginSquadIntoAuthoredSquad(t *testing.T) {
	pluginResolveEnv(t, `{"agents": ["leader"], "router_squad": "none",
		"squads": [{"name": "demo", "description": "Mine."}]}`, "")
	s, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sq := range s.Squads {
		if sq.Name == "demo" {
			if sq.Description != "Mine." || sq.Leader != "demo_lead" || len(sq.Members) != 2 || sq.Plugin != "demo" {
				t.Errorf("merged squad = %+v", sq)
			}
			return
		}
	}
	t.Fatal("demo squad missing")
}

func TestPluginAgentBaseIsJSONMarshalable(t *testing.T) {
	pluginResolveEnv(t, "", "")
	if _, err := ResolveRuntimeSettings(Options{}); err != nil {
		t.Fatal(err)
	}
	e, ok := pluginAgentBase("demo_reader")
	if !ok || e.Name != "demo_reader" {
		t.Fatalf("pluginAgentBase = %+v, %v", e, ok)
	}
	if _, err := json.Marshal(e); err != nil {
		t.Fatal(err)
	}
	if _, ok := pluginAgentBase("nope"); ok {
		t.Error("an unknown agent has no base")
	}
}
