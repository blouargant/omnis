package packaging

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/hookstate"
)

// script returns the path to the shipped hook script.
func script(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the hook script assumes a POSIX environment")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	return filepath.Join("..", "config", "hooks", "k8s-validate.py")
}

// runHook pipes a hook input into the script and returns its stdout and exit code.
// extraPath is prepended to PATH, so a test can shadow kubectl/helm with a stub.
func runHook(t *testing.T, in map[string]any, extraPath string) (string, int) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cmd := exec.Command("python3", "-B", script(t))
	cmd.Stdin = bytes.NewReader(data)
	if extraPath != "" {
		// Prepend, so a stub shadows any real kubectl/helm on the machine.
		cmd.Env = append(os.Environ(), "PATH="+extraPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	_ = cmd.Run()
	// A traceback on stderr would otherwise read as "no opinion" and pass
	// silently, which is the worst possible failure for a guard.
	if errb.Len() > 0 {
		t.Fatalf("hook wrote to stderr: %s", errb.String())
	}
	return out.String(), cmd.ProcessState.ExitCode()
}

func decisionOf(t *testing.T, stdout string) string {
	t.Helper()
	if strings.TrimSpace(stdout) == "" {
		return ""
	}
	var jo struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			Reason             string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &jo); err != nil {
		// A systemMessage-only payload is a proceed with a note.
		return ""
	}
	return jo.HookSpecificOutput.PermissionDecision
}

func reasonOf(t *testing.T, stdout string) string {
	t.Helper()
	var jo struct {
		HookSpecificOutput struct {
			Reason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	_ = json.Unmarshal([]byte(stdout), &jo)
	return jo.HookSpecificOutput.Reason
}

func bashInput(command, agent string) map[string]any {
	return map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": command},
		"agent_name":      agent,
		"attempt":         1,
		"consecutive":     0,
	}
}

// stubBin writes an executable stub named name into a fresh dir and returns the
// dir, for prepending to PATH. body is a /bin/sh script.
func stubBin(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return dir
}

// The stub kubectl/helm behaviours a commandShapeCases row can request via
// commandShapeStubs, so a row whose command reaches validate()'s real
// subprocess calls gets a DETERMINISTIC outcome instead of depending on this
// machine's kubectl/helm installation or cluster reachability.
//
// The default is HERMETIC, not the real PATH. An unlisted row used to fall
// through to "" (the real kubectl/helm), which made hermeticity depend on
// every row that reaches a subprocess call being remembered and opted in by
// hand — an enumeration exactly like the ones this file's own doctrine
// warns against. Review round 1 audited the table and stubbed 24 such rows;
// round 2's re-review found FOUR MORE round 1 missed ("newline-separated",
// "ampersand-separated", "rollout undo writes", "value flag hides rollout
// undo" — all target a non-production namespace and reach a real subprocess
// call, verified live: a stub kubectl returning a resolvable target makes
// all three PROCEED). An opt-in stub can always be under-enumerated by the
// next row added; an opt-in ESCAPE from a hermetic default cannot, because
// forgetting to opt in now means "deterministically denies", never "silently
// depends on this machine". So stubFor NEVER returns the real PATH: an
// unlisted row (commandShapeStubs[name] == "") gets stubDefaultDeny, a
// kubectl/helm that fails for ANY invocation. A row that genuinely needs a
// specific canned response still opts in via commandShapeStubs, as before.
const (
	stubDefaultDeny       = "default-deny"       // unlisted rows: kubectl/helm fail deterministically for anything
	stubKubectlDiffFails  = "kubectl-diff-fail"  // validate_manifest's `kubectl diff` step fails (exit 2)
	stubKubectlGetFails   = "kubectl-get-fail"   // resolve_target's `kubectl get` fails (exit 1)
	stubHelmUnpreviewable = "helm-unpreviewable" // validate_helm: no release, and no diff plugin
)

// stubBodies gives the /bin/sh script body for each stub key above.
var stubBodies = map[string]string{
	stubDefaultDeny: "exit 1",
	stubKubectlDiffFails: `
case "$*" in
  *diff*) exit 2 ;;
  *)      exit 0 ;;
esac`,
	stubKubectlGetFails: `
case "$*" in
  *get*) exit 1 ;;
  *)     exit 0 ;;
esac`,
	stubHelmUnpreviewable: `
case "$*" in
  *history*) exit 1 ;;
  *plugin*)  printf 'NAME\tVERSION\n' ;;
  *)         exit 1 ;;
esac`,
}

// stubFor installs the script for key — or, for "" (a row absent from
// commandShapeStubs), stubDefaultDeny — as BOTH kubectl and helm in a fresh
// dir and returns it for PATH. It never returns "": see the doc comment
// above the stub-key constants for why an unlisted row must not reach the
// real PATH. A row only ever exercises one of the two tools, so installing
// both under one fixed body is harmless and keeps commandShapeStubs to one
// line per row.
func stubFor(t *testing.T, key string) string {
	t.Helper()
	if key == "" {
		key = stubDefaultDeny
	}
	body, ok := stubBodies[key]
	if !ok {
		t.Fatalf("unknown stub key %q", key)
	}
	dir := t.TempDir()
	for _, bin := range []string{"kubectl", "helm"} {
		p := filepath.Join(dir, bin)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", bin, err)
		}
	}
	return dir
}

// approvedFor builds an attestations map whose single key is the subject hash
// of in["tool_input"], with verdict APPROVED. The hash must match
// hookstate.HashArgs — the Python hook script's own subject computation (wired
// in Task 10) is required to agree with it exactly.
func approvedFor(t *testing.T, in map[string]any) map[string]any {
	t.Helper()
	ti, ok := in["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_input is not a map[string]any: %#v", in["tool_input"])
	}
	subject := hookstate.HashArgs(ti)
	return map[string]any{
		subject: map[string]any{"verdict": "APPROVED"},
	}
}

