package compress

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

// --- existing v1 invariants preserved ---

func TestAppendMemoryCreatesAndAppends(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "memory.md")
	if err := appendMemory(path, "first"); err != nil {
		t.Fatalf("appendMemory(first) error = %v", err)
	}
	if err := appendMemory(path, "second"); err != nil {
		t.Fatalf("appendMemory(second) error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile error = %v", err)
	}
	if got := string(data); got != "first\nsecond\n" {
		t.Fatalf("file contents = %q", got)
	}
}

func TestPluginAppliesDefaults(t *testing.T) {
	t.Parallel()
	p, tools, wait, err := PluginWithTools("compress", Config{})
	if err != nil {
		t.Fatalf("PluginWithTools error = %v", err)
	}
	if p == nil || wait == nil {
		t.Fatal("PluginWithTools returned nil plugin or wait")
	}
	if len(tools) != 1 || tools[0].Name() != "compact_now" {
		t.Fatalf("expected single compact_now tool, got %+v", tools)
	}
	wait()
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()
	c := Config{}
	c.applyDefaults()
	if c.WindowTokens != DefaultWindowTokens {
		t.Errorf("WindowTokens = %d", c.WindowTokens)
	}
	if c.SoftRatio != DefaultSoftRatio || c.HardRatio != DefaultHardRatio {
		t.Errorf("ratios soft=%v hard=%v", c.SoftRatio, c.HardRatio)
	}
	if c.KeepRecentTurns != DefaultKeepRecentTurns {
		t.Errorf("KeepRecentTurns = %d", c.KeepRecentTurns)
	}
	if c.AuditPathFunc == nil || c.AuditPathFunc("u", "s") != DefaultMemoryPath {
		t.Errorf("AuditPathFunc default not wired")
	}
}

// --- token counting ---

func TestCountText(t *testing.T) {
	t.Parallel()
	if CountText("") != 0 {
		t.Error("empty text should be 0 tokens")
	}
	if n := CountText("hello world"); n <= 0 {
		t.Errorf("hello world tokens = %d, want >0", n)
	}
}

func TestCountContents(t *testing.T) {
	t.Parallel()
	c := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hello"}}},
		{Role: "model", Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{Name: "ls", Args: map[string]any{"dir": "/tmp"}}},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{Name: "ls", Response: map[string]any{"result": "a\nb\nc"}}},
		}},
	}
	if n := CountContents(c); n <= 0 {
		t.Errorf("CountContents = %d, want >0", n)
	}
}

// --- passes ---

func TestPassDedupeToolCalls(t *testing.T) {
	t.Parallel()
	args := map[string]any{"path": "/tmp"}
	contents := []*genai.Content{
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "ls", Args: args}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "ls", Response: map[string]any{"result": "first listing"}}}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "ls", Args: args}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "ls", Response: map[string]any{"result": "second listing"}}}}},
		// recent turns (kept verbatim)
		{Role: "model", Parts: []*genai.Part{{Text: "done"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "ok"}}},
	}
	out := PassDedupeToolCalls(contents, 2)
	first := out[1].Parts[0].FunctionResponse.Response["result"].(string)
	if !strings.Contains(first, "deduped") {
		t.Errorf("first response not deduped: %q", first)
	}
	second := out[3].Parts[0].FunctionResponse.Response["result"].(string)
	if second != "second listing" {
		t.Errorf("second response was modified: %q", second)
	}
}

func TestPassTruncateToolResults(t *testing.T) {
	t.Parallel()
	big := strings.Repeat("x", 5000)
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "bash", Response: map[string]any{"result": big}}}}},
		{Role: "model", Parts: []*genai.Part{{Text: "ok"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "next"}}},
	}
	out := PassTruncateToolResults(1024)(contents, 2)
	resp := out[0].Parts[0].FunctionResponse.Response
	if t2, _ := resp["truncated"].(bool); !t2 {
		t.Errorf("payload not truncated: %v", resp)
	}
	if n, _ := resp["original_bytes"].(int); n != 5000 {
		t.Errorf("original_bytes = %v want 5000", resp["original_bytes"])
	}
}

func TestPassDropUnusedSkills(t *testing.T) {
	t.Parallel()
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hello"}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "load_skill", Args: map[string]any{"name": "pdf"}}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "load_skill", Response: map[string]any{"result": "pdf skill body"}}}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "load_skill", Args: map[string]any{"name": "review"}}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "load_skill", Response: map[string]any{"result": "review skill body"}}}}},
		{Role: "model", Parts: []*genai.Part{{Text: "applying review checklist now"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "go ahead"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "done"}}},
	}
	out := PassDropUnusedSkills(contents, 2)
	if len(out) != len(contents)-2 {
		t.Fatalf("expected %d turns after drop, got %d", len(contents)-2, len(out))
	}
	foundReview := false
	for _, c := range out {
		for _, p := range c.Parts {
			if p.FunctionCall != nil && p.FunctionCall.Name == "load_skill" {
				if p.FunctionCall.Args["name"] == "review" {
					foundReview = true
				}
				if p.FunctionCall.Args["name"] == "pdf" {
					t.Error("pdf load_skill should have been dropped")
				}
			}
		}
	}
	if !foundReview {
		t.Error("review load_skill should have been kept")
	}
}

