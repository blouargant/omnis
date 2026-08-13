package agent

import (
	"strings"
	"testing"
)

// TestChoicePolicyBlockCarriesBothHalves pins the two halves of the rule, which
// pull in opposite directions and are only correct together: put a real choice in
// an AskUserQuestion menu (so the turn survives), but never use a menu to offload
// a decision the root itself owns.
func TestChoicePolicyBlockCarriesBothHalves(t *testing.T) {
	b := choicePolicyBlock(true)

	for _, want := range []string{
		"## Putting a choice to the user",
		"AskUserQuestion",
		`kind: "single"`,
		"allow_text",
		"ends your turn", // the reason prose is wrong
		"keeps the turn alive",
		"Only ask about what only the user can decide", // the guard
		"which squad or specialist should handle",
		"handoff_to_router", // handsBack half
	} {
		if !strings.Contains(b, want) {
			t.Errorf("choice policy block missing %q", want)
		}
	}
}

// TestChoicePolicyBlockOmitsHandoffWhenUnavailable checks a root without routing
// is not told to call a tool it does not have — an instruction naming a missing
// tool invites exactly the hallucinated call this codebase keeps guarding against.
func TestChoicePolicyBlockOmitsHandoffWhenUnavailable(t *testing.T) {
	b := choicePolicyBlock(false)

	if !strings.Contains(b, "AskUserQuestion") {
		t.Error("block missing the menu rule when routing is disabled")
	}
	if strings.Contains(b, "handoff_to_router") {
		t.Error("block names handoff_to_router on a root that has no such tool")
	}
}
