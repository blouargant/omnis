package compress

import (
	"context"
	"errors"
	"iter"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func call(name string) *genai.Content {
	return &genai.Content{Role: "model", Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: name, Args: map[string]any{"x": name}}}}}
}

func result(name, out string) *genai.Content {
	return &genai.Content{Role: "user", Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{Name: name, Response: map[string]any{"result": out}}}}}
}

func text(role, s string) *genai.Content {
	return &genai.Content{Role: role, Parts: []*genai.Part{{Text: s}}}
}

// A summary replaces the middle, so a boundary that falls between a tool call
// and its result would leave an orphan result, which providers reject.
func TestSplitMiddleNeverSeparatesACallFromItsResult(t *testing.T) {
	contents := []*genai.Content{
		text("user", "goal"),
		call("a"), result("a", "1"), // head boundary (2) falls on this result
		call("b"), result("b", "2"),
		call("c"), result("c", "3"), // tail boundary (len-1) falls on this result
	}
	head, middle, tail := SplitMiddle(contents, 2, 1)
	if middle == nil {
		t.Fatal("no middle")
	}
	if hasFunctionResponse(middle[0]) {
		t.Fatalf("middle starts with a result whose call stayed in the head")
	}
	if hasFunctionResponse(tail[0]) {
		t.Fatalf("tail starts with a result whose call was summarised away")
	}
	if len(head) != 3 || len(tail) != 2 {
		t.Fatalf("head=%d tail=%d, want 3 and 2", len(head), len(tail))
	}
	// A conversation that is only call/result pairs too short to split
	// returns nothing rather than a broken split.
	if _, m, _ := SplitMiddle(contents[:4], 2, 1); m != nil {
		t.Fatalf("expected no middle, got %d contents", len(m))
	}
}

func TestLimitsKeepAReserveForTheReply(t *testing.T) {
	cases := []struct {
		window, hard, soft int
	}{
		{200_000, 184_000, 150_000}, // ratio and reserve agree
		{256_000, 235_520, 192_000}, // ratio is the tighter bound
		{32_000, 16_000, 13_043},    // reserve is the tighter bound
		{1_000, 920, 750},           // too small for the reserve: plain ratios
	}
	for _, c := range cases {
		cfg := Config{WindowTokens: c.window}
		cfg.applyDefaults()
		m := newManager(cfg)
		if got := m.hardLimit(); got != c.hard {
			t.Errorf("window %d: hard = %d, want %d", c.window, got, c.hard)
		}
		if got := m.softLimit(); got != c.soft {
			t.Errorf("window %d: soft = %d, want %d", c.window, got, c.soft)
		}
	}
}

// The system instruction and the tool declarations fill the window too.
func TestOverheadCountsTowardsTheTrigger(t *testing.T) {
	cfg := Config{WindowTokens: 2_000, KeepHeadTurns: 1, KeepRecentTurns: 1}
	cfg.applyDefaults()
	m := newManager(cfg)
	req := &model.LLMRequest{
		Contents: []*genai.Content{text("user", "goal"), text("model", "ok"), text("user", "next")},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: text("user", strings.Repeat("rule number one. ", 300)),
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
				{Name: "Bash", Description: strings.Repeat("runs a command. ", 150)},
			}}},
		},
	}
	if CountContents(req.Contents) >= m.softLimit() {
		t.Fatal("test setup: contents alone already over the soft limit")
	}
	if CountContents(req.Contents)+CountRequestOverhead(req) < m.softLimit() {
		t.Fatal("test setup: overhead too small to cross the soft limit")
	}
	_, _ = m.compressOnce(context.Background(), "u", "s", req)
	if got := int(m.state("u", "s").lastTokenCount.Load()); got < m.softLimit() {
		t.Fatalf("recorded %d tokens, want the overhead included (>= %d)", got, m.softLimit())
	}
}

func TestRecentCountSizesTheTailByTokens(t *testing.T) {
	cfg := Config{WindowTokens: 200_000, KeepRecentTurns: 2}
	cfg.applyDefaults()
	m := newManager(cfg)
	var contents []*genai.Content
	for range 20 {
		contents = append(contents, text("user", strings.Repeat("word ", 500))) // ~500 tokens each
	}
	if got := m.recentCount(contents, 0, 100_000); got != 2 {
		t.Fatalf("no token budget: got %d, want the turn floor 2", got)
	}
	got := m.recentCount(contents, 2_000, 100_000)
	if got < 4 || got > 5 {
		t.Fatalf("2k-token tail: got %d contents, want 4-5", got)
	}
	// Capped at a quarter of the target.
	if got := m.recentCount(contents, 1_000_000, 4_000); got > 3 {
		t.Fatalf("cap ignored: got %d contents", got)
	}
}

func TestIsContextOverflow(t *testing.T) {
	yes := []string{
		`openai: 400 {"error":{"code":"context_length_exceeded"}}`,
		"This model's maximum context length is 32768 tokens. However, you requested 40000 tokens",
		"anthropic: prompt is too long: 210000 tokens > 200000 maximum",
		"litellm.ContextWindowExceededError: ...",
		"The input token count (1200000) exceeds the maximum number of tokens allowed",
	}
	for _, s := range yes {
		if !IsContextOverflow(errors.New(s)) {
			t.Errorf("not detected: %q", s)
		}
	}
	no := []string{"HTTP 429 rate limited", "connection reset by peer", "invalid api key"}
	for _, s := range no {
		if IsContextOverflow(errors.New(s)) {
			t.Errorf("false positive: %q", s)
		}
	}
	if IsContextOverflow(nil) {
		t.Error("nil error detected as overflow")
	}
}

