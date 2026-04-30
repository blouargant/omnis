---
name: add-skill
description: Author a new playbook under skills/ to specialise the agent for a new domain or task without changing Go code. Use whenever the user wants the agent to do something new (k8s triage, sql review, release notes, ...). Mention triggers - new skill, SKILL.md, specialise the agent, add domain knowledge, playbook.
---

# Add a project skill

A **project skill** lives under `skills/<name>/SKILL.md` and is loaded
on demand by the agent at runtime. This is the primary specialisation
mechanism — prefer it over writing Go.

> Distinguish: `skills/` (loaded by the running agent) vs `.agents/`
> (loaded by *your* development session). This skill is about
> `skills/`.

## Format

Follow the [Agent Skills spec](https://agentskills.io/specification).
Required frontmatter: `name` and `description`.

```markdown
---
name: my-domain
description: One sentence. Begin with a verb. Include the trigger keywords explicitly so the lead picks it when the user mentions them.
---

# Title

## When to use it
Disambiguate from neighbouring skills.

## Procedure
1. Always start with a read-only confirmation step.
2. Snapshot state with one batch of read-only calls.
3. Classify the situation into N categories.
4. Propose ONE next action — smallest reversible step first.

## Hard rules
- Never <destructive verb> without explicit confirmation.
- Never retry on permission denial.

## Output rule
End every run with `Result: ok | needs-attention | blocked`.
```

## Authoring rules

1. **Description is a matchable trigger.** The lead matches the user's
   prompt against `description`. Write it like a search query: include
   the verbs and nouns the user is likely to type.

   ✅ "Diagnose unhealthy Kubernetes workloads — pods crash-looping,
       deployments not ready. Use whenever the user mentions
       kubernetes, k8s, kubectl, pods, deployments, namespaces."

   ❌ "Helps with Kubernetes."

2. **Procedure is a checklist, not a tutorial.** One verb per step.
   Cite the tool by name (`bash`, `kubernetes/get_pods`, `read`, …).

3. **Always include "Hard rules".** Spell out the destructive verbs the
   skill must never invoke without consent. The agent self-restricts
   before even reaching the permissions plugin.

4. **End with an output rule.** A single closing token (`ok`,
   `needs-changes`, `blocked`, `clean`, …) makes the result
   machine-readable.

5. **Stay under ~150 lines.** Beyond that, split into a second skill
   the lead can chain.

## Wiring

No code change required. The skill loader (`internal/skills`) walks
`./skills/` at startup and registers every folder with a valid
`SKILL.md`. The lead's system prompt instructs it to prefer
`load_skill` over ad-hoc reasoning.

## Examples shipped

Read these before authoring your own:

- [`skills/review/SKILL.md`](../../skills/review/SKILL.md) — generic,
  universal review playbook.
- [`skills/k8s-triage/SKILL.md`](../../skills/k8s-triage/SKILL.md) — full
  domain specialisation example.
- [`skills/agent-builder/SKILL.md`](../../skills/agent-builder/SKILL.md)
  — meta-skill (how to build a new specialist).
- [`skills/pdf/SKILL.md`](../../skills/pdf/SKILL.md) — narrow,
  tool-bound skill.

## Optional bundled assets

Following the spec, you can bundle:

- `skills/<name>/scripts/` — executable helpers the agent can run.
- `skills/<name>/references/` — additional Markdown loaded only on
  demand.
- `skills/<name>/assets/` — templates, schemas, lookup tables.

Keep `SKILL.md` slim; push detail into `references/`.

## Verify

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go run . console
> list available skills    # the new one should appear
> <a typical user prompt>  # the lead should call load_skill on it
```

Adjust the description if the wrong skill (or no skill) is selected.

## Don'ts

- ❌ Don't restate the universal operating method (restate → plan → …).
  The lead already does that.
- ❌ Don't hard-code paths or hostnames; pass them via tool args / env.
- ❌ Don't write a skill that needs a tool that isn't mounted. Either
  add the MCP server / tool first, or include the requirement in
  `compatibility:`.
