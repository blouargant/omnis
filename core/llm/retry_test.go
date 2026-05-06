package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestOpenAIRetriesLiteLLMAnthropicOverload(t *testing.T) {
	defer setRetryBaseDelayForTest()()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", got)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"litellm.InternalServerError: AnthropicError - {\"type\":\"error\",\"error\": {\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	llm := NewOpenAI("test-model", "test-key", server.URL).(*openAI)
	resp, err := firstResponse(llm.GenerateContent(context.Background(), basicRequest(), false))
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if got := responseText(resp); got != "ok" {
		t.Fatalf("response text = %q, want ok", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestAnthropicRetriesOverload(t *testing.T) {
	defer setRetryBaseDelayForTest()()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/messages" {
			t.Fatalf("path = %q, want /messages", got)
		}
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
			return
		}
		fmt.Fprint(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	}))
	defer server.Close()

	llm := NewAnthropic("test-model", "test-key", server.URL).(*anthropic)
	resp, err := firstResponse(llm.GenerateContent(context.Background(), basicRequest(), false))
	if err != nil {
		t.Fatalf("GenerateContent() error = %v", err)
	}
	if got := responseText(resp); got != "ok" {
		t.Fatalf("response text = %q, want ok", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestOpenAIDoesNotRetryClientError(t *testing.T) {
	defer setRetryBaseDelayForTest()()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
	}))
	defer server.Close()

	llm := NewOpenAI("test-model", "test-key", server.URL).(*openAI)
	_, err := firstResponse(llm.GenerateContent(context.Background(), basicRequest(), false))
	if err == nil {
		t.Fatal("GenerateContent() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("error = %q, want 400 Bad Request", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}

func setRetryBaseDelayForTest() func() {
	original := generateRetryBaseDelay
	generateRetryBaseDelay = 0
	return func() { generateRetryBaseDelay = original }
}

func basicRequest() *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hello"}}}},
	}
}

func firstResponse(seq func(func(*model.LLMResponse, error) bool)) (*model.LLMResponse, error) {
	var got *model.LLMResponse
	var gotErr error
	seq(func(resp *model.LLMResponse, err error) bool {
		got = resp
		gotErr = err
		return false
	})
	return got, gotErr
}

func responseText(resp *model.LLMResponse) string {
	if resp == nil || resp.Content == nil {
		return ""
	}
	var out strings.Builder
	for _, part := range resp.Content.Parts {
		if part != nil {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}
