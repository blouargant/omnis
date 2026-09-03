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

# helm verbs that do not change a cluster, split in two because they are wrong for
# different reasons: a CLUSTER READ contacts the cluster and changes nothing; a
# LOCAL verb never touches the cluster at all (it edits ~/.config/helm, fetches a
# chart, renders a template). Both proceed — this guard exists for cluster
# changes, and Bash is permission-gated for everything else. `helm plugin install`
# fetching executable code is a real concern, but it is the permission layer's,
# and pretending otherwise here would also refuse `helm repo add`.
HELM_CLUSTER_READ_VERBS = {"list", "ls", "status", "get", "history", "diff", "test"}
HELM_LOCAL_VERBS = {
    "show", "search", "template", "lint", "version", "env", "repo", "plugin",
    "dependency", "dep", "pull", "package", "create", "completion", "registry",
    "verify", "docs",
}

# kubectl treats `auth` and `config` as verbs, but only SOME of their subcommands
# read: `kubectl auth reconcile -f rbac.yaml` writes RBAC, and `kubectl config
# use-context prod` rewrites the shared kubeconfig. The shipped
# config/permissions.json already enumerates the read-only ones in its own allow
# rule; this mirrors that list rather than inventing a second one.
READ_ONLY_SUBVERBS = {
    "auth": {"can-i"},
    "config": {"view", "current-context", "get-contexts", "get-clusters", "get-users"},
    # `rollout status` and `rollout history` read; undo/restart/pause/resume write.
    # Treating the whole verb as a mutation denied `kubectl rollout status`, which
    # is one of the commonest calls the triage playbook makes after an edit.
    "rollout": {"status", "history"},
    "plugin": {"list"},
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
    "--as-uid", "--username", "--password", "--vmodule",
    "--registry-config", "--repository-config", "--repository-cache",
    "--kube-apiserver", "--kube-token", "--kube-ca-file", "--kube-as-user",
    "--kube-as-group", "--kube-tls-server-name", "--qps", "--burst-limit",
}

# The full verb vocabulary of each tool. Used ONLY to decide whether a kubectl or
# helm token that is not the first word of a segment is a command or just an
# argument: `timeout 30 kubectl get pods` is an invocation, `grep -rn kubectl
# deploy.sh` is not. Missing an exotic verb behind a wrapper is the known residual
# — it leaves that one invocation unrecognised rather than mis-refusing honest work.
KUBECTL_VERBS = READ_ONLY_VERBS | APPLY_VERBS | IMPERATIVE_VERBS | DESTRUCTIVE_VERBS | {
    "edit", "run", "debug", "exec", "cp", "port-forward", "proxy", "attach",
    "kustomize", "completion", "certificate", "plugin", "wait",
}
HELM_VERBS = HELM_CLUSTER_READ_VERBS | HELM_LOCAL_VERBS | {
    "install", "upgrade", "uninstall", "rollback",
}

# Mirrors compoundOps in core/permissions/match_bash.go, longest-first so `&&` is
# not read as two `&`. The parity test in core/permissions pins the two lists.
COMPOUND_OPS = ("&&", "||", "|&", ";", "\n", "|", "&")

# Two kinds of shape we will not reason about, and the difference is load-bearing.
# A SUBSTITUTION hides an entire nested command — `kubectl get pods $(kubectl
# delete pod x)` runs a delete no amount of outer-verb inspection can see — so it
# is always refused. A plain REDIRECT hides nothing: the command is fully visible,
# we simply cannot replay it as argv, so a read-only command may proceed.
SUBSTITUTION = re.compile(r"\$\(|`|\$\{|<\(")
REDIRECT = re.compile(r"<<|>>|[<>]")


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
    kept = [s.strip() for s in out if s.strip()]
    # splitCompound returns the whole trimmed input when every fragment is blank;
    # mirror it so the parity test holds for "", ";;", "&&" and friends.
    return kept if kept else [command.strip()]


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
    if SUBSTITUTION.search(segment) or REDIRECT.search(segment):
        return None
    try:
        argv = shlex.split(segment)
    except ValueError:
        return None
    return _strip_wrappers(argv) or None


def _verb_from(argv, start):
    """Walk global flags from index `start`, returning (verb, verb_index).

    Only flags known to take a SEPARATE value consume the following token; every
    other flag is boolean, so a bare global like -A or --debug cannot swallow the
    verb.
    """
    i = start + 1
    while i < len(argv):
        tok = argv[i]
        if not tok.startswith("-"):
            return tok, i
        if "=" in tok:
            i += 1
            continue
        if tok in VALUE_FLAGS and i + 1 < len(argv):
            i += 2
            continue
        i += 1
    return None, -1


