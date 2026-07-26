package sessions

import (
	"sort"
	"testing"
)

func mustAddCollection(t *testing.T, name string) {
	t.Helper()
	if _, _, err := AddCollection(name); err != nil {
		t.Fatalf("AddCollection %q: %v", name, err)
	}
}

func TestFleetMembershipLifecycle(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	mustAddCollection(t, "api")
	mustAddCollection(t, "gateway")
	mustAddCollection(t, "legacy") // stays a project but Ungrouped
	if _, _, err := AddFleet("Payments", FleetMetaData{DefaultEngine: "claude"}); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}

	// Assign two projects; the third becomes a project with no fleet.
	if err := AssignProject("Payments", "api"); err != nil {
		t.Fatalf("AssignProject api: %v", err)
	}
	if err := AssignProject("Payments", "gateway"); err != nil {
		t.Fatalf("AssignProject gateway: %v", err)
	}
	if err := UpdateCollectionProfile("legacy", func(p *CollectionProfileData) {
		p.Role = "project"
		p.Engine = "omnis"
	}); err != nil {
		t.Fatalf("make legacy a project: %v", err)
	}

	// Assign seeds role=project and the fleet's default engine.
	ap := CollectionProfileFull("api")
	if ap.Role != "project" || ap.Fleet != "Payments" || ap.Engine != "claude" {
		t.Fatalf("assigned api profile = %+v (want role=project fleet=Payments engine=claude)", ap)
	}

	// Members are the tagged projects; Ungrouped catches the untagged project.
	if got := sortedCopy(FleetMembers("Payments")); !equalSlices(got, []string{"api", "gateway"}) {
		t.Fatalf("FleetMembers(Payments) = %v", got)
	}
	if got := FleetMembers(UngroupedFleet); !equalSlices(got, []string{"legacy"}) {
		t.Fatalf("FleetMembers(Ungrouped) = %v", got)
	}

	// Unassign returns a project to Ungrouped.
	if err := UnassignProject("gateway"); err != nil {
		t.Fatalf("UnassignProject: %v", err)
	}
	if got := FleetMembers("Payments"); !equalSlices(got, []string{"api"}) {
		t.Fatalf("after unassign, members = %v", got)
	}
	if got := sortedCopy(FleetMembers(UngroupedFleet)); !equalSlices(got, []string{"gateway", "legacy"}) {
		t.Fatalf("after unassign, ungrouped = %v", got)
	}

	// Rename migrates metadata AND every member's tag.
	if _, _, err := RenameFleet("Payments", "Billing"); err != nil {
		t.Fatalf("RenameFleet: %v", err)
	}
	if FleetExists("Payments") || !FleetExists("Billing") {
		t.Fatalf("rename didn't move the fleet object")
	}
	if CollectionProfileFull("api").Fleet != "Billing" {
		t.Fatalf("rename didn't rewrite member tag: %q", CollectionProfileFull("api").Fleet)
	}
	if FleetMetaFor("Billing").DefaultEngine != "claude" {
		t.Fatalf("rename didn't migrate metadata")
	}

	// Remove clears member tags (→ Ungrouped) and drops the fleet object.
	if _, _, err := RemoveFleet("Billing"); err != nil {
		t.Fatalf("RemoveFleet: %v", err)
	}
	if FleetExists("Billing") {
		t.Fatalf("fleet still present after remove")
	}
	if CollectionProfileFull("api").Fleet != "" {
		t.Fatalf("remove left an orphaned member tag: %q", CollectionProfileFull("api").Fleet)
	}
	if CollectionProfileFull("api").Role != "project" {
		t.Fatalf("remove must NOT strip role:project (still a project, just Ungrouped)")
	}
}

func sortedCopy(in []string) []string { out := append([]string(nil), in...); sort.Strings(out); return out }
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
