---
name: deep-research
description: The playbook for an EXHAUSTIVE, decision-grade research investigation — when the user asks for deep/in-depth/thorough/exhaustive research, says they need it to make a decision, or asks whether an approach/tool/configuration is a good idea. Audits the assumptions baked into the question, runs multiple search waves with a coverage review between them, considers the alternatives the user did not mention, and has the findings adversarially reviewed before answering. Use instead of a single search-and-summarise pass whenever being incomplete would cost the user a bad decision.
metadata:
  author: blouargant@chapsvision.com
  tags: "research, deep-research, investigation, decision, due-diligence, evidence, sources, playbook"
---

# Deep Research

The playbook for **decision-grade** research: the user is going to *act* on your
answer, so being merely correct about what they asked is not enough — you must
also be right about what they *should have* asked.

This is a different discipline from a standard lookup:

| | standard research | deep research |
|---|---|---|
| Goal | answer the question | equip a decision |
| Scope | the question as asked | the question **and its premises** |
| Waves | one search pass | ≥ 2, with a coverage review between |
| Done when | you have an answer | a critic finds no blocker |
| Failure mode | wrong answer | **complete, well-sourced answer to the wrong question** |

That last row is the one that bites. A deep-research answer that faithfully
reasons *inside* a flawed premise the user handed you is worse than useless: it
is confidently wrong, and it reads as authoritative.

> **Worked failure (the reason this skill exists).** Asked *"Is LMCache
> interesting for a quantized 27B model running on 2 GPUs (no NVLink)?"*, a
> research pass produced an excellent, well-sourced brief on LMCache — pros,
> cons, a real bug catalogue — and never once questioned the *2 GPUs* part.
> vLLM's own documentation says that without NVLink you should prefer
> **pipeline** parallelism over tensor parallelism, and a 4-bit 27B model may
> not need two GPUs at all. The single highest-value fact for the user's actual
> decision was one search away, and nobody searched for it, because it was not
> in the question. **The setup stated in a question is a claim to verify, not a
> constraint to obey.**

## Procedure

### Phase 1 — Premise audit (do this BEFORE any search)

Write the question out and extract, explicitly, two lists.

