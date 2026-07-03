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
	// genSeq is a monotonic generation-number allocator. Each Reload reserves a
	// fresh number from it under the lock BEFORE the (slow, unlocked) build, so
	// two concurrent reloads can never pick the same generation — a collision
	// previously made the second reload's teardown delete the generation it had
	// just installed, emptying the map and bricking the UI (squads:null).
	genSeq    int
	instances map[int]*managedInstance
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
		genSeq:     first.Generation,
		instances:  map[int]*managedInstance{first.Generation: {inst: first}},
		sessionGen: map[string]int{},
	}
}

// Infra exposes the underlying infrastructure (mailbox backend, registry,
// event bus, ask_user registry) so callers can reach cross-generation state.
func (m *Manager) Infra() *Infrastructure { return m.infra }

// Current returns the Instance for the current generation.
func (m *Manager) Current() *Instance {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.repairCurrentLocked()
	if mi := m.instances[m.currentGen]; mi != nil {
		return mi.inst
	}
	return nil
}

// repairCurrentLocked guarantees currentGen points at a live instance, promoting
// the highest-numbered live generation when it doesn't. A no-op in the normal
// case. This is defense-in-depth: if any path ever leaves currentGen dangling
// (e.g. a generation torn down out from under it), the whole UI would otherwise
// brick — /api/squads returns null and every new chat fails with "unknown
// squad". Self-healing to the newest live generation keeps the app usable.
// Caller must hold m.mu for writing.
func (m *Manager) repairCurrentLocked() {
	if m.instances[m.currentGen] != nil {
		return
	}
	best := 0
	for gen := range m.instances {
		if gen > best {
			best = gen
		}
	}
	if best != 0 {
		m.currentGen = best
	}
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
	m.repairCurrentLocked()
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

// MigrateToCurrent re-pins sessionID to the current generation when it is
// pinned to an older one and returns the resulting Instance. Safe to call
// at a turn boundary (caller should hold the session's run-guard). The old
// generation's refcount is decremented and the generation torn down if it
// reaches zero. When the session is already pinned to current — or not
// pinned at all — this is a no-op that returns the current Instance.
func (m *Manager) MigrateToCurrent(sessionID string) *Instance {
	if sessionID == "" {
		return m.Current()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.instances[m.currentGen]
	if cur == nil {
		return nil
	}
	oldGen, pinned := m.sessionGen[sessionID]
	if !pinned {
		m.sessionGen[sessionID] = m.currentGen
		cur.refcount++
		return cur.inst
	}
	if oldGen == m.currentGen {
		return cur.inst
	}
	m.sessionGen[sessionID] = m.currentGen
	cur.refcount++
	if oldMI := m.instances[oldGen]; oldMI != nil {
		oldMI.refcount--
		if oldMI.refcount <= 0 && oldGen != m.currentGen {
			delete(m.instances, oldGen)
			_ = oldMI.inst.Close()
		}
	}
	return cur.inst
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
	// Reserve a unique generation number under the lock. Using a monotonic
	// allocator (not currentGen+1, which two concurrent reloads both read as the
	// same value during the unlocked build below) guarantees concurrent reloads
	// get DISTINCT numbers — the fix for the race that emptied the instance map.
	m.mu.Lock()
	m.genSeq++
	nextGen := m.genSeq
	m.mu.Unlock()

	inst, err := BuildInstance(ctx, m.infra, opts, nextGen)
	if err != nil {
		return nil, fmt.Errorf("reload: %w", err)
	}
	// A generation with no squads is never valid — a parseable omnis config always
	// yields at least the synthesised "default" squad. Installing a squad-less
	// instance would brick the UI (/api/squads returns null; new chats fail with
	// "unknown squad"). This can happen if a reload reads config mid-write (e.g.
	// during a settings edit / rollback). Reject it and keep the previous, working
	// generation live rather than swapping to a degenerate one.
	if len(inst.SquadNames()) == 0 {
		_ = inst.Close()
		return nil, fmt.Errorf("reload: rebuilt agent generation has no squads — keeping the previous configuration (check the config files for a transient/empty state)")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Newest-wins guard. Two reloads overlapping (e.g. a manual /api/config/reload
	// racing a settings-tool RequestReload) each reserve a distinct nextGen up
	// front, then build unlocked, then reach here in whatever order they finish.
	// If a HIGHER generation already promoted while we were building, we must NOT
	// demote to this older build — otherwise "last reload wins" degrades to
	// "last-to-FINISH wins" and the newer config is silently dropped (and, with
	// the idle-teardown below, deleted). No session can be pinned to nextGen yet
	// (Reload has not returned it), so discarding our just-built instance is safe.
	if nextGen <= m.currentGen {
		m.repairCurrentLocked()
		if cur := m.instances[m.currentGen]; cur != nil {
			_ = inst.Close()
			return cur.inst, nil
		}
		// Degenerate: the map has no live current generation. Fall through and
		// install ours as the recovery rather than returning nil.
	}

	m.instances[nextGen] = &managedInstance{inst: inst}
	m.currentGen = nextGen
	// Retire every generation BELOW the new current that has no pinned sessions.
	// A full sweep (not just the immediately-previous generation) so out-of-order
	// reload completion can't strand an idle intermediate generation in the map,
	// while a still-pinned older generation (refcount > 0) is kept until its
	// in-flight sessions drain. currentGen itself is excluded, so Current() and
	// every live pin stay valid.
	for gen, mi := range m.instances {
		if gen < m.currentGen && mi != nil && mi.refcount == 0 {
			delete(m.instances, gen)
			_ = mi.inst.Close()
		}
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
