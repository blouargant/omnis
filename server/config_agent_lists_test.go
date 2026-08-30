package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// TestParsedAgentRoundTripPreservesNonEmptyTeams is the "real teeth" test
// requested alongside the derived-`model` fix: a byte-verbatim GET→PUT
// round trip for the two shipped agents that carry a genuine, non-empty
// `subagents` team (helper→session_search, research_critic→web_fetcher)
// must leave that team completely intact — no overlay file, no dropped
// entries. Silently stripping a gatherer team is a functional regression
// (it hands retrieval back to the expensive model — see the cleanAgent
// allowlist comment in server/config.go), unlike the inert `model` key.
func TestParsedAgentRoundTripPreservesNonEmptyTeams(t *testing.T) {
	sys, home := agentLayersSetup(t)
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"tools":["Read"]}`)
	seedSysAgentEntry(t, sys, "helper", `{"name":"helper","description":"Helper","enabled":true,"leader":false,"builtin":true,"allow_file_attachments":false,"skills":null,"tools":["Read"],"subagents":["session_search"]}`)
	seedSysAgentEntry(t, sys, "session_search", `{"name":"session_search","description":"Session search","enabled":true,"leader":false,"builtin":true,"allow_file_attachments":false,"tools":["Read"]}`)
	seedSysAgentEntry(t, sys, "research_critic", `{"name":"research_critic","description":"Research critic","enabled":true,"leader":false,"builtin":false,"allow_file_attachments":false,"skills":[],"tools":["Read"],"subagents":["web_fetcher"]}`)
	seedSysAgentEntry(t, sys, "web_fetcher", `{"name":"web_fetcher","description":"Web fetcher","enabled":true,"leader":false,"builtin":false,"allow_file_attachments":false,"tools":["Read"]}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"),
		[]byte(`{"agents":["leader","helper","session_search","research_critic","web_fetcher"],"squads":[{"name":"System","leader":"leader","members":["helper","research_critic"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	agents, _ := data["agents"].([]any)

	// Sanity: the GET must actually be showing the real teams (otherwise this
	// test would pass for the wrong reason).
	wantTeam := map[string]string{"helper": "session_search", "research_critic": "web_fetcher"}
	found := map[string]bool{}
	for _, raw := range agents {
		am, _ := raw.(map[string]any)
		name, _ := am["name"].(string)
		want, ok := wantTeam[name]
		if !ok {
			continue
		}
		subs, _ := am["subagents"].([]any)
		if len(subs) != 1 || subs[0] != want {
			t.Fatalf("GET did not surface %s's real team (want [%s]), got %v — fix the fixture, not the assertion", name, want, am["subagents"])
		}
		found[name] = true
	}
	if len(found) != 2 {
		t.Fatalf("expected to verify both helper and research_critic, found %v", found)
	}

	// Save the GET payload back completely unmodified.
	w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}

	// No overlay at all is the ideal outcome; if one exists, the team must
	// still be exactly what was shipped.
	for name, want := range wantTeam {
		overlayPath := filepath.Join(home, "registry/agents", name, "agent.json")
		b, err := os.ReadFile(overlayPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatalf("read overlay for %s: %v", name, err)
		}
		var m map[string]any
		if jsonErr := json.Unmarshal(b, &m); jsonErr != nil {
			t.Fatalf("overlay for %s is not JSON: %v", name, jsonErr)
		}
		if subs, ok := m["subagents"]; ok {
			list, _ := subs.([]any)
			if len(list) == 0 {
				t.Fatalf("no-op save STRIPPED %s's real team down to an empty overlay: %s", name, b)
			}
			if len(list) != 1 || list[0] != want {
				t.Fatalf("no-op save altered %s's team, got %v want [%s]", name, list, want)
			}
		}
	}

	// Confirm via a second GET that the effective (merged) config still
	// reports the real team — the end-to-end guarantee, not just "no file
	// was written".
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("second get parsed: %d %s", w.Code, w.Body.String())
	}
	data2 := parsedAgentData(t, w.Body.String())
	agents2, _ := data2["agents"].([]any)
	for _, raw := range agents2 {
		am, _ := raw.(map[string]any)
		name, _ := am["name"].(string)
		want, ok := wantTeam[name]
		if !ok {
			continue
		}
		subs, _ := am["subagents"].([]any)
		if len(subs) != 1 || subs[0] != want {
			t.Fatalf("%s's team did not survive the round trip: want [%s], got %v", name, want, am["subagents"])
		}
	}
}

// TestParsedAgentRoundTripDropsSpuriousEmptyListsForNoTeamAgent covers the
// exact defect observed live: an agent with NO team at all (shipped
// `skills: null`, no `subagents` key — the shape of web_agent/omnis) came
// back from a save with a user-layer overlay of
// `{"skills": [], "subagents": []}`.
//
// The web UI's agent-detail renderer used to materialise a rendering-only
// `[]` for `skills`/`subagents`/`mcp_servers` the moment an agent's detail
// view was opened — even without the user touching those sections — because
// it needs SOME array to seed its checkbox `Set` from
// (renderSkillBlockContent / renderAgentTeamBlock / renderAgentMCPBlockContent
// in web/settings.js, now fixed to use a non-mutating local instead). Because
// the Settings editor has no per-agent PATCH route (only a whole-document
// PUT), that materialised `[]` rode along on ANY subsequent save of the
// fleet — not just one that touched this agent's Skills/Team sections.
//
// This test does not depend on the JS fix: it simulates the client artifact
// directly in the PUT payload and asserts the SERVER (configedit.DiffGeneric,
// server/config.go) refuses to persist it — the backstop that holds
// regardless of what a client sends, current or future.
func TestParsedAgentRoundTripDropsSpuriousEmptyListsForNoTeamAgent(t *testing.T) {
	sys, home := agentLayersSetup(t)
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"tools":["Read"]}`)
	// web_agent's real shipped shape: skills explicitly null, no subagents key.
	seedSysAgentEntry(t, sys, "web_agent", `{"name":"web_agent","description":"Web agent","enabled":true,"leader":false,"builtin":false,"allow_file_attachments":false,"skills":null,"tools":["Read"]}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"),
		[]byte(`{"agents":["leader","web_agent"],"squads":[{"name":"System","leader":"leader","members":["web_agent"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestEngine(t, editorFiles())
	w := do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
	}
	data := parsedAgentData(t, w.Body.String())
	agents, _ := data["agents"].([]any)
	for _, raw := range agents {
		am, _ := raw.(map[string]any)
		if am["name"] == "web_agent" {
			if _, ok := am["subagents"]; ok {
				t.Fatalf("GET must not surface a 'subagents' key for a no-team agent, got %v", am["subagents"])
			}
			if am["skills"] != nil {
				t.Fatalf("GET must surface 'skills' as null for this fixture, got %v — fix the fixture, not the assertion", am["skills"])
			}
			// Simulate the client rendering artifact: materialise empty
			// arrays for fields GET omitted/nulled, exactly like the
			// (now-fixed) web UI used to do merely by opening the agent's
			// detail view.
			am["skills"] = []any{}
			am["subagents"] = []any{}
		}
	}

	w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}

	overlayPath := filepath.Join(home, "registry/agents/web_agent/agent.json")
	b, err := os.ReadFile(overlayPath)
	if os.IsNotExist(err) {
		return // ideal: nothing written at all
	}
	if err != nil {
		t.Fatalf("read overlay: %v", err)
	}
	var m map[string]any
	if jsonErr := json.Unmarshal(b, &m); jsonErr != nil {
		t.Fatalf("overlay is not JSON: %v", jsonErr)
	}
	if _, ok := m["skills"]; ok {
		t.Fatalf("spurious empty 'skills' against a null base must not be persisted: %s", b)
	}
	if _, ok := m["subagents"]; ok {
		t.Fatalf("spurious empty 'subagents' against an absent base must not be persisted: %s", b)
	}
}
