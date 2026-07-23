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
