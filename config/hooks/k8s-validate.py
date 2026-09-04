#!/usr/bin/env python3
"""PreToolUse hook: allow a Kubernetes command only when it is provably read-only.

Reads the omnis hook input on stdin and writes the Claude Code hook output
protocol on stdout. All Kubernetes policy lives here, in configuration, so the
Go core stays domain-free (see the design contract in CLAUDE.md).

THE RULE IS AN INVERSION, and it is the whole design. Earlier versions of this
script tried to RECOGNISE mutations and let everything else past. That problem is
unbounded: `bash -c "kubectl delete …"`, `eval`, `$(kubectl delete …)`,
`helm del`, `ssh host kubectl delete …` are each a legitimate spelling of the same
mutation, so every round of pattern-work closed the named cases and left the class
open. Three rounds of review demonstrated exactly that, by execution.

So this script does not decide whether a command mutates. It decides whether a
command is **provably read-only**, and refuses everything else that names kubectl
or helm. Proving a read requires all of: the segment tokenises cleanly; it carries
no command substitution; after stripping a known process wrapper by an explicit
spec, the binary IS kubectl or helm; and the verb (with its subcommand, where the
verb has one) is in that tool's read set. Anything else is refused with an
instruction to express it as a direct invocation.

That inverts the cost of being wrong. An unlisted read verb now costs friction —
one refusal, one rewrite, and a line added to a set — whereas an unlisted way of
spelling a mutation used to cost an unvalidated cluster change. The read sets are
enumerable; the ways to spell a mutation are not.

Python 3 standard library only.
"""

import json
import re
import shlex
import subprocess
import sys

MAX_ATTEMPTS = 3          # matches agentCapGraceCalls in agent/budget_plugin.go
VALIDATE_TIMEOUT = 45     # seconds; below the hook's own timeout so we can explain

# kubectl verbs that are provably reads: they report state and change none.
# port-forward and proxy open a local tunnel and change nothing in the cluster;
# they are standard investigator practice, so refusing them would be friction for
# no safety gain.
KUBECTL_READ_VERBS = {
    "get", "describe", "logs", "top", "explain", "events", "api-resources",
    "api-versions", "cluster-info", "version", "diff", "wait", "port-forward",
    "proxy", "options", "help",
}

# kubectl verbs that touch nothing outside this machine. `kustomize` renders,
# `completion` prints a shell script. Same doctrine as helm's local verbs: this
# guard exists for cluster changes, and Bash is permission-gated for the rest.
KUBECTL_LOCAL_VERBS = {"kustomize", "completion"}

# Verbs whose SUBCOMMANDS differ, so the verb alone proves nothing. `auth can-i`
# reads but `auth reconcile -f rbac.yaml` writes RBAC; `config view` reads but
# `config use-context` rewrites the shared kubeconfig; `rollout status` reads but
# `rollout undo` rolls a Deployment back. The read lists mirror what the shipped
# config/permissions.json already declares in its own allow rule, rather than
# inventing a second source of truth.
READ_ONLY_SUBVERBS = {
    "auth": {"can-i"},
    "config": {"view", "current-context", "get-contexts", "get-clusters", "get-users"},
    "rollout": {"status", "history"},
    "plugin": {"list"},
}

# helm verbs that contact the cluster and change nothing, INCLUDING the aliases.
# `hist` is history's alias. `test` is deliberately NOT here: it runs the chart's
# test hooks, which create Pods from chart-supplied specs.
HELM_READ_VERBS = {"list", "ls", "status", "get", "history", "hist", "diff"}

# helm verbs that never touch the cluster at all — they edit ~/.config/helm, fetch
# a chart, render a template. `inspect` aliases show, `fetch` aliases pull.
# `helm plugin install` fetching executable code is a real concern, but it is the
# permission layer's, and excluding it here would also refuse `helm repo add`.
HELM_LOCAL_VERBS = {
    "show", "inspect", "search", "template", "lint", "version", "env", "repo",
    "plugin", "dependency", "dep", "pull", "fetch", "package", "create",
    "completion", "registry", "verify", "docs", "help",
}

# Global flags that take a SEPARATE value, so the token after them is not the
# verb. Every other flag is treated as boolean: inferring this from "the next
# token is not a flag" made a bare -A swallow the verb.
VALUE_FLAGS = {
    "-n", "--namespace", "--context", "--kube-context", "--kubeconfig",
    "--cluster", "--user", "--as", "--as-group", "--as-uid", "--token",
    "--server", "-s", "--request-timeout", "--cache-dir", "--tls-server-name",
    "--client-certificate", "--client-key", "--certificate-authority",
    "-v", "--v", "--vmodule", "--username", "--password",
    "--log-flush-frequency", "--profile", "--profile-output",
    "--registry-config", "--repository-config", "--repository-cache",
    "--kube-apiserver", "--kube-token", "--kube-ca-file", "--kube-as-user",
    "--kube-as-group", "--kube-tls-server-name", "--qps", "--burst-limit",
}

