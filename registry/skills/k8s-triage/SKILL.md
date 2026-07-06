---
name: k8s-triage
description: Diagnose an unhealthy Kubernetes workload — pods crash-looping, deployments not ready, services not reachable. The triage decision playbook — confirm context, get evidence, classify the failure, propose one safe next action. Use whenever the user mentions kubernetes, k8s, kubectl, pods, deployments, namespaces, or attaches a kubectl error.
metadata:
  author: blouargant@chapsvision.com
  tags: "kubernetes, triage, diagnosis, coordination, playbook"
---

# Kubernetes Triage

This skill is the *decision playbook* for any K8s incident: confirm the
context, get the evidence, classify the failure, and propose ONE safe next
action. It is about **deciding**, not about the mechanics of gathering
evidence — those live in the `k8s-investigation` skill.

- **In a squad** you coordinate: delegate the read-only evidence gathering to
  the **k8s_investigator** (which follows `k8s-investigation`), classify from
  the brief it returns, and delegate any change to the **k8s_editor** and any
  leftover sweep to the **k8s_cleaner**.
- **Standalone** (single agent, no squad) you do it yourself: load
  `k8s-investigation` for the read-only snapshot mechanics and run them, then
  come back here to classify and decide.

## Procedure

1. **Confirm the cluster context.** Run `kubectl config current-context`
   (or the MCP equivalent) and quote it back to the user before doing
   anything else.
2. **Locate the workload.** Ask for namespace + name if not provided.
3. **Gather evidence (read-only).** Get a compact, cited snapshot of the
   workload's state — deployment/pod status, the most recent unhealthy pod's
   `describe`, its previous-container logs, and recent namespace events. In a
   squad, delegate this to the **k8s_investigator** and name the
   `k8s-investigation` skill (add `k8s-log-investigation` when the failure is
   log-heavy). Standalone, load `k8s-investigation` and run its snapshot
   yourself. Never conclude from assumptions when a `kubectl` read can confirm.
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
   Delegate the change to the **k8s_editor** (which follows `k8s-modification`)
   and any ephemeral-leftover sweep to the **k8s_cleaner** (`k8s-cleanup`).

## Hard rules

1. **Priority 1 (safety).** Never `kubectl delete` without explicit user
   confirmation.
2. **Priority 2 (production guardrails).** Never modify production
   namespaces (`prod`, `prd`, `production`, or any context containing
   `prod`) without an explicit user override.
3. **Priority 3 (access boundaries).** If RBAC denies a read, escalate —
   do not retry with a different account.
