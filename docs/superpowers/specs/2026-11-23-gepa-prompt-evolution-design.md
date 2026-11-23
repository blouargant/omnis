# GEPA prompt evolution for omnis agents — design

Date: 2026-11-23. Spans two repositories: **omnis** (overridable prompt texts)
and **omnis-benches** (the optimizer). Status: draft for approval.

## Intent

GEPA ("reflective prompt evolution", `gepa` on PyPI, 0.1.4) improves the texts
an agent runs on: it runs the agent on tasks, gives the traces of the failures
to a strong "reflection" model that proposes a rewritten text, keeps the
variants that score better on a Pareto frontier of tasks, and recombines them.
It has given clear gains on other agents. The goal is a **durable tool** to
improve omnis's agents with it, not a one-off experiment.

What we already learned by hand shapes it: rewriting the System leader's
instruction changed nothing on `balanced`, while a per-message `turn_reminder`
fixed its delegation (omnis-benches `reports/leader-fake-2026-11.md`). So the
texts worth evolving are not only an agent's instruction: GEPA must reach the
reminder, the skills, the shared blocks and the tool descriptions.

## Goals

- Evolve any text that reaches an agent's model: its instruction, its
  `turn_reminder`, a skill's `SKILL.md`, a shared block (operating contract,
  language/choice/steering/sub-agent blocks, skill rules, memory rules…), a
  tool description.
- **A text shared by several agents is evaluated on every agent it reaches.** A
  candidate that makes any of them regress is not adopted.
- Each run ends with a report and a patch a human reviews and commits. The
  baseline and the best candidate are compared on tasks the optimizer never saw.
- Runs are bounded (metric calls and dollars), resumable, reproducible from
  their config file.

## Non-goals

- No automatic adoption: nothing the optimizer writes reaches `registry/` or the
  Go code without a human commit.
- No optimization of tool *schemas* (parameter names, types) or of tool
  parameter descriptions in v1 — only a tool's top-level description.
- No optimization of the router's instruction in v1 (its accuracy has its own
  bench; can come later with that bench as the suite).
- No cheap "replay one recorded request" evaluator in v1 (approach 3 below): a
  later pre-filter.

## Anti-goals (a run that does these has failed, whatever its score)

- **Drops a rule born from an incident.** Many sentences in omnis's prompts
  exist because of an observed failure (CLAUDE.md lists them: "a sub-agent call
  is final, stop after two unhelpful attempts", "load every matching skill",
  "a prose question ends the turn", "never offload the routing decision"…). No
  task suite covers them all, so the score cannot protect them.
- **Writes the bench into the prompt.** A text naming the scenarios' hosts,
  files or expected answers (`batch-02`, "out of memory") scores well and is
  worthless.
- **Bloats the prompt.** Every token of a shared text is paid on every model
  call of every agent it reaches (the fixed-prompt pass cut 25 %; undoing that
  for a point of score is a loss).
- Makes one model better and the other worse: the leader runs on `premium` in
  production and on `balanced` in cheaper setups.

## Constraints found in the code

- Agent instructions, skills and `turn_reminder` are files under `registry/`
  and `agent.json`: a bench server already takes them from an overlay copied
  into its `OMNIS_HOME` (`squad-bench/setup-ab.sh`, `OVERLAY=`), hot-reloadable.
- The shared texts are **Go constants** with no override: `agentkit.SystemPrompt`
  / `SubAgentSystemPrompt`, `choicePolicyBlock`, `languagePolicyBlock`,
  `steeringAwarenessBlock`, `buildSubAgentCapabilitiesBlock`,
  `routerHandoffProtocolBlock`, `skills.skillInstruction`, `collmem.Rules`, the
  memory reminder, every tool's `Description`. Evolving them needs an omnis
  change first (part 1).
- The fake sub-agent server holds **one scenario at a time**: parallel
  evaluation needs one fake server (and one omnis bench server) per worker.
- One evaluation is a full omnis turn on the gateway: 10–45 s and a few cents.
  A GEPA run of a few hundred evaluations is hours and tens of dollars.
- Runs are noisy (the same task passes or fails across repeats), so a task is
  scored over repeats, and a gain is only claimed beyond the baseline's spread.

## Approaches considered

1. **Overridable texts in omnis + the optimizer in omnis-benches** — *chosen.*
   omnis gives each shared text a stable id and reads replacements from a
   layered `prompt_overrides.json`; the optimizer writes each candidate into a
   bench server's overlay and reloads. Costs an omnis change before the
   optimizer, but needs no rebuild per candidate, and is useful on its own
   (tune a shared text without recompiling).
2. **Rebuild omnis per candidate** with the Go constants patched. No omnis
   change, but a build per candidate and a text-substitution patch that breaks
   whenever the code moves. Rejected.
3. **Replay recorded model requests** with the candidate substituted and score
   only the next decision. 10–50× cheaper but blind to a whole turn's outcome.
   Kept as a possible pre-filter later.

The user picked 1, and the rule that a shared text is tested on every agent it
reaches.

