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
	// suggestEndingChars is how much of the last reply's tail is repeated in its
	// own section: the suggestion must follow from how that reply ENDS.
	suggestEndingChars = 800
)

// suggestEndingHeader introduces the repeated tail of the last reply.
const suggestEndingHeader = "END OF THE ASSISTANT'S LAST REPLY — the next message follows from this:"

const suggestSystemPrompt = "You predict the user's next message in a chat with an AI assistant. " +
	"Write the single most likely next message the USER would send: one sentence, first person, " +
	"in the same language the user writes in, at most about 15 words. " +
	"It must continue from how the assistant's last reply ENDS (repeated after the conversation): " +
	"answer the question that ending asks the user, pursue the point or comparison it raises last, " +
	"or take up the next step it offers — not a detail from earlier in the reply. " +
	"The message is a single question or request, written as the user addressing the assistant — " +
	"nothing else. Be factual and concise: no thanks, greeting, praise, acknowledgement of the previous " +
	"answer or filler anywhere in it (no \"Thanks\", \"Great\", \"Merci\", \"Super\", \"C'est clair\" or similar). " +
	"Output only the message — no quotes, no preamble. " +
	"If the reply ends by concluding or summarising without opening anything further (a conclusion, " +
	"a recap, a list of sources or references), there is no natural follow-up: output the single word " +
	"NONE and nothing else — never explain why."

// suggestLabelRE strips a leading label some models add despite the
// instruction. The ":"/"：" separator allows optional surrounding whitespace,
// but "-"/"–" only counts as a label separator when whitespace surrounds it
// on BOTH sides — otherwise a hyphenated compound the model legitimately wrote
// ("User-facing bug is critical") would be misread as a "User" label and have
// its first word stripped ("facing bug is critical").
var suggestLabelRE = regexp.MustCompile(`(?i)^(suggestion|suggested (message|reply)|next message|user|utilisateur)(?:\s*[:：]\s*|\s+[\-–]\s+)`)

// suggestNoneRE recognises the "no suggestion" answer in the forms models actually
// produce besides the literal NONE: translated into the conversation language
// ("Aucun", "Keine"), or explained ("Aucune suite naturelle car…", "No natural
// follow-up: …"). Matching the first word costs the rare real message opening with
// one of these words ("Rien ne marche…") — cheaper than displaying the model's
// reasoning as the user's next message.
var suggestNoneRE = regexp.MustCompile(`(?i)^(?:(?:none|nothing|aucun|aucune|rien|ninguno|ninguna|nada|kein|keine|keiner|nichts)\b|no natural follow)`)

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
			// No reasoning: one short sentence does not need it, and reasoning
			// models spend 1.5–2.8k tokens thinking first — 57 s on the hosted
			// model for a real transcript, well past the server's 20 s timeout.
			ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: genai.Ptr[int32](0)},
		},
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "CONVERSATION:\n" + transcript + lastReplyEnding(turns)}}},
		},
	}
}

// lastReplyEnding repeats the tail of the last assistant reply in its own section
// (capped at suggestEndingChars runes), so the model weighs how the reply ends
// rather than any detail from the middle of a long answer. "" when the last turn
// has no assistant text.
func lastReplyEnding(turns []Exchange) string {
	if len(turns) == 0 {
		return ""
	}
	last := strings.TrimSpace(turns[len(turns)-1].Assistant)
	if last == "" {
		return ""
	}
	if r := []rune(last); len(r) > suggestEndingChars {
		last = "…" + string(r[len(r)-suggestEndingChars:])
	}
	return "\n\n" + suggestEndingHeader + "\n" + last
}

// cleanSuggestion reduces the model output to one sendable line, or "" when there
// is no usable suggestion. A result starting with "/", "!" or "#" is rejected: the
// composer would treat it as a slash command, a host shell escape or an AGENT.md
// write when sent. One starting with "<" or "[" is model scaffolding, not a message.
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
	if s == "" || suggestNoneRE.MatchString(s) {
		return ""
	}
	switch s[0] {
	case '/', '!', '#':
		return ""
	case '<', '[':
		// Model scaffolding leaking into the output ("<thinking>", "[no A
		// message yet]"), observed on the premium model — never a real message.
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
