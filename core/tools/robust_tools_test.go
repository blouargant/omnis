package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// Parallel calls from one model response are dispatched concurrently by ADK.
// Two Edits of the same file must both land: without a per-file lock each
// read-modify-write overwrites the other and one edit is silently lost.
func TestConcurrentEditsOfOneFileAllLand(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	var b strings.Builder
	const n = 40
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "line-%02d\n", i)
	}
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, _ := RunEdit(context.Background(), EditIn{
				Path:      p,
				OldString: fmt.Sprintf("line-%02d\n", i),
				NewString: fmt.Sprintf("EDITED-%02d\n", i),
			})
			if strings.HasPrefix(out, "Error") {
				t.Errorf("edit %d: %s", i, out)
			}
		}(i)
	}
	wg.Wait()
	got, _ := os.ReadFile(p)
	for i := 0; i < n; i++ {
		if !strings.Contains(string(got), fmt.Sprintf("EDITED-%02d\n", i)) {
			t.Fatalf("edit %d was lost:\n%s", i, got)
		}
	}
}

// A model writes LF line endings; the file on disk uses CRLF. The edit must
// match and keep the file's CRLF endings.
func TestEditMatchesCRLFFileAndKeepsItsEndings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "w.txt")
	os.WriteFile(p, []byte("alpha\r\nbeta\r\ngamma\r\n"), 0o644)
	out, _ := RunEdit(context.Background(), EditIn{Path: p, OldString: "alpha\nbeta", NewString: "one\ntwo"})
	if strings.HasPrefix(out, "Error") {
		t.Fatalf("edit failed: %s", out)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "one\r\ntwo\r\ngamma\r\n" {
		t.Fatalf("got %q", got)
	}
}

// Trailing whitespace and typographic quotes in the file must not defeat a
// match; the text outside the replaced region stays byte-identical.
func TestEditFallsBackToNormalizedMatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "q.go")
	orig := "before  \nsay(“hi”)   \nnext—line\nafter \n"
	os.WriteFile(p, []byte(orig), 0o644)
	out, _ := RunEdit(context.Background(), EditIn{Path: p, OldString: "say(\"hi\")\nnext-line", NewString: "say(\"bye\")\nnext-line"})
	if strings.HasPrefix(out, "Error") {
		t.Fatalf("edit failed: %s", out)
	}
	if !strings.Contains(out, "normaliz") {
		t.Errorf("result should say the match was normalized: %s", out)
	}
	got, _ := os.ReadFile(p)
	want := "before  \nsay(\"bye\")\nnext-line\nafter \n"
	if string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizedMatchMustStillBeUnique(t *testing.T) {
	p := filepath.Join(t.TempDir(), "u.txt")
	orig := "x = “a”\nx = \"a\"\n"
	os.WriteFile(p, []byte(orig), 0o644)
	out, _ := RunEdit(context.Background(), EditIn{Path: p, OldString: `x = "a"` + "\n", NewString: "y\n"})
	// The exact match is unique (the second line), so the exact path wins.
	if strings.HasPrefix(out, "Error") {
		t.Fatalf("exact unique match should apply: %s", out)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "x = “a”\ny\n" {
		t.Fatalf("got %q", got)
	}

	p2 := filepath.Join(t.TempDir(), "u2.txt")
	os.WriteFile(p2, []byte("x = “a” \nx = ‘a’\nx = “a”\n"), 0o644)
	out, _ = RunEdit(context.Background(), EditIn{Path: p2, OldString: `x = "a"`, NewString: "y"})
	if !strings.Contains(out, "appears 2 times") {
		t.Fatalf("an ambiguous normalized match must be refused, got %s", out)
	}
}

func TestEditEmptyOldStringIsRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.txt")
	os.WriteFile(p, []byte("abc"), 0o644)
	out, _ := RunEdit(context.Background(), EditIn{Path: p, OldString: "", NewString: "x"})
	if !strings.Contains(out, "empty old_string") {
		t.Fatalf("got %s", out)
	}
}

func TestMultiEditUsesNormalizedMatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.txt")
	os.WriteFile(p, []byte("a\r\nb\r\nc\r\n"), 0o644)
	out, _ := RunMultiEdit(context.Background(), MultiEditIn{Files: []MultiEditFile{{
		Path:  p,
		Edits: []EditOp{{OldString: "a\nb", NewString: "A\nB"}, {OldString: "c\n", NewString: "C\n"}},
	}}})
	if strings.HasPrefix(out, "Error") {
		t.Fatalf("multiedit failed: %s", out)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "A\r\nB\r\nC\r\n" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateNeverSplitsARune(t *testing.T) {
	s := strings.Repeat("é", MaxToolOutput) // 2 bytes per rune
	if got := truncate(s); !utf8.ValidString(got) {
		t.Fatal("truncate produced invalid UTF-8")
	}
}

// Build and test errors are at the END of a long output: Bash must keep the
// tail and point at a file holding the whole output.
func TestRunBashKeepsTailAndSpillsFullOutput(t *testing.T) {
	out, _ := RunBash(context.Background(), BashIn{Command: "for i in $(seq 1 20000); do echo \"line $i\"; done"})
	if !strings.Contains(out, "line 20000") {
		t.Fatalf("the last line is missing from a truncated output")
	}
	if strings.Contains(out, "line 1\n") {
		t.Fatalf("the head should have been dropped, not the tail")
	}
	m := regexp.MustCompile(`Full output: (\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no full-output path in:\n%s", out[:300])
	}
	full, err := os.ReadFile(m[1])
	if err != nil {
		t.Fatalf("read full output: %v", err)
	}
	t.Cleanup(func() { os.Remove(m[1]) })
	if !strings.HasPrefix(string(full), "line 1\n") || !strings.Contains(string(full), "line 20000") {
		t.Fatal("full output file is incomplete")
	}
	if len(out) > MaxToolOutput+500 {
		t.Fatalf("output not capped: %d bytes", len(out))
	}
}

// A long file read stops on a line boundary and says where to resume.
func TestRunReadStopsOnALineAndSaysWhereToResume(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.txt")
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		fmt.Fprintf(&b, "this is line number %05d of the file\n", i)
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	out, _ := RunRead(context.Background(), ReadIn{Path: p})
	m := regexp.MustCompile(`Showing lines 1-(\d+) of 3000\. Use start_line=(\d+) to continue`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no resume hint at the end of:\n…%s", out[len(out)-300:])
	}
	if fmt.Sprint(mustAtoi(t, m[1])+1) != m[2] {
		t.Fatalf("resume line %s does not follow last shown line %s", m[2], m[1])
	}
	last := fmt.Sprintf("of the file\n\n[Showing")
	if !strings.Contains(out, last) {
		t.Fatalf("output does not end on a whole line")
	}
	if len(out) > readMaxChars+200 {
		t.Fatalf("read not capped: %d", len(out))
	}
	// Resuming from the hint continues where it stopped.
	out2, _ := RunRead(context.Background(), ReadIn{Path: p, StartLine: mustAtoi(t, m[2])})
	if !strings.HasPrefix(strings.TrimLeft(out2, " "), m[2]+"\t") {
		t.Fatalf("resume does not start at line %s: %q", m[2], out2[:60])
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscan(s, &n); err != nil {
		t.Fatal(err)
	}
	return n
}
