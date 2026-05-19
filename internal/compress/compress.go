// Package compress implements an intelligent context manager (Phase 2 / s06).
//
// Unlike the original v1 (which only wrote a side-car summary file), this
// plugin actively rewrites the live LLMRequest.Contents via a
// BeforeModelCallback so the conversation passed to the model stays under
// budget. Compression is a pipeline of passes — dedupe, truncate, drop
// unused skills, summarise the middle — applied in order until the token
// count drops below a soft target.
//
// Triggers:
//   - SOFT (default 75% of WindowTokens): runs the cheap passes only.
//   - HARD (default 92%): runs the full pipeline, including LLM summary.
//   - compact_now tool: lets the agent request compression explicitly.
//   - Task switch: a heuristic on user turns flips a per-session
//     forceCompact flag (see tasksniff.go).
//
// The .agent_memory.md side-car is kept as an audit trail, not as the
// agent's only memory of dropped turns.
package compress

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"

	"github.com/blouargant/yoke/core/events"
)

// Defaults.
const (
	DefaultWindowTokens       = 200_000 // safe baseline (Claude 3.5 Sonnet)
	DefaultSoftRatio          = 0.75
	DefaultHardRatio          = 0.92
	DefaultKeepHeadTurns      = 2
	DefaultKeepRecentTurns    = 4
	DefaultToolResultMaxBytes = 4096
	DefaultMemoryPath         = ".agent_memory.md"
	DefaultStateLogEvery      = 5
	DefaultStateLogPath       = ".agent_statelog.json"
)

// Config controls compression behaviour. v2 is a breaking change from v1:
// the single Threshold field is replaced by a window+ratio model so the
// same configuration scales across providers.
type Config struct {
	// WindowTokens is the model's effective context window in tokens.
	// Soft and hard triggers fire at SoftRatio*WindowTokens and
	// HardRatio*WindowTokens respectively. Defaults to DefaultWindowTokens.
	WindowTokens int
	// SoftRatio (0..1) — proactive trigger. Runs cheap passes only.
	SoftRatio float64
	// HardRatio (0..1) — safety-net trigger. Runs the full pipeline,
	// including LLM-backed middle summarisation.
	HardRatio float64
	// KeepHeadTurns is the count of leading turns preserved verbatim
	// (typically the original user goal + first lead reply).
	KeepHeadTurns int
	// KeepRecentTurns is the count of trailing turns preserved verbatim
	// to maintain immediate coherence.
	KeepRecentTurns int
	// ToolResultMaxBytes caps individual FunctionResponse payloads.
	ToolResultMaxBytes int
	// LLM is used to summarise the dropped middle. If nil, an extractive
	// fallback is used (no model call).
	LLM model.LLM
	// AuditPathFunc returns the per-session audit log path. When nil,
	// the AuditPath field is used; when both are empty, audit logging
	// is disabled.
	AuditPathFunc func(userID, sessionID string) string
	// AuditPath is the single audit log path used when AuditPathFunc is
	// nil. Suitable for single-user demos only.
	AuditPath string
	// EventBus, when non-nil, receives compression_start / compression_end
	// / compression_skipped events. Allows the TUI and file logger to
	// observe compression activity in real time.
	EventBus *events.Bus
	// StateLogLLM is the model used to extract durable facts into a
	// per-session State Log. If nil, the main LLM is used; if both are
	// nil, State Log extraction is disabled.
	StateLogLLM model.LLM
	// StateLogEvery is the cadence (in AfterModel callbacks) at which
	// the State Log is refreshed. Defaults to DefaultStateLogEvery.
	StateLogEvery int
	// StateLogPathFunc returns the per-session State Log file path. When
	// nil, StateLogPath is used.
	StateLogPathFunc func(userID, sessionID string) string
	// StateLogPath is the single State Log path used when
	// StateLogPathFunc is nil. Suitable for single-user demos only.
	StateLogPath string
}

