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

import hashlib
import json
import os
import re
import shlex
import subprocess
import sys

MAX_ATTEMPTS = 3          # matches agentCapGraceCalls in agent/budget_plugin.go
VALIDATE_TIMEOUT = 45     # seconds; below the hook's own timeout so we can explain
SYSTEM_MESSAGE_MAX = 16000  # cap on the JOINED systemMessage; each segment's own
                             # preview is already capped to 4000, but that cap is
                             # per segment, not per compound command

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

# `rollout` sub-verbs that support no --dry-run at all (verified against the
# real binary: `error: unknown flag: --dry-run`), so validate_imperative's
# normal dry-run attempt permanently refuses them with advice that cannot be
# followed — "express it as a manifest" is impossible for an action that
# recreates pods rather than changing desired state. `undo` is deliberately
# NOT here: it DOES accept --dry-run=server (it just fails without a reachable
# cluster, the same as any other verb), so the normal path already handles it.
ROLLOUT_SCOPE_VERBS = {"restart", "pause", "resume"}

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


def build_proceed_note(validated):
    """The systemMessage for a proceed with ≥1 validated segment: every
    segment's own diff, joined, so the permission card is informative for a
    compound command too — not just the first mutating segment.

    Each segment's own preview is already capped to 4000 chars, but that cap
    is PER segment: a compound line with many mutating segments (`a.yaml &&
    b.yaml && c.yaml && …`) can still join into an unbounded systemMessage, so
    the JOINED result is capped to SYSTEM_MESSAGE_MAX too.
    """
    note = "\n\n---\n\n".join(
        "**Validated change** (`%s %s`):\n\n```\n%s\n```" % (tool, verb, preview[:4000])
        for tool, verb, preview in validated
    )
    if len(note) > SYSTEM_MESSAGE_MAX:
        note = (note[:SYSTEM_MESSAGE_MAX] +
                "\n\n... (truncated: %d validated changes in this command; showing the first ones. "
                "Split this into separate calls to see each diff in full.)" % len(validated))
    return note


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


# kubectl's -f/--filename target flags. -k/--kustomize are handled
# separately (KUSTOMIZE_FLAGS below): kubectl treats them completely
# differently — -f <dir> applies whatever manifest files are directly in
# that directory; -k <dir> builds it as a kustomization, which can reference
# files ANYWHERE. Conflating the two (round 2's design) meant a -f directory
# that happened to contain a kustomization.yaml got rendered as if -k had
# named it, which is not what kubectl actually does with -f.
FILENAME_FLAGS = ("-f", "--filename")
KUSTOMIZE_FLAGS = ("-k", "--kustomize")

# Helm's own spelling for a values file. Shares kubectl's "-f" short flag but
# NOT its long name ("--values", not "--filename") — a real asymmetry once:
# `-f values.yaml` was content-bound while the exact synonym `--values
# values.yaml` was not bound at all. Both are hashed identically now (see
# helm_content_digest).
HELM_VALUES_FLAGS = ("-f", "--values")

# A remote scheme this guard cannot read bytes from before apply time. This is
# not the only remote shape (kustomize also treats a bare VCS-style reference
# like `github.com/org/repo` as remote, with no "://" at all) — that shape is
# caught separately, by resolve_target_digest failing to find it as a local
# path, rather than by trying to enumerate every non-URL remote syntax.
URL_SCHEME_RE = re.compile(r"^[A-Za-z][A-Za-z0-9+.\-]*://")

# Per-RUN memoisation of an already-computed digest, keyed by (kind,
# resolved path) — this script is a fresh `python3` process per hook call,
# so a bare module dict is already scoped correctly; there is no cross-call
# state to leak or invalidate. Without this, a compound command naming the
# SAME target twice (two segments, or a repeated -f) would render or walk it
# twice — real cost for a kustomize render (a subprocess call) and, at the
# extreme measured live for an unrelated bug (an empty -f= target hashing an
# entire working tree), enough to approach the hook's own 60s timeout.
_target_digest_cache = {}


def _memoized(key, compute):
    if key not in _target_digest_cache:
        _target_digest_cache[key] = compute()
    return _target_digest_cache[key]


def flag_values(argv, *names):
    """Every VALUE for a repeatable value-flag, in argv order — the plural
    sibling of flag_value, which returns only the first match."""
    out = []
    for i, tok in enumerate(argv):
        for n in names:
            if tok == n and i + 1 < len(argv):
                out.append(argv[i + 1])
                break
            if tok.startswith(n + "="):
                out.append(tok.split("=", 1)[1])
                break
    return out


def _file_bytes_digest(path):
    """sha256 of one file's bytes. Raises OSError on failure — callers
    decide what that means: a TERMINAL refusal for the target the command
    directly names (_file_digest), or a bindable "unreadable" sentinel for
    an incidental entry found while walking a directory (_walk_entry_digest)
    — see both functions' own docstrings for why they differ."""
    with open(path, "rb") as f:
        return hashlib.sha256(f.read()).hexdigest()


