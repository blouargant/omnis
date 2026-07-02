package astgrep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequirementPerOS(t *testing.T) {
	req := Requirement()
	if req.Command != "ast-grep" {
		t.Errorf("command = %q, want ast-grep", req.Command)
	}
	if got := req.Install.PerOS["linux"]; got != "pipx install ast-grep-cli" {
		t.Errorf("linux install = %q", got)
	}
	if got := req.Install.PerOS["darwin"]; !strings.Contains(got, "brew") {
		t.Errorf("darwin install = %q", got)
	}
	if req.Install.Empty() {
		t.Error("install command should resolve for the current OS")
	}
}

func TestParseMatches(t *testing.T) {
	// Array form (--json).
	arr := `[{"file":"a.go","text":"foo(1)","range":{"byteOffset":{"start":0,"end":6},"start":{"line":0,"column":0}}}]`
	ms, err := parseMatches([]byte(arr))
	if err != nil || len(ms) != 1 || ms[0].File != "a.go" {
		t.Fatalf("array parse: %v %+v", err, ms)
	}
	// Stream form (--json=stream): one object per line.
	stream := `{"file":"a.go","text":"x"}` + "\n" + `{"file":"b.go","text":"y"}`
	ms, err = parseMatches([]byte(stream))
	if err != nil || len(ms) != 2 || ms[1].File != "b.go" {
		t.Fatalf("stream parse: %v %+v", err, ms)
	}
	// Empty output.
	if ms, err := parseMatches([]byte("  \n")); err != nil || len(ms) != 0 {
		t.Fatalf("empty parse: %v %+v", err, ms)
	}
}

func TestFormatMatches(t *testing.T) {
	if got := formatMatches(nil, "", 0); got != "No structural matches." {
		t.Errorf("empty = %q", got)
	}
	ms := make([]sgMatch, 3)
	for i := range ms {
		ms[i].File = "a.go"
		ms[i].Text = "foo()"
	}
	out := formatMatches(ms, "", 2)
	if !strings.Contains(out, "1 more match") {
		t.Errorf("max cap not applied:\n%s", out)
	}
}

func TestApplyRewritesDescendingSplice(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	// content: "AA BB" — replace AA (0..2) → "X" and BB (3..5) → "Y".
	if err := os.WriteFile(p, []byte("AA BB"), 0o644); err != nil {
		t.Fatal(err)
	}
	matches := []sgMatch{
		{File: "a.go", Text: "AA", Replacement: "X"},
		{File: "a.go", Text: "BB", Replacement: "Y"},
	}
	matches[0].Range.ByteOffset.Start, matches[0].Range.ByteOffset.End = 0, 2
	matches[1].Range.ByteOffset.Start, matches[1].Range.ByteOffset.End = 3, 5

	// Dry run: no write.
	sum := applyRewrites(matches, dir, false)
	if !strings.Contains(sum, "Would apply 2 rewrite(s) across 1 file") {
		t.Errorf("dry-run summary: %q", sum)
	}
	if data, _ := os.ReadFile(p); string(data) != "AA BB" {
		t.Errorf("dry-run must not write, got %q", data)
	}

	// Apply.
	sum = applyRewrites(matches, dir, true)
	if !strings.Contains(sum, "Applied 2 rewrite(s) across 1 file") {
		t.Errorf("apply summary: %q", sum)
	}
	data, _ := os.ReadFile(p)
	if string(data) != "X Y" {
		t.Errorf("splice result = %q, want %q", data, "X Y")
	}
}

func TestApplyRewritesStaleOffsetSkipped(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	if err := os.WriteFile(p, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := sgMatch{File: "a.go", Text: "x", Replacement: "y"}
	m.Range.ByteOffset.Start, m.Range.ByteOffset.End = 0, 9999 // past EOF
	sum := applyRewrites([]sgMatch{m}, dir, true)
	if !strings.Contains(sum, "Skipped") {
		t.Errorf("expected a skipped-offset note: %q", sum)
	}
	if data, _ := os.ReadFile(p); string(data) != "short" {
		t.Errorf("file must be untouched on stale offset, got %q", data)
	}
}

func TestEnsureDepNoGate(t *testing.T) {
	SetDepGate(nil)
	notice := ensureDep(nil)
	if deps_present() {
		if notice != "" {
			t.Errorf("ast-grep present, expected no notice, got %q", notice)
		}
	} else if notice == "" {
		t.Error("ast-grep absent, expected an install notice")
	}
}

// deps_present mirrors the internal PATH check for the test above.
func deps_present() bool {
	_, err := exec.LookPath(binary)
	return err == nil
}

// TestRewriteEndToEnd drives the real ast-grep binary when present.
func TestRewriteEndToEnd(t *testing.T) {
	if _, err := exec.LookPath(binary); err != nil {
		t.Skip("ast-grep not on PATH; skipping end-to-end structural rewrite test")
	}
	SetDepGate(nil)
	dir := t.TempDir()
	p := filepath.Join(dir, "main.go")
	src := "package main\n\nfunc main() {\n\tprintln(foo(1, 2))\n\tprintln(foo(3, 4))\n}\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "--pattern", "foo($A, $B)", "--rewrite", "bar($B, $A)", "--lang", "go", "--json", "."}
	matches, stderr, err := runAstGrep(context.Background(), dir, args)
	if err != nil {
		t.Fatalf("runAstGrep: %v (stderr: %s)", err, stderr)
	}
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matches))
	}
	applyRewrites(matches, dir, true)
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "bar(2, 1)") || !strings.Contains(string(data), "bar(4, 3)") {
		t.Errorf("structural rewrite not applied:\n%s", data)
	}
}
