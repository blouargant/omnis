# Implementation plan — ACE-style softskills evolution for yoke (v2)

## Background

**ACE (Agentic Context Engineering)** — paper https://arxiv.org/abs/2510.04618, reference impl at `tmp/ACE/ace/`. Three roles: **Generator** (solves task, cites used "bullets"), **Reflector** (tags bullets `helpful|harmful|neutral` from a correctness signal), **Curator** (delta-updates counters, prunes by harm, adds new bullets from `key_insight`). The no-ground-truth Reflector prompt template lives at [tmp/ACE/ace/ace/prompts/reflector.py:63-115](tmp/ACE/ace/ace/prompts/reflector.py#L63-L115).

**Yoke today** — one curator agent at [internal/softskills/curator.go:147](internal/softskills/curator.go#L147), triggered at `EventSessionEnd` / `EventCurateNow` by [agent/curator_hook.go:76](agent/curator_hook.go#L76). It reads `agent_memory_<key>.md` + `agent_statelog_<key>.json` and writes `softskills/[<agent>/]<skill>/SKILL.md` via `softskill_create/update/delete/index_append` ([internal/softskills/writetool.go](internal/softskills/writetool.go)). No reflector, no success signal, no per-skill usage counters.

**Two-tier observability**

Reflection has to happen at two scopes because yoke has two session lifecycles:

| Scope | When it ends | Bus event today | What we can judge |
|---|---|---|---|
| Leader session | TUI quit / web UI close / idle timeout | `EventSessionEnd` (and `EventCurateNow` on `/learn`) | Did the user's overall task succeed? Cross-cutting decisions. |
| Sub-agent invocation | Sub-agent's internal runner returns to the leader (one tool call) | None — sub-agent internal runner does NOT emit `EventRunStart/End` to the bus ([core/events/events.go:280-283](core/events/events.go#L280-L283)). Tool/model events DO flow with `agent=<sub-agent>` because [agent/squad.go:143](agent/squad.go#L143) attaches `AgentCallbacks` to each sub-agent. | Did the leader retry the sub-agent, accept its output, or override it? Per-sub-agent skill attribution. |

Both tiers feed into one stats sidecar and one curator. The curator still only fires once per leader session — sub-agent micro-reflection just updates counters more frequently.

**Scope** — softskills only. Compression (`internal/compress/`) is **out of scope**: ACE is knowledge accumulation, not window fitting.

## Data model

Counters cannot go in `SKILL.md` frontmatter (loader rejects extra fields — see comment at [internal/softskills/softskills.go:10](internal/softskills/softskills.go#L10)). They live in:

```
$YOKE_HOME/softskills/_stats.json
```

Leading underscore keeps the file out of `run_glob softskills/*/SKILL.md`. Keyed by `<agent>/<name>` (leader uses bare `<name>`):

```json
{
  "version": 1,
  "entries": {
    "investigator/k8s-pod-evidence": {
      "loaded_count": 14,
      "helpful": 8,
      "harmful": 1,
      "neutral": 5,
      "first_loaded_at": "2026-01-04T12:01:00Z",
      "last_loaded_at":  "2026-05-26T09:14:00Z",
      "last_session":    "teaching-kite"
    }
  }
}
```

Explicit user feedback (Phase 5) lives in a parallel sidecar:

```
$YOKE_HOME/logs/agent_feedback_<key>.json   # {question, answer, timestamp}
```

## Phased rollout

Six phases. Each is independently mergeable. Do **not** combine.

---

### Phase 1 — Stats sidecar (passive recording, no behavior change)

**Goal**: record `load_softskill` calls in `_stats.json` at leader session end. Bumps `loaded_count`, no tagging yet.

**Files to create**:

- `internal/softskills/stats.go`:
  - `type StatsEntry struct { LoadedCount, Helpful, Harmful, Neutral int; FirstLoadedAt, LastLoadedAt time.Time; LastSession string }`
  - `type Stats struct { Version int ` + "`json:\"version\"`" + `; Entries map[string]*StatsEntry ` + "`json:\"entries\"`" + ` }`
  - `func LoadStats(dir string) (*Stats, error)` — empty struct on missing file.
  - `func (s *Stats) Save(dir string) error` — temp-file + rename, `flock`'d via [internal/softskills/writetool_flock.go](internal/softskills/writetool_flock.go).
  - `func (s *Stats) RecordLoad(key, sessionName string, t time.Time)`
  - `func (s *Stats) RecordTag(key, tag string)` — `tag ∈ {"helpful","harmful","neutral"}`.
  - `func Key(agent, name string) string` — joins with `/`, leader uses `name` alone.
- `internal/softskills/stats_test.go` — round-trip, concurrent-write, key building, missing-file recovery.

**Files to modify**:

- `agent/curator_hook.go` — add a recorder ahead of `curatorGate` (so even gated-out sessions get counted):
  - Read `paths.LogsDir() + "/agent_events_<buildTs>.log"` (build-global). Filter `event == "after_tool" && payload.tool == "load_softskill" && payload.session_id == sessionID`. Extract `payload.input.name`. Distinct → `stats.RecordLoad`. Save once.
- `core/events/events.go` — verify `EventAfterTool` payload carries `session_id` and `user_id`. Today the `afterTool` closure at [core/events/events.go:191-206](core/events/events.go#L191-L206) does **not** include session/user IDs — only `agent`, `tool`, `input`, `output`, `duration`, `call_id`. **Add** `session_id` and `user_id` to the payload by threading them through `AgentCallbacks` (carry the session from the runner; for the leader path, `PluginWithOptions`'s `BeforeRun/AfterRun` already has it; for sub-agent path, the `tool.Context` doesn't expose session — accept that sub-agent-internal tool calls won't have session_id, but the leader's `load_softskill` calls will).
  - Critical insight: `load_softskill` is called by *whichever agent owns the Skill tool group*. The leader has it. Sub-agents may also have it (per agent.json's `skills` field). We need session_id on **both** code paths.
  - Implementation: extend `AgentCallbacks` to accept a session-id provider closure, or add session to the bus via a separate per-session stamping mechanism. Simplest: extend `tool.Context` usage to read `tctx.Session()` if ADK exposes it; otherwise stamp via a wrapping callback in `agenttool` configuration.

**Acceptance**:

- Two-session local test: run a session that loads `foo` and `bar` (any softskills) twice each via leader and once via a sub-agent. After session end, `_stats.json` shows `loaded_count: 2` for `foo` and `bar` (leader-level) and `loaded_count: 1` for the sub-agent's key.
- `go test ./internal/softskills/...` passes.
- `go vet ./...` and `go test ./agent/...` pass.

---

### Phase 1b — Sub-agent run-boundary event

**Goal**: a bus event fired when a sub-agent invocation completes, carrying everything Phase 2 needs for per-call reflection. No reflection logic yet.

**Approach**: synthesize from the leader's view. When the leader's `AgentCallbacks.AfterTool` sees `tool.Name() == <sub-agent name>`, that *is* the sub-agent's return. No need to wire callbacks into the sub-agent's internal runner.

**Files to create**:

- `agent/subagent_event.go`:
  - On bus init, subscribe to `EventAfterTool` once. Filter: `payload.tool ∈ <set of sub-agent names>` from `runtime.Agents`. Re-emit as new event `EventSubAgentEnd` with payload:
    ```
    { user_id, session_id, agent: <sub-agent name>, input: <leader's prompt to the sub-agent>, output: <sub-agent's final text>, duration, call_id }
    ```
  - Also subscribe to `EventBeforeTool` for the same set → re-emit as `EventSubAgentStart`.
- `core/events/events.go`:
  - Add `EventSubAgentStart = "subagent_start"`, `EventSubAgentEnd = "subagent_end"` constants. Document payload in the same comment block as `EventCuratorStart/End`.
- `agent/subagent_event_test.go` — fake bus + fake AfterTool event → assert SubAgentEnd is emitted with the expected payload.

**Files to modify**:

- `agent/infrastructure.go` — wire `registerSubAgentBoundary(bus, subAgentNames)` once at infrastructure boot. Return the `[]*Subscription` for clean teardown (matches the `registerCuratorHook` pattern at [agent/curator_hook.go:170-173](agent/curator_hook.go#L170-L173)).

**Acceptance**:

- Manual run: leader delegates to `investigator`; events log shows `subagent_start agent=investigator` followed by `subagent_end agent=investigator` with the sub-agent's output text in the payload.
- Hot-reload: triggering `/api/config/reload` does not produce duplicate `subagent_end` events on next sub-agent call (subscriptions are detached and re-attached).

---

### Phase 2 — Heuristic reflector (deterministic, no LLM)

**Goal**: synthesize `{success, tags}` from existing artefacts. Runs at **both** scopes: `EventSubAgentEnd` (per-call, sub-agent-scoped) and `EventSessionEnd` (session-wide).

**Files to create**:

- `internal/softskills/reflector_heuristic.go`:
  - `type Outcome struct { Success Tristate; Confidence float64; Signals []string; Tags map[string]string }`
  - `type Tristate int; const (Positive Tristate = iota + 1; Negative; Ambiguous)`
  - `type HeuristicInputs struct { StateLog *compress.StateLog; LastUserMessages []string; ToolErrors []ToolError; LoadedSkills []string; SubAgentRetried bool /* phase-6 use */ }`
  - `func ReflectHeuristic(in HeuristicInputs) Outcome`
- `internal/softskills/reflector_heuristic_test.go` — table-driven.

**Heuristics**:

- **Positive markers**: `OpenIssues == 0 && len(Decisions) > 0`; final user message matches `(?i)\b(thanks|works|perfect|great|good|exactly|nice)\b` with no negation in the same message; zero tool errors in the last 5 tool calls.
- **Negative markers**: `OpenIssues` non-empty at end; final user message matches `(?i)\b(no|wrong|broken|doesn'?t|isn'?t|fail|error|bad)\b` and the previous assistant turn proposed a fix; ≥1 tool error in the last 5 tool calls; `SubAgentRetried == true` (phase 6).
- **Per-skill tag default `neutral`**. Tag `helpful` when `Success == Positive` and the skill was loaded. Tag `harmful` when `Success == Negative` *and* a tool error occurred after the load (timestamp comparison). When in doubt, stay `neutral`.

**Files to modify**:

- `agent/curator_hook.go`:
  - Refactor inputs gathering (statelog parse, conversation file read, tool-error scan) into a helper `gatherSessionSignals(key, sessionID, userID) HeuristicInputs`.
  - After `RecordLoad`, call `ReflectHeuristic`, then `RecordTag` for each loaded skill. Save stats.
- `agent/subagent_hook.go` (new) — subscribe to `EventSubAgentEnd`:
  - Gather sub-agent-scoped signals: skills loaded *during this sub-agent invocation* (from events between matching `subagent_start` and `subagent_end` `call_id`s), tool errors in that window, and the sub-agent's output text (the leader's reaction is unknown at this point — Phase 6 supplies it).
  - At this phase, with no leader reaction yet, only call `RecordLoad` on per-invocation loads (so we don't double-count if Phase 1 already counted at session end). **Move** the load-counting to this hook for sub-agent skills, leaving Phase 1's recorder responsible only for leader-loaded skills.

**Acceptance**:

- Session ending with empty `OpenIssues` + "thanks, perfect" + two loaded softskills → both `helpful++`.
- Session ending with tool_error after `load_softskill foo` + "no that broke" → `foo` `harmful++`, others `neutral++`.
- Sub-agent invocation that loads `bar`, returns cleanly, leader continues without retry → `bar` gets `loaded++` but no tag yet (Phase 6 adds the retry signal).

---

### Phase 3 — LLM Reflector agent (session-scope only)

**Goal**: at `EventSessionEnd`, run a Reflector LLM **before** the curator. Merge its tags with the heuristic (LLM authoritative on overlap, heuristic fills gaps). Sub-agent micro-reflection stays heuristic in this phase — LLM-per-call is too expensive.

**Files to create**:

- `registry/agents/reflector/agent.json`:
  ```json
  {
    "name": "reflector",
    "description": "Post-session analyst that tags loaded soft-skills as helpful/harmful/neutral and extracts a key_insight.",
    "model_ref": "<cheap profile, e.g. same as summariser>",
    "tools": ["run_read"],
    "skills": [],
    "builtin": true
  }
  ```
- `registry/agents/reflector/instruction.md` — adapted from [tmp/ACE/ace/ace/prompts/reflector.py:63-115](tmp/ACE/ace/ace/prompts/reflector.py#L63-L115). Inputs (in the user prompt): audit path, statelog path, list of loaded softskills (`agent/name`), sub-agent retry list, tool errors with timestamps, last 3 user messages, optional explicit feedback (Phase 5). Output (strict JSON):
  ```json
  {
    "reasoning": "...",
    "success": "positive|negative|ambiguous",
    "key_insight": "... or empty string",
    "bullet_tags": [
      {"key": "investigator/k8s-pod-evidence", "tag": "helpful", "reason": "..."}
    ]
  }
  ```
  Hard rule: must NEVER write files (no softskill_* tools mounted; only `run_read`).
- `internal/softskills/reflector.go`:
  - `type ReflectorConfig struct { Model model.LLM }`
  - `func NewReflector(ctx, ReflectorConfig) (adkagent.Agent, error)` — mirrors `NewCurator` at [internal/softskills/curator.go:147](internal/softskills/curator.go#L147).
  - `type ReflectInputs struct { AuditPath, StateLogPath string; LoadedSkills []string; SubAgentRetries []SubAgentRetry; ToolErrors []ToolError; LastUserMessages []string; FeedbackPath string }`
  - `func Reflect(ctx, *runner.Runner, ReflectInputs) (Outcome, error)` — runs agent, parses JSON envelope, returns `Outcome`. Malformed JSON → return zero `Outcome` + error so caller falls back.
  - `func ReflectorRunner(ctx, ReflectorConfig) (*runner.Runner, error)` — convenience pairing.
- `internal/softskills/reflector_test.go` — JSON envelope parse, malformed fallback.

**Files to modify**:

- `agents.json` (search-chain layer in `.agents/` for dev, eventually `/etc/yoke/`) — append `"reflector"` to the `agents` array. Squads do **not** need it (not delegable from the leader).
- `agent/curator_hook.go`:
  1. Run heuristic (already wired in Phase 2).
  2. If curator gate fires, build a `Reflector` runner with 60-second timeout. Merge its `Outcome` with the heuristic's (LLM wins on overlapping keys).
  3. Apply merged tags to stats.
  4. Pass merged `Outcome` into `CurateInputs` (Phase 4).
- `agent/instance.go` — verify reflector is picked up by `BuildInstance` ([agent/instance.go:101](agent/instance.go#L101))'s agent loop. Should be automatic since it's listed in `runtime.Agents`.
- `CLAUDE.md` (project-level) — add reflector to the agent topology section per the self-maintenance rule.

**Acceptance**:

- `/learn-now` on a session: event log shows `before_model agent=reflector` → `after_model` → existing curator events.
- `_stats.json` increments match the reflector's `bullet_tags`.
- Reflector failure (timeout, JSON parse error) is logged; heuristic outcome is used; curator still runs.

---

### Phase 4 — Curator consumes Outcome + stats; delta-update discipline

**Goal**: the curator's prompt stops subjectively deciding "obsolete or actively harmful" and consults stats + reflector tags.

**Files to modify**:

- `internal/softskills/curator.go`:
  - Extend `CurateInputs` with `Outcome *Outcome` and `Stats *Stats`.
  - `buildCuratePrompt` adds:
    ```
    6. Reflector outcome: success=<positive|negative|ambiguous>, key_insight=<...>
    7. Per-skill stats (top 20 by loaded_count):
       - investigator/k8s-pod-evidence: loaded=14 helpful=8 harmful=1 neutral=5
    8. Skills the reflector tagged 'harmful' this session, with reasons:
       - <key>: <reason>
    ```
  - Update `CuratorPrompt` rules (replacing the subjective wording at [internal/softskills/curator.go:90](internal/softskills/curator.go#L90)):
    - **Delete** a skill only when `(stats.Harmful >= 3 && stats.Harmful > stats.Helpful)` OR (reflector tagged it `harmful` with reason mentioning "wrong assumptions" or "superseded"). Existing 20-char `reason` rule stays.
    - **Update** when the reflector's `key_insight` cleanly extends an existing skill (justify in `reason`).
    - **Create** only when `success == positive` AND `key_insight` is non-empty AND no near-duplicate exists in the run_glob audit.
    - **Skip** is default and acceptable.
  - "At most one action per agent per invocation" rule stays.

**Acceptance**:

- `success == ambiguous` and zero harmful tags → curator writes nothing, emits the one-line rationale at [internal/softskills/curator.go:117](internal/softskills/curator.go#L117).
- `foo` with `harmful=4, helpful=1` → curator calls `softskill_delete` + `softskill_index_remove`.
- Non-empty `key_insight` + no near-duplicate → exactly one `softskill_create` + `softskill_index_append`.
- Existing `internal/softskills/curator_test.go` cases pass with empty `Outcome` / `Stats` defaults (backwards-compat).

---

### Phase 5 — Explicit leader wrap-up (interactive surfaces only)

**Goal**: when the leader judges work complete on an interactive surface, ask one low-friction question. Capture the answer for the Reflector.

**Files to create**:

- `softskills/wrap-session/SKILL.md` (intentionally a softskill, not authored — easy to disable by deletion):
  ```
  ---
  name: wrap-session
  description: One-shot wrap-up question for explicit feedback before idle harvest. Use only when work is complete on an interactive surface.
  ---
  # Wrap Session
  ## Context
  ...
  ## Steps
  1. Call AskUserQuestion with kind="text", prompt="Anything off, or are we good to wrap?".
  2. Persist answer to logs/agent_feedback_<key>.json (one record per session: {question, answer, timestamp}).
  ## Constraints
  - Fire at most once per session.
  - Skip on CLI one-shot, A2A inbound, scheduled runs (no human reachable).
  ## Validation
  ...
  ```
- `internal/softskills/feedback.go` — read/write `agent_feedback_<key>.json`, `func RecordFeedback(key, question, answer string) error` + `func LoadFeedback(key string) (*Feedback, error)`.
- `internal/softskills/feedback_test.go`.

**Files to modify**:

- `registry/agents/leader/instruction.md` — append:
  ```
  Session wrap-up: when (a) the runtime is interactive (TUI or Web UI), (b) all user-stated tasks are complete or blocked on input, and (c) you have not already done so this session, call 'load_softskill wrap-session' and follow it. NEVER fire on CLI one-shot, A2A inbound calls, or scheduled runs.
  ```
- `internal/softskills/reflector.go` (Phase 3) — `ReflectInputs.FeedbackPath` added; the reflector prompt includes "Explicit user feedback" block when present and gives it more weight than implicit signals.
- `internal/softskills/reflector_heuristic.go` (Phase 2) — when `FeedbackPath` exists, prefer the explicit answer: positive keywords → `Success=Positive`, negative → `Success=Negative`. Skip the heuristic's user-message scan in that case.

**Acceptance**:

- TUI session ending normally: leader fires wrap question; "yes but the X bit was wrong" → reflector tags `harmful` on the skill(s) connected to X.
- CLI one-shot: wrap is not fired (instruction's interactive gate).
- Leader does not fire the wrap twice in one session even across multiple completion-like moments (loader rule — soft-skill loads are recorded per-session in the existing skill-memory mechanism at [docs/skills.md:220](docs/skills.md#L220)).

---

### Phase 6 — Sub-agent micro-reflection signals

**Goal**: feed the per-sub-agent reflection (Phase 2's `EventSubAgentEnd` heuristic) with real signals so it can tag skills `helpful` / `harmful` per sub-agent call — not just `loaded`.

**Key signal sources** (all derivable from the bus, no new instrumentation required after Phase 1b):

1. **Sub-agent retry** — the leader instruction at [registry/agents/leader/instruction.md](registry/agents/leader/instruction.md) explicitly says to retry sub-agents with refined prompts. So if the leader calls `investigator` twice within one user turn with similar prompts, the first call's skills are tagged `harmful`. Detect: `EventSubAgentEnd agent=X` followed by `EventSubAgentStart agent=X` within the same `run_id` (leader's user turn).
2. **Sub-agent error or empty result** — `EventSubAgentEnd payload.output` empty/short/contains `"Error:"` prefix → tag loaded skills `harmful`.
3. **Leader's reaction text** — between `EventSubAgentEnd` and the leader's next sub-agent call or its `EventRunEnd`, the leader's assistant text either (a) cites the sub-agent's output approvingly, (b) re-tasks the sub-agent, (c) acts directly. (a) is `helpful`, (b) is `harmful`, (c) is ambiguous → `neutral`. Lexical scan is sufficient: presence of `"investigator reported"` / `"per investigator"` etc → (a); `"investigator failed"` / `"let me ask again"` → (b).

**Files to create**:

- `internal/softskills/subagent_reflector.go` — per-invocation aggregator:
  - `type SubAgentInvocation struct { Agent string; LoadedSkills []string; ToolErrors []ToolError; OutputText string; Retried bool; LeaderReaction LeaderReaction }`
  - `type LeaderReaction int; const (LeaderApproved LeaderReaction = iota+1; LeaderRetried; LeaderUnknown)`
  - `func TagInvocation(inv SubAgentInvocation) map[string]string` — applies the heuristics above, returns `key → tag`.
- `internal/softskills/subagent_reflector_test.go` — table-driven.

**Files to modify**:

- `agent/subagent_hook.go` (created in Phase 2) — extend to buffer invocations per leader run (key by `run_id` from `EventRunStart/End`). At `EventRunEnd`, walk the buffered invocations *in order*:
  - For each, decide `Retried` by looking at the next invocation with the same `agent`.
  - Decide `LeaderReaction` by lexically scanning the leader's assistant text between this invocation's end and the next sub-agent call (or run end).
  - Call `TagInvocation`, then `stats.RecordTag` for each tagged key.
  - Save stats once per `EventRunEnd`.

**Files to modify (events plumbing)**:

- `core/events/events.go` — the `BeforeRun` / `AfterRun` payload (currently at [core/events/events.go:286-302](core/events/events.go#L286-L302)) carries `user_id` and `session_id`; ensure it also carries a `run_id` (callback context address or a fresh UUID). The buffer keying depends on this.

**Acceptance**:

- Run a session where the leader calls `investigator`, sees empty result, retasks: first call's loaded skills get `harmful++`, second call's get `helpful++` if the second succeeds.
- Run a session where the leader calls `investigator`, takes the result, and continues: loaded skills get `helpful++`.
- Sub-agent throws a tool error mid-call: that call's skills get `harmful++` regardless of leader reaction.
- `_stats.json` updates fire on every `EventRunEnd`, not just `EventSessionEnd` (so streaming feedback is available before the session formally closes).

---

## Cross-cutting concerns

- **Hot-reload**: every new event subscription returns its `*events.Subscription`. The owning struct (`Infrastructure` for phase 1b, `Instance` for the rest) must call `Off()` on teardown. Match the existing pattern at [agent/curator_hook.go:170-173](agent/curator_hook.go#L170-L173).
- **Concurrency**: `_stats.json` and `agent_feedback_*.json` writes are `flock`'d via the existing helper at [internal/softskills/writetool_flock.go](internal/softskills/writetool_flock.go). Two sub-agent micro-reflections can fire at once on different sessions; the lock serialises them.
- **Per-build state**: stats and feedback are global (no `<buildTs>` suffix) so they survive rebuilds. Audit/statelog files keep per-build naming.
- **MCP / agent dedup**: reflector is a normal agent and gets the same dedup treatment automatically.
- **Backwards compatibility**: existing `CurateInputs` defaults to nil `Outcome` and nil `Stats` — older test fixtures still pass.
- **CLAUDE.md self-maintenance**:
  - After Phase 1: add `_stats.json` and `agent_feedback_*.json` to the filesystem layout.
  - After Phase 1b: add `EventSubAgentStart/End` to the architecture section.
  - After Phase 3: add the reflector to agent topology + adding-a-sub-agent instructions.
  - After Phase 5: document `softskills/wrap-session/SKILL.md` as a "built-in" softskill (yes, the contradiction is intentional — it ships in `softskills/` so it's deletable).
  - After Phase 6: document the leader-run-id event plumbing.

## Tests to write or extend

- `internal/softskills/stats_test.go` — Phase 1.
- `agent/subagent_event_test.go` — Phase 1b.
- `internal/softskills/reflector_heuristic_test.go` — Phase 2.
- `internal/softskills/reflector_test.go` — Phase 3.
- `internal/softskills/curator_test.go` (extend existing) — Phase 4.
- `internal/softskills/feedback_test.go` — Phase 5.
- `internal/softskills/subagent_reflector_test.go` — Phase 6.
- `agent/curator_hook_test.go` (create) — integration of phases 1-3 wiring.
- `agent/subagent_hook_test.go` (create) — Phase 6 integration with fake bus + fake events sequence.

## Open questions to resolve while implementing

1. **Phase 1 / events.go**: does `tool.Context.Session()` exist in ADK, or do we need to thread the session through a different channel? Inspect `google.golang.org/adk/tool` before writing the `session_id` plumbing.
2. **Phase 3 model_ref**: pick the cheapest profile available (likely the same one `summariser` uses). Confirm in [models.json](models.json) before committing the agent.json.
3. **Phase 4 thresholds**: `harmful >= 3 && harmful > helpful` is a guess. After phases 1–3 run for a few weeks on real sessions, look at `_stats.json` distributions and tune.
4. **Phase 6 lexical scan**: the "leader approved" detector is fragile. After implementing, validate on 10–20 real sessions; if false-positive rate is high, escalate to an LLM micro-classifier (cheap model, one call per `EventRunEnd`, JSON output `{reaction: "approved"|"retried"|"ignored"}`).

## Out of scope

- Bullet-level granularity inside `SKILL.md` (ACE's flat numbered bullets). Counters stay at skill level.
- Semantic dedup via embeddings (ACE's `BulletpointAnalyzer`). Yoke's softskill corpus is small; existing curator redundancy audit suffices.
- ACE applied to `internal/compress/`. Different problem.
- Online (mid-session) playbook updates. Yoke curates strictly post-session.

## Recommended ordering

Phases 1 → 1b → 2 → 3 → 4 → 5 → 6 in order. Phase 1b is the only one that touches the events package fundamentally, so land it early and let it bake. Phase 6 depends on every prior phase plus the leader-run-id plumbing.