func (c *Config) applyDefaults() {
	if c.WindowTokens <= 0 {
		c.WindowTokens = DefaultWindowTokens
	}
	if c.SoftRatio <= 0 {
		c.SoftRatio = DefaultSoftRatio
	}
	if c.HardRatio <= 0 {
		c.HardRatio = DefaultHardRatio
	}
	if c.KeepHeadTurns <= 0 {
		c.KeepHeadTurns = DefaultKeepHeadTurns
	}
	if c.KeepRecentTurns <= 0 {
		c.KeepRecentTurns = DefaultKeepRecentTurns
	}
	if c.ToolResultMaxBytes <= 0 {
		c.ToolResultMaxBytes = DefaultToolResultMaxBytes
	}
	if c.AuditPathFunc == nil {
		path := c.AuditPath
		if path == "" {
			path = DefaultMemoryPath
		}
		c.AuditPathFunc = func(_, _ string) string { return path }
	}
	if c.StateLogEvery <= 0 {
		c.StateLogEvery = DefaultStateLogEvery
	}
	if c.StateLogPathFunc == nil {
		path := c.StateLogPath
		if path == "" {
			path = DefaultStateLogPath
		}
		c.StateLogPathFunc = func(_, _ string) string { return path }
	}
}

// sessionState is the per-(userID, sessionID) bookkeeping.
type sessionState struct {
	mu               sync.Mutex
	lastTokenCount   atomic.Int64
	forceCompact     atomic.Bool
	totalTurns       atomic.Int64 // monotonically incremented in afterModel; stamped into StateLog
	recentUserTurns  []string     // last few user prompts; used by task-switch sniffer
	recentModelTurns []string     // last few model responses (text + tool calls); used by state-log extractor
	sumCache         *summaryCache
	stateLog         stateLogState
}

// Plugin returns the configured plugin plus a Wait function (kept for API
// compatibility; the v2 plugin runs synchronously inside callbacks so
// Wait is now a no-op).
func Plugin(name string, cfg Config) (*plugin.Plugin, func(), error) {
	p, _, wait, err := PluginWithTools(name, cfg)
	return p, wait, err
}

