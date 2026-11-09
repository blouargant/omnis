# Semantic Recall (embedder + vector indexes)

Omnis can search the repository, the configured registries, its own
documentation and your past chats **by meaning** rather than by literal text.
This document covers the embedder that powers it, the shared vector store, and
the four recall features built on top.

(What the agent learns from the user is not here: it is **collection memory**,
plain Markdown files injected into every turn — see
[Sessions → Collection memory](../web/docs/03-sessions.md#collection-memory).)

## The embedder (`core/embed`)

`core/embed` mirrors the `core/llm` dispatcher but for text→vector embedding. It
exposes an `Embedder` interface (`Embed`, `Model`, `Dim`), a provider `Selection`,
and `NewWithSelection`. Providers: `openai`, `openai_compat`, `gemini`
(`anthropic` returns `ErrUnsupported` — use Voyage/OpenAI via `openai_compat`).
Output is L2-normalised, with a content-hash on-disk cache so re-embedding an
unchanged string is free.

**Resolution** lives in [`agent/embedder.go`](../agent/embedder.go)
(`ResolveEmbedder`), precedence high → low:

1. `embed_model_ref` in `models.json` (or `agents.json`) → the named catalogue
   model flagged `"embedding": true`.
2. `OMNIS_EMBED_MODEL_REF`, then the `OMNIS_EMBED_*` family
   (`OMNIS_EMBED_PROVIDER` / `MODEL` / `BASE_URL` / `API_KEY` / `DIM`).
3. Nothing resolves → **no embedder**.

The embedder is built **once per process** and stored on `Infrastructure`
(`Embedder()`), so it survives hot-reload like the MCP pool. Changing the
embedder identity therefore requires a **server restart**, not a config reload —
the Settings save banner detects this via `embedderFingerprint` and offers
"Restart server" instead of "Reload".

### The nil-embedder contract

> When no embedder resolves, none of the recall tools are mounted and every path
> falls back to its glob/grep equivalent — behaviour is byte-identical to a build
> without these features. (Session search is the exception: its tools are always
> mounted, and without an embedder they scan the conversation files directly.)

Every recall feature is additive and individually gated: a `nil` embedder makes
`semindex.Open` return a usable but inert store, and `Upsert`/`Query` return
`embed.ErrNoEmbedder`. Callers check for this and simply don't mount the tool.

## The shared store (`internal/semindex`)

`internal/semindex` is a thin persistence + query layer over a
[`go-turbovec`](https://pkg.go.dev/github.com/blouargant/go-turbovec) `IdMapIndex`
(BitWidth 4 + UnitNorm cosine). One store backs every recall feature.

- **On disk**: `<name>.tvim` (the binary ANN index) plus a `<name>.meta.json`
  sidecar holding a manifest (`{model, dim, count}`) and per-ID metadata.
- **API**: `Open`, `Upsert`, `Query`, `Remove`, `Save`.
- **Self-healing**: `Open` compares the sidecar's manifest model against the
  active embedder. On mismatch (the embedding model changed) it discards the old
  vectors and rebuilds — you cannot mix vector spaces.
- `Upsert` embeds each item's text, removes any existing vector with the same ID,
  adds the new vector, and updates metadata. `Query` embeds the query and runs an
  ANN search, returning hits with score + metadata.

All indexes live under `paths.IndexDir()` (`$OMNIS_HOME/index/`), alongside the
`embed_cache/` content-hash cache.

## The four recall features

| Feature | Package | Tools | Mounted on | Index file |
|---|---|---|---|---|
| Code search | [`internal/codeindex`](../internal/codeindex/) | `search_code` / `reindex_code` | investigator | `index/<repo-hash>/codebase.tvim` |
| Registry search | [`internal/regindex`](../internal/regindex/) | `search_registries` / `reindex_registries` | helper | `index/registries.tvim` |
| Documentation search | [`internal/docindex`](../internal/docindex/) | `search_docs` / `reindex_docs` (+ glob `list_docs` / `read_doc` / `grep_docs`) | helper | `index/docs.tvim` |
| Session search | [`internal/sessindex`](../internal/sessindex/) | `search_sessions` / `read_session` / `list_sessions` / `report_sessions` | session_search (nested under helper) | `index/sessions.tvim` |

The index handles are process-wide on `Infrastructure` (`CodeIndex()`,
`RegistryIndex()`, `DocIndex()`, `SessionIndex()` in [`agent/embedder.go`](../agent/embedder.go)),
built lazily and surviving hot-reload.

### Documentation search (`internal/docindex`)

The documentation index lets the **Helper** answer questions about omnis itself
("how does hot-reload work?", "what env var sets the embedder?") from omnis's own
docs. It indexes markdown across every doc root returned by `docindex.Roots()` —
the web UI user docs (`web/docs` → `/usr/share/omnis/web/docs`) and the developer
docs (`docs` → `/usr/share/doc/omnis/docs`), overridable with `OMNIS_DOCS_DIRS`.
Chunking is line-window based (like code search) and content-hash incremental;
each hit carries the source `path`, `heading`, line range, and the quoted
`text`, so the Helper can answer and cite in one step.

Indexing runs as a background task at **server startup** (`startDocsIndexer` in
[`server/docs_indexer.go`](../server/docs_indexer.go)): the incremental
`Reindex` builds the index on first boot and refreshes it after the docs or the
embedder changed, while being a no-op otherwise. Rebuild manually with
`omnis reindex-docs`. When no embedder is configured the semantic `search_docs`
is absent and the always-available `list_docs` / `read_doc` / `grep_docs` glob
tools are the fallback.

### Session search (`internal/sessindex`)

Session search finds a past chat, archived ones included, from the web UI's
new-tab search box or from a chat (through the Helper's `session_search`
sub-agent). It indexes one chunk per turn — the user's text and the assistant's
text only, never tool calls — and folds hits by session. Sessions are indexed by
the server's idle indexer (`EventSessionIndexNow`, fired once a session has been
idle for 5 minutes and when it is archived); `omnis reindex-sessions` builds the
whole index. With no embedder, or while the index is still cold, it scans the
conversation files directly.

## Further reading

- [`core/embed/embed.go`](../core/embed/embed.go) — embedder interface + providers
- [`internal/semindex/semindex.go`](../internal/semindex/semindex.go) — shared vector store
- [`agent/embedder.go`](../agent/embedder.go) — `ResolveEmbedder` + process-wide index handles
- [`server/session_search.go`](../server/session_search.go) — session-search routes and indexing hook
