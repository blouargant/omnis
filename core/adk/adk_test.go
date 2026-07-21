package adk

import (
	"context"
	"testing"

	"google.golang.org/genai"
)

// TestSkipSummarizationImpliesFinalResponse pins the exact host-side guarantee
// omnis's turn-termination depends on: an event whose SkipSummarization is set
// reports IsFinalResponse(), which is what stops the ADK flow loop. It is the
// v2 CANARY — ADK v2 drives even a plain LlmAgent through the workflow node
// runtime, and if that changes how a run ends, EndTurnAfterToolCall silently
// stops terminating runs and the router / budget cap / session-search would
// spin forever. If this test breaks at the v2 bump, the termination design
// (not just this package) needs rework.
func TestSkipSummarizationImpliesFinalResponse(t *testing.T) {
	// Build an event that is NON-final on its own merits: it carries a
	// function call, so IsFinalResponse()'s "no function calls" branch does
	// NOT fire. This is what makes the assertion below non-vacuous — the only
	// thing that can make this event final is SkipSummarization.
	ev := NewEvent(context.Background(), "canary")
	ev.Content = &genai.Content{
		Role:  "model",
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "route_to_squad"}}},
	}
	if ev.IsFinalResponse() {
		t.Fatal("precondition failed: an event carrying a function call must NOT be final until SkipSummarization is set; the canary would be vacuous otherwise")
	}

	ev.Actions.SkipSummarization = true
	if !ev.IsFinalResponse() {
		t.Fatal("SkipSummarization must force IsFinalResponse(): omnis relies on it to end a turn after route_to_squad / handoff_to_router / the budget cap / report_sessions")
	}
}

// TestNewEventThreadsContext smoke-tests the ctx-taking constructor wrapper
// (the v2-shaped API, already available in v1.5 as NewEventWithContext).
func TestNewEventThreadsContext(t *testing.T) {
	if ev := NewEvent(context.Background(), "inv-1"); ev == nil {
		t.Fatal("NewEvent returned nil")
	}
}