# Process wrappers, each with an explicit spec, because guessing a wrapper's
# argument arity is what previously denied `timeout 30 kubectl get pods`: the
# bare operand `30` was left looking like the binary. value_flags are the
# wrapper's own flags that consume the next token; operands is how many bare
# tokens it takes before the command it wraps.
WRAPPER_SPEC = {
    "sudo": ({"-u", "--user", "-g", "--group", "-p", "--prompt", "-C",
              "--close-from", "-r", "--role", "-t", "--type", "-h", "--host"}, 0),
    "env": ({"-u", "--unset", "-C", "--chdir", "-S", "--split-string"}, 0),
    "timeout": ({"-s", "--signal", "-k", "--kill-after"}, 1),
    "nice": ({"-n", "--adjustment"}, 0),
    "ionice": ({"-c", "-n", "-p", "-P", "-u"}, 0),
    "stdbuf": ({"-i", "-o", "-e", "--input", "--output", "--error"}, 0),
    "xargs": ({"-I", "-i", "-n", "-L", "-P", "-d", "-E", "-s", "--replace",
               "--max-args", "--max-procs", "--delimiter"}, 0),
    "nohup": (set(), 0),
    "time": ({"-f", "--format", "-o", "--output"}, 0),
    "command": (set(), 0),
}

# Commands that do not execute their arguments, so a kubectl command line quoted
# or echoed as TEXT is not an invocation. Bounded on purpose: anything not listed
# is refused rather than assumed inert.
INERT_COMMANDS = {"echo", "printf", "cat", "grep", "egrep", "fgrep", "rg", "sed",
                  "awk", "head", "tail", "wc", "tee", "less", "more", "comm",
                  "diff", "git", "true", "false", "test"}

# Mirrors compoundOps# Mirrors compoundOps in core/permissions/match_bash.go, longest-first so `&&` is
# not read as two `&`. The parity test in core/permissions pins the two lists.
COMPOUND_OPS = ("&&", "||", "|&", ";", "\n", "|", "&")

# Two kinds of shape we will not reason about, and the difference is load-bearing.
# A SUBSTITUTION hides an entire nested command — `kubectl get pods $(kubectl
# delete pod x)` runs a delete no amount of outer-verb inspection can see — so it
# is always refused. A plain REDIRECT hides nothing: the command is fully visible,
# we simply cannot replay it as argv, so a read-only command may proceed.
# Only the forms that can EXECUTE a command. `${VAR}` is a plain expansion and
# hides nothing runnable — every bash ${x:-…} form that does run something also
# contains $( or a backtick, both matched here — so including it only denied the
# commonest idiom in agent-written shell.
MENTIONS_K8S = re.compile(r"\b(kubectl|helm)\b")

COMMAND_SUBSTITUTION = re.compile(r"\$\(|`|<\(|\$\{[^}]*\$\(")
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
    """Drop leading process wrappers using WRAPPER_SPEC, returning the remainder.

    Each wrapper is stripped by its explicit spec — its own value-taking flags and
    its declared count of bare operands — rather than by guessing. Guessing is what
    left `30` looking like the binary in `timeout 30 kubectl get pods` and denied
    an ordinary read. Nested wrappers are handled by looping.
    """
    while argv:
        base = argv[0].split("/")[-1]
        if base not in WRAPPER_SPEC:
            break
        value_flags, operand_count = WRAPPER_SPEC[base]
        argv = argv[1:]
        while argv and argv[0].startswith("-"):
            flag = argv[0]
            argv = argv[1:]
            if flag in value_flags and argv:
                argv = argv[1:]
        for _ in range(operand_count):
            if argv and not argv[0].startswith("-"):
                base_next = argv[0].split("/")[-1]
                if base_next in ("kubectl", "helm") or base_next in WRAPPER_SPEC:
                    break
                argv = argv[1:]
    return argv


def _verb_after(argv, start):
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


def classify(argv):
    """Return (tool, verb, verb_index) when argv is a DIRECT kubectl/helm
    invocation, else (None, None, -1).

    Direct means the binary is at the head. The caller strips wrappers ONCE and
    passes the result here, so every index this returns refers to the same argv the
    caller then hands to provably_read_only and operands — stripping again inside
    would return indices into a different list, which silently mis-resolved
    `timeout 60s kubectl rollout status` as a mutation.

    That strictness is the inversion: a command carried inside a quoted payload
    (`bash -c "kubectl delete …"`) or named as text (`echo kubectl delete …`) is
    deliberately NOT an invocation here, and the caller refuses or spares it by
    the rules in main rather than by trying to interpret shell.
    """
    if not argv:
        return None, None, -1
    tool = argv[0].split("/")[-1]
    if tool not in ("kubectl", "helm"):
        return None, None, -1
    verb, verb_idx = _verb_after(argv, 0)
    return tool, verb, verb_idx


