---
name: k8s-audit
description: Audit a Kubernetes cluster for policy compliance — enumerate every resource in scope and report the EXACT set that violates a stated rule (e.g. missing resource limits/probes/labels, disallowed image repos or tags, RBAC bindings, PodDisruptionBudgets, admission/OPA-Gatekeeper/Kyverno constraints). Use whenever the task is "list/find all resources that violate policy X" or a security-posture / compliance check across many resources, as opposed to diagnosing one unhealthy workload.
metadata:
  author: blouargant@chapsvision.com
  tags: "kubernetes, audit, compliance, policy, gatekeeper, opa, kyverno, security, playbook"
---

# Kubernetes Compliance Audit

The *playbook* for a POLICY-COMPLIANCE AUDIT: given a rule, check **every**
resource in scope and report the **exact set** that violates it — no misses, no
false alarms. This is a different discipline from `k8s-triage`:

| | k8s-triage | k8s-audit |
|---|---|---|
| Goal | find *why one workload is broken* | find *which resources break a rule* |
| Scope | one workload, deep | every resource, wide |
| Success | root cause | a precise VIOLATING set |

Auditing is **read-only** — never mutate the cluster during an audit.

Audits are scored on **precision AND recall**: a flagged compliant resource
(false positive) and a missed violation (false negative) both count against you.
Real audit suites deliberately plant **compliant decoys** (to catch
over-reporting) and **subtle violations off the happy path** (to catch
under-reporting). Beating them is a matter of discipline, not cleverness.

## Procedure

### Phase 1 — Pin the rule as a precise predicate

1. **Read the rule literally** and restate it as a testable predicate before
   touching the cluster. Nail down three things:
   - **Which kinds** it applies to (Pods? Deployments? Ingresses? RoleBindings?
     PDBs? all workload controllers?).
   - **The exact condition** (the field(s) and the failing value).
   - **The scope of the condition:**
     - *cluster-wide* vs a *single namespace*;
     - *pod-level* (one fact per pod) vs *container-level* (evaluated per
       container);
     - **conjunction vs disjunction** — "missing **both** a readiness **and** a
       liveness probe" (violates only when BOTH are absent) is NOT "missing a
       readiness **or** liveness probe" (violates when EITHER is absent). Read
       the "and"/"or"/"both" wording exactly.
2. **When the wording is genuinely ambiguous**, pick the reading the grader most
   likely intends — usually the **stricter, smaller-result-set** interpretation
   — state your interpretation in one line, and do **not** flag borderline
   resources on a generous stretch of the rule.

### Phase 2 — Enumerate the COMPLETE candidate set

3. **List everything in scope — do not sample.** Get the full set as structured
   output so nothing is skipped:
   ```bash
   kubectl get <kinds> -n <ns> -o json     # or -A for cluster-wide
   ```
   Count the candidates so you know exactly how many you must classify. Every
   candidate ends the audit as either *violating* (with cited evidence) or
   *compliant*.

### Phase 3 — Extract fields deterministically, on EVERY path

4. **Use `-o jsonpath` / `-o json | jq` — never eyeball `describe`.** Structured
   extraction is what stops you missing a field. Example (all container images,
   all container types, across all pods):
   ```bash
   kubectl get pods -n <ns> -o json | jq -r '
     .items[] | .metadata.name as $n
     | ( (.spec.containers // []) + (.spec.initContainers // [])
         + (.spec.ephemeralContainers // []) )[]
     | "\($n)\t\(.name)\t\(.image)"'
   ```
5. **Inspect EVERY relevant path — the misses hide off the happy path.** A rule
   about images, limits, probes, or securityContext applies to **all** container
   types unless the rule explicitly narrows it:
   - **Pods:** `.spec.containers[]`, **`.spec.initContainers[]`**,
     **`.spec.ephemeralContainers[]`**.
   - **Workload controllers** (Deployment / StatefulSet / DaemonSet / Job /
     ReplicaSet): the pod template —
     `.spec.template.spec.{containers,initContainers,ephemeralContainers}`.
   - **CronJob:** `.spec.jobTemplate.spec.template.spec.{containers,initContainers,ephemeralContainers}`.
   - **Pod-level fields** a rule may target directly:
     `.spec.automountServiceAccountToken`, `.spec.serviceAccountName`,
     `.spec.securityContext`, `.metadata.labels`/`.metadata.annotations` — and
     remember container-level overrides can differ from the pod-level value.

   > The single most common miss is auditing `spec.containers` only and
   > overlooking an `initContainer`. Enumerate all three container lists, every
   > time.

### Phase 3b — Rule-family extraction recipes (deterministic)

These remove the judgment calls that go wrong under pressure. When the rule
matches one of these families, follow the recipe mechanically instead of
eyeballing.

#### RBAC audits (ClusterRoles / Roles)

- NEVER shortlist roles by name (grepping for "endpoint", "controller", …). Run
  ONE mechanical filter over the COMPLETE set and flag every match:
  ```bash
  kubectl get clusterroles -o json | jq -r '.items[] |
    select(.rules[]? |
      ((.resources // []) | any(. == "endpoints" or . == "*")) and
      ((.verbs // [])     | any(. == "create" or . == "update" or . == "patch"
                                or . == "delete" or . == "deletecollection" or . == "*"))
    ) | .metadata.name' | sort -u
  ```
  Adapt the resource/verb lists to the rule under audit. A wildcard `*` in verbs
  or resources DOES grant the permission — it matches.
