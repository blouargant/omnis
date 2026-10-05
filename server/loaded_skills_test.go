package main

import "testing"

func TestLoadedSkillFromResponse(t *testing.T) {
	resp := map[string]any{
		"skill_name":        "k8s-triage",
		"instructions":      "# Triage",
		"frontmatter":       map[string]any{"name": "k8s-triage", "description": "Decide a fix"},
		"dependency_status": "kubectl missing",
	}
	s, ok := loadedSkillFromResponse("k8s_leader", "load_skill", resp)
	if !ok || s.Name != "k8s-triage" || s.Kind != "skill" || s.Description != "Decide a fix" ||
		s.Agent != "k8s_leader" || s.DependencyStatus != "kubectl missing" {
		t.Fatalf("got %+v ok=%v", s, ok)
	}
	if s, ok := loadedSkillFromResponse("leader", "load_softskill", resp); !ok || s.Kind != "softskill" {
		t.Fatalf("softskill: %+v ok=%v", s, ok)
	}
	// Other tools and failed loads (no instructions) are not recorded.
	if _, ok := loadedSkillFromResponse("leader", "list_skills", resp); ok {
		t.Error("list_skills recorded")
	}
	if _, ok := loadedSkillFromResponse("leader", "load_skill", map[string]any{"error": "skill not found"}); ok {
		t.Error("failed load recorded")
	}
}

func TestSystemSkillIsNotRecorded(t *testing.T) {
	resp := map[string]any{
		"skill_name":   "wrap-session",
		"instructions": "# Wrap Session",
		"frontmatter": map[string]any{
			"name":     "wrap-session",
			"metadata": map[string]any{"system": "true"},
		},
	}
	if _, ok := loadedSkillFromResponse("leader", "load_softskill", resp); ok {
		t.Fatal("a system skill must not be recorded for the skills dock")
	}
	resp["frontmatter"].(map[string]any)["metadata"] = map[string]any{"system": "false"}
	if _, ok := loadedSkillFromResponse("leader", "load_softskill", resp); !ok {
		t.Fatal("system: false must still be recorded")
	}
}
