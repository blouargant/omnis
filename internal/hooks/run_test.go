package hooks

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hook exec tests assume a POSIX /bin/sh")
	}
}

// run is a small helper that parses a single-event config and runs it.
func run(t *testing.T, event, subject, command string, in Input) Outcome {
	t.Helper()
	cfg, err := Parse([]byte(`{"hooks":{"` + event + `":[{"matcher":"` + subject + `","hooks":[{"command":` + jsonString(command) + `}]}]}}`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg.Run(context.Background(), event, subject, in, t.TempDir(), 10*time.Second)
}

func jsonString(s string) string {
	// minimal JSON string escaping for the test command literals
	out := []byte{'"'}
	for _, r := range s {
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\n':
			out = append(out, '\\', 'n')
		default:
			out = append(out, string(r)...)
		}
	}
	return string(append(out, '"'))
}

// runOne parses a single-command config from c and runs it, so a test can set
// fields (timeout, fail_closed) the string-based `run` helper cannot express.
func runOne(t *testing.T, event, subject string, c Command, in Input, defaultTimeout time.Duration) Outcome {
	t.Helper()
	f := File{Hooks: map[string][]Matcher{event: {{Matcher: subject, Hooks: []Command{c}}}}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cfg.Run(context.Background(), event, subject, in, t.TempDir(), defaultTimeout)
}

func TestRunExitTwoBlocks(t *testing.T) {
	skipOnWindows(t)
	out := run(t, PreToolUse, "Write", `echo "no edits to .env" >&2; exit 2`, Input{ToolName: "Write"})
	if !out.Blocked() {
		t.Fatalf("exit 2 should block, got decision=%v", out.Decision)
	}
	if out.Reason != "no edits to .env" {
		t.Fatalf("block reason = %q, want stderr text", out.Reason)
	}
}

func TestRunJSONPermissionDeny(t *testing.T) {
	skipOnWindows(t)
	cmd := `echo '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"protected"}}'`
	out := run(t, PreToolUse, "Write", cmd, Input{ToolName: "Write"})
	if !out.Blocked() {
		t.Fatalf("permissionDecision deny should block, got %v", out.Decision)
	}
	if out.Reason != "protected" {
		t.Fatalf("reason = %q, want \"protected\"", out.Reason)
	}
}

func TestRunJSONPermissionAllow(t *testing.T) {
	skipOnWindows(t)
	cmd := `echo '{"hookSpecificOutput":{"permissionDecision":"allow"}}'`
	out := run(t, PreToolUse, "Write", cmd, Input{ToolName: "Write"})
	if out.Decision != DecisionAllow {
		t.Fatalf("permissionDecision allow → %v, want DecisionAllow", out.Decision)
	}
}

func TestRunUserPromptAdditionalContext(t *testing.T) {
	skipOnWindows(t)
	// Plain exit-0 stdout becomes additionalContext for UserPromptSubmit.
	out := run(t, UserPromptSubmit, "", `echo "remember: be terse"`, Input{Prompt: "hi"})
	if out.Blocked() {
		t.Fatal("should not block")
	}
	if out.AdditionalContext != "remember: be terse" {
		t.Fatalf("additionalContext = %q", out.AdditionalContext)
	}
}

func TestRunStdinCarriesEventJSON(t *testing.T) {
	skipOnWindows(t)
	// The command inspects the stdin JSON: it denies only when tool_name=Write
	// is present, proving the engine serialised the Input to stdin.
	cmd := `grep -q '"tool_name":"Write"' && echo '{"decision":"block","reason":"saw Write"}' || exit 0`
	out := run(t, PreToolUse, "Write", cmd, Input{ToolName: "Write"})
	if !out.Blocked() || out.Reason != "saw Write" {
		t.Fatalf("stdin JSON not delivered: decision=%v reason=%q", out.Decision, out.Reason)
	}
}

func TestRunNonBlockingExitProceeds(t *testing.T) {
	skipOnWindows(t)
	// A non-zero, non-2 exit is a non-blocking error: the action proceeds.
	out := run(t, PreToolUse, "Write", `echo oops >&2; exit 1`, Input{ToolName: "Write"})
	if out.Blocked() {
		t.Fatal("exit 1 should not block (non-blocking error)")
	}
}

func TestRunNoMatchNoExec(t *testing.T) {
	skipOnWindows(t)
	cfg, _ := Parse([]byte(`{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"command":"exit 2"}]}]}}`))
	out := cfg.Run(context.Background(), PreToolUse, "Write", Input{ToolName: "Write"}, t.TempDir(), time.Second)
	if out.Ran != 0 || out.Blocked() {
		t.Fatalf("non-matching tool should run no hooks: ran=%d blocked=%v", out.Ran, out.Blocked())
	}
}

// A fail_closed hook that crashes must BLOCK. Without this, a validation hook
// whose script has a syntax error silently stops validating.
func TestFailClosedBlocksOnNonZeroExit(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "exit 1", FailClosed: true},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock", out.Decision)
	}
	if out.Reason == "" {
		t.Fatal("a fail-closed block must carry a reason naming the cause")
	}
}

// The packaging failure mode: hooks.json ships but the script does not, so the
// shell returns 127. This must block, not proceed.
func TestFailClosedBlocksOnMissingCommand(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "omnis-definitely-not-a-real-binary-xyz", FailClosed: true},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock", out.Decision)
	}
}

func TestFailClosedBlocksOnTimeout(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "sleep 5", Timeout: 1, FailClosed: true},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionBlock {
		t.Fatalf("decision = %v, want DecisionBlock", out.Decision)
	}
}

// The no-op contract: without the flag, Claude Code semantics are unchanged.
func TestWithoutFailClosedNonZeroExitStillProceeds(t *testing.T) {
	skipOnWindows(t)
	out := runOne(t, PreToolUse, "Bash",
		Command{Command: "exit 1"},
		Input{ToolName: "Bash"}, 10*time.Second)
	if out.Decision != DecisionProceed {
		t.Fatalf("decision = %v, want DecisionProceed (unchanged default)", out.Decision)
	}
}
