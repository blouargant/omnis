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
func runHook(t *testing.T, in map[string]any, extraPath string) (string, int) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cmd := exec.Command("python3", script(t))
	cmd.Stdin = bytes.NewReader(data)
	if extraPath != "" {
		// Prepend, so a stub shadows a real kubectl/helm on the machine.
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
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &jo); err != nil {
		t.Fatalf("hook stdout is not the JSON protocol: %q", stdout)
	}
	return jo.HookSpecificOutput.PermissionDecision
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

// Verb identification is where both failure directions live, and neither is
// observable from a bare "did it say something" assertion — which is why the
// first version of this file could not detect that `kubectl -A get pods` was
// denied while `helm --debug uninstall` sailed through. Table-drive the DECISION
// for every shape that has bitten, in both directions.
func TestDecisionForCommandShapes(t *testing.T) {
	cases := []struct {
		name, command, agent, want string // want: "" = proceed
	}{
		// Boolean global flags must not swallow the verb (false-positive axis).
		{"bare -A before a read verb", "kubectl -A get pods", "k8s_investigator", ""},
		{"long boolean global", "kubectl --all-namespaces get pods", "k8s_investigator", ""},
		{"another boolean global", "kubectl --insecure-skip-tls-verify get pods", "k8s_investigator", ""},
		{"value-taking global", "kubectl -n demo get pods", "k8s_investigator", ""},
		{"inline value global", "kubectl --context=prod get pods", "k8s_investigator", ""},
		// ... and must not hide a mutation either (false-negative axis).
		{"boolean global before apply", "kubectl --insecure-skip-tls-verify apply -f app.yaml", "k8s_editor", "deny"},
		{"helm boolean global before upgrade", "helm --debug upgrade r ./c -n demo", "k8s_editor", "deny"},
		{"helm boolean global before uninstall", "helm --debug uninstall r -n demo", "k8s_editor", "deny"},
		// Wrappers must not hide a mutation.
		{"sudo with its own flag", "sudo -n kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		{"absolute-path wrapper", "/usr/bin/sudo kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		{"wrapper flag with a value", "sudo -u root kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		// Separators the first regex missed entirely.
		{"newline-separated", "kubectl get pods\nkubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		{"ampersand-separated", "kubectl get pods & kubectl delete pod x -n demo", "k8s_cleaner", "deny"},
		// Not this guard's business.
		{"grep that merely mentions kubectl", "grep -rn kubectl deploy.sh > /tmp/out", "coder", ""},
		{"redirect on a READ", "kubectl get pods -o json > /tmp/pods.json", "k8s_investigator", ""},
		{"stderr redirect on a read", "kubectl get pods 2>&1", "k8s_investigator", ""},
		{"not kubernetes at all", "go test ./...", "coder", ""},
		// Shapes we refuse to reason about.
		{"heredoc apply", "kubectl apply -f - <<EOF\nkind: Pod\nEOF", "k8s_editor", "deny"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, code := runHook(t, bashInput(c.command, c.agent), "")
			got := decisionOf(t, out)
			if got != c.want {
				t.Fatalf("decision = %q, want %q (exit=%d stdout=%q)", got, c.want, code, out)
			}
			if c.want == "" && strings.TrimSpace(out) != "" {
				t.Fatalf("a proceed must emit nothing, got %q", out)
			}
		})
	}
}

// The fast path: the hook fires on every Bash call in the whole fleet, so
// anything that is not kubectl/helm must proceed with no output.
func TestNonKubernetesCommandProceeds(t *testing.T) {
	out, code := runHook(t, bashInput("go test ./...", "coder"), "")
	if code != 0 || decisionOf(t, out) != "" {
		t.Fatalf("non-k8s command: exit=%d stdout=%q, want exit 0 and no opinion", code, out)
	}
}

// A read-only verb is not a mutation and must not be gated.
func TestReadOnlyKubectlProceeds(t *testing.T) {
	out, code := runHook(t, bashInput("kubectl get pods -n demo", "k8s_investigator"), "")
	if code != 0 || decisionOf(t, out) != "" {
		t.Fatalf("kubectl get: exit=%d stdout=%q, want no opinion", code, out)
	}
}

// The script must never risk executing the mutation itself, so a command shape
// it cannot fully re-tokenise and replay as argv is refused. A heredoc is the
// canonical case, and refusing it pushes toward the declarative manifest path
// the k8s-modification playbook already prefers.
func TestHeredocApplyIsRefusedFailClosed(t *testing.T) {
	out, _ := runHook(t, bashInput("kubectl apply -f - <<EOF\nkind: Pod\nEOF", "k8s_editor"), "")
	if got := decisionOf(t, out); got != "deny" {
		t.Fatalf("heredoc apply decision = %q, want deny", got)
	}
	if !strings.Contains(out, "manifest file") {
		t.Fatalf("the refusal must tell the agent what to do instead: %q", out)
	}
}

// A mutation hidden in the second half of a compound command must still be seen.
func TestCompoundCommandMutationIsCaught(t *testing.T) {
	out, _ := runHook(t, bashInput("kubectl get pods && kubectl delete pod x -n demo", "k8s_cleaner"), "")
	if got := decisionOf(t, out); got == "" {
		t.Fatalf("compound command: got no opinion, want a decision on the delete: %q", out)
	}
}

// Wrappers must not hide a mutation either.
func TestWrapperStrippedMutationIsCaught(t *testing.T) {
	out, _ := runHook(t, bashInput("sudo kubectl delete pod x -n demo", "k8s_cleaner"), "")
	if got := decisionOf(t, out); got == "" {
		t.Fatalf("sudo-wrapped delete: got no opinion, want a decision: %q", out)
	}
}
