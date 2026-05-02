# Kubernetes Context Compression E2E Test

This test validates that context management is working under realistic, noisy conditions.

It uses a live Kubernetes cluster to generate long logs and event streams, then checks four concrete signals:

1. Compression events are emitted.
2. At least one compression pass reduces tokens.
3. The agent can answer a memory-only question without calling tools.
4. The agent still recalls a unique marker discovered early in the session.

## Why this is realistic

Kubernetes triage naturally creates large transcripts:

- `kubectl logs --tail=N` payloads
- `kubectl describe` output
- events streams
- cross-namespace listings

This is exactly the shape of context pressure that the compression plugin must handle in production.

## Prerequisites

- A reachable Kubernetes cluster (`kubectl config current-context` must work).
- A configured model in `.env` (provider + key/endpoint).
- Go toolchain installed.

## One-command run

```bash
bash scripts/run_k8s_context_e2e.sh
```

The script will:

- source `.env`
- create an isolated namespace and a noisy pod
- run [examples/s24_k8s_context_e2e/main.go](../examples/s24_k8s_context_e2e/main.go)
- cleanup the namespace

## Pass/Fail contract

The harness exits non-zero if any condition fails.

Pass requires all of:

- `compression_start > 0`
- `compression_end > 0`
- `max_reduction > 0`
- `total_reduction > 0`
- final turn contains marker + namespace + pod
- final turn performs zero tool calls

The harness prints `PASS:` lines with the exact metrics.

## Artifacts to inspect

- Compression audit file (`--audit-path`), default from script:
  `.agent_memory_k8s_e2e_<namespace>.md`
- Event stream (if root binary is used separately):
  `.agent_events.log`

## Running without the script

```bash
set -a; . ./.env; set +a

go run ./examples/s24_k8s_context_e2e \
  --namespace <ns> \
  --pod <pod> \
  --marker <marker> \
  --audit-path .agent_memory_k8s_e2e_manual.md
```

Use this mode if you want to point at an existing workload rather than the synthetic noisy pod.

## Notes

- This test performs read-only cluster inspection from the agent side.
- The setup script creates and deletes a temporary namespace/pod.
- LLM calls are billable on hosted providers.
