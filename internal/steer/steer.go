// Package steer implements mid-turn "steering": additional information, remarks
// or insights a user types while a turn is still being computed (the same
// affordance Claude Code offers). Notes are queued per session and delivered
// in one of two ways:
//
//   - injected into the *running* turn at the next model call, via the
//     BeforeModelCallback steering plugin (agent/steer_plugin.go), so the agent
//     can adapt mid-work; or
//   - when the turn finishes before a model boundary consumes them, run as the
//     next turn (the surface's post-turn fallback drain).
//
// The store is process-wide (held on agent.Infrastructure) so it survives a
// hot-reload, exactly like the background-task queues. It is purely in-memory:
// steering notes are transient per turn and never persisted on their own (a
// consumed note is folded into its turn's persisted prompt by the surface).
package steer

import (
	"strings"
	"sync"
)

// Store holds per-session steering notes. A note moves pending → consumed when
// the model is shown it (Drain); consumed notes are kept only so the surface can
// fold them into the turn's persisted prompt (TakeConsumed). Notes that are
// never consumed stay in pending and are taken for the next-turn fallback
// (TakePending).
type Store struct {
	mu sync.Mutex
	m  map[string]*entry
}

type entry struct {
	pending  []string
	consumed []string
}

// New returns an empty steering store.
func New() *Store { return &Store{m: make(map[string]*entry)} }

func (s *Store) getLocked(sid string) *entry {
	e := s.m[sid]
	if e == nil {
		e = &entry{}
		s.m[sid] = e
	}
	return e
}

// Enqueue appends a note to the pending queue for sid. Blank notes and an empty
// session id are ignored.
func (s *Store) Enqueue(sid, text string) {
	text = strings.TrimSpace(text)
	if sid == "" || text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.getLocked(sid)
	e.pending = append(e.pending, text)
}

// Drain atomically moves every pending note to the consumed list and returns
// the moved notes. Called by the BeforeModelCallback at each model boundary, so
// a note is delivered to the model exactly once.
func (s *Store) Drain(sid string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[sid]
	if e == nil || len(e.pending) == 0 {
		return nil
	}
	out := e.pending
	e.consumed = append(e.consumed, out...)
	e.pending = nil
	return out
}

// TakeConsumed returns and clears the notes consumed since the last call. The
// surface calls this at the end of each turn to fold them into that turn's
// persisted prompt (so a reload shows what the user added mid-turn).
func (s *Store) TakeConsumed(sid string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[sid]
	if e == nil || len(e.consumed) == 0 {
		return nil
	}
	out := e.consumed
	e.consumed = nil
	return out
}

// TakePending returns and clears notes that were enqueued but never shown to the
// model (they arrived after the turn's last model call). The surface runs these
// as the next turn.
func (s *Store) TakePending(sid string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[sid]
	if e == nil || len(e.pending) == 0 {
		return nil
	}
	out := e.pending
	e.pending = nil
	return out
}

// PendingLen reports how many notes are queued (not yet shown to the model).
func (s *Store) PendingLen(sid string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.m[sid]
	if e == nil {
		return 0
	}
	return len(e.pending)
}

// Forget drops all steering state for sid (call on session deletion).
func (s *Store) Forget(sid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, sid)
}
