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
}
