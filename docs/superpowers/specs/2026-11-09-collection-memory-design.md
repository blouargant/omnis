# Collection memory, replacing soft-skills (design, 2026-11-09)

## Intent

Soft-skills are never used. They are distilled after a session by a separate
curator, stored as procedures, and only come back if an agent decides to list and
load them — which in practice does not happen. Meanwhile the user has to repeat
the same corrections, and the agents repeat the same mistakes.

Claude Code's memory learns a user's way of working in a few interactions,
because it works the other way round:

- the **agent itself writes** a memory, **during the turn**, at the moment it
  learns something (a correction, a preference, "remember that…");
- a memory is **one small typed file per fact**;
- what is known is **put in the context of every conversation** by the host, so
  nothing depends on the agent deciding to go and look;
- a memory is **updated in place** or deleted when it turns out wrong.

This design brings that mechanism to omnis, **scoped to the collection**, and
removes soft-skills and everything that exists only to produce them.

**Success criterion (the user's):** a correction said once never has to be said
again in that collection.

## Decided with the user

- Memory **replaces soft-skills**.
- Memory is attached to the **collection**: what is learned in one conversation
  is available in every conversation of the same collection.
- **General** (conversations outside any collection) has its own memory, shared
  by all General conversations.
- It **replaces the collection's current Memory block** (the single text,
  hand-typed or generated from recent chats). The collection's **Instructions**
  stay as they are.
- The agent writes **during the turn**; the host injects memory on **every
  turn**; a user correction is the strongest trigger; an existing memory is
  updated rather than duplicated; writing asks for **no permission card** but is
  **visible** in the conversation; the user can read, edit and delete memories
  from the collection's "Edit context" dialog; the curator, the reflector, the
  end-of-session question and the related tools go away with soft-skills.

Everything below that is not in this list is the designer's call and is marked
**(call)** where it matters.

## Goals / non-goals / anti-goals

**Goals**

1. A correction or stated preference is saved in the turn where it is given.
2. Saved corrections are applied in later conversations of the collection with
   no action from the agent (no list call, no load call).
3. The user can see what was memorised when it happens, and fix or delete it.
4. Soft-skills, the curator and the reflector are removed; nothing is left
   half-wired.

**Non-goals**

- Sharing memory between collections, or a global "about me" memory across all
  collections. (A natural follow-up; not in this design.)
- Semantic search over memories. A collection's memory is small enough to index
  in the prompt.
- Sharing memory between users of a shared (cookie-mode) server.
- Migrating existing soft-skills into memories (they were never used).

**Anti-goals** (would make it a failure even if it "works")

- The user repeats a correction and the agent makes the same mistake again.
- A memory written from something other than the user's own words (a fetched
  page, a tool result, another agent's message) and then served everywhere.
- Memory that silently grows until it costs more context than it saves.

## How memory works

### What a memory is

One Markdown file per fact, with front matter:

```markdown
---
name: no-force-push
description: Never force-push; rebase locally and ask before rewriting history
type: feedback
updated: 2026-11-09
---

Never `git push --force` on this workstream.

**Why:** a force-push lost a colleague's commits in October.
**How to apply:** when a push is rejected, rebase and push normally; if history
must be rewritten, ask first.
```

- `name` — kebab-case slug, also the file name (`<name>.md`). It is the identity
  used for update and delete.
- `description` — one line; this is the index entry.
- `type` — one of:
  - `feedback` — how the user wants the work done: corrections and confirmed
    approaches. Body carries **Why** and **How to apply**.
  - `user` — who the user is: role, expertise, standing preferences.
  - `project` — facts about the workstream that are not in the code or
    AGENT.md: goals, decisions, constraints, dates (always absolute dates).
  - `reference` — pointers: URLs, dashboards, tickets, where something lives.
- `updated` — set by the host on every write.

### Where it lives

```
<state root>/collections/<collection>/memory/<name>.md     # a named collection
<state root>/general-memory/<name>.md                      # General
```

`<state root>` is `$OMNIS_HOME`, or the user's directory
(`$OMNIS_HOME/users/<login>/`) in cookie mode — the same root the collection's
`instructions.md` already resolves to. The existing rename and delete cascade of
a collection's directory therefore carries its memory with it. General's memory
is outside `collections/` because "General" is not a valid collection directory
name. **(call)**

A session moved to another collection simply starts seeing that collection's
memory; memories do not move with a session.

### When a memory is written (triggers)

The agent writes, in the turn, when:

| Trigger | Result |
|---|---|
| The user says to remember something ("retiens que…", "remember…") | always saved, type as fits |
| The user **corrects** the agent's behaviour or output | `feedback` |
| The user **confirms** a non-obvious choice the agent made ("yes, keep doing it that way") | `feedback` |
| The agent learns who the user is or a standing preference | `user` |
| A decision, constraint or deadline of the workstream is stated | `project` |
| The user points at an external resource to use from now on | `reference` |
| The user says to forget something, or a memory proves wrong or outdated | update or delete |