func TestPassSummarizeMiddleFallback(t *testing.T) {
	t.Parallel()
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "original goal"}}},
		{Role: "model", Parts: []*genai.Part{{Text: "first reply"}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "ls"}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "ls", Response: map[string]any{"result": "x"}}}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "cat"}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "cat", Response: map[string]any{"result": "y"}}}}},
		{Role: "model", Parts: []*genai.Part{{Text: "almost done"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "good"}}},
	}
	out := PassSummarizeMiddle(2, nil)(contents, 2)
	if len(out) != 2+1+2 {
		t.Fatalf("expected head(2)+summary(1)+tail(2)=5 turns, got %d", len(out))
	}
	if out[0].Parts[0].Text != "original goal" {
		t.Errorf("head turn 0 mutated: %q", out[0].Parts[0].Text)
	}
	if out[len(out)-1].Parts[0].Text != "good" {
		t.Errorf("tail turn mutated: %q", out[len(out)-1].Parts[0].Text)
	}
	summary := out[2].Parts[0].Text
	if !strings.Contains(summary, "Compressed history") {
		t.Errorf("synthetic summary turn missing header: %q", summary)
	}
	if !strings.Contains(summary, "ls") || !strings.Contains(summary, "cat") {
		t.Errorf("extractive summary should mention ls/cat: %q", summary)
	}
}

// --- end-to-end pipeline ---

type stubLLM struct{ summary string }

func (s *stubLLM) Name() string { return "stub" }

func (s *stubLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: s.summary}}},
		}, nil)
	}
}

func TestPipelineEndToEnd(t *testing.T) {
	t.Parallel()
	auditPath := filepath.Join(t.TempDir(), "audit.md")
	cfg := Config{
		WindowTokens:       1000,
		SoftRatio:          0.3,
		HardRatio:          0.5,
		KeepHeadTurns:      1,
		KeepRecentTurns:    1,
		ToolResultMaxBytes: 64,
		LLM:                &stubLLM{summary: "(stub summary)"},
		AuditPath:          auditPath,
	}
	cfg.applyDefaults()
	mgr := newManager(cfg)

	bigBlob := strings.Repeat("verbose tool output. ", 200)
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "original goal"}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"cmd": "ls"}}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "bash", Response: map[string]any{"result": bigBlob}}}}},
		{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"cmd": "ls"}}}}},
		{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: "bash", Response: map[string]any{"result": bigBlob}}}}},
		{Role: "model", Parts: []*genai.Part{{Text: "wrapping up"}}},
		{Role: "user", Parts: []*genai.Part{{Text: "thanks"}}},
	}
	beforeHead := contents[0]
	beforeTail := contents[len(contents)-1]
	beforeCount := CountContents(contents)

	newContents, applied := mgr.runPipeline(context.Background(), contents, true)
	if applied == nil {
		t.Fatal("pipeline returned nil applied list, expected at least one pass")
	}
	if newContents[0] != beforeHead {
		t.Error("head turn replaced; expected pointer-identity preservation")
	}
	if newContents[len(newContents)-1] != beforeTail {
		t.Error("tail turn replaced; expected pointer-identity preservation")
	}
	if afterCount := CountContents(newContents); afterCount >= beforeCount {
		t.Errorf("token count did not drop: before=%d after=%d", beforeCount, afterCount)
	}
}

// --- concurrency: per-session isolation ---

func TestSessionIsolation(t *testing.T) {
	t.Parallel()
	mgr := newManager(Config{WindowTokens: 1000})
	mgr.cfg.applyDefaults()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := mgr.state("user", string(rune('a'+i%5)))
			st.lastTokenCount.Store(int64(i))
		}(i)
	}
	wg.Wait()
	count := 0
	mgr.sessions.Range(func(_, _ any) bool { count++; return true })
	if count != 5 {
		t.Errorf("expected 5 sessions, got %d", count)
	}
}

// --- task-switch sniffer ---

func TestMaybeMarkTaskSwitch(t *testing.T) {
	t.Parallel()
	st := &sessionState{}
	contents := []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "fix the auth bug in login.go"}}}}
	maybeMarkTaskSwitch(st, contents)
	if st.forceCompact.Load() {
		t.Error("first turn should not trigger task switch")
	}
	contents = append(contents,
		&genai.Content{Role: "model", Parts: []*genai.Part{{Text: "looking..."}}},
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: "also check login.go for token leaks"}}},
	)
	maybeMarkTaskSwitch(st, contents)
	if st.forceCompact.Load() {
		t.Error("same-file follow-up should not trigger switch")
	}
	contents = append(contents,
		&genai.Content{Role: "model", Parts: []*genai.Part{{Text: "ok"}}},
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: "now refactor billing/invoice.go to use the new API"}}},
	)
	maybeMarkTaskSwitch(st, contents)
	if !st.forceCompact.Load() {
		t.Error("unrelated imperative request should trigger task switch")
	}
}
