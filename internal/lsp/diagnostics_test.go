package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func hasErrorDiag(diags []Diagnostic) bool {
	for _, d := range diags {
		if d.Severity == SeverityError {
			return true
		}
	}
	return false
}

// TestDiagnosticsErrorThenFix is the M4 feedback loop: introduce a type error,
// confirm gopls reports it, fix the file on disk, and confirm the error clears
// — proving syncDoc (didChange-from-disk) + the publishDiagnostics settle-wait.
func TestDiagnosticsErrorThenFix(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping LSP diagnostics test")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module difftest\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.go")
	bad := "package main\n\nfunc main() {\n\tvar x int = \"oops\"\n\t_ = x\n}\n"
	if err := os.WriteFile(main, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{Servers: map[string]Server{
		"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ls, err := m.ResolveServer(ctx, main)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if ls.Root() != dir {
		t.Errorf("root = %q, want temp module %q", ls.Root(), dir)
	}

	diags, err := ls.Diagnostics(ctx, main, 15*time.Second, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("diagnostics (bad): %v", err)
	}
	if !hasErrorDiag(diags) {
		t.Fatalf("expected a type error, got %d diagnostics: %+v", len(diags), diags)
	}
	t.Logf("error reported: %s", diags[0].Message)

	// Fix the file on disk; the next Diagnostics call re-syncs and should clear.
	good := "package main\n\nfunc main() {\n\tvar x int = 42\n\t_ = x\n}\n"
	if err := os.WriteFile(main, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	diags, err = ls.Diagnostics(ctx, main, 15*time.Second, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("diagnostics (fixed): %v", err)
	}
	if hasErrorDiag(diags) {
		t.Errorf("expected no errors after fix, got: %+v", diags)
	}
}

// TestNotifyChangeNoServer verifies the proactive edit hook is a safe no-op when
// no server is running / the extension is unconfigured.
func TestNotifyChangeNoServer(t *testing.T) {
	m := NewManager(func() *Config {
		return &Config{Servers: map[string]Server{
			"go": {Command: "gopls", Extensions: []string{".go"}, RootMarkers: []string{"go.mod"}},
		}}
	})
	defer m.Shutdown()
	m.NotifyChange("/nonexistent/file.go") // no live server → no panic
	m.NotifyChange("/nonexistent/file.md") // unconfigured extension → no panic
}
