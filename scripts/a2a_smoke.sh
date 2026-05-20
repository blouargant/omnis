#!/usr/bin/env bash
# Smoke-test an A2A endpoint: sends one tasks/send and asserts the remote
# returns state=completed with a non-empty artifact text.
#
# Usage:
#   scripts/a2a_smoke.sh                          # uses A2A_URL or default
#   A2A_URL=http://host:8091/ scripts/a2a_smoke.sh
#   A2A_URL=... A2A_TOKEN=... scripts/a2a_smoke.sh
#
# Env:
#   A2A_URL      endpoint base URL (default http://127.0.0.1:8091/)
#   A2A_TOKEN    optional Bearer token forwarded as Authorization
#   A2A_PROMPT   prompt to send (default: "reply with the literal token PONG")

set -euo pipefail

URL="${A2A_URL:-http://127.0.0.1:8091/}"
TOK="${A2A_TOKEN:-}"
PROMPT="${A2A_PROMPT:-reply with the literal token PONG}"

command -v curl >/dev/null 2>&1 || { echo "a2a-smoke: curl not found in PATH" >&2; exit 1; }
command -v jq   >/dev/null 2>&1 || { echo "a2a-smoke: jq not found in PATH"   >&2; exit 1; }

echo ">> tasks/send → $URL"

args=(-sS --max-time 120 -H 'Content-Type: application/json')
if [ -n "$TOK" ]; then
  args+=(-H "Authorization: Bearer $TOK")
fi

body=$(jq -n --arg p "$PROMPT" '{
  jsonrpc: "2.0",
  id: "smoke",
  method: "tasks/send",
  params: {
    id: "smoke",
    message: { role: "user", parts: [{ type: "text", text: $p }] }
  }
}')

if ! resp=$(curl "${args[@]}" -X POST "$URL" -d "$body"); then
  echo "  FAIL: curl could not reach $URL — is the A2A endpoint up?" >&2
  exit 1
fi

state=$(echo "$resp" | jq -r '.result.status.state // "unknown"')
text=$(echo "$resp"  | jq -r '.result.artifacts[0].parts[0].text // ""')
rpcerr=$(echo "$resp" | jq -r '.error.message // empty')

echo "  state=$state"
echo "  text=$(echo "$text" | tr '\n' ' ' | cut -c1-200)"

if [ -n "$rpcerr" ]; then
  echo "  FAIL: JSON-RPC error: $rpcerr" >&2
  exit 1
fi
if [ "$state" != "completed" ]; then
  echo "  FAIL: state should be 'completed' (got '$state')" >&2
  exit 1
fi
if [ -z "$text" ]; then
  echo "  FAIL: empty response text" >&2
  exit 1
fi
echo "  OK"
