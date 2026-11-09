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

> **Frontmatter is strict.** The skill loader parses the YAML block with
> known-fields checking and rejects anything outside `name`, `description`,
> `license`, `compatibility`, `metadata`, and `allowed-tools` (an unknown field
> fails the whole skill with `field <x> not found in type skill.Frontmatter`).
> Omnis-specific configuration therefore lives in **sidecar files** next to
> `SKILL.md`, never in the frontmatter: `permissions.json` (skill-scoped
> permission rules) and `requires.json` (tool dependencies — see below).

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

## Tool dependencies (`requires.json`)

A skill that shells out to an external CLI can declare it as a **runtime
dependency**, and the harness enforces installation *in code* — it checks the
binary is on `PATH`, asks the user to install it, runs the install, and
re-checks — instead of trusting the model to follow a "first check it's
installed" instruction (which it sometimes skips).

Declare dependencies in a **`requires.json`** sidecar next to `SKILL.md` (the
field cannot live in the frontmatter — see the strictness note above):

```json
{
  "requires": [
    { "command": "lit", "label": "LiteParse", "install": "pipx install liteparse" }
  ]
}
```

| Field     | Meaning                                                                                  |
|-----------|------------------------------------------------------------------------------------------|
| `command` | The binary that must resolve on `PATH` (`exec.LookPath`).                                 |
| `install` | The install command — a string, or a per-OS object keyed by `GOOS` (`{"linux": …, "darwin": …, "windows": …, "default": …}`). |
| `label`   | Optional friendly name shown in the install prompt (defaults to `command`).              |

> **Pick an install command that works in a clean environment.** For a Python
> CLI app prefer `pipx install <pkg>` over `pip install <pkg>`: `pip` into the
> system interpreter fails on PEP 668 / externally-managed Python (modern
> Debian/Ubuntu) with `error: externally-managed-environment`, whereas pipx
> isolates the app in its own venv and puts its entry-point on `PATH`. Use a
> per-OS `install` map for native packages (`brew` vs `apt`).

**When it fires.** The check runs when the skill is loaded (`load_skill`). For
each missing binary the user is asked, in the active session, to approve the
install; on approval the install runs through the **Bash safety floor** (so
catastrophic commands are still blocked even though the install was approved),
then the binary is re-checked.

**On decline or failure** the behaviour is *report unavailable, not block*: the
`load_skill` result carries a `dependency_status` notice back to the model, so
the skill's own documented fallback applies (e.g. `liteparse` falls back to the
`pdf`/`pdftotext` skill). Enforcement guarantees the install is never silently
skipped once a dependency-bearing skill is engaged — it does **not** change
which skill the lead picks in the first place (that stays prompt-driven).

> The same mechanism applies to **MCP servers** via a `requires` array on the
> server entry in `mcp_config.json`; there the check fires at the server's first
> connect (where a session exists to prompt). See
> [configuration.md](configuration.md).

Implementation: [internal/deps](../internal/deps) (the `Ensure` gate), the
`load_skill` wrapper in
[internal/skills/deps_gate.go](../internal/skills/deps_gate.go), wired from the
process-wide ask-user registry in
[agent/infrastructure.go](../agent/infrastructure.go).

## Examples shipped in the repo

| Skill            | What it demonstrates                                                  |
|------------------|-----------------------------------------------------------------------|
| `review`         | A truly generic review/audit playbook. Works on any artefact.         |
| `agent-builder`  | Meta-skill: how to scaffold a new specialist agent.                   |
| `liteparse`      | A tool-bound skill with a `requires.json` (the `lit` CLI, `pipx install liteparse`) and a `permissions.json` allowlist. |
| `pdf`            | The `pdftotext` fallback for `liteparse`; per-OS `requires.json` (brew/apt). |
| `k8s-triage`     | A domain specialisation example: the triage decision playbook (confirm context → classify failure → propose one safe fix). |
| `k8s-investigation` | Its read-only evidence-gathering companion (the `kubectl` snapshot mechanics behind a triage). |

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

## Skills vs. collection memory

Skills are authored playbooks: a human writes them and installs them. What the
agent learns from the user while working — corrections, preferences, decisions —
is not a skill: the agent saves it as **collection memory** (one small Markdown
file per fact, `memory_save` / `memory_read` / `memory_delete`), in the turn
where it learns it, and the host injects it into every later turn of that
collection. See
[Sessions → Collection memory](../web/docs/03-sessions.md#collection-memory).
