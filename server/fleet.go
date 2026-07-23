package main

import (
	"github.com/blouargant/omnis/internal/fleet"
	"github.com/blouargant/omnis/internal/sessions"
)

// installFleetResolver wires the process-wide fleet project resolver to the
// collection registry: a fleet project is a collection whose profile has
// role:"project". Called once at server startup (see run() in main.go, beside
// agent.SetCollectionResolver / fstools.SetCwdResolver). CLI/TUI never call it,
// so fleet_projects returns an empty list there — the no-op contract.
func installFleetResolver() {
	fleet.SetProjectsResolver(collectFleetProjects)
}

func collectFleetProjects() []fleet.Project {
	names, err := sessions.ListCollections()
	if err != nil {
		return nil
	}
	var out []fleet.Project
	for _, name := range names {
		p := sessions.CollectionProfileFull(name)
		if p.Role != fleet.RoleProject {
			continue
		}
		out = append(out, fleet.Project{
			Name:      name,
			Cwd:       p.Cwd,
			Engine:    fleet.Engine(p.Engine),
			DependsOn: p.DependsOn,
		})
	}
	return out
}

// fleetProjectsForTest exposes the collected projects to the package test
// without depending on resolver-call timing.
func fleetProjectsForTest() []fleet.Project { return collectFleetProjects() }
