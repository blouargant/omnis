---
name: k8s-investigation
description: Gather read-only evidence about an unhealthy Kubernetes workload — the snapshot mechanics (get/describe/logs/events) that back a triage. Use whenever you need to collect cited kubectl evidence for a pod/deployment/service before diagnosing it, without mutating the cluster.
metadata:
  author: blouargant@chapsvision.com
  tags: "kubernetes, investigation, evidence, kubectl, read-only, playbook"
---

# Kubernetes Investigation

This skill is the *read-only evidence-gathering* procedure that backs a
triage. It is the mechanics half of a K8s incident: how to snapshot a
workload's state efficiently and report a compact, cited brief. The
**decision** half — classifying the failure and proposing a fix — lives in
the `k8s-triage` skill. This skill **never mutates the cluster**.

It assumes either:

- a `kubectl` binary is reachable through the `Bash` tool, **or**
- a Kubernetes MCP server is mounted (preferred — it gives structured
  output and respects permissions).

If neither `kubectl` nor Kubernetes MCP is available, stop command-based
investigation and ask the user for manual evidence (for example:
`kubectl get` output, `describe pod`, recent events, and the last 200 log
lines).

## Procedure

1. **Confirm the cluster context.** Run `kubectl config current-context`
   (or the MCP equivalent) and quote it back before gathering anything.
2. **Locate the workload.** Namespace + name + selector. Ask if unknown.
3. **Snapshot the state.** In one batch of read-only calls:
   - `kubectl get deploy/sts/ds <name> -n <ns> -o wide`
   - `kubectl get pods -n <ns> -l <selector> -o wide`
   - `kubectl describe pod <pod>` (the most recent unhealthy one)
   - `kubectl logs <pod> --previous --tail=200`
   - relevant `kubectl get events -n <ns> --sort-by=.lastTimestamp | tail`

   Bound output: use `-o jsonpath`/`grep`, `--since`/`--tail` and label
   selectors rather than dumping whole streams.
4. **Report a compact evidence brief, not a raw dump:** findings, the exact
   commands run and the decisive output excerpts (pod phase, restart count,
   container state/reason, last event, probe failures, image-pull errors,
   OOMKilled, …), your confidence, and any open questions. Quote only the
   lines that matter. Do not classify or propose a fix — that is the
   triage/leader step.

## When logs get large

If the investigation becomes log-heavy (log output exceeds about 5,000 lines
or ~5 MB, no clear anchors after scanning at least 2,000 recent lines, or the
same pod restarts 3+ times within 30 minutes), load `k8s-log-investigation`
and follow its token-efficient anchor-first workflow.

## Read boundary

1. **Never mutate.** No `apply`, `delete`, `scale`, `edit`, `patch`,
   `rollout`, `cordon`, `drain`, `replace`, or write-`exec`. If a fix needs a
   mutation, list it under "recommended actions" for the caller to decide —
   do not execute it.
2. **Access boundaries.** If RBAC denies a read, escalate — do not retry with
   a different account.
