package configedit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeJSON writes a pretty JSON file, creating parent dirs.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSONMap(t *testing.T, path string) map[string]any {
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

// End-to-end proof of BOTH requested behaviours through the public API:
//  1. a save writes only the modified data to the user layer, and
//  2. the merged read evolves with package updates behind that user overlay.
func TestDeltaWriteThenPackageEvolution(t *testing.T) {
	userDir := t.TempDir()
	sysDir := t.TempDir()
	t.Setenv("OMNIS_HOME", userDir)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sysDir)
	// Ensure no stray project-local .agents interferes and the chain is default.
	t.Setenv("OMNIS_CONFIG_DIRS", "")

	// Package ships agents.json (system layer).
	writeJSON(t, filepath.Join(sysDir, "agents.json"), map[string]any{
		"agents": []any{"leader", "investigator"},
		"squads": []any{
			map[string]any{"name": "Default", "leader": "leader", "members": []any{"investigator"}},
		},
		"router_squad": "omnis",
	})

	// User makes one change in the UI: add a custom agent + tweak the squad.
	desired, _, _, err := ReadAgentsConfig()
	if err != nil {
		t.Fatal(err)
	}
	desired["agents"] = append(desired["agents"].([]any), "custom")
	desired["squads"] = []any{
		map[string]any{"name": "Default", "leader": "leader", "members": []any{"investigator", "custom"}},
	}
	if _, _, err := WriteSection("agent", desired); err != nil {
		t.Fatal(err)
	}

	// (1) Only the modified data is persisted to the user layer.
	overlay := readJSONMap(t, filepath.Join(userDir, "agents.json"))
	gotAgents := toStringSlice(overlay["agents"])
	if len(gotAgents) != 1 || gotAgents[0] != "custom" {
		t.Fatalf("user overlay agents should be only [custom]; got %v (full overlay: %v)", gotAgents, overlay)
	}
	if _, ok := overlay["router_squad"]; ok {
		t.Fatalf("unchanged router_squad must NOT be duplicated into the user overlay: %v", overlay)
	}

	// Merged view reflects the union + the user's squad edit.
	merged, _, err := LoadMergedSection("agents.json")
	if err != nil {
		t.Fatal(err)
	}
	if a := toStringSlice(merged["agents"]); !contains(a, "leader") || !contains(a, "custom") {
		t.Fatalf("merged agents should union package + user; got %v", a)
	}

	// (2) A package UPDATE adds a new agent + squad. It must surface through the
	// user overlay without any user action — the reported bug, fixed.
	writeJSON(t, filepath.Join(sysDir, "agents.json"), map[string]any{
		"agents": []any{"leader", "investigator", "newagent"},
		"squads": []any{
			map[string]any{"name": "Default", "leader": "leader", "members": []any{"investigator"}},
			map[string]any{"name": "Fresh", "leader": "leader", "members": []any{"newagent"}},
		},
		"router_squad": "omnis",
	})
	merged2, _, err := LoadMergedSection("agents.json")
	if err != nil {
		t.Fatal(err)
	}
	a2 := toStringSlice(merged2["agents"])
	if !contains(a2, "newagent") {
		t.Fatalf("package-added agent must surface behind the user overlay; got %v", a2)
	}
	if !contains(a2, "custom") {
		t.Fatalf("user's custom agent must still be present; got %v", a2)
	}
	// The user's Default squad edit still wins (members replace).
	squads, _ := merged2["squads"].([]any)
	var defMembers []string
	haveFresh := false
	for _, s := range squads {
		m := s.(map[string]any)
		switch m["name"] {
		case "Default":
			defMembers = toStringSlice(m["members"])
		case "Fresh":
			haveFresh = true
		}
	}
	if !haveFresh {
		t.Fatalf("package squad Fresh must surface after update; got %v", merged2["squads"])
	}
	if len(defMembers) != 2 {
		t.Fatalf("user's Default squad membership must win; got %v", defMembers)
	}
}

// Editing one field of a package-shipped agent writes only that field, and a
// later package update to the agent's OTHER fields flows through.
func TestPerAgentEntryDeltaAndEvolution(t *testing.T) {
	userDir := t.TempDir()
	sysDir := t.TempDir()
	t.Setenv("OMNIS_HOME", userDir)
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sysDir)
	t.Setenv("OMNIS_CONFIG_DIRS", "")

	// Package ships a built-in agent.
	writeJSON(t, filepath.Join(sysDir, "registry", "agents", "coder", "agent.json"), map[string]any{
		"name":      "coder",
		"builtin":   true,
		"leader":    true,
		"model_ref": "premium",
		"tools":     []any{"fs", "planning"},
	})

	// User changes only the model in the UI.
	entry, _, _, err := ReadAgentEntry("coder")
	if err != nil {
		t.Fatal(err)
	}
	entry["model_ref"] = "balanced"
	if _, _, err := WriteAgentEntry("coder", entry); err != nil {
		t.Fatal(err)
	}

	// Only model_ref (+ self-identifying name) should be in the user overlay.
	overlay := readJSONMap(t, filepath.Join(userDir, "registry", "agents", "coder", "agent.json"))
	if overlay["model_ref"] != "balanced" {
		t.Fatalf("overlay should carry model_ref override; got %v", overlay)
	}
	if _, ok := overlay["tools"]; ok {
		t.Fatalf("unchanged tools must NOT be duplicated into the overlay: %v", overlay)
	}

	// Package update: the built-in gains a new tool.
	writeJSON(t, filepath.Join(sysDir, "registry", "agents", "coder", "agent.json"), map[string]any{
		"name":      "coder",
		"builtin":   true,
		"leader":    true,
		"model_ref": "premium",
		"tools":     []any{"fs", "planning", "worktree"},
	})
	merged, _, _, err := ReadAgentEntry("coder")
	if err != nil {
		t.Fatal(err)
	}
	if merged["model_ref"] != "balanced" {
		t.Fatalf("user model override must win; got %v", merged["model_ref"])
	}
	tools := toStringSlice(merged["tools"])
	if !contains(tools, "worktree") {
		t.Fatalf("package-added tool must flow through the user overlay; got %v", tools)
	}
}