// The guard's rule is an INVERSION: a command is allowed only when it is provably
// read-only, and anything else naming kubectl or helm is refused. Three earlier
// rounds tried to recognise mutations instead, and each closed the cases it was
// shown while the class stayed open — `bash -c "kubectl delete …"` is the obvious
// next thing an agent tries after a deny.
//
// So this table asserts the DECISION and, where a wrong-reason deny would be
// indistinguishable from a right one, a substring of the REASON. Asserting only
// the decision is how a `${VAR}` read refused as "substitutes another command"
// previously looked correct.
// commandShapeCases is package-level so TestProceedsAreInertUnderBash can hold
// every proceed row to the stronger standard below. A decision-only table cannot
// see the defect that matters most: four families of command PROCEEDED here and
// still mutated a cluster under bash, and each was found in minutes by piping the
// line into a shell with a stub kubectl on PATH.
var commandShapeCases = []struct {
	name, command, agent, want, wantReason string
}{
	// ---- provably read-only: these must proceed ----
	{"bare -A before a read", "kubectl -A get pods", "k8s_investigator", "", ""},
	{"long boolean global", "kubectl --all-namespaces get pods", "k8s_investigator", "", ""},
	{"value-taking global", "kubectl -n demo get pods", "k8s_investigator", "", ""},
	{"inline value global", "kubectl --context=prod get pods", "k8s_investigator", "", ""},
	{"known value flag consumed", "kubectl --as-uid 1000 get pods", "k8s_investigator", "", ""},
	{"helm value flag consumed", "helm --registry-config /tmp/r.yaml list -n demo", "k8s_investigator", "", ""},
	{"read with a redirect", "kubectl get pods -o json > /tmp/pods.json", "k8s_investigator", "", ""},
	{"read with 2>&1", "kubectl get pods 2>&1", "k8s_investigator", "", ""},
	{"timeout wrapper before a read", "timeout 30 kubectl get pods -n demo", "k8s_investigator", "", ""},
	{"timeout with a duration operand", "timeout 60s kubectl rollout status deploy/x -n demo", "k8s_investigator", "", ""},
	{"nice wrapper before a read", "nice -n 10 kubectl get pods", "k8s_investigator", "", ""},
	// A brace expansion hides no runnable command, and it is the commonest
	// idiom in model-written shell. Refusing it dead-ended the turn, because
	// "run the inner command on its own" is unfollowable for `-n ${NS}`.
	{"braced variable in a read", "kubectl get pods -n ${NS}", "k8s_investigator", "", ""},
	{"quoted braced variable", `kubectl logs deploy/x -n "${NAMESPACE}" --tail=100`, "k8s_investigator", "", ""},
	{"unbraced variable in a read", "kubectl get pods -n $NS", "k8s_investigator", "", ""},
	// Verbs that change nothing outside this machine.
	{"kustomize renders locally", "kubectl kustomize ./overlays/dev", "coder", "", ""},
	{"completion prints a script", "kubectl completion bash", "coder", "", ""},
	{"port-forward opens a tunnel", "kubectl port-forward svc/x 8080:80 -n demo", "k8s_investigator", "", ""},
	// Sub-verbs: the verb alone proves nothing.
	{"auth can-i reads", "kubectl auth can-i create pods", "k8s_investigator", "", ""},
	{"config view reads", "kubectl config view", "k8s_investigator", "", ""},
	{"rollout status reads", "kubectl rollout status deploy/x -n demo", "k8s_investigator", "", ""},
	{"rollout history reads", "kubectl rollout history deploy/x -n demo", "k8s_investigator", "", ""},
	// A value flag between the verb and its sub-verb must not shift the
	// sub-verb. This direction denied an honest read.
	{"value flag before a read sub-verb", "kubectl auth -n demo can-i create pods", "k8s_investigator", "", ""},
	{"value flag before config view", "kubectl config --kubeconfig /tmp/k view", "k8s_investigator", "", ""},
	{"value flag before rollout status", "kubectl rollout -n demo status deploy/x", "k8s_investigator", "", ""},
	// helm reads and aliases. `hist`, `fetch` and `inspect` are live aliases;
	// omitting them denied honest work.
	{"helm list", "helm list -n demo", "k8s_investigator", "", ""},
	{"helm ls alias", "helm ls -n demo", "k8s_investigator", "", ""},
	{"helm hist alias", "helm hist myrel -n demo", "k8s_investigator", "", ""},
	{"helm pull is local", "helm pull oci://reg/chart", "k8s_investigator", "", ""},
	{"helm fetch alias", "helm fetch oci://reg/chart", "k8s_investigator", "", ""},
	{"helm inspect alias", "helm inspect chart ./c", "k8s_investigator", "", ""},
	{"helm repo add is local", "helm repo add x https://example.com", "k8s_investigator", "", ""},
	// ---- not ours at all: these must proceed ----
	{"not kubernetes", "go test ./...", "coder", "", ""},
	{"grep names kubectl", "grep -rn kubectl deploy.sh > /tmp/out", "coder", "", ""},
	{"echo names a delete", "echo kubectl delete pod x -n demo", "coder", "", ""},
	{"bare shell comment", "# kubectl delete pod x", "coder", "", ""},
	{"read then a trailing comment", "kubectl get pods -n demo ; # kubectl delete pod x", "k8s_investigator", "", ""},
	// A hyphen is a word boundary, so a naive \b regex matched inside these
	// and refused honest scripts and paths.
	{"helm inside a script name", "sudo -u jenkins /opt/scripts/deploy-helm.sh", "linux_admin", "", ""},
	{"helm inside a package name", "sudo -u root apt-get install foo-overwhelming", "linux_admin", "", ""},
	{"helm inside a path", "cat /etc/helm/values.yaml", "coder", "", ""},
	{"helm-named audit script", "timeout 300 ./helm-audit.sh", "linux_admin", "", ""},
	{"read then an unrelated sudo", "kubectl rollout status deploy/x -n demo && sudo -u root systemctl restart nginx", "linux_admin", "", ""},
	{"read then a build", "kubectl get pods -n demo && timeout 30 make build", "coder", "", ""},
	// ---- mutations: these reach validate, which refuses in the skeleton ----
	// These must reach the Task-9 stub, not merely deny. Asserting the
	// reason turns a decision check into a PATH check: if wrapper stripping
	// regressed, `sudo -u root kubectl delete` would deny as "does not
	// invoke it directly", the table would stay green, and Task 9's
	// validation would silently never run on any wrapped command.
	{"boolean global before apply", "kubectl --insecure-skip-tls-verify apply -f app.yaml", "k8s_editor", "deny", "could not be previewed"},
	{"helm boolean global before upgrade", "helm --debug upgrade myrel ./chart -n demo", "k8s_editor", "deny", "The Helm change could not be previewed"},
	{"helm boolean global before uninstall", "helm --debug uninstall myrel -n demo", "k8s_editor", "deny", "No such Helm release"},
	{"sudo with its own flag", "sudo -n kubectl delete pod x -n demo", "k8s_cleaner", "deny", "could not be resolved"},
	{"absolute-path wrapper", "/usr/bin/sudo kubectl delete pod x -n demo", "k8s_cleaner", "deny", "could not be resolved"},
	{"wrapper flag with a value", "sudo -u root kubectl delete pod x -n demo", "k8s_cleaner", "deny", "could not be resolved"},
	{"newline-separated", "kubectl get pods\nkubectl delete pod x -n demo", "k8s_cleaner", "deny", "could not be resolved"},
	{"ampersand-separated", "kubectl get pods & kubectl delete pod x -n demo", "k8s_cleaner", "deny", "could not be resolved"},
	{"xargs kubectl delete", "kubectl get pods -o name | xargs kubectl delete pod -n demo", "k8s_cleaner", "deny", "could not be resolved"},
	{"auth reconcile writes RBAC", "kubectl auth reconcile -f rbac.yaml", "k8s_editor", "deny", ""},
	{"config use-context rewrites kubeconfig", "kubectl config use-context prod", "k8s_editor", "deny", ""},
	{"rollout undo writes", "kubectl rollout undo deploy/x -n demo", "k8s_editor", "deny", "rejected this change in a dry run"},
	{"exec is not a read", "kubectl exec pod -- rm -rf /data", "k8s_editor", "deny", ""},
	{"helm test creates Pods", "helm test prod-app -n prod", "k8s_editor", "deny", ""},
	// helm's canonical destructive verb under each of its live aliases. All
	// three previously proceeded behind a wrapper and uninstalled unvalidated.
	{"helm delete alias behind sudo", "sudo helm delete myrel -n prod", "k8s_editor", "deny", "looks like production"},
	{"helm del alias behind sudo", "sudo helm del myrel -n prod", "k8s_editor", "deny", "looks like production"},
	{"helm un alias behind sudo", "sudo helm un myrel -n prod", "k8s_editor", "deny", "looks like production"},
	{"helm delete behind timeout", "timeout 60 helm delete myrel -n prod", "k8s_editor", "deny", ""},
	// A value flag shifting the sub-verb in the OPEN direction: these ran a
	// rollback and an RBAC write unvalidated.
	{"value flag hides rollout undo", "kubectl rollout -n status undo deploy/x", "k8s_editor", "deny", "rejected this change in a dry run"},
	{"value flag hides auth reconcile", "kubectl auth -n can-i reconcile -f rbac.yaml", "k8s_editor", "deny", ""},
	{"value flag hides config use-context", "kubectl config --context view use-context prod", "k8s_editor", "deny", ""},
	{"unknown value flag fails closed", "kubectl --totally-unknown-flag zzz get pods", "k8s_investigator", "deny", ""},
	{"krew behind a wrapper", "sudo kubectl krew install x", "k8s_editor", "deny", ""},
	// ---- unreadable shapes: refused, with the reason that explains why ----
	// A substitution hides a nested command whatever the OUTER word is. Every
	// earlier version gated this behind "is the outer command kubectl?", so
	// the first two of these proceeded and the nested delete executed.
	{"substitution under echo", "echo $(kubectl delete pod x -n demo)", "k8s_editor", "deny", "substitutes another command"},
	{"substitution in an assignment", "POD=$(kubectl delete pod x -n demo)", "k8s_editor", "deny", "substitutes another command"},
	{"backticks under echo", "true && echo `kubectl delete pod v -n demo`", "k8s_editor", "deny", "substitutes another command"},
	{"substitution under kubectl", "kubectl get pods $(kubectl delete pod x -n demo)", "k8s_editor", "deny", "substitutes another command"},
	// A quoted payload is one token, so no amount of basename matching sees
	// the command inside it. This is the shape an agent reaches for next.
	{"bash -c payload", `bash -c "kubectl delete pod x -n demo"`, "k8s_editor", "deny", "another program to execute"},
	{"sh -c payload", "sh -c 'kubectl delete pod x -n demo'", "k8s_editor", "deny", "another program to execute"},
	{"eval payload", `eval "kubectl delete pod x -n demo"`, "k8s_editor", "deny", "another program to execute"},
	{"nohup bash -c payload", "nohup bash -c 'helm uninstall prod -n prod'", "k8s_editor", "deny", "another program to execute"},
	// A remote invocation would otherwise be validated against the LOCAL
	// cluster and attested on that basis.
	{"ssh remote delete", "ssh prod-host kubectl delete pod x -n demo", "k8s_editor", "deny", "another program to execute"},
	{"heredoc apply", "kubectl apply -f - <<EOF\nkind: Pod\nEOF", "k8s_editor", "deny", "redirection or heredoc"},

	// ---- a verb-scoped value flag must not shift the sub-verb ----
	// Verified against the real binary: cobra's stripFlags consumes
	// `--field-manager status` as an unknown-long-flag-with-value, so this
	// resolves to `restart` and restarts a named Deployment. It proceeded
	// while the sub-verb walk only knew GLOBAL value flags.
	{"field-manager hides rollout restart", "kubectl rollout --field-manager status restart deploy/x -n demo", "k8s_editor", "deny", "does not recognise"},
	{"selector hides rollout restart", "kubectl rollout -l status restart deployment -n demo", "k8s_editor", "deny", "does not recognise"},
	{"selector hides rollout undo", "kubectl rollout -l status undo deployment -n demo", "k8s_editor", "deny", "does not recognise"},
	{"unknown flag fails closed", "kubectl --totally-unknown-flag zzz get pods", "k8s_investigator", "deny", "does not recognise"},

	// ---- a command that EXECUTES a string it is handed ----
	// awk's system(), GNU sed's `e`, and every shell wrapper. All three were
	// on an "inert commands" list and proceeded; the reviewer proved each one
	// executes. Enumerating what launches is the bounded direction.
	{"awk system()", `awk 'BEGIN{system("kubectl delete pod x -n demo")}'`, "k8s_editor", "deny", "another program to execute"},
	{"sed e command", `sed '1e kubectl delete pod x -n demo' /etc/hostname`, "k8s_editor", "deny", "another program to execute"},
	{"python -c", `python3 -c "import os; os.system('kubectl delete pod x')"`, "k8s_editor", "deny", "another program to execute"},
	{"watch with a quoted mutation", `watch "kubectl delete pod x -n demo"`, "k8s_editor", "deny", "another program to execute"},
	{"xargs into a shell", `xargs sh -c 'kubectl delete pod x -n demo'`, "k8s_cleaner", "deny", "another program to execute"},

	// ---- a suffixed binary name is a real install, not an evasion ----
	// A trailing \b made the whole guard exit on these.
	{"helm3 uninstall", "helm3 uninstall myrel -n prod", "k8s_editor", "deny", "looks like production"},
	{"helm2 delete", "helm2 delete myrel", "k8s_editor", "deny", "The Helm change could not be previewed"},
	{"suffixed kubectl path", "/usr/local/bin/kubectl.real delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"read then a suffixed mutation", "kubectl get pods; helm3 uninstall myrel -n prod", "k8s_editor", "deny", "looks like production"},

	// ---- proxy hands out a credentialed API endpoint ----
	// The process changes nothing; what it enables is a full read-write API
	// on localhost, and the follow-up curl names neither tool.
	{"proxy is not a read", "kubectl proxy --port=8001", "k8s_investigator", "deny", "no validation rule"},

	// ---- shell control flow around an ordinary READ ----
	// Five shapes, all reads. Refusing these is the investigator's normal
	// register and is how a guard gets removed from hooks.json.
	{"for/do loop", "for ns in demo prod; do kubectl get pods -n $ns; done", "k8s_investigator", "", ""},
	{"if condition", "if kubectl get pod x -n demo >/dev/null; then echo present; fi", "k8s_investigator", "", ""},
	{"while negation", "while ! kubectl get pod x -n demo; do sleep 2; done", "k8s_investigator", "", ""},
	{"subshell", "( kubectl get pods -n demo )", "k8s_investigator", "", ""},
	{"pipeline into a while loop", "kubectl get ns -o name | while read n; do kubectl get pods -n $n; done", "k8s_investigator", "", ""},
	{"time builtin", "time kubectl get pods -n demo", "k8s_investigator", "", ""},
	{"exec builtin", "exec kubectl get pods", "k8s_investigator", "", ""},

	// ---- env assignments, a silent regression from an earlier round ----
	{"env with an assignment", "env KUBECONFIG=/tmp/kc kubectl get pods -n demo", "k8s_investigator", "", ""},
	{"bare assignment prefix", "KUBECONFIG=/tmp/kc kubectl get pods -n demo", "k8s_investigator", "", ""},

	// ---- no verb at all is nothing to validate ----
	{"bare kubectl", "kubectl", "coder", "", ""},
	{"kubectl help", "kubectl --help", "coder", "", ""},
	{"helm help", "helm --help", "coder", "", ""},
	{"version", "kubectl --version", "coder", "", ""},

	// ---- naming the word without executing it ----
	// The mitigation for these used to be an inert list with no natural end.
	{"gh pr title quoting it", `gh pr create --title "fix the kubectl guard" --body x`, "coder", "", ""},
	{"git commit quoting it", `git commit -m "add kubectl delete step"`, "coder", "", ""},
	{"ls the binary", "ls -l /usr/local/bin/kubectl", "coder", "", ""},
	{"which the binary", "which kubectl", "coder", "", ""},
	{"command -v", "command -v kubectl", "coder", "", ""},

	// ---- round-6 regressions: each of these PROCEEDED and then ran the
	// mutation under bash. They are grouped by root cause, because each cause
	// was a hole in one enumerable set rather than a parsing subtlety.

	// A wrapper stripped without consuming its own flag leaves that flag at the
	// head, so classify returned None and the segment was SPARED. `time` was in
	// SHELL_KEYWORDS, which is consulted first, making its WRAPPER_SPEC entry
	// dead code that read as coverage. These now reach validate with the REAL
	// verb — a stronger outcome than refusing them as unidentifiable, and the
	// reason proves the wrapper's own flag was consumed rather than guessed.
	{"time -p hides a delete", "time -p kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"time -o hides a delete", "/usr/bin/time -o /tmp/t kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"time -p hides an uninstall", "time -p helm uninstall myrel -n prod", "k8s_editor", "deny", "looks like production"},
	{"xargs -a hides a delete", "xargs -a /tmp/l kubectl delete pod -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"sudo --chroot hides a delete", "sudo -R / kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"sudo --chdir hides a delete", "sudo -D /tmp kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},

	// When stripping does NOT land on a bare program name, identification has
	// failed and the inversion refuses. flock's `-c` genuinely hands a string to
	// sh; the other two have had the binary eaten by the wrapper's value flag.
	{"flock -c hands a string to sh", "flock /tmp/l -c 'kubectl delete pod x -n demo'", "k8s_editor", "deny", "could not be identified"},
	{"env -S swallows the payload", "env -S 'kubectl delete pod x -n demo'", "k8s_editor", "deny", "could not be identified"},
	{"value flag eats the binary", "sudo -u kubectl", "k8s_editor", "deny", "could not be identified"},
	{"flock without -c execs argv", "flock /tmp/l kubectl get pods", "k8s_investigator", "", ""},

	// Grouping punctuation glued to the binary defeated the ^-anchored basename
	// test. The spaced `( kubectl … )` row above was green throughout, which is
	// exactly why the glued form was never generated.
	{"paren glued to the binary", "(kubectl delete pod x -n demo)", "k8s_editor", "deny", "could not be resolved"},
	{"paren glued, helm", "(helm uninstall myrel -n prod)", "k8s_editor", "deny", "looks like production"},
	{"paren glued after a read", "kubectl get pods -n demo; (kubectl delete pod x -n demo)", "k8s_editor", "deny", "could not be resolved"},
	{"paren glued inside a loop", "for i in 1; do (kubectl delete pod x -n demo); done", "k8s_editor", "deny", "could not be resolved"},
	{"paren mid-token after if", "if(kubectl delete pod x -n demo); then echo y; fi", "k8s_editor", "deny", "could not be resolved"},

	// >( is the same execute-a-nested-command shape as <(, which was listed.
	{"output process substitution", "echo hi > >(kubectl delete pod x -n demo)", "k8s_editor", "deny", "substitutes another command"},
	{"tee into a process substitution", "tee >(kubectl apply -f app.yaml) < app.yaml", "k8s_editor", "deny", "substitutes another command"},

	// Wrappers that exec argv[1..]: the verb is in plain sight, there was simply
	// no entry, so the segment was spared instead of scrutinised.
	{"setsid execs argv", "setsid kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"setsid execs argv, helm", "setsid helm uninstall myrel -n prod", "k8s_editor", "deny", "looks like production"},
	{"taskset execs argv", "taskset -c 0 kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"taskset before a read", "taskset -c 0 kubectl get pods -n demo", "k8s_investigator", "", ""},
	{"chrt takes a priority operand", "chrt -f 10 kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"unshare execs argv", "unshare -r kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"strace execs argv", "strace -f kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"systemd-run execs argv", "systemd-run --scope kubectl delete pod x -n demo", "k8s_editor", "deny", "could not be resolved"},
	{"busybox launches a shell", "busybox sh -c 'kubectl delete pod x'", "k8s_editor", "deny", "another program to execute"},
}

// commandShapeStubs names, by row name, the stub kubectl/helm behaviour a row
// needs — see stubFor. A row absent here is not exempt from stubbing: it
// simply gets stubFor's default (stubDefaultDeny) — either because it never
// reaches validate()'s subprocess calls at all (refused earlier by
// identification, or by a static message with no subprocess involved, e.g.
// check_production or the "no validation rule" catch-all), in which case the
// default is never even invoked, or because "kubectl/helm fail
// deterministically for anything" is exactly the outcome that row wants.
// Kept as a separate map, rather than a new field on commandShapeCases, so
// adding a stub never touches the ~150 existing row literals.
var commandShapeStubs = map[string]string{
	"boolean global before apply":          stubKubectlDiffFails,
	"helm boolean global before upgrade":   stubHelmUnpreviewable,
	"helm boolean global before uninstall": stubHelmUnpreviewable,
	"sudo with its own flag":               stubKubectlGetFails,
	"absolute-path wrapper":                stubKubectlGetFails,
	"wrapper flag with a value":            stubKubectlGetFails,
	"xargs kubectl delete":                 stubKubectlGetFails,
	"helm2 delete":                         stubHelmUnpreviewable,
	"suffixed kubectl path":                stubKubectlGetFails,
	"paren glued to the binary":            stubKubectlGetFails,
	"paren glued after a read":             stubKubectlGetFails,
	"paren glued inside a loop":            stubKubectlGetFails,
	"paren mid-token after if":             stubKubectlGetFails,
	"setsid execs argv":                    stubKubectlGetFails,
	"taskset execs argv":                   stubKubectlGetFails,
	"chrt takes a priority operand":        stubKubectlGetFails,
	"unshare execs argv":                   stubKubectlGetFails,
	"strace execs argv":                    stubKubectlGetFails,
	"systemd-run execs argv":               stubKubectlGetFails,
	"time -p hides a delete":               stubKubectlGetFails,
	"time -o hides a delete":               stubKubectlGetFails,
	"xargs -a hides a delete":              stubKubectlGetFails,
	"sudo --chroot hides a delete":         stubKubectlGetFails,
	"sudo --chdir hides a delete":          stubKubectlGetFails,
}

func TestDecisionForCommandShapes(t *testing.T) {
	for _, c := range commandShapeCases {
		t.Run(c.name, func(t *testing.T) {
			dir := stubFor(t, commandShapeStubs[c.name])
			out, code := runHook(t, bashInput(c.command, c.agent), dir)
			if got := decisionOf(t, out); got != c.want {
				t.Fatalf("decision = %q, want %q (exit=%d stdout=%q)", got, c.want, code, out)
			}
			if c.want == "" && strings.TrimSpace(out) != "" {
				t.Fatalf("a proceed must emit nothing, got %q", out)
			}
			if c.wantReason != "" && !strings.Contains(reasonOf(t, out), c.wantReason) {
				t.Fatalf("reason = %q, want it to mention %q", reasonOf(t, out), c.wantReason)
			}
		})
	}
}

// mutatingVerbs names the kubectl/helm verbs that actually change cluster
// state. Shared by TestGuardOnlyEverPreviews (a recorded invocation whose verb
// is here must carry a --dry-run marker) and TestProceedsAreInertUnderBash (a
// proceed row must never reach one of these at all). A mutation spelled with a
// verb absent here is not detected by either oracle — the same enumeration
// risk the guard itself carries, which is why the sets are shared in spirit
// with config/hooks/k8s-validate.py.
var mutatingVerbs = map[string]bool{
	"delete": true, "apply": true, "create": true, "replace": true, "patch": true,
	"scale": true, "annotate": true, "label": true, "taint": true, "drain": true,
	"cordon": true, "uncordon": true, "edit": true, "set": true, "expose": true,
	"run": true, "attach": true, "cp": true, "debug": true, "certificate": true,
	"exec": true, "install": true, "upgrade": true, "uninstall": true, "del": true,
	"un": true, "rollback": true, "push": true, "restart": true, "undo": true,
	"pause": true, "reconcile": true,
}

// Round 0's worst failure mode was the guard executing the very mutation it
// was inspecting, and nothing tested for it. This test used to assert the
// guard ran NOTHING — true only while validate() was the Task 8 stub. Task 9
// gives validate() real work: a manifest apply is diffed and server
// dry-run'd, a destructive delete is pre-checked with a plain `get`, so
// "executes nothing" is no longer the property that holds for a mutating
// command. The property that must ALWAYS hold — renamed to say so — is
// narrower but no weaker: a provably read-only command still causes no
// kubectl/helm execution at all, and for a mutating command every kubectl/
// helm invocation the guard makes is either an inherently non-mutating verb
// (get, diff, history, plugin list, …) or carries a --dry-run marker — never
// the bare mutating verb running for real. A stub that records its own
// invocation, as before, makes that detectable.
func TestGuardOnlyEverPreviews(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked.log")
	for _, bin := range []string{"kubectl", "helm"} {
		body := "#!/bin/sh\necho \"$0 $*\" >> " + marker + "\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", bin, err)
		}
	}

	// A provably read-only command must still cause NO kubectl/helm execution
	// at all — this half of the original property is unchanged by Task 9.
	runHook(t, bashInput("kubectl get pods -n demo", "k8s_editor"), dir)
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("a provably read-only command executed kubectl/helm:\n%s", b)
	}

	// These four are expected to invoke kubectl/helm now — that is the whole
	// point of Task 9. What must never appear among the recorded calls is the
	// bare mutating verb running for real. Both helm commands deliberately
	// target a NON-production namespace: review round 1 found that "-n prod"
	// made check_production intercept before validate_helm ever ran, so this
	// oracle asserted NOTHING WHATSOEVER about the validators that append a
	// preview flag to an argv already naming a mutating verb — exactly the
	// shape this test exists to catch. Round 2's re-review found that
	// "helm uninstall" alone only exercises validate_helm's DESTRUCTIVE
	// branch (the `helm history` pre-check, which names no mutating verb at
	// all) — never the non-destructive branch that appends --dry-run=server
	// to an argv that already names "upgrade"/"install", which is the one
	// place a bug could run the bare mutation for real. "helm upgrade" closes
	// that gap.
	for _, cmd := range []string{
		"kubectl apply -f app.yaml",
		"kubectl delete pod x -n demo",
		"helm uninstall myrel -n demo",
		"helm upgrade myrel ./chart -n demo",
	} {
		runHook(t, bashInput(cmd, "k8s_editor"), dir)
	}

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("none of the four mutating commands invoked kubectl/helm at all — " +
			"validate() should have previewed each of them")
	}
	for _, rec := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		fields := strings.Fields(rec)
		if len(fields) < 2 {
			continue
		}
		verb := verbPosition(fields[1:])
		if !mutatingVerbs[verb] {
			continue // get, diff, history, plugin list, … — inherently safe reads
		}
		if !strings.Contains(rec, "dry-run") {
			t.Fatalf("the guard ran the bare mutation instead of previewing it:\n  %s", rec)
		}
	}
}

