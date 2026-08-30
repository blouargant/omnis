# web_agent cost reduction — experimental protocol

**Status:** design approved, not implemented. **Method:** every cost/quality claim
below is a *hypothesis to falsify by measurement*, never an assumption. No variant
is adopted or rejected without a run.

## 1. Problem

One Knowledge-squad session (`conversation_fitting-eagle.json`, 3 turns, a factual
question about a DS7 fuel-flap actuator) cost **$2.87**, of which the `web_agent`
sub-agent alone accounted for **$2.5157 — 88%**.

### Measured evidence

| turn | prompt tok | output tok | web_agent cost | wall |
|---|---:|---:|---:|---:|
| 0 — "is this normal?" | 2 119 166 | 13 429 | $0.572 | 3m46 |
| 1 — "what does the actuator cost?" | 6 152 334 | 23 813 | $1.637 | 8m36 |
| 2 — "give me the part references" | 1 130 519 | 7 776 | $0.306 | 3m43 |
| **total** | **9 402 019** | **45 018** | **$2.5157** | |

Three facts follow directly from that table:

1. **97.2% of the cost is input.** Output is $0.0711 of $2.5157. Any optimisation
   aimed at answer verbosity is noise.
2. **A cheaper model is not a lever in this price grid.** Excluding `hosted`
   (ruled out by the user as too slow), `balanced` is *already the cheapest model
   on input*:

   | model | input $/M | output $/M |
   |---|---:|---:|
   | **balanced** (current) | **0.26** | 1.58 |
   | simple | 0.2625 | 0.525 |
   | high | 0.63 | 3.78 |
   | premium | 3.15 | 15.75 |
   | ~~hosted~~ (excluded) | 0.04 | 0.27 |

   `simple` is **1% more expensive than `balanced` on input**. Restratifying onto a
   "smaller" model would save approximately nothing. **The lever is the number of
   tokens, not their unit price.**
3. **`web_agent` records no `cache_read` on any of the three turns**, while
   `knowledge_leader` (premium/Anthropic) does. Prompt caching is not engaging for
   the `balanced` model behind the gateway.

### Hypothesised mechanism (NOT yet measured)

A sub-agent runs its own flow loop and re-sends its whole accumulated context on
every model call, so cost is quadratic in its tool calls:

```
prompt_total  ≈  W × [ N × base  +  R × F²/2 ]
```

with `W` = parallel dispatches, `F` = fetches per dispatch, `R` = tokens per
fetched page in context.

`R ≈ 8k tokens` is **known from code**: `WebFetch` output is capped at 50 000
chars ([core/tools/common.go:18](../../../core/tools/common.go)) and then re-capped
by the sub-agent shaper at 32 000 chars
([agent/coding_efficiency_plugin.go:51](../../../agent/coding_efficiency_plugin.go)).
`WebSearch` results are negligible (5–10 title+snippet entries).

`W ≈ 10` and `F ≈ 12` are **estimates obtained by inverting the cost model against
turn 1**. They are not measurements. Phase 0 of this protocol replaces them with
measured values (`delegations` and `subagent_tools` already carry them).

## 2. Hypotheses under test

Each is a candidate explanation of, or fix for, the cost. None is privileged.

| id | lever | claim to falsify |
|---|---|---|
| **A** | sliding window on sub-agent context | Dropping tool results older than the last `K` turns `O(F²)` into `O(F×K)` without quality loss, because the agent already read each page in full. |
| **B** | stratification `web_agent` → `web_fetcher` | Delegating fetches splits one `F`-step session into `F` one-step sessions, killing the quadratic. Cost is unchanged per token but the token count collapses. |
| **C** | tuning existing dials | `max_instances` and an explicit fetch budget in the instruction cut `W` and `F` enough on their own. |
| **D** | depth-ladder gating | A lookup-tier question ("what does the actuator cost?") is being handled at deep-research tier; gating the trigger removes work rather than making it cheaper. |
| **E** | gateway prompt caching | A growing-prefix context is the textbook prompt-cache shape; if the LiteLLM gateway priced `cached_tokens` on `Balanced`, cost would drop with no behavioural change. **Out of scope for this protocol** — it is a gateway-side ask, not an omnis change. Note it is partially antagonistic with A (pruning rewrites the prefix and invalidates a cache). |

