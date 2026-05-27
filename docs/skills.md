# Authoring skills

A **skill** is a Markdown file under `skills/<name>/SKILL.md` that
encodes a reusable, domain-specific procedure. The harness loads skills
lazily: the YAML frontmatter is read at startup, but the body is only
delivered to the model when the skill is invoked via the `load_skill`
tool.

## Anatomy

```markdown
---
name: my-skill                              # required, must match folder
description: One sentence, action-oriented. # required, this is what the lead matches against
---

# Title

## When to use it

Plain prose describing the trigger conditions and scope.

## Procedure

1. Step one (always start with a read-only confirmation step).
2. Step two.
3. ...

## Hard rules

- Never <destructive action> without explicit user confirmation.
- ...

## Output rule

Always end with `Result: ok | needs-attention | blocked`.
```

## YAML frontmatter

Two fields are required:

| Field         | Notes                                                                                    |
|---------------|------------------------------------------------------------------------------------------|
| `name`        | Lowercase, hyphenated, must match the folder name.                                       |
| `description` | One sentence. Begin with a verb. **Include the trigger keywords explicitly** — the lead matches the user's prompt against this string. |

Example (good):

```yaml
description: Diagnose unhealthy Kubernetes workloads — pods crash-looping, deployments not ready, services not reachable. Use whenever the user mentions kubernetes, k8s, kubectl, pods, deployments, namespaces, or attaches a kubectl error.
```

Example (bad — no triggers):

```yaml
description: Helps with Kubernetes.
```

## Body conventions

The four sections below produce skills that compose well with the
[Claude-Code-style operating method](methodology.md):

1. **When to use it** — disambiguate from neighbouring skills.
2. **Procedure** — numbered, each step verifiable. Always start with a
   read-only confirmation.
3. **Hard rules** — short bullet list of things the agent must *never*
   do. The lead inherits the harness's RESPECT step but skills add
   domain-specific guard-rails.
4. **Output rule** — single closing sentence. Makes the agent's verdict
   machine-readable (`ok`, `needs-changes`, `blocked`, `clean`, etc.).

## Loading mechanism

`internal/skills` wraps ADK's `skilltoolset` and exposes two tools to
the agent:

- `list_skills` — returns name + description for every skill found.
- `load_skill(name)` — returns the body of the named skill so the model
  can follow it.

The lead's system prompt instructs it to prefer skills over ad-hoc
reasoning when one matches.

## Examples shipped in the repo

| Skill            | What it demonstrates                                                  |
|------------------|-----------------------------------------------------------------------|
| `review`         | A truly generic review/audit playbook. Works on any artefact.         |
| `agent-builder`  | Meta-skill: how to scaffold a new specialist agent.                   |
| `pdf`            | A narrow, tool-bound skill (uses an external PDF extraction CLI).     |
| `k8s-triage`     | A full domain specialisation example (Kubernetes incident triage).    |

## Tips

- **Keep skills short.** A skill is not a tutorial; it's a checklist
  the model can execute.
- **One verb per step.** "Snapshot pod state with `kubectl get -o yaml`"
  beats "look at how the pods are doing".
- **Cite tools by name.** If a step needs the `bash` tool or the
  `kubernetes/get_pods` MCP tool, name it explicitly.
- **Encode safety inline.** Don't rely on `permissions.json` alone;
  state the destructive verbs in the Hard rules section so the model
  can self-restrict before even calling the tool.
- **Test the trigger.** Run the root binary and ask a typical question; see
  whether `load_skill` picks the right skill. Adjust the description if
  not.

## Anti-patterns

- ❌ Skills that re-state the universal operating method (`restate →
  plan → ...`). The lead already does that.
- ❌ Skills that hard-code paths or hostnames. Pass them through tool
  arguments or env.
- ❌ Skills that exceed ~150 lines. Split into two; let the lead chain
  them.

## Soft-skills (curator-managed)

Alongside the hand-written `skills/` library, the harness maintains a
parallel **`softskills/`** library that is written *by the agent itself*.
After each session ends (or when the user asks "save this as a skill"),
a dedicated **curator** sub-agent inspects the session's audit log and
StateLog, decides whether the run produced a reusable insight, and if so
appends a short SKILL.md plus an INDEX.md entry.

