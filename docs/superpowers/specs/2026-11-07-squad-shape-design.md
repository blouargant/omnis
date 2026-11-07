# Squad shape: measure, then simplify (design, 2026-11-07)

## Intent

Pi — a single agent, four tools, no sub-agents — ties for first on the Harness
Leaderboard at about half the cost per solved task. In omnis's own Harbor
smokes the Coding squad's `reviewer` bought nothing (identical test outcomes,
+$2.60, ×4.5 wall time on httpx) and `coder` alone solved the Terminal-Bench
smoke for less than the full squad. Coding is among the hardest tasks; if a
simpler shape holds there, the same question applies to the other squads.

The goal is the **shape that is cheapest at equal quality in the production
configuration**, squad by squad, and to **ship that shape to everyone** (no
opt-in variant).

## Why the existing evidence is not enough

Every Harbor run forced one model onto every agent (leaderboard rule). With one
price, a sub-agent cannot save money; it can only isolate context, and it adds
its own overhead. In production the fleet is tiered — an expensive leader
(`premium`, Claude Sonnet 5.5) and cheap sub-agents (`simple`, `balanced`,
`hosted`) — which is exactly the case where delegating retrieval can pay. No
bench has compared a squad with a lighter shape under production tiering.

Working rule to test (my reading, not yet measured): a sub-agent earns its place
only if it (a) keeps bulk out of an expensive model's context by working on a
cheaper one, (b) brings independent judgment that changes the outcome, or (c)
enforces a guarantee.

## Goals

- For Coding, Knowledge and Kubernetes: compare the shipped shape with lighter
  shapes under production model tiering, on the existing quality measure of each
  squad.
- Adopt, for everyone, a lighter shape that costs less without a visible quality
  loss.
- Record the results in an `omnis-benches/reports/` report.

## Non-goals

- Changing which model an agent runs on (separate lever; the Knowledge
  model-cost bench already covers it).
- Ranking shapes finely: at 1–2 samples per task the only conclusion available
  is "not visibly worse, and cheaper".
- The System, Helper, Skill Editor and router squads.

## Anti-goals (a failure even if it "works")

- Removing or weakening `k8s_validator` or any safety guarantee — it stays in
  every Kubernetes shape.
- Spending past the budget, or running a variant to the end when it is already
  clearly more expensive than the reference.
- Shipping a lighter shape on noise: a quality drop seen in either sample
  blocks adoption.

## Constraints

- **Budget: 50 € total** for all model calls of this work.
- **Early stop:** a variant is stopped as soon as its cumulative cost exceeds
  **1.5×** the reference's cost on the same tasks, checked from the second task
  on. (1.5× is my call.)
- Production configuration = the shipped `config/models.json` tiers and
  `registry/agents/*/agent.json` `model_ref`s. No single-model override.
- Benches live in `omnis-benches` (repo policy); omnis changes are config +
  registry (+ instruction text), hot-reloadable.

## The experiments (decided)

Order: Coding first, then Knowledge, then Kubernetes. Each arm runs the same
omnis build; only the squad shape differs.

**Only members the benches actually exercise are under test** (decided):
`reviewer`, `code_scout`, `web_agent`, `research_critic`, `k8s_auditor`.
Members whose capability no bench calls — `code_docs` (programming-docs web
lookups), `refactorer` (isolated refactors), `agentmd_reviewer` (`/init`),
`doc_agent` (document extraction/conversion), `image_generator` — stay in every
arm and in the shipped shape whatever the result: removing them on a bench that
never calls them would drop a capability without measuring it.

| Squad | Arms | Quality measure | Samples | Budget |
|---|---|---|---|---|
| Coding | A shipped squad · B without `reviewer` · C without `reviewer` and `code_scout` (the coder searches the code itself) | Harbor Terminal-Bench 2.1 smoke (3 tasks), tests pass | 2 attempts | ~15 € |
| Knowledge | A shipped squad · B without `research_critic` · C without `research_critic` and `web_agent` (the leader holds the web tools itself) | the 4 `tasks-modelcost.json` questions, the two blind judges of `judge_modelcost.py` | 1 | ~20 € |
| Kubernetes | A shipped squad · B without `k8s_auditor` (two-pass audit off) | k8s-ai-bench gatekeeper suite, pass@1 | 1 | ~10 € |

Arms are nested (each removes one more member), so a result also says which
member was worth what. In an arm, the removed members are dropped from the
squad's `members` and from the leader's instruction (the way
`harbor/make_sysconf.py` `strip_reviewer` does it), so the leader never calls a
tool that is not mounted; the leader keeps its own tool groups. In Knowledge C
the leader gets `web_agent`'s web tool groups (`serper`, `ddg`, `web`).

DeepSWE is out: with `premium` on the coder it would cost more than the whole
budget. A Kubernetes "leader alone" arm is out: the change guard requires a
`k8s_validator` attestation and the validator is mounted only under
`k8s_editor`/`k8s_cleaner`.

Expected: Knowledge C (premium leader accumulating fetched pages) is likely to
hit the early stop; it stays in because it is the Pi-like shape the question is
about.

Cost estimate behind the budget split: the deepseek Terminal-Bench smoke cost
$0.98 per pass; `premium` (Sonnet 5.5, $2.10 in / $0.21 cached / $10.50 out per
M) with the gateway's prompt cache costs roughly 1.5× that per input token, so
about $1.5 per pass and ~$9 for 3 arms × 2 attempts. The Knowledge reference was
$5.86 for the 4 questions with a Sonnet 4.6 leader (Oct. 31), now cheaper with
Sonnet 5.5; arm C is expected to stop early. The judges' calls count toward the
budget.

## Decision rule (decided)

A lighter arm replaces the shipped shape (minus only the members it removed) when it costs less than the reference
and shows no quality drop (Coding: no task solved by A and failed by the arm;
Knowledge: judge score not lower beyond the judges' own ~1-point disagreement,
no new serious factual error flagged; Kubernetes: no gatekeeper task passed by A
and failed by the arm). If two lighter arms both qualify, the cheaper wins.
"Replace for everyone" means changing the shipped `config/agents.json` squad
composition and the leader's instruction so it no longer refers to removed
members, with CLAUDE.md updated.

## Left to the builder

- **Production tiering in the Harbor adapter.** `harbor/make_sysconf.py`
  re-points every tier at one bench model and enables the single-model override;
  a tiered mode must keep the shipped tiers, route them through the gateway, and
  price each agent at its own model's price (the per-agent usage footers the k8s
  runs already recalculate). How to expose it (flag, variant) is the builder's
  choice.
- **Coding arm C** (`coder` + `code_scout`) and Knowledge/Kubernetes arms as new
  variants of the existing adapters/setup scripts, removing the dropped members
  from the leader instruction as `strip_reviewer` does.
- How to enforce the early stop (a watcher on the usage records, or running
  tasks sequentially and checking between them).
- Concurrency, as long as runs do not starve shared gateway models (`Hosted`
  saturates under parallel load — see the model-cost report §5.2).
- Euro/dollar conversion for the budget (prices are in $; use a recent rate).
- Before spending, check that `premium` (`claude-sonnet-5-5`, no
  `max_output_tokens` in `models.json`) is not capped at 4096 output tokens by
  the gateway — the model-cost report §5.1 found that cap on Oct. 31; the Nov. 2
  bench that promoted Sonnet 5.5 suggests it was fixed. A one-request probe
  settles it; if capped, set `max_output_tokens` on the bench's models.json.
