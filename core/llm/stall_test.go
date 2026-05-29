package llm

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// stallReader emits `prefix` once, then blocks forever (simulating an upstream
// that streams partial data then goes silent without closing the connection).
// It unblocks only when ctx is cancelled.
type stallReader struct {
	ctx    context.Context
	prefix []byte
	sent   bool
}

func (s *stallReader) Read(p []byte) (int, error) {
	if !s.sent && len(s.prefix) > 0 {
		s.sent = true
		n := copy(p, s.prefix)
		return n, nil
	}
	<-s.ctx.Done() // block until the guard cancels the request
	return 0, s.ctx.Err()
}

func TestStallGuardAbortsFrozenStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &stallReader{ctx: ctx, prefix: []byte("data: {\"choices\":[]}\n")}
	g := newStallGuard(ctx, r, cancel, 100*time.Millisecond)

	start := time.Now()
	buf := make([]byte, 64)
	// First read returns the prefix.
	if n, err := g.Read(buf); err != nil || n == 0 {
		t.Fatalf("first read = (%d,%v), want data with no error", n, err)
	}
	// Second read blocks; the watchdog should fire ~100ms later and abort it.
	_, err := g.Read(buf)
	elapsed := time.Since(start)
	if !errors.Is(err, errStreamStalled) {
		t.Fatalf("err = %v, want errStreamStalled", err)
	}
	if !g.Stalled() {
		t.Fatalf("Stalled() = false, want true")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("stall took %s, expected abort near the 100ms timeout", elapsed)
	}
}

func TestStallGuardDisabledPassesThrough(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &stallReader{ctx: ctx, prefix: []byte("hello")}
	g := newStallGuard(ctx, r, cancel, 0) // disabled

	buf := make([]byte, 16)
	n, err := g.Read(buf)
	if err != nil || string(buf[:n]) != "hello" {
		t.Fatalf("read = (%q,%v), want hello/nil", string(buf[:n]), err)
	}
	if g.Stalled() {
		t.Fatalf("Stalled() = true on a disabled guard")
	}
	_ = io.EOF
}
