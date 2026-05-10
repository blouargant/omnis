package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestRunReadFullFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "full.txt")
	if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunRead(context.Background(), ReadIn{Path: path})
	if err != nil {
		t.Fatalf("RunRead() error = %v", err)
	}
	if !strings.Contains(out, "line1") || !strings.Contains(out, "line3") {
		t.Fatalf("RunRead() missing lines: %q", out)
	}
	// All three lines must be numbered.
	if !strings.Contains(out, "   1\t") || !strings.Contains(out, "   3\t") {
		t.Fatalf("RunRead() line numbers wrong: %q", out)
	}
}

func TestRunReadNonexistent(t *testing.T) {
	t.Parallel()

	out, err := RunRead(context.Background(), ReadIn{Path: "/nonexistent/path/file.txt"})
	if err != nil {
		t.Fatalf("RunRead() unexpected error = %v", err)
	}
	if !strings.Contains(out, "Error reading") {
		t.Fatalf("RunRead(nonexistent) = %q, want error message", out)
	}
}

func TestRunReadEmptyRange(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "range.txt")
	if err := os.WriteFile(path, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// start > end → empty range
	out, err := RunRead(context.Background(), ReadIn{Path: path, StartLine: 5, EndLine: 2})
	if err != nil {
		t.Fatalf("RunRead() error = %v", err)
	}
	if out != "(empty range)" {
		t.Fatalf("RunRead(start>end) = %q, want (empty range)", out)
	}
}

func TestRunWriteCreatesParentDirs(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "deep", "nested", "file.txt")
	_, err := RunWrite(context.Background(), WriteIn{Path: path, Content: "hello"})
	if err != nil {
		t.Fatalf("RunWrite() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("file content = %q, want hello", string(data))
	}
}

func TestRunRevertNoSnapshot(t *testing.T) {
	t.Parallel()

	out, err := RunRevert(context.Background(), RevertIn{Path: "/no/snapshot/here.txt"})
	if err != nil {
		t.Fatalf("RunRevert() error = %v", err)
	}
	if !strings.Contains(out, "no snapshot") {
		t.Fatalf("RunRevert(no snapshot) = %q, want error message", out)
	}
}
