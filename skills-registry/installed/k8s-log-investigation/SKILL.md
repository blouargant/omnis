---
name: k8s-log-investigation
description: Investigate Kubernetes pod logs efficiently when logs are large. Use whenever the user mentions kubernetes, k8s, pod logs, errors, warnings, exceptions, crash loops, or asks to diagnose logs without dumping everything.
metadata:
  author: blouargant@chapsvision.com
  tags: [ "kubernetes", "logs", "investigation", "playbook" ]
---

# Kubernetes Log Investigation

Use this skill to triage pod logs with a token-efficient workflow:
find suspicious anchors first, then expand around those anchors, and only
fetch full logs as a last resort.

## When to use it

- The user asks to investigate Kubernetes logs.
- Logs may be very large and expensive to retrieve in full.
- The goal is to find actionable errors quickly.

## Procedure

1. **Confirm scope first.** Identify cluster context, namespace, workload,
   and time window. Ask for missing values before pulling logs.
2. **Pick target pods.** Prefer the newest unhealthy pod(s) from the
   workload selector. If multiple replicas fail, sample one or two first.
3. **Start with a narrow log window.** Pull only a recent slice (for
   example `--tail=200` and optionally `--since=10m`).
4. **Search for anchors, do not dump all logs.** Scan for likely problem
   patterns using case-insensitive matching, for example:
   - `error|warn|warning|fatal|panic|exception|traceback`
   - `failed|failure|timeout|timed out|refused|unavailable`
   - `oomkilled|backoff|crashloop|segfault`
5. **Expand around anchors with small context.** For each interesting
   match, expand with `grep -A 5 -B 5` (or equivalent). Keep the first
   expansion small.
6. **Iterate context size gradually.** If needed, increase to `-A 20 -B 20`,
   then `-A 50 -B 50` for the same anchor. Do not jump to full logs early.
7. **Correlate with container lifecycle.** If restart/crash behavior is
   present, check previous container logs (`--previous`) and compare with
   current logs.
8. **If no anchors are found, widen carefully.** Increase tail/since window
   and retry anchor search before considering full logs.
9. **Last resort only: retrieve complete logs.** Pull full logs only when
   anchor search and progressive widening fail to produce clues.
10. **Report findings succinctly.** Provide top anchors, short surrounding
    excerpts, likely root cause category, and the smallest next action.

## Suggested command patterns

Use `bash` with `kubectl` and shell pipes, or equivalent MCP read-only log
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
