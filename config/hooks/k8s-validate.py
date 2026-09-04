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
    "options", "help",
}
# `proxy` is deliberately NOT above. The process changes nothing, but it serves
# the whole API on localhost with the operator's credentials injected, and the
# follow-up `curl -X DELETE http://127.0.0.1:8001/…` names neither tool, so the
# guard never sees it. port-forward is materially weaker: no credential injection.

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

# The three kubectl verb families `validate` routes on. These are NOT about
# whether a verb is a read — provably_read_only already settled that, and
# anything reaching validate is already known to mutate. They exist purely to
# pick WHICH validator applies: a manifest-shaped change can be diffed and
# server-dry-run'd; an imperative one only supports a dry run; a destructive
# one needs the blast-radius/ownership pre-checks instead of either.
APPLY_VERBS = {"apply", "create", "replace"}
IMPERATIVE_VERBS = {"patch", "set", "scale", "annotate", "label", "expose", "autoscale", "rollout"}
DESTRUCTIVE_VERBS = {"delete", "drain", "cordon", "uncordon", "taint"}

# helm verbs that remove or roll back a release, as opposed to installing or
# upgrading one — `validate_helm` previews the latter and pre-checks the former
# against the release's own history instead of a chart diff.
HELM_DESTRUCTIVE_VERBS = {"uninstall", "rollback"}

# Global flags that take a SEPARATE value, so the token after them is not the
# verb. Every other flag is treated as boolean: inferring this from "the next
# token is not a flag" made a bare -A swallow the verb.
# Boolean global flags: recognised, consume nothing. Anything in neither this set
# nor VALUE_FLAGS makes the verb unprovable (see _verb_after).
BOOLEAN_FLAGS = {
    "-A", "--all-namespaces", "--insecure-skip-tls-verify", "--debug",
    "--warnings-as-errors", "--disable-compression", "--no-headers",
    "-h", "--help", "--version",
}

# A verb exists but could not be identified — distinct from None, which means no
# verb is present at all. Conflating the two turned an unrecognised flag into
# "nothing to validate" and let `kubectl --totally-unknown-flag zzz get pods`
# proceed: `kubectl --help` really is a read, but an unreadable verb is not.
UNPROVABLE = object()

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

# Leading shell syntax that precedes a command inside a segment. `segments` splits
# on ; and |, so `for ns in a b; do kubectl get pods -n $ns; done` yields a segment
# whose head is `do`. Refusing those denied five distinct shapes of an ordinary
# read — the investigator's normal register, not an exotic spelling — which is how
# a guard gets removed from hooks.json. Stripping them only helps FIND the
# invocation; the read-or-mutation decision afterwards is unchanged, so
# `if kubectl delete pod x; then` still reaches validate.
SHELL_KEYWORDS = {"for", "while", "until", "if", "then", "elif", "else", "do",
                  "done", "fi", "case", "esac", "in", "select", "function",
                  "(", ")", "{", "}", "!", "[[", "]]", "&&", "||"}
# NOTE: `time` is deliberately NOT above, though it is a shell keyword. This set is
# consulted BEFORE WRAPPER_SPEC, so listing it here made its WRAPPER_SPEC entry
# unreachable dead code that read as coverage: `time` was stripped without
# consuming its own `-p`/`-o FILE`, the leftover flag became the head, classify
# returned None, and `time -p kubectl delete pod x` proceeded AND ran.

