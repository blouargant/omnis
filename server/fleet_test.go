package main

import (
	"testing"

	"github.com/blouargant/omnis/internal/fleet"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestInstallFleetResolverEnumeratesProjectCollections(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	// Two fleet projects + one plain collection.
	mustAdd := func(name string) {
		if _, _, err := sessions.AddCollection(name); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd("Service A")
	mustAdd("Service B")
	mustAdd("Notes") // plain collection, not a project

	if err := sessions.SetCollectionProfileData("Service A", sessions.CollectionProfileData{
		Role: "project", Engine: "omnis", Cwd: "/repos/a",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfileData("Service B", sessions.CollectionProfileData{
		Role: "project", Engine: "claude", Cwd: "/repos/b", DependsOn: []string{"Service A"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := sessions.SetCollectionProfileData("Notes", sessions.CollectionProfileData{
		Cwd: "/notes",
	}); err != nil {
		t.Fatal(err)
	}

	installFleetResolver()
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })

	got := fleetProjectsForTest()
	if len(got) != 2 {
		t.Fatalf("want 2 fleet projects, got %d: %+v", len(got), got)
	}
	byName := map[string]fleet.Project{}
	for _, p := range got {
		byName[p.Name] = p
	}
	if byName["Service B"].Engine != fleet.EngineClaude {
		t.Fatalf("Service B engine = %q", byName["Service B"].Engine)
	}
	if len(byName["Service B"].DependsOn) != 1 || byName["Service B"].DependsOn[0] != "Service A" {
		t.Fatalf("Service B deps = %v", byName["Service B"].DependsOn)
	}
	if _, ok := byName["Notes"]; ok {
		t.Fatal("plain collection must not appear as a fleet project")
	}
}

func TestCollectFleetProjectsMapsFleetTagAndFolds(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	if _, _, err := sessions.AddFleet("Payments", sessions.FleetMetaData{}); err != nil {
		t.Fatalf("AddFleet: %v", err)
	}
	if _, _, err := sessions.AddCollection("api"); err != nil {
		t.Fatalf("AddCollection api: %v", err)
	}
	if _, _, err := sessions.AddCollection("orphan"); err != nil {
		t.Fatalf("AddCollection orphan: %v", err)
	}
	if err := sessions.AssignProject("Payments", "api"); err != nil {
		t.Fatalf("AssignProject: %v", err)
	}
	// A project tagged to a fleet that does not exist must fold to "" (Ungrouped).
	if err := sessions.UpdateCollectionProfile("orphan", func(p *sessions.CollectionProfileData) {
		p.Role = "project"
		p.Engine = "omnis"
		p.Fleet = "GhostFleet"
	}); err != nil {
		t.Fatalf("tag orphan: %v", err)
	}

	got := map[string]string{}
	for _, p := range collectFleetProjects() {
		got[p.Name] = p.Fleet
	}
	if got["api"] != "Payments" {
		t.Fatalf("api fleet = %q, want Payments", got["api"])
	}
	if got["orphan"] != "" {
		t.Fatalf("orphan fleet = %q, want \"\" (unknown fleet folds to Ungrouped)", got["orphan"])
	}
}
