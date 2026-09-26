package agent

import (
	"strings"
	"testing"
)

// The goal verdict is one token plus one sentence: the request must ask the model
// not to reason first. Reasoning models otherwise overrun the evaluator timeout
// (22 s on the hosted model for a 6k-char transcript, against a 30 s budget and a
// transcript cap of 16k), and every overrun stops the /goal loop.
func TestBuildGoalEvalRequestDisablesThinking(t *testing.T) {
	req := buildGoalEvalRequest("tests pass", "all green")
	tc := req.Config.ThinkingConfig
	if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 0 {
		t.Fatalf("want ThinkingBudget 0, got %+v", tc)
	}
}

func TestBuildGoalEvalRequestKeepsTranscriptTail(t *testing.T) {
	long := strings.Repeat("x", goalTranscriptCap*2) + "THE-END"
	txt := buildGoalEvalRequest("cond", "START"+long).Contents[0].Parts[0].Text
	if !strings.Contains(txt, "THE-END") || strings.Contains(txt, "START") {
		t.Fatal("transcript must be capped keeping the tail")
	}
	if !strings.Contains(txt, "COMPLETION CONDITION:\ncond") {
		t.Fatalf("condition missing:\n%.200s", txt)
	}
	empty := buildGoalEvalRequest("cond", "   ").Contents[0].Parts[0].Text
	if !strings.Contains(empty, "(no transcript text was produced this turn)") {
		t.Fatalf("empty transcript placeholder missing:\n%s", empty)
	}
}
