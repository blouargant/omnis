package llm

import (
	"context"
	"fmt"
	"iter"
	"log"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// CutToolCallNudge is appended as a user turn when RetryCutToolCall re-sends
// a request whose reply was cut at the output limit in the middle of a tool
// call. It says what happened and how to avoid it: re-issuing the same call
// whole would hit the same limit.
const CutToolCallNudge = "Your previous reply reached the output limit%s while writing the arguments of a `%s` call, so the call was cut off and did not run. Nothing was changed. Do it again in smaller steps: one tool call per file, and for a long file write a first part with Write, then add the rest with further Edit calls. Keep each call's arguments well under that size."

// EmptyCutNudge is appended as a user turn when RetryCutToolCall re-sends a
// request whose reply reached the output limit without writing anything.
const EmptyCutNudge = "Your previous attempt reached the output limit while reasoning and wrote nothing. Answer now, directly, without long deliberation."

// RetryCutToolCall wraps an LLM so a reply that stops at the output limit
// while it is writing a tool call, or before writing anything, does not end
// the turn.
//
// The adapters' markTruncated drops such a call (its arguments are cut
// mid-JSON) and appends a notice. The reply then has no tool call, so ADK's
// flow loop takes it as the final answer and the turn ends half-done — what
// stopped two DeepSWE tengo attempts after a 32,000-token Write. Pi turns each
// cut call into an error tool result so its loop continues; here the cut
// attempt is discarded (it would leave up to max_output_tokens of a broken
// call in the context, which Pi also removes from a failed attempt) and the
// request is re-sent once with CutToolCallNudge. A second cut reply is
// returned as it is, notice included.
//
// A reply cut with no text and no tool call is the model reasoning until the
// limit: seen on the router with Balanced, 32,768 output tokens and 170 s for
// nothing, the turn then showing only the notice. It is re-sent once with
// reasoning off (ThinkingBudget 0) and EmptyCutNudge.
//
// Known limit: text the first attempt streamed before the call stays on screen
// and the retry may repeat it.
func RetryCutToolCall(inner model.LLM) model.LLM {
	if inner == nil {
		return nil
	}
	if _, already := inner.(*cutCallRetry); already {
		return inner
	}
	return &cutCallRetry{inner: inner}
}

type cutCallRetry struct{ inner model.LLM }

func (g *cutCallRetry) Name() string { return g.inner.Name() }

func (g *cutCallRetry) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		cut, stopped := g.attempt(ctx, req, stream, true, yield)
		if cut == nil || stopped {
			return
		}
		retry := *req
		nudge := EmptyCutNudge
		if cut.empty {
			log.Printf("llm: %s reached the output limit without writing anything; retrying once without reasoning", g.inner.Name())
			cfg := genai.GenerateContentConfig{}
			if req.Config != nil {
				cfg = *req.Config
			}
			zero := int32(0)
			cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: &zero}
			retry.Config = &cfg
		} else {
			log.Printf("llm: %s reply cut while writing a %s call; retrying once in smaller steps", g.inner.Name(), cut.name)
			nudge = cutNudge(cut)
		}
		retry.Contents = append(append([]*genai.Content(nil), req.Contents...), &genai.Content{
			Role:  "user",
			Parts: []*genai.Part{{Text: nudge}},
		})
		g.attempt(ctx, &retry, stream, false, yield)
	}
}

type cutCall struct {
	name   string
	tokens int32
	empty  bool // nothing written: no text, no tool call
}

func cutNudge(c *cutCall) string {
	size := ""
	if c.tokens > 0 {
		size = fmt.Sprintf(" (%d tokens)", c.tokens)
	}
	return fmt.Sprintf(CutToolCallNudge, size, c.name)
}

// attempt relays one inner call. With retry allowed, a final response whose
// tool call was cut is not yielded (nor its streamed notice) and is reported
// instead, so the caller can re-send the request.
func (g *cutCallRetry) attempt(ctx context.Context, req *model.LLMRequest, stream, allowRetry bool, yield func(*model.LLMResponse, error) bool) (*cutCall, bool) {
	var notice *model.LLMResponse
	for resp, err := range g.inner.GenerateContent(ctx, req, stream) {
		if err != nil {
			return nil, !yield(nil, err)
		}
		if allowRetry && resp != nil && resp.Partial && resp.CustomMetadata[truncationNoticeKey] == true {
			notice = resp
			continue
		}
		if allowRetry && resp != nil && !resp.Partial {
			if resp.CustomMetadata[emptyCutKey] == true {
				return &cutCall{empty: true}, false
			}
			if name, ok := resp.CustomMetadata[cutToolCallKey].(string); ok && name != "" && declaresTools(req) {
				c := &cutCall{name: name}
				if resp.UsageMetadata != nil {
					c.tokens = resp.UsageMetadata.CandidatesTokenCount
				}
				return c, false
			}
		}
		if notice != nil {
			if !yield(notice, nil) {
				return nil, true
			}
			notice = nil
		}
		if !yield(resp, nil) {
			return nil, true
		}
	}
	if notice != nil && !yield(notice, nil) {
		return nil, true
	}
	return nil, false
}
