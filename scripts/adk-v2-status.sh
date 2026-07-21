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
# Excludes core/adk (the façade itself) and internal/adkguard/guard_test.go
# (the boundary test's own regex source literally contains these seam
# strings as pattern text, e.g. ".SkipSummarization\b" — a false positive,
# not a real usage; the guard test allowlists itself for the same reason).
EXCLUDE='(^|/)core/adk/|(^|/)internal/adkguard/guard_test\.go'
printf '  raw context types:   %s\n' "$(grep -rE '\btool\.Context\b|\b(agent|adkagent)\.(ToolContext|CallbackContext|ReadonlyContext|InvocationContext)\b' --include='*.go' . | grep -vE "$EXCLUDE" | wc -l | tr -d ' ')"
printf '  SkipSummarization:   %s\n' "$(grep -rE '\.SkipSummarization\b' --include='*.go' . | grep -vE "$EXCLUDE" | wc -l | tr -d ' ')"
printf '  session.NewEvent:    %s\n' "$(grep -rE '\bsession\.NewEvent' --include='*.go' . | grep -vE "$EXCLUDE" | wc -l | tr -d ' ')"
printf '  direct ADK imports:  %s file(s)\n' "$(grep -rlE '\"google\.golang\.org/adk' --include='*.go' . | grep -vE "$EXCLUDE" | wc -l | tr -d ' ')"
