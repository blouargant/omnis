# Context Management

This document explains how the intelligent context manager (`internal/compress`) works, the design decisions behind it, and everything built in the current development session.

---

## The problem

Every LLM has a finite context window. During a long agentic session — diagnosing a Kubernetes incident, iterating on a large codebase, investigating a multi-step bug — the conversation grows without bound:

- Large tool responses (`kubectl logs`, `describe`, file reads)
- Repeated tool calls with identical arguments
- Skills loaded for one sub-task that were never used again
- Verbose intermediate reasoning and long model replies

Without intervention the agent eventually hits the provider's hard limit, which terminates the session abruptly. Truncating blindly from the front destroys goal context; truncating from the back destroys recent state.

---

## Design principles

1. **Active, not passive.** The manager rewrites the live `LLMRequest.Contents` slice in `BeforeModelCallback`. Earlier versions only appended a summary side-car file — the model never actually received a shorter context.
2. **Pipeline of passes.** Compression is a sequence of independent, ordered steps applied until the token count drops below a target. Cheap passes run first; expensive ones (LLM call) only run when cheaper passes are not enough.
3. **Preserve head and tail.** The original goal (first N turns) and the most recent work (last M turns) are never touched. Only the middle — the "already processed" history — is eligible for compression.
4. **Per-session isolation.** Every `(userID, sessionID)` pair gets its own state: token counter, cache, force-compact flag, recent user turns, and state log. Concurrent sessions never interfere.
5. **Best-effort, never blocking.** Compression is a best-effort optimisation. Failures (LLM error, parse error, cache miss) fall back gracefully and never crash the agent loop.

---

## Architecture

```
BeforeModelCallback
  │
  ├─ maybeMarkTaskSwitch      (tasksniff.go) — flip forceCompact on topic change
  ├─ count tokens             (tokens.go)
  ├─ evaluate trigger         soft / hard / forced
  │
  └─ runPipeline
       ├─ PassDedupeToolCalls       (passes.go)
       ├─ PassTruncateToolResults   (passes.go)
       ├─ PassDropUnusedSkills      (passes.go)
       └─ PassSummarizeMiddle       (passes.go)  ← only on hard / forced
            └─ cachedSummariser     (cache.go + compress.go)

AfterModelCallback
  └─ maybeRefreshStateLog     (statelog.go) — every N turns
```

### Token counting (`tokens.go`)

Uses the `tiktoken-go` cl100k_base encoder (the same tokenizer as GPT-4 / Claude). Per-turn overhead of 4 tokens is added to match real-world provider billing. The public surface is:

```go
CountText(s string) int
CountPart(p *genai.Part) int
CountContents(cs []*genai.Content) int
```

### Triggers

| Trigger | Condition | Pipeline |
|---------|-----------|----------|
| `soft` | tokens ≥ `SoftRatio × WindowTokens` | Cheap passes only (no LLM call) |
| `hard` | tokens ≥ `HardRatio × WindowTokens` | Full pipeline (LLM summary) |
| `forced` | `compact_now` tool called by agent | Full pipeline |
| task switch | New user turn starts with imperative verb + novel file/path tokens | Full pipeline |

---

## Passes

Passes execute left to right. Each pass receives the full content slice and a `keepRecent` count; it must never touch the last `keepRecent` turns. When a pass makes no change, the pipeline skips subsequent comparisons and moves on.

### 1. `PassDedupeToolCalls`

Finds repeated `(tool_name, args_hash)` pairs within the compressible region. For each duplicated call, every response except the most recent one is replaced by a one-line marker:

```
[deduped: superseded by a later call with identical args]
```

The original `FunctionCall` parts are preserved so the model can see what was tried.

### 2. `PassTruncateToolResults`

Any `FunctionResponse` whose JSON payload exceeds `ToolResultMaxBytes` (default 4 096 B) is replaced by a structure preserving a head/tail snippet and the original byte count:

