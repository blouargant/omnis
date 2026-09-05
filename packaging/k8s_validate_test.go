package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
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
	code := cmd.ProcessState.ExitCode()
	// decisionOf returns "" for BOTH "proceeded, no note" (the honest case)
	// and "stdout did not parse as a decision" — a caller checking only
	// `decisionOf(...) != "deny"` cannot tell them apart, so a non-zero exit
	// that somehow produced neither a stderr message (caught above) nor
	// parseable JSON on stdout would silently read as an approval. Every
	// intentional non-zero exit in the script writes to stderr FIRST (see
	// its own two sys.exit(1) call sites), so this should never fire for any
	// legitimate path — it exists to fail LOUDLY, at the source every test
	// shares, if that invariant is ever broken by a future change instead of
	// letting the ambiguity flow silently into forty decisionOf call sites.
	if code != 0 && !looksLikeHookOutput(out.String()) {
		t.Fatalf("hook exited %d with no stderr and no parseable hookSpecificOutput/systemMessage "+
			"on stdout — a silent crash would be indistinguishable from a proceed: stdout=%q", code, out.String())
	}
	return out.String(), code
}

// looksLikeHookOutput reports whether stdout is a JSON object carrying either
// half of the hook protocol this file cares about.
func looksLikeHookOutput(stdout string) bool {
	s := strings.TrimSpace(stdout)
	if s == "" {
		return false
	}
	var jo map[string]any
	if err := json.Unmarshal([]byte(s), &jo); err != nil {
		return false
	}
	_, hasDecision := jo["hookSpecificOutput"]
	_, hasMessage := jo["systemMessage"]
	return hasDecision || hasMessage
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
	stubKubectlOK         = "kubectl-ok"         // every kubectl call succeeds; `get` yields one resolvable item
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
	stubKubectlOK: `
case "$1" in
  get) echo '{"apiVersion":"v1","items":[{"kind":"Pod","metadata":{"name":"mypod","namespace":"demo"}}]}' ;;
  *)   echo "dry run ok" ;;
esac
exit 0`,
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

// writeManifest writes real bytes to a fresh temp file and returns its
// absolute path. Needed wherever a command names a -f/-k target: the guard
// now independently re-reads that target's own bytes to bind the subject
// (see local_target_digest in config/hooks/k8s-validate.py) — a path a
// STUBBED kubectl merely pretends to accept is not enough once that read is
// for real, and a relative path with no "cwd" set in the hook input would
// resolve against the test BINARY's own working directory, not a per-test
// sandbox, so every caller uses the absolute path this returns.
func writeManifest(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "change.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// changeIDRe extracts the change identifier check_attested's own "not
// reviewed" deny message carries — the literal subject string it just
// computed. This is the ONLY reliable way a test (or a real k8s_validator
// sub-agent) learns a segment's subject: subject_hash binds the manifest
// target's CONTENT (see its own doc comment), so it cannot be recomputed
// independently without reimplementing the script's exact algorithm — and
// deliberately so, since nothing on the calling side is meant to.
var changeIDRe = regexp.MustCompile("change identifier `([0-9a-f]{64})`")

func subjectFromDeny(t *testing.T, reason string) string {
	t.Helper()
	m := changeIDRe.FindStringSubmatch(reason)
	if m == nil {
		t.Fatalf("no change identifier found in deny reason: %q", reason)
	}
	return m[1]
}

// discoverSegmentSubject runs the hook against `in` once — WITHOUT
// attestations — expecting the standard "not reviewed" deny for the first
// mutating segment it reaches, and extracts the subject the hook itself
// computed for THAT segment. This is exactly the discovery a real
// k8s_validator sub-agent performs (it is handed the change identifier from
// the deny message, never expected to compute it), so it works regardless of
// subject_hash's internal algorithm — content-bound, per-segment, or
// otherwise — without this test file reimplementing it.
//
// extraPath must be the SAME stub PATH the caller will use for its own runs,
// and `in` must already carry any "cwd"/absolute paths the mechanical
// validation needs (a real manifest file on disk, etc.) — this function only
// discovers the subject; it changes nothing about `in`.
func discoverSegmentSubject(t *testing.T, in map[string]any, extraPath string) string {
	t.Helper()
	probe := make(map[string]any, len(in))
	for k, v := range in {
		probe[k] = v
	}
	delete(probe, "attestations")
	out, _ := runHook(t, probe, extraPath)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("expected an unattested first run to deny \"not reviewed\", got %q: %s", got, out)
	}
	if !strings.Contains(reasonOf(t, out), "has not been reviewed") {
		t.Fatalf("expected an unattested first run's reason to say \"not reviewed\", got: %q",
			reasonOf(t, out))
	}
	return subjectFromDeny(t, reasonOf(t, out))
}

// approveOneSegment discovers `in`'s subject (see discoverSegmentSubject) and
// returns an attestations map recording an APPROVED verdict for it.
func approveOneSegment(t *testing.T, in map[string]any, extraPath string) map[string]any {
	t.Helper()
	subject := discoverSegmentSubject(t, in, extraPath)
	return map[string]any{subject: map[string]any{"verdict": "APPROVED"}}
}

// rejectOneSegment mirrors approveOneSegment for the other verdict.
func rejectOneSegment(t *testing.T, in map[string]any, extraPath, reasons string) map[string]any {
	t.Helper()
	subject := discoverSegmentSubject(t, in, extraPath)
	return map[string]any{subject: map[string]any{"verdict": "REJECTED", "reasons": reasons}}
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
	{"auth whoami reads", "kubectl auth whoami", "k8s_investigator", "", ""},
	{"auth reconcile writes RBAC", "kubectl auth reconcile -f rbac.yaml", "k8s_editor", "deny", ""},
	{"config view reads", "kubectl config view", "k8s_investigator", "", ""},
	{"rollout status reads", "kubectl rollout status deploy/x -n demo", "k8s_investigator", "", ""},
	{"rollout history reads", "kubectl rollout history deploy/x -n demo", "k8s_investigator", "", ""},
	// `exec` is the same "the verb alone proves nothing" shape as auth/config/
	// rollout, except the discriminator is the whole container command after
	// `--` rather than one sub-verb token. A read there is the investigator's
	// ordinary register — reading a config file out of a pod is how half of
	// these tasks start — and refusing it wholesale is the friction that gets a
	// guard removed.
	{"exec reads a file", "kubectl exec mypod -n demo -- cat /etc/nginx/nginx.conf", "k8s_investigator", "", ""},
	{"exec lists a directory", "kubectl exec mypod -- ls -la /var/log", "k8s_investigator", "", ""},
	{"exec names a container", "kubectl exec -n demo mypod -c app -- printenv", "k8s_investigator", "", ""},
	{"exec greps in a pod", "kubectl exec mypod -- grep -i error /var/log/app.log", "k8s_investigator", "", ""},
	// `env` alone prints the environment; `env FOO=1 sh -c …` runs a command.
	// Only the bare form is provable, and the bare form is idiomatic enough
	// that dropping it would send every agent to `sh -c` instead.
	{"exec bare env prints", "kubectl exec mypod -- env", "k8s_investigator", "", ""},
	// -i/-t are not themselves a risk: every allowlisted command writes
	// nothing and execs nothing whatever its stdin or TTY, which is exactly
	// why `less`/`more`/`top` (shell escapes, kill keys) are NOT allowlisted.
	{"exec interactive read", "kubectl exec -it mypod -- cat /etc/hosts", "k8s_investigator", "", ""},
	// attach without stdin is `logs -f` with a different transport; cp OUT of
	// a container reads its filesystem; `label/annotate --list` displays labels
	// rather than setting one. All three were denied wholesale.
	{"attach watches output", "kubectl attach mypod -n demo", "k8s_investigator", "", ""},
	{"attach names a container", "kubectl attach mypod -c app -n demo", "k8s_investigator", "", ""},
	{"cp out of a container", "kubectl cp demo/mypod:/var/log/app.log ./app.log", "k8s_investigator", "", ""},
	{"cp out with a container flag", "kubectl cp -c app demo/mypod:/etc/nginx/nginx.conf /tmp/nginx.conf", "k8s_investigator", "", ""},
	{"label --list displays", "kubectl label --list pods mypod -n demo", "k8s_investigator", "", ""},
	{"annotate --list displays", "kubectl annotate --list pods mypod -n demo", "k8s_investigator", "", ""},
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

	// ---- run / debug: the ephemeral-diagnostic workflow the investigator's
	// own instruction mandates. Both are real creations, so neither proceeds —
	// they reach a preview and then the reviewer, instead of the catch-all's
	// "no validation rule".
	{"run reaches the reviewer", "kubectl run netshoot --image=nicolaka/netshoot --restart=Never --labels=omnis.dev/ephemeral=true -n demo", "k8s_investigator", "deny", "has not been reviewed"},
	{"run with a container command reaches the reviewer", "kubectl run tmp --image=busybox --restart=Never -n demo -- sleep 60", "k8s_investigator", "deny", "has not been reviewed"},
	{"debug reaches the reviewer", "kubectl debug pod/mypod -n demo --image=busybox", "k8s_investigator", "deny", "has not been reviewed"},
	{"debug on a node reaches the reviewer", "kubectl debug node/worker-1 -n demo --image=busybox", "k8s_investigator", "deny", "has not been reviewed"},
	{"debug naming no target", "kubectl debug -n demo --image=busybox", "k8s_investigator", "deny", "exactly one target"},
	{"debug naming two targets", "kubectl debug pod/a pod/b -n demo --image=busybox", "k8s_investigator", "deny", "exactly one target"},

	// ---- attach / cp: the write half of the same verbs ----
	{"attach -it opens stdin", "kubectl attach -it mypod -n demo", "k8s_investigator", "deny", "write channel"},
	{"attach -ti clusters the other way", "kubectl attach -ti mypod -n demo", "k8s_investigator", "deny", "write channel"},
	{"attach --stdin opens stdin", "kubectl attach --stdin mypod -n demo", "k8s_investigator", "deny", "write channel"},
	{"cp into a container", "kubectl cp ./config.yaml demo/mypod:/etc/app/config.yaml", "k8s_editor", "deny", "ConfigMap"},
	{"cp neither side remote", "kubectl cp ./a ./b", "k8s_investigator", "deny", "names a pod"},
	{"cp with too few operands", "kubectl cp demo/mypod:/var/log/app.log", "k8s_investigator", "deny", "exactly one source"},
	// Refused correctly, but validate_helm's install/upgrade branch used to
	// explain it with "chart argument does not name a local directory" — an
	// argument neither verb takes. Neither accepts --dry-run either (verified
	// against the binary), so there is no preview to attempt.
	{"helm test names its real reason", "helm test myrel -n demo", "k8s_investigator", "deny", "test hooks"},
	{"helm push names its real reason", "helm push ./chart.tgz oci://reg/x", "k8s_editor", "deny", "publishes a chart"},
	// --list alongside a label CHANGE is still a change, and RFC 1123 means no
	// resource name can be mistaken for one (`=` and a trailing `-` are both
	// illegal in a Kubernetes name).
	{"label --list with a change still sets", "kubectl label --list pods mypod tier=web -n demo", "k8s_editor", "deny", "has not been reviewed"},
	{"label --list with a removal still removes", "kubectl label --list pods mypod tier- -n demo", "k8s_editor", "deny", "has not been reviewed"},

	// ---- exec: everything not provably a read ----
	// The whole point of the split: a shell inside the container is exactly as
	// unreadable as a shell outside it, so the read allowlist can never reach
	// it, however harmless the quoted string looks.
	{"exec hands a line to sh", "kubectl exec mypod -- sh -c 'cat /etc/hosts'", "k8s_investigator", "deny", "another program to execute"},
	{"exec hands a line to bash", "kubectl exec -it mypod -- bash -c 'rm -rf /data'", "k8s_editor", "deny", "another program to execute"},
	{"exec runs an interactive shell", "kubectl exec -it mypod -- bash", "k8s_editor", "deny", "another program to execute"},
	{"exec deletes in the container", "kubectl exec mypod -- rm -rf /data", "k8s_editor", "deny", "not one of the read-only commands"},
	{"exec writes with tee", "kubectl exec mypod -- tee /etc/passwd", "k8s_editor", "deny", "not one of the read-only commands"},
	{"exec env with operands execs", "kubectl exec mypod -- env FOO=1 printenv", "k8s_investigator", "deny", "only with no arguments"},
	{"exec without a separator", "kubectl exec mypod cat /etc/hosts", "k8s_investigator", "deny", "Separate the container command"},
	{"exec with an empty command", "kubectl exec mypod --", "k8s_investigator", "deny", "Separate the container command"},
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
	// run/debug must get PAST their mechanical step to prove they now reach
	// the reviewer; the default stub fails everything, which would deny them
	// for the wrong reason and let a regression to the catch-all pass.
	"run reaches the reviewer":                          stubKubectlOK,
	"run with a container command reaches the reviewer": stubKubectlOK,
	"debug reaches the reviewer":                        stubKubectlOK,
	"debug on a node reaches the reviewer":              stubKubectlOK,
	"label --list with a change still sets":             stubKubectlOK,
	"label --list with a removal still removes":         stubKubectlOK,
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
		"kubectl run tmp --image=busybox --restart=Never -n demo -- sleep 60",
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
				if recordedIsMutating(mutating, verbPosition(fields[1:]), fields[1:]) {
					t.Fatalf("the guard let this proceed, and bash then ran a mutation:\n"+
						"  command: %s\n  invoked: %s", c.command, rec)
				}
			}
		})
	}
}

