package collectionctx

import "testing"

func TestPrevMemoryRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if HasPrevMemory("Acme") {
		t.Fatal("expected no prev initially")
	}
	if err := WritePrevMemory("Acme", "old facts"); err != nil {
		t.Fatal(err)
	}
	if !HasPrevMemory("Acme") {
		t.Fatal("expected prev after write")
	}
	if got := ReadPrevMemory("Acme"); got != "old facts" {
		t.Fatalf("ReadPrevMemory = %q", got)
	}
	if err := RemovePrevMemory("Acme"); err != nil {
		t.Fatal(err)
	}
	if HasPrevMemory("Acme") {
		t.Fatal("expected prev removed")
	}
	// Removing a missing snapshot is a no-op.
	if err := RemovePrevMemory("Acme"); err != nil {
		t.Fatalf("RemovePrevMemory on missing: %v", err)
	}
}
