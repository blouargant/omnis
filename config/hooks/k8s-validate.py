#!/usr/bin/env python3
"""PreToolUse hook: refuse a Kubernetes mutation that has not been validated.

Reads the omnis hook input on stdin and writes the Claude Code hook output
protocol on stdout. All Kubernetes policy lives here, in configuration, so the
Go core stays domain-free (see the design contract in CLAUDE.md).

Two rules govern everything below:

1. FAIL CLOSED. This script executes commands, so it must never risk executing
   the mutation itself. It validates a segment only when it can fully
   re-tokenise it and replay it as argv with no shell. Anything carrying a
   redirection, heredoc, substitution or interpolation is refused.
2. The engine reports `attempt` / `consecutive`; this script decides what they
   mean. That is what keeps the engine generic.

Python 3 standard library only.
"""

import json
import re
import shlex
import subprocess
import sys

MAX_ATTEMPTS = 3          # matches agentCapGraceCalls in agent/budget_plugin.go
VALIDATE_TIMEOUT = 45     # seconds; below the hook's own timeout so we can explain

READ_ONLY_VERBS = {
    "get", "describe", "logs", "top", "explain", "events", "api-resources",
    "api-versions", "cluster-info", "version", "auth", "config", "diff", "wait",
}

APPLY_VERBS = {"apply", "create", "replace"}
IMPERATIVE_VERBS = {"patch", "set", "scale", "annotate", "label", "expose", "autoscale", "rollout"}
DESTRUCTIVE_VERBS = {"delete", "drain", "cordon", "uncordon", "taint"}
HELM_CHANGE_VERBS = {"install", "upgrade"}
HELM_DESTRUCTIVE_VERBS = {"uninstall", "rollback"}

WRAPPERS = {"sudo", "env", "time", "nice", "ionice", "nohup", "command", "exec"}

# Shapes we refuse to reason about rather than risk mis-parsing.
UNSAFE_SHELL = re.compile(r"<<|>>|[<>]|\$\(|`|\$\{")


def emit(decision, reason):
    """Write one PreToolUse decision and exit."""
    json.dump({
        "hookSpecificOutput": {
            "hookEventName": "PreToolUse",
            "permissionDecision": decision,
            "permissionDecisionReason": reason,
        }
    }, sys.stdout)
    sys.stdout.write("\n")
    sys.exit(0)


def proceed(note=""):
    """No opinion: the tool call continues to the permission layer."""
    if note:
        json.dump({"systemMessage": note}, sys.stdout)
        sys.stdout.write("\n")
    sys.exit(0)


def refuse(reason, attempt, consecutive):
    """Deny with a diagnostic, or escalate once the agent stops learning.

    The diagnostic is the point: it reaches the agent as
    "[BLOCKED BY HOOK] Bash: <reason>" so it can correct and retry. After
    MAX_ATTEMPTS on the same command — or that many consecutive refusals of
    different-but-still-wrong commands — the user decides instead.
    """
    if attempt >= MAX_ATTEMPTS or consecutive >= MAX_ATTEMPTS:
        emit("ask", "Validation has failed %d times.\n\n%s" % (max(attempt, consecutive), reason))
    emit("deny", reason)


def segments(command):
    """Split a shell command line into independently-classified segments.

    Mirrors splitCompound in core/permissions/match_bash.go: a mutation hidden
    behind `&&`, `||`, `;` or a pipe must still be seen.
    """
    parts = re.split(r"&&|\|\||[;|]", command)
    return [p.strip() for p in parts if p.strip()]


def tokenise(segment):
    """Return the segment's argv with wrappers stripped, or None if unsafe.

    None means "this shape cannot be validated" and is always a refusal — never
    a pass — because a shape we cannot read is a shape we cannot check.
    """
    if UNSAFE_SHELL.search(segment):
        return None
    try:
        argv = shlex.split(segment)
    except ValueError:
        return None
    while argv and (argv[0] in WRAPPERS or "=" in argv[0] and not argv[0].startswith("-")):
        argv = argv[1:]
    return argv or None


def classify(argv):
    """Return (tool, verb) for a kubectl/helm invocation, else (None, None).

    Global flags may precede the verb (`kubectl --context=x -n y apply`), so the
    verb is the first bare token after the binary.
    """
    if not argv:
        return None, None
    binary = argv[0].split("/")[-1]
    if binary not in ("kubectl", "helm"):
        return None, None
    i = 1
    while i < len(argv):
        tok = argv[i]
        if tok.startswith("-"):
            # A flag taking a separate value consumes the next token.
            if "=" not in tok and i + 1 < len(argv) and not argv[i + 1].startswith("-"):
                i += 2
                continue
            i += 1
            continue
        return binary, tok
    return binary, None


def main():
    try:
        data = json.load(sys.stdin)
    except (ValueError, OSError):
        # We cannot even read the request; with fail_closed the engine turns a
        # non-zero exit into a block, which is the correct direction.
        sys.stderr.write("k8s-validate: unreadable hook input\n")
        sys.exit(1)

    command = (data.get("tool_input") or {}).get("command") or ""

    # Fast path. This hook fires on EVERY Bash call in the fleet, so anything
    # not mentioning kubectl or helm must cost only interpreter startup.
    if "kubectl" not in command and "helm" not in command:
        sys.exit(0)

    agent = data.get("agent_name") or ""
    cwd = data.get("cwd") or None
    attempt = int(data.get("attempt") or 1)
    consecutive = int(data.get("consecutive") or 0)
    attestations = data.get("attestations") or {}

    for segment in segments(command):
        argv = tokenise(segment)
        if argv is None:
            if "kubectl" in segment or "helm" in segment:
                refuse(
                    "This command shape cannot be validated (it uses a redirection, heredoc or "
                    "substitution), so it is refused rather than applied unchecked. Write the "
                    "change to a manifest file and apply that file instead.",
                    attempt, consecutive,
                )
            continue
        tool, verb = classify(argv)
        if tool is None or verb is None:
            continue
        if tool == "kubectl" and verb in READ_ONLY_VERBS:
            continue
        if tool == "helm" and verb not in HELM_CHANGE_VERBS | HELM_DESTRUCTIVE_VERBS:
            continue
        validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations)

    proceed()


def validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations):
    """Validate one mutating segment. Filled in by Task 9."""
    refuse("Validation for `%s %s` is not implemented yet." % (tool, verb), attempt, consecutive)


if __name__ == "__main__":
    main()
