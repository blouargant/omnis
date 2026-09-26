package identity

import (
	"sync"
	"time"
)

// TokenStore keeps each login's most recent validated token in process memory
// so turns with no HTTP request behind them (schedules, spawns, mailbox) can
// act as the session owner. Lost on restart by design.
type TokenStore struct {
	Now func() time.Time
	mu  sync.RWMutex
	m   map[string]storedToken
}

type storedToken struct {
	token string
	exp   time.Time // zero = unknown
}

func NewTokenStore() *TokenStore {
	return &TokenStore{Now: time.Now, m: map[string]storedToken{}}
}

// Put records login's latest token. exp may be zero (unknown expiry).
func (s *TokenStore) Put(login, token string, exp time.Time) {
	if login == "" || token == "" {
		return
	}
	s.mu.Lock()
	s.m[login] = storedToken{token: token, exp: exp}
	s.mu.Unlock()
}

// Get returns login's token unless it is known to have expired.
func (s *TokenStore) Get(login string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	t, ok := s.m[login]
	s.mu.RUnlock()
	if !ok || (!t.exp.IsZero() && !s.Now().Before(t.exp)) {
		return "", false
	}
	return t.token, true
}
