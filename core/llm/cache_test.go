package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func cacheTestRequest() *model.LLMRequest {
	return &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "you are a helpful agent"}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "hello"}}},
		},
	}
}

func lastPartCacheControl(t *testing.T, m oaiMessage) *oaiCacheControl {
	t.Helper()
	parts, ok := m.Content.([]oaiContentPart)
	if !ok {
		t.Fatalf("message content is %T, want []oaiContentPart", m.Content)
	}
	if len(parts) == 0 {
		t.Fatalf("message content has no parts")
	}
	return parts[len(parts)-1].CacheControl
}

func TestBuildRequestPromptCacheDisabled(t *testing.T) {
	t.Parallel()

	o := &openAI{model: "claude-via-litellm"}
	r := o.buildRequest(cacheTestRequest(), false)

	// With prompt caching off the system message stays a plain string and no
	// cache_control annotation is emitted anywhere.
	if _, ok := r.Messages[0].Content.(string); !ok {
		t.Fatalf("system content is %T, want plain string when prompt_cache is off", r.Messages[0].Content)
	}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), "cache_control") {
		t.Fatalf("request unexpectedly contains cache_control with prompt_cache off:\n%s", body)
	}
}

func TestBuildRequestPromptCacheEnabled(t *testing.T) {
	t.Parallel()

	o := &openAI{model: "claude-via-litellm", promptCache: true}
	r := o.buildRequest(cacheTestRequest(), false)

	if len(r.Messages) < 2 {
		t.Fatalf("expected at least system + user messages, got %d", len(r.Messages))
	}
	// (1) The system message (and therefore the tool catalogue, in Anthropic's
	// render order) carries an ephemeral breakpoint.
	if r.Messages[0].Role != "system" {
		t.Fatalf("messages[0].Role = %q, want system", r.Messages[0].Role)
	}
	if cc := lastPartCacheControl(t, r.Messages[0]); cc == nil || cc.Type != "ephemeral" {
		t.Fatalf("system message cache_control = %+v, want ephemeral", cc)
	}
	// (2) The final message carries one too, for incremental multi-turn reuse.
	if cc := lastPartCacheControl(t, r.Messages[len(r.Messages)-1]); cc == nil || cc.Type != "ephemeral" {
		t.Fatalf("final message cache_control = %+v, want ephemeral", cc)
	}

	body, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"cache_control":{"type":"ephemeral"}`) {
		t.Fatalf("serialized request missing ephemeral cache_control:\n%s", body)
	}
}

func TestMarkMessageCacheableSkipsEmptyContent(t *testing.T) {
	t.Parallel()

	// An assistant turn carrying only tool_calls has no textual content; it must
	// be left untouched (no array-form conversion, no breakpoint).
	m := oaiMessage{Role: "assistant", ToolCalls: []oaiToolCall{{ID: "1", Type: "function"}}}
	markMessageCacheable(&m)
	if m.Content != nil {
		t.Fatalf("empty-content message gained content %+v", m.Content)
	}
}
