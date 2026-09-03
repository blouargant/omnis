// Package hookstate holds the per-session state the hooks engine exposes to
// hook commands: how many times a tool call has been attempted with identical
// arguments, and how many calls of that tool were refused back-to-back.
//
// It is deliberately domain-free. The engine only *reports* these numbers to a
// hook script; the script decides what to do with them, which is what keeps the
// mechanism generic rather than a Kubernetes feature in disguise.
//
// The store is process-wide (held on agent.Infrastructure beside SteerStore /
// GoalStore / Budget, so it survives a hot-reload). That matters: the hook
// callbacks are built independently per sub-agent, so without a shared store
// k8s_editor and its leader would count attempts on the same command
// separately and a delegation bounce would silently reset the counter.
package hookstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
)

// Store is a concurrency-safe counter set. The zero value is not usable; build
// one with New.
type Store struct {
	mu    sync.Mutex
	exact map[string]int // sid \x00 tool \x00 argsHash -> attempts
	cons  map[string]int // sid \x00 tool            -> consecutive blocked calls
}

// New returns an empty Store.
func New() *Store {
	return &Store{exact: map[string]int{}, cons: map[string]int{}}
}

// Attempt records one attempt of (sid, tool, args) and returns the attempt
// number — 1 on the first — together with the consecutive-blocked count
// accumulated by PREVIOUS calls of that tool.
//
// Degrade contract: with no store or no session id it reports (1, 0) on every
// call, so a hook always sees a brand-new attempt and never escalates. That
// degrades the ESCALATION, not the blocking: these numbers only ever choose
// between refusing and asking the user — never between refusing and allowing —
// so an unwired store means "refuse indefinitely", not "let it through". The
// opposite choice (reporting a huge count when unwired) was rejected: it would
// spam the user with a card on every tool call. In practice the store is built
// unconditionally on Infrastructure, so nil occurs only in tests and examples.
//
// The split in update timing is deliberate and cannot be collapsed: an attempt
// on identical arguments is knowable before the hook runs, whereas a block is
// only knowable after, so the consecutive count is advanced by RecordOutcome.
func (s *Store) Attempt(sid, tool string, args map[string]any) (attempt, consecutive int) {
	if s == nil || sid == "" {
		return 1, 0
	}
	k := key(sid, tool, HashArgs(args))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.exact[k]++
	return s.exact[k], s.cons[key(sid, tool, "")]
}

// RecordOutcome advances the consecutive-blocked counter once the verdict is
// known: a block increments it, anything else resets it.
func (s *Store) RecordOutcome(sid, tool string, blocked bool) {
	if s == nil || sid == "" {
		return
	}
	k := key(sid, tool, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	if blocked {
		s.cons[k]++
		return
	}
	delete(s.cons, k)
}

// Forget drops every counter for a session. Called on session end.
func (s *Store) Forget(sid string) {
	if s == nil || sid == "" {
		return
	}
	prefix := sid + "\x00"
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.exact {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(s.exact, k)
		}
	}
	for k := range s.cons {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(s.cons, k)
		}
	}
}

// HashArgs is the canonical hash of a tool call's arguments. It is the attempt
// key and, in internal/attest, the attestation subject; the Python hook script
// computes the same value independently, so the two encodings must agree exactly.
// encoding/json sorts object keys, so the hash does not depend on map iteration
// order.
//
// SetEscapeHTML(false) is load-bearing, not a style choice. Go escapes &, < and >
// in strings by default, while the script's
// json.dumps(..., sort_keys=True, separators=(",",":")) does not — and EVERY
// compound shell command contains "&&". With the default escaping the two sides
// would disagree on precisely the commands that matter most, no attestation would
// ever match, and every compound command would be refused as "not reviewed" —
// a failure that looks intermittent rather than systematic.
func HashArgs(args map[string]any) string {
	if args == nil {
		// A nil map encodes as JSON `null`, but the Python hook script always
		// receives an object — `{}` for a call with no arguments — so the two
		// sides would hash differently for exactly that case. Normalise.
		args = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(args); err != nil {
		// Unencodable args cannot be identified at all, so they all share one
		// bucket. Practically unreachable: tool arguments arrive as decoded JSON.
		return ""
	}
	// Encode appends a newline; the Python side produces none.
	sum := sha256.Sum256(bytes.TrimRight(buf.Bytes(), "\n"))
	return hex.EncodeToString(sum[:])
}

func key(sid, tool, hash string) string {
	return sid + "\x00" + tool + "\x00" + hash
}