// PluginWithTools is the full constructor: it returns the plugin, the
// /compact tool list (mount via Toolset on the agent), a Wait function
// (no-op, kept for compatibility), and any construction error.
func PluginWithTools(name string, cfg Config) (*plugin.Plugin, []tool.Tool, func(), error) {
	cfg.applyDefaults()
	mgr := newManager(cfg)
	// When an EventBus is configured, honour EventCompressNow requests so
	// the web UI's "Compress Now" button can queue a forced compression cycle
	// for a specific session without requiring a code-level callback.
	if cfg.EventBus != nil {
		cfg.EventBus.On(events.EventCompressNow, func(_ string, p map[string]any) {
			userID, _ := p["user_id"].(string)
			sessionID, _ := p["session_id"].(string)
			if userID != "" && sessionID != "" {
				mgr.state(userID, sessionID).forceCompact.Store(true)
			}
		})
	}
	p, err := plugin.New(plugin.Config{
		Name:                name,
		BeforeModelCallback: llmagent.BeforeModelCallback(mgr.beforeModel),
		AfterModelCallback:  llmagent.AfterModelCallback(mgr.afterModel),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return p, mgr.tools(), func() {}, nil
}

// manager owns all session state and the compression pipeline.
type manager struct {
	cfg      Config
	sessions sync.Map // string -> *sessionState
}

func newManager(cfg Config) *manager { return &manager{cfg: cfg} }

func (m *manager) state(userID, sessionID string) *sessionState {
	key := userID + "\x00" + sessionID
	if v, ok := m.sessions.Load(key); ok {
		return v.(*sessionState)
	}
	v, _ := m.sessions.LoadOrStore(key, &sessionState{sumCache: newSummaryCache()})
	return v.(*sessionState)
}

func (m *manager) softLimit() int { return int(float64(m.cfg.WindowTokens) * m.cfg.SoftRatio) }
func (m *manager) hardLimit() int { return int(float64(m.cfg.WindowTokens) * m.cfg.HardRatio) }

// beforeModel inspects req.Contents, decides whether to compress, and
// rewrites the slice in place if so.
func (m *manager) beforeModel(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	return m.compressOnce(ctx, ctx.UserID(), ctx.SessionID(), req)
}

// compressOnce is the testable core of beforeModel: same behaviour but
// keyed by explicit IDs so callers don't need a real CallbackContext.
func (m *manager) compressOnce(ctx context.Context, userID, sessionID string, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil {
		return nil, nil
	}
	st := m.state(userID, sessionID)

	maybeMarkTaskSwitch(st, req.Contents)

	before := CountContents(req.Contents)
	st.lastTokenCount.Store(int64(before))

	soft, hard := m.softLimit(), m.hardLimit()
	forced := st.forceCompact.Swap(false)

	var trigger string
	switch {
	case forced:
		trigger = "forced"
	case before >= hard:
		trigger = "hard"
	case before >= soft:
		trigger = "soft"
	default:
		m.emit(events.EventCompressionSkipped, map[string]any{
			"tokens": before, "soft": soft, "hard": hard, "window": m.cfg.WindowTokens,
		})
		return nil, nil
	}

	m.emit(events.EventCompressionStart, map[string]any{
		"trigger": trigger, "tokens_before": before,
	})
	start := time.Now()

	newContents, applied := m.runPipeline(ctx, st, req.Contents, trigger == "hard" || trigger == "forced")
	if applied == nil {
		m.emit(events.EventCompressionEnd, map[string]any{
			"trigger": trigger, "tokens_before": before, "tokens_after": before,
			"passes": []string{}, "duration_ms": time.Since(start).Milliseconds(),
			"soft": soft, "hard": hard, "window": m.cfg.WindowTokens,
		})
		return nil, nil
	}
	req.Contents = newContents
	after := CountContents(req.Contents)
	m.audit(userID, sessionID, trigger, before, after, applied)
	m.emit(events.EventCompressionEnd, map[string]any{
		"trigger": trigger, "tokens_before": before, "tokens_after": after,
		"passes": applied, "duration_ms": time.Since(start).Milliseconds(),
		"soft": soft, "hard": hard, "window": m.cfg.WindowTokens,
	})
	return nil, nil
}

// afterModel records the most recent UsageMetadata count, buffers the
// model turn so the State Log extractor sees decisions/file paths/tool
// calls (not only user prompts), and refreshes the per-session State Log
// every StateLogEvery turns.
func (m *manager) afterModel(ctx agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
	st := m.state(ctx.UserID(), ctx.SessionID())
	st.totalTurns.Add(1)
	if resp != nil && resp.UsageMetadata != nil {
		st.lastTokenCount.Store(int64(resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.CandidatesTokenCount))
	}
	if rendered := renderModelTurn(resp); rendered != "" {
		st.mu.Lock()
		st.recentModelTurns = append(st.recentModelTurns, rendered)
		if len(st.recentModelTurns) > taskSwitchKeepUserTurns {
			st.recentModelTurns = st.recentModelTurns[len(st.recentModelTurns)-taskSwitchKeepUserTurns:]
		}
		st.mu.Unlock()
	}
	m.maybeRefreshStateLog(ctx, ctx.UserID(), ctx.SessionID(), st)
	return nil, nil
}

// renderModelTurn flattens an LLM response into a single string suitable
// for the State Log extractor: assistant text + a compact representation
// of tool calls (name + arg keys) and tool responses (name only). We
// deliberately avoid dumping full tool payloads to keep the prompt small
// and to avoid leaking secrets through the extractor.
func renderModelTurn(resp *model.LLMResponse) string {
	if resp == nil || resp.Content == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range resp.Content.Parts {
		if p == nil {
			continue
		}
		if p.Text != "" {
			b.WriteString(strings.TrimSpace(p.Text))
			b.WriteByte('\n')
		}
		if p.FunctionCall != nil {
			keys := make([]string, 0, len(p.FunctionCall.Args))
			for k := range p.FunctionCall.Args {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprintf(&b, "tool_call: %s(%s)\n", p.FunctionCall.Name, strings.Join(keys, ","))
		}
		if p.FunctionResponse != nil {
			fmt.Fprintf(&b, "tool_response: %s\n", p.FunctionResponse.Name)
		}
	}
	return strings.TrimSpace(b.String())
}

// emit pushes an event onto the configured bus (no-op when nil).
func (m *manager) emit(name string, payload map[string]any) {
	if m.cfg.EventBus == nil {
		return
	}
	m.cfg.EventBus.Emit(name, payload)
}

// runPipeline applies passes in order. Returns (newContents, appliedPassNames)
// or (nil, nil) when no pass altered the conversation.
func (m *manager) runPipeline(ctx context.Context, st *sessionState, contents []*genai.Content, includeSummariser bool) ([]*genai.Content, []string) {
	var applied []string
	soft := m.softLimit()
	cur := contents

	step := func(name string, p Pass) {
		before := CountContents(cur)
		next := p(cur, m.cfg.KeepRecentTurns)
		if next == nil {
			next = cur
		}
		if CountContents(next) < before {
			applied = append(applied, name)
		}
		cur = next
	}

	step("dedupe_tool_calls", PassDedupeToolCalls)
	if CountContents(cur) <= soft {
		return finalize(cur, applied)
	}
	step("truncate_tool_results", PassTruncateToolResults(m.cfg.ToolResultMaxBytes))
	if CountContents(cur) <= soft {
		return finalize(cur, applied)
	}
	step("drop_unused_skills", PassDropUnusedSkills)
	if CountContents(cur) <= soft || !includeSummariser {
		return finalize(cur, applied)
	}

	// Build a cache-aware summariser keyed on the exact middle slice
	// the pass will see, and inject the State Log digest as prefix.
	summarise := m.cachedSummariser(ctx, st, cur)
	logPrefix := ""
	st.stateLog.mu.Lock()
	logPrefix = st.stateLog.log.renderForPrompt()
	st.stateLog.mu.Unlock()
	step("summarize_middle", PassSummarizeMiddle(m.cfg.KeepHeadTurns, summarise, logPrefix))
	return finalize(cur, applied)
}

func finalize(cur []*genai.Content, applied []string) ([]*genai.Content, []string) {
	if len(applied) == 0 {
		return nil, nil
	}
	return cur, applied
}

// cachedSummariser wraps the configured LLM with a per-session LRU keyed
// on the (head, recent, middle) tuple about to be summarised.
func (m *manager) cachedSummariser(ctx context.Context, st *sessionState, cur []*genai.Content) func(string) (string, error) {
	base := m.summariser(ctx)
	if base == nil || st == nil || st.sumCache == nil {
		return base
	}
	_, middle, _ := SplitMiddle(cur, m.cfg.KeepHeadTurns, m.cfg.KeepRecentTurns)
	if middle == nil {
		return base
	}
	key := summaryKey(middle, m.cfg.KeepHeadTurns, m.cfg.KeepRecentTurns)
	return func(text string) (string, error) {
		if cached, ok := st.sumCache.get(key); ok {
			return cached, nil
		}
		out, err := base(text)
		if err == nil && out != "" {
			st.sumCache.put(key, out)
		}
		return out, err
	}
}

// summariser returns a closure that asks the configured LLM to summarise
// the dropped middle. Returns nil if no LLM is configured.
func (m *manager) summariser(ctx context.Context) func(string) (string, error) {
	if m.cfg.LLM == nil {
		return nil
	}
	return func(text string) (string, error) {
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: summariserPrompt + "\n\n" + text}}},
			},
		}
		seq := m.cfg.LLM.GenerateContent(ctx, req, false)
		var out string
		for resp, err := range seq {
			if err != nil {
				return "", err
			}
			if resp == nil || resp.Content == nil {
				continue
			}
			for _, p := range resp.Content.Parts {
				out += p.Text
			}
		}
		return out, nil
	}
}

