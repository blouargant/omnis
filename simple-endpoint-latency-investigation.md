# Investigation playbook — intermittent stalls on the `Simple` gateway model

**Status:** open investigation. Hand this file to a fresh session (or a spawned
subagent) and execute it top to bottom. It is self-contained.

## Context — what we observed

While benchmarking the multi-agent **Coding squad**, the `code_scout` sub-agent
(model_ref `simple` → gateway model id **`Simple`**) showed **intermittent,
severe latency**:

- One search dispatch hung ~**310 s** and returned
  `context deadline exceeded (Client.Timeout … while reading body)` — i.e. the
  HTTP client read timeout tripped mid-response.
- In one `tools/squad-bench` suite pass, the **first two** Coding tasks stalled
  past a 180 s deadline; the **later two finished fast** (~19–34 s).
- Re-running the same two tasks minutes later: both finished in **~23 s**.

Same task, same prompt, same model — **23 s vs >180 s** run-to-run. So it is an
**endpoint latency fault**, not the squad wiring or the agent instruction (the
scout searched cleanly — 1–3 greps, no flailing, no re-dispatch, in every case).

The "first calls slow, later calls fast" pattern is the classic signature of a
**scale-to-zero / cold-start** deployment (a cheap model spun down when idle;
the first request cold-starts for minutes, then it's warm). That is the leading
hypothesis — but confirm it, don't assume it. This *may* be a gateway/backend
bug in the same family as the GLM-5.2 streaming issue (see
[`glm-5.2-streaming-bug.md`](glm-5.2-streaming-bug.md) and the omnis memory
`glm52-scaleway-streaming-toolcalls`).

## Goal

Characterise **when and why** `Simple` stalls, and produce a recommendation:
cold-start (→ keep-warm ping, or use a warmer model for the scout), a streaming
bug (→ `disable_streaming` for `simple`), rate-limiting, or a concurrency/queueing
limit. Collect hard evidence (timing distributions, request/response traces),
not vibes.

## Setup

Everything runs against the **gateway directly** — you do **not** need the omnis
server for most of this.

```bash
cd /home/bertrand/Documents/Dev/omnis
# Credentials live in the repo .env (the server loads it). Source it:
set -a; . ./.env; set +a
# Sanity (do NOT echo the key):
echo "base_url=$OPENAI_BASE_URL"; [ -n "$OPENAI_API_KEY" ] && echo "key: set"
# The model under test:
MODEL=Simple
# There is also a direct Scaleway endpoint in .env (SCALEWAY_API_BASE_URL /
# SCALEWAY_API_KEY) — used later to bisect gateway-vs-backend, as in the GLM-5.2 case.
```

Tools available: `tools/model-probe/probe.py` (stdlib, real requests; streaming +
tool-call + parameterless-tool checks) and `tools/squad-bench/bench.py`
(squad-level). `curl` + `python3` for ad-hoc probes.

> **Never print the API key.** Mask it in any output you report.

## Investigation steps

Run these in order; record timings and raw errors for each.

### 1. Capability + streaming baseline (model-probe)
```bash
python3 tools/model-probe/probe.py -u "$OPENAI_BASE_URL" -m "$MODEL" -k "$OPENAI_API_KEY"
```
Note especially: does **streamed chat** work, does **streamed tool-calling** work,
and the **`Parameterless tool over streaming`** check (the exact GLM-5.2 fault —
an empty-schema tool poisoning a streamed request). Save the full output.

### 2. Cold-start vs warm latency (the key test)
Measure **time-to-first-byte (TTFB)** and total time for a cold call vs warm calls.
```bash
# helper: time one non-streamed completion, print TTFB + total (no key echo)
probe1 () {
  curl -sS -w '\nHTTP=%{http_code} TTFB=%{time_starttransfer}s TOTAL=%{time_total}s\n' \
    -o /dev/null -m 600 \
    -H "Authorization: Bearer $OPENAI_API_KEY" -H 'Content-Type: application/json' \
    "$OPENAI_BASE_URL/chat/completions" \
    -d "{\"model\":\"$MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with one word: ok\"}],\"max_tokens\":8}"
}
# COLD: wait for the model to plausibly scale down, then hit it once.
#   (idle 10–15 min, or run this as the very first call of the session.)
probe1
# WARM: immediately fire 10 back-to-back calls and watch TTFB/TOTAL drop.
for i in $(seq 1 10); do probe1; done
```
**If cold TTFB is minutes and warm TTFB is <2 s → scale-to-zero cold-start
confirmed.** Repeat after another idle period to be sure it's reproducible.

### 3. Streaming stall test
Stream a longer answer and watch whether chunks arrive steadily or the stream
freezes mid-response (the failure mode the omnis HTTP client read-timeout hit).
```bash
curl -N -sS -m 600 -H "Authorization: Bearer $OPENAI_API_KEY" -H 'Content-Type: application/json' \
  "$OPENAI_BASE_URL/chat/completions" \
  -d "{\"model\":\"$MODEL\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"Count from 1 to 40, one number per line.\"}]}" \
  | while IFS= read -r line; do printf '%s %s\n' "$(date +%T.%3N)" "$line"; done
```
Look for a long gap between `data:` chunks, or a stream that ends without
`[DONE]`. Compare a **cold** run vs a **warm** run.

### 4. Tool-call round-trip (streamed and non-streamed)
The scout makes tool calls. Verify the endpoint returns `tool_calls` under
streaming for a **tool with real parameters** (a search tool), and separately
for a **parameterless** tool (the GLM-5.2 trigger). model-probe's tool checks
cover this; also do a raw streamed tool request to time it and confirm
`delta.tool_calls` arrive. Note TTFB for the tool-call response specifically —
the scout's stalls happened on its *post-grep* model turn (which decides the
next tool call), so time a request that *should* produce a tool call.

