package agent

import (
	"testing"
)

// newTestInstance builds a minimal *Instance suitable for Manager tests.
// It does NOT call BuildInstance (which would need an LLM provider); the
// Manager only cares about the Generation field and the closers slice, so
// a hand-constructed value is enough to exercise the refcount logic.
func newTestInstance(gen int) *Instance {
	return &Instance{Generation: gen}
}

func TestManagerPinAndReleaseRefcount(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)

	if got := m.CurrentGeneration(); got != 1 {
		t.Fatalf("CurrentGeneration() = %d, want 1", got)
	}

	// Pinning the same session twice should keep the refcount at 1.
	m.Pin("sess-a")
	m.Pin("sess-a")
	if gens := m.Generations(); gens[1] != 1 {
		t.Fatalf("refcount after duplicate Pin = %d, want 1", gens[1])
	}

	m.Pin("sess-b")
	if gens := m.Generations(); gens[1] != 2 {
		t.Fatalf("refcount after second Pin = %d, want 2", gens[1])
	}

	m.Release("sess-a")
	if gens := m.Generations(); gens[1] != 1 {
		t.Fatalf("refcount after Release sess-a = %d, want 1", gens[1])
	}
	if got := m.PinnedGeneration("sess-a"); got != 0 {
		t.Fatalf("released session still pinned to gen %d", got)
	}

	// Releasing an unknown session is a no-op.
	m.Release("sess-unknown")
	if gens := m.Generations(); gens[1] != 1 {
		t.Fatalf("refcount unexpectedly changed: %d", gens[1])
	}
}

func TestManagerReleaseTearsDownDrainingGeneration(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)
	m.Pin("sess-a")

	// Simulate Reload manually: install a new instance and promote it.
	inst2 := newTestInstance(2)
	m.mu.Lock()
	m.instances[2] = &managedInstance{inst: inst2}
	m.currentGen = 2
	m.mu.Unlock()

	// sess-a is still pinned to gen 1. Gen 1 must stay alive.
	if got := m.PinnedGeneration("sess-a"); got != 1 {
		t.Fatalf("sess-a pin = %d, want 1", got)
	}
	if _, ok := m.instances[1]; !ok {
		t.Fatal("gen 1 unexpectedly torn down while pinned")
	}

	// Releasing sess-a should drop gen 1 entirely (it's not current).
	m.Release("sess-a")
	if _, ok := m.instances[1]; ok {
		t.Fatal("gen 1 still present after last session released")
	}
	if got := m.CurrentGeneration(); got != 2 {
		t.Fatalf("CurrentGeneration() = %d, want 2", got)
	}
}

func TestManagerLookupAutoPinsToCurrent(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)

	got := m.Lookup("new-session")
	if got != inst1 {
		t.Fatal("Lookup returned wrong instance")
	}
	if gen := m.PinnedGeneration("new-session"); gen != 1 {
		t.Fatalf("auto-pin failed: gen = %d, want 1", gen)
	}
}

func TestManagerPinToFailsForRetiredGeneration(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)

	_, ok := m.PinTo("sess-a", 99)
	if ok {
		t.Fatal("PinTo returned ok=true for unknown generation")
	}
}

func TestManagerMigrateToCurrentRebindsAndTearsDownOldGen(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)
	m.Pin("sess-a")

	// Simulate Reload manually: install gen 2 and promote it.
	inst2 := newTestInstance(2)
	m.mu.Lock()
	m.instances[2] = &managedInstance{inst: inst2}
	m.currentGen = 2
	m.mu.Unlock()

	got := m.MigrateToCurrent("sess-a")
	if got != inst2 {
		t.Fatal("MigrateToCurrent returned wrong instance")
	}
	if gen := m.PinnedGeneration("sess-a"); gen != 2 {
		t.Fatalf("pin after migrate = %d, want 2", gen)
	}
	if _, ok := m.instances[1]; ok {
		t.Fatal("gen 1 not torn down after last session migrated")
	}
}

func TestManagerMigrateToCurrentNoOpWhenAlreadyCurrent(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)
	m.Pin("sess-a")

	if got := m.MigrateToCurrent("sess-a"); got != inst1 {
		t.Fatal("MigrateToCurrent returned wrong instance")
	}
	if gens := m.Generations(); gens[1] != 1 {
		t.Fatalf("refcount after no-op migrate = %d, want 1", gens[1])
	}
}

func TestManagerMigrateToCurrentPinsUnpinnedSession(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)

	if got := m.MigrateToCurrent("sess-new"); got != inst1 {
		t.Fatal("MigrateToCurrent returned wrong instance for unpinned session")
	}
	if gen := m.PinnedGeneration("sess-new"); gen != 1 {
		t.Fatalf("auto-pin failed: gen = %d, want 1", gen)
	}
}

func TestManagerReleaseKeepsCurrentGeneration(t *testing.T) {
	inst1 := newTestInstance(1)
	m := NewManager(nil, inst1)

	m.Pin("sess-a")
	m.Release("sess-a")

	// gen 1 is current — it must stay alive even at refcount 0.
	if _, ok := m.instances[1]; !ok {
		t.Fatal("current generation torn down at refcount 0")
	}
}

// newTestInstanceWithSquads is the squad-aware variant of newTestInstance.
// It populates Squads + DefaultName so Manager.LookupSquad and
// Instance.Squad behave as in a real build.
func newTestInstanceWithSquads(gen int, squadNames ...string) *Instance {
	inst := &Instance{Generation: gen, Squads: map[string]*SquadInstance{}, DefaultName: DefaultSquadName}
	if len(squadNames) == 0 {
		squadNames = []string{DefaultSquadName}
	}
	for _, n := range squadNames {
		inst.Squads[n] = &SquadInstance{Name: n}
	}
	return inst
}

func TestManagerLookupSquadFallsBackToDefault(t *testing.T) {
	inst := newTestInstanceWithSquads(1, DefaultSquadName, "research")
	m := NewManager(nil, inst)

	// Named squad resolves directly.
	if sq := m.LookupSquad("sess-a", "research"); sq == nil || sq.Name != "research" {
		t.Fatalf("LookupSquad(research) = %+v, want research", sq)
	}
	// Unknown squad falls back to default.
	if sq := m.LookupSquad("sess-b", "ghost"); sq == nil || sq.Name != DefaultSquadName {
		t.Fatalf("LookupSquad(ghost) = %+v, want default fallback", sq)
	}
	// Empty squad name resolves to default.
	if sq := m.LookupSquad("sess-c", ""); sq == nil || sq.Name != DefaultSquadName {
		t.Fatalf("LookupSquad(empty) = %+v, want default", sq)
	}
}

func TestManagerHasSquad(t *testing.T) {
	inst := newTestInstanceWithSquads(1, DefaultSquadName, "research")
	m := NewManager(nil, inst)

	if !m.HasSquad("research") {
		t.Fatal("HasSquad(research) = false, want true")
	}
	if !m.HasSquad("RESEARCH") {
		t.Fatal("HasSquad case-insensitive failed")
	}
	if m.HasSquad("ghost") {
		t.Fatal("HasSquad(ghost) = true, want false")
	}
}

func TestManagerCloseRunsInstanceClosers(t *testing.T) {
	closed := 0
	inst := newTestInstance(1)
	inst.closers = append(inst.closers, func() error {
		closed++
		return nil
	})
	m := NewManager(nil, inst)
	if err := m.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if closed != 1 {
		t.Fatalf("closer ran %d times, want 1", closed)
	}
}
