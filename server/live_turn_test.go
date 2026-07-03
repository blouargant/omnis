package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestLiveTurnSeqContinuesAcrossTurns verifies that a new turn's sequence numbers
// continue past the previous (still-retained) turn's high-water mark, so a stale
// reconnect cursor from the prior turn can't alias — and silently skip — the new
// turn's first frames.
func TestLiveTurnSeqContinuesAcrossTurns(t *testing.T) {
	r := newLiveTurnRegistry()

	// Turn 1: emit a few frames, then finish (stays retained for tail replay).
	t1 := r.start("sess", func() {})
	t1.emit("token", []byte(`{"t":"a"}`))
	t1.emit("token", []byte(`{"t":"b"}`))
	t1.emit("token", []byte(`{"t":"c"}`))
	lastT1 := t1.currentSeq()
	if lastT1 != 3 {
		t.Fatalf("turn 1 last seq = %d, want 3", lastT1)
	}
	t1.finish()

	// Turn 2 starts while turn 1 is still in the registry: its seqs must continue
	// past turn 1, not reset to 1.
	t2 := r.start("sess", func() {})
	t2.emit("token", []byte(`{"t":"d"}`))
	if got := t2.currentSeq(); got != lastT1+1 {
		t.Fatalf("turn 2 first seq = %d, want %d (continues past turn 1)", got, lastT1+1)
	}

	// A reconnect carrying turn 1's stale cursor (lastT1=3) must NOT skip turn 2's
	// first frame: stream() should replay it, not treat it as already-seen.
	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	t2.finish() // so stream() returns after draining instead of blocking
	t2.stream(ctx, &buf, func() {}, lastT1)
	cancel()
	if !strings.Contains(buf.String(), `{"t":"d"}`) {
		t.Fatalf("turn 2's first frame was skipped by a stale cursor; got:\n%s", buf.String())
	}
}
