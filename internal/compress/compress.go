// Package compress implements the article's "context compression at the 92%
// threshold" (Phase 2 / s06). When the running token total of a session
// exceeds Threshold tokens, we summarise the older half of the conversation
// via a one-shot model call and write the summary to a memory file so the
// agent can refer to it.
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
)

// DefaultThreshold roughly mirrors a 92% fill of a 1M-token Gemini window.
const DefaultThreshold = 920_000

// MemoryPath is where the running summary is persisted.
const DefaultMemoryPath = ".agent_memory.md"

// Config controls compression behaviour.
type Config struct {
	// Threshold in tokens. A summarisation pass runs once cumulative
	// prompt+response token usage crosses it.
	Threshold int64
	// MemoryPath is the file the running summary is appended to.
	MemoryPath string
	// LLM used to generate the summary. If nil, compression runs in
	// "stub" mode: it just notes the threshold was hit.
	LLM model.LLM
}

// Plugin returns an ADK plugin that watches AfterModelCallback responses,
// accumulates token usage, and triggers compression once it crosses
// cfg.Threshold.
func Plugin(name string, cfg Config) (*plugin.Plugin, error) {
	if cfg.Threshold <= 0 {
		cfg.Threshold = DefaultThreshold
	}
	if cfg.MemoryPath == "" {
		cfg.MemoryPath = DefaultMemoryPath
	}
	var (
		used       atomic.Int64
		busy       atomic.Bool
		mu         sync.Mutex
		recent     []string // last few responses we've buffered to summarise
		recentText string
	)

	flush := func(ctx context.Context) {
		if cfg.LLM == nil {
			_ = appendMemory(cfg.MemoryPath,
				fmt.Sprintf("[compress] threshold %d reached; summary skipped (no LLM configured).", cfg.Threshold))
			return
		}
		mu.Lock()
		text := recentText
		recent = nil
		recentText = ""
		mu.Unlock()
		if text == "" {
			return
		}
		req := &model.LLMRequest{
			Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "Summarise the following agent transcript in <300 words, preserving facts, file paths, decisions, and open issues:\n\n" + text}}},
			},
		}
		// Drain the streaming response into one string.
		seq := cfg.LLM.GenerateContent(ctx, req, false)
		var summary string
		for resp, err := range seq {
			if err != nil {
				_ = appendMemory(cfg.MemoryPath, fmt.Sprintf("[compress] error: %v", err))
				return
			}
			if resp == nil || resp.Content == nil {
				continue
			}
			for _, p := range resp.Content.Parts {
				summary += p.Text
			}
		}
		_ = appendMemory(cfg.MemoryPath, "## Compressed summary\n"+summary+"\n")
	}

	cb := func(ctx agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
		if resp == nil || resp.UsageMetadata == nil {
			return nil, nil
		}
		var n int64
		n += int64(resp.UsageMetadata.PromptTokenCount)
		n += int64(resp.UsageMetadata.CandidatesTokenCount)
		used.Add(n)
		if resp.Content != nil {
			mu.Lock()
			for _, p := range resp.Content.Parts {
				if p != nil && p.Text != "" {
					recent = append(recent, p.Text)
					recentText += p.Text + "\n"
				}
			}
			mu.Unlock()
		}
		if used.Load() >= cfg.Threshold && busy.CompareAndSwap(false, true) {
			go func() {
				defer busy.Store(false)
				defer used.Store(0)
				flush(context.Background())
			}()
		}
		return nil, nil
	}
	return plugin.New(plugin.Config{
		Name:               name,
		AfterModelCallback: llmagent.AfterModelCallback(cb),
	})
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
