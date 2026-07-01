package testrun

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fstools "github.com/blouargant/omnis/core/tools"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDetect covers marker-based framework detection and the priority ordering
// (a Go repo that also ships a package.json resolves to Go).
func TestDetect(t *testing.T) {
	cases := []struct {
		markers []string
		want    string
		ok      bool
	}{
		{[]string{"go.mod"}, "go", true},
		{[]string{"Cargo.toml"}, "cargo", true},
		{[]string{"pyproject.toml"}, "pytest", true},
		{[]string{"package.json"}, "npm", true},
		{[]string{"go.mod", "package.json"}, "go", true}, // go wins over npm
		{[]string{"pom.xml"}, "maven", true},
		{[]string{"mix.exs"}, "mix", true},
		{nil, "", false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, m := range c.markers {
			touch(t, dir, m)
		}
		fw, ok := detect(dir)
		if ok != c.ok || (ok && fw.name != c.want) {
			t.Errorf("detect(%v) = (%q,%v), want (%q,%v)", c.markers, fw.name, ok, c.want, c.ok)
		}
	}
}

// TestCommandBuilders checks the base commands and scope threading, including
// the go package-pattern replacement and Gradle wrapper preference.
func TestCommandBuilders(t *testing.T) {
	if got := byName["go"].command("", ""); got != "go test ./..." {
		t.Errorf("go default = %q", got)
	}
	if got := byName["go"].command("", "./internal/lsp/..."); got != "go test ./internal/lsp/..." {
		t.Errorf("go scoped = %q", got)
	}
	if got := byName["cargo"].command("", "my_test"); got != "cargo test my_test" {
		t.Errorf("cargo scoped = %q", got)
	}
	dir := t.TempDir()
	if got := byName["gradle"].command(dir, ""); got != "gradle test" {
		t.Errorf("gradle without wrapper = %q", got)
	}
	touch(t, dir, "gradlew")
	if got := byName["gradle"].command(dir, ""); got != "./gradlew test" {
		t.Errorf("gradle with wrapper = %q", got)
	}
}

// TestRunValidation confirms invalid scope and unknown framework are rejected
// before anything executes.
func TestRunValidation(t *testing.T) {
	if _, err := run(context.Background(), t.TempDir(), runTestsIn{Framework: "go", Scope: "foo; rm -rf /"}); err == nil {
		t.Error("expected invalid-scope error for a scope with shell characters")
	} else if !strings.Contains(err.Error(), "invalid scope") {
		t.Errorf("wrong error: %v", err)
	}
	if _, err := run(context.Background(), t.TempDir(), runTestsIn{Framework: "cobol"}); err == nil {
		t.Error("expected unknown-framework error")
	}
	if _, err := run(context.Background(), t.TempDir(), runTestsIn{}); err == nil {
		t.Error("expected detection failure in an empty dir")
	}
}

// TestExtractSignalsGo pulls failing test + package names out of go test output.
func TestExtractSignalsGo(t *testing.T) {
	out := strings.Join([]string{
		"--- FAIL: TestAlpha (0.00s)",
		"    alpha_test.go:12: boom",
		"--- FAIL: TestBeta (0.01s)",
		"FAIL",
		"FAIL\texample.com/pkg/x\t0.2s",
		"ok  \texample.com/pkg/y\t0.1s",
	}, "\n")
	_, failing := extractSignals("go", out)
	joined := strings.Join(failing, ",")
	for _, want := range []string{"TestAlpha", "TestBeta", "example.com/pkg/x"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in failing=%v", want, failing)
		}
	}
	if strings.Contains(joined, "pkg/y") {
		t.Errorf("passing package leaked into failing: %v", failing)
	}
}

// TestExtractSignalsPytest captures the summary line and FAILED node ids.
func TestExtractSignalsPytest(t *testing.T) {
	out := strings.Join([]string{
		"FAILED tests/test_a.py::test_one - assert 1 == 2",
		"==== 1 failed, 3 passed in 0.12s ====",
	}, "\n")
	summary, failing := extractSignals("pytest", out)
	if !strings.Contains(summary, "1 failed, 3 passed") {
		t.Errorf("summary = %q", summary)
	}
	if len(failing) != 1 || failing[0] != "tests/test_a.py::test_one" {
		t.Errorf("failing = %v", failing)
	}
}

// TestSummarizeStatus checks the status line for pass/fail/timeout/blocked.
func TestSummarizeStatus(t *testing.T) {
	pass := summarize("go", "go test ./...", fstools.CapturedRun{Stdout: "ok\texample\t0.1s", ExitCode: 0})
	if !strings.HasPrefix(pass, "✓ tests passed") {
		t.Errorf("pass status = %q", pass)
	}
	fail := summarize("go", "go test ./...", fstools.CapturedRun{Stdout: "--- FAIL: TestX (0s)\nFAIL", ExitCode: 1})
	if !strings.HasPrefix(fail, "✗ tests FAILED (exit 1)") || !strings.Contains(fail, "TestX") {
		t.Errorf("fail status = %q", fail)
	}
	to := summarize("go", "go test ./...", fstools.CapturedRun{TimedOut: true, ExitCode: -1})
	if !strings.HasPrefix(to, "✗ tests timed out") {
		t.Errorf("timeout status = %q", to)
	}
	blk := summarize("go", "go test ./...", fstools.CapturedRun{Blocked: true, Stderr: "rm -rf", ExitCode: -1})
	if !strings.HasPrefix(blk, "✗ blocked by safety floor") {
		t.Errorf("blocked status = %q", blk)
	}
}

func TestLastBytes(t *testing.T) {
	if got := lastBytes("short", 100); got != "short" {
		t.Errorf("no-truncate = %q", got)
	}
	got := lastBytes("line1\nline2\nline3\nline4", 12)
	if !strings.HasPrefix(got, "…(truncated)\n") {
		t.Errorf("truncated should be marked: %q", got)
	}
	if strings.Contains(got, "line1") {
		t.Errorf("oldest line should be dropped: %q", got)
	}
}
