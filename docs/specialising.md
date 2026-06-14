# Specialising the agent

The harness is generic on purpose. **You never edit Go code to change
its domain.** Specialisation is always one of three things, in
combination:

1. A new **skill** under `skills/<name>/SKILL.md`.
2. A new **MCP server** in `mcp_config.json`.
3. New **permission rules** in `permissions.json`.

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

**Option A — MCP server.** Edit `mcp_config.json`:

```json
{
  "servers": {
    "kubernetes": {
      "command": "npx",
      "args": ["-y", "mcp-server-kubernetes"],
      "env": {"KUBECONFIG": "/home/you/.kube/config"}
    }
  }
}
```

**Option B — Bash + permissions.** Just rely on the built-in `bash`
tool plus an entry in `permissions.json` (Claude Code nomenclature):

```json
{
  "permissions": {
    "allow": [
      "Bash(kubectl get *)",
      "Bash(kubectl describe *)",
      "Bash(kubectl logs *)",
      "Bash(kubectl top *)",
      "Bash(kubectl explain *)"
    ],
    "ask": [
      {"regex": "\\bkubectl\\s+(apply|delete|patch|edit|scale|rollout|drain|cordon)\\b", "tools": ["Bash"], "reason": "cluster mutation"}
    ]
  }
}
```

Read-only verbs auto-allowed; mutating verbs gated; destructive ones
hard-denied.

### Step 4 — Run

```bash
go run . console
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
permission rules above), the same root binary becomes a K8s
diagnostician with no Go change.

## Worked example: PostgreSQL DBA

```json
// mcp_config.json
{
  "servers": {
    "postgres": {
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-postgres",
        "postgresql://reader:pw@localhost/app?sslmode=require"
      ]
    }
  }
}
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

```json
{
  "servers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {"GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_…"}
    }
  }
}
```

Add `skills/issue-triage/SKILL.md` describing your label taxonomy and
escalation rules. That's it.

## When you *do* need code

You only need to write Go when you want a brand-new **tool** or a
brand-new **sub-agent**. See [extending.md](extending.md). Even then,
keep the tool generic — the policy that turns it into a domain skill
belongs in `skills/`.

## Multiple specialisations in one binary — squads

When a single agent.json needs to support several different *kinds* of
session (e.g. an interactive triage helper and a focused web-research
flow), don't fork the binary. Declare each as a **squad** in
`agents.json`: a named group with its own leader and member
sub-agents picked from the shared `agents:` catalogue.

Users don't have to know which squad to pick: by default every new chat
starts on the **Omnis router** (a leaderless `omnis` squad), which reads
the request and **routes it to the best squad automatically**, handing
over control. Give each squad a clear `description` — the router matches
the request against those descriptions when choosing. A picker next to
**New Chat** still lets a user pin a specific squad (bypassing the router),
and the recorded squad survives reloads and server restarts. Set
`router_squad: "none"` to disable routing if you'd rather users always
pick. See
[configuration.md#omnis-router-default-chat-routing](configuration.md#omnis-router-default-chat-routing),
[configuration.md#squads--per-session-agent-groups](configuration.md#squads--per-session-agent-groups)
and [extending.md#add-a-squad](extending.md#add-a-squad).

## Anti-patterns

- ❌ Hard-coding domain knowledge into a system prompt or sub-agent
  instruction.
- ❌ One sub-agent per domain. Use one generic sub-agent + many skills.
- ❌ Mutating tools without a `permissions.json` rule.
- ❌ Mounting an MCP server without restricting its surface in
  `permissions.json`.
- ❌ Forking the binary to ship a "research-only" variant. Declare a
  `research` squad in `agent.json` instead and let users pick it from
  the new-chat dropdown.