| Aspect              | `skills/` (authored)             | `softskills/` (curated)              |
|---------------------|----------------------------------|---------------------------------------|
| Source              | Humans                           | Curator sub-agent                     |
| Trigger to write    | `git commit`                     | `EventSessionEnd`, `/learn-now`, or idle harvester |
| Format              | `<name>/SKILL.md` w/ frontmatter | Same                                  |
| Loaded by lead via  | `list_skills` / `load_skill`     | `list_softskills` / `load_softskill`  |
| Mutated by lead     | No                               | No (write/delete tools mounted on curator only) |
| Permissions         | n/a                              | `softskills/` writes denied to lead in `permissions.json` |

### Lifecycle

1. The lead works through the user's request as usual; `compress`
   writes per-session `.agent_memory_<key>.md` (audit) and
   `.agent_statelog_<key>.json`. The compress plugin also tracks the
   total model-response count (`TurnCount`) in the statelog, which the
   curator pre-flight gate reads.

2. The curator is triggered by one of two events:

   - **`EventSessionEnd`** — fired when the TUI or console launcher exits. The
     hook ([agent/curator_hook.go](agent/curator_hook.go)) spawns the curator in
     a goroutine bounded by a 2-minute timeout.
   - **`EventCurateNow`** — fired immediately. Three paths produce this event:
     - `/learn-now` command (TUI) or `POST /sessions/:id/curate` with `immediate: true`
       (Web UI). The `curate_session` tool flags a session for curation on
       `EventSessionEnd`; only `/learn-now` fires it immediately.
     - The **idle harvester** ([server/idle_curator.go](../server/idle_curator.go)),
       which runs every `checkInterval` and emits `EventCurateNow` for sessions idle
       longer than `YOKE_CURATOR_IDLE_TIMEOUT`. This is the primary auto-curation path
       for the Web UI, where `EventSessionEnd` is never fired. Before emitting, the
       harvester marks the session as **Harvested** — a persistent flag stored in the
       conversation file — so the session is completely removed from future scans until
       new user activity clears the flag.

3. **Pre-flight gate** — before the curator LLM is invoked, a quick check decides
   whether the session is worth processing:
   - **Forced** (`/learn` or `/learn-now`) — bypass gate, always run.
   - **Unforced** — run only if `TurnCount ≥ YOKE_CURATOR_MIN_TURNS` (default 3)
     **and** either at least one decision was recorded in the StateLog **or** total
     sub-agent invocations `≥ YOKE_CURATOR_MIN_SUB_AGENT_CALLS` (default 2).

   Sessions that do not pass are silently skipped without calling the LLM. The
   **Harvested** flag is set *before* the gate check, so even skipped sessions are
   not re-evaluated until new activity arrives.

