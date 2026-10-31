package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

// A LiteLLM gateway that does not know an Anthropic model fills a missing
// max_tokens with 4096 and cuts the reply. models.json `max_output_tokens`
// makes the adapter send its own ceiling.

func TestOpenAIMaxOutputTokensDefault(t *testing.T) {
	t.Parallel()

	r := (&openAI{model: "m"}).buildRequest(cacheTestRequest(), false)
	body, _ := json.Marshal(r)
	if strings.Contains(string(body), "max_tokens") {
		t.Fatalf("max_tokens sent with no max_output_tokens configured:\n%s", body)
	}

	r = (&openAI{model: "m", maxOutputTokens: 32000}).buildRequest(cacheTestRequest(), true)
	if r.MaxTokens == nil || *r.MaxTokens != 32000 {
		t.Fatalf("MaxTokens = %v, want 32000", r.MaxTokens)
	}
}

func TestOpenAIRequestMaxOutputTokensWins(t *testing.T) {
	t.Parallel()

	req := cacheTestRequest()
	req.Config.MaxOutputTokens = 200
	r := (&openAI{model: "m", maxOutputTokens: 32000}).buildRequest(req, false)
	if r.MaxTokens == nil || *r.MaxTokens != 200 {
		t.Fatalf("MaxTokens = %v, want the request's own 200", r.MaxTokens)
	}
}

func TestAnthropicMaxOutputTokensReplacesDefault(t *testing.T) {
	t.Parallel()

	req := &model.LLMRequest{
		Config:   &genai.GenerateContentConfig{},
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	if got := (&anthropic{model: "m"}).buildRequest(req, false).MaxTokens; got != anthropicDefaultMaxToks {
		t.Fatalf("MaxTokens = %d, want default %d", got, anthropicDefaultMaxToks)
	}
	if got := (&anthropic{model: "m", maxOutputTokens: 64000}).buildRequest(req, false).MaxTokens; got != 64000 {
		t.Fatalf("MaxTokens = %d, want 64000", got)
	}
}

func TestApplyModelPrefsMaxOutputTokens(t *testing.T) {
	t.Parallel()

	m := applyModelPrefs(NewOpenAI("m", "k", "https://gw.example"), Selection{MaxOutputTokens: 16000})
	if got := m.(*openAI).maxOutputTokens; got != 16000 {
		t.Fatalf("maxOutputTokens = %d, want 16000", got)
	}
}
