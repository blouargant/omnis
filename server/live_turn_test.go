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
	t1 := r.start("sess", func() {}, "")
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
	t2 := r.start("sess", func() {}, "")
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

// TestLiveTurnFreshConsumerGetsSeededTurnFrames guards the POST /messages
// consumer, which always attaches at from=0 (it starts the turn; it has seen
// nothing).
//
// The seq seeding above makes a new turn's firstSeq continue past the previous
// turn's high-water mark. firstSeq doubles as "the seq of frames[0]", and
// stream() used it to detect a consumer asking for a range that had been TRIMMED
// — but a seeded turn has firstSeq > 1 with nothing trimmed, so `cursor+1 <
// firstSeq` was true for a from=0 attach and the consumer was handed a bare
// "reload" and an otherwise EMPTY stream. Any turn started within the previous
// turn's ~60s retention window therefore streamed nothing at all: in chat the
// question vanished (a mid-turn history re-render has no in-flight turn to show),
// and the session-search agent's report_sessions frame — the only thing its
// result list is built from — never reached the browser, so a search that had in
// fact succeeded rendered as "found nothing".
func TestLiveTurnFreshConsumerGetsSeededTurnFrames(t *testing.T) {
	r := newLiveTurnRegistry()

	t1 := r.start("sess", func() {}, "")
	t1.emit("token", []byte(`{"t":"a"}`))
	t1.finish() // retained ~60s for tail replay, so it still seeds the next turn

	t2 := r.start("sess", func() {}, "")
	t2.emit("tool_call", []byte(`{"name":"report_sessions"}`))
	t2.emit("done", []byte(`{}`))
	t2.finish()

	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t2.stream(ctx, &buf, func() {}, 0) // a fresh consumer: it has seen nothing

	got := buf.String()
	if strings.Contains(got, "event: reload") {
		t.Fatalf("a fresh consumer was told to reload instead of being streamed the turn:\n%s", got)
	}
	if !strings.Contains(got, `"report_sessions"`) || !strings.Contains(got, "event: done") {
		t.Fatalf("the turn's frames never reached the consumer:\n%s", got)
	}
}

// The reload directive still has a real job: when the buffer overflowed and the
// front was trimmed, a consumer whose cursor precedes the retained window cannot
// be replayed without corrupting its transcript, so it must reload from history.
func TestLiveTurnReloadsWhenFramesWereTrimmed(t *testing.T) {
	lt := newLiveTurn(func() {}, 0, "")
	big := bytes.Repeat([]byte("x"), maxBufferBytes/2)
	lt.emit("token", big)
	lt.emit("token", big)
	lt.emit("token", big) // pushes past the cap, trimming frame 1
	lt.finish()

	if lt.firstSeq == 1 {
		t.Fatal("nothing was trimmed; the test no longer exercises the trim path")
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lt.stream(ctx, &buf, func() {}, 0) // cursor precedes the retained window

	if !strings.Contains(buf.String(), "event: reload") {
		t.Fatal("a consumer asking for trimmed frames was replayed a partial (corrupt) stream instead of being told to reload")
	}
}

// A turn is persisted only when it completes, so while it runs the liveTurn's
// prompt is the ONLY record of what the user asked. handleTurnStatus serves it
// to a browser that loaded the page mid-turn, which is what stops the question
// from vanishing on reload. Once the turn finishes the answer IS in history, so
// the still-retained turn (kept ~60s for tail replay) must report inactive —
// otherwise a browser opening the session would sit on a spinner forever for
// work that is already done.
func TestLiveTurnActiveReportsPromptUntilFinished(t *testing.T) {
	r := newLiveTurnRegistry()

	lt := r.start("sess", func() {}, "how many GPUs should I use?")
	active, prompt := lt.active()
	if !active {
		t.Fatal("a just-started turn reports inactive; a reloading browser would show an idle session")
	}
	if prompt != "how many GPUs should I use?" {
		t.Fatalf("prompt = %q, want the question the turn is answering", prompt)
	}

	lt.finish()
	// Still in the registry (retained for tail replay) but no longer running.
	if got := r.get("sess"); got != lt {
		t.Fatal("finished turn was evicted from the registry immediately; the tail-replay window is gone")
	}
	if active, _ := lt.active(); active {
		t.Fatal("a finished turn still reports active; the UI would spin forever on a completed turn")
	}
}
