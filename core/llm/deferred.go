package llm

import (
	"context"
	"iter"
	"strings"
	"sync"

	"google.golang.org/adk/model"
)

// NewDeferredWithSelection builds an LLM eagerly when it can, but never fails:
// if eager construction errors (typically a missing API key or base URL, e.g.
// `openai_compat requires OPENAI_BASE_URL`), it returns a deferredLLM that
// re-attempts the build at first use and surfaces the underlying error there.
//
// This lets the server boot with an unconfigured/unreachable provider — the web
// UI's provider-health banner reports it, and any actual turn fails with the
// real error — instead of aborting startup. A correctly configured selection is
// built immediately and behaves exactly like NewWithSelection (no wrapper), so
// the deferred path is a pure no-op when credentials are present.
func NewDeferredWithSelection(ctx context.Context, sel Selection) model.LLM {
	if m, err := NewWithSelection(ctx, sel); err == nil {
		return m
	}
	return &deferredLLM{name: deferredName(sel), sel: sel}
}

// deferredName resolves a human-readable model name for a selection whose
// eager build failed, so Name() (used in logs / introspection) is still useful.
func deferredName(sel Selection) string {
	if name := strings.TrimSpace(sel.Model); name != "" {
		return name
	}
	if p := strings.ToLower(strings.TrimSpace(sel.Provider)); p != "" {
		if def := defaultModel[p]; def != "" {
			return def
		}
	}
	return "unconfigured"
}

// deferredLLM defers real model construction until GenerateContent is called.
// It retries the build on every call until one succeeds (the underlying
// NewWithSelection only constructs an HTTP client — it makes no network call —
// so retrying is cheap); once built, the real model is cached and delegated to.
type deferredLLM struct {
	name string
	sel  Selection

	mu   sync.Mutex
	real model.LLM
}

func (d *deferredLLM) Name() string { return d.name }

func (d *deferredLLM) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	d.mu.Lock()
	if d.real == nil {
		if m, err := NewWithSelection(ctx, d.sel); err == nil {
			d.real = m
		} else {
			d.mu.Unlock()
			return func(yield func(*model.LLMResponse, error) bool) { yield(nil, err) }
		}
	}
	real := d.real
	d.mu.Unlock()
	return real.GenerateContent(ctx, req, stream)
}