def operands(argv, verb_idx):
    """Everything after the verb: the resource or release operands and their flags."""
    return argv[verb_idx + 1:] if verb_idx >= 0 else []


def subverb_of(argv, verb_idx):
    """The subcommand token after a verb like `auth` or `rollout`.

    Uses the same flag grammar as the verb walk, because a bare
    `next(o for o in ops if not o.startswith("-"))` reads a flag's VALUE as the
    subcommand — which both refused `kubectl auth -n demo can-i …` and, far worse,
    permitted `kubectl rollout -n status undo deploy/x` by reading "status".
    """
    sub, _ = _verb_after(argv, verb_idx)
    return sub


def provably_read_only(tool, verb, argv, verb_idx):
    """True only when this invocation is PROVEN to change no cluster state.

    Anything unproven is refused by the caller. That is the inversion: this
    function's sets are enumerable, whereas the set of ways to spell a mutation is
    not.
    """
    if verb is None:
        return False
    if tool == "helm":
        return verb in HELM_READ_VERBS or verb in HELM_LOCAL_VERBS
    if verb in READ_ONLY_SUBVERBS:
        return subverb_of(argv, verb_idx) in READ_ONLY_SUBVERBS[verb]
    return verb in KUBECTL_READ_VERBS or verb in KUBECTL_LOCAL_VERBS


def is_inert(argv):
    """True when argv's head is a command that does not execute its arguments, so
    a kubectl command line appearing in it is text rather than an invocation.

    Takes an already-stripped argv, like classify.
    """
    if not argv:
        return False
    return argv[0].split("/")[-1] in INERT_COMMANDS


def names_k8s(tokens):
    """True when these tokens actually name kubectl or helm, as opposed to merely
    containing the letters.

    Two shapes count. A token whose BASENAME is exactly kubectl or helm is the
    binary — this is what excludes `/opt/scripts/deploy-helm.sh`, where a hyphen is
    a word boundary so a naive \\b regex matched and refused an honest script. And
    a token that contains the word AND whitespace is a quoted payload
    (`bash -c "kubectl delete …"`), which must still be caught even though its
    basename is the whole string.
    """
    for t in tokens:
        if t.split("/")[-1] in ("kubectl", "helm"):
            return True
        if (" " in t or "\t" in t or "\n" in t) and MENTIONS_K8S.search(t):
            return True
    return False


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
    # Word boundaries, not substrings: "overwhelming" contains "helm" and
    # denying `apt-get install foo-overwhelming` is exactly the friction that
    # gets a guard disabled.
    if not MENTIONS_K8S.search(command):
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
        # A shell comment is text, not a command.
        if segment.lstrip().startswith("#"):
            continue

        # A command substitution hides a whole nested command from us, whatever
        # the outer word is. Checked BEFORE any identification, because gating it
        # behind "is the outer command kubectl?" is exactly what let
        # `echo $(kubectl delete pod x)` through.
        if COMMAND_SUBSTITUTION.search(segment) and MENTIONS_K8S.search(segment):
            refuse(
                "This command substitutes another command inside itself, so what it would "
                "actually run cannot be read. Run the inner command on its own line as a "
                "direct kubectl/helm invocation.",
                attempt, consecutive,
            )

        try:
            tokens = shlex.split(segment)
        except ValueError:
            if MENTIONS_K8S.search(segment):
                refuse(
                    "This command has an unbalanced quote, so it cannot be read. Rewrite it as "
                    "a direct kubectl/helm invocation.",
                    attempt, consecutive,
                )
            continue

        if not names_k8s(tokens):
            continue

        # Strip wrappers exactly ONCE, so every index below refers to this argv.
        argv = _strip_wrappers(tokens)

        if is_inert(argv):
            # echo/grep/git and friends do not execute their arguments.
            continue

        tool, verb, verb_idx = classify(argv)
        if tool is None:
            # Names kubectl or helm but is not a direct invocation of either:
            # a quoted payload (bash -c "…"), an eval, an ssh, an unknown
            # launcher. Unprovable, therefore refused.
            refuse(
                "This command names kubectl or helm but does not invoke it directly, so what "
                "it would run cannot be proven safe. Express it as a direct kubectl/helm "
                "command instead of wrapping it in a shell, an eval, or a remote call.",
                attempt, consecutive,
            )

        if provably_read_only(tool, verb, argv, verb_idx):
            continue

        if REDIRECT.search(segment):
            # Not provably a read, and not replayable as argv either.
            refuse(
                "This command changes the cluster and uses a redirection or heredoc, so it "
                "cannot be validated. Write the change to a manifest file and apply that file "
                "instead.",
                attempt, consecutive,
            )

        validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations)

    proceed()


def validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations):
    """Validate one mutating segment. Filled in by Task 9."""
    refuse("Validation for `%s %s` is not implemented yet." % (tool, verb), attempt, consecutive)


if __name__ == "__main__":
    main()
