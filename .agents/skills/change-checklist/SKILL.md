---
name: change-checklist
description: Pre-flight and post-flight checklist for any code or configuration change in the yoke repository. Use before declaring a task complete and before committing. Mention triggers - finished, ready to commit, done, verify, sanity check, definition of done.
---

# Change checklist

A change is **not done** until every item below is true. Walk the list
in order; do not skip.

## Pre-flight (before writing code)

- [ ] **Does this change add a domain?** If yes → it belongs in
      `skills/<name>/SKILL.md`, `config/mcp_config.json`, or
      `config/permissions.json`, **not** in Go code. Re-check
      [project-overview](../project-overview/SKILL.md).
- [ ] **Does an existing tool/MCP server already cover the capability?**
      If yes, prefer a skill over new Go code.
- [ ] **Is the smallest possible change clear?** Don't refactor
      adjacent code, don't add docstrings to untouched functions, don't
      "improve" things unrelated to the request.

## During the change

- [ ] Every new agent is constructed via `agentkit.New(...)` (not
      `llmagent.New` directly).
- [ ] Every new sub-agent has a **role-based** instruction, not a
      domain-based one.
- [ ] Every mutating tool has a matching pattern in
      `config/permissions.json`.
- [ ] No third-party LLM SDK was added to `go.mod`.
- [ ] No domain keyword (`kubectl`, `psql`, `aws`, `docker`, …) appears
      in `core/agentkit/agentkit.go` or in any sub-agent's
      `Instruction:` string literal.

## Post-flight (must pass before declaring done)

```bash
PATH=$HOME/.local/go/bin:$PATH go build ./... && \
PATH=$HOME/.local/go/bin:$PATH go vet ./... && echo OK
```

- [ ] Final line is `OK`.
- [ ] If you touched `main.go (root)`: `go run . console`
      starts and the agent enumerates the new tool/skill on demand.
- [ ] If you touched `core/llm/`: pick one provider, set its env var,
      and confirm `BeforeModel` appears in `.agent_events.log`.
- [ ] If you touched `config/permissions.json`: at least one denied
      mutation is rejected and at least one allowed read still works.
- [ ] If you added a `SKILL.md`: the `description` includes the trigger
      keywords a user is likely to type.

## Documentation sync

If the change is **user-visible** (new env var, new skill, new
provider, new launcher mode), update:

- [ ] `README.md` (the relevant table or section).
- [ ] The matching file under `docs/`.
- [ ] If it changes how the dev agent should work, the matching skill
      under `.agents/`.

(Do not create new `docs/` files unless explicitly requested. Do not
create commit/CHANGELOG markdown unless asked.)

## Things to never do

- ❌ Pass `--no-verify`, force-push, or amend a published commit.
- ❌ `rm -rf` a directory that may contain in-progress work.
- ❌ Bypass the permissions plugin "just for testing".
- ❌ Declare the task done while `go vet` reports anything.
- ❌ Restate the change in chat as if it were code; show the file diffs
      instead.