**B was initially dismissed on a latency argument. That dismissal was premature**
and is retracted: B's quality profile may be *better* than A's (a gatherer returns
verbatim quotes), and B is the cheapest hypothesis to falsify because it needs no
Go at all.

## 3. Instrument

All bench code lives in the sibling repo **`omnis-benches/squad-bench`** (project
policy: no bench or eval code in omnis). Three additions to `bench.py`:

### 3.1 Multi-turn tasks

`bench.py` is currently single-turn (one `POST /api/sessions/:id/messages`). The
expensive turn in the trace was a **follow-up**, with the leader already holding
context. Add `prompts: [...]` alongside the existing `prompt` (backward
compatible): `run_task` loops the POSTs on one session, aggregates metrics, and
retains one answer per turn for scoring.

### 3.2 Layer 1 — deterministic fact checklist (hard gate)

A task declares expected and forbidden facts, scored per turn:

```json
"facts": [
  {"id":"actuator",  "on":0, "required":true,  "match":"/actuateur|actionneur/"},
  {"id":"price-new", "on":1, "required":true,  "match":"/7[0-9]\\s*€/"},
  {"id":"oem",       "on":2, "required":true,  "match":"/9820772380/"},
  {"id":"caveat",    "on":2, "required":true,  "match":"/à vérifier|non confirm|indicatif/"},
  {"id":"price-used","on":1, "required":false, "match":"/39\\s*€/"}
],
"forbidden": [
  {"id":"ref-as-fact", "on":2, "match":"/9831776780/", "unless":"/non confirm|à vérifier|pourrait/"}
]
```

The `forbidden … unless` clause is the core of the guard. The baseline mentions
reference `9831776780` **while flagging it unconfirmed**; a degraded variant that
asserts it flatly is disqualified. No "facts found" score would catch that.

New metrics: `facts{found,required,missing[]}`, `forbidden_hits`, `quality_gate`
(pass/fail), `distinct_urls`, `facts_per_fetch`.

**Rejected metric — "sources cited in the answer".** Measured on the trace, the
three answers contain **0, 0 and 1** URLs. That metric would read ≈0 on the
baseline itself. Source coverage is instead taken from the URLs in `agent_tool_call`
arguments, and is treated as an **efficiency** signal (same facts with fewer
fetches is strictly better), not a quality signal.

### 3.3 Layer 2 — blind pairwise LLM judge (survivors only)

A separate script `judge.py`, no coupling to `bench.py`. Compares each surviving
variant **pairwise against the baseline**, **blind** (variant identity stripped),
with **position permutation** to cancel position bias, repeated 3× because the
judge is stochastic too. Verdict per criterion — completeness, sourcing,
caveats/nuance, absence of invention — as `better / equivalent / worse`. Pairwise
detects "lost nothing" far better than an absolute score.

### 3.4 Task set (validity floor)

One DS7 task cannot validate a general change — it would tune to a single trace.
**At least three shapes are required:**

- **lookup** — a question whose answer is one search away (where D must pay off);
- **deep research** — a multi-source question (where A and B must pay off);
- **canary** — a question whose answer sits deep inside a long page, designed to
  **fail** if a variant truncates. Without the canary, the bench would ratify a
  harmful truncation. Its source URL is pinned in the task notes so its
  disappearance is detectable.

## 4. Variants

One variable at a time.

| id | lever | exact change | phase |
|---|---|---|---|
| **V0** | — | current state (reference) | 0 |
| **V1** | `W`, linear | `web_agent.max_instances` 10 → 4 | 1 |
| **V2** | `F`, quadratic | `web_agent/instruction.md`: numbered procedure, explicit fetch cap, explicit stop condition | 1 |
| **V3** | structural (**B**) | `web_agent.subagents:["web_fetcher"]`, drop the `web` tool group, `web_fetcher.model_ref="balanced"` | 1 |
| **V3b** | structural (**B**) | as V3, plus dropping `serper`/`ddg` (the fetcher searches too) | 1 |
| **V4** | do less (**D**) | tighten the `deep-research` trigger so the lookup tier does not reach it | 1 |
| **V5** | `R`, linear | `budgetFor("WebFetch")` → 8 000 chars | 2 |
| **V6** | structural (**A**) | sliding window over sub-agent tool results (`BeforeModelCallback`), swept at `K` ∈ {2, 3} as two sub-variants — `K` is a parameter to measure, not to guess | 2 |

