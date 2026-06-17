package main

import (
	"context"
	"io"
	"strconv"
	"sync"
	"time"
)

// liveTurn buffers one in-flight agent turn's SSE frames so the run can outlive
// the HTTP request that started it. A browser that drops its connection (a
// reverse-proxy idle timeout, a Wi-Fi blip, a closed tab) can reconnect and
// replay the frames it missed instead of losing the turn. The producer (the
// agent run) appends frames via emit(); one or more HTTP consumers stream them
// out via stream(). The run is bound to a background context cancelled only by
// the Stop button (the cancel endpoint) or server shutdown — never by a request
// context — so a disconnect can never abort it.
//
// The buffer is the single source of truth: consumers read frames by sequence
// from the slice and are woken by a closed-channel broadcast. A slow or
// reconnecting consumer therefore never loses a frame — it catches up from the
// buffer rather than relying on a per-consumer queue that could overflow.
type liveTurn struct {
	mu       sync.Mutex
	frames   []bufFrame
	firstSeq int           // seq of frames[0]; advances when the front is trimmed
	seq      int           // last assigned seq (monotonic, 1-based)
	bytes    int           // approximate retained payload bytes (for the cap)
	notify   chan struct{} // closed-to-broadcast wakeup, replaced on each emit
	finished bool          // set by finish(); consumers drain then return
	cancel   context.CancelFunc
}

type bufFrame struct {
	seq   int
	event string
	data  []byte
}

// maxBufferBytes caps the retained replay buffer. A turn rarely approaches this
// (the model's context window bounds total tokens), but a runaway turn trims its
// oldest frames; a later reconnect requesting a trimmed range gets a "reload"
// directive instead of a corrupt partial replay (see stream()).
const maxBufferBytes = 8 << 20 // 8 MiB

func newLiveTurn(cancel context.CancelFunc) *liveTurn {
	return &liveTurn{
		firstSeq: 1,
		notify:   make(chan struct{}),
		cancel:   cancel,
	}
}

// emit appends a frame and wakes every attached consumer. Safe for concurrent
// callers, though in practice only the single producer goroutine calls it.
func (lt *liveTurn) emit(event string, data []byte) {
	lt.mu.Lock()
	lt.seq++
	lt.frames = append(lt.frames, bufFrame{seq: lt.seq, event: event, data: data})
	lt.bytes += len(data) + len(event)
	lt.trimLocked()
	// Broadcast: closing the current notify channel wakes all waiters; a fresh
	// channel takes its place for the next emit.
	close(lt.notify)
	lt.notify = make(chan struct{})
	lt.mu.Unlock()
}

// trimLocked drops the oldest frames while the retained payload exceeds the cap.
// firstSeq advances so stream() can detect a consumer asking for a dropped range.
func (lt *liveTurn) trimLocked() {
	for lt.bytes > maxBufferBytes && len(lt.frames) > 1 {
		f := lt.frames[0]
		lt.frames = lt.frames[1:]
		lt.bytes -= len(f.data) + len(f.event)
		lt.firstSeq = f.seq + 1
	}
}

// finish marks the turn complete and wakes consumers so they drain and return.
func (lt *liveTurn) finish() {
	lt.mu.Lock()
	lt.finished = true
	close(lt.notify)
	lt.notify = make(chan struct{})
	lt.mu.Unlock()
}

// stream writes every frame with seq > from to w until the turn finishes (and
// the buffer is fully drained) or reqCtx is cancelled (the client went away).
// Returning detaches only this consumer; it does NOT affect the run.
func (lt *liveTurn) stream(reqCtx context.Context, w io.Writer, flush func(), from int) {
	cursor := from
	for {
		lt.mu.Lock()
		// The client is resuming after a frame we've already trimmed: we can't
		// replay the gap, so tell it to reload history instead of corrupting the
		// transcript with a partial replay.
		if cursor+1 < lt.firstSeq {
			lt.mu.Unlock()
			writeSSEFrame(w, 0, "reload", []byte("{}"))
			flush()
			return
		}
		var batch []bufFrame
		if n := len(lt.frames); n > 0 {
			start := cursor - lt.firstSeq + 1
			if start < 0 {
				start = 0
			}
			if start < n {
				batch = append(batch, lt.frames[start:]...)
			}
		}
		finished := lt.finished
		notify := lt.notify
		lt.mu.Unlock()

		for _, f := range batch {
			writeSSEFrame(w, f.seq, f.event, f.data)
			cursor = f.seq
		}
		if len(batch) > 0 {
			flush()
		}
		if finished {
			return
		}
		select {
		case <-notify:
		case <-reqCtx.Done():
			return
		}
	}
}

// writeSSEFrame writes one SSE frame, prefixing an "id:" line carrying the
// sequence number so the client can resume from it on reconnect (seq 0 omits it,
// used for control frames like "reload").
func writeSSEFrame(w io.Writer, seq int, event string, data []byte) {
	var b []byte
	if seq > 0 {
		b = append(b, "id: "...)
		b = append(b, strconv.Itoa(seq)...)
		b = append(b, '\n')
	}
	b = append(b, "event: "...)
	b = append(b, event...)
	b = append(b, "\ndata: "...)
	b = append(b, data...)
	b = append(b, '\n', '\n')
	_, _ = w.Write(b)
}

// liveTurnRegistry tracks the current in-flight turn per session.
type liveTurnRegistry struct {
	mu sync.Mutex
	m  map[string]*liveTurn
}

func newLiveTurnRegistry() *liveTurnRegistry {
	return &liveTurnRegistry{m: make(map[string]*liveTurn)}
}

// start registers a fresh live turn for sessionID, replacing any prior one (the
// run-guard guarantees the prior turn has finished before a new one starts).
func (r *liveTurnRegistry) start(sessionID string, cancel context.CancelFunc) *liveTurn {
	lt := newLiveTurn(cancel)
	r.mu.Lock()
	r.m[sessionID] = lt
	r.mu.Unlock()
	return lt
}

// get returns the current live turn for sessionID, or nil.
func (r *liveTurnRegistry) get(sessionID string) *liveTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[sessionID]
}

// release removes lt from the registry after a grace period, but only if it is
// still the registered turn (a newer turn must not be evicted). The grace window
// lets a reconnect arriving just after completion still replay the tail before
// the client falls back to a history reload.
func (r *liveTurnRegistry) release(sessionID string, lt *liveTurn) {
	time.AfterFunc(60*time.Second, func() {
		r.mu.Lock()
		if r.m[sessionID] == lt {
			delete(r.m, sessionID)
		}
		r.mu.Unlock()
	})
}

// cancel aborts the in-flight run for sessionID (the Stop button). Returns false
// when no turn is in flight.
func (r *liveTurnRegistry) cancel(sessionID string) bool {
	lt := r.get(sessionID)
	if lt == nil || lt.cancel == nil {
		return false
	}
	lt.cancel()
	return true
}
