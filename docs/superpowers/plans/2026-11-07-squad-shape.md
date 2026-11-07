# Squad shape experiments — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Measure, under production model tiering, whether the Coding,
Knowledge and Kubernetes squads can drop `reviewer`, `code_scout`,
`research_critic`, `web_agent` or `k8s_auditor` at equal quality and lower cost —
and ship the lighter shape to everyone where it wins.

**Architecture:** One shared description of a squad *arm* (`squad-bench/shapes.json`)
is turned into a config overlay directory by `squad-bench/shape_overlay.py`
(agents.json squad members + the leader's agent.json / instruction.md). Each bench
drops that overlay into the omnis user layer: the Harbor adapter (Coding), a
squad-bench server per arm (Knowledge), and `omnis-agent`'s per-task home
(Kubernetes). The overlay is checked for free with `prompt-size/` before any
model is called. A ledger + early-stop helper enforce the budget.

**Tech Stack:** Python 3 stdlib (omnis-benches rule), bash, Harbor 0.24, k8s-ai-bench,
squad-bench; omnis config/registry (JSON + markdown) for the adoption step.

**Spec:** `docs/superpowers/specs/2026-11-07-squad-shape-design.md`

## Global Constraints

- Budget **50 € total** for every model call (benches + judges + probes). Track in
  dollars at **1 € = 1.16 $ → 58 $ cap** (my call; recheck the rate if it moved).
- Early stop: an arm stops as soon as its cumulative cost exceeds **1.5×** the
  reference arm's cost on the same tasks, checked from the **second** task on.
- Production configuration: the shipped `config/models.json` tiers and agent
  `model_ref`s; **no single-model override**.
- Members never removed in any arm: `code_docs`, `refactorer`, `agentmd_reviewer`,
  `doc_agent`, `image_generator`, `summariser`, `k8s_validator`, and every other
  member not listed in an arm's `drop`.
- Bench code lives in `omnis-benches`, stdlib only, credentials from the root
  `.env`; nothing in omnis imports it.
- Adoption rule: an arm replaces the shipped shape when it costs less than the
  reference AND has no quality drop (Coding: no task solved by A and failed by the
  arm; Knowledge: mean judge score not lower than A by more than 1.0 and no new
  serious factual error noted by a judge; Kubernetes: no gatekeeper task passed by
  A and failed by the arm). Two qualifying arms → the cheaper wins.
- Run omnis at the current `feat/truncated-reply-grep` tip (or whatever it is
  merged into) in every arm; record the commit in each report.

## Review Focus

- An overlay that only *looks* applied: omnis layering keeps a member (squad
  merge by name) or ignores the overlay instruction.md → Task 2 checks the
  rendered prompt with `prompt-size`, not the files.
- A leader instruction still telling it to call a removed member → the overlay
  prepends an "unavailable" note naming every removed member; Task 2 asserts it.
- Per-agent pricing wrong under tiering (an agent without `model_ref`, an agent
  renamed in the event log, cached tokens priced at input price) → Task 3 tests.
- `premium` output capped at 4096 tokens by the gateway → Task 1 probe, before
  any spend.
- A run that dies early (gateway 429, budget exceeded) recorded as a quality
  failure → every campaign task separates infra failures from task failures
  (the Harbor summarizer already does: `test_infra_error_is_not_a_fail`).

---

### Task 1: Gateway check for `premium`

**Files:**
- Create: `omnis-benches/reports/squad-shape-2026-11-07.md` (start of the report)

- [ ] **Step 1: Probe the output cap.** One chat request to `claude-sonnet-5-5` on
  the gateway asking for ~1,500 words, no `max_tokens`. Record `finish_reason` and
  `completion_tokens` in the report.
  Expected: `stop` with > 4096 completion tokens. If `length` at 4096: every arm's
  models.json overlay sets `"max_output_tokens": 32000` on `premium` (Tasks 3–5 add
  it), and say so in the report.
- [ ] **Step 2: Start the ledger** `omnis-benches/reports/squad-shape-ledger.jsonl`
  (one line per spend: `{"step", "usd"}`), probe cost included.
- [ ] **Step 3: Commit** (omnis-benches) `reports: squad-shape probe + ledger`.

### Task 2: Arm overlays (`shapes.json`, `shape_overlay.py`) checked with prompt-size

**Files:**
- Create: `omnis-benches/squad-bench/shapes.json`
- Create: `omnis-benches/squad-bench/shape_overlay.py`
- Modify: `omnis-benches/prompt-size/run.sh` (copy `$OVERLAY` into the probe's home)
- Test: `omnis-benches/squad-bench/test_shape_overlay.py`

**Interfaces:**
- Produces: `shapes.json` = `{"<arm>": {"squad": str, "leader": str, "drop": [str], "add_tools": [str]}}`
  with exactly these arms: `coding-A` (drop []), `coding-B` (drop `["reviewer"]`),
  `coding-C` (drop `["reviewer","code_scout"]`), `knowledge-A` (drop []),
  `knowledge-B` (drop `["research_critic"]`), `knowledge-C` (drop
  `["research_critic","web_agent"]`, add_tools `["serper","ddg","web"]`),
  `k8s-A` (drop []), `k8s-B` (drop `["k8s_auditor"]`).
- Produces: `build_overlay(sys_dir: Path, arm: dict, out: Path) -> Path` and CLI
  `shape_overlay.py --sys <dir> --arm <name> --out <dir>`.

- [ ] **Step 1: Write the failing tests** in `test_shape_overlay.py` against a
  fake sys dir (agents.json with a `Coding` squad `coder` + `[code_scout, code_docs, reviewer]`,
  `registry/agents/coder/{agent.json,instruction.md}`):
  - `test_drop_writes_members_without_dropped`: overlay `agents.json` ==
    `{"squads":[{"name":"Coding","members":["code_docs"]}]}` for drop `["code_scout","reviewer"]`
    (order of the shipped members kept; squad name matched case-insensitively,
    written with the shipped spelling).
  - `test_note_names_every_dropped_member`: overlay `instruction.md` starts with a
    paragraph containing `code_scout` and `reviewer` and the words "not available",
    followed by the shipped instruction unchanged.
  - `test_add_tools_extends_leader_tools`: for add_tools `["serper","ddg","web"]`
    overlay `agent.json` `tools` == shipped tools + those three, no duplicates.
  - `test_reference_arm_writes_nothing`: drop `[]` and no add_tools → `out` empty.
  - `test_unknown_member_is_an_error`: dropping a name not in the squad raises
    `SystemExit`.
- [ ] **Step 2: Run** `python3 -m unittest squad-bench/test_shape_overlay.py` → FAIL.
- [ ] **Step 3: Implement `build_overlay`.** The note text (exact):
  `"In this configuration the following team members are not available: {names}. Wherever this prompt or a skill tells you to delegate to them, do that part yourself with your own tools, or skip a review step that only they perform."`
  For `knowledge-C` append: `" You hold the web search and fetch tools yourself."`
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: prompt-size check (no model cost).** Add to `prompt-size/run.sh`:
  when `$OVERLAY` is set, `cp -r "$OVERLAY"/. "$WORK/home/"` before the runs. For
  each non-reference arm build the overlay from the omnis checkout's `config/` +
  `registry/` and run `OVERLAY=<dir> prompt-size/run.sh ../omnis out-<arm>`.
  Expected, per arm: the squad root's request has no tool named like a dropped
  member, starts its instruction with the note, and (knowledge-C) declares
  `WebSearch` and `WebFetch`. Record the root's fixed-prompt size per arm in the report.
- [ ] **Step 6: Commit** (omnis-benches) `squad-bench: squad shape arms as config overlays`.

### Task 3: Harbor — production tiering, overlays, per-agent pricing

**Files:**
- Modify: `omnis-benches/harbor/make_sysconf.py`, `omnis-benches/harbor/omnis_harbor/agent.py`,
  `omnis-benches/harbor/omnis_harbor/usage.py`, `omnis-benches/harbor/run.sh`, `omnis-benches/harbor/README.md`
- Test: `omnis-benches/harbor/test_harbor_adapter.py`

**Interfaces:**
- Consumes: `shape_overlay.build_overlay` (Task 2), `shapes.json` arm names.
- Produces: option `tiered: bool` (default False), option `shape: str` (default `""`, an arm name);
  `make_sysconf.tiered_models(shipped: dict, premium_max_output: int | None) -> dict`;
  `usage.agent_prices(sysconf: Path) -> dict[str, tuple[float, float, float | None]]`;
  `usage.cost_by_agent(agents: dict, prices: dict, default: tuple) -> float`;
  `run.sh` env `TIERED=1`, `SHAPE=<arm>`.

- [ ] **Step 1: Write the failing tests:**
  - `test_tiered_models_keep_tiers_and_disable_override`: shipped catalogue with
    `premium`/`simple` → result keeps both entries and their prices,
    `override_model_enabled` is False, providers' `base_url`/`api_key` are the env
    names `OPENAI_BASE_URL`/`OPENAI_API_KEY`, `embed_model_ref` is `""`.
  - `test_agent_prices_follow_model_ref`: sysconf with coder→premium (2.1/10.5/0.21)
    and code_scout→simple (0.2625/0.525/None) → those tuples; an agent without
    `model_ref` gets its squad leader's price.
  - `test_cost_by_agent_sums_each_agent_at_its_price`: tallies
    `{"coder":{prompt 1e6, cached 9e5, output 1e5}, "code_scout":{prompt 1e6, cached 0, output 0}}`
    → `0.1*2.1 + 0.9*0.21 + 0.1*10.5 + 1*0.2625` = 1.7115 $.
  - `test_shape_overlay_lands_in_user_layer`: `build(..., shape="coding-C")` puts
    the overlay under `sysconf/overlay/` and `_env()` sets `OMNIS_HOME` to a dir the
    run command seeds from it (`cp -r overlay/. $OMNIS_HOME/`).
- [ ] **Step 2: Run** `python3 -m unittest discover -s harbor` → FAIL.
- [ ] **Step 3: Implement.** In tiered mode `bench_models` is replaced by
  `tiered_models`, prices come from `agent_prices`, `populate_context_post_run`
  uses `cost_by_agent` (the `-m` model is only the label). The overlay is copied
  into `OMNIS_HOME` by the run command before omnis starts. `TIERED`/`SHAPE` are
  passed to Harbor as `--ak tiered=…`, `--ak shape=…`; the job name includes the shape.
- [ ] **Step 4: Run tests** → PASS.
- [ ] **Step 5: Commit** (omnis-benches) `harbor: production tiering and squad-shape overlays`.

### Task 4: squad-bench and k8s-ai-bench take an overlay; budget helpers

**Files:**
- Modify: `omnis-benches/squad-bench/setup-ab.sh` (`MODEL=tiered` keeps the shipped tiers; `OVERLAY=<dir>` copied into the arm's home)
- Modify: `omnis-benches/k8s-ai-bench/omnis-agent` (`OMNIS_BENCH_OVERLAY` copied into each per-task home, after `permissions.json`)
- Create: `omnis-benches/squad-bench/budget.py`
- Test: `omnis-benches/squad-bench/test_budget.py`, `omnis-benches/k8s-ai-bench/test_omnis_agent.py`

**Interfaces:**
- Produces: `budget.spent(ledger: Path) -> float`; `budget.over_ratio(ref: list[float], arm: list[float], ratio=1.5, min_tasks=2) -> bool`
  (cumulative per-task costs in run order); CLI `budget.py check --ledger L --cap 58`
  (exit 1 when the cap would be passed) and `budget.py stop --ref R.jsonl --arm A.jsonl`
  (exit 1 when the arm must stop; reads `total_cost_usd` per record in order).

- [ ] **Step 1: Write the failing tests:**
  - `test_over_ratio_waits_for_second_task`: ref `[1]`, arm `[5]` → False;
    ref `[1,1]`, arm `[1,2]` → True (3 > 1.5×2); ref `[1,1]`, arm `[1,1.9]` → False.
  - `test_spent_sums_ledger`: two lines 1.5 and 2.25 → 3.75.
  - `test_overlay_copied_into_task_home` (omnis-agent): with `OMNIS_BENCH_OVERLAY`
    pointing at a dir holding `agents.json`, the spawned home contains it and still
    contains `permissions.json`.
- [ ] **Step 2: Run** the three test files → FAIL.
- [ ] **Step 3: Implement** the helpers and the two overlay hooks.
- [ ] **Step 4: Run tests** → PASS (plus the existing suites in both dirs).
- [ ] **Step 5: Commit** (omnis-benches) `squad-bench, k8s-ai-bench: overlays and budget helpers`.

### Task 5: Coding campaign (~15 € cap)

- [ ] **Step 1:** `budget.py check` with this campaign's estimate (9 $) → OK.
- [ ] **Step 2: Reference.** `TIERED=1 SHAPE=coding-A ATTEMPTS=1 ./harbor/run.sh tb full smoke`
  twice (two jobs = two attempts). Append each job's cost to the ledger.
- [ ] **Step 3: Arms B, C**, each attempt 1 then attempt 2, `budget.py stop`-style
  check against A after every job (Harbor: compare cumulative job costs with the
  same rule). Stop an arm that trips it; append costs to the ledger.
- [ ] **Step 4: Record** per arm: tasks passed per attempt, cost, wall time,
  delegations per member, in the report. Apply the adoption rule.
- [ ] **Step 5: Commit** (omnis-benches) the jobs' summaries + report section.

### Task 6: Knowledge campaign (~20 € cap)

- [ ] **Step 1:** Start three servers with `MODEL=tiered OVERLAY=<arm overlay> setup-ab.sh`
  (`web_fetcher` moved to `balanced` in all three, as in the model-cost bench, so
  `Hosted` is not saturated — same overlay field for every arm).
- [ ] **Step 2: Reference** `knowledge-A` on `tasks-modelcost.json` (4 tasks, run
  sequentially), ledger updated.
- [ ] **Step 3: Arms B and C** sequentially, `budget.py stop` after each task.
- [ ] **Step 4: Judge** the answers of the arms that finished with
  `judge_modelcost.py` (both judges); ledger updated with the judges' cost.
- [ ] **Step 5: Record** cost, judge scores, serious errors noted, per arm; apply the
  adoption rule; commit (omnis-benches).

### Task 7: Kubernetes campaign (~10 € cap)

- [ ] **Step 1:** Build `omnis-server` from the omnis tip; `OMNIS_SERVER_BIN` and
  `OMNIS_SYSTEM_CONFIG_DIR` point at it and at a copy of its `config/` + `registry/`.
- [ ] **Step 2: Reference** `k8s-A`: `TASKS_DIR=.../tasks/gatekeeper ./k8s-ai-bench/run.sh`;
  ledger updated from the usage footers.
- [ ] **Step 3: Arm B** with `OMNIS_BENCH_OVERLAY=<k8s-B overlay>`, early-stop check
  every 5 tasks against A's cumulative cost on the same tasks.
- [ ] **Step 4: Record** pass@1 per task for both arms; apply the adoption rule;
  commit (omnis-benches).

### Task 8: Adopt the winners in omnis, finish the report

**Files (only for squads where an arm won):**
- Modify: `omnis/config/agents.json` (the squad's `members`)
- Modify: the leader's `omnis/registry/agents/<leader>/instruction.md` (remove the
  dropped members' paragraphs and every instruction to call them; Knowledge C also
  adds `serper`, `ddg`, `web` to `knowledge_leader`'s `tools` in its `agent.json`)
- Modify: `omnis/CLAUDE.md` (Agent topology, the squad's section), `omnis/internal/features/FEATURES.md`
- Modify: skills that require a removed member (e.g. `registry/skills/deep-research/SKILL.md`
  and its mandatory critic pass, `registry/skills/k8s-audit/SKILL.md` and the two-pass flow)

- [ ] **Step 1:** For each winning arm, make the edits; `go test ./agent/ ./server/`
  (shipped-config tests), then `prompt-size/run.sh` → the root has no tool for a
  removed member and no instruction naming it (grep the recorded system prompt).
- [ ] **Step 2:** Finish `reports/squad-shape-2026-11-07.md`: per squad, arms, cost,
  quality, decision, total spend from the ledger (≤ 58 $).
- [ ] **Step 3: Commit** omnis (`feat(squads): …` naming each change) and
  omnis-benches (report).
