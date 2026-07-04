---
name: k8s-cleanup
description: Remove ephemeral Kubernetes diagnostic leftovers the squad created — debug pods, throwaway run pods, one-off jobs, temporary configmaps/secrets — found by the omnis.dev/ephemeral=true label (well-known debug-pod name patterns are surfaced as confirm-first suspects). Removes only ephemeral resources, never real workloads. Use to sweep leftovers at the end of an investigation or on request.
metadata:
  author: blouargant@chapsvision.com
  tags: "kubernetes, cleanup, hygiene, debug pods, ephemeral, playbook"
---

# Kubernetes Cleanup

The *playbook* for sweeping the transient diagnostic resources the squad
created and leaving every real workload untouched.

Every deletion is **permission-gated** — it prompts the user before it runs. Do
not try to work around the prompt. If a call is denied, report it and stop.

## The ephemeral-resource convention (shared across the squad)

Every transient resource any squad agent creates for diagnosis MUST carry:

- `omnis.dev/ephemeral=true` — the primary cleaner selector.
- `omnis.dev/created-by=<agent>` — provenance (e.g. `k8s_investigator`).

The cleaner treats these labels as the *only* automatic delete signal.

## Procedure

1. **Confirm scope.** `kubectl config current-context`; default to the
   namespace(s) under investigation. Sweep all namespaces (`-A`) only when
   explicitly asked, and never delete in a production namespace/context
   (`prod`, `prd`, `production`) without an explicit override.
2. **Discover labeled ephemerals** (removable):
   ```bash
   kubectl get pods,jobs,cm,secret,svc,deploy -n <ns> -l omnis.dev/ephemeral=true -o wide
   ```
3. **Discover UNLABELED suspects** (confirm-first — do NOT auto-delete):
   - `kubectl debug` leftovers: node-debugger pods (often `node-debugger-*` in
     `default`) and `--copy-to` copy pods.
   - throwaway `kubectl run` pods: common debug images (`netshoot`, `busybox`,
     `alpine`, `curl`, `tmp-shell`), or a `run=<debug-name>` label.
   - completed / failed one-off jobs and their pods.
   List each with age + owner so the leader/user can decide.
4. **Classify** each as labeled-ephemeral / unlabeled-suspect / legitimate.
5. **Remove** (each deletion prompts the user):
   - Labeled ephemerals — delete by label or name, e.g.
     `kubectl delete pod,job -n <ns> -l omnis.dev/ephemeral=true`.
   - Unlabeled suspects — do NOT delete; list them for the leader/user to
     confirm, then delete only the confirmed ones.
6. **Caveats:**
   - Ephemeral *containers* added by `kubectl debug pod/<p>` cannot be removed
     individually; they clear only when the pod is recreated. Do NOT delete a
     user's real pod to clear a debug container without explicit approval —
     note it instead.
   - Client-side leftovers (a `kubectl port-forward`/`proxy` started via
     `bash_background`) are not cluster resources; report them so the leader
     can `bg_cancel` the task.
7. **Report:** found / removed / left-for-confirmation / could-not-remove, with
   the exact `kubectl delete` commands run.

## Hard rules

- Delete ONLY resources positively identified as ephemeral (labeled) or
  explicitly confirmed. When in doubt, leave it and ask.
- **Prod protection:** never delete in production namespaces/contexts without an
  explicit override.
- Never delete real workloads, PVCs/PVs, namespaces, or anything with an owner
  reference to a controller you did not create.
- RBAC denial → report the boundary, do not retry with a different identity.

## Output rule

Always finish with a single line:
`Result: clean | removed-<N> | needs-confirmation | blocked`.
