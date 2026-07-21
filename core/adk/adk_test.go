package adk

import (
	"context"
	"testing"
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
	ev := NewEvent(context.Background(), "canary")
	ev.Actions.SkipSummarization = true
	if !ev.IsFinalResponse() {
		t.Fatal("SkipSummarization must imply IsFinalResponse(): omnis relies on it to end a turn after route_to_squad / handoff_to_router / the budget cap / report_sessions")
	}
}

// TestNewEventThreadsContext smoke-tests the ctx-taking constructor wrapper
// (the v2-shaped API, already available in v1.5 as NewEventWithContext).
func TestNewEventThreadsContext(t *testing.T) {
	if ev := NewEvent(context.Background(), "inv-1"); ev == nil {
		t.Fatal("NewEvent returned nil")
	}
}
