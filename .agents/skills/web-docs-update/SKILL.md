---
name: web-docs-update
description: Audit and update the web UI documentation (web/docs/*.md) after changes to the web UI source files (web/app.js, web/settings.js, web/index.html, server/*.go). Use when the user modified the web UI and wants the docs kept in sync, or when a doc review reveals stale content.
---

# Web Documentation Update

Keeps `web/docs/` in sync with the actual web UI after source changes.

## When to use it

- Web UI source files changed (`web/app.js`, `web/settings.js`, `web/index.html`, `web/css/`, `server/*.go`) and docs may be stale.
- A doc review surfaced inaccuracies that need fixing.
- A new UI feature was added with no matching doc update.

Do NOT use this skill to make content improvements unrelated to accuracy (e.g. rewriting prose style, adding new conceptual pages).

## Source → Doc mapping

| Changed source | Docs to check |
|---|---|
| `web/settings.js` → `MENU_ITEMS` | `04-settings-panel.md` Sections table — labels, backing files, purpose |
| `web/settings.js` → `FILES` | `04-settings-panel.md` — JSON-backed sections and their `id`s |
| `web/settings.js` → `DOC_PAGES` | `04-settings-panel.md` — docs listed must match the TOC entries |
| `web/settings.js` → `THEMES` | `05-themes.md` — principal/secondary tier lists |
| `web/css/*` | `02-composer.md`, `03-sessions.md`, `05-themes.md` — update visual behavior descriptions when UI styling/interaction states change |
| `web/app.js` composer area | `02-composer.md` — send/stop/attach/slash/context ring features |
| `web/app.js` session sidebar | `03-sessions.md` — session lifecycle, squad picker, hot reload |
| `web/app.js` debug overlay | `16-env-vars.md` Web UI debug section |
| `server/config.go` → `ServerConfig` | `16-env-vars.md` Server section; `14-config.md` |
| `server/user_commands.go` | `04-settings-panel.md` Commands row |
| `server/main.go` env var comments | `16-env-vars.md` — every `OMNIS_*` var must appear |
| `internal/paths/paths.go` | `11-skills.md` skill paths; `14-config.md` write root tree |
| `registry/agents/<name>/agent.json` builtin flag | `04-settings-panel.md` built-in agents list |

## Procedure

### 1. Identify scope

Use one of these commands:

```bash
# Most recent commit
git diff --name-only HEAD~1

# Feature branch comparison
git diff --name-only main...HEAD
```

If the user provides a specific commit or range, use that exact range instead. Keep only these paths from the diff: `web/app.js`, `web/settings.js`, `web/index.html`, `web/css/*`, `server/*.go`, `internal/paths/paths.go`, and `registry/agents/*/agent.json`. Map each changed file to the doc(s) in the table above. Skip this step when the user already listed the changed files.

### 2. Read the changed source

For each relevant source file, read only the sections that drive the docs:

- `web/settings.js`: `MENU_ITEMS`, `FILES`, `DOC_PAGES`, `THEMES` arrays (top ~100 lines).
- `web/app.js`: grep for `BUILTIN_SLASH_COMMANDS`, `AgentDebug`, `localStorage`, `?debug`.
- `server/config.go`: `ServerConfig` struct fields and their comments.
- `server/main.go`: the env-var comment block at the top.
- `internal/paths/paths.go`: `SkillsRegistryDir`, `AgentsRegistryDir`, write-dir functions.

### 3. Read the candidate doc files

Read only the docs identified in step 1. Focus on:

- Tables that list UI sections, env vars, or file paths — these go stale fastest.
- Any `Settings → X` navigation reference — the label `X` must match the sidebar label in `MENU_ITEMS`.
- Filesystem trees in `14-config.md` write root.
- Skill lookup paths in `11-skills.md`.

### 4. Diff source vs. doc

For each doc, list every discrepancy found:

```
FILE: web/docs/04-settings-panel.md
  - Sections table missing row for "A2A" (present in MENU_ITEMS, id="a2a")
  - Section label "Agent" should be "Agents" (MENU_ITEMS label="Agents")
```

Write the discrepancy list first, then continue directly with edits unless the user explicitly asks for a review-only pass. If there are no discrepancies, stop here and report `Result: ok — docs already up to date`.

### 5. Apply fixes

For each discrepancy, make the minimal edit using the `Edit` tool. Prefer targeted replacements over full-file rewrites. Do not rewrite surrounding prose.

### 6. Verify cross-references

After edits, grep for remaining stale references:

```bash
grep -rn "Settings → Agent\b" web/docs/
grep -rn "\$OMNIS_HOME/skills/" web/docs/
```

Fix any that surfaced.

## Hard rules

- Never edit a doc without first reading the relevant source to confirm the fix is correct.
- Never rewrite correct content — scope changes strictly to inaccuracies.
- Never add new doc pages; only update existing ones.
- Do not change the source files (web/*.js, server/*.go) — this skill is doc-only.

## Output rule

End with `Result: ok — N files updated` (list each file) or `Result: ok — docs already up to date`.