// recordedIsMutating decides whether a recorded invocation would actually
// change anything. verbPosition alone cannot, for four verbs: `exec`,
// `attach`, `cp` and `label` are all in mutatingVerbs because each has a real
// mutating form, but each also has a read form the guard now waves through —
// `-- cat`, no `-i`, a copy OUT, `--list` — and the difference is in the
// arguments, not the verb.
//
// It duplicates neither the guard's parser nor its allowlists. Every rule
// below is deliberately tinier and cruder than the script's, and is owned by
// this test: an argument shape the guard newly waves through but that is not
// named here is still reported as a mutation, so widening the script cannot
// silently widen this oracle too — the row has to be argued for in both
// places. Every case errs toward calling something a mutation.
func recordedIsMutating(mutating map[string]bool, verb string, args []string) bool {
	if !mutating[verb] {
		return false
	}
	// args still carries the verb itself (the caller strips only the binary),
	// and an operand count that includes it is off by one — which read every
	// `kubectl cp POD:/a ./b` as a three-operand shape it could not parse, and
	// therefore as a mutation.
	rest := args
	for i, a := range args {
		if a == verb {
			rest = args[i+1:]
			break
		}
	}
	switch verb {
	case "exec":
		return execIsMutating(rest)
	case "attach":
		return attachIsMutating(rest)
	case "cp":
		return cpIsMutating(rest)
	case "label", "annotate":
		return labelIsMutating(rest)
	}
	return true
}

// execIsMutating: a read only when the command after `--` is one of a handful
// of names spelled out here. No separator at all is treated as a mutation,
// which is also what the guard does.
func execIsMutating(args []string) bool {
	inner := []string{}
	for i, a := range args {
		if a == "--" {
			inner = args[i+1:]
			break
		}
	}
	if len(inner) == 0 {
		return true
	}
	switch inner[0] {
	case "cat", "ls", "grep", "printenv", "env", "head", "tail", "wc", "stat", "df", "ps":
		return false
	}
	return true
}

// attachIsMutating: anything that could open stdin counts as a write, whether
// spelled long or clustered into a short-flag group.
func attachIsMutating(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "--stdin") {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "i") {
			return true
		}
	}
	return false
}

// cpIsMutating: a copy is a read only when the FIRST non-flag operand names a
// pod (carries a ":") and the second does not. Anything else — an upload, a
// shape this oracle cannot parse — counts as a mutation.
func cpIsMutating(args []string) bool {
	ops := []string{}
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			if args[i] == "-c" || args[i] == "--container" {
				i++
			}
			continue
		}
		ops = append(ops, args[i])
	}
	if len(ops) != 2 {
		return true
	}
	return !strings.Contains(ops[0], ":") || strings.Contains(ops[1], ":")
}

// labelIsMutating: `--list` displays labels, but only when nothing alongside
// it sets (`key=value`) or removes (`key-`) one.
func labelIsMutating(args []string) bool {
	list := false
	for _, a := range args {
		if a == "--list" || strings.HasPrefix(a, "--list=") {
			list = true
		}
	}
	if !list {
		return true
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.Contains(a, "=") || strings.HasSuffix(a, "-") {
			return true
		}
	}
	return false
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

// THE trap for `run`, and one TestGuardOnlyEverPreviews cannot see: that
// oracle asks only whether a recorded invocation CONTAINS a dry-run marker,
// and the dangerous spelling contains one.
//
// `kubectl run x --image=y -- sleep 60` takes a `--` separator, which no
// pre-existing imperative verb does. Appending the flag the way
// validate_imperative always had produces `kubectl run x --image=y -- sleep
// 60 --dry-run=server`, where kubectl hands `--dry-run=server` to the
// CONTAINER as an argument to `sleep` and never sees it itself — so the guard
// CREATES THE POD while believing it previewed it. That is the exact failure
// this whole file exists to prevent, and it would have passed every other
// test here. So assert the position, not the presence.
func TestRunDryRunPrecedesTheContainerCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked.log")
	body := "#!/bin/sh\necho \"$*\" >> " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	runHook(t, bashInput("kubectl run tmp --image=busybox --restart=Never -n demo -- sleep 60",
		"k8s_investigator"), dir)

	b, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the guard never invoked kubectl at all — `run` should have been dry-run")
	}
	rec := strings.TrimSpace(string(b))
	if !strings.Contains(rec, "--dry-run=server") {
		t.Fatalf("no dry run at all: %q", rec)
	}
	sep := strings.Index(rec, " -- ")
	dry := strings.Index(rec, "--dry-run=server")
	if sep >= 0 && dry > sep {
		t.Fatalf("--dry-run=server landed AFTER the `--` separator, so kubectl would pass it "+
			"to the container and create the pod for real:\n  %s", rec)
	}
}

// A well-formed List with ZERO items is a POSITIVE answer — the API server was
// read successfully and said nothing matches — but resolve_target reported it
// with "the response was not the JSON of an identifiable resource", blaming a
// failure that did not occur. It lands on the k8s_cleaner's ordinary happy
// path (sweeping an already-clean namespace), where the agent can learn
// nothing from that message except to retry, and three retries escalate to the
// user for a command that would delete nothing.
//
// Both halves are asserted, because "make the empty case say something else"
// is trivially satisfiable by making the unreadable case say it too — which
// would trade one wrong diagnostic for another.
func TestEmptyMatchIsNotBlamedOnAnUnreadableResponse(t *testing.T) {
	empty := stubBin(t, "kubectl", `
case "$1" in
  get) echo '{"apiVersion":"v1","kind":"List","items":[]}' ;;
  *)   echo ok ;;
esac
exit 0`)
	out, _ := runHook(t, bashInput("kubectl delete pod,job -n demo -l omnis.dev/ephemeral=true",
		"k8s_cleaner"), empty)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("an empty match must still be refused (the decision is deliberately "+
			"unchanged); decision = %q", got)
	}
	reason := reasonOf(t, out)
	if !strings.Contains(reason, "matched no resources") {
		t.Fatalf("an empty match must say so; reason = %q", reason)
	}
	if strings.Contains(reason, "could not be read") {
		t.Fatalf("an empty match must NOT be blamed on an unreadable response; reason = %q", reason)
	}

	// The other half: a response that genuinely cannot be read must still say
	// exactly that.
	garbage := stubBin(t, "kubectl", `
case "$1" in
  get) echo 'Error from server: the connection was refused' ;;
  *)   echo ok ;;
esac
exit 0`)
	out2, _ := runHook(t, bashInput("kubectl delete pod x -n demo", "k8s_cleaner"), garbage)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("an unreadable response must be refused; decision = %q", got)
	}
	if !strings.Contains(reasonOf(t, out2), "could not be read") {
		t.Fatalf("an unreadable response must say so; reason = %q", reasonOf(t, out2))
	}
}

