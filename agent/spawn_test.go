package agent

import "testing"

func TestSpawnRegistryEnqueueDrainOrder(t *testing.T) {
	r := NewSpawnRegistry()
	if ds := r.Drain("s"); ds != nil {
		t.Fatalf("Drain on empty = %+v, want nil", ds)
	}
	r.Enqueue("s", &SpawnDirective{Name: "a", Squad: "coding"})
	r.Enqueue("s", &SpawnDirective{Name: "b", Prompt: "do x"})
	ds := r.Drain("s")
	if len(ds) != 2 {
		t.Fatalf("Drain len = %d, want 2", len(ds))
	}
	if ds[0].Name != "a" || ds[1].Name != "b" {
		t.Fatalf("Drain order = %q,%q, want a,b", ds[0].Name, ds[1].Name)
	}
	// Drain is one-shot: a second Drain sees nothing.
	if again := r.Drain("s"); again != nil {
		t.Fatalf("Drain not one-shot: %+v", again)
	}
}

func TestSpawnRegistryPerSessionIsolation(t *testing.T) {
	r := NewSpawnRegistry()
	r.Enqueue("s1", &SpawnDirective{Name: "one"})
	r.Enqueue("s2", &SpawnDirective{Name: "two"})
	if ds := r.Drain("s1"); len(ds) != 1 || ds[0].Name != "one" {
		t.Fatalf("Drain s1 = %+v, want [one]", ds)
	}
	if ds := r.Drain("s2"); len(ds) != 1 || ds[0].Name != "two" {
		t.Fatalf("Drain s2 = %+v, want [two]", ds)
	}
}

func TestSpawnRegistryCap(t *testing.T) {
	r := NewSpawnRegistry()
	for i := 0; i < maxSpawnsPerSession; i++ {
		if !r.Enqueue("s", &SpawnDirective{Name: "x"}) {
			t.Fatalf("Enqueue #%d rejected below cap", i)
		}
	}
	// One past the cap is rejected (so the tool can tell the leader to stop).
	if r.Enqueue("s", &SpawnDirective{Name: "over"}) {
		t.Fatalf("Enqueue past cap of %d should be rejected", maxSpawnsPerSession)
	}
	if ds := r.Drain("s"); len(ds) != maxSpawnsPerSession {
		t.Fatalf("Drain len = %d, want %d", len(ds), maxSpawnsPerSession)
	}
}

func TestSpawnRegistryNilAndEmptySafe(t *testing.T) {
	var nilReg *SpawnRegistry
	if nilReg.Enqueue("s", &SpawnDirective{}) {
		t.Fatalf("nil Enqueue should return false")
	}
	if ds := nilReg.Drain("s"); ds != nil {
		t.Fatalf("nil Drain = %+v, want nil", ds)
	}
	r := NewSpawnRegistry()
	if r.Enqueue("", &SpawnDirective{}) {
		t.Fatalf("empty-session Enqueue should return false")
	}
	if r.Enqueue("s", nil) {
		t.Fatalf("nil-directive Enqueue should return false")
	}
}
