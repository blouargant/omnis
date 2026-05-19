package compress

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"

	"github.com/blouargant/yoke/core/events"
)

// countingLLM wraps a fixed summary and counts how many times
// GenerateContent is invoked. Used to verify the per-session summary
// cache short-circuits repeated requests.
type countingLLM struct {
	summary string
	calls   atomic.Int32
}

func (c *countingLLM) Name() string { return "counting" }

func (c *countingLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	c.calls.Add(1)
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: c.summary}}},
		}, nil)
	}
}

func TestSummaryCacheReuse(t *testing.T) {
	t.Parallel()
	llm := &countingLLM{summary: "(cached summary)"}
	cfg := Config{
		WindowTokens:    1000,
		SoftRatio:       0.3,
		HardRatio:       0.5,
		KeepHeadTurns:   1,
		KeepRecentTurns: 1,
		LLM:             llm,
		AuditPath:       filepath.Join(t.TempDir(), "audit.md"),
	}
	cfg.applyDefaults()
	mgr := newManager(cfg)

	build := func() []*genai.Content {
		blob := strings.Repeat("verbose tool output. ", 200)
		return []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "original goal"}}},
			{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "Bash", Args: map[string]any{"cmd": "ls"}}}}},
			{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "Bash", Response: map[string]any{"result": blob}}}}},
			{Role: "model", Parts: []*genai.Part{{Text: "wrapping up"}}},
			{Role: "user", Parts: []*genai.Part{{Text: "thanks"}}},
		}
	}

	st := mgr.state("u", "s")

	if _, applied := mgr.runPipeline(context.Background(), st, build(), true); applied == nil {
		t.Fatal("first pipeline run produced no compression")
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("expected 1 LLM call after first pass, got %d", got)
	}
	if _, applied := mgr.runPipeline(context.Background(), st, build(), true); applied == nil {
		t.Fatal("second pipeline run produced no compression")
	}
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("expected cache hit on second pass (still 1 LLM call), got %d", got)
	}
}

func TestEventBusEmitsCompression(t *testing.T) {
	t.Parallel()
	bus := events.NewBus()
	got := map[string]map[string]any{}
	for _, name := range []string{events.EventCompressionStart, events.EventCompressionEnd, events.EventCompressionSkipped} {
		n := name
		bus.On(n, func(_ string, p map[string]any) { got[n] = p })
	}

	llm := &stubLLM{summary: "(stub)"}
	cfg := Config{
		WindowTokens:    1000,
		SoftRatio:       0.3,
		HardRatio:       0.5,
		KeepHeadTurns:   1,
		KeepRecentTurns: 1,
		LLM:             llm,
		EventBus:        bus,
		AuditPath:       filepath.Join(t.TempDir(), "audit.md"),
	}
	cfg.applyDefaults()
	mgr := newManager(cfg)

	blob := strings.Repeat("verbose tool output. ", 200)
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "original goal"}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "Bash", Args: map[string]any{"cmd": "ls"}}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "Bash", Response: map[string]any{"result": blob}}}}},
		{Role: "model", Parts: []*genai.Part{{Text: "wrapping up"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "thanks"}}},
	}
	req := &model.LLMRequest{Contents: contents}
	if _, err := mgr.compressOnce(context.Background(), "u", "s", req); err != nil {
		t.Fatalf("compressOnce error = %v", err)
	}
	if _, ok := got[events.EventCompressionStart]; !ok {
		t.Fatal("missing compression_start event")
	}
	end, ok := got[events.EventCompressionEnd]
	if !ok {
		t.Fatal("missing compression_end event")
	}
	if end["tokens_after"].(int) >= end["tokens_before"].(int) {
		t.Errorf("expected tokens_after < tokens_before, got %v < %v", end["tokens_after"], end["tokens_before"])
	}
}

// jsonLLM returns a fixed JSON document for every call. Used by State Log
// extractor tests.
type jsonLLM struct {
	body  string
	calls atomic.Int32
}

func (j *jsonLLM) Name() string { return "json-stub" }

func (j *jsonLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	j.calls.Add(1)
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: j.body}}},
		}, nil)
	}
}

func TestStateLogExtractAndPersist(t *testing.T) {
	t.Parallel()
	logPath := filepath.Join(t.TempDir(), "statelog.json")
	body := `{"goal":"ship feature X","decisions":["use Go"],"files":{"main.go":"entrypoint"},"tools":{"Read":2}}`
	llm := &jsonLLM{body: body}

	cfg := Config{
		WindowTokens:    1000,
		SoftRatio:       0.3,
		HardRatio:       0.5,
		KeepHeadTurns:   1,
		KeepRecentTurns: 1,
		LLM:             llm,
		StateLogLLM:     llm,
		StateLogEvery:   1,
		StateLogPath:    logPath,
		AuditPath:       filepath.Join(t.TempDir(), "audit.md"),
	}
	cfg.applyDefaults()
	mgr := newManager(cfg)

	st := mgr.state("u", "s")
	st.recentUserTurns = []string{"please ship feature X using Go"}

	mgr.maybeRefreshStateLog(context.Background(), "u", "s", st)
	if got := llm.calls.Load(); got != 1 {
		t.Fatalf("expected 1 extractor call, got %d", got)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("statelog file not written: %v", err)
	}
	var got2 StateLog
	if err := json.Unmarshal(data, &got2); err != nil {
		t.Fatalf("statelog json invalid: %v", err)
	}
	if got2.Goal != "ship feature X" {
		t.Errorf("goal not persisted, got %q", got2.Goal)
	}
	if got2.Files["main.go"] != "entrypoint" {
		t.Errorf("files map not persisted, got %+v", got2.Files)
	}

	prefix := st.stateLog.log.renderForPrompt()
	if !strings.Contains(prefix, "ship feature X") {
		t.Errorf("renderForPrompt missing goal: %q", prefix)
	}
}
