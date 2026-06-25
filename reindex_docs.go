// reindex_docs.go — `omnis reindex-docs` subcommand. Walks every markdown file
// under the configured documentation roots (see docindex.Roots) and (re)builds
// the documentation semantic index in one pass. Incremental and idempotent:
// chunk ids are derived from (absolute path, line start) and only changed files
// are re-embedded.
//
// Requires an embedder (models.json embed_model_ref or OMNIS_EMBED_*); without
// one there is nothing to embed and the command errors out.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/docindex"
)

func runReindexDocs(ctx context.Context, opts options, _ []string) error {
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
		return fmt.Errorf("no embedder configured: set an embedding model_ref in models.json (embed_model_ref) or the OMNIS_EMBED_* environment")
	}

	roots := docindex.Roots()
	if len(roots) == 0 {
		fmt.Println("no documentation roots found (set OMNIS_DOCS_DIRS to override)")
		return nil
	}

	idx, err := docindex.Open(emb)
	if err != nil {
		return fmt.Errorf("open docs index: %w", err)
	}
	indexed, removed, err := idx.Reindex(ctx)
	if err != nil {
		return fmt.Errorf("reindex docs: %w", err)
	}
	fmt.Printf("docs roots: %s\n", strings.Join(roots, ", "))
	fmt.Printf("indexed %d file(s), removed %d, %d chunks total\n", indexed, removed, idx.Len())
	return nil
}
