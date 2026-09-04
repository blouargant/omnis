package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The validator must be enabled and nested under BOTH mutating agents. It is
// deliberately NOT a squad member: the leader's tool list must not grow, and the
// gatherer doctrine puts a specialist's helper on the specialist.
func TestValidatorIsNestedUnderBothMutatingAgents(t *testing.T) {
	for _, name := range []string{"k8s_editor", "k8s_cleaner"} {
		data := readShippedAgentJSON(t, name)
		if !strings.Contains(data, `"k8s_validator"`) {
			t.Fatalf("%s does not declare k8s_validator in its subagents", name)
		}
	}
}

// It must be able to attest, and it must never be able to mutate the cluster:
// a reviewer that can change what it reviews is not a reviewer.
//
// The mutation half of this CANNOT be proven by a tool-list assertion, and an
// earlier version of this test tried to: it forbade "Write"/"Edit"/"revert"
// while "Bash" — which reaches kubectl/helm, and is how a Kubernetes change
// actually gets made — sits in the very same array. That assertion passed
// while the shipped config had no gate at all stopping a validator-issued
// mutating kubectl, and it was passing precisely because it checked the
// wrong layer: k8s_validator legitimately needs Bash for READ-ONLY kubectl
// (re-deriving facts from the live cluster is its entire job), so the tool
// list can never distinguish "may read" from "may mutate" — only the
// PreToolUse hook (config/hooks/k8s-validate.py) can, since it inspects the
// verb, not just the binary. So this test drives the real shipped hook and
// asserts the property that actually holds: a mutating kubectl/helm command
// whose agent_name is k8s_validator is refused, and refused BEFORE the hook
// ever computes a change identifier for it (the identifier is the one thing
// that makes the attestation check reachable at all, so its absence in the
// refusal is the property, not incidental). The fuller version of this
// guarantee — no escalation to "ask", the editor/cleaner loop still works,
// the cleaner's ephemeral-label gate can't be bypassed by routing through the
// validator — lives in packaging/k8s_validate_test.go, which already has the
// stub/PATH infrastructure this needs; this test is the minimal single-call
// smoke test that belongs beside the wiring it is pinning.
func TestValidatorIsReadOnlyAndCanAttest(t *testing.T) {
	data := readShippedAgentJSON(t, "k8s_validator")
	if !strings.Contains(data, `"attest"`) {
		t.Fatal("k8s_validator must declare the attest tool group, or it cannot record a verdict")
	}

	if runtime.GOOS == "windows" {
		t.Skip("the hook script assumes a POSIX environment")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	script := filepath.Join("..", "config", "hooks", "k8s-validate.py")

	// Hermetic by construction, never the real PATH: a stub kubectl/helm that
	// fails for ANY invocation. This matters here specifically — without it,
	// this test would be exercised against whatever kubectl this machine
	// happens to have (this repo's dev machine has a real, reachable test
	// cluster — see the k8s-test-cluster memory note), and a target that
	// merely fails to resolve on THAT cluster produces the SAME outward shape
	// (deny, no change identifier) as the guard under test — so the test
	// would keep passing even with the guard removed, for the wrong reason.
	// Verified live: with the guard temporarily disabled and no stub, this
	// test still read "deny" (kubectl failing to resolve a nonexistent pod
	// against the real cluster), so it is the REASON TEXT below — unique to
	// this guard — that actually discriminates, not the decision alone.
	dir := t.TempDir()
	for _, bin := range []string{"kubectl", "helm"} {
		if err := os.WriteFile(filepath.Join(dir, bin), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
			t.Fatalf("write stub %s: %v", bin, err)
		}
	}

	in := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{"command": "kubectl delete pod x -n demo"},
		"agent_name":      "k8s_validator",
		"attempt":         1,
		"consecutive":     0,
	}
	payload, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal hook input: %v", err)
	}
	cmd := exec.Command("python3", "-B", script)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	if errb.Len() > 0 {
		t.Fatalf("hook wrote to stderr (a traceback would read as \"no opinion\" and pass silently): %s", errb.String())
	}

	var decision struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			Reason             string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &decision); err != nil {
		t.Fatalf("hook stdout did not parse as a decision: %q (err: %v)", out.String(), err)
	}
	if decision.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("a validator-issued mutating kubectl decision = %q, want deny",
			decision.HookSpecificOutput.PermissionDecision)
	}
	// The discriminating assertion: this exact phrase comes ONLY from the
	// reviewer-may-not-mutate guard, never from a mechanical failure (a stub
	// kubectl failing to resolve the target would say "could not be
	// resolved" instead) — so this is what proves THIS gate fired, not
	// merely that something along the way happened to deny.
	if !strings.Contains(decision.HookSpecificOutput.Reason, "may not make changes") {
		t.Fatalf("reason = %q, want the reviewer-may-not-mutate refusal", decision.HookSpecificOutput.Reason)
	}
	if matched, _ := regexp.MatchString("change identifier `[0-9a-f]{64}`", decision.HookSpecificOutput.Reason); matched {
		t.Fatalf("the refusal must not disclose a change identifier (that is what would make the "+
			"attestation check reachable): %q", decision.HookSpecificOutput.Reason)
	}
}

// validateSubAgentGraph is fatal on an unknown or disabled target, so a
// mistake here breaks the whole config at reload, not just this agent.
func TestShippedFleetResolvesWithTheValidator(t *testing.T) {
	rs, err := loadShippedRuntimeSettings(t)
	if err != nil {
		t.Fatalf("the shipped config must resolve: %v", err)
	}
	found := false
	for _, a := range rs.Agents {
		if a.Name == "k8s_validator" {
			found = true
		}
	}
	if !found {
		t.Fatal("k8s_validator is not enabled in config/agents.json")
	}
}

// loadShippedRuntimeSettings resolves RuntimeSettings against the repo's real
// shipped config/ + registry/ trees, the same way a packaged install lays them
// out under /etc/omnis (config/*.json -> <layer>/*.json, registry/ ->
// <layer>/registry/) — see the "omnis runs from /etc/omnis, not the repo" and
// "OMNIS_CONFIG_DIRS doesn't redirect registry" notes: config/ and registry/
// are SIBLINGS in the checkout, so OMNIS_SYSTEM_CONFIG_DIR must point at a
// directory that has both, not at config/ directly. It builds that layer with
// symlinks in a temp dir rather than copying, and isolates OMNIS_HOME so a
// developer's real $HOME/.omnis never leaks into the resolved settings.
func loadShippedRuntimeSettings(t *testing.T) (RuntimeSettings, error) {
	t.Helper()

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	configDir := filepath.Join(repoRoot, "config")
	registryDir := filepath.Join(repoRoot, "registry")

	sysDir := t.TempDir()
	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatalf("read %s: %v", configDir, err)
	}
	for _, e := range entries {
		if err := os.Symlink(filepath.Join(configDir, e.Name()), filepath.Join(sysDir, e.Name())); err != nil {
			t.Fatalf("symlink %s: %v", e.Name(), err)
		}
	}
	if err := os.Symlink(registryDir, filepath.Join(sysDir, "registry")); err != nil {
		t.Fatalf("symlink registry: %v", err)
	}

	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", sysDir)
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	return ResolveRuntimeSettings(Options{})
}
