package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFleetProjectsToolValid(t *testing.T) {
	// Real, git-initialized temp dirs: runProjects also runs ValidateWorkspaces
	// (the projectsOut.Valid field is documented as "graph + workspaces sound"),
	// so a fake, nonexistent Cwd would make this project set workspace-invalid
	// regardless of the (valid, acyclic) dependency graph under test here.
	dirA := t.TempDir()
	dirB := t.TempDir()
	if err := os.Mkdir(filepath.Join(dirA, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dirB, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	SetProjectsResolver(func() []Project {
		return []Project{
			{Name: "a", Cwd: dirA, Engine: EngineOmnis},
			{Name: "b", Cwd: dirB, Engine: EngineClaude, DependsOn: []string{"a"}},
		}
	})
	t.Cleanup(func() { SetProjectsResolver(nil) })

	out, err := runProjects(nil, projectsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Projects) != 2 {
		t.Fatalf("want 2 projects, got %d", len(out.Projects))
	}
	if len(out.Order) != 2 || out.Order[0] != "a" || out.Order[1] != "b" {
		t.Fatalf("bad topo order: %v", out.Order)
	}
	if !out.Valid {
		t.Fatalf("graph should be valid, problems=%v", out.Problems)
	}
}

func TestFleetProjectsToolNoResolver(t *testing.T) {
	SetProjectsResolver(nil)
	out, err := runProjects(nil, projectsIn{})
	if err != nil {
		t.Fatalf("no-resolver must not error: %v", err)
	}
	if !out.Valid {
		t.Fatalf("empty fleet must be valid, problems=%v", out.Problems)
	}
	if len(out.Projects) != 0 || len(out.Order) != 0 {
		t.Fatalf("expected empty projects/order, got %+v", out)
	}
}

func TestFleetToolsShape(t *testing.T) {
	ts := Tools()
	if len(ts) != 1 {
		t.Fatalf("expected exactly 1 fleet tool, got %d", len(ts))
	}
	if ts[0].Name() != "fleet_projects" {
		t.Fatalf("expected tool name %q, got %q", "fleet_projects", ts[0].Name())
	}
}

func TestFleetProjectsToolReportsCycle(t *testing.T) {
	SetProjectsResolver(func() []Project {
		return []Project{
			{Name: "a", Cwd: "/x/a", Engine: EngineOmnis, DependsOn: []string{"b"}},
			{Name: "b", Cwd: "/x/b", Engine: EngineOmnis, DependsOn: []string{"a"}},
		}
	})
	t.Cleanup(func() { SetProjectsResolver(nil) })

	out, err := runProjects(nil, projectsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Valid {
		t.Fatal("cyclic graph must report invalid")
	}
	if len(out.Problems) == 0 {
		t.Fatal("expected a problem describing the cycle")
	}
}
