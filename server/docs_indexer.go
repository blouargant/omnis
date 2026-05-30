package main

import (
	"context"
	"log"

	"github.com/blouargant/yoke/agent"
)

// startDocsIndexer ensures the documentation semantic index is built and up to
// date, as a one-shot background task at server startup.
//
// Reindex is incremental and content-hash gated: it builds the index when none
// exists yet (first startup) and refreshes it when the docs or the embedder
// changed (e.g. after an operator set an embedder and restarted the server),
// while being a cheap no-op otherwise. Running it once at boot therefore covers
// both required triggers without a polling loop — docs are static between
// releases. When no embedder is configured the index is nil and the Helper
// falls back to list_docs / read_doc / grep_docs.
func startDocsIndexer(ctx context.Context, infra *agent.Infrastructure, runtime agent.RuntimeSettings) {
	idx := infra.DocIndex(ctx, runtime)
	if idx == nil {
		log.Printf("docs-indexer: disabled (no embedder configured)")
		return
	}
	go func() {
		indexed, removed, err := idx.Reindex(ctx)
		if err != nil {
			log.Printf("docs-indexer: reindex failed: %v", err)
			return
		}
		log.Printf("docs-indexer: ready (%d file(s) indexed, %d removed, %d chunks total)",
			indexed, removed, idx.Len())
	}()
}
