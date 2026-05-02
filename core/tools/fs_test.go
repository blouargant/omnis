package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestRunReadWriteAndRevert(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.txt")

	msg, err := RunWrite(context.Background(), WriteIn{Path: path, Content: "a\nb\n"})
	if err != nil {
		t.Fatalf("RunWrite() error = %v", err)
	}
	if !strings.Contains(msg, "snapshot saved") {
		t.Fatalf("RunWrite() = %q", msg)
	}

	read, err := RunRead(context.Background(), ReadIn{Path: path, StartLine: 2, EndLine: 2})
	if err != nil {
		t.Fatalf("RunRead() error = %v", err)
	}
	if read != "   2\tb\n" {
		t.Fatalf("RunRead() = %q", read)
	}

	_, _ = RunWrite(context.Background(), WriteIn{Path: path, Content: "changed\n"})
	reverted, err := RunRevert(context.Background(), RevertIn{Path: path})
	if err != nil {
		t.Fatalf("RunRevert() error = %v", err)
	}
	if !strings.Contains(reverted, "restored") {
		t.Fatalf("RunRevert() = %q", reverted)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "a\nb\n" {
		t.Fatalf("file contents after revert = %q", string(data))
	}
}

func TestRunRevertDeletesNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.txt")
	_, _ = RunWrite(context.Background(), WriteIn{Path: path, Content: "new"})
	result, err := RunRevert(context.Background(), RevertIn{Path: path})
	if err != nil {
		t.Fatalf("RunRevert() error = %v", err)
	}
	if !strings.Contains(result, "removed") {
		t.Fatalf("RunRevert() = %q", result)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file still exists after revert: %v", err)
	}
}

func TestRunGrepAndGlob(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.txt")
	pathB := filepath.Join(dir, "b.go")
	if err := os.WriteFile(pathA, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(a) error = %v", err)
	}
	if err := os.WriteFile(pathB, []byte("package demo\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(b) error = %v", err)
	}

	matches, err := RunGrep(context.Background(), GrepIn{Pattern: "hello", Path: dir, Recursive: true})
	if err != nil {
		t.Fatalf("RunGrep() error = %v", err)
	}
	if !strings.Contains(matches, "a.txt:1:hello") {
		t.Fatalf("RunGrep() = %q", matches)
	}

	files, err := RunGlob(context.Background(), GlobIn{Pattern: filepath.Join(dir, "*.txt")})
	if err != nil {
		t.Fatalf("RunGlob() error = %v", err)
	}
	if files != pathA {
		t.Fatalf("RunGlob() = %q, want %q", files, pathA)
	}
}
