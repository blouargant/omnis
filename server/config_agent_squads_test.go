package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agentLayersSetup isolates the config chain: an empty cwd (no .agents layer),
// a temp system layer and a temp user layer, with every ambient override
// neutralised (OMNIS_CONFIG_PATH in particular would otherwise bypass the temp
// layers and resolve the developer's real /etc/omnis config).
func agentLayersSetup(t *testing.T) (sys, home string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	sys = t.TempDir()
	home = t.TempDir()
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sys)
	t.Setenv("OMNIS_HOME", home)
	t.Setenv("OMNIS_CONFIG_DIRS", "")
	t.Setenv("OMNIS_CONFIG_PATH", "")
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	return sys, home
}

func seedSysAgentEntry(t *testing.T, sys, name, agentJSON string) {
	t.Helper()
	dir := filepath.Join(sys, "registry/agents", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.json"), []byte(agentJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "instruction.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedTwoAgentFleet writes a minimal but fully resolvable fleet: a leader and
// one member, plus the given top-level agents.json body.
func seedTwoAgentFleet(t *testing.T, sys, agentsJSON string) {
	t.Helper()
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"tools":["Read"]}`)
	seedSysAgentEntry(t, sys, "helper", `{"name":"helper","description":"Helper","enabled":true,"leader":false,"builtin":true,"tools":["Read"]}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"), []byte(agentsJSON+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func parsedAgentData(t *testing.T, body string) map[string]any {
	t.Helper()
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decode parsed response: %v", err)
	}
	return resp.Data
}

// A squad that cannot resolve (leader is not an enabled agent) must surface as
// an explicit error. Before the fix the resolve error was swallowed and the
// handler returned the RAW agents value — a list of NAMES — which the editor
// rendered as a fleet of "(unnamed)" rows.
func TestParsedAgentInvalidConfigReturnsError(t *testing.T) {
	sys, _ := agentLayersSetup(t)
	seedTwoAgentFleet(t, sys, `{"agents":["leader","helper"],"squads":[{"name":"System","leader":"leader","members":["helper"]},{"name":"Fleet","leader":"conductor","members":[]}]}`)

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for an unresolvable config, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "conductor") {
		t.Fatalf("error must name the offending agent, got %s", w.Body.String())
	}
	// The name list must never reach the client as the `agents` payload.
	if strings.Contains(w.Body.String(), `"data"`) {
		t.Fatalf("must not return config data on a resolve error: %s", w.Body.String())
	}
}

// The parsed GET must hand back squads exactly as AUTHORED. The resolver
// normalises them (lower-cases the name, rewrites leader "none" → "", drops the
// leader from its own members) and the editor PUTs back whatever it got — so a
// normalised GET makes a no-op save fork the entire squad list into the user
// layer under new ids, plus a `squads_removed` tombstone for every shipped one.
func TestParsedAgentKeepsAuthoredSquads(t *testing.T) {
	sys, home := agentLayersSetup(t)
	seedTwoAgentFleet(t, sys, `{"agents":["leader","helper"],"squads":[{"name":"System","leader":"leader","members":["leader","helper"]},{"name":"Helper","leader":"none","members":["helper"],"hidden":true}]}`)

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	squads, _ := data["squads"].([]any)
	if len(squads) != 2 {
		t.Fatalf("want 2 squads, got %v", data["squads"])
	}
	first, _ := squads[0].(map[string]any)
	if first["name"] != "System" {
		t.Fatalf("squad name must keep its authored casing, got %v", first["name"])
	}
	if first["leader"] != "leader" {
		t.Fatalf("leader changed: %v", first["leader"])
	}
	second, _ := squads[1].(map[string]any)
	if second["name"] != "Helper" || second["leader"] != "none" {
		t.Fatalf("leaderless squad must round-trip verbatim, got %v", second)
	}
	if second["hidden"] != true {
		t.Fatalf("hidden flag must round-trip, got %v", second)
	}

	// Saving that payload back unchanged must not fork the squads into the
	// user layer, and must not tombstone the shipped ones.
	w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	userCfg, err := os.ReadFile(filepath.Join(home, "agents.json"))
	if err != nil {
		return // nothing written at all — the ideal no-op
	}
	var overlay map[string]any
	if err := json.Unmarshal(userCfg, &overlay); err != nil {
		t.Fatalf("user overlay is not JSON: %v", err)
	}
	if _, ok := overlay["squads"]; ok {
		t.Fatalf("no-op save forked the squad list into the user layer: %s", userCfg)
	}
	if _, ok := overlay["squads_removed"]; ok {
		t.Fatalf("no-op save tombstoned the shipped squads: %s", userCfg)
	}
}

// A PUT whose `agents` list holds plain NAMES (never agent objects) must be
// refused: the per-agent loop skips non-objects, so it would save an empty
// agents list and let the orphan sweep delete every per-user agent overlay.
func TestPutParsedAgentRefusesNameList(t *testing.T) {
	sys, home := agentLayersSetup(t)
	seedTwoAgentFleet(t, sys, `{"agents":["leader","helper"]}`)
	// A user-layer overlay that must survive the refused save.
	overlayDir := filepath.Join(home, "registry/agents/helper")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(overlayDir, "agent.json")
	if err := os.WriteFile(overlayPath, []byte(`{"model_ref":"high"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{
		"data": map[string]any{"agents": []any{"leader", "helper"}},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a name-only agents list, got %d body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(overlayPath); err != nil {
		t.Fatalf("refused save must not delete per-user agent overlays: %v", err)
	}
}
