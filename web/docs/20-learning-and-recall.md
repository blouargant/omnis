# Session Learning & Semantic Recall

Every finished session leaves two durable traces that make future sessions
smarter:

- a **StateLog** — a compact digest of the session's goal and decisions, which
  feeds the cross-session **precedents** index; and
- **soft-skills** — reusable procedures the curator distils from the session
  (covered in [Skills](11-skills.md#post-session-reflection)).

This page explains where the StateLog comes from, how sessions get indexed for
semantic recall, and exactly which parts of the loop cover sub-agents versus the
leader.

## The StateLog (built during the session)

The StateLog is **not** assembled at session end — it is maintained
incrementally *while the session runs* by the context-compression plugin. Every
few model turns the plugin issues a small side LLM call that reads only the
recent conversation delta and returns a JSON digest:

```json
{
  "goal": "one-sentence objective",
  "decisions": ["durable facts decided this session"],
  "open_issues": ["unresolved questions"],
  "files": { "path": "short fact about it" },
  "tools": { "name": invocation_count }
}
```

Key properties:

- **Cadence** — extraction runs every 5 model responses by default. This is a
  build-time setting, not an environment variable. The `compact_now` tool (and
  the manual compaction path) force an extraction on the next turn.
- **LLM-extracted** — `goal`, `decisions`, `open_issues`, `files`, and `tools`
  are all produced by the model. Only the turn count is set mechanically.
- **Merged, not replaced** — each extraction merges into the running log: the
  goal is overwritten (latest wins) while decisions and open issues are appended
  and de-duplicated. The StateLog is therefore an *accumulating* picture.
- **Off the hot path** — the extraction runs on a detached goroutine with a
  60-second timeout, so it never blocks the live turn.
- **Persisted** to `$OMNIS_HOME/logs/agent_statelog_<session-key>.json` after each
  refresh. This is the same file the curator reads, and the source for the
  precedents index below.

The StateLog is also injected as a "durable facts" header when the conversation
is compressed, so a summarised history stays anchored to what the session
already established. See [Sessions](03-sessions.md) for the other per-session
files.

## Cross-session precedents

When a session ends, its StateLog is indexed into a process-wide **semantic
vector index** so later sessions can ask *"how was a similar problem solved
before?"*

- **What gets indexed** — one entry per goal and per decision. Each entry stores
  the text, a `goal`/`decision` kind, the session key, and a timestamp. Entry
  IDs are derived deterministically, so re-indexing the same session overwrites
  rather than duplicates.
- **When** — automatically at session end, on the same reflection event that
  drives curation. No manual step is required.
- **Where** — `$OMNIS_HOME/index/precedents.tvim` (the vector index) plus a
  `precedents.meta.json` sidecar holding the metadata and the embedding-model
  manifest. (Changing the embedding model invalidates the manifest and rebuilds
  the index.)
- **How it's recalled** — a `recall_precedents` tool (arguments: `query`, and an
  optional `k`, default 5) returns the most semantically similar past goals and
  decisions. It is mounted on the **reflector** and **curator** agents — the
  post-session analysts — not on the leader during normal chat. They use it as
  *weak prior evidence* while deciding what to distil; it never fabricates or
  forces an outcome.

To backfill the index from every existing StateLog on disk (for example after
first configuring an embedding model), run:

```bash
omnis reindex-precedents
```

It walks every `agent_statelog_*.json` and rebuilds the index in one pass;
because entry IDs are deterministic, it is safe to re-run.

## The embedder gate

Precedents are one of five **embedder-gated** semantic-recall features that
share the same vector machinery:

| Feature | Tool | Indexes |
|---|---|---|
| Soft-skill recall | `recall_softskills` | Curator-distilled procedures |
| Precedent recall | `recall_precedents` | Past sessions' goals + decisions |
| Code search | `search_code` / `reindex_code` | The current repo, semantically |
| Registry search | `search_registries` / `reindex_registries` | Remote registry items |
| Documentation search | `search_docs` / `reindex_docs` | Omnis's own docs (user + developer) |

**Documentation search** powers the **Helper** agent: ask a question about omnis
("how does hot-reload work?", "what env var sets the embedder?") and the leader
delegates to the Helper, which searches the bundled documentation and answers
with the relevant passage quoted and its source cited. The docs index is built
in the background at server startup and refreshed incrementally; rebuild it by
hand with `omnis reindex-docs`. Without an embedder the Helper falls back to the
always-available `list_docs` / `read_doc` / `grep_docs` tools.

All of them depend on an **internal embedding model**. Select it in
Settings → [Providers & Models](15-providers.md) by flagging a model as an
embedding model and pointing `embed_model_ref` at it (overridable with the
`OMNIS_EMBED_*` variables — see [Environment Variables](16-env-vars.md#embedding--semantic-recall)).
The embedder is built once per process and survives hot-reload, so **changing it
requires a server restart** (the Settings save banner will prompt for a restart
rather than a reload when you change the embedder identity).

**When no embedding model is configured, none of the recall tools are mounted
and every path falls back to its glob/grep equivalent — behaviour is identical
to a build without these features.** Recall is purely additive.

## Leader vs. sub-agents

The two halves of the learning loop draw the leader/sub-agent line differently,
because a sub-agent runs inside a private runner and only surfaces to the leader
as a single tool call (input in, output out).

| Mechanism | Leader | Sub-agents |
|---|---|---|
| StateLog (goal/decisions) | Yes — extracted per turn | No log of their own; their work only appears via the leader's tool-call output |
| Precedents index / `recall_precedents` | Yes | No |
| Own soft-skill directory + `list_softskills` / `load_softskill` | `softskills/<skill>/` | `softskills/<agent>/<skill>/` |
| Semantic `recall_softskills` | Yes | No — glob-only loader |
| Per-invocation helpful/harmful tagging | n/a | Yes — see the [micro-reflection](11-skills.md#per-sub-agent-micro-reflection) |
| Curator create / update / delete | Yes (root) | Yes (per-agent dirs) |

In short: **StateLog and precedents are leader-level** — they capture the
session's goal and the leader's decisions, treating each sub-agent call as a
black box. The **soft-skill loop reaches inside that boundary**: each sub-agent
accumulates its own skills and is graded on them per invocation, and the curator
writes skills into per-agent directories. The grading rules and the curator's
create/update/delete gating are documented in
[Skills → Post-session reflection](11-skills.md#post-session-reflection).

## See also

- [Skills](11-skills.md) — authored skills, soft-skills, the reflection pipeline
  and curator gating.
- [Sessions](03-sessions.md) — the session lifecycle and per-session files.
- [Architecture](10-architecture.md) — agent topology, the reflector/curator
  hooks, and the compression plugin.
- [Environment Variables](16-env-vars.md) — curator and embedding settings.