// An unpreviewable helm verb must reach its refusal WITHOUT spending
// subprocess calls on a preview that cannot exist — the old path ran
// `helm plugin list` and then a `--dry-run=server` helm never accepts, purely
// to arrive at a wrong explanation.
func TestUnpreviewableHelmVerbsSpendNoSubprocess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked.log")
	body := "#!/bin/sh\necho \"$*\" >> " + marker + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "helm"), []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	for _, cmd := range []string{"helm test myrel -n demo", "helm push ./chart.tgz oci://reg/x"} {
		out, _ := runHook(t, bashInput(cmd, "k8s_editor"), dir)
		if got := decisionOf(t, out); got != "deny" {
			t.Fatalf("%s: decision = %q, want deny", cmd, got)
		}
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("an unpreviewable helm verb invoked helm anyway:\n%s", b)
	}
}

// A refusal an agent cannot act on spends its attempts and then escalates to
// the user for a problem it could have fixed itself. Observed live: `kubectl
// diff` refused with a bare "Error from server (NotFound)", the model burned
// its retries, and only then guessed the real cause aloud ("the diff hook
// blocks because the namespace doesn't exist yet").
//
// Each case asserts BOTH halves: the advice appears, and the server's own
// words survive alongside it. A hint that replaced the evidence would trade
// one dead end for a prettier one.
func TestServerErrorsCarryAnActionableHint(t *testing.T) {
	for _, c := range []struct {
		name, stub, wantAdvice, wantRaw string
	}{
		{
			name: "namespace not found on the diff step",
			stub: `
case "$1" in
  diff) echo 'Error from server (NotFound): namespaces "demo" not found' >&2; exit 2 ;;
  *) exit 0 ;;
esac`,
			wantAdvice: "kubectl create namespace demo",
			wantRaw:    `namespaces "demo" not found`,
		},
		{
			name: "unknown kind on the dry-run step",
			stub: `
case "$1" in
  diff) exit 1 ;;
  *) echo 'error: unable to recognize "app.yaml": no matches for kind "Certificate" in version "cert-manager.io/v1"' >&2; exit 1 ;;
esac`,
			wantAdvice: "CustomResourceDefinition",
			wantRaw:    `no matches for kind "Certificate"`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := stubBin(t, "kubectl", c.stub)
			out, _ := runHook(t, bashInput("kubectl apply -f app.yaml -n demo", "k8s_editor"), dir)
			if got := decisionOf(t, out); got != "deny" {
				t.Fatalf("decision = %q, want deny", got)
			}
			reason := reasonOf(t, out)
			if !strings.Contains(reason, c.wantAdvice) {
				t.Fatalf("reason carries no actionable advice (%q missing):\n%s", c.wantAdvice, reason)
			}
			if !strings.Contains(reason, c.wantRaw) {
				t.Fatalf("the hint replaced the server's own words (%q missing):\n%s", c.wantRaw, reason)
			}
		})
	}
}

// The complementary half, and the reason the hint table can be extended
// cheaply: an error it does not recognise must come back verbatim, with
// nothing invented. A table that quietly attached advice to everything would
// be worse than no table, because the advice would sometimes be wrong AND
// confident.
func TestUnrecognisedServerErrorGetsNoInventedAdvice(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$1" in
  diff) echo 'Error from server: etcdserver: request timed out' >&2; exit 2 ;;
  *) exit 0 ;;
esac`)
	out, _ := runHook(t, bashInput("kubectl apply -f app.yaml -n demo", "k8s_editor"), dir)
	reason := reasonOf(t, out)
	if !strings.Contains(reason, "etcdserver: request timed out") {
		t.Fatalf("the server's own words were lost:\n%s", reason)
	}
	for _, invented := range []string{"does not exist yet", "CustomResourceDefinition", "create namespace"} {
		if strings.Contains(reason, invented) {
			t.Fatalf("advice %q was attached to an unrelated error:\n%s", invented, reason)
		}
	}
}

// Advice the guard itself blocks is worse than no advice: it spends the
// agent's remaining attempts on a second dead end. The namespace hint says
// "create it as its own separately reviewed change", so that command must
// actually reach the reviewer rather than being refused as unvalidatable.
func TestAdvisedRemedyIsItselfFollowable(t *testing.T) {
	dir := stubBin(t, "kubectl", "echo ok\nexit 0")
	out, _ := runHook(t, bashInput("kubectl create namespace demo", "k8s_editor"), dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("decision = %q, want deny-pending-review", got)
	}
	if !strings.Contains(reasonOf(t, out), "has not been reviewed") {
		t.Fatalf("the advised remedy does not reach the reviewer, so the advice dead-ends:\n%s",
			reasonOf(t, out))
	}
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
	manifest := writeManifest(t, "replicas: 1\n")
	in := bashInput("kubectl apply -f "+manifest+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)
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
	in["attestations"] = approveOneSegment(t, in, dir)
	out, _ := runHook(t, in, dir)
	// A deny for an UNRELATED reason (e.g. the attestation not matching)
	// would also fail to mention "ephemeral" and pass the check below for
	// the wrong reason — assert the positive outcome too.
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("an approved, mechanically-clean editor delete was not allowed to proceed: %q", out)
	}
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
  *"diff upgrade"*) exit 7 ;;
  *) exit 0 ;;
esac`)
			// I1 (review round 3): content-binding now walks the chart
			// DIRECTORY (see helm_content_digest), which must resolve to a
			// real Chart.yaml — "./chart" was only ever a placeholder string
			// for the stub, never a real directory, so it would now
			// terminally refuse "not a local directory" before ever
			// reaching check_attested. A real chart is required for this
			// test to exercise what it is actually about (which preview
			// command validate_helm chose), matched by the stub via a
			// wildcard since the real path is a dynamic temp dir.
			chart := writeChart(t)
			// A mechanically-clean change now also needs an APPROVED
			// attestation to PROCEED. Only the "no plugins installed" case
			// reaches that far — "diff plugin installed" already denies
			// mechanically (its dry-run stub exits 7), so discovering a
			// subject via approveOneSegment (which itself requires a "not
			// reviewed" deny) would fail there for the wrong reason; the
			// mechanical deny alone already gives that branch its correct
			// `usedDiffCmd == true` signal.
			in := bashInput("helm upgrade myrel "+chart+" -n demo", "k8s_editor")
			if !tc.pluginListed {
				in["attestations"] = approveOneSegment(t, in, dir)
			}
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
			// "create"/"replace" reach check_attested on a clean dry-run and
			// need an attestation to actually proceed, or the deny would be
			// "not reviewed" rather than the --server-side signal this test
			// reads; "apply" always denies mechanically first (its dry-run
			// stub exits 3), so discovering a subject for it would fail
			// (the probe would see a mechanical deny, not "not reviewed").
			manifest := writeManifest(t, "app: "+tc.verb+"\n")
			in := bashInput("kubectl "+tc.verb+" -f "+manifest+" -n demo", "k8s_editor")
			if !tc.wantServerSide {
				in["attestations"] = approveOneSegment(t, in, dir)
			}
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
				// Attested: a resolvable target now reaches check_attested,
				// and an unattested "not reviewed" deny would be
				// indistinguishable from the dead-end this test guards
				// against.
				in := bashInput("kubectl rollout "+action+" deploy/x -n demo", "k8s_editor")
				in["attestations"] = approveOneSegment(t, in, dir)
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
	in["attestations"] = approveOneSegment(t, in, dir)
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