# Process wrappers, each with an explicit spec, because guessing a wrapper's
# argument arity is what previously denied `timeout 30 kubectl get pods`: the
# bare operand `30` was left looking like the binary. value_flags are the
# wrapper's own flags that consume the next token; operands is how many bare
# tokens it takes before the command it wraps.
WRAPPER_SPEC = {
    "sudo": ({"-u", "--user", "-g", "--group", "-p", "--prompt", "-C",
              "--close-from", "-r", "--role", "-t", "--type", "-h", "--host",
              "-R", "--chroot", "-D", "--chdir", "-a", "--auth-type"}, 0),
    "env": ({"-u", "--unset", "-C", "--chdir", "-S", "--split-string"}, 0),
    "timeout": ({"-s", "--signal", "-k", "--kill-after"}, 1),
    "nice": ({"-n", "--adjustment"}, 0),
    "ionice": ({"-c", "-n", "-p", "-P", "-u"}, 0),
    "stdbuf": ({"-i", "-o", "-e", "--input", "--output", "--error"}, 0),
    "xargs": ({"-I", "-i", "-n", "-L", "-P", "-d", "-E", "-s", "--replace",
               "--max-args", "--max-procs", "--delimiter",
               "-a", "--arg-file", "--process-slot-var"}, 0),
    "nohup": (set(), 0),
    "time": ({"-f", "--format", "-o", "--output"}, 0),
    "command": (set(), 0),
    # These exec argv[1..] directly, so the real verb is in plain sight and a
    # missing entry meant the segment was spared, not scrutinised: `setsid
    # kubectl delete pod x` proceeded and deleted. The open-ended tail (an
    # arbitrary unlisted wrapper) is undecidable — `setsid kubectl delete` is
    # shape-identical to `echo kubectl delete` — so this enumerates the members
    # actually present on a workstation rather than pretending to be complete.
    "setsid": (set(), 0),
    "unshare": ({"--map-user", "--map-group", "--propagation", "--setgroups",
                 "-S", "--setuid", "-G", "--setgid"}, 0),
    "taskset": ({"-c", "--cpu-list", "-p", "--pid"}, 1),
    "chrt": (set(), 1),
    "strace": ({"-o", "-e", "-p", "-s", "-E", "-P"}, 0),
    "ltrace": ({"-o", "-e", "-p", "-s"}, 0),
    "systemd-run": ({"--unit", "-p", "--property", "--slice", "--description",
                     "--uid", "--gid", "--setenv", "-E"}, 0),
    "doas": ({"-u", "-C"}, 0),
    # flock execs argv after its lockfile operand, so as a LAUNCHER it refused an
    # honest `flock /tmp/l kubectl get pods`. Its `-c` form DOES hand a string to
    # sh, and that is caught by the post-strip head check in main: the payload is
    # left at the head, which is not a bare program name.
    "flock": ({"-w", "--wait", "--timeout", "-E", "--conflict-exit-code"}, 1),
}

# Commands that EXECUTE a string handed to them. A kubectl command line quoted
# inside one of these is a real invocation the guard cannot read, so it is refused.
# This replaced an "inert commands" list, which was the wrong property to
# enumerate: `awk`, `sed` and `git` were on it, and all three execute their
# arguments (`awk 'BEGIN{system(…)}'`, GNU `sed '1e …'`, `git -c alias.x='!…'`),
# so listing them as inert made three proven bypasses.
LAUNCHERS = {
    "bash", "sh", "zsh", "dash", "ksh", "csh", "fish", "eval", "source",
    "ssh", "su", "nsenter", "chroot", "docker", "podman", "nerdctl",
    "python", "python3", "perl", "ruby", "node", "php",
    "awk", "gawk", "sed", "make", "watch", "parallel", "busybox",
}
# NOTE: `xargs` is a WRAPPER, not a launcher — after stripping it, kubectl is at
# the head, so `xargs kubectl delete pod x` reaches validate with the real verb
# instead of a generic refusal. `xargs sh -c '…'` still refuses, because the
# stripped head is then `sh`. `watch` is the opposite case and IS a launcher: it
# joins its argv and runs it through sh, so treating it as a wrapper would let
# `watch "kubectl delete pod x"` past. That costs friction on `watch -n 2 kubectl
# get pods`, which is one line to move if it proves too expensive.

# Mirrors compoundOps in core/permissions/match_bash.go, longest-first so `&&` is
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
# A suffixed binary name is an ordinary side-by-side install (`helm3`,
# `kubectl.real`, `kubectl1.29`), and a trailing \b made the whole guard exit on
# it. Widening the tail keeps the reason the \b was added — there is no word
# boundary before the "helm" inside "overwhelming", so that still does not match.
MENTIONS_K8S = re.compile(r"\b(kubectl|helm)[\w.-]*")
K8S_BINARY = re.compile(r"^(kubectl|helm)[\w.-]*$")

COMMAND_SUBSTITUTION = re.compile(r"\$\(|`|<\(|>\(|\$\{[^}]*\$\(")
REDIRECT = re.compile(r"<<|>>|[<>]")
# Shell grouping punctuation, which may be glued to a program name on either
# side or in the middle of the token. See _basename.
GROUPING = re.compile(r"[(){}]")


