package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestApplyTextEdits checks descending-order splicing and UTF-16 offset math
// without a server.
func TestApplyTextEdits(t *testing.T) {
	content := "alpha beta gamma\n"
	edits := []TextEdit{
		{Range: Range{Start: Position{0, 0}, End: Position{0, 5}}, NewText: "ALPHA"},
		{Range: Range{Start: Position{0, 11}, End: Position{0, 16}}, NewText: "GAMMA"},
	}
	if got, want := applyTextEdits(content, edits), "ALPHA beta GAMMA\n"; got != want {
		t.Errorf("applyTextEdits = %q, want %q", got, want)
	}

	// An edit on a second line, with a non-BMP rune earlier in the file, must
	// still land on the right bytes.
	content2 := "x := \"😀\"\nfoo := 1\n"
	off, ok := byteOffset(content2, Position{Line: 1, Character: 0})
	if !ok || content2[off:off+3] != "foo" {
		t.Errorf("byteOffset line 1 col 0 wrong: ok=%v off=%d", ok, off)
	}
}

// TestRenameCrossFile renames a symbol defined in one file and used in another,
// against real gopls, and confirms both files are rewritten and the result
// compiles clean.
func TestRenameCrossFile(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP rename test")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("go.mod", "module renametest\n\ngo 1.21\n")
	aFile := write("a.go", "package main\n\nfunc Greet() string { return \"hi\" }\n")
	bFile := write("b.go", "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(Greet()) }\n")

	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ls, err := m.ResolveServer(ctx, aFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	edits, err := ls.Rename(ctx, aFile, "Greet", "Hello")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if len(edits) != 2 {
		t.Fatalf("expected edits across 2 files, got %d: %v", len(edits), edits)
	}

	// Apply each file's edits (mirrors the tool's applyWorkspaceEdit).
	for uri, tes := range edits {
		p := URIToPath(uri)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(applyTextEdits(string(data), tes)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Sync the rewritten files into the server (mirrors the tool's post-apply
	// NotifyChange) so its open-buffer overlay for a.go isn't left stale.
	for uri := range edits {
		m.NotifyChange(URIToPath(uri))
	}

	for _, p := range []string{aFile, bFile} {
		data, _ := os.ReadFile(p)
		s := string(data)
		if strings.Contains(s, "Greet") {
			t.Errorf("%s still contains Greet after rename:\n%s", filepath.Base(p), s)
		}
		if !strings.Contains(s, "Hello") {
			t.Errorf("%s missing Hello after rename:\n%s", filepath.Base(p), s)
		}
	}

	// The renamed project must still type-check clean.
	diags, err := ls.Diagnostics(ctx, bFile, 15*time.Second, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if hasErrorDiag(diags) {
		t.Errorf("expected clean diagnostics after rename, got: %+v", diags)
	}
}
