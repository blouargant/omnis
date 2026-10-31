package llm

import (
	"fmt"
	"log"

	"google.golang.org/genai"

	"google.golang.org/adk/v2/model"
)

// oaiFinishReason maps an OpenAI-compatible finish_reason onto genai's.
func oaiFinishReason(s string) genai.FinishReason {
	switch s {
	case "":
		return ""
	case "stop", "tool_calls", "function_call":
		return genai.FinishReasonStop
	case "length":
		return genai.FinishReasonMaxTokens
	case "content_filter":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonOther
	}
}

// antFinishReason maps an Anthropic stop_reason onto genai's.
func antFinishReason(s string) genai.FinishReason {
	switch s {
	case "":
		return ""
	case "end_turn", "stop_sequence", "tool_use", "pause_turn":
		return genai.FinishReasonStop
	case "max_tokens", "model_context_window_exceeded":
		return genai.FinishReasonMaxTokens
	case "refusal":
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonOther
	}
}

// truncationNotice is appended to a reply the model could not finish because
// it hit its output-token limit. Nothing else in the stack reacts to that stop
// reason, so without it the reply simply ends mid-sentence — and a gateway
// that silently caps output (LiteLLM's 4096 fallback for Anthropic models it
// does not know) looks like a broken model.
func truncationNotice(outTokens int32, droppedCall bool) string {
	n := ""
	if outTokens > 0 {
		n = fmt.Sprintf(" after %d tokens", outTokens)
	}
	s := fmt.Sprintf("\n\n[Reply cut: the model reached its output limit%s.", n)
	if droppedCall {
		s += " An unfinished tool call was dropped."
	}
	return s + " Raise max_output_tokens for this model in Settings → Models.]"
}

// markTruncated handles a final response that stopped at the output limit:
// it drops tool calls whose arguments were cut mid-JSON (running one would
// fail on missing fields, and the model would retry the same oversized call),
// appends truncationNotice to the content, and returns the notice so a
// streaming caller can also yield it as a partial chunk. No-op otherwise.
func markTruncated(modelName string, r *model.LLMResponse) string {
	if r.FinishReason != genai.FinishReasonMaxTokens {
		return ""
	}
	if r.Content == nil {
		r.Content = &genai.Content{Role: "model"}
	}
	dropped := false
	kept := r.Content.Parts[:0]
	for _, p := range r.Content.Parts {
		if p != nil && p.FunctionCall != nil {
			if _, broken := p.FunctionCall.Args["_raw"]; broken {
				dropped = true
				continue
			}
		}
		kept = append(kept, p)
	}
	r.Content.Parts = kept
	var out int32
	if r.UsageMetadata != nil {
		out = r.UsageMetadata.CandidatesTokenCount
	}
	notice := truncationNotice(out, dropped)
	r.Content.Parts = append(r.Content.Parts, &genai.Part{Text: notice})
	mergeTextParts(r.Content)
	log.Printf("llm: %s reply cut at the output limit (%d tokens, unfinished tool call dropped: %v)", modelName, out, dropped)
	return notice
}

// mergeTextParts moves the trailing notice part added by markTruncated into the
// leading text part (or in front of the tool calls when there is none), so a
// streamed final keeps its "one text part, then calls" shape: surfaces skip
// that text as a repeat of the partials, and only until the first call.
func mergeTextParts(c *genai.Content) {
	n := len(c.Parts)
	if n < 2 || c.Parts[n-1] == nil || c.Parts[n-1].Text == "" {
		return
	}
	notice := c.Parts[n-1]
	c.Parts = c.Parts[:n-1]
	if first := c.Parts[0]; first != nil && first.Text != "" && first.FunctionCall == nil {
		first.Text += notice.Text
		return
	}
	c.Parts = append([]*genai.Part{notice}, c.Parts...)
}
