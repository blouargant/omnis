package main

import (
	"context"
	"log"

	"github.com/blouargant/yoke/agent"
)

// startRegistryIndexer warms the remote-registry semantic index as a one-shot
// background task at server startup.
//
// Without this, the index is built lazily on the first search_registries call,
// which browses every configured registry over the network and embeds every
// item — so the user's first search is slow. EnsureBuilt applies the same
// trigger as that lazy path (build when empty or stale) but runs it at boot, so
// the slow browse + embed happens in the background and the first search hits a
// ready index. It is a cheap no-op when a persisted index for the current
// registry set is already loaded. When no embedder is configured the index is
// nil and the crawler falls back to browse_registry.
func startRegistryIndexer(ctx context.Context, infra *agent.Infrastructure, runtime agent.RuntimeSettings) {
	idx := infra.RegistryIndex(ctx, runtime)
	if idx == nil {
		log.Printf("registry-indexer: disabled (no embedder configured)")
		return
	}
	go func() {
		indexed, err := idx.EnsureBuilt(ctx)
		if err != nil {
			log.Printf("registry-indexer: build failed: %v", err)
			return
		}
		log.Printf("registry-indexer: ready (%d item(s) indexed)", indexed)
	}()
}