// The launcher list is now an execution surface of its own: every command on it
// is one the guard refuses BECAUSE it executes a string. Nothing tested that
// property, and an "inert commands" list carrying awk, sed and git was how three
// proven bypasses shipped. This applies TestGuardExecutesNoSubprocess's trick to
// the launcher list: shadow each launcher with a stub that records itself, and
// assert the guard refused rather than letting the launcher run.
func TestLaunchersAreRefusedNotExecuted(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked.log")
	launchers := []string{"bash", "sh", "eval", "awk", "sed", "python3", "ssh", "watch"}
	for _, bin := range launchers {
		body := "#!/bin/sh\necho \"$0 $*\" >> " + marker + "\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", bin, err)
		}
	}
	for _, cmd := range []string{
		`bash -c "kubectl delete pod x -n demo"`,
		`sh -c 'kubectl delete pod x -n demo'`,
		`awk 'BEGIN{system("kubectl delete pod x -n demo")}'`,
		`sed '1e kubectl delete pod x -n demo' /etc/hostname`,
		`python3 -c "import os; os.system('kubectl delete pod x')"`,
		"ssh prod-host kubectl delete pod x -n demo",
		`watch "kubectl delete pod x -n demo"`,
	} {
		out, _ := runHook(t, bashInput(cmd, "k8s_editor"), dir)
		if got := decisionOf(t, out); got != "deny" {
			t.Fatalf("%s: decision = %q, want deny", cmd, got)
		}
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the guard executed a launcher while deciding:\n%s", b)
	}
}

