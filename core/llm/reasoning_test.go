package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// A zero thinking budget is how a caller says "answer directly, do not reason".
// On the OpenAI-compatible path that is reasoning_effort "none": reasoning models
// behind LiteLLM otherwise spend thousands of tokens thinking before a one-line
// answer (57 s instead of 0.9 s measured for the composer suggestion call).
func TestBuildRequestZeroThinkingBudgetDisablesReasoning(t *testing.T) {
	req := cacheTestRequest()
	req.Config.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)}
	o := &openAI{model: "m"}
	body, err := json.Marshal(o.buildRequest(req, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"reasoning_effort":"none"`) {
		t.Fatalf("want reasoning_effort none in %s", body)
	}
}

// No thinking config, or a non-zero budget, must leave the request unchanged —
// the no-op contract for every existing caller.
func TestBuildRequestWithoutZeroBudgetOmitsReasoningEffort(t *testing.T) {
	cases := map[string]*genai.ThinkingConfig{
		"no thinking config": nil,
		"nil budget":         {},
		"positive budget":    {ThinkingBudget: genai.Ptr[int32](1024)},
	}
	for name, tc := range cases {
		req := cacheTestRequest()
		req.Config.ThinkingConfig = tc
		o := &openAI{model: "m"}
		body, err := json.Marshal(o.buildRequest(req, false))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "reasoning_effort") {
			t.Errorf("%s: unexpected reasoning_effort in %s", name, body)
		}
	}
}
