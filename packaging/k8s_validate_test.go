package packaging

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
func TestDecisionForCommandShapes(t *testing.T) {
	cases := []struct {
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
		{"boolean global before apply", "kubectl --insecure-skip-tls-verify apply -f app.yaml", "k8s_editor", "deny", "is not implemented yet"},
		{"helm boolean global before upgrade", "helm --debug upgrade myrel ./chart -n demo", "k8s_editor", "deny", "is not implemented yet"},
		{"helm boolean global before uninstall", "helm --debug uninstall myrel -n demo", "k8s_editor", "deny", "is not implemented yet"},
		{"sudo with its own flag", "sudo -n kubectl delete pod x -n demo", "k8s_cleaner", "deny", "is not implemented yet"},
		{"absolute-path wrapper", "/usr/bin/sudo kubectl delete pod x -n demo", "k8s_cleaner", "deny", "is not implemented yet"},
		{"wrapper flag with a value", "sudo -u root kubectl delete pod x -n demo", "k8s_cleaner", "deny", "is not implemented yet"},
		{"newline-separated", "kubectl get pods\nkubectl delete pod x -n demo", "k8s_cleaner", "deny", ""},
		{"ampersand-separated", "kubectl get pods & kubectl delete pod x -n demo", "k8s_cleaner", "deny", ""},
		{"xargs kubectl delete", "kubectl get pods -o name | xargs kubectl delete pod -n demo", "k8s_cleaner", "deny", "is not implemented yet"},
		{"auth reconcile writes RBAC", "kubectl auth reconcile -f rbac.yaml", "k8s_editor", "deny", ""},
		{"config use-context rewrites kubeconfig", "kubectl config use-context prod", "k8s_editor", "deny", ""},
		{"rollout undo writes", "kubectl rollout undo deploy/x -n demo", "k8s_editor", "deny", ""},
		{"exec is not a read", "kubectl exec pod -- rm -rf /data", "k8s_editor", "deny", ""},
		{"helm test creates Pods", "helm test prod-app -n prod", "k8s_editor", "deny", ""},
		// helm's canonical destructive verb under each of its live aliases. All
		// three previously proceeded behind a wrapper and uninstalled unvalidated.
		{"helm delete alias behind sudo", "sudo helm delete myrel -n prod", "k8s_editor", "deny", "is not implemented yet"},
		{"helm del alias behind sudo", "sudo helm del myrel -n prod", "k8s_editor", "deny", "is not implemented yet"},
		{"helm un alias behind sudo", "sudo helm un myrel -n prod", "k8s_editor", "deny", "is not implemented yet"},
		{"helm delete behind timeout", "timeout 60 helm delete myrel -n prod", "k8s_editor", "deny", ""},
		// A value flag shifting the sub-verb in the OPEN direction: these ran a
		// rollback and an RBAC write unvalidated.
		{"value flag hides rollout undo", "kubectl rollout -n status undo deploy/x", "k8s_editor", "deny", ""},
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
		{"helm3 uninstall", "helm3 uninstall myrel -n prod", "k8s_editor", "deny", "is not implemented yet"},
		{"helm2 delete", "helm2 delete myrel", "k8s_editor", "deny", "is not implemented yet"},
		{"suffixed kubectl path", "/usr/local/bin/kubectl.real delete pod x -n demo", "k8s_editor", "deny", "is not implemented yet"},
		{"read then a suffixed mutation", "kubectl get pods; helm3 uninstall myrel -n prod", "k8s_editor", "deny", "is not implemented yet"},

		// ---- proxy hands out a credentialed API endpoint ----
		// The process changes nothing; what it enables is a full read-write API
		// on localhost, and the follow-up curl names neither tool.
		{"proxy is not a read", "kubectl proxy --port=8001", "k8s_investigator", "deny", "is not implemented yet"},

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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := runHook(t, bashInput(c.command, c.agent), "")
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

// The skeleton must decide without running anything. Round 0's worst failure mode
// was the guard executing the very mutation it was inspecting, and nothing tested
// for it — the stub-PATH parameter existed but was passed "" at every call site.
// A stub that records its own invocation makes that detectable.
func TestGuardExecutesNoSubprocess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked.log")
	for _, bin := range []string{"kubectl", "helm"} {
		body := "#!/bin/sh\necho \"$0 $*\" >> " + marker + "\nexit 0\n"
		if err := os.WriteFile(filepath.Join(dir, bin), []byte(body), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", bin, err)
		}
	}
	for _, cmd := range []string{
		"kubectl apply -f app.yaml",
		"kubectl delete pod x -n demo",
		"helm uninstall myrel -n prod",
		"kubectl get pods -n demo",
	} {
		runHook(t, bashInput(cmd, "k8s_editor"), dir)
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the guard executed kubectl/helm while only deciding:\n%s", b)
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
	out, _ := runHook(t, bashInput("cat > /tmp/runbook.sh <<'SH'\nkubectl delete pod x -n demo\nSH", "coder"), "")
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
	in := bashInput("kubectl apply -f app.yaml", "k8s_editor")
	in["attempt"] = 3
	out, _ := runHook(t, in, "")
	if got := decisionOf(t, out); got != "ask" {
		t.Fatalf("third attempt decision = %q, want ask", got)
	}
}
