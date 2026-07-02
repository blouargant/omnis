# squad-bench — benchmark omnis squads across models and tasks

A dependency-free (Python stdlib) harness that drives a **running omnis-server**
through its HTTP API the same way the web UI does — create a session pinned to a
squad, send one task prompt, stream the SSE — and reduces the event stream to a
**metrics record**. The point: change which **model** an agent runs on and re-run
the **same task** to see the effect on cost, latency, delegation behaviour, and
correctness. Sister tool to [`tools/model-probe`](../model-probe) (which checks a
raw endpoint's capabilities); this one checks *squad behaviour*.

## Why this exists

While turning the Coding squad multi-agent (a `premium` leader that delegates
search to a cheap `code_scout` and docs to `code_docs`), a live test showed the
wiring worked but surfaced two model-level facts that only a repeatable benchmark
can track over time:

- the **`simple`** model backing `code_scout` was slow enough that one search
  dispatch hung ~**310 s** and tripped the HTTP client read timeout
  (`context deadline exceeded … while reading body`), returning an empty result —
  so the `premium` leader re-dispatched, and the turn ran away;
- **`premium`** returned answers in **2–3 coarse chunks** where `hosted` streamed
  **~750** token events for a comparable answer — i.e. premium wasn't streaming
  token-by-token.

Rather than eyeball these once, `bench.py` measures them every run.

## Run it

```bash
# server must be running (default http://127.0.0.1:8080; set OMNIS_SERVER_TOKEN if auth is on)
python3 tools/squad-bench/bench.py --suite                 # all tasks in tasks.json
python3 tools/squad-bench/bench.py --task search-single    # one task
python3 tools/squad-bench/bench.py --suite --repeat 3       # 3 samples each (models are stochastic)
python3 tools/squad-bench/bench.py --suite --out runs.jsonl # append a JSON record per run
python3 tools/squad-bench/bench.py --task search-single --json   # machine-readable only
```

Flags: `--server`, `--token`, `--deadline <s>` (per-run cap, default 420),
`--cwd <dir>` (override a task's working dir), `--keep` (don't delete the bench
session), `--tasks <file>`.

## The benchmarking loop (models × tasks)

1. Establish a baseline: `bench.py --suite --out baseline.jsonl`.
2. Change **one** agent's model — Settings → Agent → model, or edit `models.json`
   / `registry/agents/<name>/agent.json` and `POST /api/config/reload` (model
   changes hot-reload; embedder identity needs a restart).
3. Re-run the same suite: `bench.py --suite --out variant.jsonl`.
4. Compare the JSONL records. Each carries a `models` block keyed by agent with
   the **price** that was active (price is the model's identity here), so you can
   tell which model produced which numbers.

The same loop tunes **instructions**, not just models (see "Tuning prompts on
weak models" below): change the agent's `instruction.md`, reload/reinstall, re-run.

## Metrics (per run)

| Field | Meaning |
|---|---|
| `status` | `done` / `timeout` / `cancelled` / `error` |
| `wall_ms`, `ttfb_ms` | total time; time to first `token`/`message` frame |
| `token_events` | # of streamed `token` frames — **high ⇒ streams token-by-token; 1–3 ⇒ coarse/buffered/non-streaming** |
| `delegations` | `{agent: count}` the leader delegated to (a sub-agent tool call) |
| `redispatches` | # of times the leader called the **same** sub-agent again (retry / flailing) |
| `leader_tools` | `{tool: count}` the leader ran directly |
| `subagent_tools` | `{agent: {tool: count}}` each sub-agent ran internally (e.g. a scout doing 12 greps = over-searching) |
| `models` | `{agent: {in$/M, out$/M, prompt_tok, out_tok, calls, est_cost_usd}}` |
| `total_cost_usd` | summed estimate across all agents in the turn |
| `subagent_errors` | sub-agent results that were empty or carried an error (`deadline exceeded`, `timeout`, `"error"`) |
| `ask_user` | # of permission prompts (want **0** for a squad whose read-only members are allow-listed) |
| `correct` | if the task has `expect`, whether the final answer matched |

## Tasks

Defined in [`tasks.json`](tasks.json). `cwd: "sandbox"` runs against a **fresh temp
copy** of [`sandbox/`](sandbox) (a tiny Go module, git-seeded and isolated, so an
accidental edit can never touch a real repo and is trivially reverted). `expect`
is a substring or `/regex/` (case-insensitive) matched against the final answer.

Shipped tasks: `search-single` (one-target search — measures scout over-search /
redispatch), `search-multi` (parallel fan-out over two targets), `symbol-fields`
(precise symbol read), `docs-lookup` (network — `code_docs` web research; skip
offline). Add your own with the same shape — a good bench grows a task per
behaviour you care about.

## Tuning prompts on weak models

A cheap model (e.g. `simple`) that "gets lost" — over-searching, not stopping,
returning empty — is usually a **prompt** problem before it's a model problem.
Bench the weak model, tighten the agent's `instruction.md` into a literal
numbered procedure with explicit **stop conditions** (see `code_scout`'s
instruction: one search at a time, stop as soon as found, hard cap on searches),
re-run, and watch `subagent_tools` grep counts and `redispatches` drop. Prompts
hardened against a dumb model tend to run even better on a smart one — so this is
worth doing *before* reaching for a pricier tier.

## Notes / limits

- One run is one sample; models are stochastic. Use `--repeat` and compare
  distributions, not single numbers.
- Costs are **estimates** from the per-model prices in `models.json` (the same
  numbers the web UI shows), not a provider invoice.
- Endpoint latency (e.g. the `simple` 310 s outlier) is a property of the gateway
  deployment, not of omnis — but it's exactly what you want a benchmark to catch.