def _file_digest(path):
    """sha256 of the DIRECTLY-NAMED target's bytes — the exact -f/-k
    argument, or a Helm values file. TERMINALLY refuses (emit("deny", …)
    directly, like check_attested/resolve_target_digest — see
    resolve_target_digest's docstring for why) on OSError: this file IS
    what the command names, so an unreadable target here is the same
    "cannot bind content" state as a missing or remote one — a click past
    three failed attempts must not substitute for the review this file
    could never even identify.
    """
    try:
        return _file_bytes_digest(path)
    except OSError as e:
        emit("deny",
             "The manifest target %r could not be read (%s), so its content "
             "cannot be verified." % (path, e))


def _walk_entry_digest(full_path):
    """Digest contribution for ONE file found INSIDE a directory WALK
    (_dir_digest) — as opposed to the directly-named target (_file_digest,
    which still terminally refuses on the SAME kind of error). A stray
    unreadable entry anywhere under an ordinary directory target — a
    dangling symlink, a FIFO, a permission-denied leftover unrelated to the
    actual change — must not dead-end the whole apply with no escalation
    path at all: that was _file_digest's terminal refusal applied one level
    too broadly, catching M1's own fix in its blast radius. Its PRESENCE and
    TYPE are still bound (so the digest changes if the entry appears,
    disappears, or its bytes become readable again), via a sentinel string
    in place of its content — only the DIRECTLY-NAMED target's own
    unreadability is treated as fatal.
    """
    try:
        return "file:" + _file_bytes_digest(full_path)
    except OSError as e:
        return "unreadable:" + type(e).__name__