// recordingLLM answers every call and records the requests it saw.
type recordingLLM struct {
	reqs []*model.LLMRequest
}

func (c *recordingLLM) Name() string { return "counting" }

func (c *recordingLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	c.reqs = append(c.reqs, req)
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(&model.LLMResponse{Content: text("model", "recovered")}, nil)
	}
}

// On a context overflow the request is compacted and sent once more; the
// retry's answer replaces the error.
func TestOverflowCompactsAndRetriesOnce(t *testing.T) {
	llm := &recordingLLM{}
	cfg := Config{WindowTokens: 200_000, KeepHeadTurns: 1, KeepRecentTurns: 1, LLM: llm}
	cfg.applyDefaults()
	m := newManager(cfg)
	blob := strings.Repeat("verbose tool output. ", 2_000)
	req := &model.LLMRequest{Contents: []*genai.Content{
		text("user", "goal"),
		call("a"), result("a", blob),
		call("b"), result("b", blob),
		text("model", "working"),
		text("user", "continue"),
	}}
	before := CountContents(req.Contents)
	resp, err := m.recoverOverflow(context.Background(), "u", "s", req)
	if err != nil || resp == nil || resp.Content.Parts[0].Text != "recovered" {
		t.Fatalf("resp=%v err=%v, want the retry's answer", resp, err)
	}
	if after := CountContents(req.Contents); after >= before {
		t.Fatalf("contents not compacted: before=%d after=%d", before, after)
	}
	retry := llm.reqs[len(llm.reqs)-1]
	if retry != req {
		t.Fatal("the retry was not sent with the compacted request")
	}
	for i, c := range req.Contents {
		if hasFunctionResponse(c) && (i == 0 || !hasFunctionCall(req.Contents[i-1])) {
			t.Fatalf("content %d is a tool result without its call", i)
		}
	}
}

func hasFunctionCall(c *genai.Content) bool {
	for _, p := range c.Parts {
		if p != nil && p.FunctionCall != nil {
			return true
		}
	}
	return false
}

// When even the summariser is needed, a tight target forces it.
func TestOverflowSummarisesWhenTruncationIsNotEnough(t *testing.T) {
	llm := &recordingLLM{}
	cfg := Config{WindowTokens: 200_000, KeepHeadTurns: 1, KeepRecentTurns: 1, LLM: llm}
	cfg.applyDefaults()
	m := newManager(cfg)
	var contents []*genai.Content
	contents = append(contents, text("user", "goal"))
	for range 40 {
		contents = append(contents, text("model", strings.Repeat("long reasoning ", 300)), text("user", "go on"))
	}
	req := &model.LLMRequest{Contents: contents}
	if _, err := m.recoverOverflow(context.Background(), "u", "s", req); err != nil {
		t.Fatal(err)
	}
	if len(llm.reqs) != 2 {
		t.Fatalf("model calls = %d, want 2 (summary + retry)", len(llm.reqs))
	}
	if !strings.Contains(llm.reqs[0].Contents[0].Parts[0].Text, "## Next steps") {
		t.Fatal("summariser was not given the checkpoint prompt")
	}
	if len(req.Contents) != 3 {
		t.Fatalf("contents = %d, want head + summary + tail", len(req.Contents))
	}
}

func TestOverflowLeavesOtherErrorsAlone(t *testing.T) {
	cfg := Config{WindowTokens: 200_000, LLM: &recordingLLM{}}
	cfg.applyDefaults()
	m := newManager(cfg)
	resp, err := m.onModelError(nil, &model.LLMRequest{}, errors.New("HTTP 429 rate limited"))
	if resp != nil || err != nil {
		t.Fatalf("resp=%v err=%v, want nil/nil so the original error propagates", resp, err)
	}
}

func TestMaxSessionsBoundsState(t *testing.T) {
	cfg := Config{MaxSessions: 3}
	cfg.applyDefaults()
	m := newManager(cfg)
	for i := range 10 {
		m.state("u", string(rune('a'+i)))
	}
	n := 0
	m.sessions.Range(func(_, _ any) bool { n++; return true })
	if n != 3 {
		t.Fatalf("kept %d states, want 3", n)
	}
	if _, ok := m.sessions.Load("u\x00j"); !ok {
		t.Fatal("the newest state must be kept")
	}
}

// Sub-agent compaction writes no audit file: it would land in the working
// directory, which a read-only agent cannot write anyway.
func TestAgentCallbacksWriteNoAuditFile(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	before, onErr := AgentCallbacks(Config{WindowTokens: 2_000, KeepHeadTurns: 1, KeepRecentTurns: 1, LLM: &recordingLLM{}})
	if before == nil || onErr == nil {
		t.Fatal("nil callbacks")
	}
	var contents []*genai.Content
	contents = append(contents, text("user", "goal"))
	for range 10 {
		contents = append(contents, text("model", strings.Repeat("reasoning ", 200)), text("user", "go on"))
	}
	m := newManager(func() Config {
		c := Config{WindowTokens: 2_000, KeepHeadTurns: 1, KeepRecentTurns: 1, LLM: &recordingLLM{},
			AuditPathFunc: func(_, _ string) string { return "" }}
		c.applyDefaults()
		return c
	}())
	req := &model.LLMRequest{Contents: contents}
	if _, err := m.compressOnce(context.Background(), "u", "s", req); err != nil {
		t.Fatal(err)
	}
	if len(req.Contents) >= len(contents) {
		t.Fatal("test setup: nothing was compacted")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written to the working directory: %v", entries)
	}
}
