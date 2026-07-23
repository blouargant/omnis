// Package fleet holds the multi-project coordination registry: the Project
// type, the dependency-graph logic, and the read-only fleet_projects tool.
// It imports only stdlib + ADK + core/adk so both `agent` and `server` can
// depend on it without the agent<->sessions import cycle; collection data
// reaches it through the resolver installed via SetProjectsResolver.
package fleet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Engine selects the worker backing a project.
type Engine string

const (
	EngineOmnis  Engine = "omnis"
	EngineClaude Engine = "claude"
)

// RoleProject is the collection-profile Role value marking a collection as a
// fleet project. A collection without this role is a plain collection.
const RoleProject = "project"

// Project is one fleet project, derived from a collection with role:"project".
type Project struct {
	Name      string
	Cwd       string
	Engine    Engine
	DependsOn []string
}

// TopoOrder returns project names in dependency-first order (a project appears
// after everything it depends on). It errors if the graph has a cycle. Edges to
// names that are not themselves projects are ignored here (they are reported by
// Validate as unknown dependencies), so a dangling edge is never mistaken for a
// cycle. Order is deterministic (ties broken alphabetically).
func TopoOrder(projects []Project) ([]string, error) {
	known := make(map[string]bool, len(projects))
	for _, p := range projects {
		known[p.Name] = true
	}
	indeg := make(map[string]int, len(projects))
	adj := make(map[string][]string, len(projects))
	for _, p := range projects {
		if _, ok := indeg[p.Name]; !ok {
			indeg[p.Name] = 0
		}
	}
	for _, p := range projects {
		for _, dep := range p.DependsOn {
			if !known[dep] {
				continue // unknown dep: a Validate concern, not an ordering/cycle one
			}
			adj[dep] = append(adj[dep], p.Name) // dep must precede p
			indeg[p.Name]++
		}
	}
	var ready []string
	for name, d := range indeg {
		if d == 0 {
			ready = append(ready, name)
		}
	}
	sort.Strings(ready)
	var order []string
	for len(ready) > 0 {
		n := ready[0]
		ready = ready[1:]
		order = append(order, n)
		var freed []string
		for _, m := range adj[n] {
			indeg[m]--
			if indeg[m] == 0 {
				freed = append(freed, m)
			}
		}
		sort.Strings(freed)
		ready = append(ready, freed...)
		sort.Strings(ready)
	}
	if len(order) != len(indeg) {
		return nil, fmt.Errorf("dependency cycle among fleet projects (ordered %d of %d)", len(order), len(indeg))
	}
	return order, nil
}

// Validate aggregates all structural problems: blank names, unknown/self edges,
// unknown engine, and cycles. Returns nil when the graph is sound.
func Validate(projects []Project) error {
	var problems []string
	names := make(map[string]bool, len(projects))
	for i, p := range projects {
		if strings.TrimSpace(p.Name) == "" {
			problems = append(problems, fmt.Sprintf("project #%d has a blank name", i))
			continue
		}
		names[p.Name] = true
	}
	for _, p := range projects {
		if strings.TrimSpace(p.Name) == "" {
			continue // already reported above
		}
		if p.Engine != EngineOmnis && p.Engine != EngineClaude {
			problems = append(problems, fmt.Sprintf("project %q: unknown engine %q (want omnis|claude)", p.Name, p.Engine))
		}
		for _, dep := range p.DependsOn {
			switch {
			case dep == p.Name:
				problems = append(problems, fmt.Sprintf("project %q depends on itself", p.Name))
			case !names[dep]:
				problems = append(problems, fmt.Sprintf("project %q depends on unknown project %q", p.Name, dep))
			}
		}
	}
	if _, err := TopoOrder(projects); err != nil {
		problems = append(problems, err.Error())
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Projects returns the currently-configured fleet projects via the installed
// resolver (nil when no resolver is installed — e.g. CLI/TUI, or no server).
func Projects() []Project { return currentProjects() }

// EngineSquad maps a project engine to the squad name that runs a Driver for it.
// omnis → the Coding squad. The claude engine has no squad yet (Plan 3) and
// returns ok=false so callers report it as not-yet-available rather than
// silently running it on the wrong engine.
func EngineSquad(e Engine) (string, bool) {
	if e == EngineOmnis {
		return "coding", true
	}
	return "", false
}
