# ADK v2 migration readiness

**Status:** paving (still on ADK v1.5; NOT migrated). **Do not delete this doc
or the CLAUDE.md "ADK v2 transition" section until `go.mod` is on
`google.golang.org/adk/v2` and this file says "migrated".**

## Why

ADK Go 2.0 (GA 2026-06-30, module `google.golang.org/adk/v2`, Go 1.25+) is a
graph-based rewrite with breaking changes to the agent API, event model, and
session schema. We are deliberately waiting for it to mature, but paving the way
so the eventual upgrade is small and low-risk. Primary sources: the ADK Go 2.0
announcement and the README-v2.md migration guide.

## The three seams v2 breaks (all fenced behind `core/adk`)

| v2 change | omnis lives behind |
|---|---|
| context types merged into `agent.Context` | `adk.ToolContext` / `adk.CallbackContext` / `adk.ReadonlyContext` / `adk.InvocationContext` |
| `session.NewEvent` takes a `ctx` | `adk.NewEvent(ctx, id)` |
| run termination reworked (node runtime) | `adk.EndTurnAfterToolCall(ctx)` + the canary `core/adk.TestSkipSummarizationImpliesFinalResponse` |

## Surface tracker

Run `make adk-v2-status`. Its "seams outside core/adk" line is not a separate
hand-grepped count — it **runs `go test ./internal/adkguard/
-run TestNoRawADKSeamsOutsideFacade`** and reports that test's own PASS/FAIL, so
the dashboard can never drift from the guard. This matters because the guard's
`rawADK` matcher is **alias-agnostic** (`\b(\w+)\.(ToolContext|CallbackContext|
ReadonlyContext|InvocationContext)\b`, `\b(\w+)\.NewEvent(WithContext)?\(`,
skipping only `adk.`-prefixed matches — our own façade): it catches a seam
introduced under *any* import alias (e.g. `adksession.NewEvent(...)`, or a
`CallbackContext` reached via an aliased `adk/agent` import), not just the
default package names. Definition of ready = `make adk-v2-status` shows
"seams outside core/adk: 0" (equivalently, `go test ./internal/adkguard/`
passes) AND `core/adk` compiles/tests green against the target ADK version.

- Baseline (2026-07-21, on v1.5): seams outside core/adk **0** (guard passes);
  direct ADK imports (stable types, expected non-zero) **100 files**.

## Manual v2 spike procedure (run occasionally; do NOT commit the result)

```bash
git worktree add /tmp/omnis-v2-spike HEAD
cd /tmp/omnis-v2-spike
go get google.golang.org/adk/v2@latest
grep -rl '"google.golang.org/adk/' --include='*.go' core/adk | \
  xargs sed -i 's#"google.golang.org/adk/#"google.golang.org/adk/v2/#g'   # façade only
# In core/adk/adk.go: change the four alias RHS to agent.Context and
# NewEvent's body to session.NewEvent(ctx, invocationID).
go build ./... 2>&1 | tee /tmp/v2-build.log | grep -c '^'   # count of remaining errors
cd - && git worktree remove /tmp/omnis-v2-spike --force
```
The remaining errors after fixing `core/adk` are the true migration cost. Record
the count + a one-line summary here each time you spike.

## When we migrate (checklist)

- [ ] Bump `go.mod` to `google.golang.org/adk/v2` (+ `go mod tidy`).
- [ ] In `core/adk/adk.go`: alias RHS -> `agent.Context`; `NewEvent` body -> `session.NewEvent(ctx, id)`.
- [ ] `go build ./...`; fix any stable-type API drift the spike surfaced.
- [ ] `go test ./core/adk/` — the termination canary MUST pass (if not, redesign termination before proceeding).
- [ ] `make test` green; live-gateway smoke (see the v1.5 upgrade plan for the recipe).
- [ ] Delete this doc, the `adk-v2-status` target + script, `internal/adkguard`, and the CLAUDE.md "ADK v2 transition" section — the scaffolding has done its job.