// subjectHashPy runs the script's OWN subject_hash(tool, verb, argv,
// content_digest) and returns the hex digest. It loads the script as a
// module (rather than `python3 -B script.py`) so `if __name__ ==
// "__main__": main()` never runs and blocks on stdin.
func subjectHashPy(t *testing.T, tool, verb string, argv []string, contentDigest string) string {
	t.Helper()
	payload := map[string]any{
		"tool": tool, "verb": verb, "argv": argv, "content_digest": contentDigest,
	}
	data, err := json.Marshal(payload)
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
p = json.load(sys.stdin)
print(mod.subject_hash(p["tool"], p["verb"], p["argv"], p["content_digest"]))
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

// subject_hash is deliberately UNRELATED to hookstate.HashArgs — see
// subject_hash's own doc comment in config/hooks/k8s-validate.py. HashArgs
// hashes only the raw tool_input and keys the Go engine's attempt/consecutive
// counters, a coarser job with no reason to read the filesystem; subject_hash
// is the Python side's own value, binding a specific segment's normalised
// argv PLUS its manifest target's actual bytes (via content_digest — see
// local_target_digest), so an earlier version of this test that asserted
// subject_hash(tool_input) == HashArgs(tool_input) encoded exactly the
// property this file's review found broken: the subject didn't change when
// the file's content did.
//
// What must hold instead — pinned here — is what the attestation guarantee
// actually depends on: subject_hash is a deterministic, pure function of its
// four inputs, and it is sensitive to EACH of them independently. The
// content-sensitivity half is the property that matters most; it is also
// demonstrated end to end (a real file, rewritten between two runs of the
// SAME command) by TestAttestationDoesNotCrossDifferentChanges below.
func TestSubjectHashIsDeterministicAndSensitiveToContentAndArgv(t *testing.T) {
	script(t) // skips early on windows / no python3, before spending on cases

	argv := []string{"kubectl", "apply", "-f", "change.yaml", "-n", "demo"}
	base := subjectHashPy(t, "kubectl", "apply", argv, "aaaa...content-digest-a")
	again := subjectHashPy(t, "kubectl", "apply", argv, "aaaa...content-digest-a")
	if base != again {
		t.Fatalf("subject_hash is not deterministic for identical inputs: %s vs %s", base, again)
	}

	differentContent := subjectHashPy(t, "kubectl", "apply", argv, "bbbb...content-digest-b")
	if differentContent == base {
		t.Fatalf("subject_hash did not change when ONLY the content digest changed: %s", base)
	}

	differentArgv := subjectHashPy(t, "kubectl", "apply",
		[]string{"kubectl", "apply", "-f", "other.yaml", "-n", "demo"}, "aaaa...content-digest-a")
	if differentArgv == base {
		t.Fatalf("subject_hash did not change when ONLY argv changed: %s", base)
	}

	differentVerb := subjectHashPy(t, "kubectl", "delete", argv, "aaaa...content-digest-a")
	if differentVerb == base {
		t.Fatalf("subject_hash did not change when ONLY the verb changed: %s", base)
	}

	// A verb with no manifest target (content_digest == "") must still be
	// deterministic — the normalised argv alone identifies the change.
	deleteArgv := []string{"kubectl", "delete", "pod", "x", "-n", "demo"}
	empty := subjectHashPy(t, "kubectl", "delete", deleteArgv, "")
	emptyAgain := subjectHashPy(t, "kubectl", "delete", deleteArgv, "")
	if empty != emptyAgain {
		t.Fatalf("subject_hash with an empty content digest is not deterministic: %s vs %s", empty, emptyAgain)
	}
	if empty == base {
		t.Fatalf("an unrelated delete collided with the apply subject: %s", empty)
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
	manifest := writeManifest(t, "app: missing-attestation\n")
	in := bashInput("kubectl apply -f "+manifest+" -n demo", "k8s_editor")
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
	manifest := writeManifest(t, "app: rejected-attestation\n")
	in := bashInput("kubectl apply -f "+manifest+" -n demo", "k8s_editor")
	in["attempt"] = 3
	in["consecutive"] = 3
	in["attestations"] = rejectOneSegment(t, in, dir, "wrong namespace")
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
	manifest := writeManifest(t, "app: approved-diff\n")
	in := bashInput("kubectl apply -f "+manifest+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)
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
// THE demonstration that the hash binds CONTENT, varying exactly ONE thing at
// a time as the review demanded: the SAME command string, the SAME
// attestations map, across two runs — the only thing that changes between
// them is the target FILE's own bytes, rewritten on disk in place. An
// earlier version of this test instead used two DIFFERENT command strings
// (`-f v1.yaml` vs `-f v2.yaml`), which only proved that two different
// commands get different subjects — a property that was already true before
// this fix, and true independently of content-binding. When a proof's setup
// varies two things at once, it establishes neither.
// `kubectl debug --custom=<file>` is a partial container spec — the bytes
// that decide what the ephemeral container actually IS. It is not a
// -f/--filename target, so nothing bound it, and an attestation would have
// covered an argv merely NAMING the file: approve `--custom=probe.json`,
// rewrite probe.json, replay the identical command. Same shape as the manifest
// case below, on the flag that shape had not reached.
func TestDebugCustomSpecContentBinds(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$1" in
  get) echo '{"apiVersion":"v1","items":[{"kind":"Pod","metadata":{"name":"mypod","namespace":"demo"}}]}' ;;
  *)   echo ok ;;
esac
exit 0`)
	spec := filepath.Join(t.TempDir(), "probe.json")
	if err := os.WriteFile(spec, []byte(`{"image":"busybox"}`), 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	in := bashInput("kubectl debug pod/mypod -n demo --custom="+spec, "k8s_investigator")
	in["attestations"] = approveOneSegment(t, in, dir)

	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("the debug, attested for its own spec content, was refused: %q", out)
	}

	// Vary exactly one thing: the spec file's bytes. The command string and
	// the attestations map are untouched.
	if err := os.WriteFile(spec, []byte(`{"image":"attacker/tools","securityContext":{"privileged":true}}`), 0o644); err != nil {
		t.Fatalf("rewrite spec: %v", err)
	}
	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME debug command, now naming a DIFFERENT container spec, decision = %q, want deny (%s)",
			got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

func TestAttestationDoesNotCrossDifferentChanges(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)    exit 1 ;;
  *dry-run*) exit 0 ;;
  *)         exit 0 ;;
esac`)
	manifest := filepath.Join(t.TempDir(), "change.yaml")
	if err := os.WriteFile(manifest, []byte("replicas: 1\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	// ONE fixed command string, attested once against the file's CURRENT
	// (replicas: 1) content.
	in := bashInput("kubectl apply -f "+manifest+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	outV1, _ := runHook(t, in, dir)
	if got := decisionOf(t, outV1); got == "deny" || got == "ask" {
		t.Fatalf("the change, attested for its own content, was not allowed to proceed: %q", outV1)
	}

	// Vary exactly ONE thing: rewrite the SAME file's bytes in place. `in` —
	// the command string and the attestations map — is untouched.
	if err := os.WriteFile(manifest, []byte("replicas: 99\n"), 0o644); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	outV2, _ := runHook(t, in, dir)
	if got := decisionOf(t, outV2); got != "deny" {
		t.Fatalf("the SAME command, now naming a file with DIFFERENT content, decision = %q, want deny (%s)",
			got, outV2)
	}
	if !strings.Contains(reasonOf(t, outV2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed",
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
	manifest := writeManifest(t, "app: segment-one\n")
	// Segment 1 (apply) mechanically validates cleanly and needs its OWN
	// attestation to clear check_attested. Segment 2 (delete) fails
	// resolve_target's fail-closed JSON parse — if the guard stopped checking
	// after segment 1's attestation cleared, this whole line would proceed
	// and delete an unvalidated resource.
	command := "kubectl apply -f " + manifest + " -n demo && kubectl delete pod x -n demo"
	in := bashInput(command, "k8s_editor")
	// Discover + approve ONLY segment 1's subject: the loop denies there
	// first on an unattested run (it mechanically validates cleanly, so it
	// reaches check_attested before segment 2 is ever examined).
	in["attestations"] = approveOneSegment(t, in, dir)
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
// Each mutating segment of a compound command needs its OWN attestation —
// not one attestation covering the whole line. Per-segment binding is a
// direct consequence of content-binding: two segments can name completely
// different manifests with completely different content, so one shared
// subject for the whole line would let an attestation of the first
// authorize the second's unreviewed content too. Demonstrated by attesting
// one segment at a time and watching the guard deny the NEXT one until it,
// too, is independently attested.
func TestEachMutatingSegmentNeedsItsOwnAttestation(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*)      echo "~ segment one"; exit 1 ;;
  *dry-run*)   echo "segment one applied (dry run)"; exit 0 ;;
  *"-o json"*) echo '{"metadata":{"name":"x","labels":{}}}'; exit 0 ;;
  *)           exit 0 ;;
esac`)
	manifest := writeManifest(t, "app: two-segments\n")
	command := "kubectl apply -f " + manifest + " -n demo && kubectl delete pod x -n demo"
	in := bashInput(command, "k8s_editor")
	attestations := map[string]any{}

	// Round 1 (implicit, inside discoverSegmentSubject): neither segment is
	// attested, so the loop denies at segment 1 — it mechanically validates
	// cleanly and reaches check_attested before segment 2 is examined.
	subj1 := discoverSegmentSubject(t, in, dir)
	attestations[subj1] = map[string]any{"verdict": "APPROVED"}
	in["attestations"] = attestations

	// Round 2: segment 1 now clears check_attested; segment 2 also
	// mechanically succeeds (this stub resolves its delete target), so it
	// reaches ITS OWN check_attested and denies "not reviewed" for its own,
	// DIFFERENT subject — proving the two segments do NOT share one.
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("segment 2, still unattested, decision = %q, want deny (%s)", got, out)
	}
	if !strings.Contains(reasonOf(t, out), "has not been reviewed") {
		t.Fatalf("segment 2's refusal reason = %q, want \"not reviewed\"", reasonOf(t, out))
	}
	subj2 := subjectFromDeny(t, reasonOf(t, out))
	if subj2 == subj1 {
		t.Fatalf("segment 2 got the SAME subject as segment 1 — two different targets must not collide")
	}
	attestations[subj2] = map[string]any{"verdict": "APPROVED"}
	in["attestations"] = attestations

	// Round 3: both segments independently attested — both proceed, and the
	// combined diff mentions both.
	out, _ = runHook(t, in, dir)
	if got := decisionOf(t, out); got == "deny" || got == "ask" {
		t.Fatalf("two mechanically-clean segments, EACH independently attested, were not allowed to proceed: %q", out)
	}
	if !strings.Contains(out, "apply") || !strings.Contains(out, "delete") {
		t.Fatalf("the combined diff must mention BOTH validated segments: %q", out)
	}
}

// A -f target the guard cannot read bytes from before apply time (kubectl
// fetches it itself) must be refused outright — content binding is
// meaningless against a target that could serve DIFFERENT bytes on the next
// apply while the URL string in the command line stays identical. This is
// the mechanical refusal resolve_target_digest raises; it fires even for a
// FIRST attempt, before attestation is ever considered.
func TestURLManifestTargetIsRefused(t *testing.T) {
	dir := stubBin(t, "kubectl", "exit 0") // never reached — refused first
	out, _ := runHook(t, bashInput(
		"kubectl apply -f https://example.com/manifest.yaml -n demo", "k8s_editor"), dir)
	if decisionOf(t, out) != "deny" {
		t.Fatalf("a URL manifest target decision = %q, want deny", decisionOf(t, out))
	}
	if !strings.Contains(reasonOf(t, out), "remote target") {
		t.Fatalf("reason = %q, want it to explain the target is remote", reasonOf(t, out))
	}
}

// kustomizeAwareKubectlStub is a kubectl stub whose "kustomize" subcommand
// delegates to the REAL kubectl binary (kustomize renders locally with no
// cluster contact at all — KUBECTL_LOCAL_VERBS already treats it that way
// in the guard itself) while every OTHER invocation (diff, --dry-run=server,
// …) follows `mechanicalBody`, a /bin/sh case block in the same shape every
// other stubBin caller in this file writes. Round 1's version of this test
// stubbed "kustomize" as a bare `exit 0` (empty stdout) — which made the
// render-based digest hash the SAME empty string before and after an edit,
// so the test could not see the exact bug it was meant to pin. Real
// kustomize is deterministic and network-free, so delegating to it keeps
// the mechanical (diff/dry-run) half hermetic while making the render half
// genuine.
func kustomizeAwareKubectlStub(t *testing.T, mechanicalBody string) string {
	t.Helper()
	real, err := exec.LookPath("kubectl")
	if err != nil {
		t.Skip("kubectl not available")
	}
	dir := t.TempDir()
	body := "#!/bin/sh\nif [ \"$1\" = \"kustomize\" ]; then\n  exec " + real + " \"$@\"\nfi\n" + mechanicalBody + "\n"
	if err := os.WriteFile(filepath.Join(dir, "kubectl"), []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return dir
}

const kustomizeMechanicalBody = `
case "$*" in
  *diff*)    exit 1 ;;
  *dry-run*) exit 0 ;;
  *)         exit 0 ;;