def _basename(token):
    """The program name a token denotes, with shell grouping punctuation removed.

    `(` may be glued to the binary — `(kubectl delete pod x)` is ordinary bash —
    and the token is then `(kubectl`, whose ^-anchored basename test failed and
    whose lack of whitespace failed the quoted-payload test, so the segment
    proceeded and deleted. SHELL_KEYWORDS' `(` entry only ever fired on the
    spaced form, which is why the table's `( kubectl get pods )` row was green
    while the glued form was never generated.

    Splitting rather than trimming is load-bearing: bash also allows the
    punctuation MID-token (`if(kubectl delete pod x)`, `while(...)`), where a
    trim leaves `if(kubectl` untouched and the same bypass survives. A token
    that is nothing BUT punctuation is returned as-is, so SHELL_KEYWORDS still
    recognises a lone `(`.
    """
    parts = [x for x in GROUPING.split(token) if x]
    if not parts:
        return token
    return parts[-1].split("/")[-1]


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
        base = _basename(argv[0])
        # Leading shell keywords and env assignments come before any wrapper.
        if base in SHELL_KEYWORDS:
            argv = argv[1:]
            continue
        if "=" in argv[0] and not argv[0].startswith("-") and not K8S_BINARY.match(base):
            # VAR=value kubectl … — round 2 stripped these and the inversion
            # dropped the branch, silently denying `KUBECONFIG=/tmp/kc kubectl get`.
            argv = argv[1:]
            continue
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
                base_next = _basename(argv[0])
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
        if tok in VALUE_FLAGS:
            i += 2
            continue
        if tok in BOOLEAN_FLAGS:
            i += 1
            continue
        # An unrecognised flag: we cannot know whether it consumes the next
        # token, so we cannot know which token is the verb. Under the inversion
        # that is a failure to prove, not a licence to guess — guessing here let
        # `kubectl rollout --field-manager status restart deploy/x` resolve its
        # sub-verb to "status" and restart a Deployment unvalidated.
        return UNPROVABLE, -1
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
    base = _basename(argv[0])
    if not K8S_BINARY.match(base):
        return None, None, -1
    tool = "helm" if base.startswith("helm") else "kubectl"
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
    if verb is UNPROVABLE:
        return False
    if verb is None:
        # No verb at all: `kubectl`, `kubectl --help`, `helm --help`. There is
        # nothing to validate, and refusing it produced the nonsense diagnostic
        # "Validation for `kubectl None` is not implemented yet".
        return True
    if tool == "helm":
        return verb in HELM_READ_VERBS or verb in HELM_LOCAL_VERBS
    if verb in READ_ONLY_SUBVERBS:
        sub = subverb_of(argv, verb_idx)
        if sub is UNPROVABLE or sub is None:
            return False
        return sub in READ_ONLY_SUBVERBS[verb]
    return verb in KUBECTL_READ_VERBS or verb in KUBECTL_LOCAL_VERBS