// A malformed tool_input must not produce a traceback. The engine turns a
// non-zero exit into a block for a fail_closed hook, which is the right
// direction, but runHook fatals on any stderr and this file's own comment calls a
// traceback the worst possible failure for a guard.
func TestMalformedToolInputDoesNotTraceback(t *testing.T) {
	for _, in := range []map[string]any{
		{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": []string{"kubectl", "delete"}}},
		{"hook_event_name": "PreToolUse", "tool_name": "Bash", "tool_input": "kubectl delete pod x"},
		{"hook_event_name": "PreToolUse", "tool_name": "Bash"},
	} {
		cmd := exec.Command("python3", "-B", script(t))
		data, _ := json.Marshal(in)
		cmd.Stdin = bytes.NewReader(data)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		_ = cmd.Run()
		if strings.Contains(errb.String(), "Traceback") {
			t.Fatalf("malformed input produced a traceback: %s", errb.String())
		}
	}
}

// Authoring a runbook that CONTAINS a kubectl line is refused, because `segments`
// splits on newline and cannot tell a heredoc body from a command — a limit it
// shares with core/permissions/match_bash.go's splitCompound. Pinned as a KNOWN
// limitation so that fixing it is a deliberate change rather than a surprise, and
// so the diagnostic's wrongness is on the record: it tells the agent its file
// write changes the cluster.
func TestHeredocBodyIsRefusedKnownLimitation(t *testing.T) {
	// The heredoc's "kubectl delete" line reaches validate_destructive like
	// any other segment (see the doc comment above), so — round 2's
	// re-review — this needs a deterministic stub exactly like every other
	// row that reaches a real subprocess call; otherwise this passes only
	// because this machine's kubectl cannot resolve anything, same coupling
	// as Finding F.
	dir := stubBin(t, "kubectl", "exit 1")
	out, _ := runHook(t, bashInput("cat > /tmp/runbook.sh <<'SH'\nkubectl delete pod x -n demo\nSH", "coder"), dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("decision = %q, want deny — if this now proceeds, the heredoc limitation was fixed and this test should be replaced", got)
	}
}

// The fast path fires on every Bash call in the whole fleet, so a non-Kubernetes
// command must emit nothing at all.
func TestNonKubernetesCommandEmitsNothing(t *testing.T) {
	out, code := runHook(t, bashInput("go test ./...", "coder"), "")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("non-k8s command: exit=%d stdout=%q, want exit 0 and no output", code, out)
	}
}

