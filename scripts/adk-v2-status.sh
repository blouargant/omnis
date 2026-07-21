#!/usr/bin/env bash
# ADK v2 migration dashboard: current ADK pin, latest published adk/v2, and the
# size of the surface still naming ADK's churny seams outside core/adk (i.e. the
# migration cost + a divergence tripwire). See docs/adk-v2-readiness.md.
set -euo pipefail
cd "$(dirname "$0")/.."

echo "ADK pinned (go.mod):   $(grep -E 'google.golang.org/adk ' go.mod | awk '{print $2}')"
echo "Latest adk/v2:         $(go list -m -versions google.golang.org/adk/v2 2>/dev/null | tr ' ' '\n' | tail -1 || echo '(offline)')"
echo
echo "Surface outside core/adk (aliased seams should read 0):"

# Excludes core/adk (the façade itself), where these imports/seam types are
# expected and allowed.
EXCLUDE='(^|/)core/adk/'
printf '  direct ADK imports:  %s file(s)\n' "$(grep -rlE '\"google\.golang\.org/adk' --include='*.go' . | grep -vE "$EXCLUDE" | wc -l | tr -d ' ')"

# seams outside core/adk: DERIVED FROM internal/adkguard's guard test, not a
# hand-maintained parallel regex here. That guard is the single authoritative,
# alias-agnostic detector: its rawADK matcher is
# `\b(\w+)\.(ToolContext|CallbackContext|ReadonlyContext|InvocationContext)\b`
# and `\b(\w+)\.NewEvent(WithContext)?\(` (skipping any `adk.`-prefixed match,
# our own façade) — so it catches a seam introduced under ANY import alias
# (e.g. `adksession.NewEvent(...)`, or a `CallbackContext` reached via an
# aliased `adk/agent` import), not just the default names. A closed-qualifier
# grep here would silently miss those and could report a false "0" while the
# guard correctly fails the build — so this line reruns that exact test
# instead of re-implementing its regex, and can never drift from it.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
if go test ./internal/adkguard/ -run TestNoRawADKSeamsOutsideFacade >"$tmp" 2>&1; then
  echo "  seams outside core/adk:  0  (internal/adkguard guard passes — authoritative)"
else
  # Offender lines are the Fatalf message's continuation lines
  # ("<rel/path>.go:<line>: <source>"); the single "guard_test.go:<line>:"
  # line above them is go test's own call-site location, not an offender —
  # exclude it by name so it's never miscounted as a seam.
  n="$(grep -E '\.go:[0-9]+:' "$tmp" | grep -v 'guard_test\.go:' | wc -l | tr -d ' ')"
  echo "  seams outside core/adk:  ${n} — GUARD FAILS; run: go test ./internal/adkguard/ -v"
fi
