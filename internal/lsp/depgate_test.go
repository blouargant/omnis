package lsp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/deps"
)

// TestDepGateBlocksStart verifies the dependency gate is consulted before a
// server with `requires` starts, receives the session id, and that returning an
// error blocks the start (no exec attempt).
func TestDepGateBlocksStart(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{
		"fake": {
			Command:    "omnis-nonexistent-lsp-binary",
			Extensions: []string{".fake"},
			Requires:   []deps.Requirement{{Command: "omnis-nonexistent-lsp-binary"}},
		},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	called := false
	var gotSession string
	SetDepGate(func(_ context.Context, sessionID string, reqs []deps.Requirement) error {
		called = true
		gotSession = sessionID
		if len(reqs) != 1 {
			t.Errorf("gate got %d reqs, want 1", len(reqs))
		}
		return fmt.Errorf("dep unavailable")
	})
	defer SetDepGate(nil)

	ctx := withSession(context.Background(), "sess-123")
	_, err := m.ResolveServer(ctx, "/tmp/x.fake")
	if !called {
		t.Fatal("dependency gate was not called")
	}
	if gotSession != "sess-123" {
		t.Errorf("gate session = %q, want sess-123", gotSession)
	}
	if err == nil || !strings.Contains(err.Error(), "dep unavailable") {
		t.Errorf("expected the gate error to block start, got %v", err)
	}
	// The failed slot must be dropped so a later call can retry.
	if _, err := m.ResolveServer(ctx, "/tmp/x.fake"); err == nil {
		t.Error("expected a retry to consult the gate again, got nil error")
	}
}

// TestDepGateSkippedWithoutRequires confirms a server with no `requires` never
// invokes the gate (it goes straight to start, which then fails on the missing
// binary — a distinct, non-gate error).
func TestDepGateSkippedWithoutRequires(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{
		"fake": {Command: "omnis-nonexistent-lsp-binary", Extensions: []string{".fake"}},
	}}
	m := NewManager(func() *Config { return cfg })
	defer m.Shutdown()

	SetDepGate(func(_ context.Context, _ string, _ []deps.Requirement) error {
		t.Error("gate must not be called when the server declares no requires")
		return nil
	})
	defer SetDepGate(nil)

	if _, err := m.ResolveServer(context.Background(), "/tmp/x.fake"); err == nil {
		t.Error("expected a start error for the missing binary")
	}
}

// TestURIRoundTrip checks PathToURI/URIToPath round-trip for a unix path.
func TestURIRoundTrip(t *testing.T) {
	const p = "/home/user/project/main.go"
	uri := PathToURI(p)
	if uri != "file:///home/user/project/main.go" {
		t.Errorf("PathToURI = %q", uri)
	}
	if got := URIToPath(uri); got != p {
		t.Errorf("URIToPath round-trip = %q, want %q", got, p)
	}
}
