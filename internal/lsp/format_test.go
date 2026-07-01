package lsp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFormatLocationsIncludesSource confirms locations carry the source line,
// with a graceful fallback when the file can't be read.
func TestFormatLocationsIncludesSource(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.go")
	if err := os.WriteFile(f, []byte("package a\n\nfunc Greet() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	locs := []Location{
		{URI: PathToURI(f), Range: Range{Start: Position{Line: 2, Character: 5}}},
		{URI: PathToURI(filepath.Join(dir, "missing.go")), Range: Range{Start: Position{Line: 0}}},
	}
	out := formatLocations(locs, dir)
	if !strings.Contains(out, "a.go:3:6: func Greet() string { return \"hi\" }") {
		t.Errorf("expected source line in output:\n%s", out)
	}
	if !strings.Contains(out, "missing.go:1:1") {
		t.Errorf("expected bare fallback for unreadable file:\n%s", out)
	}
}

// TestFormatWorkspaceSymbolsContainer confirms the enclosing container is shown.
func TestFormatWorkspaceSymbolsContainer(t *testing.T) {
	syms := []SymbolInformation{
		{Name: "Initialize", Kind: SymbolKindMethod, ContainerName: "*Client",
			Location: Location{URI: "file:///x/client.go", Range: Range{Start: Position{Line: 41}}}},
		{Name: "Loose", Kind: SymbolKindFunction,
			Location: Location{URI: "file:///x/other.go", Range: Range{Start: Position{Line: 9}}}},
	}
	out := formatWorkspaceSymbols(syms, "")
	if !strings.Contains(out, "Initialize  (method)") || !strings.Contains(out, "in *Client") {
		t.Errorf("expected container in workspace symbol output:\n%s", out)
	}
	if strings.Contains(out, "Loose") && strings.Contains(out, "Loose  (function)  /x/other.go:10  in") {
		t.Errorf("symbol without container should not show 'in':\n%s", out)
	}
}

// TestDiagCode covers the int-or-string diagnostic code rendering.
func TestDiagCode(t *testing.T) {
	cases := map[string]any{
		"UndeclaredName": "UndeclaredName",
		"1006":           float64(1006),
		"":               nil,
	}
	for want, in := range cases {
		if got := diagCode(in); got != want {
			t.Errorf("diagCode(%v) = %q, want %q", in, got, want)
		}
	}
}
