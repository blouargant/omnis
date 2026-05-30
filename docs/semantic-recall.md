# Semantic Recall (embedder + vector indexes)

Yoke turns the traces a session leaves behind — its goal, its decisions, the
soft-skills it loaded — into **semantically searchable memory** for future
sessions. This document covers the embedder that powers it, the shared vector
store, the five recall features built on top, and how cross-session **precedents**
are indexed end to end.

For the upstream artefacts these indexes consume, see
[context-management.md — State Log](context-management.md) (how the
goal/decisions digest is produced during a session) and
[skills.md — Post-session reflection](skills.md#post-session-reflection-pipeline)
(how soft-skills are distilled and tagged).

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
2. `YOKE_EMBED_MODEL_REF`, then the `YOKE_EMBED_*` family
   (`YOKE_EMBED_PROVIDER` / `MODEL` / `BASE_URL` / `API_KEY` / `DIM`).
3. Nothing resolves → **no embedder**.

The embedder is built **once per process** and stored on `Infrastructure`
(`Embedder()`), so it survives hot-reload like the MCP pool. Changing the
embedder identity therefore requires a **server restart**, not a config reload —
the Settings save banner detects this via `embedderFingerprint` and offers
"Restart server" instead of "Reload".

### The nil-embedder contract

> When no embedder resolves, none of the recall tools are mounted and every path
> falls back to its glob/grep equivalent — behaviour is byte-identical to a build
> without these features.

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

All indexes live under `paths.IndexDir()` (`$YOKE_HOME/index/`), alongside the
`embed_cache/` content-hash cache.

## The five recall features

| Feature | Package | Tools | Mounted on | Index file |
|---|---|---|---|---|
| Soft-skill recall | [`internal/softskills`](../internal/softskills/recall.go) | `recall_softskills` | leader | `index/softskills.tvim` |
| Precedent recall | [`internal/precedents`](../internal/precedents/) | `recall_precedents` | reflector + curator | `index/precedents.tvim` |
| Code search | [`internal/codeindex`](../internal/codeindex/) | `search_code` / `reindex_code` | investigator | `index/<repo-hash>/codebase.tvim` |
| Registry search | [`internal/regindex`](../internal/regindex/) | `search_registries` / `reindex_registries` | helper | `index/registries.tvim` |
| Documentation search | [`internal/docindex`](../internal/docindex/) | `search_docs` / `reindex_docs` (+ glob `list_docs` / `read_doc` / `grep_docs`) | helper | `index/docs.tvim` |

The index handles are process-wide on `Infrastructure` (`Precedents()`,
`CodeIndex()`, `RegistryIndex()`, `DocIndex()` in [`agent/embedder.go`](../agent/embedder.go)),
built lazily and surviving hot-reload.

### Documentation search (`internal/docindex`)

The documentation index lets the **Helper** answer questions about yoke itself
("how does hot-reload work?", "what env var sets the embedder?") from yoke's own
docs. It indexes markdown across every doc root returned by `docindex.Roots()` —
the web UI user docs (`web/docs` → `/usr/share/yoke/web/docs`) and the developer
docs (`docs` → `/usr/share/doc/yoke/docs`), overridable with `YOKE_DOCS_DIRS`.
Chunking is line-window based (like code search) and content-hash incremental;
each hit carries the source `path`, `heading`, line range, and the quoted
`text`, so the Helper can answer and cite in one step.

Indexing runs as a background task at **server startup** (`startDocsIndexer` in
[`server/docs_indexer.go`](../server/docs_indexer.go)): the incremental
`Reindex` builds the index on first boot and refreshes it after the docs or the
embedder changed, while being a no-op otherwise. Rebuild manually with
`yoke reindex-docs`. When no embedder is configured the semantic `search_docs`
is absent and the always-available `list_docs` / `read_doc` / `grep_docs` glob
tools are the fallback.

## Precedents end to end (`internal/precedents`)

The precedent index records each finished session's goal and decisions so later
sessions can ask "how was a comparable problem solved before?".

### What gets indexed

One `semindex.Item` per goal and per decision in the session's
[`StateLog`](context-management.md):

- the goal → `kind="goal"`, index 0;
- each `Decisions[i]` → `kind="decision"`, index `i`.

Each item's metadata is `{session_key, kind, text, timestamp}`. Item IDs are an
FNV-1a hash of `(sessionKey, kind, index)` — **deterministic**, so re-indexing a
session overwrites its own entries rather than duplicating them. This is what
makes the backfill idempotent.

### When indexing happens

At session end the reflection pipeline emits `EventSessionReflected`
(see [`agent/load_recorder.go`](../agent/load_recorder.go)).
[`agent/precedents_hook.go`](../agent/precedents_hook.go) subscribes to it,
reads `$YOKE_HOME/logs/agent_statelog_<key>.json`, and calls
`store.IndexStateLog(key, sl, ts)` (timestamp = the statelog's mtime). The hook
is registered per generation in [`agent/instance.go`](../agent/instance.go) and
detached on hot-reload; it is wired only when an embedder resolves.

### Recall

`Store.Tool()` returns the `recall_precedents` tool (`query` required, `k`
optional, default 5). It is mounted on the **reflector** and **curator** agents —
the post-session analysts — and passed to them as `precedentsTool` in
`registerCuratorHook`. Both treat results as *weak prior evidence*: the prompt
hint instructs the model never to invent precedents the tool did not return, and
the hard create/update/delete gates come from the reflector's `Outcome`, not from
precedents (see [skills.md — Curator gating rules](skills.md#curator-gating-rules)).

### Backfill

[`reindex_precedents.go`](../reindex_precedents.go) (`yoke reindex-precedents`)
globs every `agent_statelog_*.json`, calls `store.Add` for each, then `Save`
once. Because IDs are deterministic it converges to the same index on repeated
runs; it errors out cleanly when no embedder is configured.

## Leader vs. sub-agents

Sub-agents run inside agenttool's private runner, so the shared bus never sees
their internal model turns — to the leader a sub-agent call is one tool call.
The two halves of the learning loop draw the line accordingly:

| Mechanism | Leader | Sub-agents |
|---|---|---|
| StateLog (goal/decisions) | yes — extracted per turn | none of their own; surfaces only via the leader's tool-call output |
| Precedents index / `recall_precedents` | yes | no |
| Soft-skill directory + glob `list/load_softskill` | `softskills/<skill>/` | `softskills/<agent>/<skill>/` |
| Semantic `recall_softskills` | yes | no — glob-only loader (nil embedder passed) |
| Per-invocation helpful/harmful tagging | n/a | yes — Phase 6, see [skills.md](skills.md#per-sub-agent-micro-reflection) |
| Curator create/update/delete | yes (root) | yes (per-agent dirs) |

**StateLog and precedents are leader-level** — they capture the session goal and
the leader's decisions and treat each sub-agent invocation as a black box. The
**soft-skill loop reaches inside** that boundary: each sub-agent accumulates and
is graded on its own skills, and the per-agent soft-skill toolset is built with a
`nil` embedder so sub-agents use the glob loader while the leader gets semantic
recall.

## Further reading

- [`core/embed/embed.go`](../core/embed/embed.go) — embedder interface + providers
- [`internal/semindex/semindex.go`](../internal/semindex/semindex.go) — shared vector store
- [`internal/precedents/precedents.go`](../internal/precedents/precedents.go) — precedent index + `recall_precedents`
- [`agent/embedder.go`](../agent/embedder.go) — `ResolveEmbedder` + process-wide index handles
- [`agent/precedents_hook.go`](../agent/precedents_hook.go) — session-end indexing hook
- [`reindex_precedents.go`](../reindex_precedents.go) — backfill command
- [context-management.md](context-management.md) — the StateLog these indexes consume
- [skills.md](skills.md) — soft-skills, reflection, and curator gating
