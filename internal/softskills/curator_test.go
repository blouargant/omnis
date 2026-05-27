package softskills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCuratePromptIncludesPaths(t *testing.T) {
	dir := t.TempDir()
	audit := filepath.Join(dir, "audit.md")
	state := filepath.Join(dir, "state.json")
	if err := os.WriteFile(audit, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := buildCuratePrompt(CurateInputs{
		AuditPath:      audit,
		StateLogPath:   state,
		AuthoredSkills: []string{"review: …", "agent-builder: …"},
	})
	for _, want := range []string{audit, state, "review:", "run_glob"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q\n----\n%s", want, got)
		}
	}
	if strings.Contains(got, "is missing") {
		t.Errorf("unexpected missing-file note when files exist:\n%s", got)
	}
}

func TestBuildCuratePromptFlagsMissingFiles(t *testing.T) {
	got := buildCuratePrompt(CurateInputs{
		AuditPath:    "/no/such/audit.md",
		StateLogPath: "/no/such/state.json",
	})
	if !strings.Contains(got, "is missing") {
		t.Errorf("expected missing-file note in:\n%s", got)
	}
}

func TestBuildCuratePromptHandlesEmptyInputs(t *testing.T) {
	got := buildCuratePrompt(CurateInputs{})
	for _, want := range []string{"(none provided)", "(none listed)"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// New sections (6, 7, 8) must still render — even with no Outcome
	// and no Stats — so the curator's instruction always has a slot to
	// look at.
	for _, want := range []string{"6. Reflector outcome", "7. Per-skill usage stats", "8. Skills the reflector tagged 'harmful'"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing section heading %q in:\n%s", want, got)
		}
	}
}

func TestBuildCuratePromptRendersOutcomeAndStats(t *testing.T) {
	stats := &Stats{Version: 1, Entries: map[string]*StatsEntry{
		"investigator/k8s-pod-evidence": {LoadedCount: 14, Helpful: 8, Harmful: 1, Neutral: 5},
		"wrap-session":                  {LoadedCount: 3, Helpful: 1, Neutral: 2},
	}}
	outcome := &Outcome{
		Success:    Positive,
		KeyInsight: "always validate after apply",
		Tags: map[string]string{
			"investigator/k8s-pod-evidence": "helpful",
			"wrap-session":                  "neutral",
			"investigator/log-grep":         "harmful",
		},
		TagReasons: map[string]string{
			"investigator/log-grep": "superseded by k8s-pod-evidence",
		},
	}
	got := buildCuratePrompt(CurateInputs{
		AuditPath:    "/tmp/audit.md",
		StateLogPath: "/tmp/state.json",
		Outcome:      outcome,
		Stats:        stats,
	})
	for _, want := range []string{
		"success=positive",
		"key_insight=\"always validate after apply\"",
		"investigator/k8s-pod-evidence: loaded=14 helpful=8 harmful=1 neutral=5",
		"wrap-session: loaded=3 helpful=1 harmful=0 neutral=2",
		"- investigator/log-grep: superseded by k8s-pod-evidence",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q\n----\n%s", want, got)
		}
	}
}

func TestTopStatsLinesOrderingAndLimit(t *testing.T) {
	s := &Stats{Entries: map[string]*StatsEntry{
		"a": {LoadedCount: 5},
		"b": {LoadedCount: 10},
		"c": {LoadedCount: 10},
		"d": {LoadedCount: 1},
	}}
	lines := topStatsLines(s, 2)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	// Highest count first; ties broken by key ascending.
	if !strings.HasPrefix(lines[0], "b:") || !strings.HasPrefix(lines[1], "c:") {
		t.Errorf("ordering wrong: %v", lines)
	}
}

func TestCuratorPromptIncludesGatingRules(t *testing.T) {
	// The role prompt itself is a constant; this sanity-checks that the
	// Phase 4 gating rules survived editing.
	for _, want := range []string{
		"Skip is the default",
		"harmful >= 3",
		"harmful > helpful",
		"wrong assumptions",
		"superseded",
		"success == positive",
		"key_insight",
	} {
		if !strings.Contains(CuratorPrompt, want) {
			t.Errorf("CuratorPrompt missing gating phrase %q", want)
		}
	}
}