// maybeRefreshStateLog runs the State Log extractor when the per-session
// turn counter exceeds StateLogEvery (or whenever a forced compaction was
// requested). Best-effort: failures are silent and never block the loop.
func (m *manager) maybeRefreshStateLog(ctx context.Context, userID, sessionID string, st *sessionState) {
	llm := m.cfg.StateLogLLM
	if llm == nil {
		llm = m.cfg.LLM
	}
	if llm == nil {
		return
	}
	st.stateLog.mu.Lock()
	st.stateLog.turnsSince++
	due := st.stateLog.turnsSince >= m.cfg.StateLogEvery || st.forceCompact.Load()
	st.stateLog.mu.Unlock()
	if !due {
		return
	}
	transcript := m.stateLogDelta(st)
	if transcript == "" {
		st.stateLog.mu.Lock()
		st.stateLog.turnsSince = 0
		st.stateLog.mu.Unlock()
		return
	}
	delta := extractStateLog(ctx, llm, transcript)
	st.stateLog.mu.Lock()
	defer st.stateLog.mu.Unlock()
	if delta != nil {
		st.stateLog.log.merge(delta)
	}
	st.stateLog.log.TurnCount = int(st.totalTurns.Load())
	_ = persistStateLog(m.cfg.StateLogPathFunc(userID, sessionID), &st.stateLog.log)
	st.stateLog.turnsSince = 0
}

