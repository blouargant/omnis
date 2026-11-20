package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func lessonsFixture(t *testing.T) (home string, data map[string]any, r http.Handler) {
	t.Helper()
	sys, home := agentLayersSetup(t)
	seedSysAgentEntry(t, sys, "leader", `{"name":"leader","description":"Leader","enabled":true,"leader":true,"builtin":true,"allow_file_attachments":false,"tools":["Read"]}`)
	seedSysAgentEntry(t, sys, "quiet", `{"name":"quiet","description":"Quiet","enabled":true,"leader":false,"builtin":true,"allow_file_attachments":false,"tools":["Read"],"lessons":false}`)
	if err := os.WriteFile(filepath.Join(sys, "agents.json"),
		[]byte(`{"agents":["leader","quiet"],"squads":[{"name":"System","leader":"leader","members":["quiet"]}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(t, editorFiles())
	w := do(t, eng, http.MethodGet, "/api/config/parsed/agent", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get parsed: %d %s", w.Code, w.Body.String())
	}
	return home, parsedAgentData(t, w.Body.String()), eng
}

// An agent with lessons switched off keeps `lessons: false` through a save of
// an unrelated field; the default (on) is never surfaced.
func TestParsedAgentRoundTripKeepsLessonsFalse(t *testing.T) {
	home, data, r := lessonsFixture(t)
	quiet, leader := agentByName(t, data, "quiet"), agentByName(t, data, "leader")
	if quiet["lessons"] != false {
		t.Fatalf("GET did not surface lessons:false: %v", quiet["lessons"])
	}
	if _, present := leader["lessons"]; present {
		t.Fatalf("GET surfaced the default: %v", leader["lessons"])
	}
	quiet["description"] = "Quiet (edited)"
	w := do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(home, "registry/agents/quiet/agent.json")); err == nil {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if _, present := m["lessons"]; present {
			t.Fatalf("unchanged lessons forked into the user overlay: %s", b)
		}
	}
	w = do(t, r, http.MethodGet, "/api/config/parsed/agent", nil)
	if got := agentByName(t, parsedAgentData(t, w.Body.String()), "quiet")["lessons"]; got != false {
		t.Fatalf("lessons:false lost after save: %v", got)
	}

	// Turning it off from the editor persists it.
	data = parsedAgentData(t, w.Body.String())
	agentByName(t, data, "leader")["lessons"] = false
	w = do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	b, err := os.ReadFile(filepath.Join(home, "registry/agents/leader/agent.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["lessons"] != false {
		t.Fatalf("lessons:false not persisted: %s", b)
	}
}

// An untouched GET→PUT writes no `lessons` key anywhere.
func TestParsedAgentRoundTripDoesNotForkLessons(t *testing.T) {
	home, data, r := lessonsFixture(t)
	w := do(t, r, http.MethodPut, "/api/config/parsed/agent", map[string]any{"data": data})
	if w.Code != http.StatusOK {
		t.Fatalf("put parsed: %d %s", w.Code, w.Body.String())
	}
	for _, name := range []string{"leader", "quiet"} {
		b, err := os.ReadFile(filepath.Join(home, "registry/agents", name, "agent.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("no-op save created a user overlay for %s: %s", name, b)
	}
}
