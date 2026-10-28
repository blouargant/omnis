package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/paths"
)

// pluginServerEnv builds an isolated layer chain whose Agent-Skills root is the
// demo plugin fixture. agentsJSON/modelsJSON default to a minimal resolvable
// fleet.
func pluginServerEnv(t *testing.T, agentsJSON, modelsJSON string) (sys, home string) {
	t.Helper()
	// Resolve the fixture before agentLayersSetup moves the working directory.
	fixture, err := filepath.Abs("../internal/claudeplugins/testdata/agentskills")
	if err != nil {
		t.Fatal(err)
	}
	sys, home = agentLayersSetup(t)
	t.Setenv("OMNIS_AGENTSKILLS_DIR", fixture)
	// The fixture holds deliberately broken plugins; their discovery warnings
	// are expected noise.
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	os.Unsetenv("OMNIS_CONFIG_PATH")
	t.Cleanup(func() {
		// Re-resolve with the Agent-Skills layer blank so the published plugin
		// catalogue does not leak into the next test.
		os.Setenv("OMNIS_AGENTSKILLS_DIR", "")
		_, _ = agent.ResolveRuntimeSettings(agent.Options{ConfigPathStrict: true})
		paths.SetPluginSkillDirs(nil)
	})
	if agentsJSON == "" {
		agentsJSON = `{"agents":["leader"],"router_squad":"none"}`
	}
	if modelsJSON == "" {
		modelsJSON = `{"providers":{"p":{"kind":"openai_compat","base_url":"http://x","api_key":"k"}},` +
			`"models":{"hosted":{"provider_ref":"p","model":"m1"},"high":{"provider_ref":"p","model":"m2"},"premium":{"provider_ref":"p","model":"m3"}},` +
			`"claude_model_map":{"HAIKU":"hosted"}}`
	}
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Coordinator.","enabled":true,"leader":true,"model_ref":"high","allow_file_attachments":false}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"), []byte(agentsJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sys, "models.json"), []byte(modelsJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return sys, home
}

func agentByName(t *testing.T, data map[string]any, name string) map[string]any {
	t.Helper()
	list, _ := data["agents"].([]any)
	for _, raw := range list {
		if m, _ := raw.(map[string]any); m != nil && m["name"] == name {
			return m
		}
	}
	return nil
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

func TestParsedAgentGetMarksPluginAgents(t *testing.T) {
	pluginServerEnv(t, "", "")
	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	dw := agentByName(t, data, "demo_worker")
	if dw == nil || dw["plugin"] != "demo" {
		t.Fatalf("demo_worker must carry plugin=demo, got %v", dw)
	}
	if l := agentByName(t, data, "leader"); l == nil || l["plugin"] != nil {
		t.Fatalf("leader must carry no plugin key, got %v", l)
	}
}

func TestParsedAgentRoundTripDoesNotForkPluginAgents(t *testing.T) {
	_, home := pluginServerEnv(t, "", "")
	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	data := parsedAgentData(t, w.Body.String())
	if w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data}); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(home, "agents.json")); err == nil && strings.Contains(string(b), "demo") {
		t.Fatalf("user agents.json must not mention the plugin: %s", b)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "registry", "agents"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "demo_") {
			t.Fatalf("unchanged plugin agent forked an overlay: %s", e.Name())
		}
	}
	// A second GET still sees the plugin agents (nothing was tombstoned).
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if agentByName(t, parsedAgentData(t, w.Body.String()), "demo_worker") == nil {
		t.Fatal("demo_worker vanished after a no-op save")
	}
}

func TestParsedAgentEditForksOnlyTheDelta(t *testing.T) {
	_, home := pluginServerEnv(t, "", "")
	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	data := parsedAgentData(t, w.Body.String())
	agentByName(t, data, "demo_worker")["model_ref"] = "high"
	if w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data}); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	dir := filepath.Join(home, "registry", "agents", "demo_worker")
	got := readJSONFile(t, filepath.Join(dir, "agent.json"))
	if len(got) != 1 || got["model_ref"] != "high" {
		t.Fatalf("overlay must be exactly {model_ref:high}, got %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "instruction.md")); err == nil {
		t.Fatal("an unchanged instruction must not be forked")
	}
	for _, other := range []string{"demo_lead", "demo_reader"} {
		if _, err := os.Stat(filepath.Join(home, "registry", "agents", other)); err == nil {
			t.Fatalf("untouched plugin agent %s forked", other)
		}
	}
}

func TestSaveKeepsDisabledPluginOverrides(t *testing.T) {
	_, home := pluginServerEnv(t, `{"agents":["leader"],"router_squad":"none","disabled_plugins":["demo"]}`, "")
	overlay := filepath.Join(home, "registry", "agents", "demo_worker", "agent.json")
	if err := os.MkdirAll(filepath.Dir(overlay), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlay, []byte(`{"model_ref":"premium"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	if agentByName(t, data, "demo_worker") != nil {
		t.Fatal("a disabled plugin's agents must not be listed")
	}
	if w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data}); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(overlay); err != nil {
		t.Fatalf("the disabled plugin's override was swept: %v", err)
	}
	// disabled_plugins survives the save (it lives in the merged agents.json).
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	dp, _ := parsedAgentData(t, w.Body.String())["disabled_plugins"].([]any)
	if len(dp) != 1 || dp[0] != "demo" {
		t.Fatalf("disabled_plugins lost on save: %v", dp)
	}
}

func TestModelsRoundTripKeepsClaudeModelMap(t *testing.T) {
	pluginServerEnv(t, "", "")
	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/models", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	if w = do(t, r, http.MethodPut, "/api/config/parsed/models", map[string]any{"data": data}); w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	w = do(t, r, http.MethodGet, "/api/config/parsed/models", nil)
	m, _ := parsedAgentData(t, w.Body.String())["claude_model_map"].(map[string]any)
	if m["HAIKU"] != "hosted" {
		t.Fatalf("claude_model_map lost on save: %v", m)
	}
}
