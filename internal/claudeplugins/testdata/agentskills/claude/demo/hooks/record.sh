#!/bin/sh
# Test hook: appends what it was given to $HOOK_RECORD, and answers with
# $HOOK_DECISION (allow|ask|deny) when that is set.
{
  cat
  echo
  echo "CLAUDE_PLUGIN_ROOT=$CLAUDE_PLUGIN_ROOT"
  echo "IAPCLI_TOKEN=${IAPCLI_TOKEN:-unset}"
  echo "---"
} >> "$HOOK_RECORD"
if [ -n "$HOOK_DECISION" ]; then
  printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"%s"}}\n' "$HOOK_DECISION"
fi
exit 0
