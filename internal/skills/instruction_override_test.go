package skills

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"

	"github.com/blouargant/omnis/internal/paths"
)

func systemInstructionOf(t *testing.T, instruction string) string {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", t.TempDir())
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")
	writeSkill(t, filepath.Join(paths.SkillsRegistryWriteDir(), "demo"), "demo")
	ts, err := ToolsetWith(context.Background(), nil, instruction)
	if err != nil {
		t.Fatal(err)
	}
	pr, ok := ts.(interface {
		ProcessRequest(agent.Context, *model.LLMRequest) error
	})
	if !ok {
		t.Fatalf("toolset %T has no ProcessRequest", ts)
	}
	req := &model.LLMRequest{}
	if err := pr.ProcessRequest(nil, req); err != nil {
		t.Fatal(err)
	}
	if req.Config == nil || req.Config.SystemInstruction == nil {
		t.Fatal("no system instruction added")
	}
	return req.Config.SystemInstruction.Parts[0].Text
}

func TestToolsetWithInstructionOverride(t *testing.T) {
	if got := systemInstructionOf(t, "CUSTOM SKILL RULES"); !strings.Contains(got, "CUSTOM SKILL RULES") || strings.Contains(got, "MANDATORY") {
		t.Errorf("override not used: %q", got)
	}
	if got := systemInstructionOf(t, ""); !strings.Contains(got, DefaultInstruction[:60]) {
		t.Errorf("empty instruction must keep the default: %q", got)
	}
}
