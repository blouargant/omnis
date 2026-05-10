package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunBashSafetyFloorAndOutput(t *testing.T) {
	t.Parallel()

	out, err := RunBash(context.Background(), BashIn{Command: "printf hello"})
	if err != nil {
		t.Fatalf("RunBash() error = %v", err)
	}
	if out != "hello" {
		t.Fatalf("RunBash() = %q, want hello", out)
	}

	blocked, err := RunBash(context.Background(), BashIn{Command: "rm -rf /tmp/demo"})
	if err != nil {
		t.Fatalf("RunBash(blocked) error = %v", err)
	}
	if !strings.Contains(blocked, "command blocked by safety floor") {
		t.Fatalf("blocked output = %q", blocked)
	}
}

func TestRunBashAllSafetyPatterns(t *testing.T) {
	t.Parallel()

	for _, cmd := range alwaysBlock {
		out, err := RunBash(context.Background(), BashIn{Command: cmd})
		if err != nil {
			t.Fatalf("RunBash(%q) error = %v", cmd, err)
		}
		if !strings.Contains(out, "command blocked by safety floor") {
			t.Fatalf("RunBash(%q) not blocked: %q", cmd, out)
		}
	}
}

func TestRunBashNoOutput(t *testing.T) {
	t.Parallel()

	out, err := RunBash(context.Background(), BashIn{Command: "true"})
	if err != nil {
		t.Fatalf("RunBash() error = %v", err)
	}
	if out != "(no output)" {
		t.Fatalf("RunBash(true) = %q, want (no output)", out)
	}
}

func TestRunBashTimeout(t *testing.T) {
	t.Parallel()

	// sleep 2 with a 1s timeout. The test takes up to 2s because the child
	// sleep process keeps the pipe open until it exits naturally after /bin/sh
	// is killed.
	out, err := RunBash(context.Background(), BashIn{Command: "sleep 2", Timeout: 1})
	if err != nil {
		t.Fatalf("RunBash(timeout) error = %v", err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("RunBash(timeout) = %q, want timed-out message", out)
	}
}

func TestSetBashDefaultTimeout(t *testing.T) {
	// Not parallel: mutates global state.
	original := bashDefaultTimeout

	SetBashDefaultTimeout(1 * time.Second)
	out, err := RunBash(context.Background(), BashIn{Command: "sleep 2"})
	if err != nil {
		t.Fatalf("RunBash() error = %v", err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("RunBash() with short default = %q, want timed-out message", out)
	}

	SetBashDefaultTimeout(original)
}

func TestSetBashDefaultTimeoutZeroCoerced(t *testing.T) {
	t.Parallel()

	// Zero should coerce to 120s without panicking.
	SetBashDefaultTimeout(0)
	bashDefaultTimeoutMu.RLock()
	got := bashDefaultTimeout
	bashDefaultTimeoutMu.RUnlock()
	if got != 120*time.Second {
		t.Fatalf("SetBashDefaultTimeout(0) left timeout = %v, want 120s", got)
	}
}

func TestRunBashOutputFilterOptIn(t *testing.T) {
	dir := t.TempDir()
	rules := `
name: "printf-head"
version: 1
match:
  command: "printf"
pipeline:
  - action: "head"
    n: 1
on_error: "passthrough"
`
	path := filepath.Join(dir, "printf.yaml")
	if err := os.WriteFile(path, []byte(rules), 0o644); err != nil {
		t.Fatalf("WriteFile(rules) error = %v", err)
	}

	if err := ConfigureBashOutputFilter(BashOutputFilterConfig{}); err != nil {
		t.Fatalf("ConfigureBashOutputFilter(disable) error = %v", err)
	}
	t.Cleanup(func() {
		_ = ConfigureBashOutputFilter(BashOutputFilterConfig{})
	})

	raw, err := RunBash(context.Background(), BashIn{Command: "printf 'a\\nb\\n'"})
	if err != nil {
		t.Fatalf("RunBash(raw) error = %v", err)
	}
	if raw != "a\nb" {
		t.Fatalf("RunBash(raw) = %q, want %q", raw, "a\\nb")
	}

	if err := ConfigureBashOutputFilter(BashOutputFilterConfig{Enabled: true, FiltersDir: dir}); err != nil {
		t.Fatalf("ConfigureBashOutputFilter(enable) error = %v", err)
	}

	filtered, err := RunBash(context.Background(), BashIn{Command: "printf 'a\\nb\\n'"})
	if err != nil {
		t.Fatalf("RunBash(filtered) error = %v", err)
	}
	if filtered != "a\n+1 more lines" {
		t.Fatalf("RunBash(filtered) = %q, want %q", filtered, "a\\n+1 more lines")
	}
}

func TestRunBashOutputFilterInjectsArgs(t *testing.T) {
	dir := t.TempDir()
	rules := `
name: "echo-inject"
version: 1
match:
  command: "echo"
inject:
  args: ["world"]
  skip_if_present: ["world"]
pipeline:
  - action: "head"
    n: 1
on_error: "passthrough"
`
	path := filepath.Join(dir, "echo.yaml")
	if err := os.WriteFile(path, []byte(rules), 0o644); err != nil {
		t.Fatalf("WriteFile(rules) error = %v", err)
	}

	if err := ConfigureBashOutputFilter(BashOutputFilterConfig{Enabled: true, FiltersDir: dir}); err != nil {
		t.Fatalf("ConfigureBashOutputFilter(enable) error = %v", err)
	}
	t.Cleanup(func() {
		_ = ConfigureBashOutputFilter(BashOutputFilterConfig{})
	})

	out, err := RunBash(context.Background(), BashIn{Command: "echo hello"})
	if err != nil {
		t.Fatalf("RunBash() error = %v", err)
	}
	if out != "hello world" {
		t.Fatalf("RunBash() = %q, want %q", out, "hello world")
	}
}
