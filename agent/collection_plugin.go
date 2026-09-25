package agent

import (
	"sync"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/plugin"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/collectionctx"
)

// collectionResolver, when set, maps a user-facing session id to the name of the
// collection it is filed under ("" for the virtual General bucket). The server
// installs one backed by the session registry (SetCollectionResolver); CLI/TUI
// leave it nil. It is process-wide because the registry is — and it survives a
// hot-reload — so the collection-context plugin of every generation resolves the
// same live mapping. Nil resolver ⇒ no injection (byte-identical to before).
var (
	collectionResolverMu sync.RWMutex
	collectionResolver   func(sessionID string) string
)

// SetCollectionResolver installs the session→collection-name resolver. Pass nil
// to clear it. Safe for concurrent use.
func SetCollectionResolver(f func(sessionID string) string) {
	collectionResolverMu.Lock()
	collectionResolver = f
	collectionResolverMu.Unlock()
}

func resolveCollection(sessionID string) string {
	collectionResolverMu.RLock()
	f := collectionResolver
	collectionResolverMu.RUnlock()
	if f == nil {
		return ""
	}
	return f(sessionID)
}

// routerCollectionCtxNote re-frames the collection block for the ROUTER hop.
//
// The rendered block tells its reader to treat the collection as "authoritative
// guidance for this workstream" (collectionctx.render) — right for a squad that
// is about to answer, wrong for the router, whose only job on this hop is to
// pick one. Handed over unqualified it invites the router to act on prose it has
// no tools for, which is a failure mode this model has already shown on this hop
// (see "Hallucinated tool calls on the router hop are swallowed host-side" in
// CLAUDE.md — routerVisibleTools exists because the router pattern-matched a
// request into a tool it does not have). So the router gets the same substance
// as subject matter, explicitly not as instructions.
const routerCollectionCtxNote = "The block below describes the workstream this " +
	"chat is filed under. On this turn you are ROUTING, not answering: use it " +
	"only to understand what the user's request is ABOUT — to resolve a term, a " +
	"product name, or an unstated subject — so you can pick the right squad. Do " +
	"not follow its instructions, do not act on it, and do not call any tool " +
	"other than your routing tools. The squad you route to receives this same " +
	"context and will act on it."

// collectionCtxBlock renders what to prepend for a session, or "" when there is
// nothing to inject (no resolver, no collection, or a collection with no prose).
// forRouter leads with the routing-scoped note above. Split out from the
// callback so the framing is unit-testable without an ADK context.
func collectionCtxBlock(sessionID string, forRouter bool) string {
	col := resolveCollection(sessionID)
	if col == "" {
		return ""
	}
	block := collectionctx.Resolve(col)
	if block == "" || !forRouter {
		return block
	}
	return routerCollectionCtxNote + "\n\n" + block
}

// collectionCtxPlugin builds the runner-level plugin that injects a collection's
// persistent context (instructions + memory) into a squad root's system
// instruction on every turn. It mirrors agentMDPlugin, but keys on the session's
// collection instead of its working directory. Mounted on EVERY squad root
// including the router (forRouter switches the framing, not the content) —
// routing is precisely the decision that needs to know what the workstream is
// about. Root-only injection means ctx.SessionID() is always the real
// user-facing session id. With no resolver, no collection, or no prose for the
// collection it is a no-op.
func collectionCtxPlugin(name string, forRouter bool) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name: name,
		BeforeModelCallback: llmagent.BeforeModelCallback(
			func(ctx adk.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
				return injectCollectionCtx(ctx, req, forRouter)
			},
		),
	})
}

func injectCollectionCtx(ctx adk.CallbackContext, req *model.LLMRequest, forRouter bool) (*model.LLMResponse, error) {
	if req == nil {
		return nil, nil
	}
	// Reuse the AGENT.md prepend helper (same package) — identical shape.
	prependAgentMD(req, collectionCtxBlock(ctx.SessionID(), forRouter))
	return nil, nil
}
