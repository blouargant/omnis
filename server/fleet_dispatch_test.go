package main

import (
	"context"
	"testing"

	"github.com/blouargant/omnis/internal/fleet"
	"github.com/blouargant/omnis/internal/sessions"
)

// TestMaterializeSessionFilesUnderCollection verifies that materializeSession
// files a freshly-created session under spawnOptions.Collection, mirroring
// both the in-memory registry (meta.Collection) and the persisted conversation
// file (ConversationFile.Collection).
func TestMaterializeSessionFilesUnderCollection(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	reg := sessions.NewEmptyRegistry()
	d := serverDeps{Registry: reg, rootCtx: context.Background()}

	meta := materializeSession(d, spawnOptions{
		Squad:      "coding",
		Collection: "Service A",
		Title:      "driver: Service A",
	})
	if meta == nil {
		t.Fatal("expected a session")
	}
	if meta.Collection != "Service A" {
		t.Fatalf("session not filed under collection: %q", meta.Collection)
	}

	f, err := sessions.LoadConversationFile(meta.ID)
	if err != nil {
		t.Fatalf("load conversation file: %v", err)
	}
	if f.Collection != "Service A" {
		t.Fatalf("collection not persisted: %q", f.Collection)
	}
}

// TestFleetDriverOptions verifies the pure project→spawnOptions mapping used by
// drainFleetDispatches: an omnis-engine project maps to a Coding-squad Driver
// and a claude-engine project maps to a Claude Worker-squad Driver, both filed
// under their own collection and rooted at their cwd; an unknown project name
// is rejected (ok=false) so the caller skips it rather than mis-dispatching.
func TestFleetDriverOptions(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{
			{Name: "Service A", Cwd: "/repos/a", Engine: fleet.EngineOmnis},
			{Name: "Service B", Cwd: "/repos/b", Engine: fleet.EngineClaude},
		}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })

	opts, ok := fleetDriverOptions("Service A", "alice")
	if !ok {
		t.Fatal("expected ok=true for an omnis-engine project")
	}
	want := spawnOptions{
		Squad:      "coding",
		Title:      "driver: Service A",
		Dir:        "/repos/a",
		Collection: "Service A",
		UserID:     "alice",
	}
	if opts != want {
		t.Fatalf("fleetDriverOptions = %+v, want %+v", opts, want)
	}

	// Case-insensitive project match (mirrors runFleetDispatch's lookup).
	if opts2, ok := fleetDriverOptions("service a", "alice"); !ok || opts2 != want {
		t.Fatalf("case-insensitive lookup: opts=%+v ok=%v", opts2, ok)
	}

	wantClaude := spawnOptions{
		Squad:      "claude worker",
		Title:      "driver: Service B",
		Dir:        "/repos/b",
		Collection: "Service B",
		UserID:     "alice",
	}
	if optsB, ok := fleetDriverOptions("Service B", "alice"); !ok || optsB != wantClaude {
		t.Fatalf("fleetDriverOptions(Service B) = %+v, ok=%v, want %+v", optsB, ok, wantClaude)
	}

	if _, ok := fleetDriverOptions("Unknown", "alice"); ok {
		t.Fatal("expected ok=false for an unknown project")
	}
}