esac`

// The same content-binding property TestAttestationDoesNotCrossDifferentChanges
// proves for a -f FILE also holds for a -k kustomize DIRECTORY target when the
// edited file lives DIRECTLY inside the overlay: an attestation is bound to
// what the rendered tree currently contains, not to the directory's path.
// Same discipline — one fixed command string, one attestations map, only the
// target's on-disk bytes change between runs.
func TestKustomizeDirectoryTargetContentBinds(t *testing.T) {
	dir := kustomizeAwareKubectlStub(t, kustomizeMechanicalBody)
	overlay := t.TempDir()
	writeFile := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(overlay, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	writeFile("kustomization.yaml", "resources:\n- deploy.yaml\n")
	writeFile("deploy.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\nspec:\n  replicas: 1\n")

	in := bashInput("kubectl apply -k "+overlay+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the overlay, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: a file INSIDE the same overlay directory.
	writeFile("deploy.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\nspec:\n  replicas: 99\n")

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME command, overlay directory now holding DIFFERENT content, decision = %q, want deny (%s)",
			got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

// I1 (review round 2): a walk over the overlay directory ALONE is blind to a
// base referenced via `resources: [../base]` — the dominant kustomize idiom
// — so editing the base manifest left the round-1 (walk-based) digest
// unchanged; reproduced live before the fix (see the fix commit's message
// for the exact before/after digest). Rendering via `kubectl kustomize`
// (this round's fix) sees straight through the reference.
func TestKustomizeBaseReferenceContentBinds(t *testing.T) {
	dir := kustomizeAwareKubectlStub(t, kustomizeMechanicalBody)
	root := t.TempDir()
	base := filepath.Join(root, "base")
	overlay := filepath.Join(root, "overlay")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir base: %v", err)
	}
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(base, "kustomization.yaml"), "resources:\n- deploy.yaml\n")
	write(filepath.Join(base, "deploy.yaml"),
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\nspec:\n  replicas: 1\n")
	write(filepath.Join(overlay, "kustomization.yaml"), "resources:\n- ../base\n")

	in := bashInput("kubectl apply -k "+overlay+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the overlay, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: the BASE manifest, referenced but never named
	// on the command line and OUTSIDE the overlay directory the command names.
	write(filepath.Join(base, "deploy.yaml"),
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\nspec:\n  replicas: 99\n")

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME overlay command, its BASE now holding DIFFERENT content, decision = %q, want deny (%s)",
			got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

// I1 (review round 2), the other half: a resource reached through a
// SYMLINKED subdirectory. os.walk does not follow directory symlinks by
// default, so the round-1 walk-based digest was blind to this too;
// `kubectl kustomize` resolves it correctly since kustomize itself follows
// the reference (verified live before writing this test).
func TestKustomizeSymlinkedResourceContentBinds(t *testing.T) {
	dir := kustomizeAwareKubectlStub(t, kustomizeMechanicalBody)
	root := t.TempDir()
	overlay := filepath.Join(root, "overlay")
	real := filepath.Join(root, "real-subdir")
	if err := os.MkdirAll(overlay, 0o755); err != nil {
		t.Fatalf("mkdir overlay: %v", err)
	}
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir real-subdir: %v", err)
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(filepath.Join(real, "kustomization.yaml"), "resources:\n- extra.yaml\n")
	write(filepath.Join(real, "extra.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: extra\ndata:\n  k: v1\n")
	if err := os.Symlink(real, filepath.Join(overlay, "symlinked")); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
	write(filepath.Join(overlay, "kustomization.yaml"), "resources:\n- symlinked\n")

	in := bashInput("kubectl apply -k "+overlay+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the overlay, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: the file reached only THROUGH the symlink.
	write(filepath.Join(real, "extra.yaml"), "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: extra\ndata:\n  k: v2\n")

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME overlay command, its symlinked resource now holding DIFFERENT content, decision = %q, want deny (%s)",
			got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

// --- Minor: the joined systemMessage must be capped too ---

// Each validated segment's own preview is capped to 4000 chars, but that cap
// is PER segment — a compound command with many mutating segments can still
// join into an unbounded systemMessage. Exercised directly against
// build_proceed_note (loaded as a module, like subjectHashPy) rather than
// running six real mutating segments through the whole guard: this is a pure
// string-formatting property, and this is the narrower, faster way to pin it.
func TestJoinedSystemMessageIsCapped(t *testing.T) {
	script(t)
	py := `
import importlib.util
spec = importlib.util.spec_from_file_location("k8s_validate", ` + strconv.Quote(script(t)) + `)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
validated = [("kubectl", "apply", "X" * 5000) for _ in range(6)]
note = mod.build_proceed_note(validated)
print(len(note))
print(mod.SYSTEM_MESSAGE_MAX)
print("truncated" in note)
`
	cmd := exec.Command("python3", "-B", "-c", py)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("build_proceed_note failed: %v\nstderr: %s", err, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected output: %q", out.String())
	}
	noteLen, maxLen := lines[0], lines[1]
	if lines[2] != "True" {
		t.Fatalf("a truncated joined note must say so: length=%s max=%s mentions truncation=%s",
			noteLen, maxLen, lines[2])
	}
	// 30000 chars of raw preview material (6 * 5000, itself already under the
	// 4000-per-segment cap) must not survive intact into one systemMessage.
	if noteLen == "30000" {
		t.Fatal("the joined note was not capped at all")
	}
}

// --- I1 (review round 3): Helm chart/values content binding, via a WALK
// of the chart directory rather than a `helm template` render (round 2's
// design, retired — see helm_content_digest's own docstring for why). No
// real helm binary is needed for these tests any more: content-binding no
// longer shells out to helm at all, only validate_helm's OWN mechanical
// diff/dry-run does, which the plain stubBin(t, "helm", …) below already
// covers.

// No diff plugin listed, so validate_helm falls to its --dry-run=server
// path, which this body accepts unconditionally.
const helmMechanicalBody = `
case "$*" in
  "plugin list") echo "NAME VERSION"; exit 0 ;;
  *)             exit 0 ;;
