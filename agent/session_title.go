// session_title.go — auto-derive a short session label from the user's first
// prompt, so a fresh chat moves from its petname (e.g. "teaching-kite") to a
// topic-bearing title without a manual rename. Two tiers, used together by the
// server in a "hybrid" flow:
//
//   - HeuristicTitle: pure string work (strip code/whitespace, truncate). Zero
//     cost, instant, fully offline — applied immediately so the sidebar updates
//     the moment the first turn lands.
//   - Manager.GenerateTitle: one non-streamed LLM completion on the session's
//     leader model (the same one-off-LLM pattern as the routing capability
//     probe in routing.go — no runner, tools, or event bus, so nothing reaches
//     the SSE stream). Fired asynchronously to *refine* the heuristic title.
//
// Both degrade safely: GenerateTitle returns ("", false) on any failure so the
// caller simply keeps the heuristic title.
package agent

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const (
	// titleMaxLen caps the displayed title (matches the ≤60-char convention).
	titleMaxLen = 60
	// titlePromptScan bounds how much of the first prompt is fed to the LLM —
	// a topic is clear from the opening; the rest is wasted tokens.
	titlePromptScan = 2000
)

const titleSystemPrompt = "You write concise titles for chat sessions. " +
	"Given the user's first message, reply with a short, specific title " +
	"(3–6 words, at most 60 characters) capturing its topic. " +
	"Output ONLY the title — no surrounding quotes, no trailing punctuation, " +
	"no preamble, no explanation."

const titleUserPrefix = "First message:\n\n"

// HeuristicTitle derives a short session label directly from the user's first
// prompt with no model call: it drops fenced code blocks, collapses
// whitespace, and truncates to titleMaxLen on a word boundary. Returns "" when
// the prompt carries no visible text (e.g. an attachment-only turn).
func HeuristicTitle(prompt string) string {
	s := stripForTitle(prompt)
	if s == "" {
		return ""
	}
	return truncateOnWord(s, titleMaxLen)
}

// GenerateTitle produces a concise title from the first user prompt via a
// single non-streamed completion on the session's leader model. It resolves
// the session's pinned instance (falling back to the current generation), so
// the title uses whatever model that session's leader is configured with.
// Returns ("", false) on any failure — the caller keeps the heuristic title.
func (m *Manager) GenerateTitle(ctx context.Context, sessionID, prompt string) (string, bool) {
	if m == nil {
		return "", false
	}
	inst := m.Lookup(sessionID)
	if inst == nil {
		inst = m.Current()
	}
	if inst == nil {
		return "", false
	}
	mdl, err := newModelForAgent(ctx, inst.LeaderCfg)
	if err != nil {
		return "", false
	}

	p := strings.TrimSpace(prompt)
	if p == "" {
		return "", false
	}
	if len(p) > titlePromptScan {
		p = p[:titlePromptScan]
	}

	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: titleSystemPrompt}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: titleUserPrefix + p}}},
		},
	}

	var out strings.Builder
	for resp, gerr := range mdl.GenerateContent(ctx, req, false) {
		if gerr != nil {
			return "", false
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, part := range resp.Content.Parts {
			out.WriteString(part.Text)
		}
	}

	title := cleanLLMTitle(out.String())
	if title == "" {
		return "", false
	}
	return title, true
}

// stripForTitle reduces a raw prompt to a single clean line: fenced code blocks
// removed, all whitespace runs (including newlines) collapsed to single spaces.
func stripForTitle(prompt string) string {
	s := removeFencedCode(prompt)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

// removeFencedCode drops ``` ... ``` blocks so a title never quotes a code dump.
// An unterminated fence drops the remainder of the text.
func removeFencedCode(s string) string {
	const fence = "```"
	var b strings.Builder
	for {
		i := strings.Index(s, fence)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		rest := s[i+len(fence):]
		j := strings.Index(rest, fence)
		if j < 0 {
			break // unterminated fence — drop the tail
		}
		s = rest[j+len(fence):]
	}
	return b.String()
}

// truncateOnWord trims s to at most max runes, backing up to the last word
// boundary and appending an ellipsis when the cut lands mid-word.
func truncateOnWord(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	cut := runes[:max]
	// Back up to the last space so we don't slice a word in half.
	if idx := lastIndexSpace(cut); idx > 0 {
		cut = cut[:idx]
	}
	return strings.TrimRight(string(cut), " ") + "…"
}

func lastIndexSpace(runes []rune) int {
	for i := len(runes) - 1; i >= 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return i
		}
	}
	return -1
}

// cleanLLMTitle normalises a model's title reply: first line only, surrounding
// quotes stripped, trailing sentence punctuation removed, truncated to the cap.
func cleanLLMTitle(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	// First non-empty line.
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			s = t
			break
		}
	}
	s = strings.Trim(s, "\"'`")
	s = strings.TrimRight(s, ".!?,;: ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return truncateOnWord(s, titleMaxLen)
}
