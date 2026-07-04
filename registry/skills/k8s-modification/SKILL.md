---
name: k8s-modification
description: Safely modify a live Kubernetes resource — detect whether it is Helm-, GitOps- (Flux/Argo), or kubectl-managed; preview every change with kubectl/helm diff and dry-run; prefer Helm-native changes for Helm releases; and fall back to server-side apply / strategic-merge patch with care so future helm upgrades are not broken. Use whenever a change to a live cluster is required (apply, patch, scale, set image, edit values, helm upgrade).
metadata:
  author: blouargant@chapsvision.com
  tags: "kubernetes, helm, modification, apply, server-side-apply, gitops, playbook"
---

# Kubernetes Safe Modification

The *playbook* for CHANGING a live Kubernetes resource with the smallest blast
radius and without breaking the tool that owns it (especially Helm). Read this
before any `apply`, `patch`, `scale`, `set`, `edit`, or `helm upgrade`.

Every mutating command is **permission-gated** — it prompts the user for
confirmation before it runs. Do not try to work around the prompt. If a call is
denied, report it and stop; never retry with a different flag or identity.

## Phase 1 — Establish the target and guardrails

1. **Confirm the cluster context.** `kubectl config current-context` and quote
   it back before doing anything else.
2. **Pin the target:** namespace + kind/name + the *exact* intended change.
3. **Production guardrail.** Never modify production namespaces or contexts
   (name contains `prod`, `prd`, `production`) without an explicit user override.

## Phase 2 — Determine who OWNS the resource

Read the resource's labels/annotations before choosing a method:

```bash
kubectl get <kind> <name> -n <ns> \
  -o jsonpath='labels={.metadata.labels}{"\n"}annotations={.metadata.annotations}{"\n"}'
```

- **Helm-managed** — label `app.kubernetes.io/managed-by=Helm` **and**
  annotations `meta.helm.sh/release-name`, `meta.helm.sh/release-namespace`.
  Confirm with `helm status <release> -n <rel-ns>` and
  `helm get values <release> -n <rel-ns>`.
- **GitOps-managed (Flux/Argo)** — labels like
  `kustomize.toolkit.fluxcd.io/name`, `helm.toolkit.fluxcd.io/name` (Flux) or
  `argocd.argoproj.io/instance` / annotation `argocd.argoproj.io/tracking-id`
  (Argo CD). The desired state lives in **Git**.
- **kubectl / manual** — annotation
  `kubectl.kubernetes.io/last-applied-configuration` (client-side apply) or no
  manager markers at all.

## Phase 3 — Choose the change strategy by owner

- **Helm →** change *through Helm*: `helm upgrade <release> <chart> -n <rel-ns>`
  with `-f values.yaml` or `--set`, using `--reuse-values` so you do not drop
  other settings. **Never** `kubectl edit`/`patch` a Helm-owned resource for a
  persistent change — the next `helm upgrade` will revert it (drift) or hit a
  field-manager conflict. If the chart/values source is not available to you,
  say so: a persistent change needs the chart; a direct kubectl change will be
  reconciled away on the next upgrade, so do it only as an explicitly
  acknowledged stop-gap.
- **GitOps →** the source of truth is Git; a direct cluster change will be
  reconciled away (often within minutes). Recommend changing the Git source (or
  deliberately suspending reconciliation). Apply directly only as an
  acknowledged temporary measure, and state that it is temporary.
- **kubectl / manual →** prefer `kubectl apply --server-side -f <manifest>`
  (declarative, tracks field ownership) or a targeted `kubectl patch`
  (strategic-merge by default; `--type=merge`/`json` when needed). Save the
  manifest to a file first (Write tool) so the change is reviewable and
  revertible. Avoid `kubectl edit` (no diff, no audit trail).

## Phase 4 — Preview before you change (always)

Prefer previews that do **not** prompt:

```bash
kubectl diff -f change.yaml                 # exactly what apply would change
helm diff upgrade <release> <chart> -n <ns> -f values.yaml   # helm-diff plugin
helm template <chart> -f values.yaml        # render locally
```

Only when a diff is insufficient, fall back to a dry-run apply
(`kubectl apply --server-side --dry-run=server -f change.yaml` or
`helm upgrade --dry-run=server`). If the `helm-diff` plugin is missing, use
`helm upgrade --dry-run=server` and inspect the rendered output.

## Phase 5 — Apply the smallest change

- Change **only** the intended fields. Do not re-apply a whole fetched manifest
  (that steals field ownership and can wipe other managers' fields).
- Keep existing labels/annotations, especially `app.kubernetes.io/managed-by`
  and `meta.helm.sh/*`.
- **Server-side apply conflicts:** if `apply --server-side` reports a conflict
  with another manager (e.g. `helm`, `argocd`), STOP. Do **not**
  `--force-conflicts` on a field owned by Helm/Argo/Flux — stealing ownership
  breaks that tool. Resolve through the owner (helm upgrade / git) instead.
  `--force-conflicts` is only for a manually-owned resource, and only with
  explicit user approval.
- **Immutable fields** (Service `.spec.clusterIP`, Deployment `.spec.selector`,
  Job template, …) cannot be patched — they require recreate. For a Helm
  release that means `helm upgrade`; never manually delete+recreate a
  Helm-owned resource.

## Phase 6 — Verify and report

- `kubectl rollout status` / `kubectl get` the target; for Helm,
  `helm status` + `helm history` (confirm a NEW, deployed revision — not a
  failed one).
- Report: the ownership determination, the strategy chosen and why, the exact
  commands run, the diff, and the post-change health. Cite command + output.

## Hard rules

1. **Preview (diff/dry-run) before every real change** — no exceptions.
2. **Never break the owner.** Do not diverge a Helm-owned resource from its
   chart; do not `--force-conflicts` a field owned by helm/argo/flux; do not
   manually delete+recreate a Helm resource.
3. **Prod protection.** Never modify production namespaces/contexts without an
   explicit user override.
4. **Never `delete` without explicit user confirmation** (deletion is
   destructive; it is gated).
5. **Smallest change.** Touch the fewest fields that achieve the goal; preserve
   existing labels/annotations.
6. **RBAC denial → escalate**, do not retry with a different account.

## Output rule

Always finish with a single line:
`Result: applied | previewed | blocked | needs-confirmation`.
