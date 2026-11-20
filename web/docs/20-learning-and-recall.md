# Memory & Semantic Recall

## Memory

The main way Omnis learns your way of working is **collection memory**: when you
correct the agent, state a preference, or ask it to remember something, the
agent saves a small memory **in that same turn**, and every later chat of the
same collection starts with it — you don't have to say it again, and the agent
doesn't have to go looking for it.

- **Scope** — each collection has its own memories; chats outside any
  collection share **General**'s.
- **What is kept** — four types: **Feedback** (corrections and confirmed ways of
  working), **User** (who you are, your preferences), **Project** (decisions,
  constraints, dates) and **Reference** (links, where things live), all given to
  the agent on every turn.
- **Dated** — each memory shows when it was saved, last changed and how many
  times you restated it; in a conflict the most recent wins, and old project
  facts are flagged for review.
- **What is not** — what the repository or `AGENT.md` already says, what only
  matters to the current chat, anything that came from a web page, a file, a
  tool's output or another agent, and secrets.
- **Visible and editable** — each save or delete shows as a chip in the
  conversation; the environment panel's **Memory** card, the collection's
  **Edit context** dialog and **`/memory`** let you read, edit, add and delete
  memories.

See [Sessions → Collection memory](03-sessions.md#collection-memory) for the
details.

## Lessons from tool errors

Collection memory keeps what **you** said. **Agent lessons** keep what an agent
**learned by itself**: when it calls a tool wrongly, gets an error, then calls
the same tool again correctly, Omnis stores a one-line lesson for that agent
("when listing pods in a namespace, pass `-n <namespace>`"). Nothing asks you
to approve it, and no question or card is ever shown.

- **When one is learned** — only after an error followed by a success of the
  same tool, in the same run. A failure the agent never recovered from, a
  refusal you made on a permission card, a permission rule, a budget notice or a
  Stop never create a lesson. A small model judges whether the error teaches
  something general; a one-off typo or a transient failure writes nothing.
  Episodes that look like they contain a password or a token are dropped.
- **How it comes back** — the next time the same error appears for that agent,
  a note is added to the error: the lesson and the arguments that worked then.
  A lesson that keeps recurring (three times, in at least two chats) is
  promoted into the agent's system prompt so the error is prevented. A lesson
  whose hint does not lead to a working call is retired, and a prompt lesson
  that does not prevent the error, or is not seen for 60 days, goes back to
  being a hint.
- **Where to see and delete them** — Settings → Agents → pick an agent →
  **Lessons**: each lesson with its state (hint, prompt, retired), the tool,
  what happened and how often. Delete any you do not want. The Lessons list appears once the agent has
  at least one lesson; the **Learn from errors** toggle is always there.
- **Scope** — a lesson belongs to the agent that made the error and applies
  only to that tool. Lessons are stored under `$OMNIS_HOME/lessons/<agent>/`
  (per user on a shared server).
- **Switching it off** — for one agent, turn off **Learn from errors** in its
  settings; for everything, set `OMNIS_LESSONS=false` or `lessons: {"enabled":
  false}` in `agents.json`. Off, nothing is added to any prompt or error.

## The StateLog (built during the session)

While a session runs, the context-compression plugin maintains a compact digest
of it — the **StateLog**. Every few model turns the plugin issues a small side
LLM call that reads only the recent conversation delta and returns a JSON
digest:

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
  build-time setting, not an environment variable. A manual compaction (`/compress`)
  forces an extraction on the next turn.
- **LLM-extracted** — `goal`, `decisions`, `open_issues`, `files`, and `tools`
  are all produced by the model. Only the turn count is set mechanically.
- **Merged, not replaced** — each extraction merges into the running log: the
  goal is overwritten (latest wins) while decisions and open issues are appended
  and de-duplicated. The StateLog is therefore an *accumulating* picture.
- **Off the hot path** — the extraction runs on a detached goroutine with a
  60-second timeout, so it never blocks the live turn.
- **Persisted** to `$OMNIS_HOME/logs/agent_statelog_<session-key>.json` after each
  refresh. Nothing reads it once the session is over.

Its purpose is compression: the StateLog is injected as a "durable facts" header
when the conversation is compressed, so a summarised history stays anchored to
what the session already established. It is built for the leader only — a
sub-agent's work appears in it through the leader's tool-call output. See
[Sessions](03-sessions.md) for the other per-session files.

## Semantic recall (the embedder gate)

Four **embedder-gated** semantic-recall features share the same vector
machinery:

| Feature | Tool | Indexes |
|---|---|---|
| Code search | `search_code` / `reindex_code` | The current repo, semantically |
| Registry search | `search_registries` / `reindex_registries` | Remote registry items |
| Documentation search | `search_docs` / `reindex_docs` | Omnis's own docs (user + developer) |
| Session search | `search_sessions` | Your past chats, archived ones included |

**Documentation search** powers the **Helper** agent: ask a question about omnis
("how does hot-reload work?", "what env var sets the embedder?") and the leader
delegates to the Helper, which searches the bundled documentation and answers
with the relevant passage quoted and its source cited. The docs index is built
in the background at server startup and refreshed incrementally; rebuild it by
hand with `omnis reindex-docs`. Without an embedder the Helper falls back to the
always-available `list_docs` / `read_doc` / `grep_docs` tools.

**Session search** is the search box on the new-tab page (see
[Sessions](03-sessions.md)). Sessions are indexed once they have been idle for a
few minutes, or when archived; `omnis reindex-sessions` builds the whole index
now. Unlike the other three, it also works **without** an embedder: it then scans
the conversation files directly — the same results, literal and slower.

All of them depend on an **internal embedding model**. Select it in
Settings → [Providers & Models](15-providers.md) by flagging a model as an
embedding model and pointing `embed_model_ref` at it (overridable with the
`OMNIS_EMBED_*` variables — see [Environment Variables](16-env-vars.md#embedding--semantic-recall)).
The embedder is built once per process and survives hot-reload, so **changing it
requires a server restart** (the Settings save banner will prompt for a restart
rather than a reload when you change the embedder identity).

**When no embedding model is configured, the recall tools fall back to their
glob/grep (or direct scan) equivalent — behaviour is identical to a build
without these features.** Recall is purely additive.

## See also

- [Sessions](03-sessions.md) — collections, collection memory, session search
  and the per-session files.
- [Skills](11-skills.md) — authored skills.
- [Architecture](10-architecture.md) — agent topology and the compression
  plugin.
- [Environment Variables](16-env-vars.md) — embedding settings.