V1–V4 are **hot-reloadable, no build**. V5–V6 need a binary. Hence the phasing: no
Go is written until config-only variants are shown insufficient.

### 4.1 Variants as declarative patches

Variants are applied over the HTTP API (`PUT /api/config/parsed/…` then
`POST /api/config/reload`) from a `variants.json`, with V0 restored at the end of
each run — not by hand-editing files. This is not convenience: it is what makes
interleaving (§5.1) possible, and it sidesteps a known trap — `OMNIS_CONFIG_PATH`
is exported in the developer's shell profile and bypasses `OMNIS_SYSTEM_CONFIG_DIR`.

## 5. Controls

### 5.1 The web moves — interleave

The confound specific to a *web* bench: a page can change or vanish between runs.
Running all of V0 then all of V3 would confuse web drift with variant effect.
**Interleave variants in time** (V0,V1,V3,V0,V1,V3…) and **re-run V0 at the end of
the campaign** as a drift witness. If the closing V0 diverges from the opening V0
beyond the checklist gate, the campaign is void and is repeated.

### 5.2 Gateway variance — medians only

The gateway has documented generation-throughput spikes (20–90 s intermittently on
Simple/Balanced). Compare **medians across repeats**, never single runs, and treat
latency as a noisy signal until differences exceed observed dispersion.

### 5.3 Judge stochasticity

Covered in §3.3: blind, position-permuted, 3 passes.

## 6. Decision rule (pre-registered)

Fixed **before** any number is seen, so that no variant can be rescued post hoc.

> A variant is **retained** iff:
> 1. `quality_gate` passes on **every** repeat of **every** task (layer 1), **and**
> 2. median **`total_cost_usd`** falls by **≥ 30%** — the *session* total, not
>    `web_agent`'s share alone, so a variant that merely moves spend onto the
>    leader is not a win — **and**
> 3. median `wall_ms` is **not above V0's median plus one interquartile range**
>    of V0's own latency distribution. The looser form is deliberate: §5.2
>    establishes latency as a noisy signal, so a strict inequality would
>    disqualify variants on gateway jitter.
>
> A variant is **disqualified** if a required fact is missing even once, or if any
> `forbidden` clause is hit.
>
> Retained variants go to the judge (layer 2) and are kept only on a verdict of
> **≥ equivalent on all four criteria**.
>
> Retained variants are then **combined, and the combination is re-tested as a
> variant in its own right**. Gains are not assumed multiplicative — they are
> measured.

Clause 4 exists because an earlier analysis projected `A × C` as multiplicative.
That was a model output, not a result.

## 7. Budget

3 tasks, `--repeat 2` for screening, `--repeat 3` for finalists.

| phase | runs | estimated cost |
|---|---:|---:|
| Phase 0 — baseline + witness | 8 | ~$12 |
| Phase 1 — V1–V4, V3b | 30 | ~$35 |
| Layer-2 judge (survivors) | — | ~$1 |
| Phase 2 — V5, V6(K=2), V6(K=3) (only if needed) | 27 | ~$22 |
| **Total** | **~65** | **~$70** |

That is roughly **28 DS7 sessions' worth** — repaid quickly if $2.52 becomes $0.30,
never if the campaign is abandoned halfway. Note a good variant **makes its own
bench cheaper**, so Phase 2 will cost less than budgeted if Phase 1 bites.

## 8. Non-goals / known limits

- **Absolute correctness is not measured.** The reference facts (`9820772380`,
  ~77 €, 39 €) come from omnis itself. The protocol measures **consistency** against
  a ground truth that must be established once, by hand, by the user.
- **Generalisation beyond three question shapes is not established.** Three tasks
  is the floor for not overfitting to one trace, not a guarantee.
- **`hosted` is excluded** by user constraint (too slow to be usable). The harness
  would measure it at no extra design cost if that exclusion is ever to be settled
  on numbers rather than impression.
- **Hypothesis E (gateway caching) is out of scope** — a gateway configuration ask,
  tracked here only so it is not forgotten, and flagged as partially antagonistic
  with A.
- **CLI/TUI are untouched.** This concerns server-mode Knowledge-squad research.
