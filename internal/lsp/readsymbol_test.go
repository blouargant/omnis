package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"os/exec"
)

// TestExpandToDocComment covers the upward doc-comment scan without a server.
func TestExpandToDocComment(t *testing.T) {
	lines := []string{
		"package p",        // 0
		"",                 // 1
		"// Foo does bar.", // 2
		"// It is nice.",   // 3
		"func Foo() {}",    // 4
	}
	if got := expandToDocComment(lines, 4); got != 2 {
		t.Errorf("expandToDocComment = %d, want 2 (start of doc comment)", got)
	}
	// No preceding comment → unchanged.
	if got := expandToDocComment(lines, 0); got != 0 {
		t.Errorf("expandToDocComment at top = %d, want 0", got)
	}
	// Blank line breaks the run.
	lines2 := []string{"# note", "", "def foo():"}
	if got := expandToDocComment(lines2, 2); got != 2 {
		t.Errorf("blank line should break comment run: got %d, want 2", got)
	}
	// Python hash comment is captured.
	lines3 := []string{"# doc", "def foo():"}
	if got := expandToDocComment(lines3, 1); got != 0 {
		t.Errorf("python comment not captured: got %d, want 0", got)
	}
}

// TestPickSymbolFile covers exact > base > first precedence.
func TestPickSymbolFile(t *testing.T) {
	syms := []SymbolInformation{
		{Name: "(*Client).Call", Location: Location{URI: "file:///a.go"}},
		{Name: "Call", Location: Location{URI: "file:///b.go"}},
	}
	if p, ok := pickSymbolFile(syms, "Call"); !ok || !strings.HasSuffix(p, "b.go") {
		t.Errorf("exact-name match should win: %q ok=%v", p, ok)
	}
	only := []SymbolInformation{{Name: "(*Client).Call", Location: Location{URI: "file:///a.go"}}}
	if p, ok := pickSymbolFile(only, "Call"); !ok || !strings.HasSuffix(p, "a.go") {
		t.Errorf("base-name match should resolve: %q ok=%v", p, ok)
	}
	if _, ok := pickSymbolFile(nil, "Call"); ok {
		t.Error("empty hits should return false")
	}
}

// TestSliceSymbol slices a symbol's lines with the doc comment and a header.
func TestSliceSymbol(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	src := "package main\n\n// Foo greets.\nfunc Foo() {\n\tprintln(\"hi\")\n}\n\nfunc Bar() {}\n"
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// Foo's declaration (func line) is line index 3, ends at 5 (0-based).
	rng := Range{Start: Position{Line: 3}, End: Position{Line: 5, Character: 1}}
	out, err := sliceSymbol(p, rng, SymbolKind(12), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "// Foo greets.") {
		t.Errorf("doc comment not included:\n%s", out)
	}
	if !strings.Contains(out, "func Foo() {") || strings.Contains(out, "func Bar()") {
		t.Errorf("wrong slice extent:\n%s", out)
	}
	if !strings.Contains(out, "a.go  L3-6") {
		t.Errorf("header line range wrong (doc comment widens start to L3):\n%s", out)
	}
}

// TestReadSymbolGopls drives SymbolExtent + sliceSymbol against real gopls.
func TestReadSymbolGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP read_symbol test")
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("go.mod", "module rstest\n\ngo 1.21\n")
	src := write("a.go", "package main\n\n// Greet says hi.\nfunc Greet(name string) string {\n\treturn \"hi \" + name\n}\n\nfunc main() { println(Greet(\"x\")) }\n")

	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ls, err := m.ResolveServer(ctx, src)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	sym, ok, err := ls.SymbolExtent(ctx, src, "Greet")
	if err != nil || !ok {
		t.Fatalf("SymbolExtent Greet: ok=%v err=%v", ok, err)
	}
	body, err := sliceSymbol(src, sym.Range, sym.Kind, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "func Greet(name string) string") {
		t.Errorf("body missing Greet signature:\n%s", body)
	}
	if strings.Contains(body, "func main()") {
		t.Errorf("body leaked into main():\n%s", body)
	}
	if !strings.Contains(body, "// Greet says hi.") {
		t.Errorf("doc comment not included:\n%s", body)
	}
}