```json
{
  "truncated": true,
  "original_bytes": 45000,
  "head": "...<first 2048 chars>...",
  "tail": "...<last 2048 chars>..."
}
```

This is particularly effective for `kubectl logs` and file reads which routinely produce tens of kilobytes.

### 3. `PassDropUnusedSkills`

Scans every `load_skill` call in the compressible region. If the skill name never appears again in any later turn (as a tool argument, tool name, or plain text), the call turn and its paired response turn are dropped entirely. The model can always re-issue `load_skill` if it needs the skill again.

### 4. `PassSummarizeMiddle`

The heavyweight pass, only run on `hard` or `forced` triggers. The conversation is split into three regions:

```
head (KeepHeadTurns)  |  middle (eligible)  |  tail (KeepRecentTurns)
```

The middle is replaced by a single synthetic user turn:

```
## State log (durable facts)        ← injected when available (see below)
...

## Compressed history (auto-generated)
<prose summary>
```

The summary is produced by calling the configured `LLM` with the rendered transcript of the middle. When no LLM is configured, an extractive fallback lists file paths and tool names found in the dropped turns.

---

## Summary cache (`cache.go`)

The LLM summary call is expensive. If the same middle region appears in two consecutive pipeline runs (e.g. the user issues several rapid-fire questions before context decays), the second call can reuse the cached result.

**Key design:**
- Per-session LRU with capacity 16.
- Cache key = `xxhash.Sum64` over `"h=<keepHead>;r=<keepRecent>;n=<len(middle)>;" + renderTranscript(middle)`.
- Boundary values are included in the key so a config change automatically invalidates stale entries.
- The cache is nil-safe: a disabled or failed LRU silently bypasses caching without error.

**Measured effect** in the Kubernetes E2E test (9 compression rounds): the LLM summary was called only once for each unique middle range despite repeated pipeline executions.

---

## State Log (`statelog.go`)

Aggressive compression replaces large middle sections with a single prose paragraph. Without additional scaffolding, key facts (the current goal, decisions already taken, files touched) can be buried under too-brief prose and then forgotten.

The State Log solves this by extracting a **structured, durable digest** independently of the prose summary.

### Schema

```go
type StateLog struct {
    Goal       string            // current session objective
    Decisions  []string          // decided items (de-duplicated across merges)
    OpenIssues []string          // pending questions
    Files      map[string]string // path → one-line fact
    Tools      map[string]int    // tool_name → cumulative call count
    TurnCount  int               // total model-response count (tracked by compress plugin, not LLM-extracted)
}
```

### Lifecycle

1. `AfterModelCallback` increments an atomic `totalTurns` counter per session.
2. Every `StateLogEvery` turns (default 5) — or on `compact_now` — the extractor runs.
3. The extractor sends the last 3 user turns to `StateLogLLM` (falls back to `LLM`) with a strict "JSON only" prompt.
4. The returned JSON is `json.Unmarshal`'d into a `StateLog`, then merged into the in-memory log (slices de-duplicated, maps merged, tool counts summed).
5. `TurnCount` is stamped from the atomic counter (always, even when no LLM delta was returned), then the merged log is persisted to `StateLogPath` (default `.agent_statelog.json`). This ensures `TurnCount` is always accurate and up-to-date regardless of whether an LLM extraction ran.
6. On the next `PassSummarizeMiddle` run, `renderForPrompt()` prepends the log as a Markdown block above the prose summary, ensuring the model always receives durable facts even after heavy compression.

