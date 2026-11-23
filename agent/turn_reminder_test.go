package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// An agent's turn_reminder goes on every typed user message, as a late part,
// never on a tool result or the compression summary, never twice, and it
// coexists with the memory reminder whichever runs first.
func TestAppendTurnReminder(t *testing.T) {
	const note = "[Delegation — ask the investigator first.]"
	contents := []*genai.Content{
		content("user", &genai.Part{Text: "## Compressed history (auto-generated)\n…"}),
		content("user", &genai.Part{Text: "Why did the batch fail?"}),
		content("model", &genai.Part{FunctionCall: &genai.FunctionCall{Name: "Read"}}),
		content("user", &genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "Read"}}),
		content("user", &genai.Part{Text: "And the disk?"}),
	}
	appendTurnReminder(contents, note)
	appendMemoryReminder(contents)
	appendTurnReminder(contents, note)
	appendMemoryReminder(contents)
	count := func(c *genai.Content, text string) int {
		n := 0
		for _, p := range c.Parts {
			if p.Text == text {
				n++
			}
		}
		return n
	}
	for i, want := range []int{0, 1, 0, 0, 1} {
		if got := count(contents[i], note); got != want {
			t.Errorf("content %d carries the turn reminder %d times, want %d", i, got, want)
		}
		if got := count(contents[i], memoryTurnReminder); got != want {
			t.Errorf("content %d carries the memory reminder %d times, want %d", i, got, want)
		}
	}
	if contents[1].Parts[0].Text != "Why did the batch fail?" {
		t.Error("the user's words must stay the first part")
	}
	before := len(contents[1].Parts)
	appendTurnReminder(contents, "  ")
	if len(contents[1].Parts) != before {
		t.Error("an empty reminder must add nothing")
	}
}

// The shipped System leader carries a delegation reminder, and it reaches the
// runtime config.
func TestShippedLeaderHasADelegationReminder(t *testing.T) {
	var e AgentEntry
	if err := json.Unmarshal([]byte(readShippedAgentJSON(t, "leader")), &e); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"investigator", "same response"} {
		if !strings.Contains(e.TurnReminder, want) {
			t.Errorf("leader turn_reminder lacks %q: %q", want, e.TurnReminder)
		}
	}
}
