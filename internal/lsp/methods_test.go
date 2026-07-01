package lsp

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestQueriesAgainstGopls exercises the M3 name-based query layer end-to-end:
// references to a declared symbol, hover signature, cross-file definition of a
// referenced symbol, and the workspace symbol index — all against real gopls.
func TestQueriesAgainstGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP query test")
	}
	root := findRepoRoot(t)
	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clientFile := filepath.Join(root, "internal", "lsp", "client.go")
	serverFile := filepath.Join(root, "internal", "lsp", "server.go")

	ls, err := m.ResolveServer(ctx, clientFile)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// References to NewClient (declared in client.go) — at least the
	// declaration plus its use in server.go's startServer.
	refs, err := ls.References(ctx, clientFile, "NewClient", true)
	if err != nil {
		t.Fatalf("references: %v", err)
	}
	if len(refs) < 2 {
		t.Errorf("expected >=2 references to NewClient, got %d: %v", len(refs), refs)
	}

	// Hover on the declaration returns its signature.
	hov, err := ls.Hover(ctx, clientFile, "NewClient")
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if !strings.Contains(hov, "NewClient") {
		t.Errorf("hover missing signature, got: %q", hov)
	}
	t.Logf("hover NewClient: %s", strings.ReplaceAll(hov, "\n", " "))

	// Definition of NewClient where it is *referenced* in server.go must point
	// back into client.go (cross-file, textual-occurrence fallback path).
	defs, err := ls.Definition(ctx, serverFile, "NewClient")
	if err != nil {
		t.Fatalf("definition: %v", err)
	}
	if len(defs) == 0 {
		t.Fatal("expected a definition location, got none")
	}
	if !strings.Contains(URIToPath(defs[0].URI), "client.go") {
		t.Errorf("definition should resolve to client.go, got %s", URIToPath(defs[0].URI))
	}

	// Workspace symbol index finds NewClient by name.
	ws, err := ls.WorkspaceSymbols(ctx, "NewClient")
	if err != nil {
		t.Fatalf("workspace symbol: %v", err)
	}
	found := false
	for _, s := range ws {
		if strings.Contains(s.Name, "NewClient") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("workspace symbol NewClient not found in %d results", len(ws))
	}
}

// TestFormatSymbolsIncludesSignature verifies the outline shows the server's
// detail (signature) when present and omits it cleanly when absent.
func TestFormatSymbolsIncludesSignature(t *testing.T) {
	syms := []DocumentSymbol{
		{
			Name:           "(*Client).Initialize",
			Kind:           SymbolKindMethod,
			Detail:         "func(ctx context.Context, rootPath string) (*InitializeResult, error)",
			SelectionRange: Range{Start: Position{Line: 41}},
		},
		{Name: "NoDetail", Kind: SymbolKindFunction, SelectionRange: Range{Start: Position{Line: 9}}},
	}
	out := formatSymbols(syms)
	if !strings.Contains(out, "func(ctx context.Context, rootPath string) (*InitializeResult, error)") {
		t.Errorf("signature missing from outline:\n%s", out)
	}
	if !strings.Contains(out, "(*Client).Initialize") || !strings.Contains(out, "L42") {
		t.Errorf("outline missing name/line:\n%s", out)
	}
}

// TestToolsConstruction verifies the lsp_* tool set builds with the expected
// names and arity (no server contact).
func TestToolsConstruction(t *testing.T) {
	tools := Tools(NewManager(nil))
	got := make(map[string]bool, len(tools))
	for _, tl := range tools {
		got[tl.Name()] = true
	}
	for _, want := range []string{
		"lsp_document_symbols", "lsp_workspace_symbol",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_diagnostics", "lsp_rename",
	} {
		if !got[want] {
			t.Errorf("missing tool %q (have %v)", want, got)
		}
	}
	if len(tools) != 7 {
		t.Errorf("expected 7 tools, got %d", len(tools))
	}
}

// TestLocateWord covers the whole-word matcher and UTF-16 column math without a
// server.
func TestLocateWord(t *testing.T) {
	content := "package p\n\nfunc Foo() {}\nvar FooBar = Foo()\n"
	pos, ok := locateWord(content, "Foo")
	if !ok {
		t.Fatal("expected to locate Foo")
	}
	if pos.Line != 2 || pos.Character != 5 { // "func Foo" → col 5 (0-based)
		t.Errorf("Foo at %+v, want line 2 char 5", pos)
	}
	// Must not match inside the identifier FooBar.
	if _, ok := locateWord("xFooBar = 1", "Foo"); ok {
		t.Error("Foo should not match inside FooBar")
	}
	// UTF-16 column past a non-BMP rune (😀 is one rune, two UTF-16 units).
	pos, ok = locateWord("a := \"😀\"; bar()", "bar")
	if !ok {
		t.Fatal("expected to locate bar after emoji")
	}
	if got := utf16Len("a := \"😀\"; "); pos.Character != got {
		t.Errorf("bar column = %d, want utf16 %d", pos.Character, got)
	}
}
