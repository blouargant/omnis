package permissions

import (
	"sync"
	"time"
)

const clearanceTTL = 10 * time.Minute

// Clearances records per-call grants: a PreToolUse hook that allowed a call (or
// whose ask the user approved) clears that one call past the mode-default
// prompt. Grants are keyed by session + call id + tool, consumed once, and
// never persisted or put in the session approval cache.
type Clearances struct {
	mu sync.Mutex
	m  map[string]time.Time
}

// NewClearances returns an empty store.
func NewClearances() *Clearances { return &Clearances{m: map[string]time.Time{}} }

func clearanceKey(sessionID, callID, toolName string) string {
	return sessionID + "\x00" + callID + "\x00" + toolName
}

// Grant clears one call, evicting grants older than 10 minutes. Nil-safe.
func (c *Clearances) Grant(sessionID, callID, toolName string) {
	if c == nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, at := range c.m {
		if now.Sub(at) > clearanceTTL {
			delete(c.m, k)
		}
	}
	c.m[clearanceKey(sessionID, callID, toolName)] = now
}

// Take consumes a grant, reporting whether one existed. Nil-safe.
func (c *Clearances) Take(sessionID, callID, toolName string) bool {
	if c == nil {
		return false
	}
	k := clearanceKey(sessionID, callID, toolName)
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.m[k]
	if !ok {
		return false
	}
	delete(c.m, k)
	return time.Since(at) <= clearanceTTL
}
