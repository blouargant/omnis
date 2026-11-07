package llm

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// cutScriptLLM answers each call with the next scripted response stream and
// records the requests.
type cutScriptLLM struct {
	script [][]*model.LLMResponse
	reqs   []*model.LLMRequest
}

func (s *cutScriptLLM) Name() string { return "scripted" }

func (s *cutScriptLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	s.reqs = append(s.reqs, req)
	resps := s.script[0]
	s.script = s.script[1:]
	return func(yield func(*model.LLMResponse, error) bool) {
		for _, r := range resps {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// cutReply is what an adapter yields for a reply cut mid tool call: the text
// streamed so far, the notice partial, then the final response.
func cutReply() []*model.LLMResponse {
	final := &model.LLMResponse{
		FinishReason:  genai.FinishReasonMaxTokens,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{CandidatesTokenCount: 32000},
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{
			{Text: "Writing the file."},
			{FunctionCall: &genai.FunctionCall{Name: "Write", Args: map[string]any{"_raw": `{"file_path":"a.go","content":"pack`}}},
		}},
	}
	notice := markTruncated("m", final)
	return []*model.LLMResponse{
		{Partial: true, Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "Writing the file."}}}},
		noticePartial(notice),
		final,
	}
}

func okReply() []*model.LLMResponse {
	return []*model.LLMResponse{{Content: &genai.Content{Role: "model", Parts: []*genai.Part{
		{FunctionCall: &genai.FunctionCall{Name: "Write", Args: map[string]any{"file_path": "a.go", "content": "package a"}}},
	}}}}
}

func toolsReq() *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "write a.go"}}}},
		Config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "Write"}}}}},
	}
}

func collectCut(t *testing.T, seq iter.Seq2[*model.LLMResponse, error]) []*model.LLMResponse {
	t.Helper()
	var out []*model.LLMResponse
	for r, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func joinedText(rs []*model.LLMResponse) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Content != nil {
			b.WriteString(contentText(r.Content))
		}
	}
	return b.String()
}

func TestCutToolCallIsRetriedInSmallerSteps(t *testing.T) {
	inner := &cutScriptLLM{script: [][]*model.LLMResponse{cutReply(), okReply()}}
	out := collectCut(t, RetryCutToolCall(inner).GenerateContent(context.Background(), toolsReq(), true))

	if len(inner.reqs) != 2 {
		t.Fatalf("model calls = %d, want 2", len(inner.reqs))
	}
	retry := inner.reqs[1]
	nudge := contentText(retry.Contents[len(retry.Contents)-1])
	if !strings.Contains(nudge, "`Write`") || !strings.Contains(nudge, "32000 tokens") || !strings.Contains(nudge, "smaller steps") {
		t.Fatalf("nudge = %q", nudge)
	}
	if len(retry.Contents) != 2 {
		t.Fatalf("retry contents = %d, want the original request + the nudge (the cut attempt discarded)", len(retry.Contents))
	}
	if strings.Contains(joinedText(out), TruncationNoticePrefix) {
		t.Fatal("the notice of the retried attempt was shown")
	}
	last := out[len(out)-1]
	if !hasFunctionCall(last.Content) {
		t.Fatal("the final response is not the retry's tool call")
	}
}

func TestSecondCutIsReturnedAsIs(t *testing.T) {
	inner := &cutScriptLLM{script: [][]*model.LLMResponse{cutReply(), cutReply()}}
	out := collectCut(t, RetryCutToolCall(inner).GenerateContent(context.Background(), toolsReq(), true))
	if len(inner.reqs) != 2 {
		t.Fatalf("model calls = %d, want 2 (one retry only)", len(inner.reqs))
	}
	if !strings.Contains(joinedText(out), TruncationNoticePrefix) {
		t.Fatal("the second cut reply lost its notice")
	}
	if last := out[len(out)-1]; last.Partial || last.FinishReason != genai.FinishReasonMaxTokens {
		t.Fatalf("last response = %+v, want the cut final", last)
	}
}

func TestCutWithoutToolsOrToolCallPassesThrough(t *testing.T) {
	// A request without tools is not wrapped.
	inner := &cutScriptLLM{script: [][]*model.LLMResponse{cutReply()}}
	req := toolsReq()
	req.Config = nil
	collectCut(t, RetryCutToolCall(inner).GenerateContent(context.Background(), req, true))
	if len(inner.reqs) != 1 {
		t.Fatalf("model calls = %d, want 1", len(inner.reqs))
	}

	// A reply cut in plain text (no tool call dropped) is not retried here.
	final := &model.LLMResponse{FinishReason: genai.FinishReasonMaxTokens,
		Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: "long analysis"}}}}
	notice := markTruncated("m", final)
	inner = &cutScriptLLM{script: [][]*model.LLMResponse{{noticePartial(notice), final}}}
	out := collectCut(t, RetryCutToolCall(inner).GenerateContent(context.Background(), toolsReq(), true))
	if len(inner.reqs) != 1 || !strings.Contains(joinedText(out), TruncationNoticePrefix) {
		t.Fatalf("calls=%d text=%q", len(inner.reqs), joinedText(out))
	}
}