esac`

// writeChart writes a minimal, real Helm chart (Chart.yaml + one templated
// Deployment) under a fresh temp dir and returns the chart directory.
func writeChart(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, contents string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("Chart.yaml", "apiVersion: v2\nname: demo\nversion: 0.1.0\n")
	write("templates/deploy.yaml",
		"apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\nspec:\n  replicas: {{ .Values.replicas }}\n")
	return dir
}

// Reproduced live before helm_content_digest existed: round 1's design hashed
// only whatever -f/-k NAMED, so `helm upgrade myrel ./chart -f values.yaml`
// bound values.yaml's bytes but never the CHART DIRECTORY's own content —
// editing a template file inside ./chart proceeded unchanged. Same
// discipline throughout this file: one fixed command string, one
// attestations map, only the chart's on-disk bytes change between runs.
func TestHelmChartDirectoryContentBinds(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	chart := writeChart(t)
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte("replicas: 1\n"), 0o644); err != nil {
		t.Fatalf("write values: %v", err)
	}

	in := bashInput("helm upgrade myrel "+chart+" -f "+values+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the chart, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: a TEMPLATE file inside the chart directory —
	// never itself named by -f/-k on the command line.
	tmpl := filepath.Join(chart, "templates", "deploy.yaml")
	data, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	edited := strings.ReplaceAll(string(data), "replicas }}", "replicas }}  # edited")
	if err := os.WriteFile(tmpl, []byte(edited), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME command, chart TEMPLATE now holding DIFFERENT content, decision = %q, want deny (%s)",
			got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

// The flag-name asymmetry: Helm's "-f" and "--values" are exact synonyms,
// but round 1's MANIFEST_TARGET_FLAGS (kubectl-oriented: "-f"/"--filename"/
// "-k"/"--kustomize") only matched the SHORT spelling by accident (it shares
// "-f" with kubectl), so a values file named via "--values" was never
// content-bound by EITHER spelling under that design — the render-based fix
// must cover both identically. One table, one values file, two flag
// spellings, same discipline (fixed command, fixed attestation, only the
// values file's bytes change between the two runs each spelling gets).
func TestHelmValuesFlagSynonymsBothContentBind(t *testing.T) {
	for _, flag := range []string{"-f", "--values"} {
		t.Run(flag, func(t *testing.T) {
			dir := stubBin(t, "helm", helmMechanicalBody)
			chart := writeChart(t)
			values := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(values, []byte("replicas: 1\n"), 0o644); err != nil {
				t.Fatalf("write values: %v", err)
			}

			in := bashInput("helm upgrade myrel "+chart+" "+flag+" "+values+" -n demo", "k8s_editor")
			in["attestations"] = approveOneSegment(t, in, dir)

			out1, _ := runHook(t, in, dir)
			if got := decisionOf(t, out1); got == "deny" || got == "ask" {
				t.Fatalf("%s: attested for its own content, was not allowed to proceed: %q", flag, out1)
			}

			if err := os.WriteFile(values, []byte("replicas: 99\n"), 0o644); err != nil {
				t.Fatalf("rewrite values: %v", err)
			}

			out2, _ := runHook(t, in, dir)
			if got := decisionOf(t, out2); got != "deny" {
				t.Fatalf("%s: SAME command, values file now DIFFERENT content, decision = %q, want deny (%s)",
					flag, got, out2)
			}
			if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
				t.Fatalf("%s: refusal reason = %q, want \"not reviewed\"", flag, reasonOf(t, out2))
			}
		})
	}
}

// --- M2 (review round 2, raised from Minor): content-binding refusals must
// never escalate ---

// A URL/remote target, an empty -f=/-k= value, and a missing target are all
// "cannot bind content" states — the guard could never even identify what
// would be applied. check_attested's own docstring explains why THAT class
// of refusal must be terminal: escalating to `ask` after MAX_ATTEMPTS would
// let one user click substitute for a review of content that was never
// identified, which is precisely the state a click is least safe in. These
// three new (round 2) refusal paths get the same property, exercised at
// attempt=consecutive=3 — the exact threshold that would turn any ordinary
// refuse() into "ask".
func TestContentBindingRefusalsNeverEscalate(t *testing.T) {
	for _, tc := range []struct {
		name, command, wantReason string
	}{
		{"URL target", "kubectl apply -f https://example.com/manifest.yaml -n demo", "remote target"},
		{"empty target", "kubectl apply -f= -n demo", "empty target"},
		{"missing target", "kubectl apply -f /does/not/exist-" + t.Name() + ".yaml -n demo", "could not be found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := stubBin(t, "kubectl", "exit 0")
			in := bashInput(tc.command, "k8s_editor")
			in["attempt"] = 3
			in["consecutive"] = 3
			out, _ := runHook(t, in, dir)
			if got := decisionOf(t, out); got != "deny" {
				t.Fatalf("%s at attempt=consecutive=3, decision = %q, want deny (never ask): %s", tc.name, got, out)
			}
			if !strings.Contains(reasonOf(t, out), tc.wantReason) {
				t.Fatalf("%s: reason = %q, want it to mention %q", tc.name, reasonOf(t, out), tc.wantReason)
			}
		})
	}
}

// M1: an unreadable target (permission denied, a dangling symlink, …) must
// report a clean diagnostic — TERMINALLY, same reasoning as
// TestContentBindingRefusalsNeverEscalate — not an uncaught OSError
// traceback, which the file's own doctrine on tracebacks (see
// TestMalformedToolInputDoesNotTraceback) rejects everywhere else.
func TestUnreadableManifestTargetIsRefusedNotATraceback(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission bits do not block root's own reads")
	}
	dir := stubBin(t, "kubectl", "exit 0")
	manifestDir := t.TempDir()
	path := filepath.Join(manifestDir, "secret.yaml")
	if err := os.WriteFile(path, []byte("apiVersion: v1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(path, 0o644) // let t.TempDir() clean up afterward

	in := bashInput("kubectl apply -f "+path+" -n demo", "k8s_editor")
	in["attempt"] = 3
	in["consecutive"] = 3
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("unreadable target at attempt=3, decision = %q, want deny (never ask): %s", got, out)
	}
	if !strings.Contains(reasonOf(t, out), "could not be read") {
		t.Fatalf("reason = %q, want it to explain the target could not be read", reasonOf(t, out))
	}
}

// --- I1 (review round 3): the render was retired; cover the canonical
// shapes whose absence from earlier coverage let the flag-table version
// ship in the first place ---

// THE critical proof: every canonical helm upgrade/install flag must reach
// a change identifier ("has not been reviewed"), never a flag refusal. The
// retired `helm template`-replay design terminally denied each of these —
// none of --install/--set/--wait/--atomic/--timeout/--version/
// --create-namespace/--generate-name is in any kubectl-oriented flag set —
// even though a bare `helm upgrade myrel <chart>` was fine, making
// `helm upgrade --install` (the canonical idiom) and `--set` (the single
// most common override) both unusable.
func TestHelmCanonicalFlagsReachAChangeIdentifier(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	chart := writeChart(t)
	for _, tc := range []struct{ name, command string }{
		{"--install", "helm upgrade myrel " + chart + " --install -n demo"},
		{"--set", "helm upgrade myrel " + chart + " --set replicas=2 -n demo"},
		{"--atomic", "helm upgrade myrel " + chart + " --atomic -n demo"},
		{"--wait", "helm upgrade myrel " + chart + " --wait -n demo"},
		{"--timeout", "helm upgrade myrel " + chart + " --timeout 5m -n demo"},
		{"--version", "helm upgrade myrel " + chart + " --version 1.2.3 -n demo"},
		{"--create-namespace", "helm upgrade myrel " + chart + " --install --create-namespace -n demo"},
		{"--generate-name", "helm install " + chart + " --generate-name -n demo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, _ := runHook(t, bashInput(tc.command, "k8s_editor"), dir)
			if got := decisionOf(t, out); got != "deny" {
				t.Fatalf("expected an unattested run to deny, got %q: %s", got, out)
			}
			if !strings.Contains(reasonOf(t, out), "has not been reviewed") {
				t.Fatalf("%s: expected a change identifier (\"has not been reviewed\"), "+
					"got a DIFFERENT refusal — a flag-table version would deny here instead: %q",
					tc.name, reasonOf(t, out))
			}
		})
	}
}

// Helm stores a chart's dependencies INSIDE the chart directory (charts/) —
// unlike a kustomization's `resources: [../../base]`, nothing in a chart
// references outside itself, which is exactly why a walk suffices here and
// never did for kustomize. crds/ is part of that same walk: `helm template`
// (the retired design) excludes crds/ unless --include-crds, while a real
// install/upgrade applies them — reproduced live before this fix (see the
// commit message) by rewriting a CRD's own scope with the render-based
// subject left UNCHANGED. Same discipline as every other content-binding
// test: one fixed command, one attestation, only the CRD's bytes change.
func TestHelmChartCRDsContentBind(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	chart := writeChart(t)
	crd := filepath.Join(chart, "crds", "crd.yaml")
	if err := os.MkdirAll(filepath.Dir(crd), 0o755); err != nil {
		t.Fatalf("mkdir crds: %v", err)
	}
	write := func(scope string) {
		t.Helper()
		body := "apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\n" +
			"metadata:\n  name: widgets.example.com\nspec:\n  scope: " + scope + "\n"
		if err := os.WriteFile(crd, []byte(body), 0o644); err != nil {
			t.Fatalf("write crd: %v", err)
		}
	}
	write("Cluster")

	in := bashInput("helm upgrade myrel "+chart+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the chart, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: the CRD's scope — a file `helm template`
	// would never even have rendered.
	write("Namespaced")

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME command, CRD scope now DIFFERENT, decision = %q, want deny (%s)", got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

// A chart reference that is not a LOCAL DIRECTORY (a repo alias, an OCI
// reference, a packaged .tgz, a URL) has nothing on this machine to bind —
// the same "cannot verify, so cannot attest" state a remote -f manifest is
// in, and TERMINAL for the identical reason (see
// TestContentBindingRefusalsNeverEscalate): a click past three attempts
// must not substitute for a review of content that was never identified.
func TestHelmNonLocalChartReferenceIsRefusedTerminally(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	in := bashInput("helm upgrade myrel bitnami/nginx -n demo", "k8s_editor")
	in["attempt"] = 3
	in["consecutive"] = 3
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("a non-local chart reference at attempt=3, decision = %q, want deny (never ask): %s", got, out)
	}
	if !strings.Contains(reasonOf(t, out), "local directory") {
		t.Fatalf("reason = %q, want it to explain the chart is not a local directory", reasonOf(t, out))
	}
}

// A stray unreadable entry INSIDE the walked chart directory — unrelated to
// the actual change — must not dead-end the whole apply the way an
// unreadable DIRECTLY-NAMED target still correctly does
// (TestUnreadableManifestTargetIsRefusedNotATraceback): _walk_entry_digest
// binds its presence/type via a sentinel instead of refusing. This is the
// same property TestUnreadableManifestTargetIsRefusedNotATraceback pins for
// kubectl, exercised here for a Helm chart directory specifically.
func TestHelmChartStrayUnreadableFileDoesNotBlockTheApply(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root — permission bits do not block root's own reads")
	}
	dir := stubBin(t, "helm", helmMechanicalBody)
	chart := writeChart(t)
	stray := filepath.Join(chart, "stray.bin")
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}
	if err := os.Chmod(stray, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(stray, 0o644)

	out, _ := runHook(t, bashInput("helm upgrade myrel "+chart+" -n demo", "k8s_editor"), dir)
	if !strings.Contains(reasonOf(t, out), "has not been reviewed") {
		t.Fatalf("an unrelated unreadable stray file inside the chart must not dead-end "+
			"the whole apply — expected a change identifier, got: %q", reasonOf(t, out))
	}
}

// Per-run memoisation: a compound command naming the SAME -k target twice
// must render it only once. Exercised directly against
// resolve_kustomize_target_digest (loaded as a module) by instrumenting
// _kustomize_render_digest to count its own invocations — narrower and
// faster than driving the whole guard through a compound command, and
// avoids relying on timing (which would be flaky).
func TestKustomizeRenderIsMemoizedByTargetPath(t *testing.T) {
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not available")
	}
	overlay := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(overlay, name), []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("kustomization.yaml", "resources:\n- deploy.yaml\n")
	write("deploy.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: demo\nspec:\n  replicas: 1\n")

	py := `
import importlib.util
spec = importlib.util.spec_from_file_location("k8s_validate", ` + strconv.Quote(script(t)) + `)
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)

calls = []
orig = mod._kustomize_render_digest
def counted(path, cwd):
    calls.append(path)
    return orig(path, cwd)
mod._kustomize_render_digest = counted

