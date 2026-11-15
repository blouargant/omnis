package agent

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func eventWith(parts ...*genai.Part) *session.Event {
	ev := &session.Event{}
	ev.LLMResponse.Content = &genai.Content{Role: "user", Parts: parts}
	return ev
}

// A sub-agent whose last tool ends the turn (session_search's report_sessions)
// finishes on a function response, not on text. Its final_result must reach the
// caller: before, the Helper got "stopped without writing a result" for every
// session search run from a chat.
func TestSubAgentResultUsesFinalResultOfTurnEndingTool(t *testing.T) {
	ev := eventWith(&genai.Part{FunctionResponse: &genai.FunctionResponse{
		Name:     "report_sessions",
		Response: map[string]any{"ok": true, "count": 1, "final_result": "- fair-lioness: GPU prices"},
	}})
	if got := subAgentResultText(ev); got != "- fair-lioness: GPU prices" {
		t.Fatalf("got %q", got)
	}
}

func TestSubAgentResultPrefersText(t *testing.T) {
	ev := eventWith(&genai.Part{Text: "answer"})
	if got := subAgentResultText(ev); got != "answer" {
		t.Fatalf("got %q", got)
	}
}

// A run stopped by a cap ends on a refusal without final_result: keep the
// explicit "stopped without a result" message.
func TestSubAgentResultEmptyWithoutFinalResult(t *testing.T) {
	ev := eventWith(&genai.Part{FunctionResponse: &genai.FunctionResponse{
		Name: "WebSearch", Response: map[string]any{"output": "[tool budget exhausted]"},
	}})
	if got := subAgentResultText(ev); !strings.Contains(got, "stopped without writing a result") {
		t.Fatalf("got %q", got)
	}
	if got := subAgentResultText(nil); !strings.Contains(got, "stopped without writing a result") {
		t.Fatalf("nil: got %q", got)
	}
}
