package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/blouargant/yoke/core/embed"
	"github.com/blouargant/yoke/internal/codeindex"
	"github.com/blouargant/yoke/internal/precedents"
)

// embedderEnvConfigured reports whether the operator set any YOKE_EMBED_*
// environment variable, signalling an environment-driven embedder.
func embedderEnvConfigured() bool {
	for _, k := range []string{
		"YOKE_EMBED_PROVIDER", "YOKE_EMBED_MODEL",
		"YOKE_EMBED_BASE_URL", "YOKE_EMBED_API_KEY",
	} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

// ResolveEmbedder builds the internal embedder for semantic recall, in
// precedence order:
//
//  1. runtime.EmbedModelRef → the named embedding model in models.json
//     (the Web UI "internal embedding model" selector writes this).
//  2. YOKE_EMBED_* environment → embed.New.
//  3. otherwise → (nil, nil): semantic recall is disabled and every caller
//     falls back to its glob/grep path. This is the additive safety contract.
func ResolveEmbedder(ctx context.Context, runtime RuntimeSettings) (embed.Embedder, error) {
	if ref := strings.ToLower(strings.TrimSpace(runtime.EmbedModelRef)); ref != "" {
		m, ok := runtime.Models[ref]
		if !ok {
			return nil, fmt.Errorf("embed_model_ref %q not found in models.json", ref)
		}
		return embed.NewWithSelection(ctx, embed.Selection{
			Provider: m.Provider,
			Model:    m.Model,
			BaseURL:  m.BaseURL,
			APIKey:   m.APIKey,
			Dim:      m.Dim,
		})
	}
	if embedderEnvConfigured() {
		return embed.New(ctx)
	}
	return nil, nil
}

// embedderCache memoises the process-wide embedder so the (expensive) client is
// built once and survives generation rebuilds, like the MCP pool.
type embedderCache struct {
	once sync.Once
	emb  embed.Embedder
	err  error
}

// Embedder lazily resolves and caches the process-wide embedder. A resolution
// error is logged once and thereafter treated as "no embedder" so a misconfig
// degrades gracefully rather than breaking every session.
func (i *Infrastructure) Embedder(ctx context.Context, runtime RuntimeSettings) embed.Embedder {
	if i == nil {
		return nil
	}
	i.embed.once.Do(func() {
		i.embed.emb, i.embed.err = ResolveEmbedder(ctx, runtime)
		if i.embed.err != nil {
			log.Printf("embed: semantic recall disabled: %v", i.embed.err)
		} else if i.embed.emb != nil {
			log.Printf("embed: semantic recall enabled (model %q)", i.embed.emb.Model())
		}
	})
	return i.embed.emb
}

// precedentsCache memoises the process-wide precedent index.
type precedentsCache struct {
	once  sync.Once
	store *precedents.Store
}

// Precedents lazily opens and caches the cross-session precedent index, backed
// by the process-wide embedder. Returns nil when no embedder is configured, so
// callers skip mounting recall_precedents and the session-end indexing hook.
func (i *Infrastructure) Precedents(ctx context.Context, runtime RuntimeSettings) *precedents.Store {
	if i == nil {
		return nil
	}
	emb := i.Embedder(ctx, runtime)
	if emb == nil {
		return nil
	}
	i.precedents.once.Do(func() {
		st, err := precedents.Open(emb)
		if err != nil {
			log.Printf("precedents: index unavailable: %v", err)
			return
		}
		i.precedents.store = st
	})
	return i.precedents.store
}

// codeIndexCache memoises the process-wide code-search index.
type codeIndexCache struct {
	once  sync.Once
	index *codeindex.Index
}

// CodeIndex lazily opens and caches the project code-search index for the
// infrastructure's repo root, backed by the process-wide embedder. Returns nil
// when no embedder is configured, so callers skip mounting search_code.
func (i *Infrastructure) CodeIndex(ctx context.Context, runtime RuntimeSettings) *codeindex.Index {
	if i == nil {
		return nil
	}
	emb := i.Embedder(ctx, runtime)
	if emb == nil {
		return nil
	}
	i.codeIndex.once.Do(func() {
		idx, err := codeindex.Open(i.Repo, emb)
		if err != nil {
			log.Printf("codeindex: index unavailable: %v", err)
			return
		}
		i.codeIndex.index = idx
	})
	return i.codeIndex.index
}
