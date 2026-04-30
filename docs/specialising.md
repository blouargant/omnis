# Specialising the agent

The harness is generic on purpose. **You never edit Go code to change
its domain.** Specialisation is always one of three things, in
combination:

1. A new **skill** under `skills/<name>/SKILL.md`.
2. A new **MCP server** in `config/mcp_config.yaml`.
3. New **permission rules** in `config/permissions.yaml`.

That is the entire contract.

## Recipe

### Step 1 — Decide the domain in one sentence

> "Diagnose unhealthy Kubernetes workloads in a given namespace."

Write it down — it becomes the `description:` of the skill and the
trigger phrase the lead agent uses to load it.

### Step 2 — Write the skill

A skill is a Markdown file with YAML frontmatter. The lead reads the
frontmatter at startup; the body is loaded only when the skill is
selected (`load_skill`).

Minimum template (see [skills.md](skills.md) for the full guide):

```markdown
---
name: my-domain
description: Short, action-oriented sentence. Use whenever the user mentions <triggers>.
---

# My domain

## Procedure
1. Confirm scope (read-only).
2. Snapshot current state with one batch of read-only calls.
3. Classify the situation into one of N categories.
4. Propose ONE next action, smallest reversible step first.

## Hard rules
- Never <destructive thing> without explicit confirmation.
- Never retry on permission denial.
```

### Step 3 — Mount the tools the skill needs

Two options, freely combinable:

**Option A — MCP server.** Edit `config/mcp_config.yaml`:

```yaml
servers:
  - name: kubernetes
    command: npx
    args: ["-y", "mcp-server-kubernetes"]
    env:
      KUBECONFIG: /home/you/.kube/config
```

**Option B — Bash + permissions.** Just rely on the built-in `bash`
tool plus an entry in `config/permissions.yaml`:

```yaml
always_allow:
  - "^kubectl (get|describe|logs|top|explain) "

ask_user:
  - "^kubectl (apply|delete|patch|edit|scale|rollout|drain|cordon)"
```

Read-only verbs auto-allowed; mutating verbs gated; destructive ones
hard-denied.

### Step 4 — Run

```bash
go run ./cmd/full console
> diagnose why pods in namespace payments are crash-looping
```

The lead agent:
1. Restates the goal.
2. Lists available skills, finds the description matches your prompt.
3. Calls `load_skill("k8s-triage")` to get the procedure.
4. Follows the procedure using whatever tools (`bash` + `kubectl`, MCP
   server, or both) are mounted.
5. Reports back, asks before any mutation.

## Worked example: Kubernetes triage

The repository ships `skills/k8s-triage/SKILL.md` as a complete example.
With it in place plus a Kubernetes MCP server (or `kubectl` and the
permission rules above), the same `cmd/full` binary becomes a K8s
diagnostician with no Go change.

## Worked example: PostgreSQL DBA

```yaml
# config/mcp_config.yaml
servers:
  - name: postgres
    command: npx
    args:
      - -y
      - "@modelcontextprotocol/server-postgres"
      - "postgresql://reader:pw@localhost/app?sslmode=require"
```

```markdown
<!-- skills/dba-triage/SKILL.md -->
---
name: dba-triage
description: Diagnose Postgres performance / locking problems. Use when the user mentions slow queries, locks, deadlocks, vacuum, bloat, or attaches a pg_stat snapshot.
---

# Postgres triage

1. List active queries via the postgres MCP `query` tool:
   `select pid, state, wait_event, query_start, query from pg_stat_activity where state != 'idle' order by query_start;`
2. Inspect locks: `select * from pg_locks where not granted;`
3. Classify: slow plan / lock contention / IO / config.
4. Propose ONE remedy. Never run `ALTER`, `VACUUM FULL` or anything
   non-read without explicit user confirmation.

## Hard rules
- Read-only by default. Any write needs explicit consent.
- Never run `pg_terminate_backend` without naming the pid in the proposal.
```

Done — same binary, new specialist.

## Worked example: GitHub issue triager

```yaml
servers:
  - name: github
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "ghp_…"
```

Add `skills/issue-triage/SKILL.md` describing your label taxonomy and
escalation rules. That's it.

## When you *do* need code

You only need to write Go when you want a brand-new **tool** or a
brand-new **sub-agent**. See [extending.md](extending.md). Even then,
keep the tool generic — the policy that turns it into a domain skill
belongs in `skills/`.

## Anti-patterns

- ❌ Hard-coding domain knowledge into a system prompt or sub-agent
  instruction.
- ❌ One sub-agent per domain. Use one generic sub-agent + many skills.
- ❌ Mutating tools without a `permissions.yaml` rule.
- ❌ Mounting an MCP server without restricting its surface in
  `permissions.yaml`.
