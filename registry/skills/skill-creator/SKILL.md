---
name: skill-creator
description: Create or update a skill playbook (SKILL.md) for the agent. Use when the user wants to create a new skill, add a skill, define a new agent capability, update an existing skill, edit a skill, refine a skill playbook, or specialise the agent for a new domain.
---

# Skill Creator

Guides the user through authoring a new SKILL.md or updating an existing one,
then writes it to the skills registry.

## When to use it

- **Create**: user wants a brand-new skill the agent does not have yet
- **Update**: user wants to revise or improve an existing skill

Do NOT use this skill when the user just wants to *run* an existing skill.

## Procedure

### A — Create a new skill

1. **Confirm the name** — verify it matches `^[a-z0-9][a-z0-9._-]{0,63}$`.
   If the user provided a name that doesn't fit, suggest a corrected form.

2. **Gather requirements** — ask the user (in one message, not one-by-one):
   - What domain or task should this skill cover?
   - What trigger words should appear in the description (the keywords the
     user will type to select this skill)?
   - Which agent tools will the procedure use? (`Bash`, `Read`, `Write`,
     an MCP server, …)
   - What must the agent **never** do without explicit confirmation?
   - What output token closes a run? (e.g. `ok`, `clean`, `needs-attention`)

3. **Draft the SKILL.md** following the template in the *Hard rules* section.
   Present the full draft to the user.

4. **Iterate** — incorporate feedback until the user approves the draft.

5. **Write** — save the file using the `Write` tool:
   - Run `Bash` → `mkdir -p "$HOME/.omnis/registry/skills/<name>"` first.
   - Write to `$HOME/.omnis/registry/skills/<name>/SKILL.md`.

6. **Confirm** — tell the user the skill is saved and that they can link it
   to an agent via Settings → Skills in the web UI (or hot-reload picks it up
   if already linked).

### B — Update an existing skill

1. **Find the skill** — call `list_skills`. If the target skill appears,
   call `load_skill` with its name to read the current content.
   If it is not in `list_skills`, locate it via:
   ```
   Bash: find "$HOME/.omnis/registry/skills" -name SKILL.md
   ```

2. **Show** — display the current content to the user.

3. **Gather changes** — ask what to add, remove, or rewrite.

4. **Draft** the updated version and show the diff or full new content.

5. **Confirm and write** — once the user approves, overwrite the file with
   the `Write` tool.

6. **Confirm** — inform the user. Hot-reload picks up the updated file
   automatically; no restart is needed.

## SKILL.md template

```markdown
---
name: <kebab-case-name>
description: <One sentence. Start with a verb. Include trigger keywords the
user is likely to type so the leader selects it automatically.>
---

# <Title>

## When to use it
<One paragraph disambiguating this skill from neighbouring ones.>

## Procedure
1. <Read-only confirmation step — always first.>
2. <Gather state in one batch of read-only calls.>
3. <Classify or analyse.>
4. <Propose ONE action — smallest reversible step first.>

## Hard rules
- Never <destructive verb> without explicit confirmation.
- Never retry on permission denial.

## Output rule
End every run with `Result: ok | needs-attention | blocked`.
```

## Hard rules

- Skill name must match `^[a-z0-9][a-z0-9._-]{0,63}$`; reject names that do not.
- Always show the full draft before writing any file.
- Never overwrite an existing file without user approval.
- Keep SKILL.md under 150 lines; push extra detail into `references/` files.
- Never embed credentials, hostnames, or environment variable values.

## Output rule

End with `Result: ok` once the file is written and confirmed, or
`Result: blocked` if the user cancelled.
