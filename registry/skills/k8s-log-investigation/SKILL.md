---
name: k8s-log-investigation
description: Investigate Kubernetes pod logs efficiently when logs are large. Use whenever the user mentions kubernetes, k8s, pod logs, errors, warnings, exceptions, crash loops, or asks to diagnose logs without dumping everything.
metadata:
  author: blouargant@chapsvision.com
  tags: "kubernetes, logs, investigation, playbook"
---

# Kubernetes Log Investigation

Use this skill to triage pod logs with a token-efficient workflow:
find anchor log lines that match high-signal problem patterns first, then
expand around those anchors, and only
fetch full logs as a last resort.

## When to use it

- The user asks to investigate Kubernetes logs.
- Logs may be very large and expensive to retrieve in full.
- The goal is to find actionable errors quickly.

## Procedure

### Phase 1: Scope and pod selection

1. **Confirm scope first.** Identify cluster context, namespace, workload,
   and time window. Ask for missing values before pulling logs.
2. **Pick target pods.** Prefer the newest unhealthy pod(s) from the
   workload selector. If multiple replicas fail, sample one or two first.
3. **Start with a narrow log window.** Pull only a recent slice (for
   example `--tail=200` and optionally `--since=10m`).

### Phase 2: Anchor search

4. **Search for anchors, do not dump all logs.** Treat anchors as log lines
   matching high-signal problem patterns (case-insensitive), for example:
   - `error|warn|warning|fatal|panic|exception|traceback`
   - `failed|failure|timeout|timed out|refused|unavailable`
   - `oomkilled|backoff|crashloop|segfault`

### Phase 3: Anchor expansion and lifecycle correlation

5. **Expand around anchors with small context.** For each interesting
   match, expand with `grep -A 5 -B 5` (or equivalent). Keep the first
   expansion small.
6. **Iterate context size gradually.** If needed, increase to `-A 20 -B 20`,
   then `-A 50 -B 50` for the same anchor. Do not jump to full logs early.
7. **Correlate with container lifecycle.** If restart/crash behavior is
   present, check previous container logs (`--previous`) and compare with
   current logs.

### Phase 4: If anchors are missing

8. **Widen carefully and retry anchor search.** Increase tail/since window
   and repeat Phase 2 before considering full logs.
9. **Last resort only: retrieve complete logs.** Pull full logs only when
   anchor search and progressive widening fail to produce clues.

### Phase 5: Report

10. **Report findings succinctly.** Provide top anchors, short surrounding
    excerpts, likely root cause category, and the smallest next action.

## Suggested command patterns

Use `Bash` with `kubectl` and shell pipes, or equivalent MCP read-only log
calls, while preserving the same strategy.

```bash
# Current logs, narrow window
kubectl logs -n <ns> <pod> --tail=200 --since=10m

# Anchor search
kubectl logs -n <ns> <pod> --tail=2000 --since=30m | \
  grep -Ei 'error|warn|warning|fatal|panic|exception|failed|timeout|refused|oomkilled|crashloop'

# Small contextual expansion around a specific anchor term
kubectl logs -n <ns> <pod> --tail=4000 --since=1h | \
  grep -Ein -A 5 -B 5 'timeout|connection refused|panic'

# Previous container logs for crash loops
kubectl logs -n <ns> <pod> --previous --tail=500
```

## Hard rules

- Never fetch full logs first when the stream can be sampled and searched.
- Never run destructive Kubernetes commands during log investigation.
- Never include secrets/tokens found in logs in the final response; redact.
- If access is denied, report the permission boundary instead of retrying
  with guessed credentials.

## Output rule

Always finish with a single line: `Result: ok | needs-attention | blocked`.
