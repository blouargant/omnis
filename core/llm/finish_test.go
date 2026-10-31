package llm

import (
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

func collectStream(t *testing.T, run func(yield func(*model.LLMResponse, error) bool)) (partial string, final *model.LLMResponse) {
	t.Helper()
	run(func(r *model.LLMResponse, err error) bool {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		if r.Partial {
			for _, p := range r.Content.Parts {
				partial += p.Text
			}
		} else {
			final = r
		}
		return true
	})
	if final == nil {
		t.Fatal("no final response")
	}
	return partial, final
}

func finalText(r *model.LLMResponse) string {
	var b strings.Builder
	for _, p := range r.Content.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func hasCall(r *model.LLMResponse) bool {
	for _, p := range r.Content.Parts {
		if p.FunctionCall != nil {
			return true
		}
	}
	return false
}

func TestOpenAIStreamFinishStop(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	partial, final := collectStream(t, func(y func(*model.LLMResponse, error) bool) { (&openAI{}).streamSSE(strings.NewReader(sse), y) })
	if final.FinishReason != genai.FinishReasonStop {
		t.Fatalf("FinishReason = %q, want STOP", final.FinishReason)
	}
	if partial != "hello" || finalText(final) != "hello" {
		t.Fatalf("text changed on a normal stop: partial=%q final=%q", partial, finalText(final))
	}
}

func TestOpenAIStreamFinishLengthAddsNoticeAndDropsBrokenCall(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"web_agent\",\"arguments\":\"{\\\"request\\\": \\\"half\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4096,\"total_tokens\":4106}}\n\n" +
		"data: [DONE]\n\n"
	partial, final := collectStream(t, func(y func(*model.LLMResponse, error) bool) { (&openAI{}).streamSSE(strings.NewReader(sse), y) })
	if final.FinishReason != genai.FinishReasonMaxTokens {
		t.Fatalf("FinishReason = %q, want MAX_TOKENS", final.FinishReason)
	}
	if hasCall(final) {
		t.Fatal("an unfinished tool call was kept; it would run with broken arguments")
	}
	for name, s := range map[string]string{"streamed": partial, "final": finalText(final)} {
		if !strings.HasPrefix(s, "partial answer") || !strings.Contains(s, "output limit") || !strings.Contains(s, "4096") {
			t.Fatalf("%s text lacks the truncation notice: %q", name, s)
		}
	}
}

func TestOpenAINonStreamFinishLength(t *testing.T) {
	r := &oaiResponse{}
	r.Choices = append(r.Choices, struct {
		Message      oaiMessage `json:"message"`
		FinishReason string     `json:"finish_reason"`
	}{Message: oaiMessage{Role: "assistant", Content: "cut"}, FinishReason: "length"})
	out := (&openAI{}).fromResponse(r)
	if out.FinishReason != genai.FinishReasonMaxTokens || !strings.Contains(finalText(out), "output limit") {
		t.Fatalf("FinishReason=%q text=%q", out.FinishReason, finalText(out))
	}
}

func TestAnthropicStreamMaxTokens(t *testing.T) {
	sse := "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n" +
		"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"cut\"}}\n\n" +
		"event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"max_tokens\"},\"usage\":{\"output_tokens\":4096}}\n\n"
	_, final := collectStream(t, func(y func(*model.LLMResponse, error) bool) { (&anthropic{}).streamSSE(strings.NewReader(sse), y) })
	if final.FinishReason != genai.FinishReasonMaxTokens || !strings.Contains(finalText(final), "output limit") {
		t.Fatalf("FinishReason=%q text=%q", final.FinishReason, finalText(final))
	}
}

func TestAnthropicNonStreamEndTurn(t *testing.T) {
	out := (&anthropic{}).fromResponse(&antResponse{Content: []antContentBlk{{Type: "text", Text: "ok"}}, StopReason: "end_turn"})
	if out.FinishReason != genai.FinishReasonStop || finalText(out) != "ok" {
		t.Fatalf("FinishReason=%q text=%q", out.FinishReason, finalText(out))
	}
}

func TestTruncationKeepsCompleteCallAfterText(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"intro\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"Read\",\"arguments\":\"{\\\"file_path\\\":\\\"a\\\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n" +
		"data: [DONE]\n\n"
	_, final := collectStream(t, func(y func(*model.LLMResponse, error) bool) { (&openAI{}).streamSSE(strings.NewReader(sse), y) })
	ps := final.Content.Parts
	if len(ps) != 2 || !strings.HasPrefix(ps[0].Text, "intro") || !strings.Contains(ps[0].Text, "output limit") || ps[1].FunctionCall == nil {
		t.Fatalf("want [text+notice, call], got %d parts: %+v", len(ps), ps)
	}
	if strings.Contains(ps[0].Text, "dropped") {
		t.Fatal("a complete tool call was reported as dropped")
	}
}
