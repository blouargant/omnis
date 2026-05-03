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
- **Encode safety inline.** Don't rely on `permissions.yaml` alone;
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
| Trigger to write    | `git commit`                     | `EventSessionEnd` or `curate_session` |
| Format              | `<name>/SKILL.md` w/ frontmatter | Same                                  |
| Loaded by lead via  | `list_skills` / `load_skill`     | `list_softskills` / `load_softskill`  |
| Mutated by lead     | No                               | No (write tools mounted on curator only) |
| Permissions         | n/a                              | `softskills/` writes denied to lead in `config/permissions.yaml` |

### Lifecycle

1. The lead works through the user's request as usual; `compress`
   writes per-session `.agent_memory_<key>.md` (audit) and
   `.agent_statelog_<key>.json`.
2. When the session ends, the EventSessionEnd hook
   ([agent/curator_hook.go](agent/curator_hook.go)) spawns the curator
   in a goroutine bounded by a 2-minute timeout. The user can also call
   the `curate_session` tool mid-session to *flag* the session as worth
   curating.
3. The curator reads both files plus the existing `softskills/INDEX.md`
   and the authored skill list, then decides:
   - **skip** — nothing reusable; no files touched.
   - **create** — call `softskill_create(name, content)`.
   - **update** — call `softskill_update(name, content, reason)`.
   - **append-index** — call `softskill_index_append(category, name, summary)`.
4. The next session's lead sees the new soft-skill via `list_softskills`
   and may load it on demand.

### Manual curation

Run the curator on demand against an existing session's files:

```bash
agent-toolkit curate --user alice --session 2025-01-15-deploy
# or
agent-toolkit curate --audit .agent_memory_alice_2025...md \
                     --statelog .agent_statelog_alice_2025...json
```

### Why the lead does not write

Two reasons:

1. **Quality**: the lead is busy solving the user's task; lifting "what
   did we learn" into a clean skill is itself a non-trivial reasoning
   step that benefits from a fresh context window.
2. **Safety**: the write-side tools (`softskill_create`, `softskill_update`,
   `softskill_index_append`) live on the curator only, and
   `config/permissions.yaml` denies generic `write` / `bash` paths under
   `softskills/`. The library cannot be corrupted by a confused lead.

### Memory of loaded soft-skills within a session

The harness does **not** record which soft-skills the lead loaded —
once `load_softskill` returns, the content lives in the conversation
window and the model carries it forward naturally. If the session is
resumed, the compressor's audit log preserves the load call so context
restoration is automatic.