def find_invocation(argv):
    """Return (index, tool) of the kubectl/helm invocation in argv, else (-1, None).

    ONE rule replaces the three overlapping heuristics that preceded it, each of
    which was wrong in a different direction. A token is an invocation when it
    basenames to kubectl or helm AND either it is the first word of the segment —
    so any verb, even an unknown one, is ours and fails closed — or the verb
    following it belongs to that tool's vocabulary.

    The second clause is what separates `timeout 30 kubectl get pods` and `xargs
    kubectl delete pod x`, both invocations, from `grep -rn kubectl deploy.sh`,
    which merely names the word. It needs no wrapper grammar, which is precisely
    why it replaced the wrapper-stripping guesswork: that denied `timeout 30
    kubectl get pods`, and denied `sudo -u root apt-get install foo-overwhelming`
    because "helm" hides inside "overwhelming".
    """
    for i, tok in enumerate(argv):
        base = tok.split("/")[-1]
        if base not in ("kubectl", "helm"):
            continue
        if i == 0:
            return i, base
        verb, _ = _verb_from(argv, i)
        vocab = KUBECTL_VERBS if base == "kubectl" else HELM_VERBS
        if verb in vocab:
            return i, base
    return -1, None


def classify(argv):
    """Return (tool, verb, verb_index), or (None, None, -1) if this is not ours.

    The index is returned because the validators need the operands AFTER the verb,
    and a fixed slice like argv[2:] is wrong the moment a global flag precedes it:
    for `helm --debug uninstall myrel`, argv[2] is "uninstall".
    """
    idx, tool = find_invocation(argv)
    if tool is None:
        return None, None, -1
    verb, verb_idx = _verb_from(argv, idx)
    return tool, verb, verb_idx


def operands(argv, verb_idx):
    """Everything after the verb: the resource or release operands and their flags."""
    return argv[verb_idx + 1:] if verb_idx >= 0 else []


def is_read_only(tool, verb, ops):
    """True when this invocation cannot change a cluster.

    `auth` and `config` are verbs whose subcommands differ: `auth can-i` reads,
    `auth reconcile -f rbac.yaml` writes RBAC.
    """
    if tool == "helm":
        return verb in HELM_CLUSTER_READ_VERBS or verb in HELM_LOCAL_VERBS
    if verb in READ_ONLY_SUBVERBS:
        sub_verb = next((o for o in ops if not o.startswith("-")), None)
        return sub_verb in READ_ONLY_SUBVERBS[verb]
    return verb in READ_ONLY_VERBS


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
        try:
            words = shlex.split(segment)
        except ValueError:
            words = segment.split()
        _, tool = find_invocation(words) if words else (-1, None)
        if tool is None:
            # Not a kubectl/helm invocation, so not this guard's business. A
            # segment that merely NAMES the word lands here.
            continue

        # A substitution hides a whole nested command, so it is refused whatever
        # the outer verb is: `kubectl get pods $(kubectl delete pod x)` would
        # otherwise read as a harmless get while the delete executes.
        if SUBSTITUTION.search(segment):
            refuse(
                "This command substitutes another command inside itself, so what it would "
                "actually run cannot be inspected. Run the inner command on its own, or "
                "write the change to a manifest file and apply that file.",
                attempt, consecutive,
            )

        argv = tokenise(segment)
        if argv is None:
            # A plain redirect hides nothing, so spare it if it only reads.
            rtool, rverb, ridx = classify(words)
            if rtool and rverb and is_read_only(rtool, rverb, operands(words, ridx)):
                continue
            refuse(
                "This command shape cannot be validated (it uses a redirection or heredoc), "
                "so it is refused rather than applied unchecked. Write the change to a "
                "manifest file and apply that file instead.",
                attempt, consecutive,
            )
            continue

        tool, verb, verb_idx = classify(argv)
        if verb is None:
            # Fail closed on identification.
            refuse(
                "This looks like a kubectl/helm command but its verb could not be "
                "identified, so it cannot be validated. Re-run it with the verb "
                "immediately after the binary.",
                attempt, consecutive,
            )
            continue
        if is_read_only(tool, verb, operands(argv, verb_idx)):
            continue
        validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations)

    proceed()


def validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations):
    """Validate one mutating segment. Filled in by Task 9."""
    refuse("Validation for `%s %s` is not implemented yet." % (tool, verb), attempt, consecutive)


if __name__ == "__main__":
    main()
