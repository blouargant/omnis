package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunMultiEditAcrossFiles applies edits to two files in one call and
// confirms both are rewritten (ordered edits, replace_all).
func TestRunMultiEditAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	mustWrite(t, a, "foo one foo\n")
	mustWrite(t, b, "keep\nold\n")

	msg, err := RunMultiEdit(context.Background(), MultiEditIn{Files: []MultiEditFile{
		{Path: a, Edits: []EditOp{{OldString: "foo", NewString: "bar", ReplaceAll: true}}},
		{Path: b, Edits: []EditOp{
			{OldString: "old", NewString: "new"},
			{OldString: "new", NewString: "newer"}, // sees the first edit's result
		}},
	}})
	if err != nil {
		t.Fatalf("RunMultiEdit error = %v", err)
	}
	if !strings.Contains(msg, "4 replacement(s) across 2 file(s)") { // 2 foos + 2 sequential
		t.Errorf("unexpected summary: %q", msg)
	}
	if got := readFile(t, a); got != "bar one bar\n" {
		t.Errorf("a.txt = %q", got)
	}
	if got := readFile(t, b); got != "keep\nnewer\n" {
		t.Errorf("b.txt = %q", got)
	}
}

// TestRunMultiEditAtomicOnFailure verifies that when one file's edit is invalid
// (ambiguous without replace_all), NO file is modified — the batch is atomic.
func TestRunMultiEditAtomicOnFailure(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	b := filepath.Join(dir, "b.txt")
	mustWrite(t, a, "alpha\n")
	mustWrite(t, b, "dup dup\n")

	msg, err := RunMultiEdit(context.Background(), MultiEditIn{Files: []MultiEditFile{
		{Path: a, Edits: []EditOp{{OldString: "alpha", NewString: "ALPHA"}}},
		{Path: b, Edits: []EditOp{{OldString: "dup", NewString: "x"}}}, // appears twice, no replace_all
	}})
	if err != nil {
		t.Fatalf("RunMultiEdit error = %v", err)
	}
	if !strings.Contains(msg, "appears 2 times") {
		t.Errorf("expected ambiguity error, got %q", msg)
	}
	if got := readFile(t, a); got != "alpha\n" {
		t.Errorf("a.txt was modified despite atomic failure: %q", got)
	}
	if got := readFile(t, b); got != "dup dup\n" {
		t.Errorf("b.txt was modified despite atomic failure: %q", got)
	}
}

// TestRunMultiEditValidation covers the guard rails: no files, missing edits,
// duplicate paths, empty old_string, and a not-found old_string.
func TestRunMultiEditValidation(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	mustWrite(t, a, "hello\n")

	cases := []struct {
		name string
		in   MultiEditIn
		want string
	}{
		{"no files", MultiEditIn{}, "no files supplied"},
		{"no edits", MultiEditIn{Files: []MultiEditFile{{Path: a}}}, "has no edits"},
		{"dup path", MultiEditIn{Files: []MultiEditFile{
			{Path: a, Edits: []EditOp{{OldString: "hello", NewString: "hi"}}},
			{Path: a, Edits: []EditOp{{OldString: "x", NewString: "y"}}},
		}}, "appears more than once"},
		{"empty old", MultiEditIn{Files: []MultiEditFile{
			{Path: a, Edits: []EditOp{{OldString: "", NewString: "x"}}},
		}}, "empty old_string"},
		{"not found", MultiEditIn{Files: []MultiEditFile{
			{Path: a, Edits: []EditOp{{OldString: "zzz", NewString: "x"}}},
		}}, "not found"},
	}
	for _, c := range cases {
		msg, err := RunMultiEdit(context.Background(), c.in)
		if err != nil {
			t.Fatalf("%s: err = %v", c.name, err)
		}
		if !strings.Contains(msg, c.want) {
			t.Errorf("%s: got %q, want substring %q", c.name, msg, c.want)
		}
	}
	// a.txt must be untouched by every rejected batch above.
	if got := readFile(t, a); got != "hello\n" {
		t.Errorf("a.txt mutated by a rejected batch: %q", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
