package agent

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/blouargant/omnis/core/agentkit"
	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/skills"
)

var updateGolden = flag.Bool("update", false, "rewrite agent/testdata/prompts_golden")

// goldenDefaults renders every overridable shared text with default settings.
// The files under testdata/prompts_golden were written before overrides
// existed: with no prompt_overrides.json, these texts must stay byte-identical.
func goldenDefaults() map[string]string {
	rt := RuntimeSettings{}
	fake := []RuntimeAgentConfig{{Name: "fake", Enabled: true, Description: "A fake sub-agent."}}
	return map[string]string{
		"contract_root":      agentkit.SystemPrompt,
		"contract_subagent":  agentkit.SubAgentSystemPrompt,
		"choice_handsback":   choicePolicyBlock(true),
		"choice":             choicePolicyBlock(false),
		"language_delegates": languagePolicyBlock(true),
		"language":           languagePolicyBlock(false),
		"steering":           steeringAwarenessBlock(),
		"handoff":            routerHandoffProtocolBlock(),
		"subagents":          buildSubAgentCapabilitiesBlock(fake, rt),
		"skills_rules":       skills.DefaultInstruction,
		"memory_rules":       collmem.Rules,
		"memory_reminder":    memoryTurnReminder,
	}
}

func TestPromptGoldenDefaults(t *testing.T) {
	dir := filepath.Join("testdata", "prompts_golden")
	for name, got := range goldenDefaults() {
		p := filepath.Join(dir, name+".txt")
		if *updateGolden {
			if err := os.WriteFile(p, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v (run with -update once)", name, err)
		}
		if string(want) != got {
			t.Errorf("%s differs from its golden text", name)
		}
	}
}
