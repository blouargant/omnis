package fleet

import (
	"strings"
	"testing"
)

func p(name string, deps ...string) Project {
	return Project{Name: name, Cwd: "/x/" + name, Engine: EngineOmnis, DependsOn: deps}
}

func TestTopoOrderChain(t *testing.T) {
	// c depends on b depends on a  =>  a, b, c
	order, err := TopoOrder([]Project{p("c", "b"), p("a"), p("b", "a")})
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, n := range order {
		idx[n] = i
	}
	if !(idx["a"] < idx["b"] && idx["b"] < idx["c"]) {
		t.Fatalf("bad order: %v", order)
	}
}

func TestTopoOrderCycle(t *testing.T) {
	_, err := TopoOrder([]Project{p("a", "b"), p("b", "a")})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestValidateUnknownEdge(t *testing.T) {
	err := Validate([]Project{p("a", "ghost")})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected unknown-edge error, got %v", err)
	}
}

func TestValidateBadEngine(t *testing.T) {
	bad := Project{Name: "a", Cwd: "/x/a", Engine: "python"}
	err := Validate([]Project{bad})
	if err == nil || !strings.Contains(err.Error(), "engine") {
		t.Fatalf("expected engine error, got %v", err)
	}
}

func TestValidateOK(t *testing.T) {
	if err := Validate([]Project{p("a"), p("b", "a")}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateAggregatesMultipleProblems(t *testing.T) {
	projects := []Project{
		{Name: "", Cwd: "/x", Engine: EngineOmnis},
		{Name: "b", Cwd: "/x/b", Engine: "python", DependsOn: []string{"ghost"}},
	}
	err := Validate(projects)
	if err == nil {
		t.Fatal("expected aggregated errors")
	}
	msg := err.Error()
	for _, want := range []string{"blank name", "engine", "ghost"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("want %q in aggregated error, got %q", want, msg)
		}
	}
}

func TestValidateSelfEdge(t *testing.T) {
	err := Validate([]Project{{Name: "a", Cwd: "/x/a", Engine: EngineOmnis, DependsOn: []string{"a"}}})
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("expected self-edge error, got %v", err)
	}
}

func TestValidateAggregatesCycleWithOtherProblem(t *testing.T) {
	projects := []Project{
		{Name: "a", Cwd: "/x/a", Engine: EngineOmnis, DependsOn: []string{"b"}},
		{Name: "b", Cwd: "/x/b", Engine: EngineOmnis, DependsOn: []string{"a"}},
		{Name: "c", Cwd: "/x/c", Engine: "python"},
	}
	err := Validate(projects)
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cycle") || !strings.Contains(msg, "engine") {
		t.Fatalf("expected both cycle and engine in aggregated error, got %q", msg)
	}
}

func TestTopoOrderDanglingEdgeIsNotCycle(t *testing.T) {
	order, err := TopoOrder([]Project{p("a", "ghost")})
	if err != nil {
		t.Fatalf("dangling edge must not be reported as a cycle: %v", err)
	}
	if len(order) != 1 || order[0] != "a" {
		t.Fatalf("expected [a], got %v", order)
	}
}

func TestEngineSquad(t *testing.T) {
	if s, ok := EngineSquad(EngineOmnis); !ok || s != "coding" {
		t.Fatalf("omnis => coding, got %q ok=%v", s, ok)
	}
	if _, ok := EngineSquad(EngineClaude); ok {
		t.Fatal("claude engine has no squad yet (Plan 3)")
	}
	if _, ok := EngineSquad(Engine("bogus")); ok {
		t.Fatal("unknown engine must not map")
	}
}
