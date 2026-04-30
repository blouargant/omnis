---
name: k8s-triage
description: Diagnose an unhealthy Kubernetes workload — pods crash-looping, deployments not ready, services not reachable. Use whenever the user mentions kubernetes, k8s, kubectl, pods, deployments, namespaces, or attaches a kubectl error.
---

# Kubernetes Triage

This skill is the *playbook* the agent follows for any K8s incident. It
assumes either:

- a `kubectl` binary is reachable through the `bash` tool, **or**
- a Kubernetes MCP server is mounted (preferred — it gives structured
  output and respects permissions).

## Procedure

1. **Confirm the cluster context.** Run `kubectl config current-context`
   (or the MCP equivalent) and quote it back to the user before doing
   anything else.
2. **Locate the workload.** Ask for namespace + name if not provided.
3. **Snapshot the state.** In one batch of read-only calls:
   - `kubectl get deploy/sts/ds <name> -n <ns> -o wide`
   - `kubectl get pods -n <ns> -l <selector> -o wide`
   - `kubectl describe pod <pod>` (the most recent unhealthy one)
   - `kubectl logs <pod> --previous --tail=200`
   - relevant `kubectl get events -n <ns> --sort-by=.lastTimestamp | tail`
4. **Classify the failure** into one of:
   - image / pull
   - scheduling (resource, taint, affinity)
   - probe (liveness / readiness)
   - configuration (env, secret, configmap)
   - network (service, dns, network policy)
   - permission (RBAC, PSP / PSA)
   - application (crashes after startup)
5. **Propose ONE next action** — never a multi-step mitigation in the
   first message. Always a dry-run first when possible (`--dry-run=server`).

## Hard rules

- Never `kubectl delete` without explicit user confirmation.
- Never modify production namespaces (`prod`, `prd`, `production`, or any
  context containing `prod`) without an explicit user override.
- If RBAC denies a read, escalate — do not retry with a different
  account.
