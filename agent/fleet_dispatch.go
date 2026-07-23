// fleet_dispatch.go — the leader-only `fleet_dispatch` tool. The Conductor hands
// one project a coding task; the tool records a directive (mirroring
// spawn_session → SpawnRegistry), and the server drains it after the turn,
// materialising a Driver session (a Coding-squad session rooted at the project's
// collection cwd + filed under its collection) and delivering the result back to
// the Conductor. Host-side record-then-drain, because agent cannot import server.
package agent

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/fleet"
)

// maxDispatchesPerSession bounds how many project dispatches one Conductor turn
// may queue, so a runaway plan can't fan out without limit.
const maxDispatchesPerSession = 16

// FleetDispatchDirective is one queued "dispatch this task to this project's
// Driver" request. Only the project name + task are recorded; the server
// re-resolves the project's cwd/collection/engine at drain time (single source
// of truth).
type FleetDispatchDirective struct {
	Project string
	Task    string
}

// FleetDispatchRegistry holds pending dispatches per Conductor session
// (process-wide, on Infrastructure, survives hot-reload). Mirrors SpawnRegistry.
type FleetDispatchRegistry struct {
	mu sync.Mutex
	m  map[string][]*FleetDispatchDirective
}

// NewFleetDispatchRegistry returns an empty registry.
func NewFleetDispatchRegistry() *FleetDispatchRegistry {
	return &FleetDispatchRegistry{m: map[string][]*FleetDispatchDirective{}}
}

// Enqueue records a dispatch request for sessionID. It returns false when the
// per-turn cap for the session is already reached (the tool surfaces that as an
// error so the leader stops dispatching).
func (r *FleetDispatchRegistry) Enqueue(sessionID string, d *FleetDispatchDirective) bool {
	if r == nil || sessionID == "" || d == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.m[sessionID]) >= maxDispatchesPerSession {
		return false
	}
	r.m[sessionID] = append(r.m[sessionID], d)
	return true
}

// Drain returns and clears the queued dispatch requests for sessionID (nil when
// none), in enqueue order.
func (r *FleetDispatchRegistry) Drain(sessionID string) []*FleetDispatchDirective {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.m[sessionID]
	delete(r.m, sessionID)
	return ds
}

// Forget drops any queued dispatch directives for sessionID. Directives are
// normally transient (Drain clears them after each turn), but a directive
// recorded during a turn that is torn down mid-flight would otherwise linger
// until the id is reused; call this on session delete/archive.
func (r *FleetDispatchRegistry) Forget(sessionID string) {
	if r == nil || sessionID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.m, sessionID)
}

type fleetDispatchIn struct {
	Project string `json:"project" jsonschema:"the fleet project (collection name) to hand the task to; must be one listed by fleet_projects"`
	Task    string `json:"task" jsonschema:"the self-contained coding task for that project's Driver; restate everything it needs — it does not see this conversation"`
}
type fleetDispatchOut struct {
	Result string `json:"result"`
}

// runFleetDispatch validates the project against the live fleet registry and
// enqueues the directive. Extracted from the tool closure so it is unit-testable
// without ADK plumbing (takes the session id directly).
func runFleetDispatch(reg *FleetDispatchRegistry, sessionID string, in fleetDispatchIn) (fleetDispatchOut, error) {
	name := strings.TrimSpace(in.Project)
	task := strings.TrimSpace(in.Task)
	if name == "" || task == "" {
		return fleetDispatchOut{}, fmt.Errorf("both project and task are required")
	}
	projects := fleet.Projects()
	var match *fleet.Project
	var names []string
	for i := range projects {
		names = append(names, projects[i].Name)
		if strings.EqualFold(projects[i].Name, name) {
			match = &projects[i]
		}
	}
	if match == nil {
		return fleetDispatchOut{}, fmt.Errorf("unknown fleet project %q; available: %s", in.Project, strings.Join(names, ", "))
	}
	if _, ok := fleet.EngineSquad(match.Engine); !ok {
		return fleetDispatchOut{}, fmt.Errorf("project %q uses the %q engine, which is not available yet (the external Claude Code worker lands in a later phase); only omnis-engine projects can be dispatched today", match.Name, match.Engine)
	}
	if !reg.Enqueue(sessionID, &FleetDispatchDirective{Project: match.Name, Task: task}) {
		return fleetDispatchOut{}, fmt.Errorf("too many dispatches this turn (max %d) — let the running Drivers report back first", maxDispatchesPerSession)
	}
	return fleetDispatchOut{Result: fmt.Sprintf("Dispatched %q to project %q. Its Driver is working in the background; its result will come back to you as a follow-up message.", task, match.Name)}, nil
}

// fleetDispatchTool builds the leader-only fleet_dispatch tool.
func fleetDispatchTool(reg *FleetDispatchRegistry) tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "fleet_dispatch",
		Description: "Hand a coding task to one fleet project's Driver — a dedicated session rooted in that " +
			"project's directory with the project's own instructions. Use the exact project names from " +
			"fleet_projects. The Driver runs in the background and its result is delivered back to you as a " +
			"follow-up message; dispatch dependencies (see fleet_projects order) BEFORE the projects that depend " +
			"on them, and wait for a project's result before dispatching a project that needs its output. Restate " +
			"everything the task needs in `task` — the Driver does not see this conversation.",
	}, func(ctx adk.ToolContext, in fleetDispatchIn) (fleetDispatchOut, error) {
		return runFleetDispatch(reg, ctx.SessionID(), in)
	})
	if err != nil {
		panic(fmt.Errorf("fleet_dispatch tool: %w", err))
	}
	return t
}
