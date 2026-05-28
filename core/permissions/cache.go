package permissions

import "sync"

// sessionApprovalCache stores session-scoped approvals so identical (or,
// for tool grants, same-tool) calls in the same session don't re-prompt.
// Two granularities are kept:
//
//   - m:     per-call grants keyed by (sessionID, probeKey) — "Allow once".
//   - tools: per-tool grants keyed by (sessionID, toolName) — "Allow all
//     <Tool> this session", which silences every later call of that
//     tool regardless of its arguments.
//
// Entries persist for the lifetime of the process; sessions are
// short-lived enough that a periodic cleanup isn't worth the complexity.
// No-op when sessionID is empty (e.g. CLI mode).
type sessionApprovalCache struct {
	mu    sync.RWMutex
	m     map[string]map[string]struct{}
	tools map[string]map[string]struct{}
}

func newSessionApprovalCache() *sessionApprovalCache {
	return &sessionApprovalCache{
		m:     map[string]map[string]struct{}{},
		tools: map[string]map[string]struct{}{},
	}
}

func (c *sessionApprovalCache) has(sessionID, probeKey string) bool {
	if sessionID == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if sm, ok := c.m[sessionID]; ok {
		_, ok = sm[probeKey]
		return ok
	}
	return false
}

func (c *sessionApprovalCache) add(sessionID, probeKey string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sm := c.m[sessionID]
	if sm == nil {
		sm = map[string]struct{}{}
		c.m[sessionID] = sm
	}
	sm[probeKey] = struct{}{}
}

// hasTool reports whether the session holds an "allow all <toolName> this
// session" grant.
func (c *sessionApprovalCache) hasTool(sessionID, toolName string) bool {
	if sessionID == "" {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if tm, ok := c.tools[sessionID]; ok {
		_, ok = tm[toolName]
		return ok
	}
	return false
}

// addTool records a session-scoped grant for every future call of toolName.
func (c *sessionApprovalCache) addTool(sessionID, toolName string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	tm := c.tools[sessionID]
	if tm == nil {
		tm = map[string]struct{}{}
		c.tools[sessionID] = tm
	}
	tm[toolName] = struct{}{}
}

// Forget drops the session's cached approvals (both per-call and per-tool
// grants). Called on session end.
func (c *sessionApprovalCache) Forget(sessionID string) {
	if sessionID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, sessionID)
	delete(c.tools, sessionID)
}