// The escalation is the script's, not the engine's: the engine reports attempt
// and consecutive and compares nothing.
func TestEscalatesToAskOnThirdAttempt(t *testing.T) {
	// A deterministic failure — round 2's re-review found this relied on the
	// real kubectl (extraPath "") failing to preview the change, which is
	// the same machine-coupling Finding F closed for commandShapeCases. On
	// a machine where the dry run happened to succeed this would silently
	// proceed instead of escalating, and attempt=3 would never be tested at
	// all.
	dir := stubBin(t, "kubectl", "exit 1")
	in := bashInput("kubectl apply -f app.yaml", "k8s_editor")
	in["attempt"] = 3
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "ask" {
		t.Fatalf("third attempt decision = %q, want ask", got)
	}
}

// The decision table asserts what the guard SAYS. It cannot see the defect that
// has mattered most: a row that proceeds while bash, running that same line,
// mutates a cluster. Four families of command did exactly that — a wrapper flag
// left at the head, `(` glued to the binary, `>(`, and an unlisted exec-wrapper —
// and every one was invisible to a decision-only assertion. This runs each
// proceed row under a real shell whose only reachable binaries are stubs, and
// fails if a kubectl/helm stub is invoked with a mutating verb.
//
// The inverse test already exists twice (TestGuardExecutesNoSubprocess,
// TestLaunchersAreRefusedNotExecuted); this is the same trick pointed at the
// rows the guard waves through.
func TestProceedsAreInertUnderBash(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	marker := filepath.Join(dir, "invoked.log")

	// The PATH is REPLACED, not prepended: several proceed rows name real,
	// outward-facing commands (`go test ./...`, `git commit`, `gh pr create`,
	// `sudo -u root systemctl restart nginx`). Prepending stubs would leave those
	// reachable and the oracle would commit, publish, and recurse into itself.
	// An unstubbed binary must therefore resolve to nothing at all.
	for _, bin := range []string{
		"kubectl", "helm", "kubectl.real", "helm3", "helm2",
		"sudo", "env", "timeout", "nice", "xargs", "flock", "setsid",
		"taskset", "chrt", "unshare", "strace", "systemd-run", "time",
	} {
		// The stub's own name is baked in rather than derived with basename(1):
		// the PATH is hermetic, so `$(basename "$0")` resolved to nothing, every
		// record began with the first ARGUMENT, isK8s rejected all of them, and
		// this test detected nothing while reporting 62 passing subtests.
		body := "#!/bin/sh\necho \"" + bin + " $*\" >> " + marker + "\nexit 0\n"
		if err := os.WriteFile(filepath.Join(binDir, bin), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", bin, err)
		}
	}

	// Verbs that change cluster state — mutatingVerbs, shared with
	// TestGuardOnlyEverPreviews. A mutation spelled with a verb absent there is
	// not detected — the same enumeration risk the guard itself carries.
	mutating := mutatingVerbs
	isK8s := func(bin string) bool {
		return strings.HasPrefix(bin, "kubectl") || strings.HasPrefix(bin, "helm")
	}

	for _, c := range commandShapeCases {
		if c.want != "" {
			continue // only the rows the guard waves through
		}
		t.Run(c.name, func(t *testing.T) {
			_ = os.Remove(marker)
			// Absolute /tmp writes are redirected into the test's own directory so
			// a redirect cannot clobber a real file. bash opens a redirect target
			// before exec, so a hermetic PATH alone would not prevent it, and which
			// kubectl/helm invocations occur is unaffected by the substitution.
			line := strings.ReplaceAll(c.command, "/tmp/", dir+"/")

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bash, "-c", line)
			cmd.Dir = dir
			cmd.Env = []string{"PATH=" + binDir, "HOME=" + dir}
			_, _ = cmd.CombinedOutput() // a failing command is fine; we only read the marker

			b, err := os.ReadFile(marker)
			if err != nil {
				return // nothing ran
			}
			for _, rec := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				fields := strings.Fields(rec)
				if len(fields) == 0 || !isK8s(fields[0]) {
					continue
				}
				if mutating[verbPosition(fields[1:])] {
					t.Fatalf("the guard let this proceed, and bash then ran a mutation:\n"+
						"  command: %s\n  invoked: %s", c.command, rec)
				}
			}
		})
	}
}

