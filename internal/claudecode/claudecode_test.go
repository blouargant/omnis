package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/adk"
)

// stubToolContext embeds adk.ToolContext (nil) and overrides only the two
// methods runClaudeCode actually reaches: SessionID() (used directly, and to
// key the resumed-session store) and Value() (fstools.CwdForContext probes the
// context for a WithCwd-planted value before falling back to the session
// resolver — with neither set here it must resolve to "", not panic). Any
// other method would panic on the nil embedded interface, but the tested paths
// never call them. Mirrors the fakeCtx/testCtx pattern in
// internal/settings/settings_test.go, the repo's existing minimal-ToolContext-
// stub convention.
type stubToolContext struct {
	adk.ToolContext
	sid string
}

func (s stubToolContext) SessionID() string { return s.sid }
func (s stubToolContext) Value(any) any     { return nil }

func tcStub(sessionID string) adk.ToolContext {
	return stubToolContext{sid: sessionID}
}

// writeFakeClaude puts a fake `claude` executable first on PATH. It appends its
// argv to $CLAUDE_ARGS_LOG (one invocation per line) and prints a JSON envelope
// carrying a session id derived from whether --resume was passed, so tests can
// assert both the flags sent and the resume round-trip.
func writeFakeClaude(t *testing.T) (argsLog string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> \"" + argsLog + "\"\n" +
		"sid=fresh-123\n" +
		"case \"$*\" in *--resume*) sid=resumed-123;; esac\n" +
		"printf '{\"result\":\"ok\",\"session_id\":\"%s\",\"total_cost_usd\":0.001,\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}' \"$sid\"\n"
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argsLog
}

func TestClaudeCodeFreshThenResume(t *testing.T) {
	argsLog := writeFakeClaude(t)
	SetDepGate(nil) // plain PATH check; fake claude is present
	SetAllowlistResolver(nil)
	t.Cleanup(func() { ForgetSession("sessA") })

	// First call: no prior session ⇒ no --resume; captures the fresh id.
	r1, err := runClaudeCode(tcStub("sessA"), claudeCodeIn{Task: "do a thing"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r1.Result, "ok") {
		t.Fatalf("expected result text, got %q", r1.Result)
	}
	if resumeID("sessA") != "fresh-123" {
		t.Fatalf("session id not captured, got %q", resumeID("sessA"))
	}

	// Second call in the same session: must pass --resume fresh-123.
	if _, err := runClaudeCode(tcStub("sessA"), claudeCodeIn{Task: "next step"}); err != nil {
		t.Fatal(err)
	}
	logged, _ := os.ReadFile(argsLog)
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 invocations, got %d: %q", len(lines), logged)
	}
	if strings.Contains(lines[0], "--resume") {
		t.Fatalf("first call must not resume: %q", lines[0])
	}
	if !strings.Contains(lines[1], "--resume fresh-123") {
		t.Fatalf("second call must resume fresh-123: %q", lines[1])
	}
	// Default allowlist + json format are always present.
	for _, want := range []string{"-p", "--output-format json", "--allowedTools"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("first call missing %q: %q", want, lines[0])
		}
	}
}

func TestClaudeCodeMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no claude
	SetDepGate(nil)
	out, err := runClaudeCode(tcStub("sessB"), claudeCodeIn{Task: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out.Result), "claude") || !strings.Contains(out.Result, "install") {
		t.Fatalf("expected a not-installed notice, got %q", out.Result)
	}
}