Before writing, the agent checks the index it already has in context: when a
memory covers the subject, it **rewrites that memory** (same `name`) instead of
adding a second one.

It does **not** save: what the repository or AGENT.md already records; what only
matters to the current conversation; anything that comes from a tool result, a
fetched page, an attached file or another agent rather than from the user's own
words; secrets (keys, tokens, passwords).

These rules are a short instruction block injected with the memory (below), so
they reach every answering root with no change to any agent's own instruction.

### How it is written (tools)

Three dedicated tools — the `memory` group — rather than the generic `Write`
tool **(call)**: several squad roots have no file tools (`knowledge_leader`,
`helper`), a dedicated tool can be allow-listed without opening file writes, and
the host can validate and confine what is written.

- `memory_save(name, description, type, content)` — creates or replaces
  `<name>.md`. The host validates the slug, the type, the size (body ≤ 4 000
  characters), stamps `updated`, writes atomically under a per-directory lock,
  and returns whether it created or updated.
- `memory_read(name)` — returns one memory's full text.
- `memory_delete(name)`.

The directory is derived by the host from the session's collection; no path is
ever an argument.

The **index is not a file the model maintains** **(call)** — Claude Code keeps a
`MEMORY.md` by hand; here the host builds the index from the files' front matter
on each change. One less thing for a weak model to get wrong, and the index can
never drift from the files.

**Who has the tools:** every non-router squad root, always on, like the mailbox
and `AskUserQuestion` (no `tools` declaration needed). Sub-agents and the router
do not. **(call)**

**Permissions:** the three tools are in the shipped `allow` tier. They write
only inside the memory directory, with host-validated names — the same reasoning
as the existing `record_session_feedback` exception.

**Unattended turns:** in a turn the user did not start (mailbox delivery, inbound
A2A, a spawned-task result notice), `memory_save` and `memory_delete` are
refused; reading and injection still work. Scheduled `/loop` and `/schedule`
turns may write (the user authored their prompt). **(call)** This is the host-side
half of "only from the user's own words".

### How it reaches the conversation (recall)

A `BeforeModelCallback` on every squad root — the existing collection-context
plugin, extended — adds a `<collection-memory>` block to the system instruction
on **every turn**:

1. the short rules above (when to save, update, delete; what not to save);
2. the **full body** of every `feedback` and `user` memory;
3. **one index line** (`name — description`) for every `project` and
   `reference` memory, to be opened with `memory_read` when relevant.

Point 2 is the heart of the design **(call)**. Claude Code injects only the
index and lets the model open files; with weaker leader models that is exactly
the step that fails (it is why soft-skills were never loaded). Corrections and
preferences are short and must apply *without a decision to look them up*, so
they are in the prompt whole.

**Budget:** the block is capped (default 12 000 characters). When `feedback` +
`user` bodies exceed it, the least recently updated ones fall back to index
lines, and the block says so. A collection holds at most 200 memories;
`memory_save` beyond that returns an error asking the agent to merge or delete.

