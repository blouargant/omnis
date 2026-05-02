package stream

import (
	"bytes"
	"errors"
	"iter"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
)

func TestPrintSuppressesAggregatedFinalTextAfterPartial(t *testing.T) {
	t.Parallel()

	seq := iter.Seq2[*session.Event, error](func(yield func(*session.Event, error) bool) {
		yield(&session.Event{
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{Parts: []*genai.Part{{Text: "hel"}}},
				Partial: true,
			},
		}, nil)
		yield(&session.Event{
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{Parts: []*genai.Part{{Text: "hello"}}},
				Partial: false,
			},
		}, nil)
	})

	var buf bytes.Buffer
	if err := Print(&buf, seq); err != nil {
		t.Fatalf("Print() error = %v", err)
	}
	if got := buf.String(); got != "hel\n" {
		t.Fatalf("Print() output = %q", got)
	}
}

func TestPrintRendersToolCallsAndErrors(t *testing.T) {
	t.Parallel()

	seq := iter.Seq2[*session.Event, error](func(yield func(*session.Event, error) bool) {
		yield(&session.Event{
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "read", Args: map[string]any{"path": "demo.txt"}}}}},
			},
		}, nil)
		yield(&session.Event{
			LLMResponse: model.LLMResponse{
				Content: &genai.Content{Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "read", Response: map[string]any{"content": "ok"}}}}},
			},
		}, nil)
		yield(nil, errors.New("stop"))
	})

	var buf bytes.Buffer
	err := Print(&buf, seq)
	if err == nil || err.Error() != "stop" {
		t.Fatalf("Print() error = %v, want stop", err)
	}
	got := buf.String()
	if !bytes.Contains([]byte(got), []byte("[tool_call read")) || !bytes.Contains([]byte(got), []byte("[tool_result read")) {
		t.Fatalf("Print() output = %q", got)
	}
}
