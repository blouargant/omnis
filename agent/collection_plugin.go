package agent

import (
	"sync"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"

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

// collectionCtxPlugin builds the runner-level plugin that injects a collection's
// persistent context (instructions + memory) into the answering root's system
// instruction on every turn. It mirrors agentMDPlugin, but keys on the session's
// collection instead of its working directory. Mounted on answering squad roots
// only (never the router — see buildPlugins), so ctx.SessionID() is always the
// real user-facing session id. With no resolver, no collection, or no prose for
// the collection it is a no-op.
func collectionCtxPlugin(name string) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name:                name,
		BeforeModelCallback: llmagent.BeforeModelCallback(injectCollectionCtx),
	})
}

func injectCollectionCtx(ctx adkagent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil {
		return nil, nil
	}
	col := resolveCollection(ctx.SessionID())
	if col == "" {
		return nil, nil
	}
	// Reuse the AGENT.md prepend helper (same package) — identical shape.
	prependAgentMD(req, collectionctx.Resolve(col))
	return nil, nil
}