**Router:** receives the index lines only (all types), under the existing
routing-scoped note ("use this to understand what the request is about, do not
act on it"), no tools.

**Sub-agents:** receive nothing in v1 **(call)**. A leader that delegates must
pass on what applies, and the injected rules say so. If measurement shows
sub-agents repeating corrected mistakes, the next step is injecting the
`feedback` bodies into sub-agents too (read-only) — the session id needed for
that is already available to them (`realSessionID`).

**Cache:** the block is stable between writes, so the prompt cache hits as
today; a write costs one cache miss on the next call.

**CLI / TUI:** no collections there, so they use General's memory. This is new
(today they inject nothing) and is what replaces soft-skills on those surfaces.

### How it is updated and kept small

- **Same subject → same file.** `memory_save` with an existing `name` replaces
  it. The rules tell the agent to check the index first.
- **Wrong or outdated → delete or rewrite**, in the turn where it is noticed.
- **By the user**, in the Edit context dialog (below).
- No background rewriting of memories in v1. The current "generate from recent
  chats" distiller and its auto-update worker are removed (see Removal).

### What the user sees

- **In the conversation:** a `memory_save` / `memory_delete` call renders as a
  small chip — "Memory saved: *no-force-push*" / "Memory updated: …" / "Memory
  deleted: …" — not a tool block. Clicking it shows the memory with **Edit** and
  **Delete**. This is the "visible, no permission card" requirement, and the
  user's first line of defence against a wrong memory.
- **Environment panel:** a **Memory** card listing the collection's memories
  (type, name, description), opening the same view.
- **Edit context dialog:** the Memory textarea, its "Generate from recent chats"
  button, the size selector, the auto-update switch and Revert are replaced by
  the list of memories grouped by type, each editable and deletable, plus
  **Add**. General gets the same dialog (memory only — it has no instructions or
  profile). The drafting assistant keeps helping with Instructions only.
- **`/memory`** opens that dialog for the current conversation's collection.
- Other browsers are kept in sync by a `memory_changed` event on `/api/events`
  (owner-scoped in cookie mode).

**Routes** (owner-scoped like the other collection routes):
`GET /api/collections/:name/memories`, `PUT …/memories/:mem`,
`DELETE …/memories/:mem`, with `General` accepted as `:name`.

## Migration

- **Existing collection `memory.md`:** on first read of a collection that has a
  `memory.md` and no `memory/` directory, the host converts it into one memory —
  `name: imported-notes`, `type: project`, description "Notes carried over from
  the previous collection memory" — and removes `memory.md` and
  `memory.prev.md`. **(call)** One `project` memory means it is indexed, not
  injected whole; the user (or the agent, when asked) can split it.
- **Profile keys** `memory_size` and `auto_update` in `collections.json` are
  ignored on read and dropped on the next write.
- **Soft-skills on disk** (`$OMNIS_HOME/softskills`) are left where they are and
  no longer read. Not migrated.
- **Configs that still name removed things** must keep loading: a `softskills`
  entry in an agent's `tools`, `curator` / `reflector` in the `agents` list or a
  squad, `softskills_dir`, the `OMNIS_CURATOR_*` variables — all ignored with at
  most one log line, never an error (user overlays in `$OMNIS_HOME` will carry
  them for a long time).

## Removal

Removed with soft-skills, because they exist only to produce or serve them:

- `internal/softskills` (toolset, recall, stats, curator, reflector, feedback,
  seeding) and the shipped top-level `softskills/` tree with its packaging
  entries and tests;
- the `curator` and `reflector` agents; `curate_session`,
  `record_session_feedback`; the load recorder, the sub-agent reflection hook,
  the curator hook; the server's idle curator and the session `Harvested` flag;
  `omnis curate`; `/learn-now`;
- the `softskills` tool group from every shipped `agent.json`, the Settings tool
  list, the permission rules and allow-list entries, the skills dock's
  soft-skill kind, the instruction lines that mention soft-skills;
- the collection memory distiller (`DistillCollectionMemory`, the distill and
  revert routes, the auto-update worker and its environment variable);
- `web/docs/20-learning-and-recall.md` is rewritten around memory; CLAUDE.md and
  FEATURES.md are updated.

**Precedents** (`internal/precedents`, `recall_precedents`, `omnis
reindex-precedents`) are consumed only by the reflector and the curator, so they
are removed too. **(call)** The idle indexer and `EventSessionIndexNow` stay —
session search uses them.

## Order of work

1. **Memory, alongside what exists.** Store, tools, injection, chip, card,
   dialog, routes, `/memory`, migration of `memory.md`, removal of the distiller.
   Soft-skills still present. Usable and testable on its own.
2. **Measure.** A squad-bench task set in `omnis-benches` (not in this repo):
   turn 1 gives a correction; a later *new* session in the same collection must
   apply it without being told; a session in another collection must not see
   it. Run it across the leader model tiers — the open question is whether the
   weaker leaders call `memory_save` on a correction at all.
3. **Remove soft-skills, curator, reflector, precedents.**
4. **Only if step 2 shows weak models missing corrections:** a catch-up pass.
   When a session goes idle (the existing rail), one cheap model call reads the
   session's user messages plus the current index and proposes the
   `memory_save` calls the agent should have made; they are applied and shown as
   chips at the top of the next turn. Not built unless measured as needed.

## Constraints for the builder

- Storage and rendering belong in `internal/collectionctx` (or a sibling
  package with the same rule: `internal/paths` + stdlib only), so `agent` can
  use it without an import cycle. The render cache must key on the memory
  directory's content, not just two files.
- The session → collection and session → state-root resolvers already exist
  (`SetCollectionResolver`, `SetCollectionRootResolver`). General currently
  resolves to "no injection"; it must now resolve to General's memory, and a
  nil resolver (CLI/TUI) likewise.
- Tools must resolve the session with `realSessionID`, not `ctx.SessionID()`.
- Names from the model and from the HTTP routes go through one slug validator;
  nothing else ever becomes a path segment.
- Every write path (tool, route, migration) uses the same lock and atomic
  write, and fires `memory_changed`.
- No-op contract: a collection with no memories injects only the short rules
  block; nothing else changes for it.

## Left to the builder

Exact wording of the injected rules (to be tuned on the weakest leader model
first, per the fleet's usual practice); the block's position relative to
AGENT.md and the collection instructions; chip and card styling; i18n keys;
how "a turn the user did not start" is flagged at the entry points; test layout.
