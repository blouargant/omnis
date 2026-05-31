//go:build !windows

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBashInteractiveOutputAndCwd(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "child")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Plain command: output captured, cwd unchanged, sentinel stripped.
	out, cwd, _ := RunBashInteractive(context.Background(), "echo hello", dir, 5)
	if out != "hello" {
		t.Fatalf("output = %q, want %q", out, "hello")
	}
	if cwd != dir {
		t.Fatalf("cwd = %q, want %q", cwd, dir)
	}
	if strings.Contains(out, cwdSentinel) {
		t.Fatalf("sentinel leaked into output: %q", out)
	}

	// Embedded cd: resulting cwd reflects the change.
	out, cwd, _ = RunBashInteractive(context.Background(), "cd child && pwd", dir, 5)
	if !strings.HasSuffix(cwd, "child") {
		t.Fatalf("cwd = %q, want suffix child", cwd)
	}
	if !strings.Contains(out, "child") {
		t.Fatalf("output = %q, want it to contain child", out)
	}
}

func TestRunBashInteractiveSafetyFloor(t *testing.T) {
	out, _, _ := RunBashInteractive(context.Background(), "rm -rf /", "", 5)
	if !strings.Contains(out, "safety floor") {
		t.Fatalf("expected safety-floor block, got %q", out)
	}
}