// verbPosition names the token a kubectl/helm invocation would treat as its verb:
// the first bare token, skipping flags and the value of an unambiguous global
// value flag. It is deliberately CRUDER than the guard's own parser — no
// per-verb flag tables, no sub-verb resolution — because duplicating that parser
// here would make the oracle agree with the code it is supposed to check.
//
// Scanning the whole argv instead was the first attempt, and it flagged
// `kubectl auth can-i create pods`: a read that asks whether creating is
// permitted. A false alarm on the investigator's ordinary register is what gets
// a test deleted, so the oracle errs toward missing a mutation rather than
// inventing one.
func verbPosition(args []string) string {
	globalValueFlags := map[string]bool{
		"-n": true, "--namespace": true, "--context": true,
		"--kubeconfig": true, "--as": true,
	}
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
		if globalValueFlags[args[i]] {
			i++
		}
	}
	return ""
}

// THE trap: `kubectl diff` exits 1 when a diff EXISTS. That is the normal case
// for any real change, so treating exit 1 as failure would refuse every correct
// apply. Only >1 is an error.
func TestKubectlDiffExitOneIsNotAFailure(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)      echo "~ spec.replicas: 1 -> 2"; exit 1 ;;
  *dry-run*)   echo "deployment.apps/x configured (server dry run)"; exit 0 ;;
  *)           exit 0 ;;
esac`)
	in := bashInput("kubectl apply -f change.yaml -n demo", "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("a normal diff (exit 1) must not be refused: %q", out)
	}
}

// A dry-run the API server rejects must be refused, with the server's own error
// carried back so the agent can correct it.
func TestServerDryRunFailureIsRefusedWithTheServerError(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)    exit 1 ;;
  *dry-run*) echo "error: unknown field spec.replica" >&2; exit 1 ;;
  *)         exit 0 ;;
esac`)
	out, _ := runHook(t, bashInput("kubectl apply -f change.yaml -n demo", "k8s_editor"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("failed dry-run decision = %q, want deny", decisionOf(t, out))
	}
	if !strings.Contains(out, "unknown field") {
		t.Fatalf("the refusal must carry the server error verbatim: %q", out)
	}
}

// A delete with no name and no selector has an unbounded blast radius.
func TestDeleteAllIsRefused(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0")
	out, _ := runHook(t, bashInput("kubectl delete pods --all -n demo", "k8s_cleaner"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("delete --all decision = %q, want deny", decisionOf(t, out))
	}
}

// The cleaner's documented contract, now enforced: it removes only resources
// labelled ephemeral. This needs agent_name — see Task 5.
func TestCleanerCannotDeleteAnUnlabelledResource(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *"-o json"*) echo '{"metadata":{"name":"real-app","labels":{}}}' ;;
  *)           exit 0 ;;
esac`)
	out, _ := runHook(t, bashInput("kubectl delete pod real-app -n demo", "k8s_cleaner"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("unlabelled delete by the cleaner = %q, want deny", decisionOf(t, out))
	}
	if !strings.Contains(out, "ephemeral") {
		t.Fatalf("the refusal must name the missing label: %q", out)
	}
}

// The same delete by the change agent is not gated on that label: k8s_editor may
// legitimately remove a real resource.
func TestEditorMayDeleteAnUnlabelledResource(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *"-o json"*) echo '{"metadata":{"name":"real-app","labels":{}}}' ;;
  *)           exit 0 ;;
esac`)
	in := bashInput("kubectl delete pod real-app -n demo", "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if strings.Contains(out, "ephemeral") {
		t.Fatalf("the ephemeral rule must apply to the cleaner only: %q", out)
	}
}

// A production target is a validation failure like any other — refused with a
// diagnostic, escalated after MAX_ATTEMPTS, never a special harder policy
// (nothing has been written yet, and a typo must not hard-block).
func TestProductionTargetIsRefusedOnTheStandardPath(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0")
	out, _ := runHook(t, bashInput("kubectl apply -f change.yaml -n production", "k8s_editor"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("production apply decision = %q, want deny", decisionOf(t, out))
	}
}

func TestProductionTargetEscalatesAfterThreeAttempts(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0")
	in := bashInput("kubectl apply -f change.yaml -n production", "k8s_editor")
	in["attempt"] = 3
	out, _ := runHook(t, in, dir)
	if decisionOf(t, out) != "ask" {
		t.Fatalf("third attempt decision = %q, want ask", decisionOf(t, out))
	}
}

// --- Review round 1 fixes ---

// CRITICAL, fail-open (Finding A). `doc.get("items", [doc]) if doc else []`
// defaulted to `[]` — an empty, therefore skipped, per-item loop — whenever
// the `get` pre-check's stdout was empty or unparseable, which is exactly
// what a ZERO-EXIT kubectl prints on a server hiccup or a plain-text error.
// Both let a k8s_cleaner delete PROCEED with no ephemeral-label or
// ownerReferences check ever having run. Proven the same way the reviewer
// proved it was open: a stub kubectl that exits 0 with empty stdout, and one
// that exits 0 with unparseable stdout, must both deny.
func TestDestructiveFailsClosedOnUnusableGetOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"empty stdout", "exit 0"},
		{"unparseable stdout", `echo "Error from server: gateway timeout"; exit 0`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := stubBin(t, "kubectl", tc.body)
			out, _ := runHook(t, bashInput("kubectl delete pod real-app -n demo", "k8s_cleaner"), dir)
			if decisionOf(t, out) != "deny" {
				t.Fatalf("a delete whose pre-check could not be read = %q, want deny (%s)", decisionOf(t, out), out)
			}
		})
	}
}

// Finding B, overriding the brief. classify() already normalises the binary
// to "kubectl"/"helm"; the validators used to exec argv[0] instead — the RAW
// head token, which is "(kubectl" for a paren-glued segment or a relative
// path like "./kubectl" for an explicit relative invocation. run_argv's cwd
// comes straight from the hook input (the session's writable working
// directory), so an agent-planted `./kubectl` shim in that cwd answered the
// guard's own resolve_target probe instead of the real binary — proven here
// with a forged ephemeral label that would otherwise make a k8s_cleaner
// delete PROCEED.
func TestGuardExecsTheNormalisedBinaryNeverArgvZero(t *testing.T) {
	cwd := t.TempDir()
	shim := filepath.Join(cwd, "kubectl")
	body := "#!/bin/sh\necho '{\"metadata\":{\"name\":\"real-app\",\"labels\":{\"omnis.dev/ephemeral\":\"true\"}}}'\nexit 0\n"
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	// The LEGITIMATE kubectl on PATH is a deterministic stub that behaves
	// DIFFERENTLY from the shim — it fails to resolve anything — so the two
	// are distinguishable by decision alone: round 2's re-review found the
	// original extraPath "" (real PATH) version was itself the same
	// machine-coupling Finding F closed elsewhere — on a machine whose real
	// kubectl also happened to resolve the target, "decision != proceed"
	// couldn't tell a legitimate resolve from the shim's forged one.
	dir := stubBin(t, "kubectl", "exit 1")
	in := bashInput("./kubectl delete pod real-app -n demo", "k8s_cleaner")
	in["cwd"] = cwd
	out, _ := runHook(t, in, dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("the guard's own cwd-planted shim answered its probe and let the delete proceed: %s", out)
	}
}

