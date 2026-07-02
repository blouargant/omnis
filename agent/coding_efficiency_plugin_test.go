package agent

import (
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/lsp"
)

func TestDominantString(t *testing.T) {
	k, v := dominantString(map[string]any{"content": "hello world", "meta": "x"})
	if k != "content" || v != "hello world" {
		t.Errorf("dominantString picked (%q,%q), want content", k, v)
	}
	if k, _ := dominantString(map[string]any{"n": 3}); k != "" {
		t.Errorf("no string field should give empty key, got %q", k)
	}
}

func TestCapText(t *testing.T) {
	// Fits → no change.
	if _, ok := capText("short", 100, 0.7); ok {
		t.Error("small text should not be capped")
	}
	// Build a big multi-line body.
	var b strings.Builder
	for i := 0; i < 4000; i++ {
		b.WriteString("line of text here\n")
	}
	full := b.String()
	capped, ok := capText(full, 32000, 0.7)
	if !ok {
		t.Fatal("large text should be capped")
	}
	if len(capped) >= len(full) {
		t.Errorf("capped (%d) not smaller than full (%d)", len(capped), len(full))
	}
	if !strings.Contains(capped, "truncated to fit context") {
		t.Errorf("missing truncation marker:\n%s", capped[:200])
	}
	// Head and tail are both preserved (first and last lines survive).
	if !strings.HasPrefix(capped, "line of text here") {
		t.Error("head not preserved")
	}
	if !strings.HasSuffix(strings.TrimRight(capped, "\n"), "line of text here") {
		t.Error("tail not preserved")
	}
}

func TestBudgetForRunTestsTailWeighted(t *testing.T) {
	if b := budgetFor("run_tests"); b.headRatio >= 0.5 {
		t.Errorf("run_tests should be tail-weighted, got headRatio %v", b.headRatio)
	}
	if b := budgetFor("Grep"); b.headRatio != shaperHeadRatio {
		t.Errorf("default headRatio wrong: %v", b.headRatio)
	}
}

func TestShapeResultExemptAndCap(t *testing.T) {
	big := strings.Repeat("x\n", 40000)
	m := map[string]any{"matches": big}
	out, ok := shapeResult("Grep", m)
	if !ok {
		t.Fatal("Grep with huge output should be shaped")
	}
	if len(out["matches"].(string)) >= len(big) {
		t.Error("Grep output not capped")
	}
	// Original map untouched (we copy).
	if len(m["matches"].(string)) != len(big) {
		t.Error("shapeResult mutated the original map")
	}
}

func TestEditedPaths(t *testing.T) {
	cwd := "/proj"
	single := editedPaths("Edit", map[string]any{"file_path": "a/b.go"}, cwd)
	if len(single) != 1 || single[0] != "/proj/a/b.go" {
		t.Errorf("single edit path: %v", single)
	}
	abs := editedPaths("Write", map[string]any{"file_path": "/x/y.go"}, cwd)
	if len(abs) != 1 || abs[0] != "/x/y.go" {
		t.Errorf("absolute path should pass through: %v", abs)
	}
	multi := editedPaths("MultiEdit", map[string]any{
		"files": []any{
			map[string]any{"file_path": "one.go"},
			map[string]any{"file_path": "two.go"},
		},
	}, cwd)
	if len(multi) != 2 || multi[0] != "/proj/one.go" || multi[1] != "/proj/two.go" {
		t.Errorf("multiedit paths: %v", multi)
	}
}

