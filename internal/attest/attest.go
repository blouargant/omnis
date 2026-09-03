// Package attest holds short-lived, per-session review verdicts that a hook can
// read: "this exact change was reviewed by a designated reviewer, and here is
// the verdict".
//
// It exists because the obvious design does not work. Letting the reviewer write
// its verdict to a FILE and having the hook read it is forgeable: an agent that
// holds the Write tool — k8s_editor does — can author its own approval. So the
// verdict lives in process memory, unreachable from any file tool, and the tool
// that writes it is mounted on the reviewer alone.
//
// The package is domain-free: it is a "this action requires attestation from
// that reviewer" mechanism, equally able to gate a git push on a code review.
package attest

import (
	"sync"
	"time"
)

// TTL is how long a verdict stays valid. Short, because a cluster moves: a
// review of the world as it was an hour ago is not a review of the world now.
const TTL = 30 * time.Minute

// Verdict is a reviewer's conclusion about one subject.
type Verdict string

const (
	VerdictApproved Verdict = "APPROVED"
	VerdictRejected Verdict = "REJECTED"
)

type record struct {
	Verdict Verdict
	Reasons string
	At      time.Time
}

// Store is a concurrency-safe set of per-session verdicts. Build one with New.
type Store struct {
	mu      sync.Mutex
	records map[string]map[string]record // sessionID -> subject -> record
}

// New returns an empty Store.
func New() *Store {
	return &Store{records: map[string]map[string]record{}}
}

// Record stores a reviewer's verdict about subject, replacing any previous one.
func (s *Store) Record(sid, subject string, v Verdict, reasons string) {
	if s == nil || sid == "" || subject == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[sid] == nil {
		s.records[sid] = map[string]record{}
	}
	s.records[sid][subject] = record{Verdict: v, Reasons: reasons, At: time.Now()}
}

// For returns the session's unexpired verdicts, shaped for the hook input:
// subject -> {verdict, reasons, age_seconds}. A hook reads this from its stdin,
// so no query channel into the process is needed.
func (s *Store) For(sid string) map[string]any {
	out := map[string]any{}
	if s == nil || sid == "" {
		return out
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for subject, r := range s.records[sid] {
		if now.Sub(r.At) > TTL {
			continue
		}
		out[subject] = map[string]any{
			"verdict":     string(r.Verdict),
			"reasons":     r.Reasons,
			"age_seconds": int(now.Sub(r.At).Seconds()),
		}
	}
	return out
}

// Forget drops a session's verdicts. Called on session end.
func (s *Store) Forget(sid string) {
	if s == nil || sid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, sid)
}
