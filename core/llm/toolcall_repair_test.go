package llm

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestRepairToolCallsRewritesArgsBeforeTheCallerSeesThem(t *testing.T) {
	inner := &scriptedLLM{scripts: [][]*model.LLMResponse{{
		partial("thinking"),
		{TurnComplete: true, Content: &genai.Content{Role: "model", Parts: []*genai.Part{
			{Text: "ok"},
			{FunctionCall: &genai.FunctionCall{Name: "Edit", Args: map[string]any{"path": "a.go"}}},
			{FunctionCall: &genai.FunctionCall{Name: "Bash", Args: map[string]any{"path": "x"}}},
		}}},
	}}}
	var seen []string
	m := RepairToolCalls(inner, func(name string, args map[string]any) {
		seen = append(seen, name)
		if name == "Edit" {
			args["file_path"] = args["path"]
			delete(args, "path")
		}
	})
	var calls []*genai.FunctionCall
	n := 0
	for resp, err := range m.GenerateContent(context.Background(), toolRequest(), true) {
		if err != nil {
			t.Fatal(err)
		}
		n++
		for _, p := range resp.Content.Parts {
			if p.FunctionCall != nil {
				calls = append(calls, p.FunctionCall)
			}
		}
	}
	if n != 2 {
		t.Fatalf("responses yielded = %d, want 2 (nothing dropped)", n)
	}
	if len(seen) != 2 {
		t.Fatalf("repair saw %v, want every function call", seen)
	}
	if calls[0].Args["file_path"] != "a.go" || calls[0].Args["path"] != nil {
		t.Fatalf("Edit args not repaired: %#v", calls[0].Args)
	}
	if calls[1].Args["path"] != "x" {
		t.Fatalf("Bash args touched: %#v", calls[1].Args)
	}
	if RepairToolCalls(inner, nil) != model.LLM(inner) {
		t.Fatal("a nil repair must return the inner model unchanged")
	}
}
