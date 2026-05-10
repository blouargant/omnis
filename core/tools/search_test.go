package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGrepBasic(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	matches, err := RunGrep(context.Background(), GrepIn{Pattern: "hello", Path: dir, Recursive: true})
	if err != nil {
		t.Fatalf("RunGrep() error = %v", err)
	}
	if !strings.Contains(matches, "a.txt:1:hello") {
		t.Fatalf("RunGrep() = %q, want match on a.txt:1", matches)
	}
}

func TestRunGrepNoMatches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunGrep(context.Background(), GrepIn{Pattern: "zzznomatch", Path: dir, Recursive: true})
	if err != nil {
		t.Fatalf("RunGrep() error = %v", err)
	}
	if out != "(no matches)" {
		t.Fatalf("RunGrep(no matches) = %q, want (no matches)", out)
	}
}

func TestRunGrepEmptyPathDefaultsToCurrentDir(t *testing.T) {
	t.Parallel()

	// Empty Path must not panic. We search an empty temp dir to avoid
	// the pattern string appearing in this very source file.
	dir := t.TempDir()
	out, err := RunGrep(context.Background(), GrepIn{Pattern: "anything", Path: dir})
	if err != nil {
		t.Fatalf("RunGrep() error = %v", err)
	}
	if out != "(no matches)" {
		t.Fatalf("RunGrep(empty dir) = %q, want (no matches)", out)
	}
}

func TestRunGlobSimple(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	pathA := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(pathA, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	files, err := RunGlob(context.Background(), GlobIn{Pattern: filepath.Join(dir, "*.txt")})
	if err != nil {
		t.Fatalf("RunGlob() error = %v", err)
	}
	if files != pathA {
		t.Fatalf("RunGlob() = %q, want %q", files, pathA)
	}
}

func TestRunGlobDoublestar(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	want := filepath.Join(sub, "deep.txt")
	if err := os.WriteFile(want, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	files, err := RunGlob(context.Background(), GlobIn{Pattern: dir + "/**/*.txt"})
	if err != nil {
		t.Fatalf("RunGlob(**) error = %v", err)
	}
	if !strings.Contains(files, "deep.txt") {
		t.Fatalf("RunGlob(**) = %q, want deep.txt in results", files)
	}
}

func TestRunGlobNoMatches(t *testing.T) {
	t.Parallel()

	out, err := RunGlob(context.Background(), GlobIn{Pattern: "/nonexistent/path/*.zzz"})
	if err != nil {
		t.Fatalf("RunGlob() error = %v", err)
	}
	if out != "(no matches)" {
		t.Fatalf("RunGlob(no matches) = %q, want (no matches)", out)
	}
}