// stateLogDelta renders the conversation slice fed to the State Log
// extractor. We buffer the last few user prompts (via maybeMarkTaskSwitch)
// and the last few model responses (via afterModel), then interleave them
// so the extractor sees decisions, file paths and tool names — not only
// user questions. Returns an empty string when nothing has been buffered
// yet.
func (m *manager) stateLogDelta(st *sessionState) string {
	st.mu.Lock()
	users := append([]string(nil), st.recentUserTurns...)
	models := append([]string(nil), st.recentModelTurns...)
	st.mu.Unlock()
	if len(users) == 0 && len(models) == 0 {
		return ""
	}
	// Cap each side to keep the call cheap.
	if len(users) > taskSwitchKeepUserTurns {
		users = users[len(users)-taskSwitchKeepUserTurns:]
	}
	if len(models) > taskSwitchKeepUserTurns {
		models = models[len(models)-taskSwitchKeepUserTurns:]
	}
	var b strings.Builder
	// Interleave user[i] then model[i] so the extractor reads each
	// question with its corresponding answer; trailing items from the
	// longer slice are appended after.
	n := len(users)
	if len(models) > n {
		n = len(models)
	}
	for i := 0; i < n; i++ {
		if i < len(users) {
			b.WriteString("user: ")
			b.WriteString(users[i])
			b.WriteString("\n")
		}
		if i < len(models) {
			b.WriteString("assistant: ")
			b.WriteString(models[i])
			b.WriteString("\n")
		}
	}
	return b.String()
}

const summariserPrompt = `Summarise the following agent transcript in fewer than 500 words.
Preserve: file paths touched, tool names invoked, decisions made, open questions, and any errors encountered.
Drop: small talk, intermediate reasoning, and verbose tool output.
Write in past tense bullet points.`

// audit appends a structured entry to the per-session memory file.
func (m *manager) audit(userID, sessionID, trigger string, before, after int, applied []string) {
	path := m.cfg.AuditPathFunc(userID, sessionID)
	if path == "" {
		return
	}
	entry := fmt.Sprintf("## compression event\n- trigger: %s\n- tokens_before: %d\n- tokens_after: %d\n- passes: %v\n", trigger, before, after, applied)
	_ = appendMemory(path, entry)
}

func appendMemory(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text + "\n")
	return err
}
