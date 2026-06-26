package agent

import (
	"context"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestSteerSessionContextRoundTrip(t *testing.T) {
	base := context.Background()
	if got := steerSessionID(base); got != "" {
		t.Fatalf("steerSessionID(empty) = %q, want \"\"", got)
	}
	ctx := WithSteerSession(base, "web-sess-1")
	if got := steerSessionID(ctx); got != "web-sess-1" {
		t.Fatalf("steerSessionID = %q, want web-sess-1", got)
	}
	// A blank id is a no-op (no value planted).
	if got := steerSessionID(WithSteerSession(base, "")); got != "" {
		t.Fatalf("blank WithSteerSession leaked a value: %q", got)
	}
}

func TestInjectSteeringMergesIntoTrailingUserContent(t *testing.T) {
	// Mid-tool-loop: the last content is the user turn carrying tool results.
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "do the thing"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "ok"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "tool result"}}},
	}}
	injectSteering(req, []string{"also check X"})

	if n := len(req.Contents); n != 3 {
		t.Fatalf("content count = %d, want 3 (merged, not appended)", n)
	}
	last := req.Contents[2]
	if len(last.Parts) != 2 {
		t.Fatalf("trailing user parts = %d, want 2", len(last.Parts))
	}
	if got := last.Parts[1].Text; got == "" || !contains(got, "also check X") {
		t.Fatalf("steering text not merged: %q", got)
	}
}

func TestInjectSteeringAppendsWhenTrailingNotUser(t *testing.T) {
	// Trailing content is a model turn (no user turn to merge into).
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "thinking"}}},
	}}
	injectSteering(req, []string{"note"})

	if n := len(req.Contents); n != 3 {
		t.Fatalf("content count = %d, want 3 (appended user turn)", n)
	}
	if req.Contents[2].Role != "user" {
		t.Fatalf("appended role = %q, want user", req.Contents[2].Role)
	}
}

func TestInjectSteeringNoNotesIsNoOp(t *testing.T) {
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
	}}
	injectSteering(req, nil)
	if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 1 {
		t.Fatalf("no-op violated: %+v", req.Contents)
	}
}

func TestLastAssistantText(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "task"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "first step"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "tool result"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "second step"}}},
	}
	if got := lastAssistantText(contents); got != "second step" {
		t.Fatalf("lastAssistantText = %q, want %q", got, "second step")
	}
	// No model turns → empty.
	if got := lastAssistantText([]*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}); got != "" {
		t.Fatalf("lastAssistantText(no model) = %q, want \"\"", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