`TurnCount` is consumed by the curator pre-flight gate to decide whether a session is substantive enough to warrant a full curation run (see [skills.md — Pre-flight gate](skills.md#lifecycle)).

---

## Event bus integration (`core/events`)

Three new events were added so any subscriber — the TUI, the file logger, external monitoring — can observe compression activity:

| Event name | When fired | Key payload fields |
|---|---|---|
| `compression_start` | Before pipeline runs | `trigger`, `tokens_before` |
| `compression_end` | After pipeline completes | `trigger`, `tokens_before`, `tokens_after`, `passes`, `duration_ms` |
| `compression_skipped` | Token count below both thresholds | `tokens`, `soft`, `hard` |

Wire via:

```go
cfg := compress.Config{
    EventBus: events.NewBus(),
    ...
}
```

---

## Task-switch detection (`tasksniff.go`)

Long sessions frequently involve multiple distinct sub-tasks. The sniffer heuristic flips `forceCompact` automatically when:

1. The new user turn **starts with an imperative verb** (add, build, fix, refactor, implement, create, write, remove, delete, rename, migrate, extract, split, merge, rewrite, document, test).
2. The turn references **path-like tokens** (file paths, symbol names with extensions) that were **not present** in any of the previous 3 user turns.

This avoids mid-task compression while catching the natural "ok, now let's fix the other thing" pattern that characterises real sessions.

---

## `compact_now` tool

The agent can request compression explicitly at any point:

```
compact_now(reason="finished the feature, starting review")
```

This sets a per-session `forceCompact` flag, which triggers the full pipeline on the next `BeforeModelCallback`. The tool is returned by `PluginWithTools` and must be mounted on the agent:

```go
plug, compactTools, wait, err := compress.PluginWithTools("compress", cfg)
// ...
tools := append(fstools.New(), compactTools...)
```

---

## Configuration reference

```go
compress.Config{
    // Token budget
    WindowTokens:       200_000, // model's effective context window
    SoftRatio:          0.75,    // cheap-pass trigger
    HardRatio:          0.92,    // full-pipeline trigger

    // Region preservation
    KeepHeadTurns:      2,       // verbatim leading turns
    KeepRecentTurns:    4,       // verbatim trailing turns

    // Pass tunables
    ToolResultMaxBytes: 4096,    // max bytes per tool response

    // LLM for middle summarisation
    LLM: myLLM,

    // Audit log (per-session via PathFunc, or single path)
    AuditPathFunc: func(u, s string) string { ... },
    AuditPath:     ".agent_memory.md",

    // Events (optional)
    EventBus: bus,

    // State Log (optional)
    StateLogLLM:      cheapLLM,  // nil = same as LLM
    StateLogEvery:    5,
    StateLogPathFunc: func(u, s string) string { ... },
    StateLogPath:     ".agent_statelog.json",
}
```

All fields have safe defaults. `LLM: nil` is valid and falls back to extractive summarisation.

---

## Runtime artefacts

| File | Created by | Content |
|---|---|---|
| `.agent_memory_<u>_<s>.md` | `audit()` | Append-only compression event log |
| `.agent_statelog.json` | `persistStateLog()` | Latest merged `StateLog` as JSON |
| `.agent_events.log` | `core/events` file logger | JSONL of all `compression_*` events |

---

## What was built in this session

This section records the changes made during the development session that produced the current implementation. Kept here as a decision log.

### Stage 1 — Active context management (compress v2)

**Problem:** v1 only wrote a side-car `.agent_memory.md` file. The model's actual `LLMRequest.Contents` was never modified, so there was no compression.

**What changed:**

- `BeforeModelCallback` now rewrites `req.Contents` in place.
- Introduced the pipeline of four passes (dedupe, truncate, drop-skills, summarise-middle).
- Replaced the old single-threshold `Threshold` field with a `WindowTokens` + `SoftRatio` / `HardRatio` model.
- Added `tokens.go` (tiktoken cl100k_base token counting).
- Added `passes.go` with all four passes and their helpers.
- Added `tool.go` — `compact_now` explicit compression tool.
- Added `tasksniff.go` — heuristic task-switch detector.
- Full test suite rewritten (`compress_test.go`).
- Wired into `examples/s06_compress` with a tiny window to make the trigger visible in demo runs.
- System prompt updated with a note about `compact_now`.

### Stage 2 — Summary cache, events, State Log

**Problem:** Stage 1 had no observability, called the LLM redundantly when the same middle recurred, and had no mechanism to preserve structured facts through aggressive compression.

**F1 — Summary cache (`cache.go`):**

- Per-session LRU (capacity 16) keyed by `xxhash.Sum64` over the serialised middle + boundary values.
- `cachedSummariser` wraps the base summariser closure in `compress.go`.
- `SplitMiddle` exported from `passes.go` so the manager can compute the key over the exact same middle the pass will use.
- `TestSummaryCacheReuse` verifies that a repeated middle yields exactly one LLM call.

**F2 — Events bus (`core/events/events.go` + `compress.go`):**

- Added `EventCompressionStart`, `EventCompressionEnd`, `EventCompressionSkipped` constants.
- `Config.EventBus *events.Bus` optional field.
- `compressOnce` (the testable core of `beforeModel`) emits start/end/skipped at the right points with token and timing metadata.
- `TestEventBusEmitsCompression` covers handler invocation and token-delta sign.

**F3 — State Log (`statelog.go`):**

- `StateLog` struct with goal, decisions, open issues, files map, and tools count map.
- `merge()` de-duplicates slices and merges maps.
- `renderForPrompt()` outputs a Markdown block prepended to the synthetic summary turn.
- `extractStateLog()` calls `StateLogLLM` with a strict "JSON only" prompt and parses the response; tolerates fenced code blocks.
- `persistStateLog()` writes the merged log to disk as JSON.
- `maybeRefreshStateLog()` wired into `AfterModelCallback`, fires every `StateLogEvery` turns.
- `stateLogDelta()` feeds the last 3 buffered user turns as the extraction transcript.
- `TestStateLogExtractAndPersist` covers extract → merge → persist → render round-trip.

**Refactoring for testability:**

- `beforeModel` delegates to `compressOnce(ctx, userID, sessionID, req)` so tests do not need to implement `agent.CallbackContext`.
- `audit` and `maybeRefreshStateLog` take plain `userID / sessionID` strings instead of the full interface.

### Real-world validation (`examples/s24_k8s_context_e2e`)

**What it does:**

Runs a multi-turn Kubernetes investigation through the full agent loop against a live cluster, with an aggressively small window (`WindowTokens: 2200`) to force compression on every substantive turn. Checks four objective outcomes:

1. `compression_start > 0` and `compression_end > 0` — compression events fired.
2. `max_reduction > 0` and `total_reduction > 0` — at least one pass actually reduced tokens.
3. Final memory-only turn (no tool calls allowed) — agent recalls the unique log marker, namespace and pod name from memory alone.
4. Zero tool calls on the final turn — the agent did not try to re-fetch what it should have remembered.

**Measured result:**

```
PASS: compression_start=9 compression_end=9 max_reduction=52663 total_reduction=131217
PASS: memory recall preserved marker=CTX-MARKER-1777745555-6405 namespace=context-e2e-1777745555 pod=cm-loggen
```

9 compression rounds; 131 217 tokens removed in total; a 52 663-token peak single-round reduction; and the marker was correctly recalled at the end with no tool calls.

**Run:**

```bash
bash scripts/run_k8s_context_e2e.sh
```

The script sources `.env`, creates a temporary namespace with a noisy log-emitting pod, runs the harness, and cleans up.

---

## Further reading

- [docs/k8s-context-compression-e2e.md](k8s-context-compression-e2e.md) — E2E test details and pass/fail contract
- [internal/compress/compress.go](../internal/compress/compress.go) — plugin entrypoint, Config, manager
- [internal/compress/passes.go](../internal/compress/passes.go) — all four passes
- [internal/compress/cache.go](../internal/compress/cache.go) — summary LRU cache
- [internal/compress/statelog.go](../internal/compress/statelog.go) — State Log extraction and persistence
- [internal/compress/tasksniff.go](../internal/compress/tasksniff.go) — task-switch heuristic
- [internal/compress/tool.go](../internal/compress/tool.go) — `compact_now` tool
- [core/events/events.go](../core/events/events.go) — event bus and event name constants