## Part 1 — omnis: `prompt_overrides.json`

### Behaviour

- Every overridable shared text has a **stable id** and a default (today's
  constant). Proposed ids (builder may refine names, not the scheme):
  `contract.root`, `contract.subagent`, `block.language`, `block.choice`,
  `block.steering`, `block.subagents`, `block.handoff`, `skills.rules`,
  `memory.rules`, `memory.reminder`, and `tool.<ToolName>` for a tool's
  description (`tool.Bash`, `tool.WebFetch`, `tool.AskUserQuestion`…).
- `prompt_overrides.json` = `{"prompts": {"<id>": "<text>"}}`, resolved through
  the 3-layer search chain and **deep-merged by key** like the other config
  files (`configedit` section spec: a map merged by key, tombstone
  `prompts_removed`). Resolved in `ResolveRuntimeSettings`, so a hot-reload
  applies it to the next generation.
- A text that is built from parts (the sub-agent block lists the agent's team;
  a block interpolates a squad name) keeps its generated parts: the override
  replaces the **fixed wording**, with named placeholders where the default has
  generated content (`{subagents}`, `{squad}`…). An override missing a
  required placeholder is rejected for that id.
- An unknown id, an empty text, or a rejected placeholder ⇒ the default is used,
  a log line, and an entry in `RuntimeSettings.ConfigWarnings` (shown by
  Settings like the squad warnings). Never a failed resolution.
- **No-op contract:** no file ⇒ every request byte-identical to today.

### Catalogue route

`GET /api/prompts` → `{prompts: [{id, default, override?, agents: [...]}]}`,
where `agents` lists the agents of the **current generation** whose requests
contain that text (computed at build time from what each agent was given; a
tool description reaches the agents holding that tool, deferred or not). This
is the **reach map** the optimizer uses to decide which agents to evaluate. It
returns no secrets (texts only). Token-gated like the other config routes. Not
exposed in the Settings UI in v1.

### Tests (omnis)

Default-identity test per id (no file ⇒ same text as before), override applied
on root and sub-agent requests, placeholder rejection, layering, hot-reload,
reach map for a tool held by two agents and for a block on roots only.

## Part 2 — omnis-benches: `gepa/`

A new top-level directory beside `squad-bench/`, Python, depending on `gepa`
(pinned) and reusing `squad-bench`'s runner, fake server and overlay helpers.
Still driven over HTTP: nothing imports omnis.

### Components

A candidate is GEPA's `dict[str, str]`. Component names:

| Component | Written to (in the bench server's overlay) |
|---|---|
| `instruction:<agent>` | `registry/agents/<agent>/instruction.md` |
| `turn_reminder:<agent>` | `turn_reminder` in that agent's `agent.json` overlay |
| `skill:<name>` | body of `registry/skills/<name>/SKILL.md` (front matter kept as is) |
| `prompt:<id>` | `prompt_overrides.json` |

The seed candidate is read from the omnis checkout under test (files) and from
`GET /api/prompts` (defaults).

### Reach and suites

- `suites.json` maps an agent to its task files and the squad they run in
  (`leader` → `tasks-leader-fake.json`…; `k8s_leader` → `tasks-leader-fake-k8s.json`;
  `coder` → `tasks-leader-fake-coding.json`).
- The agents to evaluate = the agents each selected component reaches
  (`instruction`/`turn_reminder` ⇒ that agent; `skill` ⇒ agents whose skills
  list it, or all holding `Skill` with an empty list; `prompt` ⇒ the reach map).
- **A run refuses to start** when a reached agent has no suite, listing them.
  This is the user's rule, enforced rather than advised. (Consequence: the
  operating contract, which reaches almost every agent, cannot be optimized
  until most agents have suites. Single-agent texts come first.)

### Evaluation (the GEPA adapter)

- `evaluate(batch, candidate, capture_traces)`: write the candidate into a fresh
  overlay, reload the worker's bench server (`POST /api/config/reload`, check
  the generation changed and `ConfigWarnings` is empty — a rejected override
  is a failed evaluation, not a silent default), run each task of the batch
  with `bench.py` (`--repeat r`, `--nonce`, `--fake` when scripted), and return
  one score per task in [0, 1] = fraction of repeats passing all of the task's
  gates (`correct`, `fake_gate`, required `facts`, no `forbidden`).
- `objective_scores` carries cost ($) and tokens per task; the scalar score is
  correctness only. Cost is used for tie-breaks and in the report.
- Trajectories = the bench records: answer, failed gates (`fake_failed`,
  missing facts), sub-agent requests (`fake.requests`), tool calls, errors,
  cost. `make_reflective_dataset` turns a failed task into a short text:
  the task prompt, what was expected (gate names and fact ids, **not** the
  match patterns or scripted replies), what happened, and for a shared text
  which agent failed.
- Workers: N parallel workers, each with its own omnis bench server and fake
  server ports (default 2). Evaluations of one candidate are spread over them.

### Proposals and guards

