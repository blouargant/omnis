package fleet

import (
	"fmt"
	"strings"
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

var (
	sessionFleetMu sync.RWMutex
	sessionFleetFn func(sessionID string) string
)

// SetSessionFleetResolver installs the process-wide hook mapping a session id to
// the fleet it coordinates (""=unscoped ⇒ Ungrouped). The server installs one
// backed by the session registry; nil clears it (tests, CLI/TUI, no-fleet
// default). Mirrors SetProjectsResolver.
func SetSessionFleetResolver(f func(sessionID string) string) {
	sessionFleetMu.Lock()
	sessionFleetFn = f
	sessionFleetMu.Unlock()
}

func sessionFleet(sessionID string) string {
	sessionFleetMu.RLock()
	f := sessionFleetFn
	sessionFleetMu.RUnlock()
	if f == nil {
		return ""
	}
	return strings.TrimSpace(f(sessionID))
}

// ProjectsForSession returns the projects visible to a session: those whose Fleet
// matches the session's fleet scope. An unscoped session ("" fleet) sees the
// Ungrouped pool (projects with an empty Fleet). This is what confines a Conductor
// to its own fleet.
func ProjectsForSession(sessionID string) []Project {
	return projectsForFleet(sessionFleet(sessionID))
}

func projectsForFleet(fleetName string) []Project {
	fleetName = strings.TrimSpace(fleetName)
	var out []Project
	for _, p := range currentProjects() {
		if strings.EqualFold(strings.TrimSpace(p.Fleet), fleetName) {
			out = append(out, p)
		}
	}
	return out
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
func runProjects(ctx adk.ToolContext, _ projectsIn) (projectsOut, error) {
	sessionID := ""
	if ctx != nil {
		sessionID = ctx.SessionID()
	}
	projects := ProjectsForSession(sessionID)
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
