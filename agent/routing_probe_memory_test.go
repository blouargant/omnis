package agent

import (
	"strings"
	"testing"
)

// A probed lead sees only its instruction, not its tools, so without this note
// it declined "forget that…" (memory is not in any squad description), every
// squad declined, and the router spun until its output limit.
func TestCapabilityCheckPromptSaysEverySquadHasMemory(t *testing.T) {
	p := buildCapabilityCheckPrompt("OS administration.", "Oublie quel outil de suivi j'utilise.")
	if !strings.Contains(p, "remember, correct or forget a fact is within your scope") {
		t.Fatalf("probe prompt lacks the memory note:\n%s", p)
	}
}
