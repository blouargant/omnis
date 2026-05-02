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
	"sync"
	"sync/atomic"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"
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
}

// sessionState is the per-(userID, sessionID) bookkeeping.
type sessionState struct {
	mu              sync.Mutex
	lastTokenCount  atomic.Int64
	forceCompact    atomic.Bool
	recentUserTurns []string // last few user prompts; used by task-switch sniffer
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
	v, _ := m.sessions.LoadOrStore(key, &sessionState{})
	return v.(*sessionState)
}

func (m *manager) softLimit() int { return int(float64(m.cfg.WindowTokens) * m.cfg.SoftRatio) }
func (m *manager) hardLimit() int { return int(float64(m.cfg.WindowTokens) * m.cfg.HardRatio) }

// beforeModel inspects req.Contents, decides whether to compress, and
// rewrites the slice in place if so.
func (m *manager) beforeModel(ctx agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil {
		return nil, nil
	}
	st := m.state(ctx.UserID(), ctx.SessionID())

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
		return nil, nil
	}

	newContents, applied := m.runPipeline(ctx, req.Contents, trigger == "hard" || trigger == "forced")
	if applied == nil {
		return nil, nil
	}
	req.Contents = newContents
	after := CountContents(req.Contents)
	m.audit(ctx, trigger, before, after, applied)
	return nil, nil
}

// afterModel records the most recent UsageMetadata count for diagnostics.
func (m *manager) afterModel(ctx agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
	if resp == nil || resp.UsageMetadata == nil {
		return nil, nil
	}
	st := m.state(ctx.UserID(), ctx.SessionID())
	st.lastTokenCount.Store(int64(resp.UsageMetadata.PromptTokenCount + resp.UsageMetadata.CandidatesTokenCount))
	return nil, nil
}

// runPipeline applies passes in order. Returns (newContents, appliedPassNames)
// or (nil, nil) when no pass altered the conversation.
func (m *manager) runPipeline(ctx context.Context, contents []*genai.Content, includeSummariser bool) ([]*genai.Content, []string) {
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
	step("summarize_middle", PassSummarizeMiddle(m.cfg.KeepHeadTurns, m.summariser(ctx)))
	return finalize(cur, applied)
}

func finalize(cur []*genai.Content, applied []string) ([]*genai.Content, []string) {
	if len(applied) == 0 {
		return nil, nil
	}
	return cur, applied
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

const summariserPrompt = `Summarise the following agent transcript in fewer than 500 words.
Preserve: file paths touched, tool names invoked, decisions made, open questions, and any errors encountered.
Drop: small talk, intermediate reasoning, and verbose tool output.
Write in past tense bullet points.`

// audit appends a structured entry to the per-session memory file.
func (m *manager) audit(ctx agent.CallbackContext, trigger string, before, after int, applied []string) {
	path := m.cfg.AuditPathFunc(ctx.UserID(), ctx.SessionID())
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
