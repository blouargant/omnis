// prompt_suggest.go — predict the user's next message so the web UI can offer it
// as a Tab-to-accept placeholder in the composer. One non-streamed completion on
// the eval model (eval_model_ref, else the leader) — the same isolated one-off-LLM
// pattern as EvaluateGoal / GenerateTitle: no runner, tools or event bus, so
// nothing reaches any SSE stream. The server caches the result per session.
package agent

import (
	"context"
	"regexp"
	"strings"
	"unicode/utf8"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const (
	// suggestMaxTurns is how many trailing exchanges the model sees.
	suggestMaxTurns = 3
	// suggestTranscriptCap bounds the transcript (runes); the TAIL is kept since
	// the latest reply is what the suggestion answers.
	suggestTranscriptCap = 6000
	// suggestMaxLen drops an over-long "sentence" rather than truncating it: a
	// suggestion ending in "…" is not something the user can send.
	suggestMaxLen = 160
)

const suggestSystemPrompt = "You predict the user's next message in a chat with an AI assistant. " +
	"Write the single most likely next message the USER would send: one sentence, first person, " +
	"in the same language the user writes in, at most about 15 words. It must move the conversation " +
	"forward (a follow-up question, a next step, a request to apply or refine). " +
	"Output only the message — no quotes, no preamble. " +
	"If there is no natural follow-up, output exactly NONE."

// suggestLabelRE strips a leading label some models add despite the
// instruction. The ":"/"：" separator allows optional surrounding whitespace,
// but "-"/"–" only counts as a label separator when whitespace surrounds it
// on BOTH sides — otherwise a hyphenated compound the model legitimately wrote
// ("User-facing bug is critical") would be misread as a "User" label and have
// its first word stripped ("facing bug is critical").
var suggestLabelRE = regexp.MustCompile(`(?i)^(suggestion|suggested (message|reply)|next message|user|utilisateur)(?:\s*[:：]\s*|\s+[\-–]\s+)`)

// buildSuggestRequest renders the last suggestMaxTurns exchanges as a plain
// transcript, capped at suggestTranscriptCap runes keeping the tail.
func buildSuggestRequest(turns []Exchange) *model.LLMRequest {
	if len(turns) > suggestMaxTurns {
		turns = turns[len(turns)-suggestMaxTurns:]
	}
	var b strings.Builder
	for _, t := range turns {
		if u := strings.TrimSpace(t.User); u != "" {
			b.WriteString("USER: ")
			b.WriteString(u)
			b.WriteString("\n\n")
		}
		if a := strings.TrimSpace(t.Assistant); a != "" {
			b.WriteString("ASSISTANT: ")
			b.WriteString(a)
			b.WriteString("\n\n")
		}
	}
	transcript := strings.TrimSpace(b.String())
	if r := []rune(transcript); len(r) > suggestTranscriptCap {
		transcript = "…(earlier conversation omitted)…\n" + string(r[len(r)-suggestTranscriptCap:])
	}
	return &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: suggestSystemPrompt}}},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "CONVERSATION:\n" + transcript}}},
		},
	}
}

// cleanSuggestion reduces the model output to one sendable line, or "" when there
// is no usable suggestion. A result starting with "/", "!" or "#" is rejected: the
// composer would treat it as a slash command, a host shell escape or an AGENT.md
// write when sent.
func cleanSuggestion(raw string) string {
	s := ""
	for _, line := range strings.Split(raw, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			s = l
			break
		}
	}
	s = suggestLabelRE.ReplaceAllString(s, "")
	// Strip various quote styles and trim whitespace
	s = strings.TrimSpace(strings.Trim(s, " \t\"'`“”«»"))
	if s == "" || strings.EqualFold(strings.TrimRight(s, ".!"), "NONE") {
		return ""
	}
	switch s[0] {
	case '/', '!', '#':
		return ""
	}
	if utf8.RuneCountInString(s) > suggestMaxLen {
		return ""
	}
	return s
}

// SuggestNextPrompt predicts the user's next message from the trailing turns.
// Returns ok=false only when the call could not be made or failed; ("", true)
// means "no natural follow-up" and is safe to cache.
func (m *Manager) SuggestNextPrompt(ctx context.Context, sessionID string, turns []Exchange) (string, bool) {
	if m == nil {
		return "", false
	}
	if len(turns) == 0 {
		return "", true
	}
	// Peek, not Lookup: this is a read-only path with no matching Release, so
	// pinning an unpinned session here would leak its generation's refcount
	// forever (reachable when the session was archived/deleted between the
	// HTTP handler's registry snapshot and this call).
	inst := m.Peek(sessionID)
	if inst == nil {
		inst = m.Current()
	}
	if inst == nil {
		return "", false
	}
	mdl, err := m.evalModel(ctx, inst)
	if err != nil || mdl == nil {
		return "", false
	}
	var out strings.Builder
	for resp, gerr := range mdl.GenerateContent(ctx, buildSuggestRequest(turns), false) {
		if gerr != nil {
			return "", false
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, p := range resp.Content.Parts {
			out.WriteString(p.Text)
		}
	}
	return cleanSuggestion(out.String()), true
}