// Finding C. `helm plugin list` exits 0 even with ZERO plugins installed —
// verified against the real binary, it prints only the header row — so
// gating on the EXIT CODE made the fallback branch dead code: every Helm
// change tried `helm diff upgrade`, which does not exist without the
// plugin, and hard-blocked with `unknown command "diff"` regardless of
// whether the plugin was actually there.
func TestHelmDiffPluginDetectedByOutputNotExitCode(t *testing.T) {
	for _, tc := range []struct {
		name         string
		pluginListed bool
	}{
		{"diff plugin installed", true},
		{"no plugins installed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pluginLine := ""
			if tc.pluginListed {
				pluginLine = `echo "diff 3.9.0 preview-helm-changes"`
			}
			dir := stubBin(t, "helm", `
case "$*" in
  "plugin list")
    echo "NAME VERSION DESCRIPTION"
    `+pluginLine+`
    exit 0 ;;
  "diff upgrade myrel ./chart -n demo") exit 7 ;;
  *) exit 0 ;;
esac`)
			// Task 10 requires an APPROVED attestation for the mechanical
			// validation to actually PROCEED (rather than deny "not
			// reviewed"). Attested unconditionally: the "diff plugin
			// installed" case still denies before check_attested ever runs
			// (its dry-run stub exits 7), so this only changes the
			// "no plugins installed" case's decision from a false-positive
			// "not reviewed" deny back to what this test exists to check.
			in := bashInput("helm upgrade myrel ./chart -n demo", "k8s_editor")
			in["attestations"] = approvedFor(t, in)
			out, _ := runHook(t, in, dir)
			usedDiffCmd := decisionOf(t, out) == "deny"
			if usedDiffCmd != tc.pluginListed {
				t.Fatalf("used `helm diff upgrade` = %v, want %v (%s)", usedDiffCmd, tc.pluginListed, out)
			}
		})
	}
}

// Finding D. --server-side is an apply-only flag: `kubectl create
// --server-side` and `kubectl replace --server-side` both fail with "unknown
// flag: --server-side" (verified against the real binary), which
// permanently blocked both verbs and blamed the API server for a flag the
// guard itself injected. The stub exits 3 (recorded as deny) iff
// --server-side appears in the dry-run step, so the DECISION itself proves
// whether the flag was sent.
func TestServerSideAppliesOnlyToApply(t *testing.T) {
	for _, tc := range []struct {
		verb           string
		wantServerSide bool
	}{
		{"apply", true},
		{"create", false},
		{"replace", false},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*) exit 1 ;;
  *--server-side*) exit 3 ;;
  *) exit 0 ;;
esac`)
			// Attested unconditionally (Task 10): "apply" still denies before
			// check_attested runs (its dry-run stub exits 3), but "create"/
			// "replace" now reach check_attested on a clean dry-run and would
			// otherwise deny as "not reviewed" — a false positive for
			// gotServerSide that has nothing to do with --server-side.
			in := bashInput("kubectl "+tc.verb+" -f app.yaml -n demo", "k8s_editor")
			in["attestations"] = approvedFor(t, in)
			out, _ := runHook(t, in, dir)
			gotServerSide := decisionOf(t, out) == "deny"
			if gotServerSide != tc.wantServerSide {
				t.Fatalf("%s: --server-side present = %v, want %v (%s)", tc.verb, gotServerSide, tc.wantServerSide, out)
			}
		})
	}
}

// Design ruling for Finding E. rollout restart/pause/resume support no
// --dry-run at all (verified against the real binary: "unknown flag:
// --dry-run"), so validate_imperative's normal path permanently refused them
// with "express it as a manifest and apply that file instead" — impossible
// advice for an action that recreates pods rather than changing desired
// state. The fix resolves the target and reports its scope instead: a
// resolvable target now PROCEEDS (the canonical safe rolling restart is no
// longer dead-ended), and an unresolvable one still denies, exactly like
// validate_destructive's own pre-check.
func TestRolloutRestartResolvesInsteadOfDeadEnding(t *testing.T) {
	for _, action := range []string{"restart", "pause", "resume"} {
		t.Run(action, func(t *testing.T) {
			t.Run("resolvable target proceeds", func(t *testing.T) {
				dir := stubBin(t, "kubectl", `
case "$*" in
  *get*) echo '{"metadata":{"name":"x","labels":{}}}'; exit 0 ;;
  *)     exit 1 ;;
esac`)
				// Attested (Task 10): a resolvable target now reaches
				// check_attested, and an unattested "not reviewed" deny would
				// be indistinguishable from the dead-end this test guards
				// against.
				in := bashInput("kubectl rollout "+action+" deploy/x -n demo", "k8s_editor")
				in["attestations"] = approvedFor(t, in)
				out, _ := runHook(t, in, dir)
				if decisionOf(t, out) == "deny" {
					t.Fatalf("a resolvable rollout %s target was denied instead of previewed: %s", action, out)
				}
			})
			t.Run("unresolvable target still denies", func(t *testing.T) {
				dir := stubBin(t, "kubectl", "exit 1")
				out, _ := runHook(t, bashInput("kubectl rollout "+action+" deploy/x -n demo", "k8s_editor"), dir)
				if decisionOf(t, out) != "deny" {
					t.Fatalf("an unresolvable rollout %s target decision = %q, want deny", action, decisionOf(t, out))
				}
			})
		})
	}
}

// Finding H, first bug. ops[:1] mis-selected the release name whenever a
// flag preceded it: "helm uninstall -n demo myrel" built
// "helm history -n -n demo", which can never resolve any real release. The
// stub only succeeds for the correctly-parsed args, so a deny here proves
// the release name was misidentified.
func TestHelmReleaseNameSkipsAPrecedingFlag(t *testing.T) {
	dir := stubBin(t, "helm", `
case "$*" in
  "history myrel -n demo") exit 0 ;;
  *)                       exit 9 ;;
esac`)
	// Attested (Task 10): a correctly-resolved release now reaches
	// check_attested, and an unattested "not reviewed" deny would be
	// indistinguishable from the misidentification bug this test guards
	// against.
	in := bashInput("helm uninstall -n demo myrel", "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if decisionOf(t, out) == "deny" {
		t.Fatalf("the release name was not correctly identified from behind a preceding flag: %s", out)
	}
}

// Finding H, second bug. `[a for a in ops if not a.startswith("-")]` counted
// a flag's VALUE as if it were a named resource: "kubectl delete -n demo"
// has no resource at all, only a namespace value, yet the old check saw
// `["demo"]` and let it through. This needs no stub — the blast-radius check
// runs before any subprocess call, so it is deterministic on any machine.
func TestBlastRadiusIgnoresAFlagsValue(t *testing.T) {
	out, _ := runHook(t, bashInput("kubectl delete -n demo", "k8s_editor"), "")
	if decisionOf(t, out) != "deny" {
		t.Fatalf("decision = %q, want deny", decisionOf(t, out))
	}
	if !strings.Contains(reasonOf(t, out), "names no specific resource") {
		t.Fatalf("reason = %q, want it to explain no resource was named", reasonOf(t, out))
	}
}

// --- Task 10: the attestation requirement ---

// subjectHashPy runs the script's OWN subject_hash against toolInput and
// returns the hex digest. It loads the script as a module (rather than
// `python3 -B script.py`) so `if __name__ == "__main__": main()` never runs
// and blocks on stdin.
func subjectHashPy(t *testing.T, toolInput map[string]any) string {
	t.Helper()
	data, err := json.Marshal(toolInput)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// strconv.Quote produces a Go double-quoted string literal, which Python
	// also accepts as a double-quoted literal (both escape the same way for
	// any path this test produces — under t.TempDir()).
	py := `