func TestDedup(t *testing.T) {
	ce := newCodingEfficiency(nil)
	sid, cwd := "s1", "/proj"
	args := map[string]any{"file_path": "a.go"}
	res := map[string]any{"content": "   1\tpackage p\n   2\tfunc F(){}\n"}

	// First read → no dedup.
	if _, ok := ce.dedup(sid, cwd, args, res); ok {
		t.Fatal("first read should not be deduped")
	}
	// Identical re-read → stub.
	out, ok := ce.dedup(sid, cwd, args, res)
	if !ok {
		t.Fatal("identical re-read should be deduped")
	}
	if !strings.Contains(out["content"].(string), "unchanged") {
		t.Errorf("stub text: %q", out["content"])
	}
	// Changed content → not deduped (self-invalidates on hash change).
	res2 := map[string]any{"content": "   1\tpackage p\n   2\tfunc G(){}\n"}
	if _, ok := ce.dedup(sid, cwd, args, res2); ok {
		t.Fatal("changed content must not be deduped")
	}
	// After a change, that new content re-read once → stub again.
	if _, ok := ce.dedup(sid, cwd, args, res2); !ok {
		t.Fatal("stable new content should dedup on re-read")
	}
	// Error/empty reads never dedup.
	if _, ok := ce.dedup(sid, cwd, args, map[string]any{"content": "Error reading a.go: no such file"}); ok {
		t.Error("error read must not dedup")
	}
}

func TestDedupClearedOnCompression(t *testing.T) {
	ce := newCodingEfficiency(nil)
	sid, cwd := "s1", "/proj"
	args := map[string]any{"file_path": "a.go"}
	res := map[string]any{"content": "   1\tpackage p\n"}
	ce.dedup(sid, cwd, args, res)                    // seed
	if _, ok := ce.dedup(sid, cwd, args, res); !ok { // would dedup
		t.Fatal("precondition: identical re-read deduped")
	}
	ce.clearSession(sid) // compression event
	if _, ok := ce.dedup(sid, cwd, args, res); ok {
		t.Error("after clearSession the re-read must return full content again")
	}
}

func TestDiagDelta(t *testing.T) {
	ce := newCodingEfficiency(nil)
	sid, path, cwd := "s1", "/proj/a.go", "/proj"

	mkDiag := func(line int, msg string) lsp.Diagnostic {
		var d lsp.Diagnostic
		d.Range.Start.Line = line
		d.Message = msg
		return d
	}

	// First: two errors, no prior → both new.
	note := ce.diagDelta(sid, path, cwd, []lsp.Diagnostic{mkDiag(10, "undefined: x"), mkDiag(20, "unused y")})
	if !strings.Contains(note, "2 new") {
		t.Errorf("first delta: %q", note)
	}
	// Fix one: one resolved, one unchanged.
	note = ce.diagDelta(sid, path, cwd, []lsp.Diagnostic{mkDiag(10, "undefined: x")})
	if !strings.Contains(note, "1 resolved") || !strings.Contains(note, "1 unchanged") {
		t.Errorf("second delta: %q", note)
	}
	// Fix the rest: clean.
	note = ce.diagDelta(sid, path, cwd, nil)
	if !strings.Contains(note, "clean") {
		t.Errorf("clean delta: %q", note)
	}
}

func TestFuseNilManager(t *testing.T) {
	ce := newCodingEfficiency(nil)
	if got := ce.fuse(nil, "s1", "/proj", "Edit", map[string]any{"file_path": "a.go"},
		map[string]any{"result": "edited a.go (1 replacement)"}); got != nil {
		t.Errorf("fuse with nil lsp manager must be a no-op, got %v", got)
	}
}

func TestLooksLikeError(t *testing.T) {
	if !looksLikeError("Error: old_string not found in a.go") {
		t.Error("should detect error result")
	}
	if looksLikeError("edited a.go (1 replacement(s))") {
		t.Error("false positive on success result")
	}
}

func TestDiagnosticsIfRunningNoServer(t *testing.T) {
	// A manager with no running server returns ok=false (so fusion is skipped).
	m := lsp.NewManager(func() *lsp.Config { return &lsp.Config{} })
	if _, ok := m.DiagnosticsIfRunning(nil, "/tmp/x.go", fuseMaxWait, fuseQuiet); ok {
		t.Error("no running server should return ok=false")
	}
}
