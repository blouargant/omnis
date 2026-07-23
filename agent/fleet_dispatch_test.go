package agent

import (
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/fleet"
)

func TestFleetDispatchRegistryEnqueueDrain(t *testing.T) {
	r := NewFleetDispatchRegistry()
	if !r.Enqueue("sess1", &FleetDispatchDirective{Project: "Service A", Task: "add field"}) {
		t.Fatal("enqueue failed")
	}
	got := r.Drain("sess1")
	if len(got) != 1 || got[0].Project != "Service A" {
		t.Fatalf("drain mismatch: %+v", got)
	}
	if len(r.Drain("sess1")) != 0 {
		t.Fatal("second drain should be empty")
	}
}

func TestFleetDispatchToolUnknownProject(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "Service A", Cwd: "/x/a", Engine: fleet.EngineOmnis}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	_, err := runFleetDispatch(reg, "sess1", fleetDispatchIn{Project: "Ghost", Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "Ghost") {
		t.Fatalf("expected unknown-project error, got %v", err)
	}
}

func TestFleetDispatchToolClaudeEngineNotReady(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "Service B", Cwd: "/x/b", Engine: fleet.EngineClaude}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	_, err := runFleetDispatch(reg, "sess1", fleetDispatchIn{Project: "Service B", Task: "x"})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "claude") {
		t.Fatalf("expected claude-not-ready error, got %v", err)
	}
	// nothing enqueued on error
	if len(reg.Drain("sess1")) != 0 {
		t.Fatal("must not enqueue on a rejected dispatch")
	}
	_ = adk.ToolContext(nil) // ensure the adk import is used if the test file needs it
}