### 5. Concurrency (scout fan-out)
`code_scout` has `max_instances: 5`, so a fan-out fires up to 5 concurrent
`Simple` calls. Test whether concurrency triggers queueing/stalls:
```bash
for i in $(seq 1 5); do probe1 & done; wait
```
Compare per-call TOTAL under concurrency vs sequential.

### 6. Gateway vs backend bisection (like the GLM-5.2 case)
If `.env` has `SCALEWAY_API_BASE_URL` / `SCALEWAY_API_KEY`, repeat step 2/3
against the **direct Scaleway endpoint** for the same underlying model (find its
real id via `GET $OPENAI_BASE_URL/v1/model/info` or `/models`, or the LiteLLM
config). If the stall reproduces direct-to-Scaleway → backend cold-start; if only
through the ChapsVision gateway → gateway (LiteLLM) queueing/buffering.

### 7. Squad-level correlation loop (optional, ties it back)
Reproduce the intermittency end-to-end and correlate with cold/warm:
```bash
for i in $(seq 1 8); do
  python3 tools/squad-bench/bench.py --task search-single --deadline 400 \
    --server http://127.0.0.1:8080 --out /tmp/simple_loop.jsonl --json | \
    python3 -c 'import sys,json;d=json.loads(sys.stdin.read().splitlines()[-1]);print(d["status"],d["wall_ms"],"ms",d.get("subagent_errors"))'
done
```
Expect the first run(s) after idle to be slow/timeout and later ones fast.

## Evidence to collect (report these)

- Cold vs warm **TTFB / TOTAL** for `Simple` (step 2), with the idle interval used.
- Any streamed response that **stalls or ends without `[DONE]`** (step 3), with timestamps.
- Whether **tool_calls** arrive under streaming, and the **parameterless-tool**
  result (step 1/4).
- Concurrency effect (step 5).
- Gateway-vs-direct-backend result (step 6).
- The squad loop status/wall distribution (step 7).
- **Masked** creds only — never the key.

## Hypotheses (confirm / rule out)

1. **Scale-to-zero cold-start** (leading): first-after-idle call takes minutes,
   warm calls <2 s. *Fix:* a periodic keep-warm ping, accept the latency, or run
   the scout on an always-warm model (e.g. `balanced`) — which is option (a) we're
   about to try.
2. **Streaming stall / no-`[DONE]`** like GLM-5.2: streamed responses freeze.
   *Fix:* set `"disable_streaming": true` on `simple` in `models.json` (scoped to
   that model), as done historically for GLM-5.2.
3. **Rate-limiting / concurrency queueing:** stalls appear under the 5-way scout
   fan-out or bursty load. *Fix:* lower `code_scout` `max_instances`, or a warmer
   model.
4. **Gateway (LiteLLM) buffering** vs backend: isolate with step 6.

## What a good outcome looks like

A one-paragraph verdict naming which hypothesis holds, backed by the timing
evidence, and a concrete recommendation (keep-warm, `disable_streaming`, lower
concurrency, or switch the scout's model). If it turns out to be a genuine
gateway/backend bug, capture a minimal `curl` repro (masked key) suitable for a
bug report to the gateway/Scaleway team — mirroring `glm-5.2-streaming-bug.md`.
**Strip any secret before sharing the repro.**
