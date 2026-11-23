package agent

import (
	"strings"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"
)

// Per-message reminders (agent.json `turn_reminder`).
//
// A rule at the top of a long system instruction is skipped by the weaker
// models; the same rule next to the user's words is read. Measured twice: the
// memory rules (omnis-benches reports/memory-remind-2026-11-10.md) and the
// System leader's delegation rules, which `balanced` ignored whatever their
// wording in the instruction (reports/leader-fake-2026-11.md).

// appendTurnReminder adds text to each user message: a user content whose
// first part is non-empty text (a typed prompt, a steering note turn), never a
// tool-result content and never the compression summary. It is appended after
// the existing parts, so the first text part stays the user's words. On every
// message, not only the latest, so the request stays a stable prefix for the
// prompt cache. Idempotent.
func appendTurnReminder(contents []*genai.Content, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
next:
	for _, c := range contents {
		if c == nil || c.Role != "user" || len(c.Parts) == 0 {
			continue
		}
		first := c.Parts[0]
		if first == nil || first.FunctionResponse != nil || strings.TrimSpace(first.Text) == "" ||
			strings.Contains(first.Text, compressedHistoryMarker) {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.Text == text {
				continue next
			}
		}
		c.Parts = append(c.Parts, &genai.Part{Text: text})
	}
}

// turnReminderPlugin appends the root agent's turn_reminder before every model
// call of the root. ADK clones event content per request, so the session is
// not modified.
func turnReminderPlugin(name, text string) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: name,
		BeforeModelCallback: llmagent.BeforeModelCallback(
			func(_ adk.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
				if req != nil {
					appendTurnReminder(req.Contents, text)
				}
				return nil, nil
			},
		),
	})
}
