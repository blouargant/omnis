package llm

import (
	"context"
	"iter"

	"google.golang.org/adk/v2/model"
)

// RepairToolCalls wraps inner so that every function call in its responses is
// passed to repair (which may rewrite the arguments in place) before ADK sees
// it. Repairing here, at the model boundary, means the event bus, lifecycle
// hooks, the permission gate, the tool itself and the persisted history all
// see the same, corrected arguments. A nil repair returns inner unchanged.
func RepairToolCalls(inner model.LLM, repair func(name string, args map[string]any)) model.LLM {
	if inner == nil || repair == nil {
		return inner
	}
	return &toolCallRepair{inner: inner, repair: repair}
}

type toolCallRepair struct {
	inner  model.LLM
	repair func(name string, args map[string]any)
}

func (r *toolCallRepair) Name() string { return r.inner.Name() }

func (r *toolCallRepair) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for resp, err := range r.inner.GenerateContent(ctx, req, stream) {
			if err == nil && resp != nil && resp.Content != nil {
				for _, p := range resp.Content.Parts {
					if p != nil && p.FunctionCall != nil && p.FunctionCall.Args != nil {
						r.repair(p.FunctionCall.Name, p.FunctionCall.Args)
					}
				}
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}
