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
	if len(reg.Drain("sess1")) != 0 {
		t.Fatal("must not enqueue on unknown project")
	}
}

func TestFleetDispatchToolClaudeEngineDispatches(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "Service B", Cwd: "/x/b", Engine: fleet.EngineClaude}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	out, err := runFleetDispatch(reg, "sess1", fleetDispatchIn{Project: "Service B", Task: "x"})
	if err != nil {
		t.Fatalf("claude-engine dispatch errored: %v", err)
	}
	if !strings.Contains(out.Result, "Dispatched") {
		t.Fatalf("unexpected result: %q", out.Result)
	}
	got := reg.Drain("sess1")
	if len(got) != 1 || got[0].Project != "Service B" || got[0].Task != "x" {
		t.Fatalf("expected one canonical directive, got %+v", got)
	}
	_ = adk.ToolContext(nil) // ensure the adk import is used if the test file needs it
}

func TestFleetDispatchToolHappyPath(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "Service A", Cwd: "/x/a", Engine: fleet.EngineOmnis}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	out, err := runFleetDispatch(reg, "sess1", fleetDispatchIn{Project: "service a", Task: "add a field"})
	if err != nil {
		t.Fatalf("valid dispatch errored: %v", err)
	}
	if !strings.Contains(out.Result, "Dispatched") {
		t.Fatalf("unexpected result: %q", out.Result)
	}
	got := reg.Drain("sess1")
	if len(got) != 1 || got[0].Project != "Service A" || got[0].Task != "add a field" {
		t.Fatalf("expected one canonical directive, got %+v", got)
	}
}

func TestFleetDispatchToolBlankRejected(t *testing.T) {
	reg := NewFleetDispatchRegistry()
	if _, err := runFleetDispatch(reg, "s", fleetDispatchIn{Project: "", Task: "x"}); err == nil {
		t.Fatal("blank project must error")
	}
	if _, err := runFleetDispatch(reg, "s", fleetDispatchIn{Project: "p", Task: ""}); err == nil {
		t.Fatal("blank task must error")
	}
	if len(reg.Drain("s")) != 0 {
		t.Fatal("nothing should enqueue on blank input")
	}
}

func TestFleetDispatchToolCapRejected(t *testing.T) {
	fleet.SetProjectsResolver(func() []fleet.Project {
		return []fleet.Project{{Name: "P", Cwd: "/x/p", Engine: fleet.EngineOmnis}}
	})
	t.Cleanup(func() { fleet.SetProjectsResolver(nil) })
	reg := NewFleetDispatchRegistry()
	for i := 0; i < maxDispatchesPerSession; i++ {
		if _, err := runFleetDispatch(reg, "s", fleetDispatchIn{Project: "P", Task: "t"}); err != nil {
			t.Fatalf("dispatch %d errored: %v", i, err)
		}
	}
	if _, err := runFleetDispatch(reg, "s", fleetDispatchIn{Project: "P", Task: "t"}); err == nil {
		t.Fatal("over-cap dispatch must error")
	}
}
