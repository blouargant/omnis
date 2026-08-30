package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestParsedAgentRoundTripDoesNotForkModelField locks in the fix for a
// GET/PUT idempotence defect distinct from (but in the same family as) the
// squad-normalisation hazard covered by config_agent_squads_test.go.
//
// GET /api/config/parsed/agent annotates each agent with a "model" field —
// server/config.go sets it from RuntimeAgentConfig.Model, which is resolved
// from models.json via model_ref (agent/runtime_config.go
// resolveAgentEntries/withInheritedModels). AgentEntry — the actual on-disk
// agent.json schema — has NO Model field at all (see the doc comment on
// AgentEntry in agent/runtime_config.go: "Model selection is owned
// exclusively by models.json ... Older agent.json files may still carry
// provider/model/base_url/api_key fields; Go's JSON decoder silently drops
// them"). So "model" is purely a derived, display-only value, exactly like
// the sibling "source" and "recommended_model" fields the same GET handler
// already emits.
//
// Because the editor PUTs back verbatim what the GET handed it, and the
// per-agent delta writer (configedit.AgentEntryOverlayBytes) persists any
// key present in the desired document but absent from the merged base, a
// derived value that is always non-empty for a resolvable agent diffs as a
// deliberate override on EVERY save — forking every agent from the system
// layer into the user layer even when nothing was changed. This is the
// CLAUDE.md GOTCHA "a GET must return the AUTHORED config, never the
// RESOLVED one," previously fixed for squads; "model" was the one exception
// that leaked through that same handler.
func TestParsedAgentRoundTripDoesNotForkModelField(t *testing.T) {
	sys, home := agentLayersSetup(t)
	if err := os.WriteFile(filepath.Join(sys, "models.json"),
		[]byte(`{"models":{"premium":{"model":"claude-sonnet-4-6"}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Both agent.json fixtures explicitly declare every scalar the GET handler
	// surfaces (mirroring the shape of the real shipped registry/agents/*/agent.json
	// files) so the only possible source of a round-trip delta is the field under
	// test. A fixture that OMITS a boolean field such as allow_file_attachments
	// would itself manufacture a spurious diff — the generic structural diff
	// engine (configedit.diffValue) cannot distinguish "absent, defaults to
	// false" from "explicitly false" — which is a separate, pre-existing
	// limitation of DiffGeneric and not the defect this test targets.
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"model_ref":"premium","tools":["Read"]}`)
	// helper carries no model_ref of its own, so it INHERITS the leader's
	// resolved model (withInheritedModels) — exercising the case that made 23
	// of the 27 files observed live contain only `{"model": "..."}`.
	seedSysAgentEntry(t, sys, "helper", `{"name":"helper","description":"Helper","enabled":true,"leader":false,"builtin":true,"allow_file_attachments":false,"tools":["Read"]}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"),
		[]byte(`{"agents":["leader","helper"],"squads":[{"name":"System","leader":"leader","members":["helper"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	agents, _ := data["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("want 2 agents, got %v", data["agents"])
	}
	for _, raw := range agents {
		am, _ := raw.(map[string]any)
		if m, _ := am["model"].(string); m == "" {
			t.Fatalf("test setup did not reproduce a resolved model for agent %v (got %v) — fix the fixture, not the assertion", am["name"], am["model"])
		}
	}

	// Save that GET payload back completely unmodified. A true no-op save
	// must not create ANY per-user agent overlay file.
	w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}

	for _, name := range []string{"leader", "helper"} {
		overlayPath := filepath.Join(home, "registry/agents", name, "agent.json")
		b, err := os.ReadFile(overlayPath)
		if os.IsNotExist(err) {
			continue // the ideal no-op: nothing written at all
		}
		if err != nil {
			t.Fatalf("read overlay for %s: %v", name, err)
		}
		var m map[string]any
		if jsonErr := json.Unmarshal(b, &m); jsonErr != nil {
			t.Fatalf("overlay for %s is not JSON: %v", name, jsonErr)
		}
		if _, ok := m["model"]; ok {
			t.Fatalf("no-op save forked the derived 'model' field into the user layer for %s: %s", name, b)
		}
		t.Fatalf("no-op save created an unexpected user overlay for %s: %s", name, b)
	}
}
