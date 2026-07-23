package fleet

import (
	"fmt"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

var (
	resolverMu sync.RWMutex
	resolver   func() []Project
)

// SetProjectsResolver installs the process-wide hook that enumerates fleet
// projects. The server installs one backed by internal/sessions; passing nil
// clears it (used by tests and the no-fleet default). Mirrors
// agent.SetCollectionResolver / fstools.SetCwdResolver.
func SetProjectsResolver(f func() []Project) {
	resolverMu.Lock()
	resolver = f
	resolverMu.Unlock()
}

func currentProjects() []Project {
	resolverMu.RLock()
	f := resolver
	resolverMu.RUnlock()
	if f == nil {
		return nil
	}
	return f()
}

type projectsIn struct{}

type projectView struct {
	Name      string   `json:"name"`
	Cwd       string   `json:"cwd"`
	Engine    string   `json:"engine"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type projectsOut struct {
	Projects []projectView `json:"projects"`
	Order    []string      `json:"order"`    // dependency-first; empty on cycle
	Valid    bool          `json:"valid"`    // graph + workspaces sound
	Problems []string      `json:"problems"` // human-readable issues, empty when valid
}

// runProjects is the handler, extracted so tests can call it without ADK plumbing.
func runProjects(_ adk.ToolContext, _ projectsIn) (projectsOut, error) {
	projects := currentProjects()
	out := projectsOut{Valid: true, Projects: []projectView{}}
	for _, p := range projects {
		out.Projects = append(out.Projects, projectView{
			Name: p.Name, Cwd: p.Cwd, Engine: string(p.Engine), DependsOn: p.DependsOn,
		})
	}
	if err := Validate(projects); err != nil {
		out.Valid = false
		out.Problems = append(out.Problems, err.Error())
	}
	if err := ValidateWorkspaces(projects); err != nil {
		out.Valid = false
		out.Problems = append(out.Problems, err.Error())
	}
	if order, err := TopoOrder(projects); err == nil {
		out.Order = order
	}
	return out, nil
}

// Tools returns the read-only fleet tool group.
func Tools() []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "fleet_projects",
		Description: "List the configured fleet projects (collections marked role:project), " +
			"their engine (omnis|claude) and dependency edges, the dependency-first " +
			"execution order, and any validation problems. Read-only; takes no arguments.",
	}, runProjects)
	if err != nil {
		panic(fmt.Errorf("build fleet_projects tool: %w", err))
	}
	return []tool.Tool{t}
}