A custom proposer wraps GEPA's default reflection (reflection model: `premium`
through the gateway, via an OpenAI-compatible callable) and rejects a proposal,
asking again up to twice, when:

- it drops a **protected rule**: each component may have a `keep` list
  (`gepa/keep/<component>.txt`, one rule per line, checked by the reflection
  model as "is this rule still stated, possibly reworded?" — a yes/no call on
  the eval model); seeded by the builder from the rules CLAUDE.md attributes to
  incidents, and reviewed by the user before the first run;
- it contains a **leak**: any token from a denylist built from the suites
  (scripted hostnames, file names, fact patterns, distinctive words of
  scripted replies);
- it grows the component by more than a set ratio (default +30 % characters
  over the seed).

The reflection prompt states these three rules too, so most proposals pass.

### Data split and final check

- Each suite's tasks are split train / val by the run config (GEPA's minibatches
  come from train, its Pareto frontier from val).
- A **held-out** suite per agent, never shown to GEPA, decides adoption: for the
  System leader `tasks-leader-real.json` and `tasks-leader-fake-direct.json`.
- At the end: baseline vs best on the held-out suites, on **every model in the
  run's model list** (first run: `balanced` and `premium`), `--repeat 5`.
  Verdict "adopt" only if no reached agent on no listed model loses more than
  the baseline's own spread, and at least one gains beyond it.

### Budget, resume, output

- Run config file (`gepa/runs/<name>.json`): components, agents (derived, shown),
  models, suites and split, repeats, workers, `max_metric_calls`, a dollar
  ceiling (stops the run, checked from the records' costs), reflection model.
- `run_dir` = `gepa/out/<name>/` (GEPA checkpoints ⇒ resumable).
- Output: `reports/gepa-<name>-<date>.md` (scores per agent × model, cost,
  candidates on the frontier, held-out verdict), the records (`.jsonl`), and a
  patch against the omnis checkout: files for registry components, and for a
  `prompt:<id>` component the change to the Go default (proposed text in the
  report; the builder decides whether the tool writes a Go patch or only the
  text).
- `gepa/README.md` documents running it, and the omnis-benches CLAUDE.md/README
  list the new tool.

## Training tasks

The shipped suites hold 5 scenarios per leader, too few for GEPA to do more
than fit them. Before the first real run, each leader with a suite (System,
Kubernetes, Coding) gets ~20 new scripted scenarios in their own files
(`tasks-leader-fake-train*.json`), written by a Fable model and reviewed by the
user. The existing 5 stay as validation; the held-out suites stay untouched, and
the new scenarios must share no host, file or scripted fact with them.

## First run

System leader, components `instruction:leader` + `turn_reminder:leader`, models
`balanced` and `premium`, train/val from `tasks-leader-fake.json`, held-out
`tasks-leader-real.json` + `tasks-leader-fake-direct.json`. This target has
hand-tuned before/after numbers, so it also tells us whether GEPA beats what we
did by hand. The second candidate is `k8s_leader` or `coder` with their suites.

## Calls made while writing (for review)

- **Search on one model, check the others at the end.** GEPA searches on the
  run's first model (`balanced` for the leader: weaker models show prompt
  effects first); the other listed models are only run on the held-out suites.
  Searching on (task, model) pairs would double every evaluation and need a
  bench server per model per worker.
- **First-run budget:** search repeats 2 per task, held-out repeats 5, dollar
  ceiling $60, `max_metric_calls` set from it after a calibration minibatch.
- **Order:** Part 2 is built and exercised first on the registry components
  (the first run needs no `prompt_overrides.json`); Part 1 follows, then runs on
  shared texts.
- **Tool descriptions in v1 = the tools omnis writes itself** with a fixed
  description: the fs tools (`Read`, `Write`, `Edit`, `MultiEdit`, `Grep`,
  `Glob`, `Bash`), `AskUserQuestion`, `WebFetch`, `WebSearch`, the teammate,
  background-task and todo tools. Not ADK's skill toolset, MCP tools, routing
  tools, or descriptions generated at build time (sub-agent wrappers).
- **Adopt rule:** per (agent, model), the suite mean over repeats, with a 90 %
  bootstrap interval resampling repeats within tasks. Adopt when no (agent,
  model) mean of the candidate falls below the baseline's interval and at least
  one rises above it.
- **Keep-list check on the whole candidate:** a protected rule counts as kept if
  any component that reaches the same agent still states it, so a rule may move
  from the instruction to the reminder.

## Left to the builder

- Exact Go wiring of the overrides (passing texts through `agentkit.AgentConfig`,
  tool-description wrapping), final id names and placeholders per block.
- How the reach map is computed in omnis.
- The score composition details per task kind, the default train/val ratio,
  `reflection_minibatch_size`, and GEPA options (merge on/off).
- Whether to start a fresh bench server per candidate or reload one (reload is
  the default; fall back to restart if a reload leaves state behind).
- Seeding the `keep` lists and the leak denylist (user reviews them).
