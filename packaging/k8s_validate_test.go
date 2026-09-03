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
