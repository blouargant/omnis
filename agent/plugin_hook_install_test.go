package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/blouargant/omnis/internal/hooks"
)

// pluginBashHooks counts the PreToolUse Bash commands the process-wide engine
// currently holds; the demo fixture plugin contributes exactly one.
func pluginBashHooks(infra *Infrastructure) int {
	if infra.hooks.reloader == nil {
		return 0
	}
	return len(infra.hooks.reloader.Snapshot().Match(hooks.PreToolUse, "Bash"))
}

// bootPluginManager builds the first generation the way every surface does
// (BuildInfrastructure → BuildInstance → NewManager) with the demo plugin on.
func bootPluginManager(t *testing.T) (*Manager, *Infrastructure, string) {
	t.Helper()
	sys, _ := pluginResolveEnv(t, "", "")
	ctx := context.Background()
	infra, err := BuildInfrastructure(ctx, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { infra.Close() })
	first, err := BuildInstance(ctx, infra, Options{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return NewManager(infra, first), infra, sys
}

func TestBootInstallsPluginHookLayers(t *testing.T) {
	_, infra, _ := bootPluginManager(t)
	if n := pluginBashHooks(infra); n != 1 {
		t.Fatalf("boot generation: want the demo plugin's Bash hook, got %d commands", n)
	}
}

func TestFailedReloadKeepsPluginHookLayers(t *testing.T) {
	m, infra, sys := bootPluginManager(t)
	// The reload disables the plugin but its build then fails on a squad whose
	// leader has an unbuildable model — after the first squad already ran.
	writeFile(t, filepath.Join(sys, "agents.json"), `{"agents": ["leader", "badlead"], "router_squad": "none",
		"disabled_plugins": ["demo"],
		"squads": [{"name": "system", "leader": "leader"}, {"name": "zbad", "leader": "badlead"}]}`)
	writeFile(t, filepath.Join(sys, "models.json"), `{"providers": {"p": {"kind": "openai_compat", "base_url": "http://x", "api_key": "k"},
		"broken": {"kind": "bogus", "base_url": "http://x", "api_key": "k"}},
		"models": {"hosted": {"provider_ref": "p", "model": "m1"}, "high": {"provider_ref": "p", "model": "m2"},
		"premium": {"provider_ref": "p", "model": "m3"}, "bad": {"provider_ref": "broken", "model": "x"}}}`)
	writeFile(t, filepath.Join(sys, "registry", "agents", "badlead", "agent.json"),
		`{"name":"badlead","description":"Broken.","leader":true,"model_ref":"bad"}`)
	if _, err := m.Reload(context.Background(), Options{}); err == nil {
		t.Fatal("reload was expected to fail")
	}
	if n := pluginBashHooks(infra); n != 1 {
		t.Fatalf("a failed reload switched the plugin hook layers: %d Bash commands, want 1", n)
	}
}

func TestSuccessfulReloadAppliesPluginHookLayers(t *testing.T) {
	m, infra, sys := bootPluginManager(t)
	writeFile(t, filepath.Join(sys, "agents.json"), `{"agents": ["leader"], "router_squad": "none", "disabled_plugins": ["demo"]}`)
	if _, err := m.Reload(context.Background(), Options{}); err != nil {
		t.Fatal(err)
	}
	if n := pluginBashHooks(infra); n != 0 {
		t.Fatalf("the installed generation disabled the plugin, yet %d Bash commands remain", n)
	}
}