import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("k8s_validate", ` + strconv.Quote(script(t)) + `)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
print(mod.subject_hash(json.load(sys.stdin)))
`
	cmd := exec.Command("python3", "-B", "-c", py)
	cmd.Stdin = bytes.NewReader(data)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("subject_hash failed: %v\nstderr: %s", err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

// THE central claim: the subject hash binds CONTENT, computed identically by
// the Go engine (hookstate.HashArgs, the attestation-store key) and the
// Python hook script (subject_hash, what main() actually looks up). If they
// ever disagree, every attestation would look "not reviewed" to the hook —
// or worse, two DIFFERENT tool_inputs could collide on the same subject,
// letting an attestation for one command authorize a different one.
func TestSubjectHashAgreesWithHashArgs(t *testing.T) {
	script(t) // skips early on windows / no python3, before spending on cases
	cases := []map[string]any{
		{"command": "kubectl get pods"},
		// A compound command containing "&&" is the exact case Go's
		// SetEscapeHTML(false) exists for (see hookstate.HashArgs's doc
		// comment) — every compound shell command contains it.
		{"command": "kubectl apply -f a.yaml && kubectl delete pod x -n demo"},
		{}, // an empty tool_input, same as a malformed/absent one
		{"command": "kubectl get pods -n demo", "timeout": 30.0},
		{"command": `helm upgrade myrel ./chart --set "a<b&c>d"`},
	}
	for _, ti := range cases {
		want := hookstate.HashArgs(ti)
		got := subjectHashPy(t, ti)
		if got != want {
			t.Fatalf("tool_input=%#v: Python subject_hash=%s, Go HashArgs=%s", ti, got, want)
		}
	}
}

// The design's non-negotiable property (see check_attested's docstring): a
// missing attestation must be a TERMINAL refusal, never an escalation to
// `ask`. If it escalated, the whole guarantee would be removable by
// unticking the validator agent in Settings and then clicking "allow" on the
// resulting card — reinstating the exact hole record_validation closes. This
// is what `attempt`/`consecutive` being absent from check_attested's
// signature is FOR: it structurally cannot call `refuse`.
func TestMissingAttestationNeverEscalates(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*) exit 1 ;;
  *)      exit 0 ;;
esac`)
	in := bashInput("kubectl apply -f app.yaml -n demo", "k8s_editor")
	// attempt is AT the escalation threshold — if check_attested behaved like
	// refuse(), this would come back "ask", not "deny".
	in["attempt"] = 3
	in["consecutive"] = 3
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("missing attestation at attempt=3 decision = %q, want deny (never ask)", got)
	}
	if !strings.Contains(reasonOf(t, out), "has not been reviewed") {
		t.Fatalf("reason = %q, want it to name the missing review", reasonOf(t, out))
	}
}

// A REJECTED verdict must deny too, and must not escalate either — same
// property, the other verdict.
func TestRejectedAttestationNeverEscalates(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*) exit 1 ;;
  *)      exit 0 ;;
esac`)
	in := bashInput("kubectl apply -f app.yaml -n demo", "k8s_editor")
	in["attempt"] = 3
	in["consecutive"] = 3
	ti := in["tool_input"].(map[string]any)
	subject := hookstate.HashArgs(ti)
	in["attestations"] = map[string]any{
		subject: map[string]any{"verdict": "REJECTED", "reasons": "wrong namespace"},
	}
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("rejected attestation at attempt=3 decision = %q, want deny (never ask)", got)
	}
	if !strings.Contains(reasonOf(t, out), "REJECTED") {
		t.Fatalf("reason = %q, want it to name the rejection", reasonOf(t, out))
	}
}

// An APPROVED attestation for the exact subject lets a mechanically-valid
// change proceed, and the diff is carried back via systemMessage so the
// permission card stays informative.
func TestApprovedAttestationProceedsWithDiff(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)    echo "~ spec.replicas: 1 -> 2"; exit 1 ;;
  *dry-run*) echo "deployment.apps/x configured (server dry run)"; exit 0 ;;
  *)         exit 0 ;;
esac`)
	in := bashInput("kubectl apply -f app.yaml -n demo", "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("an approved, mechanically-clean change was not allowed to proceed: %q", out)
	}
	// validate_manifest's returned "preview" is the server dry run's own
	// confirmation text (what `kubectl diff` printed is used only to decide
	// pass/fail, not carried forward) — that is what must reach the user via
	// systemMessage, alongside the identifying `kubectl apply` header.
	if !strings.Contains(out, "deployment.apps/x configured") || !strings.Contains(out, "kubectl apply") {
		t.Fatalf("the validated preview must reach the user via systemMessage: %q", out)
	}
}

// THE demonstration that the hash binds CONTENT: an attestation recorded for
// v1 of a manifest must NOT authorize applying v2 — even though both are
// `kubectl apply -f change.yaml`, the file's own bytes differ and command-line
// attestation is keyed on tool_input (the command string), which is
// identical for both. This is exactly why record_validation's caller must
// pass the CURRENT tool_input's subject: the guard cannot tell v1 from v2
// apart on the command line alone, so a real workflow's attestation is only
// as trustworthy as the reviewer re-hashing before approving. What THIS test
// pins is narrower and unconditional: two DIFFERENT commands (the ordinary
// way a "v2" would actually show up to the guard — a different -f path, or a
// different verb/target) never share a subject, so approving one can never
// silently authorize the other.
func TestAttestationDoesNotCrossDifferentChanges(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)    exit 1 ;;
  *dry-run*) exit 0 ;;
  *)         exit 0 ;;
esac`)
	v1 := bashInput("kubectl apply -f v1.yaml -n demo", "k8s_editor")
	v1["attestations"] = approvedFor(t, v1) // approves ONLY v1's subject
	v2 := bashInput("kubectl apply -f v2.yaml -n demo", "k8s_editor")
	v2["attestations"] = v1["attestations"] // the SAME (now-stale) attestations map

	outV1, _ := runHook(t, v1, dir)
	if got := decisionOf(t, outV1); got == "deny" || got == "ask" {
		t.Fatalf("v1, attested for its own subject, was not allowed to proceed: %q", outV1)
	}
	outV2, _ := runHook(t, v2, dir)
	if got := decisionOf(t, outV2); got != "deny" {
		t.Fatalf("v2, carrying only v1's attestation, decision = %q, want deny", got)
	}
	if !strings.Contains(reasonOf(t, outV2), "has not been reviewed") {
		t.Fatalf("v2's refusal reason = %q, want it to say the change was not reviewed",
			reasonOf(t, outV2))
	}
}

// A compound command with TWO mutating segments must have BOTH mechanically
// validated before either is allowed to run — not just the first. Before
// Task 10, `validate()` never exited on success, so the loop always reached
// every segment; this pins that the attestation wiring did not change that:
// the first segment attested+clean must NOT make the guard stop looking at
// the rest of the line and let a mechanically-broken second segment through.
func TestSecondMutatingSegmentIsStillMechanicallyValidated(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)      exit 1 ;;
  *dry-run*)   exit 0 ;;
  *"-o json"*) echo "not json at all"; exit 0 ;;
  *)           exit 0 ;;
esac`)
	// Segment 1 (apply) mechanically validates cleanly. Segment 2 (delete)
	// fails resolve_target's fail-closed JSON parse — if the guard stopped
	// checking after segment 1, this whole line would proceed and delete an
	// unvalidated resource.
	command := "kubectl apply -f app.yaml -n demo && kubectl delete pod x -n demo"
	in := bashInput(command, "k8s_editor")
	subject := hookstate.HashArgs(in["tool_input"].(map[string]any))
	in["attestations"] = map[string]any{subject: map[string]any{"verdict": "APPROVED"}}
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("a compound command's mechanically-broken SECOND segment decision = %q, want deny (%s)",
			got, out)
	}
	if !strings.Contains(reasonOf(t, out), "could not be read") {
		t.Fatalf("reason = %q, want it to explain the second segment's target could not be read", reasonOf(t, out))
	}
}

// The positive half of the same property: two mutating segments that BOTH
// mechanically validate, under ONE attestation covering the whole line
// (record_validation's caller reviews the whole tool_input, not one
// segment), both get previewed and the combined diff reaches the user.
func TestTwoMutatingSegmentsBothPreviewUnderOneAttestation(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)      echo "~ segment one"; exit 1 ;;
  *dry-run*)   echo "segment one applied (dry run)"; exit 0 ;;
  *"-o json"*) echo '{"metadata":{"name":"x","labels":{}}}'; exit 0 ;;
  *)           exit 0 ;;
esac`)
	command := "kubectl apply -f app.yaml -n demo && kubectl delete pod x -n demo"
	in := bashInput(command, "k8s_editor")
	in["attestations"] = approvedFor(t, in)
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("two mechanically-clean, attested segments were not allowed to proceed: %q", out)
	}
	if !strings.Contains(out, "apply") || !strings.Contains(out, "delete") {
		t.Fatalf("the combined diff must mention BOTH validated segments: %q", out)
	}
}
