// session_aside.go — one-off, non-persisted LLM helpers backing the web UI's
// /btw (quick side question) and /recap (session summary) slash commands. Both
// reuse the single-shot pattern from session_title.go / routing.go: resolve the
// session's leader model and run ONE non-streamed completion with no runner,
// tools, or event bus. Nothing is written to the session's ADK context or to the
// persisted transcript — these are read-only asides over a flattened copy of the
// transcript the caller passes in.
package agent

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

const (
	// asideHistoryTurns caps how many recent turns are fed as context so the
	// one-off call stays cheap and within the model window.
	asideHistoryTurns = 24
	// asideTurnScan bounds the characters kept per historical turn part.
	asideTurnScan = 4000
)

const btwSystemPrompt = "You are answering a quick SIDE QUESTION about an " +
	"ongoing chat session. Use the conversation so far as context, but treat " +
	"this as an aside: your answer will NOT be added to the conversation history " +
	"and you cannot run tools or change anything. Answer directly and concisely."

const recapSystemPrompt = "You summarise chat sessions. Given the conversation " +
	"so far, write a brief recap: what the user is working on, the key decisions " +
	"or findings, and the current state. Use 3–6 short Markdown bullet points. " +
	"Be specific and factual; do not invent details and do not add a preamble."

const recapUserPrompt = "Recap this session so far."

// AskAside answers a quick side question in the context of a session WITHOUT
// persisting anything (no ADK turn, no transcript write). history is a flattened
// text-only copy of the prior turns supplied by the caller (the agent package
// cannot import internal/sessions). Returns an error only when no model resolves
// or the call fails.
func (m *Manager) AskAside(ctx context.Context, sessionID, question string, history []Exchange) (string, error) {
	q := strings.TrimSpace(question)
	if q == "" {
		return "", fmt.Errorf("empty question")
	}
	return m.oneShotWithHistory(ctx, sessionID, btwSystemPrompt, history, q)
}

// Recap produces a short summary of the session so far via the same one-off
// pattern. Returns an error when there is nothing to summarise yet.
func (m *Manager) Recap(ctx context.Context, sessionID string, history []Exchange) (string, error) {
	if len(history) == 0 {
		return "", fmt.Errorf("nothing to recap yet")
	}
	return m.oneShotWithHistory(ctx, sessionID, recapSystemPrompt, history, recapUserPrompt)
}

// oneShotWithHistory is the shared engine: the session's leader model + a system
// prompt + the recent history (as alternating user/model turns) + a final user
// message. Consecutive same-role messages are merged so strict providers never
// see two user turns in a row.
func (m *Manager) oneShotWithHistory(ctx context.Context, sessionID, system string, history []Exchange, finalUser string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("no manager")
	}
	inst := m.Lookup(sessionID)
	if inst == nil {
		inst = m.Current()
	}
	if inst == nil {
		return "", fmt.Errorf("no agent generation")
	}
	mdl, err := newModelForAgent(ctx, inst.LeaderCfg)
	if err != nil {
		return "", err
	}

	var contents []*genai.Content
	push := func(role, text string) {
		text = clampAside(text, asideTurnScan)
		if text == "" {
			return
		}
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, &genai.Part{Text: text})
			return
		}
		contents = append(contents, &genai.Content{Role: role, Parts: []*genai.Part{{Text: text}}})
	}

	start := 0
	if len(history) > asideHistoryTurns {
		start = len(history) - asideHistoryTurns
	}
	for _, ex := range history[start:] {
		push("user", ex.User)
		push("model", ex.Assistant)
	}
	push("user", finalUser)

	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: system}}},
		},
		Contents: contents,
	}

	var out strings.Builder
	for resp, gerr := range mdl.GenerateContent(ctx, req, false) {
		if gerr != nil {
			return "", gerr
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, part := range resp.Content.Parts {
			out.WriteString(part.Text)
		}
	}
	res := strings.TrimSpace(out.String())
	if res == "" {
		return "", fmt.Errorf("empty response")
	}
	return res, nil
}

// clampAside trims surrounding whitespace and caps a turn part to max bytes.
func clampAside(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) > max {
		return strings.TrimSpace(s[:max])
	}
	return s
}
