// reindex_sessions.go — `omnis reindex-sessions` subcommand. Embeds every
// persisted conversation ($OMNIS_HOME/logs/conversation_*.json) into the
// past-session search index in one pass. Incremental and idempotent: chunk ids
// are derived from (session id, turn index, offset) and only sessions whose
// indexed text changed are re-embedded.
//
// The server does not need this — the idle-indexer rail backfills the history on
// its own, and the search box refreshes the index as the user types. It exists
// for the impatient case (build it all now, before the first search) and for
// rebuilding after an embedding-model change.
//
// Requires an embedder (models.json embed_model_ref or OMNIS_EMBED_*); without
// one there is no index at all and search falls back to scanning the files.
package main

import (
	"context"
	"fmt"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessindex"
)

func runReindexSessions(ctx context.Context, opts options, _ []string) error {
	runtime, err := agent.ResolveRuntimeSettings(agent.Options{
		SoftSkillsDir:    opts.softSkillsDir,
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
	})
	if err != nil {
		return err
	}

	emb, err := agent.ResolveEmbedder(ctx, runtime)
	if err != nil {
		return fmt.Errorf("embedder: %w", err)
	}
	if emb == nil {
		return fmt.Errorf("no embedder configured: set an embedding model_ref in models.json (embed_model_ref) or the OMNIS_EMBED_* environment. " +
			"Session search still works without one — it scans the conversation files directly — but there is nothing to index")
	}

	idx, err := sessindex.Open(emb)
	if err != nil {
		return fmt.Errorf("open session index: %w", err)
	}
	indexed, removed, err := idx.Reindex(ctx)
	if err != nil {
		return fmt.Errorf("reindex sessions: %w", err)
	}
	fmt.Printf("indexed %d session(s), removed %d, %d chunks total\n", indexed, removed, idx.Len())
	return nil
}