d1 = mod.resolve_kustomize_target_digest(None, ` + strconv.Quote(overlay) + `)
d2 = mod.resolve_kustomize_target_digest(None, ` + strconv.Quote(overlay) + `)
print(d1 == d2)
print(len(calls))
`
	cmd := exec.Command("python3", "-B", "-c", py)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if lines[0] != "True" {
		t.Fatalf("the two digests for the SAME target must be identical, got equal=%s", lines[0])
	}
	if lines[1] != "1" {
		t.Fatalf("kubectl kustomize invoked %s time(s) for the same target resolved twice, want 1 (memoised)", lines[1])
	}
}

// --- I1 (review round 4): a flag's value must never be read as the chart
// operand ---

// writeChartAt is writeChart's shape, but at a caller-chosen directory
// (writeChart always makes a fresh t.TempDir()) — needed here because the
// decoy and the real chart must be two SIBLING directories under one
// t.TempDir(), not two independent temp roots.
func writeChartAt(t *testing.T, dir, configMapName string) {
	t.Helper()
	write := func(rel, contents string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("Chart.yaml", "apiVersion: v2\nname: demo\nversion: 0.1.0\n")
	write("templates/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: "+configMapName+"\n")
}

// THE bypass the coordinator measured in round 4: a decoy chart directory
// positioned as a flag's value (--kubeconfig, which happens to be in the
// KNOWN value-flag set) used to be read as the chart operand — being the
// FIRST bare token the old first-match scan found — instead of the real
// chart named later on the same command line. The CURRENT rule (round 5) is
// purely positional — any bare token immediately after ANY "-"-prefixed
// token is excluded, whether or not that flag is "known" — so this case now
// passes for a structural reason, not because --kubeconfig happens to be
// recognised. See TestHelmUnlistedFlagDecoyWithRemoteChartIsRefusedWithNoIdentifier
// for the harder case this rewrite closes: the identical shape behind a
// flag this guard has never heard of. Same discipline as every other
// content-binding test: one fixed command, one attestation, vary exactly
// one directory's content at a time.
func TestHelmFlagValueDecoyDoesNotBindInsteadOfRealChart(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	root := t.TempDir()
	real := filepath.Join(root, "chart")
	decoy := filepath.Join(root, "decoy")
	writeChartAt(t, real, "real-configmap")
	writeChartAt(t, decoy, "decoy-configmap")

	command := "helm upgrade --kubeconfig " + decoy + " myrel " + real + " -n demo"
	in := bashInput(command, "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the command, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: the REAL chart (named as the ordinary chart
	// operand, not hidden behind any flag).
	writeChartAt(t, real, "SNEAKY-configmap")

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME command, the REAL chart rewritten, decision = %q, want deny (%s) — "+
			"the decoy named by --kubeconfig was bound instead of the real chart", got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}

	// The complement: mutating the DECOY alone (real chart back to its
	// attested content) must NOT change the subject at all — proving the
	// decoy plays no part in the binding, not merely that a re-run works.
	writeChartAt(t, real, "real-configmap")
	subjBefore := discoverSegmentSubject(t, in, dir)
	writeChartAt(t, decoy, "decoy-edited")
	subjAfter := discoverSegmentSubject(t, in, dir)
	if subjBefore != subjAfter {
		t.Fatalf("editing ONLY the decoy changed the subject (%s -> %s) — "+
			"the decoy must play no part in content-binding", subjBefore, subjAfter)
	}
}

// If more than one operand on the command line resolves to a real local
// chart, which one Helm would actually use cannot be determined from here
// — the earlier (buggy) design picked the first one found, unconditionally,
// which is exactly how the decoy bypass above was possible. Refusing as
// ambiguous is the fail-closed alternative to guessing.
func TestHelmAmbiguousChartOperandsRefusedTerminally(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	root := t.TempDir()
	chartA := filepath.Join(root, "chart-a")
	chartB := filepath.Join(root, "chart-b")
	writeChartAt(t, chartA, "a")
	writeChartAt(t, chartB, "b")

	in := bashInput("helm upgrade myrel "+chartA+" "+chartB+" -n demo", "k8s_editor")
	in["attempt"] = 3
	in["consecutive"] = 3
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("two candidate charts at attempt=3, decision = %q, want deny (never ask): %s", got, out)
	}
	if !strings.Contains(reasonOf(t, out), "more than one possible chart target") {
		t.Fatalf("reason = %q, want it to explain the ambiguity", reasonOf(t, out))
	}
}

// A local packaged chart (.tgz) is a file whose bytes can be hashed
// directly — strictly better than refusing it, since binding it costs
// nothing a remote/non-local reference would need (a fetch, a build). Same
// discipline: one fixed command, one attestation, only the tarball's own
// bytes (rebuilt at the same path) change between runs.
func TestHelmLocalPackagedChartContentBinds(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	root := t.TempDir()
	tgz := filepath.Join(root, "chart-0.1.0.tgz")
	writeTgz := func(contents string) {
		t.Helper()
		f, err := os.Create(tgz)
		if err != nil {
			t.Fatalf("create tgz: %v", err)
		}
		defer f.Close()
		gz := gzip.NewWriter(f)
		defer gz.Close()
		tw := tar.NewWriter(gz)
		defer tw.Close()
		data := []byte(contents)
		if err := tw.WriteHeader(&tar.Header{Name: "data.txt", Mode: 0o644, Size: int64(len(data))}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	writeTgz("v1 content\n")

	in := bashInput("helm upgrade myrel "+tgz+" -n demo", "k8s_editor")
	in["attestations"] = approveOneSegment(t, in, dir)

	out1, _ := runHook(t, in, dir)
	if got := decisionOf(t, out1); got == "deny" || got == "ask" {
		t.Fatalf("the tarball, attested for its own content, was not allowed to proceed: %q", out1)
	}

	// Vary exactly ONE thing: rebuild the SAME tarball path with different
	// bytes inside it.
	writeTgz("v2 DIFFERENT content\n")

	out2, _ := runHook(t, in, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("the SAME command, tarball rebuilt with DIFFERENT content, decision = %q, want deny (%s)",
			got, out2)
	}
	if !strings.Contains(reasonOf(t, out2), "has not been reviewed") {
		t.Fatalf("the refusal reason = %q, want it to say the change was not reviewed", reasonOf(t, out2))
	}
}

// --- round 5: the candidate rule must be POSITIONAL, not flag-list-based ---

// THE bypass the coordinator measured in round 5: `--post-renderer` is a
// real Helm flag that takes a value, but it is in NEITHER VALUE_FLAGS NOR
// HELM_VALUES_FLAGS — the two sets round 4's fix skipped values for. So a
// decoy chart directory named as ITS value was still scanned as a bare
// candidate, and being the ONLY local candidate on a line whose real chart
// (bitnami/nginx) is a remote repo alias with nothing on this machine to
// bind, the decoy was bound, attested, and applied in place of a chart that
// was never local at all. The fix makes the rule purely positional (skip
// any token immediately after ANY "-"-prefixed token, known or not), which
// closes this by construction — there is no set of flags to have missed.
//
// The critical assertion is not just "deny": it is that NO change
// identifier is ever produced for this shape, at any attempt count. A
// content-binding refusal is written with emit() directly (see
// helm_content_digest), never refuse() — so, unlike an ordinary "has not
// been reviewed" refusal, it structurally cannot escalate to "ask" no
// matter how many times the same command is retried. A user click must
// never substitute for a review of content this guard could not pin down.
func TestHelmUnlistedFlagDecoyWithRemoteChartIsRefusedWithNoIdentifier(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	root := t.TempDir()
	decoy := filepath.Join(root, "decoy")
	writeChartAt(t, decoy, "decoy-configmap")

	command := "helm upgrade myrel bitnami/nginx --post-renderer " + decoy + " -n demo"

	// attempt=1: the very first try must already refuse, with no identifier.
	in1 := bashInput(command, "k8s_editor")
	out1, _ := runHook(t, in1, dir)
	if got := decisionOf(t, out1); got != "deny" {
		t.Fatalf("remote chart with a decoy behind an UNLISTED flag, decision = %q, want deny: %s", got, out1)
	}
	reason1 := reasonOf(t, out1)
	if changeIDRe.MatchString(reason1) {
		t.Fatalf("a change identifier was issued for a chart this guard could not verify as local — "+
			"the decoy must never be bound, attested, or applied: %q", reason1)
	}
	if !strings.Contains(reason1, "in a position this guard can verify") {
		t.Fatalf("reason = %q, want the content-binding refusal, not some other denial", reason1)
	}

	// attempt=3, consecutive=3: still "deny", never "ask" — proving this
	// refusal is TERMINAL and does not escalate like an ordinary
	// not-yet-reviewed refusal would.
	in2 := bashInput(command, "k8s_editor")
	in2["attempt"] = 3
	in2["consecutive"] = 3
	out2, _ := runHook(t, in2, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("same command at attempt=3/consecutive=3, decision = %q, want deny (never ask): %s", got, out2)
	}
	reason2 := reasonOf(t, out2)
	if changeIDRe.MatchString(reason2) {
		t.Fatalf("a change identifier was issued at attempt=3 — this must stay unidentifiable "+
			"forever, not just on the first try: %q", reason2)
	}
	if strings.Contains(reason2, "Validation has failed") {
		t.Fatalf("reason = %q, escalated to the ask-wrapper text — emit() must bypass refuse() entirely here", reason2)
	}
}

// The accepted cost of the positional rule, ruled explicitly by the
// coordinator: a flag placed BETWEEN the release name and the chart path
// now refuses, even though the chart is perfectly real and local, because
// the token immediately preceding it starts with "-". This is deliberate —
// distinguishing a boolean flag (--atomic) from a value-taking one to
// recover this case needs Helm's own flag-arity table again, exactly the
// unbounded enumeration the positional rule exists to avoid. The refusal
// must name the fix (move the chart path) so the cost is cheap to pay.
func TestHelmFlagBeforeChartRefusedWithPositionalAdvice(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	chart := writeChart(t)

	command := "helm upgrade myrel --atomic " + chart + " -n demo"
	in := bashInput(command, "k8s_editor")
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("real chart preceded by --atomic, decision = %q, want deny (positional rule, accepted cost): %s", got, out)
	}
	reason := reasonOf(t, out)
	if changeIDRe.MatchString(reason) {
		t.Fatalf("a change identifier was issued despite the chart following a flag: %q", reason)
	}
	if !strings.Contains(reason, "immediately after the release name") {
		t.Fatalf("reason = %q, want it to tell the user to move the chart path", reason)
	}

	// Moving the SAME chart to immediately follow the release name (the
	// flag now trailing the chart instead of preceding it) must reach an
	// ordinary change identifier — proving the refusal above was positional
	// friction, not some other problem with this chart or command.
	command2 := "helm upgrade myrel " + chart + " --atomic -n demo"
	in2 := bashInput(command2, "k8s_editor")
	out2, _ := runHook(t, in2, dir)
	if got := decisionOf(t, out2); got != "deny" {
		t.Fatalf("unattested change, decision = %q, want deny (unreviewed, not a flag refusal): %s", got, out2)
	}
	reason2 := reasonOf(t, out2)
	if !changeIDRe.MatchString(reason2) {
		t.Fatalf("moving the chart after the release name should reach a change identifier, got: %q", reason2)
	}
}

// The RELEASE NAME slot is never the chart. This is the hole the positional rule
// opened and then had to close: excluding tokens that follow a flag is necessary
// but not sufficient, because `helm upgrade <release> --atomic <chart>` excludes
// the real chart (the accepted cost) and, if the release name happens to name a
// local chart directory, left it as the SOLE candidate. The digest then bound the
// release-name directory and the real chart was never bound at all — attested
// once, the real chart could be rewritten forever and still proceed. Reproduced
// live before the fix; this pins both halves.
func TestHelmReleaseNameIsNeverTheChartCandidate(t *testing.T) {
	dir := stubBin(t, "helm", helmMechanicalBody)
	real := writeChart(t)
	decoy := writeChart(t) // a second, unrelated local chart directory

	// The release name is spelled as the decoy chart's path, and the real chart
	// follows a flag. Nothing may be bound here, so no identifier may be issued:
	// an identifier is what made the bypass reachable.
	command := "helm upgrade " + decoy + " --atomic " + real + " -n demo"
	out, _ := runHook(t, bashInput(command, "k8s_editor"), dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("decision = %q, want deny: %s", got, out)
	}
	if reason := reasonOf(t, out); changeIDRe.MatchString(reason) {
		t.Fatalf("a change identifier was issued for a release-name directory, so it was "+
			"bound as the chart while the real chart went unbound: %q", reason)
	}

	// And the canonical `--install` idiom must still bind the REAL chart. This is
	// the regression the first attempt at this fix caused: counting positional
	// slots and dropping the first one deleted the chart from
	// `helm upgrade --install <release> <chart>`, because the flag rule had
	// already removed the release name and shifted every later slot. An adjacency
	// rule ("the chart follows a bare token") does not shift.
	install := "helm upgrade --install myrel " + real + " -n demo"
	out2, _ := runHook(t, bashInput(install, "k8s_editor"), dir)
	reason2 := reasonOf(t, out2)
	if !changeIDRe.MatchString(reason2) {
		t.Fatalf("`helm upgrade --install RELEASE CHART` must reach a change identifier, got: %q", reason2)
	}
}

// --- k8s_validator must never be able to mutate what it reviews ---
//
// k8s_validator holds Bash (for read-only kubectl — re-deriving facts from
// the live cluster IS its job) and the attest tools (to record its verdict).
// Nothing about its Go-side wiring stops it from also running a MUTATING
// kubectl/helm command: the tool-list shape that keeps every other agent off
// record_validation (TestOnlyTheValidatorCanAttest) says nothing about
// keeping the reviewer off Bash. So this half of the "the signer is never
// the actor" guarantee has to be policy, enforced here on the already-plumbed
// agent_name field — the same shape as the CLEANUP_AGENTS branch below it.
//
// Two things make an unguarded reviewer worse than an ordinary forged
// signature: (1) check_attested only fires AFTER mechanical validation
// reaches it, and mechanical validation is what computes and discloses the
// change's subject — so a validator with no guard would learn the
// identifier for its OWN attempted mutation for free, then only need an
// attestation (its own tool!) to complete the loop; (2) CLEANUP_AGENTS
// (validate_destructive's ephemeral-label gate) names only k8s_cleaner, so a
// deletion the cleaner is refused for lacking the label was — before this
// guard — not refused for the validator at all: routing a deletion through
// the reviewer was a privilege escalation, not merely a forged signature.

// A validator-issued mutating kubectl is refused, and refused with NO change
// identifier in the message — the identifier is the one thing that makes the
// check_attested chain reachable at all (see its own docstring), so its
// absence here is the property under test, not an incidental detail.
func TestValidatorCannotMutate(t *testing.T) {
	dir := stubFor(t, stubDefaultDeny)
	manifest := writeManifest(t, "app: validator-cannot-mutate\n")
	out, _ := runHook(t, bashInput("kubectl apply -f "+manifest+" -n demo", "k8s_validator"), dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("validator-issued apply decision = %q, want deny", got)
	}
	reason := reasonOf(t, out)
	if !strings.Contains(reason, "may not make changes") {
		t.Fatalf("reason = %q, want the reviewer-may-not-mutate refusal", reason)
	}
	if changeIDRe.MatchString(reason) {
		t.Fatalf("the refusal must not disclose a change identifier: %q", reason)
	}
}

// Terminal, never an escalation to "ask" — exactly the property
// TestMissingAttestationNeverEscalates pins for check_attested, and for the
// identical reason: an escalation would let the reviewer ask the USER for
// permission to sign its own work, which is precisely the hole this guard
// exists to close. attempt/consecutive are AT the escalation threshold.
func TestValidatorCannotMutateNeverEscalates(t *testing.T) {
	dir := stubFor(t, stubDefaultDeny)
	in := bashInput("kubectl delete pod x -n demo", "k8s_validator")
	in["attempt"] = 3
	in["consecutive"] = 3
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("validator-issued delete at attempt=3/consecutive=3 decision = %q, want deny (never ask)", got)
	}
}

// The full chain is dead, not merely its first hop. subject_hash is a pure
// function of tool/verb/argv/content — independent of agent_name (see its
// own doc comment) — so the byte-identical command run as k8s_editor reveals
// exactly what THIS command would hash to: the best case available to an
// adversarial reviewer, a real APPROVED verdict for the exact subject.
// Even armed with that, the validator's own attempt is refused, and refused
// for BEING the validator — never reaching check_attested at all, so the
// reason names the reviewer-may-not-mutate rule, not an attestation outcome.
// A second, byte-identical re-run confirms nothing about having tried it
// once opens a path.
func TestValidatorMutationChainIsFullyDead(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*) exit 1 ;;
  *)      exit 0 ;;
esac`)
	manifest := writeManifest(t, "app: validator-chain-dead\n")
	command := "kubectl apply -f " + manifest + " -n demo"

	// Step 1: attempt the mutation as the reviewer. Denied, with no
	// identifier disclosed — this is TestValidatorCannotMutate's property,
	// re-checked here as the starting point of the sequence below.
	in := bashInput(command, "k8s_validator")
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("step 1 (attempt mutation): decision = %q, want deny", got)
	}
	if changeIDRe.MatchString(reasonOf(t, out)) {
		t.Fatalf("step 1: the refusal disclosed a change identifier: %q", reasonOf(t, out))
	}

	// Step 2: attempt record_validation anyway, for whatever subject can be
	// obtained by OTHER means — here, the byte-identical command discovered
	// via k8s_editor (see the doc comment above for why that is the exact
	// subject this command would hash to). attestations now holds a real
	// APPROVED verdict for it.
	editorIn := bashInput(command, "k8s_editor")
	in["attestations"] = approveOneSegment(t, editorIn, dir)

	// Step 3: re-run the identical mutating command as the reviewer, now
	// armed with that attestation. Still refused, and for the SAME reason as
	// step 1 — proving check_attested was never reached, not merely that
	// something else also happened to deny.
	out, _ = runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("step 3 (retry with an approved attestation): decision = %q, want deny", got)
	}
	if !strings.Contains(reasonOf(t, out), "may not make changes") {
		t.Fatalf("step 3: reason = %q, want the reviewer-may-not-mutate refusal, not a check_attested outcome",
			reasonOf(t, out))
	}

	// Re-run once more, identical in every respect: still refused.
	out, _ = runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("final re-run: decision = %q, want deny", got)
	}
}

