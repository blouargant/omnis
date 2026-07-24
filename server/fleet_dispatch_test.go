package main

import (
	"context"
	"testing"

	toolkitagent "github.com/blouargant/omnis/agent"
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

// TestDrainFleetDispatchesFailsClosedWhenParentGone guards the TOCTOU fix: if
// the Conductor session has been deleted/archived (dropped from the registry)
// between the fleet_dispatch tool queuing a directive and the drain running,
// drainFleetDispatches must skip the WHOLE drain rather than falling through
// to a non-experiment (main-checkout) dispatch. Building a fully-wired Manager
// (real squads/runners) is disproportionate here — per the drainSpawns/
// drainFleetDispatches precedent — so this uses the minimal Manager the
// function actually touches on this path: an Infrastructure exposing
// FleetDispatches, wrapping an empty Instance. The assertion is behavioural:
// the queued directive is fully drained (consumed) and no session is
// materialized, which is only possible if the function returned at the
// hoisted parent lookup and never reached materializeSession.
func TestDrainFleetDispatchesFailsClosedWhenParentGone(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	infra := &toolkitagent.Infrastructure{FleetDispatches: toolkitagent.NewFleetDispatchRegistry()}
	inst := &toolkitagent.Instance{Generation: 1, Squads: map[string]*toolkitagent.SquadInstance{}}
	mgr := toolkitagent.NewManager(infra, inst)
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}

	reg := sessions.NewEmptyRegistry() // parentID deliberately absent
	d := serverDeps{Registry: reg, Manager: mgr, rootCtx: context.Background()}

	const parentID = "vanished-conductor"
	if !infra.FleetDispatches.Enqueue(parentID, &toolkitagent.FleetDispatchDirective{
		Project: "Service A",
		Task:    "do the thing",
	}) {
		t.Fatal("Enqueue returned false")
	}

	// Must not panic, and must fail closed: no session materialized for the
	// vanished parent.
	drainFleetDispatches(d, parentID, "alice")

	if got := reg.List(); len(got) != 0 {
		t.Fatalf("expected no session materialized for a vanished parent, got %d: %+v", len(got), got)
	}
	// The directive was drained (and discarded) rather than left queued or
	// re-processed — draining again returns nothing.
	if left := infra.FleetDispatches.Drain(parentID); len(left) != 0 {
		t.Fatalf("expected the directive to have been drained, %d left", len(left))
	}
}
