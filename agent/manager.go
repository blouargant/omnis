// manager.go — coordinates one infrastructure with N agent generations.
// New sessions pin to the current generation; in-flight sessions stay on
// their pinned generation across reloads until they end. Old generations
// are torn down once their pinned-session refcount hits zero.
package agent

import (
	"context"
	"fmt"
	"sync"
)

// Manager owns the shared Infrastructure and the set of agent Instances that
// are currently alive in the process. At any moment exactly one Instance is
// the "current" generation — it receives new sessions. Reload (Phase 3)
// creates a new Instance, promotes it to current, and keeps the previous one
// running for any sessions still pinned to it.
type Manager struct {
	infra *Infrastructure

	mu         sync.RWMutex
	currentGen int
	instances  map[int]*managedInstance
	// sessionGen tracks the generation a session is pinned to. A session
	// without an entry is not yet pinned and will pin to currentGen on its
	// first Lookup / Pin call.
	sessionGen map[string]int
}

// managedInstance wraps an Instance with a refcount of pinned sessions.
type managedInstance struct {
	inst     *Instance
	refcount int
}

// NewManager creates a Manager seeded with first as generation 1 (the
// current generation). Subsequent Reload calls bump the generation.
func NewManager(infra *Infrastructure, first *Instance) *Manager {
	if first == nil {
		return nil
	}
	return &Manager{
		infra:      infra,
		currentGen: first.Generation,
		instances:  map[int]*managedInstance{first.Generation: {inst: first}},
		sessionGen: map[string]int{},
	}
}

// Infra exposes the underlying infrastructure (mailbox backend, registry,
// event bus, ask_user registry) so callers can reach cross-generation state.
func (m *Manager) Infra() *Infrastructure { return m.infra }

// Current returns the Instance for the current generation.
func (m *Manager) Current() *Instance {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if mi := m.instances[m.currentGen]; mi != nil {
		return mi.inst
	}
	return nil
}

// CurrentGeneration returns the current generation number.
func (m *Manager) CurrentGeneration() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentGen
}

// Generations returns a snapshot of (generation → refcount) for all live
// instances. Useful for diagnostics and the web UI status indicator.
func (m *Manager) Generations() map[int]int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[int]int, len(m.instances))
	for gen, mi := range m.instances {
		out[gen] = mi.refcount
	}
	return out
}

// Pin pins sessionID to the current generation and returns the matching
// Instance. Idempotent: a session already pinned keeps its existing pin.
func (m *Manager) Pin(sessionID string) *Instance {
	if sessionID == "" {
		return m.Current()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if gen, ok := m.sessionGen[sessionID]; ok {
		if mi := m.instances[gen]; mi != nil {
			return mi.inst
		}
	}
	mi := m.instances[m.currentGen]
	if mi == nil {
		return nil
	}
	m.sessionGen[sessionID] = m.currentGen
	mi.refcount++
	return mi.inst
}

// PinTo pins sessionID to a specific (already-known) generation. Returns
// false when the generation is no longer alive (the caller should fall back
// to Pin to attach the session to the current generation).
func (m *Manager) PinTo(sessionID string, generation int) (*Instance, bool) {
	if sessionID == "" {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mi, ok := m.instances[generation]
	if !ok {
		return nil, false
	}
	if _, already := m.sessionGen[sessionID]; !already {
		mi.refcount++
	}
	m.sessionGen[sessionID] = generation
	return mi.inst, true
}

// Release decrements the session's pin and tears down draining generations
// that reach refcount zero. The current generation is never torn down.
func (m *Manager) Release(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	gen, ok := m.sessionGen[sessionID]
	if !ok {
		return
	}
	delete(m.sessionGen, sessionID)
	mi := m.instances[gen]
	if mi == nil {
		return
	}
	mi.refcount--
	if mi.refcount <= 0 && gen != m.currentGen {
		delete(m.instances, gen)
		_ = mi.inst.Close()
	}
}

// LookupSquad returns the SquadInstance pinned to sessionID for the given
// squad name. Falls back to the default squad when the named squad does
// not exist in the pinned generation. Returns nil only when the session
// has no live generation at all.
func (m *Manager) LookupSquad(sessionID, squadName string) *SquadInstance {
	inst := m.Lookup(sessionID)
	if inst == nil {
		return nil
	}
	if sq := inst.Squad(squadName); sq != nil {
		return sq
	}
	return inst.Default()
}

// HasSquad reports whether the **current** generation contains a squad with
// the given name. Used by the new-session handler to validate the client's
// squad choice before pinning a session to it.
func (m *Manager) HasSquad(squadName string) bool {
	inst := m.Current()
	if inst == nil {
		return false
	}
	return inst.Squad(squadName) != nil
}

// Lookup returns the Instance pinned to sessionID. If the session is not yet
// pinned it is auto-pinned to the current generation.
func (m *Manager) Lookup(sessionID string) *Instance {
	if sessionID == "" {
		return m.Current()
	}
	m.mu.RLock()
	if gen, ok := m.sessionGen[sessionID]; ok {
		mi := m.instances[gen]
		m.mu.RUnlock()
		if mi != nil {
			return mi.inst
		}
		return nil
	}
	m.mu.RUnlock()
	return m.Pin(sessionID)
}

// PinnedGeneration returns the generation a session is pinned to, or 0 when
// the session is not currently pinned.
func (m *Manager) PinnedGeneration(sessionID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessionGen[sessionID]
}

// Reload builds a new generation using the current runtime config snapshot
// and promotes it to current. In-flight sessions keep their existing pin
// (and the Instance backing that pin stays alive). Returns the new Instance.
//
// On error the current generation is preserved.
func (m *Manager) Reload(ctx context.Context, opts Options) (*Instance, error) {
	m.mu.Lock()
	nextGen := m.currentGen + 1
	m.mu.Unlock()

	inst, err := BuildInstance(ctx, m.infra, opts, nextGen)
	if err != nil {
		return nil, fmt.Errorf("reload: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.instances[nextGen] = &managedInstance{inst: inst}
	oldGen := m.currentGen
	m.currentGen = nextGen
	// Old generation with no pinned sessions can be torn down immediately.
	if oldMI := m.instances[oldGen]; oldMI != nil && oldMI.refcount == 0 {
		delete(m.instances, oldGen)
		_ = oldMI.inst.Close()
	}
	return inst, nil
}

// Close tears down every live generation and the infrastructure. Safe to
// call at most once during process shutdown.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for _, mi := range m.instances {
		if err := mi.inst.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	m.instances = nil
	m.sessionGen = nil
	if err := m.infra.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