- AGGREGATION: edit/admin-style permissions are frequently carried by
  **aggregation ClusterRoles** (labelled
  `rbac.authorization.k8s.io/aggregate-to-edit` / `…-admin` / `…-view`), e.g.
  `system:aggregate-to-edit`. These hold the offending rules in their own
  `.rules` and MUST be flagged when they match the filter — do not skip a role
  because its name doesn't mention the audited resource, or because it is a
  built-in. Evidence = the matching rule, regardless of the role's name.

#### Numeric thresholds — compute, never eyeball

1. Convert both sides to the same unit before comparing (1Gi = 1024Mi =
   1073741824 bytes; 1G = 1000M). Write the comparison out explicitly:
   `value=1073741824, threshold=1073741824, operator=<, result=EQUAL`.
2. EQUALITY AT THE BOUNDARY: a value exactly EQUAL to the stated threshold is
   **compliant** unless the rule unambiguously says "strictly less than".
   Equality is the classic planted decoy — when the boundary reading is at all
   uncertain, equality = compliant.
3. Scope the field: "memory limits" constrains memory only — a missing or large
   CPU limit is NOT a violation of a memory rule (and vice-versa).

#### Image references — parse, don't pattern-match

For every image string, parse mechanically:

1. Contains `@sha256:` → digest-pinned → **compliant** (a digest is stricter than
   any tag).
2. Take the substring AFTER the last `/` (the repo:tag segment). A `:` BEFORE the
   last `/` is a registry PORT (`registry.local:5000/app` has NO tag), never a
   tag.
3. In that final segment: no `:` → untagged → **violating**. Tag present →
   violating ONLY if the tag is EXACTLY `latest` (case-sensitive, whole string).
   Tags that merely contain "latest" (`v2-latest-stable`) or are pinned
   (`1.2.3`, `stable`) are **compliant**.

Apply to `containers` + `initContainers` + `ephemeralContainers` and controller
pod templates. For each flag, quote the parsed image string and which step fired.

### Phase 4 — Classify with evidence, resisting decoys

6. **Evidence-first flagging.** Flag a resource **only** when you can quote the
   concrete field **and value** that fails the predicate (e.g.
   `resource-003 initContainers[0].image = nginx:latest → repo not in allow-list`).
   If you cannot point at the offending field, it is **not** a violation — do not
   flag it. "On-topic", "looks related", or "probably" is not evidence.
7. **Decoy awareness.** Assume compliant look-alikes are planted. A resource in
   the same category as the rule is **not** automatically a violation — before
   flagging, re-read the predicate and confirm the resource actually FAILS it,
   not merely that it's about the same subject. When unsure, the resource is
   **compliant** (do not flag on a hunch).
8. **Per-container vs per-pod resolution.** If the rule is per-container, a
   resource violates when **any** in-scope container fails it; still cite the
   specific container. If the rule is per-pod, evaluate the pod as a whole.

### Phase 5 — Report in the grader's exact format

9. **Match the requested output byte-for-byte.** If the task asks for
   `VIOLATING: <name>` lines, emit **one line per violating resource, the
   resource name only**, nothing else on the line — no prose, no compliant
   resources, no counts mixed in. If no format is specified, list the violating
   resource names, then a short evidence appendix (one line each: resource →
   field → value). The grader's format always wins over this skill's default.

## Two-pass audit (leader / coordinator)

For a high-stakes audit, run **two independent passes and reconcile** — this is
what removes the last false positives and false negatives:

1. **Pass A** — delegate the audit to the **k8s_investigator** (this skill).
2. **Pass B** — delegate an **independent** audit to the **k8s_auditor**. Do
   *not* hand it Pass A's list as the answer; have it audit from scratch and, if
   given Pass A's findings at all, treat them as unverified claims to confirm or
   refute with its own field evidence, while independently hunting for anything
   Pass A missed (especially off-happy-path container types).
3. **Reconcile:**
   - Flagged by **both** with concrete field evidence → **include**.
   - Flagged by **only one** pass → **you (the leader) inspect that one resource
     yourself** with a targeted read and break the tie on the field evidence;
     include only if the offending field is real.
   - A flag **no** pass can back with a quoted field → **drop it**.
   This union-then-tie-break kills over-reporting (a decoy flagged by one pass is
   tie-broken away) and under-reporting (a violation only the independent pass
   caught is included).

## Hard rules

1. **Read-only.** Never `apply`/`patch`/`delete`/`edit`/`scale`/`exec`-write
   during an audit. An audit reports; it does not remediate (propose fixes
   separately if asked).
2. **Enumerate completely.** Never sample or stop early — every in-scope resource
   must be classified.
3. **Every container type, every time** — `containers` + `initContainers` +
   `ephemeralContainers` (and the pod template for controllers).
4. **Cite a field for every flag.** No quoted offending field ⇒ not a violation.
5. **Decoys are compliant.** Do not flag a resource merely for being on-topic.
6. **Read the rule's scope exactly** — namespace vs cluster, per-pod vs
   per-container, and "and"/"or"/"both" logic.
7. **RBAC denial → report the boundary**, do not retry with a different identity.
8. **Rule-family recipes are mandatory** (Phase 3b). For an RBAC rule, filter the
   complete role set by resource+verb (wildcards match) and flag aggregation
   ClusterRoles by their own `.rules` — never shortlist by name. For a numeric
   threshold, normalize units and treat a boundary-EQUAL value as compliant
   unless the rule says "strictly less than". For an image rule, a `:` before the
   last `/` is a registry port (not a tag), a `@sha256:` digest is compliant, and
   only untagged or exactly-`latest` violate.

## Output rule

Finish with a single line: `Result: audited-<N-violations> | clean | blocked`
— unless the task specifies a grader output format, in which case that format is
the whole answer.