// The reviewer's entire job — re-deriving facts from the live cluster — is
// read-only kubectl, and it must keep proceeding completely silently, exactly
// like any other agent's read: the guard above must fire only for a command
// that reaches validate(), never at the top of main() where it would also
// catch reads.
func TestValidatorReadOnlyStillProceeds(t *testing.T) {
	out, code := runHook(t, bashInput("kubectl get pods -n demo", "k8s_validator"), "")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("validator read: exit=%d stdout=%q, want exit 0 and no output", code, out)
	}
}

// The actual review loop must still work for the agents that are SUPPOSED to
// reach it: this guard must not have broken k8s_editor's or k8s_cleaner's own
// mutating commands, which must still reach a change identifier.
func TestEditorAndCleanerStillReachAnIdentifier(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *diff*) exit 1 ;;
  *)      exit 0 ;;
esac`)
	for _, agent := range []string{"k8s_editor", "k8s_cleaner"} {
		manifest := writeManifest(t, "app: "+agent+"\n")
		in := bashInput("kubectl apply -f "+manifest+" -n demo", agent)
		out, _ := runHook(t, in, dir)
		if got := decisionOf(t, out); got != "deny" {
			t.Fatalf("%s: decision = %q, want deny (not reviewed)", agent, got)
		}
		if !changeIDRe.MatchString(reasonOf(t, out)) {
			t.Fatalf("%s: reason has no change identifier: %q", agent, reasonOf(t, out))
		}
	}
}

// Closes the escalation IMPORTANT A found: CLEANUP_AGENTS (the
// ephemeral-label gate inside validate_destructive) names only k8s_cleaner,
// so before this guard, a deletion the CLEANER is refused for lacking
// omnis.dev/ephemeral=true was NOT refused for the VALIDATOR — routing a
// real, unlabelled resource's deletion through the reviewer would have been
// a way around a check the cleanup agent itself cannot get past. Proven the
// same way as TestValidatorMutationChainIsFullyDead: even with a real
// APPROVED attestation for this exact deletion (obtained via k8s_editor —
// who, like the reviewer, is not ephemeral-label-gated either, so this is
// a genuinely obtainable attestation), the validator's own attempt is
// refused for being the reviewer, never reaching validate_destructive (and
// its ephemeral-label check) at all.
func TestValidatorCannotDeleteAnUnlabelledResourceEither(t *testing.T) {
	dir := stubBin(t, "kubectl", `
case "$*" in
  *"-o json"*) echo '{"metadata":{"name":"real-app","labels":{}}}' ;;
  *)           exit 0 ;;
esac`)
	command := "kubectl delete pod real-app -n demo"
	editorIn := bashInput(command, "k8s_editor")
	attestations := approveOneSegment(t, editorIn, dir)

	in := bashInput(command, "k8s_validator")
	in["attestations"] = attestations
	out, _ := runHook(t, in, dir)
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("validator delete of an unlabelled resource, with an approved attestation, decision = %q, want deny", got)
	}
	reason := reasonOf(t, out)
	if !strings.Contains(reason, "may not make changes") {
		t.Fatalf("reason = %q, want the reviewer-may-not-mutate refusal — a different reason means "+
			"validate_destructive ran and the ephemeral-label escape may be open again", reason)
	}
	if changeIDRe.MatchString(reason) {
		t.Fatalf("the refusal must not disclose a change identifier: %q", reason)
	}
}