def _dir_digest(path):
    """A digest of every entry under `path`, sensitive to CONTENT (or, for
    an unreadable stray entry, its presence/type — see _walk_entry_digest)
    only: sorted RELATIVE paths, each paired with its own digest, so the
    result depends on the tree's bytes — never os.walk's traversal order
    (unspecified), never mtimes or permissions, which can change with the
    content untouched.

    Used for an ORDINARY directory target: a kubectl -f <dir> (which applies
    whatever manifest files are directly there — kubectl does not invoke
    kustomize for -f even when the directory happens to contain a
    kustomization.yaml, so this never renders one), a Helm values file that
    happens to be a directory, or a Helm CHART directory (see
    helm_content_digest) — none of these reference files OUTSIDE `path`,
    unlike a kustomization (see _kustomize_render_digest's docstring for why
    THAT needs a render instead of a walk).
    """
    entries = []
    for root, dirs, files in os.walk(path):
        dirs.sort()
        for name in sorted(files):
            full = os.path.join(root, name)
            rel = os.path.relpath(full, path).replace(os.sep, "/")
            entries.append([rel, _walk_entry_digest(full)])
    entries.sort(key=lambda e: e[0])
    canonical = json.dumps(entries, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _kustomize_render_digest(path, cwd):
    """Digest of `kubectl kustomize <path>`'s RENDERED output — the actual
    resources that would be applied — rather than a walk of `path`'s own
    files.

    A kustomization's `resources:`/`bases:`/`components:`/`patches:` (each
    itself recursive) can each point anywhere, including outside `path` via
    the dominant kustomize idiom (an overlay's `resources: [../../base]`),
    and can reach a target through a SYMLINKED subdirectory — os.walk does
    not follow directory symlinks by default. A walk over the overlay
    directory alone is blind to both: reproduced live (before this function
    existed) by rewriting a BASE manifest referenced via `../base` — the walk
    digest was byte-identical before and after — and again through a
    symlinked subdirectory. This is why -k (unconditionally) renders while
    an ordinary -f directory (never referencing anything outside itself) is
    walked instead — see _dir_digest's docstring for that half.

    A render FAILURE is treated as "cannot bind content" too (TERMINAL,
    emit("deny", …) directly) rather than an escalatable mechanical failure:
    by the time this runs, the per-verb validator has ALREADY previewed this
    exact target successfully (kubectl diff / a dry run, which for `-k`
    requires a successful local kustomize build first), so a render failure
    here is either a race (content changed between the two calls — itself
    grounds for suspicion) or an environment inconsistency, not a normal
    "fix the manifest and retry" situation an ask-then-click should ever
    paper over. `kubectl kustomize` is a read-only, no-cluster-contact
    render (KUBECTL_LOCAL_VERBS already treats it as such elsewhere in this
    file), so this costs nothing beyond the render itself.
    """
    code, out, err = run_argv(["kubectl", "kustomize", path], cwd)
    if code != 0:
        emit("deny",
             "`kubectl kustomize` could not render %r, so its content cannot "
             "be verified:\n\n%s" % (path, (err or out).strip()))
    return hashlib.sha256(out.encode("utf-8")).hexdigest()


def _resolve_local_path(cwd, path):
    """Common prelude for any -f/-k/Helm-values target: resolves `path`
    against `cwd` exactly as kubectl/helm themselves resolve it (never
    argv[0]'s cwd — see run_argv's callers' own comment on that trap), and
    TERMINALLY refuses — emit("deny", …) directly, like check_attested (see
    its own docstring for why, and note this function, like check_attested,
    takes no attempt/consecutive: there is nothing in scope here for a
    future edit to reach for and accidentally pass to refuse()) — for
    anything whose bytes cannot be pinned down right now. Escalating any of
    these to `ask` would let one user click substitute for a review of
    content the guard could never even identify, which is precisely the
    state a click is least safe in:

    - an EMPTY target (a malformed `-f=`/`-k=` with nothing after the `=`):
      resolves to cwd itself via os.path.join, which — if cwd happens to be
      a real directory, usually true — silently hashed the ENTIRE working
      tree instead of refusing an unusable target (measured: 3.39s / 7958
      files on this repo — comfortably able to exceed the hook's own 60s
      timeout, and therefore the block-the-call outcome that timeout exists
      to avoid, on a larger one).
    - a URL, or a reference that merely LOOKS remote (caught below as "does
      not exist locally" rather than enumerated by scheme): the API server
      (or Helm) would fetch its content independently at apply time, so an
      attestation of "these bytes" could never be re-verified.
    - a path that resolves to neither a file nor a directory: nothing to
      bind, and proceeding as if there were nothing to check would silently
      drop the guarantee for a typo'd path.

    Returns (full_path, is_dir) once resolution proves the target actually
    exists locally as one or the other.
    """
    if not path:
        emit("deny", "A -f/-k flag was given an empty target, so nothing can be verified.")
    if URL_SCHEME_RE.match(path):
        emit("deny",
             "%r is a remote target; the API server would fetch its content "
             "independently at apply time, so it cannot be bound to a "
             "reviewed change. Fetch it to a local file first (e.g. `curl -Lo "
             "change.yaml %s`) and apply that file instead." % (path, path))
    full = os.path.join(cwd or ".", path)
    if os.path.isdir(full):
        return full, True
    if os.path.isfile(full):
        return full, False
    emit("deny",
         "The manifest target %r could not be found (resolved to %r), so its "
         "content cannot be verified. A remote target cannot be validated "
         "either — fetch it to a local file first." % (path, full))


def _local_target_digest(cwd, path):
    """Content digest of a plain -f-style local target (kubectl's
    -f/--filename, or a Helm -f/--values file): a file's bytes, or a
    memoised WALK of a directory's files. Never a kustomize render — that
    is resolve_kustomize_target_digest's job, reached only via -k/--kustomize
    (see _dir_digest / _kustomize_render_digest for why the two targets need
    different treatment).
    """
    full, is_dir = _resolve_local_path(cwd, path)
    if is_dir:
        return _memoized(("walk", full), lambda: _dir_digest(full))
    return _memoized(("file", full), lambda: _file_digest(full))


def resolve_target_digest(cwd, path):
    """-f/--filename target digest — see _local_target_digest."""
    return _local_target_digest(cwd, path)


def resolve_kustomize_target_digest(cwd, path):
    """-k/--kustomize target digest: always rendered via `kubectl kustomize`
    (_kustomize_render_digest) — kubectl itself requires a -k target to
    already be a valid kustomization directory (or a remote reference,
    refused above like any other -f/-k remote target), so reaching here
    with something that is not a directory would already have failed
    mechanical validation before this point; refused defensively rather
    than falling back to a file hash that would not match what `kubectl
    kustomize` — or a real apply -k — would actually see.
    """
    full, is_dir = _resolve_local_path(cwd, path)
    if not is_dir:
        emit("deny", "%r is not a directory, so it cannot be built as a kustomization." % path)
    return _memoized(("kustomize", full), lambda: _kustomize_render_digest(full, cwd))


def local_target_digest(argv, cwd):
    """The content half of a kubectl segment's subject: "" when the verb
    carries no local manifest/kustomization target at all (delete-by-name,
    patch, rollout, …) — the normalised argv alone already identifies those
    changes. Otherwise, a digest that changes when and only when the
    target's own bytes (a file), walked contents (an ordinary directory), or
    RENDERED output (a kustomization — see resolve_kustomize_target_digest)
    change.

    -f/--filename and -k/--kustomize are resolved by DIFFERENT functions
    (_local_target_digest vs resolve_kustomize_target_digest) — see their
    own docstrings for why a -f directory is walked while a -k directory is
    always rendered, never the other way around based on what happens to be
    inside it.

    Helm is NOT handled here — see helm_content_digest, which walks the
    chart directory (Helm vendors its own dependencies inside the chart, so
    nothing there references outside it) plus hashes -f/--values targets,
    rather than rendering.
    """
    manifest_targets = flag_values(argv, *FILENAME_FLAGS)
    kustomize_targets = flag_values(argv, *KUSTOMIZE_FLAGS)
    if not manifest_targets and not kustomize_targets:
        return ""
    digests = [["f", path, resolve_target_digest(cwd, path)] for path in manifest_targets]
    digests += [["k", path, resolve_kustomize_target_digest(cwd, path)] for path in kustomize_targets]
    canonical = json.dumps(sorted(digests), separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _local_chart_candidates(argv, verb_idx, cwd):
    """Every BARE token after the verb, in a POSITION this guard can trust,
    that resolves to something local this guard can read: a directory
    containing Chart.yaml (Helm's own chart marker — it does not accept
    "Chart.yml"), or a local packaged chart archive (a ".tgz" file Helm can
    install directly). Returns a LIST — the caller (helm_content_digest)
    decides what to do with the count: exactly one candidate is used, zero
    or more than one both refuse, for different, clearly-labelled reasons.

    "A position this guard can trust" means: the token immediately BEFORE
    it does not start with "-". This is deliberately POSITIONAL, not based
    on a set of known value-taking flags (an earlier version of this
    function skipped only the value of a flag it recognised — VALUE_FLAGS
    plus HELM_VALUES_FLAGS — which closed the ./decoy-behind-a-KNOWN-flag
    bypass but left the identical shape open behind an UNKNOWN one:
    `helm upgrade myrel bitnami/nginx --post-renderer ./decoy -n demo`
    installs the REMOTE chart bitnami/nginx, but --post-renderer is not in
    either flag set, so ./decoy — its value — was still scanned as a bare
    candidate, became the SOLE local match, and got bound, attested, and
    applied in place of the actual (non-local, therefore unbindable)
    chart. Reproduced live before this fix. Same bug CLASS as Task 9's
    `ops[:1]` — a flag's value read as an operand — which subverb_of's own
    docstring already warns against elsewhere in this file, and the same
    lesson repeated: enumerating which flags are "known" is unbounded;
    "does this token immediately follow ANY flag" is not.

    The accepted cost, deliberately: a flag placed BETWEEN the release name
    and the chart (`helm upgrade myrel --atomic ./chart`) makes the chart
    token "follow a flag" too, so it is excluded here — this refuses with
    actionable advice (see helm_content_digest) rather than guessing, and
    the fix (move the chart path so nothing precedes it but the release
    name) is trivial for whoever wrote the command. Distinguishing a
    boolean flag like --atomic from a value-taking one to recover that case
    would need Helm's own flag-arity table again, by another name — exactly
    the unbounded enumeration this guard rejects everywhere else. A flag
    AFTER the chart path is unaffected either way (`--timeout 5m` following
    the chart never touches this rule; verified directly).
    """
    tokens = argv[verb_idx + 1:]

    # A chart candidate must sit in the CHART SLOT, and Helm's grammar fixes that
    # slot without any knowledge of individual flags: the chart follows the release
    # name. So a candidate must be a bare token whose immediate predecessor is also
    # a bare token — never a flag, and never the first token.
    #
    # That last clause is the one that matters. Excluding tokens that follow a flag
    # is necessary but not sufficient: `helm upgrade decoy --atomic ./chart`
    # excludes the real chart (it follows a flag — the accepted cost), and if the
    # RELEASE NAME happens to name a local chart directory it was then the SOLE
    # candidate, so the digest bound `decoy` and the real chart was never bound at
    # all. Attested once, `./chart` could be rewritten forever and still proceed —
    # "validate v1, apply v2", reproduced live.
    #
    # Requiring a bare predecessor is also why this cannot be done by COUNTING
    # positional slots: the flag rule removes tokens, which shifts every later
    # slot, so "drop the first positional" deleted the chart from
    # `helm upgrade --install myrel ./chart` — the canonical idiom. A relationship
    # between two adjacent tokens does not shift.
    #
    # One exception, and it is a positional-arity modifier rather than an entry in
    # a flag-arity table: `--generate-name` removes the release operand entirely
    # (`helm install ./chart --generate-name`), so the first token is the chart.
    generate_name = any(t == "--generate-name" or t.startswith("--generate-name=")
                        for t in tokens)

    candidates = []
    for i, tok in enumerate(tokens):
        if tok.startswith("-"):
            continue
        if i == 0:
            if not generate_name:
                continue  # the release-name slot is never the chart
        elif tokens[i - 1].startswith("-"):
            continue  # follows a flag positionally — never a candidate, known or not
        full = os.path.join(cwd or ".", tok)
        if os.path.isfile(os.path.join(full, "Chart.yaml")):
            candidates.append((tok, full, "dir"))
        elif tok.endswith(".tgz") and os.path.isfile(full):
            candidates.append((tok, full, "tgz"))
    return candidates


def helm_content_digest(verb, argv, verb_idx, cwd):
    """The content half of a Helm change's subject: a memoised WALK of the
    chart DIRECTORY (_dir_digest — the same treatment an ordinary kubectl -f
    <dir> gets: templates/, values.yaml, crds/, and any vendored charts/
    subchart dependency, since Helm stores a chart's dependencies INSIDE the
    chart directory rather than by reference — the property a kustomization
    lacks, via `resources: [../../base]`, which is why kustomize genuinely
    needs a render and Helm never did) — or, for a local packaged chart
    (a ".tgz" Helm can install directly), a memoised hash of its own bytes
    (_file_digest) — PLUS the bytes of every -f/--values target
    (_local_target_digest — identical treatment for both spellings).

    "" for a Helm verb with no chart argument at all (uninstall, rollback —
    release-name-only mutations already identified by argv alone). `--set
    k=v` needs no special handling: its value is literally a token in argv,
    and argv is already part of the subject (see subject_hash) — nothing
    here parses Helm's flags at all: _local_chart_candidates' rule is
    purely positional (see its own docstring for why that, not a flag
    table, is what closes the bypass).

    TERMINALLY refuses (like _resolve_local_path — see its docstring) in
    two distinct cases, both because a click past three attempts must not
    substitute for a review of content this guard could not pin down, and
    both name the fix rather than just the failure:
    - ZERO local candidates: either a repo alias, an OCI reference, a
      non-local .tgz reference, or a URL (nothing on this machine to bind
      — told to `helm pull` it locally first, the same shape
      _resolve_local_path gives for a remote kubectl manifest), OR a real
      local chart whose token happens to follow a flag (see
      _local_chart_candidates — the accepted cost of the positional rule).
      Both are told to put the chart path immediately after the release
      name.
    - MORE THAN ONE local candidate: which one Helm would actually use
      cannot be determined from here — guessing (an earlier version of
      this function picked the FIRST bare token unconditionally, then
      merely the first UNFLAGGED one) is exactly what produced two
      successive real bypasses. Refusing as ambiguous is the fail-closed
      direction; told to leave only the real chart's path unflagged.

    A previous version of this function rendered via `helm template`
    instead, replaying the command's own flags into it. Retired: proven live
    that the render EXCLUDES crds/ unless --include-crds — while a real
    install/upgrade DOES apply them — so rewriting a CRD's own content left
    the render-based subject unchanged; separately, building the replay
    argv correctly needed Helm's own flag-arity table, which is exactly the
    unbounded enumeration this guard rejects everywhere else. The walk
    needs neither a subprocess nor a flag table, and binds crds/ that the
    render dropped.
    """
    if verb in HELM_DESTRUCTIVE_VERBS:
        return ""
    candidates = _local_chart_candidates(argv, verb_idx, cwd)
    if not candidates:
        emit("deny",
             "This Helm command's chart argument does not name a local "
             "directory (containing Chart.yaml) or a local packaged chart "
             "(.tgz) in a position this guard can verify — the chart "
             "operand must not be preceded by a flag. Put the chart path "
             "immediately after the release name (e.g. `helm upgrade "
             "RELEASE ./chart ...`), or if the chart is genuinely remote, "
             "`helm pull` it locally and point at that directory/archive, "
             "then retry.")
    if len(candidates) > 1:
        emit("deny",
             "This command's operands name more than one possible chart "
             "target (%s); which one Helm would actually use cannot be "
             "determined. Put the chart path immediately after the "
             "release name, with no other operand unflagged before it, "
             "and retry." %
             ", ".join(repr(c[0]) for c in candidates))
    _, chart_path, kind = candidates[0]
    if kind == "dir":
        chart_digest = _memoized(("walk", chart_path), lambda: _dir_digest(chart_path))
    else:
        chart_digest = _memoized(("file", chart_path), lambda: _file_digest(chart_path))
    entries = [["chart", chart_digest]]
    for path in flag_values(argv, *HELM_VALUES_FLAGS):
        entries.append([path, _local_target_digest(cwd, path)])
    canonical = json.dumps(sorted(entries), separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def subject_hash(tool, verb, argv, content_digest):
    """The change identifier for ONE mutating segment: canonicalised,
    normalised argv (tool, verb, and every token, in order — the design
    spec's "normalised argv") PLUS content_digest, the manifest/chart
    target's own rendered/raw content when the verb names one (see
    local_target_digest / helm_content_digest). Folding content in is what
    makes "validate v1, apply v2" refuse even though the command line naming
    the target is byte-identical in both cases.

    Deliberately UNRELATED to hookstate.HashArgs in Go: HashArgs hashes only
    the raw tool_input and exists solely to key the attempt/consecutive
    counters — a coarser job with no reason to touch the filesystem. This is
    the Python side's own, independent value, and nothing on the Go side
    needs to reproduce it: record_validation's caller is always TOLD the
    subject verbatim, from a deny message's "change identifier `...`", never
    expected to recompute it (see check_attested's own deny message below).
    """
    payload = {"tool": tool, "verb": verb, "argv": list(argv), "content": content_digest}
    canonical = json.dumps(payload, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


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
    # Each mutating segment gets its OWN subject, computed inside validate()
    # from that segment's own argv + manifest content (see subject_hash) —
    # not one subject for the whole tool call. A compound command's two
    # mutating segments can name completely different targets with completely
    # different content, so binding them to one shared subject would let an
    # attestation of the first authorize the second's unreviewed content too.

    # Every validated segment's preview, so the final proceed() can show the
    # diff. Collected rather than shown per-segment: `validate()` must not
    # exit on success, or a SECOND mutating segment in a compound command
    # (`kubectl apply -f a.yaml && kubectl delete ns x`) would never even
    # reach mechanical validation — the first segment's proceed() would end
    # the process before the loop got to it. Refusal is unaffected: `refuse`/
    # `emit("deny", …)` still exit immediately, which is the correct
    # direction for a failure (nothing in the compound command should run).
    validated = []

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

        preview = validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive,
                           attestations)
        validated.append((tool, verb, preview))

    if not validated:
        proceed()
    else:
        proceed(build_proceed_note(validated))


PROD_PATTERN = re.compile(r"prod|prd|production", re.IGNORECASE)
EPHEMERAL_LABEL = "omnis.dev/ephemeral"
CLEANUP_AGENTS = {"k8s_cleaner"}

# The reviewer's only legitimate write is record_validation — never a cluster
# mutation. agent/agent.go's attest-group comment states the invariant this
# enforces: "Mount ONLY on a reviewer agent. An agent holding this can approve
# its own changes, which is exactly the hole internal/attest exists to close."
# That Go-side rule (see TestOnlyTheValidatorCanAttest) keeps every OTHER
# agent off the attest tools; it says nothing about keeping k8s_validator off
# Bash, which it legitimately needs for READ-ONLY kubectl (re-deriving facts
# from the live cluster is its entire job — see its instruction.md). So the
# other half of the invariant — the reviewer may never MUTATE — has to be
# policy, enforced here, on the already-plumbed agent parameter, the same
# shape as the CLEANUP_AGENTS branch above.
VALIDATOR_AGENTS = {"k8s_validator"}


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


def first_operand(argv, start):
    """The first bare token after index `start`, using the SAME value-flag walk
    `_verb_after` uses to find a verb: UNPROVABLE when a flag this guard does
    not recognise makes the token unknowable, None when there is none.

    Reused here (not just for verbs) so a flag's VALUE is never mistaken for
    the operand that follows it. `ops[:1]` used to make exactly that mistake
    for a Helm release name preceded by a flag — `helm uninstall -n demo
    myrel` built `helm history -n -n demo`, which cannot resolve any release
    and either wrongly refuses or, worse, wrongly matches an unrelated one.
    """
    return _verb_after(argv, start)


def bare_operands(argv, start):
    """The non-flag tokens after index `start`: the actual resource/release
    identifiers, as opposed to a value-flag's VALUE.

    Uses the same VALUE_FLAGS/BOOLEAN_FLAGS grammar `_verb_after` uses to find
    a verb, reused here so a flag's value is never mistaken for a named
    resource. `[a for a in ops if not a.startswith("-")]` made exactly that
    mistake: `kubectl delete pod -n demo` counted `demo` — the NAMESPACE
    VALUE — as if it were a second named resource, so the blast-radius check
    could not tell "a resource is named" from "a flag was given a value".

    A flag this guard does not recognise is treated as taking no value
    (skipped alone) rather than refused: unlike identifying THE verb,
    collecting operands has no single right answer to fail closed on, and
    erring toward "not a flag's value" only ever makes this check MORE
    willing to find a named resource — never less, so it can never
    manufacture a missing one. The real safety net for an under-specified
    delete is resolve_target's fail-closed `get`, not this presence check.
    """
    out = []
    i = start + 1
    while i < len(argv):
        tok = argv[i]
        if not tok.startswith("-"):
            out.append(tok)
            i += 1
            continue
        if "=" in tok:
            i += 1
            continue
        if tok in VALUE_FLAGS:
            i += 2
            continue
        i += 1  # a boolean flag, known or not — see the docstring above
    return out


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


def check_attested(argv, attestations, subject):
    """Require an APPROVED verdict from the reviewer for this exact change.

    A missing verdict is a TERMINAL refusal: it must never escalate to `ask`, or
    the guarantee would be removable by disabling the reviewer agent and then
    clicking "allow". `attempt`/`consecutive` are deliberately NOT parameters
    here (unlike every other check_* function) — `refuse()` is the only thing
    that can turn a denial into an escalation, and it needs those two numbers
    to do it. Leaving them out of this function's scope means escalating a
    missing attestation would take a signature change, not just a stray call
    to `refuse(reason, attempt, consecutive)` with the numbers sitting right
    there. See TestMissingAttestationNeverEscalates.
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


def validate_manifest(verb, argv, cwd, attempt, consecutive):
    path = flag_value(argv, "-f", "--filename")
    if not path:
        return validate_imperative(argv, verb, cwd, attempt, consecutive)
    # kubectl, never argv[0]: argv[0] is the RAW head token — "(kubectl" for a
    # paren-glued segment, "kubectl.real"/"kubectl3" for a suffixed install —
    # and running it verbatim with cwd from the hook input (the session's
    # writable working directory) let an agent-planted `./kubectl`/`kubectl`
    # shim answer the guard's own probe instead of the real binary.
    code, out, err = run_argv(["kubectl", "diff", "-f", path] + scope_flags(argv), cwd)
    # exit 1 means "a diff exists" — the normal case. Only >1 is an error.
    if code > 1:
        refuse("`kubectl diff` failed, so the change could not be previewed:\n\n%s"
               % (err.strip() or out.strip()), attempt, consecutive)
    dry_run = ["kubectl"] + list(argv[1:])
    if verb == "apply":
        # --server-side is an apply-only flag: `kubectl replace --server-side`
        # and `kubectl create --server-side` both fail with "unknown flag:
        # --server-side", permanently blocking both verbs and blaming the API
        # server for a flag the guard itself injected.
        dry_run += ["--server-side"]
    dry_run += ["--dry-run=server"]
    code, out, err = run_argv(dry_run, cwd)
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
    # kubectl, never argv[0] — see validate_manifest's comment.
    code, out, err = run_argv(["kubectl"] + list(argv[1:]) + ["--dry-run=server"], cwd)
    if code != 0:
        text = (err or out).strip()
        if "unknown flag" in text or "unknown shorthand" in text:
            refuse("`%s` does not support a server-side dry run, so this change cannot be "
                   "validated as written. Express it as a manifest and apply that file "
                   "instead." % ("%s %s" % ("kubectl", verb)), attempt, consecutive)
        refuse("The API server rejected this change in a dry run:\n\n%s" % text,
               attempt, consecutive)
    return out.strip()


def validate_rollout_action(argv, verb_idx, cwd, attempt, consecutive):
    """rollout restart/pause/resume support no --dry-run at all, so validate by
    resolving the target and reporting its scope instead of dead-ending —
    the same pre-check shape validate_destructive uses, without the
    deletion-specific ownership/label checks (none of these three verbs can
    change desired state beyond recreating pods, so a resolve-and-report
    validation is the right trade against permanently refusing the canonical
    safe rolling restart with advice that cannot be followed: restart is a
    patch, not a manifest)."""
    items = resolve_target(argv, verb_idx, cwd, attempt, consecutive)
    return "%d resource(s) in scope." % len(items)


def resolve_target(argv, verb_idx, cwd, attempt, consecutive):
    """Resolve the verb's target with a plain `get` — never the mutating verb
    itself — and fail CLOSED: a non-zero exit, unparseable output, or zero
    identifiable items is refused rather than treated as "nothing to check".

    `doc.get("items", [doc]) if doc else []` used to default to `[]` — an
    empty, therefore skipped, per-item loop — whenever stdout was empty or
    not valid JSON, which is exactly what a zero-exit `kubectl` prints on a
    server hiccup (`{}` is falsy) or a plain-text server error (not JSON at
    all). Both let a delete's ownership/ephemeral-label checks run zero
    times and PROCEED. Provably resolving at least one real item is the
    inversion's own doctrine — "anything unproven is refused" — applied to
    the one validator that exists purely as a safety pre-check.
    """
    ops = operands(argv, verb_idx)
    # kubectl, never argv[0] — see validate_manifest's comment. This is the
    # probe an agent-planted shim was made to answer with a forged label.
    code, out, err = run_argv(["kubectl", "get"] + ops + ["-o", "json"], cwd)
    if code != 0:
        refuse("The target could not be resolved, so it cannot be checked:\n\n%s"
               % (err.strip() or out.strip()), attempt, consecutive)
    try:
        doc = json.loads(out)
    except ValueError:
        doc = None
    items = doc.get("items", [doc]) if isinstance(doc, dict) else None
    if not items or not all(
        isinstance(i, dict) and (i.get("metadata") or {}).get("name") for i in items
    ):
        refuse("The target's state could not be read, so it cannot be checked. "
               "The API server's response was not the JSON of an identifiable "
               "resource:\n\n%s" % out.strip(), attempt, consecutive)
    return items


def validate_helm(verb, argv, verb_idx, cwd, attempt, consecutive):
    ops = operands(argv, verb_idx)
    if verb in HELM_DESTRUCTIVE_VERBS:
        release, _ = first_operand(argv, verb_idx)
        if release is None or release is UNPROVABLE:
            refuse("The Helm release name could not be determined from this command "
                   "(a flag this guard does not recognise may be hiding it), so "
                   "`helm %s` cannot be validated." % verb, attempt, consecutive)
        code, out, err = run_argv(["helm", "history", release] + scope_flags(argv), cwd)
        if code != 0:
            refuse("No such Helm release, so `helm %s` cannot be validated:\n\n%s"
                   % (verb, (err or out).strip()), attempt, consecutive)
        return out.strip()
    # `helm plugin list` exits 0 even with ZERO plugins installed — verified
    # against the real binary, it prints only the header row — so gating on
    # its EXIT CODE (rather than whether "diff" actually appears in its
    # output) made the fallback branch dead code: every Helm change tried
    # `helm diff upgrade`, which does not exist without the plugin, and every
    # one hard-blocked with `unknown command "diff"`.
    diff_installed = False
    code, out, _ = run_argv(["helm", "plugin", "list"], cwd)
    if code == 0:
        for line in out.splitlines()[1:]:  # skip the header row
            fields = line.split()
            if fields and fields[0] == "diff":
                diff_installed = True
                break
    if diff_installed:
        preview = ["helm", "diff", "upgrade"] + ops
    else:
        # helm, never argv[0] — see validate_manifest's comment.
        preview = ["helm"] + list(argv[1:]) + ["--dry-run=server"]
    code, out, err = run_argv(preview, cwd)
    if code != 0:
        refuse("The Helm change could not be previewed:\n\n%s" % ((err or out).strip()),
               attempt, consecutive)
    return out.strip()


def validate_destructive(argv, verb_idx, agent, cwd, attempt, consecutive):
    if has_flag(argv, "--all") or not bare_operands(argv, verb_idx):
        refuse("This deletion names no specific resource, so its blast radius is "
               "unbounded. Name the resources to delete explicitly.", attempt, consecutive)
    items = resolve_target(argv, verb_idx, cwd, attempt, consecutive)
    for item in items:
        meta = item["metadata"]
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
    return "%d resource(s) would be deleted." % len(items)


def validate(tool, verb, argv, verb_idx, agent, cwd, attempt, consecutive, attestations):
    # The reviewer may never reach here for a command it can sign off on
    # itself. This is checked FIRST — before check_production, before any
    # mechanical kubectl/helm call, before subject_hash — so nothing about
    # THIS change (least of all its subject, the one thing that makes the
    # check_attested chain reachable at all) is ever computed or disclosed to
    # the reviewer. It only ever fires here, never at the top of main(): a
    # command main() already proved provably_read_only() `continue`s before
    # reaching validate() at all, so the reviewer's read-only kubectl (its
    # entire job) is untouched. Terminal — emit("deny", …), never refuse():
    # refuse() escalates to "ask" after MAX_ATTEMPTS, which would let the
    # reviewer ask the USER for permission to sign its own work, exactly the
    # hole check_attested's own identical rule closes for a missing
    # attestation.
    if agent in VALIDATOR_AGENTS:
        emit("deny",
             "The reviewer agent may not make changes to the cluster, only review "
             "them. Run this command as the agent that requested the review, not "
             "as k8s_validator.")
    check_production(argv, attempt, consecutive)
    if tool == "helm":
        preview = validate_helm(verb, argv, verb_idx, cwd, attempt, consecutive)
    elif verb in DESTRUCTIVE_VERBS:
        preview = validate_destructive(argv, verb_idx, agent, cwd, attempt, consecutive)
    elif verb in APPLY_VERBS:
        preview = validate_manifest(verb, argv, cwd, attempt, consecutive)
    elif verb == "rollout":
        # sub_idx (not verb_idx) is what operands()/resolve_target must walk
        # from: verb_idx still points at "rollout" itself, so operands(argv,
        # verb_idx) would include the SUB-verb ("restart") as if it were the
        # first resource operand — `kubectl get restart deploy/x -n demo`,
        # which cannot resolve anything. subverb_of already computes this
        # walk internally and throws its index away; do it once here instead.
        sub, sub_idx = _verb_after(argv, verb_idx)
        if sub in ROLLOUT_SCOPE_VERBS:
            preview = validate_rollout_action(argv, sub_idx, cwd, attempt, consecutive)
        else:
            preview = validate_imperative(argv, verb, cwd, attempt, consecutive)
    elif verb in IMPERATIVE_VERBS:
        preview = validate_imperative(argv, verb, cwd, attempt, consecutive)
    else:
        refuse("`%s %s` changes the cluster but has no validation rule, so it is refused "
               "rather than applied unchecked." % (tool, verb), attempt, consecutive)
    # Mechanical validation passed for THIS segment. Bind ITS OWN subject to
    # the manifest/chart target's actual content now (see local_target_digest /
    # helm_content_digest) — deliberately AFTER the per-verb branch above, not
    # before: that branch already proved (via kubectl diff / a dry run, or
    # helm's own diff/dry-run) that the target is renderable, so by the time
    # we independently re-render/re-read it here it is expected to succeed; a
    # URL target, or a render failure, still TERMINALLY refuses here even
    # though the mechanical step just "successfully" previewed it (it fetched
    # or rendered it; this guard refuses to bind an attestation to content it
    # cannot pin down for a future re-check).
    if tool == "helm":
        content_digest = helm_content_digest(verb, argv, verb_idx, cwd)
    else:
        content_digest = local_target_digest(argv, cwd)
    subject = subject_hash(tool, verb, argv, content_digest)
    # Now require the reviewer's verdict for THIS segment's own subject.
    # Checked here (not once after the loop) so a missing/rejected
    # attestation denies at the first mutating segment it reaches — the same
    # fail-fast shape every mechanical check above already has — while a
    # successful check returns normally instead of ending the run, so
    # main()'s loop still reaches any FURTHER mutating segment in a compound
    # command, mechanically validates it, and requires ITS OWN attestation
    # too (see the `validated` comment in main — one shared subject for a
    # whole compound line was the earlier, less precise design).
    check_attested(argv, attestations, subject)
    return preview


if __name__ == "__main__":
    main()