def is_launcher(argv):
    """True when argv's head executes a command line handed to it as a string.

    Takes an already-stripped argv, like classify. This is the inverse of the
    "inert commands" list it replaced, and the inversion matters: enumerating what
    is SAFE to ignore put `awk`, `sed` and `git` on the safe list, and all three
    execute their arguments. Enumerating what LAUNCHES is the bounded direction,
    but it is not free: an UNLISTED launcher is treated as an ordinary program, so
    the kubectl token in it reads as an argument and the segment proceeds. That is
    the residual this direction accepts, in exchange for not refusing every
    `gh pr create --title "…kubectl…"`. `git -c alias.x='!cmd'` is the known
    instance that needs deliberate obfuscation, but an ordinary unlisted
    program that execs its argv is the same hole with no obfuscation at all,
    so WRAPPER_SPEC carries the members that actually exist on a workstation
    and the post-strip head check in main refuses whatever a wrapper leaves
    unidentifiable. The general case cannot be decided from argv alone.
    """
    if not argv:
        return False
    return _basename(argv[0]) in LAUNCHERS


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
        if K8S_BINARY.match(_basename(t)):
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

    tool_input = data.get("tool_input")
    if not isinstance(tool_input, dict):
        tool_input = {}
    command = tool_input.get("command")
    if not isinstance(command, str):
        # A non-string command cannot be inspected. The engine turns a non-zero
        # exit into a block for a fail_closed hook, which is the right direction,
        # but a traceback is not an acceptable way to say so.
        sys.stderr.write("k8s-validate: tool_input.command is not a string\n")
        sys.exit(1)

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

        # Stripping must land on a bare program name. Anything else means a
        # wrapper consumed the binary, left its own flag at the head, or left a
        # quoted payload there — and every one of those fell through to
        # `tool is None` below, i.e. was SPARED rather than examined. That is a
        # failure to identify, which under the inversion is a refusal: it is how
        # `time -p kubectl delete pod x` and `flock /tmp/l -c '…'` both ran.
        head = argv[0] if argv else ""
        if not head or head.startswith("-") or any(c.isspace() for c in head):
            refuse(
                "After removing the process wrappers this guard recognises, the command it "
                "would actually run could not be identified. Express it as a direct "
                "kubectl/helm command, with the binary first.",
                attempt, consecutive,
            )

        if is_launcher(argv):
            refuse(
                "This command hands a command line to another program to execute — a shell, an "
                "interpreter, or a remote call — so what it would actually run cannot be read. "
                "Express it as a direct kubectl/helm command.",
                attempt, consecutive,
            )

        tool, verb, verb_idx = classify(argv)
        if tool is None:
            # The head is neither a launcher nor kubectl/helm, so this segment
            # runs some other program that merely takes the word as an argument:
            # `grep -rn kubectl deploy.sh`, `echo kubectl delete pod x`,
            # `gh pr create --title "fix the kubectl guard"`, `ls -l …/kubectl`.
            # Refusing these was the friction that gets a guard disabled, and the
            # dangerous direction is already covered: anything that EXECUTES a
            # string it is handed is in LAUNCHERS and was refused above.
            continue

        # The sentinel must cover the SUB-verb too, not only the top-level verb:
        # `kubectl rollout --field-manager status restart` is exactly the shape
        # round 5 targeted, and checking only the head let it deny via the
        # Task-9 stub with a diagnostic that named the wrong problem.
        unprovable = verb is UNPROVABLE or (
            verb in READ_ONLY_SUBVERBS and subverb_of(argv, verb_idx) is UNPROVABLE
        )
        if unprovable:
            refuse(
                "This looks like a kubectl/helm command but it carries a flag this guard does "
                "not recognise, so which token is the verb cannot be determined. Re-run it "
                "with the verb immediately after the binary, or with only well-known global "
                "flags before it.",
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


PROD_PATTERN = re.compile(r"prod|prd|production", re.IGNORECASE)
EPHEMERAL_LABEL = "omnis.dev/ephemeral"
CLEANUP_AGENTS = {"k8s_cleaner"}


def run_argv(argv, cwd):
    """Run argv with no shell and return (exit_code, stdout, stderr)."""
    try:
        p = subprocess.run(argv, cwd=cwd, capture_output=True, text=True,
                           timeout=VALIDATE_TIMEOUT)
    except subprocess.TimeoutExpired:
        return 124, "", "timed out after %ds" % VALIDATE_TIMEOUT
    except OSError as e:
        return 127, "", str(e)
    return p.returncode, p.stdout, p.stderr


def has_flag(argv, *names):
    for tok in argv:
        for n in names:
            if tok == n or tok.startswith(n + "="):
                return True
    return False


def flag_value(argv, *names):
    for i, tok in enumerate(argv):
        for n in names:
            if tok == n and i + 1 < len(argv):
                return argv[i + 1]
            if tok.startswith(n + "="):
                return tok.split("=", 1)[1]
    return None


def target_scope(argv):
    """Return the namespace and context this command targets, if named."""
    return (flag_value(argv, "-n", "--namespace") or "",
            flag_value(argv, "--context") or "")


def check_production(argv, attempt, consecutive):
    ns, ctx = target_scope(argv)
    for label, value in (("namespace", ns), ("context", ctx)):
        if value and PROD_PATTERN.search(value):
            refuse(
                "The target %s is %r, which looks like production. Nothing has been "
                "changed. If this is genuinely the intended target, say so explicitly; "
                "if it is a typo, correct the %s and retry."
                % (label, value, label),
                attempt, consecutive,
            )


def check_attested(argv, attestations, attempt, consecutive, subject):
    """Require an APPROVED verdict from the reviewer for this exact change.

    A missing verdict is a TERMINAL refusal: it must never escalate to `ask`, or
    the guarantee would be removable by disabling the reviewer agent and then
    clicking "allow".
    """
    rec = attestations.get(subject) if isinstance(attestations, dict) else None
    if not rec:
        emit("deny",
             "This change has not been reviewed. Delegate it to the k8s_validator "
             "sub-agent with the change identifier `%s`, and retry once it has "
             "recorded an APPROVED verdict. (If no validation agent is configured, "
             "Kubernetes changes are refused by design.)" % subject)
    if rec.get("verdict") != "APPROVED":
        emit("deny",
             "The reviewer REJECTED this change:\n\n%s" % (rec.get("reasons") or "(no reasons given)"))


def validate_manifest(argv, cwd, attempt, consecutive):
    path = flag_value(argv, "-f", "--filename")
    if not path:
        return validate_imperative(argv, "apply", cwd, attempt, consecutive)
    code, out, err = run_argv([argv[0], "diff", "-f", path] + scope_flags(argv), cwd)
    # exit 1 means "a diff exists" — the normal case. Only >1 is an error.
    if code > 1:
        refuse("`kubectl diff` failed, so the change could not be previewed:\n\n%s"
               % (err.strip() or out.strip()), attempt, consecutive)
    code, out, err = run_argv(list(argv) + ["--server-side", "--dry-run=server"], cwd)
    if code != 0:
        refuse("The API server rejected this change in a server-side dry run:\n\n%s"
               % (err.strip() or out.strip()), attempt, consecutive)
    return out.strip()


def scope_flags(argv):
    flags = []
    ns, ctx = target_scope(argv)
    if ns:
        flags += ["-n", ns]
    if ctx:
        flags += ["--context", ctx]
    return flags


def validate_imperative(argv, verb, cwd, attempt, consecutive):
    code, out, err = run_argv(argv + ["--dry-run=server"], cwd)
    if code != 0:
        text = (err or out).strip()
        if "unknown flag" in text or "unknown shorthand" in text:
            refuse("`%s` does not support a server-side dry run, so this change cannot be "
                   "validated as written. Express it as a manifest and apply that file "
                   "instead." % ("%s %s" % (argv[0], verb)), attempt, consecutive)
        refuse("The API server rejected this change in a dry run:\n\n%s" % text,
               attempt, consecutive)
    return out.strip()


def validate_helm(verb, argv, verb_idx, cwd, attempt, consecutive):
    ops = operands(argv, verb_idx)
    if verb in HELM_DESTRUCTIVE_VERBS:
        code, out, err = run_argv(["helm", "history"] + ops[:1] + scope_flags(argv), cwd)
        if code != 0:
            refuse("No such Helm release, so `helm %s` cannot be validated:\n\n%s"
                   % (verb, (err or out).strip()), attempt, consecutive)
        return out.strip()
    code, _, _ = run_argv(["helm", "plugin", "list"], cwd)
    preview = ["helm", "diff", "upgrade"] + ops if code == 0 else argv + ["--dry-run=server"]
    code, out, err = run_argv(preview, cwd)
    if code != 0:
        refuse("The Helm change could not be previewed:\n\n%s" % ((err or out).strip()),
               attempt, consecutive)
    return out.strip()


def validate_destructive(argv, verb_idx, agent, cwd, attempt, consecutive):
    ops = operands(argv, verb_idx)
    if has_flag(argv, "--all") or not [a for a in ops if not a.startswith("-")]:
        refuse("This deletion names no specific resource, so its blast radius is "
               "unbounded. Name the resources to delete explicitly.", attempt, consecutive)
    # ops already carries -n/--context, so scope_flags would duplicate them.
    code, out, _ = run_argv([argv[0], "get"] + ops + ["-o", "json"], cwd)
    if code != 0:
        refuse("The deletion target could not be resolved, so it cannot be checked. "
               "Verify the resource exists before deleting it.", attempt, consecutive)
    try:
        doc = json.loads(out or "{}")
    except ValueError:
        doc = {}
    items = doc.get("items", [doc]) if doc else []
    for item in items:
        meta = (item or {}).get("metadata") or {}
        labels = meta.get("labels") or {}
        if agent in CLEANUP_AGENTS and labels.get(EPHEMERAL_LABEL) != "true":
            refuse("%r is not labelled %s=true, so it is not a leftover this agent may "
                   "remove. Only resources the squad created for diagnosis are in scope."
                   % (meta.get("name", "?"), EPHEMERAL_LABEL), attempt, consecutive)
        if meta.get("ownerReferences"):
            refuse("%r is owned by %s, so deleting it is futile — its controller will "
                   "recreate it. Change or remove the owner instead."
                   % (meta.get("name", "?"), meta["ownerReferences"][0].get("kind", "a controller")),
                   attempt, consecutive)
    return "%d resource(s) would be deleted." % max(len(items), 1)


def validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations):
    check_production(argv, attempt, consecutive)
    if tool == "helm":
        preview = validate_helm(verb, argv, verb_idx, cwd, attempt, consecutive)
    elif verb in DESTRUCTIVE_VERBS:
        preview = validate_destructive(argv, verb_idx, agent, cwd, attempt, consecutive)
    elif verb in APPLY_VERBS:
        preview = validate_manifest(argv, cwd, attempt, consecutive)
    elif verb in IMPERATIVE_VERBS:
        preview = validate_imperative(argv, verb, cwd, attempt, consecutive)
    else:
        refuse("`%s %s` changes the cluster but has no validation rule, so it is refused "
               "rather than applied unchecked." % (tool, verb), attempt, consecutive)
    return preview


if __name__ == "__main__":
    main()
