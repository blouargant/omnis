#!/usr/bin/env bash
# E2E smoke test for the soft-skills curator.
#
# 1) Builds the binary.
# 2) Synthesises a realistic-looking audit + statelog for a fake session.
# 3) Runs `yoke curate --audit ... --statelog ...`.
# 4) Prints any new files under softskills/.
#
# Requires .env to be configured for whatever LLM provider you use.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [[ -f .env ]]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
fi

if [[ -z "${YOKE_PROVIDER:-}" ]]; then
  echo "YOKE_PROVIDER is not set (.env should define it)" >&2
  exit 1
fi

echo "==> Building yoke"
go build -o bin/yoke ./

KEY="softskills_e2e_$(date +%s)"
AUDIT=".agent_memory_${KEY}.md"
STATE=".agent_statelog_${KEY}.json"

cleanup() {
  rm -f "$AUDIT" "$STATE" ".agent_curate_${KEY}.txt"
}
trap cleanup EXIT

echo "==> Writing synthetic session inputs ($AUDIT, $STATE)"
cat > "$AUDIT" <<'EOF'
# Session audit (synthetic)

## Goal
Diagnose a Postgres connection-pool exhaustion in production.

## Key actions
- Read application logs; spotted `FATAL: sorry, too many clients already`.
- Inspected pgbouncer stats: `default_pool_size=20`, all in use.
- Identified one report worker holding 18 connections after a long-running
  analytical query; refactored worker to release the connection between
  pages instead of keeping it across the whole job.
- Verified `idle_in_transaction` count dropped to <5 within 10 minutes.

## Lesson
When pgbouncer reports "too many clients", the bottleneck is usually a
single offender holding connections, not raw pool size. Checking
`pg_stat_activity` ordered by query duration reliably finds it before
raising the pool limit (which only delays the next outage).
EOF

cat > "$STATE" <<'EOF'
{
  "version": 1,
  "session": {"user_id": "e2e", "session_id": "softskills"},
  "tool_calls": [
    {"tool": "kubectl", "args": "logs deploy/api -n prod --tail=500"},
    {"tool": "psql",    "args": "-c 'select * from pg_stat_activity order by query_start'"},
    {"tool": "edit",    "args": "{\"path\":\"workers/report.py\"}"}
  ],
  "outcome": "fixed"
}
EOF

echo "==> Running curator"
./bin/yoke curate \
  --audit "$AUDIT" \
  --statelog "$STATE" \
  --softskills softskills

echo "==> softskills/ tree:"
find softskills -maxdepth 2 -type f | sort
