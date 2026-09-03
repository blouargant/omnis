#!/usr/bin/env python3
"""PreToolUse hook: refuse a Kubernetes mutation that has not been validated.

Reads the omnis hook input on stdin and writes the Claude Code hook output
protocol on stdout. All Kubernetes policy lives here, in configuration, so the
Go core stays domain-free (see the design contract in CLAUDE.md).

Three rules govern everything below:

1. FAIL CLOSED, and fail closed on IDENTIFICATION too. This script executes
   commands, so it must never risk executing the mutation itself: it validates a
   segment only when it can fully re-tokenise it and replay it as argv with no
   shell. But the subtler rule is that when a segment LOOKS like a kubectl/helm
   invocation and we cannot identify its verb, that is also a refusal — never a
   pass. Guessing "probably harmless" is how an unvalidated `helm uninstall`
   slips through.
2. Do not refuse what is not ours. A guard that fires on honest work gets
   disabled by its users, so the refusal gates key on "the first real word of
   this segment is kubectl/helm", never on "this text contains the word
   kubectl". `grep -rn kubectl deploy.sh > out` is not a cluster mutation.
3. The engine reports `attempt` / `consecutive`; this script decides what they
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

# helm uses an ALLOWLIST of read-only verbs, symmetric with kubectl above, so an
# unknown helm verb fails closed. A denylist of change verbs would let any future
# mutating verb through by default, and combined with a verb misparse that is how
# an unvalidated `helm uninstall` reaches the cluster.
HELM_READ_ONLY_VERBS = {
    "list", "status", "get", "history", "show", "search", "template", "lint",
    "version", "env", "repo", "dependency", "plugin", "verify", "diff",
}

WRAPPERS = {"sudo", "env", "time", "nice", "ionice", "nohup", "command", "exec",
            "timeout", "stdbuf"}

# Global flags that take a SEPARATE value, so the token after them is not the verb.
# Every other flag is treated as boolean. Inferring this instead from "the next
# token is not a flag" is what made `kubectl -A get pods` parse its verb as `pods`
# (denying an ordinary read-only command) and `helm --debug uninstall r` parse as
# `r` (letting a destructive command through unvalidated).
VALUE_FLAGS = {
    "-n", "--namespace", "--context", "--kube-context", "--kubeconfig",
    "--cluster", "--user", "--as", "--as-group", "--token", "--server", "-s",
    "--request-timeout", "--cache-dir", "--tls-server-name",
    "--client-certificate", "--client-key", "--certificate-authority",
    "-v", "--v", "--log-flush-frequency", "--profile", "--profile-output",
}

# Mirrors compoundOps in core/permissions/match_bash.go, longest-first so `&&` is
# not read as two `&`. The parity test in core/permissions pins the two lists.
COMPOUND_OPS = ("&&", "||", "|&", ";", "\n", "|", "&")

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
    """No opinion: the tool call continues to the permission layer.

    A note is surfaced to the user through the hook protocol's systemMessage,
    which is how the validated diff reaches the permission card.
    """
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


def _is_redirect_amp(command, i):
    """True when the `&` at i belongs to a redirect (2>&1, >&2, &>file) rather
    than backgrounding a command. Mirrors isRedirectAmp in match_bash.go: the
    nearest non-blank neighbour on either side being `>` makes it a redirect."""
    j = i - 1
    while j >= 0 and command[j] in " \t":
        j -= 1
    if j >= 0 and command[j] == ">":
        return True
    k = i + 1
    while k < len(command) and command[k] in " \t":
        k += 1
    return k < len(command) and command[k] == ">"


def segments(command):
    """Split a shell command line into independently-classified segments.

    Mirrors splitCompound in core/permissions/match_bash.go, including its quote
    awareness and its redirect carve-out for `&`. A regex cannot do this: it
    would split inside a quoted argument (turning `note="a && b"` into two
    unbalanced halves) and it would miss `\\n` and `&`, so a mutation on the
    second line of a multi-line command would never be examined. Like the Go
    side, backslash escapes are NOT interpreted.
    """
    out, buf = [], []
    quote = ""
    i, n = 0, len(command)
    while i < n:
        c = command[i]
        if quote:
            buf.append(c)
            if c == quote:
                quote = ""
            i += 1
            continue
        if c in ("'", '"'):
            quote = c
            buf.append(c)
            i += 1
            continue
        matched = ""
        for op in COMPOUND_OPS:
            if command.startswith(op, i):
                matched = op
                break
        if matched == "&" and _is_redirect_amp(command, i):
            buf.append(c)
            i += 1
            continue
        if matched:
            out.append("".join(buf))
            buf = []
            i += len(matched)
            continue
        buf.append(c)
        i += 1
    out.append("".join(buf))
    return [s.strip() for s in out if s.strip()]


def looks_like_k8s_invocation(segment):
    """True when this segment plausibly invokes kubectl or helm.

    Deliberately NOT a substring test on the text: `grep -rn kubectl deploy.sh`
    merely mentions the word and must not be refused. It walks leading wrappers
    and their flags to find the real binary; if a wrapper was stripped and the
    binary still cannot be identified (`sudo -u root kubectl …`), it returns True
    so the caller fails closed rather than guessing.
    """
    stripped_wrapper = False
    for word in segment.split():
        base = word.split("/")[-1]
        if base in ("kubectl", "helm"):
            return True
        if base in WRAPPERS:
            stripped_wrapper = True
            continue
        if word.startswith("-") or ("=" in word and not word.startswith("-")):
            continue
        return stripped_wrapper
    return stripped_wrapper


def _strip_wrappers(argv):
    """Drop leading process wrappers, their own flags, and env assignments.

    Basenames each token so /usr/bin/sudo is recognised (classify basenames too),
    and drops a stripped wrapper's flags so `sudo -n kubectl delete` does not
    leave `-n` as the apparent binary.
    """
    while argv:
        head = argv[0]
        base = head.split("/")[-1]
        if base in WRAPPERS:
            argv = argv[1:]
            while argv and argv[0].startswith("-"):
                argv = argv[1:]
            continue
        if "=" in head and not head.startswith("-"):
            argv = argv[1:]
            continue
        break
    return argv


def tokenise(segment):
    """Return the segment's argv with wrappers stripped, or None if unsafe.

    None means "this shape cannot be validated". For a segment that looks like a
    kubectl/helm MUTATION that is a refusal, never a pass — a shape we cannot read
    is a shape we cannot check.
    """
    if UNSAFE_SHELL.search(segment):
        return None
    try:
        argv = shlex.split(segment)
    except ValueError:
        return None
    return _strip_wrappers(argv) or None


def raw_classify(segment):
    """Best-effort (tool, verb) from the raw text, skipping shlex.

    Used for one purpose: to spare a READ-ONLY command whose shape we refuse to
    tokenise. A redirect on `kubectl get pods -o json > out` is harmless, and
    refusing it would be exactly the false positive that gets a guard disabled by
    its users. Never used to permit a mutation — an unrecognised or mutating verb
    still falls through to the refusal, and `segments` has already split the line,
    so an `apply` later in the command line is its own segment.
    """
    argv = _strip_wrappers(segment.split())
    return classify(argv) if argv else (None, None)


def classify(argv):
    """Return (tool, verb) for a kubectl/helm invocation, else (None, None).

    Global flags may precede the verb. Only flags known to take a SEPARATE value
    consume the following token; every other flag is boolean, so a bare global
    like -A or --debug cannot swallow the verb.
    """
    if not argv:
        return None, None
    binary = argv[0].split("/")[-1]
    if binary not in ("kubectl", "helm"):
        return None, None
    i = 1
    while i < len(argv):
        tok = argv[i]
        if not tok.startswith("-"):
            return binary, tok
        if "=" in tok:
            i += 1
            continue
        if tok in VALUE_FLAGS and i + 1 < len(argv):
            i += 2
            continue
        i += 1
    return binary, None


def is_read_only(tool, verb):
    return (tool == "kubectl" and verb in READ_ONLY_VERBS) or \
           (tool == "helm" and verb in HELM_READ_ONLY_VERBS)


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
    # not mentioning kubectl or helm must cost only interpreter startup. This is
    # a substring test by design: an alias (`k delete …`) or an indirection
    # (`$KUBECTL delete …`) is a known, accepted blind spot of the cheap path.
    if "kubectl" not in command and "helm" not in command:
        sys.exit(0)

    agent = data.get("agent_name") or ""
    cwd = data.get("cwd") or None
    try:
        attempt = int(data.get("attempt") or 1)
        consecutive = int(data.get("consecutive") or 0)
    except (TypeError, ValueError):
        attempt, consecutive = 1, 0
    attestations = data.get("attestations") or {}

    for segment in segments(command):
        argv = tokenise(segment)
        if argv is None:
            if looks_like_k8s_invocation(segment):
                rtool, rverb = raw_classify(segment)
                if rtool and rverb and is_read_only(rtool, rverb):
                    # A redirect on a read is not this guard's business.
                    continue
                refuse(
                    "This command shape cannot be validated (it uses a redirection, heredoc "
                    "or substitution), so it is refused rather than applied unchecked. Write "
                    "the change to a manifest file and apply that file instead.",
                    attempt, consecutive,
                )
            continue
        tool, verb = classify(argv)
        if tool is None or verb is None:
            # Fail closed on identification: a segment that looks like a
            # kubectl/helm invocation whose verb we cannot read is refused, not
            # waved through.
            if looks_like_k8s_invocation(segment):
                refuse(
                    "This looks like a kubectl/helm command but its verb could not be "
                    "identified, so it cannot be validated. Re-run it without process "
                    "wrappers (sudo/env/time) and with the verb immediately after the binary.",
                    attempt, consecutive,
                )
            continue
        if is_read_only(tool, verb):
            continue
        validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations)

    proceed()


def validate(tool, verb, argv, agent, cwd, attempt, consecutive, attestations):
    """Validate one mutating segment. Filled in by Task 9."""
    refuse("Validation for `%s %s` is not implemented yet." % (tool, verb), attempt, consecutive)


if __name__ == "__main__":
    main()
