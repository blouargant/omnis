package agentkit

import (
	"strings"
	"testing"
)

// A sub-agent runs once and returns: nobody can answer it. The root's step 1
// ("restate, then confirm scope") made a research_critic ask for confirmation
// instead of reviewing, so the sub-agent contract must not carry it.
func TestSubAgentPromptDoesNotAskForConfirmation(t *testing.T) {
	for _, banned := range []string{"RESTATE", "confirm scope", "<acknowledgement>", "%STEP1%", "to the user and ask", "before asking the user"} {
		if strings.Contains(SubAgentSystemPrompt, banned) {
			t.Errorf("SubAgentSystemPrompt still contains %q", banned)
		}
	}
	if !strings.Contains(SubAgentSystemPrompt, "START WORKING IMMEDIATELY") {
		t.Error("SubAgentSystemPrompt lacks the sub-agent step 1")
	}
}

func TestRootPromptKeepsRestatement(t *testing.T) {
	if strings.Contains(SystemPrompt, "%STEP1%") || !strings.Contains(SystemPrompt, "RESTATE the user's request") {
		t.Fatal("root SystemPrompt lost its restatement step")
	}
	if !strings.Contains(SystemPrompt, "surface\n     it to the user and ask how to proceed.") {
		t.Fatal("root SystemPrompt step 6 changed")
	}
}
