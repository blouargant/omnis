// Package cache implements the article's "prompt-cache reuse statistics"
// (Phase 5 / s20). The Gemini provider returns CachedContentTokenCount
// in UsageMetadata; we expose a plugin that aggregates and prints them.
package cache

import (
	"fmt"
	"sync/atomic"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
)

// Stats are the running totals.
type Stats struct {
	cached atomic.Int64
	prompt atomic.Int64
	calls  atomic.Int64
}

// Cached returns the total cached-content tokens reused.
func (s *Stats) Cached() int64 { return s.cached.Load() }

// Prompt returns the total prompt tokens billed.
func (s *Stats) Prompt() int64 { return s.prompt.Load() }

// Calls returns the number of LLM responses observed.
func (s *Stats) Calls() int64 { return s.calls.Load() }

// HitRate returns cached/prompt as a percentage 0..100.
func (s *Stats) HitRate() float64 {
	p := s.Prompt()
	if p == 0 {
		return 0
	}
	return 100 * float64(s.Cached()) / float64(p)
}

// Summary is a one-line printable summary.
func (s *Stats) Summary() string {
	return fmt.Sprintf("cache: calls=%d prompt=%d cached=%d hit_rate=%.1f%%",
		s.Calls(), s.Prompt(), s.Cached(), s.HitRate())
}

// Plugin returns a stats collector plus the ADK plugin that feeds it.
func Plugin(name string) (*Stats, *plugin.Plugin, error) {
	s := &Stats{}
	cb := func(_ agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
		if resp == nil || resp.UsageMetadata == nil {
			return nil, nil
		}
		s.calls.Add(1)
		s.prompt.Add(int64(resp.UsageMetadata.PromptTokenCount))
		s.cached.Add(int64(resp.UsageMetadata.CachedContentTokenCount))
		return nil, nil
	}
	p, err := plugin.New(plugin.Config{
		Name:               name,
		AfterModelCallback: llmagent.AfterModelCallback(cb),
	})
	return s, p, err
}
