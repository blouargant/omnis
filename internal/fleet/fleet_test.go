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