1. **The asked question(s)** — what the user literally wants to know.
2. **The premises** — every fact, choice, constraint, or identifier the user
   stated *as given*. Hunt for these in particular:
   - **Configuration choices**: a topology, a parallelism mode, a flag, a
     deployment shape, an architecture ("on 2 GPUs", "behind nginx", "with
     Redis as the queue"). The user chose it; the choice may be wrong.
   - **Hardware / environment facts**: interconnect, VRAM, CPU, network, OS,
     managed-vs-self-hosted.
   - **Named entities and versions**: a model name, a library, a product, a
     version number. It may not exist, may be misremembered, or may be
     superseded — verify the entity is real before researching it in depth.
   - **The framing itself**: "should I use X for Y" presupposes X is a
     candidate and Y is the goal. Is there a Z that dominates X? Is Y really
     the goal?
   - **Implicit goals**: the user says "throughput" but the setup implies
     "latency", or vice versa.

3. **Turn every premise into a research question of its own.** Each one gets a
   line in the matrix below. A premise you do not research is a premise you have
   silently endorsed.

### Phase 2 — Build the research matrix

Before searching, write the matrix. It is your definition of "done" and the
checklist the coverage review (Phase 4) grades against.

| # | Angle | Question | Why it matters to the decision |
|---|---|---|---|
| A | **Asked** | the user's literal question(s) | it is what they asked |
| B | **Premise** | one row per premise from Phase 1 | a bad premise invalidates the answer |
| C | **Alternatives** | what are the other ways to achieve the user's goal? | the best option may not be on their list |
| D | **Failure modes** | known bugs, limits, gotchas, regressions, versions | what will bite them in week two |
| E | **Expert delta** | "what would a specialist tell this person that they did not think to ask?" | the highest-value rows usually appear here |

Row E is not decoration. Write it last and force yourself to fill it — if you
cannot think of anything an expert would add, you have not understood the domain
well enough to be giving a decision-grade answer yet.

### Phase 3 — Wave 1: breadth

Dispatch the matrix to the research sub-agent(s) — **in parallel, one task per
angle**, not one task containing everything. Each task must be a self-contained,
precisely worded question with the context it needs (the sub-agent is stateless
across calls).

Rules for the wave:

- **Never fold the premise rows into the asked rows.** "Is LMCache good for
  TP=2 on PCIe?" and "Is TP=2 on PCIe even the right topology?" are different
  searches and one of them will not happen if you merge them.
- **Prefer first-party sources for anything normative.** For a configuration
  recommendation, the project's own documentation, its issue tracker, and its
  release notes outrank a blog post, a Reddit thread, or your memory. A vendor
  saying "do not do X on hardware Y" is a decisive source; a benchmark blog is
  corroboration.
- **Version-anchor everything.** A bug, a flag, a default, and a limit are all
  version-dependent. An unversioned claim is an unusable claim.

### Phase 4 — Coverage review (the step that is always skipped)

**Do not synthesise yet.** Put the wave-1 results next to the matrix and answer,
row by row, in writing:

1. **Which rows are actually answered?** An angle that came back thin, generic,
   or unsourced is *not* answered. Mark it open.
2. **What contradicts what?** Two sources disagreeing is a finding, not noise.
   Resolve it with a third, more authoritative source, or report the
   disagreement explicitly.
3. **What did the results themselves raise?** Wave 1 almost always surfaces a
   term, a flag, a bug, or a trade-off you did not know to ask about. These are
   the best rows in the matrix and they can only appear here. Add them.
4. **Which claims are load-bearing but unsourced?** Anything the recommendation
   rests on must trace to a page that was actually fetched in this task.

### Phase 5 — Wave 2: depth

Run a second wave on everything Phase 4 left open: the unanswered rows, the
contradictions, the newly discovered angles, the unsourced load-bearing claims.

**A deep-research task is never finished after one wave.** If wave 1 genuinely
answered every row, wave 2 exists to attack the answer: search for the strongest
case *against* your emerging recommendation, and for the failure reports of
people who did what you are about to recommend.

Iterate (wave 3, …) while a wave keeps opening material rows. Stop when a wave
returns only confirmation of what you already have.

### Phase 6 — Adversarial review

Hand the **original question** and your **draft brief** to a fresh reviewer (the
`research_critic` sub-agent when one is mounted; otherwise do this pass yourself,
explicitly and in writing, before you answer). The reviewer answers one question:
*"If the user acts on this, what will go wrong?"*

Loop on the blockers it returns until none remain. Do not defend the draft —
re-search.

### Phase 7 — Deliver

Structure the answer so the decision is easy to make and the reasoning is easy to
audit:

1. **Verdict first.** The direct answer to the asked question, in one or two
   sentences, with the condition that governs it.
2. **Premise check — "what we challenged in your question".** Any premise that
   did not survive Phase 1–5 goes *here, near the top*, not buried in section 4.
   State the assumption, what the evidence says, and what to do instead. If every
   premise held, say so in one line — that is a real finding too.
3. **The analysis** — pros / cons / mechanics, each load-bearing claim carrying
   its source link and its version.
4. **Alternatives you did not ask about**, as a comparison table, with the
   condition under which each wins.
5. **Recommendation**, with the concrete configuration, and the measurement that
   would prove or disprove it in the user's own environment.
6. **Confidence and open questions.** What you could not verify, and what a
   source disagreed about. Never fill a gap from memory to make the brief look
   complete.

## Sourcing rules

- Every load-bearing claim traces to a page **fetched in this task**. Training
  memory is a lead to verify, never a citation.
- Distinguish **fetched fact** (cite it), **inference** (say "this implies"),
  and **unverified** (say so, and put it under open questions).
- Version-anchor bugs and behaviour: "open as of vLLM 0.24 / LMCache 0.4" beats
  "known bug".
- Prefer primary sources for normative claims (official docs > maintainer issue
  comment > vendor blog > third-party blog > forum thread).
- Do not launder a guess through a confident tone. A gap named honestly is worth
  more to a decision than a plausible sentence.

## Red flags that you are about to ship a bad brief

- You accepted a number, a topology, or a product name from the question without
  a single search aimed at it.
- Your brief answers every question the user asked and raises none they did not.
- One sub-agent call produced the whole brief.
- The recommendation has no measurement attached — nothing the user could run
  tomorrow to falsify it.
- You "know" a fact you did not fetch.
- The alternatives section is missing, or contains only alternatives the user
  already named.

## Output rule

Finish with a single line: `Confidence: high | medium | low` — followed by the
one open question that would most change the recommendation if answered.
