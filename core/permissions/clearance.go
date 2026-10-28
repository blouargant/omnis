package permissions

import (
	"sync"
	"time"
)

const clearanceTTL = 10 * time.Minute

// now is the clock; tests replace it.
var now = time.Now

// Clearances records per-call grants: a PreToolUse hook that allowed a call (or
// whose ask the user approved) clears that one call past the mode-default
// prompt. Grants are keyed by session + invocation id + call id + tool,
// consumed once, and never persisted or put in the session approval cache. The
// invocation id is part of the key because some backends repeat call ids
// (call_0) and every sub-agent shares the user-facing session id, so
// concurrent instances could otherwise consume each other's grant. Granters
// must use exactly (realSessionID(tc), tc.InvocationID(), tc.FunctionCallID(),
// t.Name()) with the un-normalised tool name.
type Clearances struct {
	mu sync.Mutex
	m  map[string]time.Time
}

// NewClearances returns an empty store.
func NewClearances() *Clearances { return &Clearances{m: map[string]time.Time{}} }

func clearanceKey(sessionID, invocationID, callID, toolName string) string {
	return sessionID + "\x00" + invocationID + "\x00" + callID + "\x00" + toolName
}

// Grant clears one call, evicting grants older than 10 minutes. Nil-safe.
func (c *Clearances) Grant(sessionID, invocationID, callID, toolName string) {
	if c == nil {
		return
	}
	t0 := now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, at := range c.m {
		if t0.Sub(at) > clearanceTTL {
			delete(c.m, k)
		}
	}
	c.m[clearanceKey(sessionID, invocationID, callID, toolName)] = t0
}

// Take consumes a grant, reporting whether one existed. Nil-safe.
func (c *Clearances) Take(sessionID, invocationID, callID, toolName string) bool {
	if c == nil {
		return false
	}
	k := clearanceKey(sessionID, invocationID, callID, toolName)
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.m[k]
	if !ok {
		return false
	}
	delete(c.m, k)
	return now().Sub(at) <= clearanceTTL
}
