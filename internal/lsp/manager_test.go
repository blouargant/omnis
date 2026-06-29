package lsp

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestManagerLazyStartAndReuse exercises M2 end-to-end against a real gopls:
// extension routing, root-marker detection, lazy start, pooled reuse for two
// files in the same module, and ErrNoServer for an unconfigured extension.
// findRepoRoot / containsSymbol / symbolNames are defined in client_test.go.
func TestManagerLazyStartAndReuse(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP manager test")
	}
	root := findRepoRoot(t)
	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	target := filepath.Join(root, "internal", "lsp", "client.go")
	ls1, err := m.ResolveServer(ctx, target)
	if err != nil {
		t.Fatalf("resolve client.go: %v", err)
	}
	if ls1.Root() != root {
		t.Errorf("detected root = %q, want module root %q", ls1.Root(), root)
	}
	if ls1.Lang() != "go" {
		t.Errorf("languageID = %q, want go", ls1.Lang())
	}

	syms, err := ls1.DocumentSymbols(ctx, target)
	if err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	if !containsSymbol(syms, "NewClient") {
		t.Errorf("expected NewClient in %v", symbolNames(syms))
	}

	// A sibling file in the same module must reuse the pooled server.
	other := filepath.Join(root, "internal", "lsp", "protocol.go")
	ls2, err := m.ResolveServer(ctx, other)
	if err != nil {
		t.Fatalf("resolve protocol.go: %v", err)
	}
	if ls1 != ls2 {
		t.Error("expected pooled server reuse for same (root, language)")
	}
	if syms2, err := ls2.DocumentSymbols(ctx, other); err != nil {
		t.Fatalf("documentSymbol protocol.go: %v", err)
	} else if !containsSymbol(syms2, "DocumentSymbol") {
		t.Errorf("expected DocumentSymbol in %v", symbolNames(syms2))
	}

	// An unconfigured extension resolves to ErrNoServer (clean fallback signal).
	if _, err := m.ResolveServer(ctx, filepath.Join(root, "README.md")); !errors.Is(err, ErrNoServer) {
		t.Errorf("expected ErrNoServer for .md, got %v", err)
	}
}

// TestDetectRoot covers the marker walk-up and the no-marker fallback without
// needing a language server.
func TestDetectRoot(t *testing.T) {
	root := findRepoRoot(t)
	start := filepath.Join(root, "internal", "lsp")

	if got := DetectRoot(start, []string{"go.mod"}); got != root {
		t.Errorf("DetectRoot(go.mod) = %q, want %q", got, root)
	}
	// No marker anywhere → falls back to the starting directory.
	if got := DetectRoot(start, []string{"this-marker-does-not-exist"}); got != start {
		t.Errorf("DetectRoot(missing) = %q, want fallback %q", got, start)
	}
	// Empty markers → starting directory.
	if got := DetectRoot(start, nil); got != start {
		t.Errorf("DetectRoot(nil) = %q, want %q", got, start)
	}
}

// TestServerForFile covers extension routing in isolation.
func TestServerForFile(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
		"ts": {Command: "typescript-language-server", Args: []string{"--stdio"},
			Extensions: []string{".ts", ".tsx"}, LanguageID: "typescript"},
	}}
	if s, ok := cfg.ServerForFile("/x/main.go"); !ok || s.Name != "go" {
		t.Errorf("ServerForFile(.go) = %q,%v; want go,true", s.Name, ok)
	}
	if s, ok := cfg.ServerForFile("/x/app.TSX"); !ok || s.Name != "ts" || s.langID() != "typescript" {
		t.Errorf("ServerForFile(.TSX) = %q (lang %q),%v; want ts/typescript,true", s.Name, s.langID(), ok)
	}
	if _, ok := cfg.ServerForFile("/x/readme.md"); ok {
		t.Error("ServerForFile(.md) matched unexpectedly")
	}
}
