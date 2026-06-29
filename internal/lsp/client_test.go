package lsp

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// findRepoRoot walks up from the test's working directory to the module root
// (the directory containing go.mod).
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

// TestClientHandshakeAndDocumentSymbols is the M1 smoke test: it spawns a real
// gopls, completes the LSP handshake against this repo, opens a known Go file,
// and asserts documentSymbol returns the symbols we expect. It is skipped when
// gopls is not installed so `make test` stays green on machines without it.
func TestClientHandshakeAndDocumentSymbols(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP smoke test")
	}

	root := findRepoRoot(t)
	target := filepath.Join(root, "internal", "lsp", "client.go")
	src, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "gopls")
	cmd.Dir = root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start gopls: %v", err)
	}
	go func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			t.Logf("gopls stderr: %s", s.Text())
		}
	}()
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	client := NewClient(stdout, stdin)
	client.Start()
	defer client.Close()

	initRes, err := client.Initialize(ctx, root)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if initRes.ServerInfo != nil {
		t.Logf("connected to %s %s", initRes.ServerInfo.Name, initRes.ServerInfo.Version)
	}

	uri := PathToURI(target)
	if err := client.DidOpen(uri, "go", string(src)); err != nil {
		t.Fatalf("didOpen: %v", err)
	}

	syms, err := client.DocumentSymbols(ctx, uri)
	if err != nil {
		t.Fatalf("documentSymbol: %v", err)
	}
	if len(syms) == 0 {
		t.Fatal("expected document symbols, got none")
	}

	for _, want := range []string{"NewClient", "Client", "PathToURI"} {
		if !containsSymbol(syms, want) {
			t.Errorf("expected symbol %q in %v", want, symbolNames(syms))
		}
	}
	t.Logf("found %d top-level symbols: %v", len(syms), symbolNames(syms))
}

func containsSymbol(syms []DocumentSymbol, name string) bool {
	for _, s := range syms {
		if s.Name == name {
			return true
		}
		if containsSymbol(s.Children, name) {
			return true
		}
	}
	return false
}

func symbolNames(syms []DocumentSymbol) []string {
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		out = append(out, s.Name)
	}
	return out
}