4. The curator reads both files plus the existing `softskills/INDEX.md`
   and the authored skill list, then decides per existing soft-skill:
   - **skip** — nothing reusable or already covered; no files touched.
   - **create** — call `softskill_create(name, content)` + `softskill_index_append(...)`.
   - **update** — call `softskill_update(name, content, reason)` + `softskill_index_append(...)`.
   - **delete** — call `softskill_delete(name, reason)` + `softskill_index_remove(name)`.
     Used for obsolete or harmful skills. Prefer skip over delete when in doubt.

   The decision is gated by concrete thresholds rather than the curator's
   intuition. See the [post-session reflection pipeline](#post-session-reflection-pipeline)
   below for the inputs (reflector outcome, `key_insight`, per-skill
   `_stats.json` counters) and the rules wired into `CuratorPrompt`.

5. The next session's lead sees the updated soft-skill library via `list_softskills`
   and may load entries on demand.

## Post-session reflection pipeline

The curator does not judge the session by itself. Two upstream
reflectors prepare the inputs:

### Heuristic reflector (always runs)

[internal/softskills/reflector_heuristic.go](../internal/softskills/reflector_heuristic.go)
is a deterministic, no-LLM tagger that runs in-process at every
`EventSessionEnd`. It consumes:

- the compressed StateLog (`open_issues`, `decisions`, `tools`),
- the last user messages from the conversation file,
- the tool errors observed during the session,
- any explicit `wrap-session` feedback recorded by the leader,

and emits a verdict (`positive` / `negative` / `ambiguous` / `unknown`)
plus one tag per loaded soft-skill. Tags are written to
[`softskills/_stats.json`](#softskillsstatsjson) — a sidecar tracking
`loaded_count` + `helpful` / `harmful` / `neutral` per skill key.

### LLM reflector (optional)

`registry/agents/reflector/agent.json` defines a separate analyst agent
(default model: `balanced`). When present and enabled, the curator hook
runs it before the curator with a 60-second timeout. The reflector
returns a strict JSON envelope:

```json
{
  "reasoning":   "...",
  "success":     "positive" | "negative" | "ambiguous",
  "key_insight": "single-sentence generalisable lesson, or empty",
  "bullet_tags": [
    {"key": "<agent>/<name>", "tag": "helpful|harmful|neutral", "reason": "..."}
  ]
}
```

Tags returned by the LLM are merged onto the heuristic's — the LLM
wins on overlap. Conflicting tags are reconciled by `Stats.Retag`
(decrement the heuristic tag, increment the LLM tag) so each session
contributes at most one tag per skill.

### `softskills/_stats.json`

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

Keyed by `<agent>/<name>` for sub-agent skills, bare `<name>` for the
leader. Written with file-level locking so multiple yoke processes on
the same host never corrupt the counters.

### Per-sub-agent micro-reflection

`agent/subagent_hook.go` buffers all sub-agent invocations seen during
one leader run (keyed by `run_id`), then at `EventRunEnd` tags each
invocation:

- **retry** (same sub-agent appears later in the same run) → harmful.
- **`Error:` prefix or empty output** → harmful.
- **leader's assistant text cites the sub-agent approvingly**
  (`per <agent>`, `<agent> reported`, `<agent>'s findings`) → helpful.
- **leader retries / dismisses** ("let me ask again", "that failed",
  "let's try again") → harmful.
- otherwise → neutral.

This runs on every user turn — not just at session end — so streaming
feedback accrues over the lifetime of the session.

### Curator gating rules

`CuratorPrompt` enforces these explicit rules so the create / update /
delete decision is auditable:

- **create** only when `success == positive` AND `key_insight` is
  non-empty AND no near-duplicate exists in `run_glob softskills/*/SKILL.md`.
- **update** only when the `key_insight` cleanly extends an existing
  skill (the `softskill_update` `reason` argument must be ≥20 chars
  citing the improvement).
- **delete** only when `(stats.harmful >= 3 && stats.harmful > stats.helpful)`
  OR the reflector tagged the skill `harmful` this session with a
  reason mentioning "wrong assumptions" or "superseded".
- **skip** is the default; the curator falls through to it whenever
  none of the rules permits action.

### Explicit feedback (wrap-session)

On interactive surfaces the leader is instructed to load the
**wrap-session** soft-skill once per session when the user's work is
complete. The skill asks one closing question ("Anything off, or are
we good to wrap?") and persists the answer via the
`record_session_feedback` tool to
`$YOKE_HOME/logs/agent_feedback_<key>.json`. Both reflectors treat the
answer as the dominant verdict signal (positive keywords drive a
positive verdict; negative keywords drive a negative verdict).

`softskills/wrap-session/SKILL.md` ships in the repo as a deletable
built-in: delete the directory to disable the wrap-up question
globally.

### The Harvested flag

Each session carries a `Harvested` boolean that controls whether the idle
harvester will revisit it:

| Event | Effect on `Harvested` |
|---|---|
| Idle harvester fires (session is eligible) | Set to `true` — session skipped on all future scans |
| New user message (`Touch`) | Cleared to `false` in memory; disk cleared on the next conversation turn append |
| Server restart | Restored from `logs/conversation_<id>.json` (persistent) |

Long-idle sessions are therefore visited at most once per idle period, regardless
of how often the harvester runs or how many server restarts occur.

### Manual curation

Run the curator on demand against an existing session's files:

```bash
yoke curate --user alice --session 2025-01-15-deploy
# or
yoke curate --audit .agent_memory_alice_2025...md \
                     --statelog .agent_statelog_alice_2025...json
```

### Why the lead does not write

Two reasons:

1. **Quality**: the lead is busy solving the user's task; lifting "what
   did we learn" into a clean skill is itself a non-trivial reasoning
   step that benefits from a fresh context window.
2. **Safety**: the write-side tools (`softskill_create`, `softskill_update`,
   `softskill_index_append`) live on the curator only, and
   `permissions.json` denies generic `write` / `bash` paths under
   `softskills/`. The library cannot be corrupted by a confused lead.

### Memory of loaded soft-skills within a session

The harness does **not** record which soft-skills the lead loaded —
once `load_softskill` returns, the content lives in the conversation
window and the model carries it forward naturally. If the session is
resumed, the compressor's audit log preserves the load call so context
restoration is automatic.

