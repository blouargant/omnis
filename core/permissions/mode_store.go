package permissions

import (
	"log"
	"sync"
	"sync/atomic"
)

const (
	autoMaxConsecutive = 3
	autoMaxTotal       = 20
)

type autoCount struct{ consecutive, total int }

// ModeStore holds per-session permission-mode overrides and the auto-mode
// block counters. A nil *ModeStore is safe: it always reports ModeDefault.
type ModeStore struct {
	mu        sync.RWMutex
	override  map[string]Mode
	auto      map[string]*autoCount
	src       Source
	bypassOff atomic.Bool
	logged    sync.Map
}

func NewModeStore() *ModeStore {
	return &ModeStore{override: map[string]Mode{}, auto: map[string]*autoCount{}}
}

func (s *ModeStore) Set(sid string, m Mode) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.override[sid] = m
	s.mu.Unlock()
}

func (s *ModeStore) Clear(sid string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.override, sid)
	s.mu.Unlock()
}

func (s *ModeStore) Get(sid string) (Mode, bool) {
	if s == nil {
		return ModeDefault, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.override[sid]
	return m, ok
}

// Forget drops everything held for a session.
func (s *ModeStore) Forget(sid string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.override, sid)
	delete(s.auto, sid)
	s.mu.Unlock()
	s.logged.Delete(sid)
}

func (s *ModeStore) SetConfigSource(src Source) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.src = src
	s.mu.Unlock()
}

// ConfigMode is the configured defaultMode (ModeDefault with no source).
func (s *ModeStore) ConfigMode() Mode {
	if s == nil {
		return ModeDefault
	}
	s.mu.RLock()
	src := s.src
	s.mu.RUnlock()
	if src == nil {
		return ModeDefault
	}
	if c := src.Snapshot(); c != nil {
		return c.Permissions.DefaultMode
	}
	return ModeDefault
}

func (s *ModeStore) SetBypassDisabled(v bool) {
	if s != nil {
		s.bypassOff.Store(v)
	}
}

func (s *ModeStore) BypassDisabled() bool { return s != nil && s.bypassOff.Load() }

// Effective is the session's override, else the configured mode. Bypass is
// downgraded to default when disabled by policy (logged once per session).
func (s *ModeStore) Effective(sid string) Mode {
	if s == nil {
		return ModeDefault
	}
	m, ok := s.Get(sid)
	if !ok {
		m = s.ConfigMode()
	}
	if m == ModeBypass && s.bypassOff.Load() {
		if _, seen := s.logged.LoadOrStore(sid, true); !seen {
			log.Printf("permissions: bypassPermissions is disabled by policy; session %s runs in default mode", sid)
		}
		return ModeDefault
	}
	return m
}

func (s *ModeStore) counts(sid string) *autoCount {
	c := s.auto[sid]
	if c == nil {
		c = &autoCount{}
		s.auto[sid] = c
	}
	return c
}

// AutoBlocked records a block in auto mode and reports whether the session
// reached the escalation limit (3 consecutive or 20 total).
func (s *ModeStore) AutoBlocked(sid string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.counts(sid)
	c.consecutive++
	c.total++
	return c.consecutive >= autoMaxConsecutive || c.total >= autoMaxTotal
}

// AutoAllowed records an allowed call: it resets the consecutive count only.
func (s *ModeStore) AutoAllowed(sid string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.counts(sid).consecutive = 0
	s.mu.Unlock()
}

func (s *ModeStore) AutoEscalated(sid string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.auto[sid]
	return c != nil && (c.consecutive >= autoMaxConsecutive || c.total >= autoMaxTotal)
}

func (s *ModeStore) ResetAuto(sid string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.auto, sid)
	s.mu.Unlock()
}
