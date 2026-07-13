package agent

import (
	"strings"
	"testing"
)

func agentCfg(name string, subs ...string) RuntimeAgentConfig {
	return RuntimeAgentConfig{Name: name, Enabled: true, SubAgents: subs}
}

func TestValidateSubAgentGraphAcceptsADAG(t *testing.T) {
	agents := []RuntimeAgentConfig{
		agentCfg("research_critic", "web_fetcher"),
		agentCfg("web_agent", "web_fetcher"), // a gatherer may serve several callers
		agentCfg("web_fetcher"),
	}
	if err := validateSubAgentGraph(agents); err != nil {
		t.Fatalf("valid DAG rejected: %v", err)
	}
}

// A cycle can never be BUILT — wiring a's nested tool needs b's agent object, and
// b's needs a's — so it has to be rejected at config time, not discovered mid-turn.
func TestValidateSubAgentGraphRejectsCycles(t *testing.T) {
	cases := map[string][]RuntimeAgentConfig{
		"direct": {
			agentCfg("a", "b"),
			agentCfg("b", "a"),
		},
		"transitive": {
			agentCfg("a", "b"),
			agentCfg("b", "c"),
			agentCfg("c", "a"),
		},
	}
	for name, agents := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateSubAgentGraph(agents)
			if err == nil {
				t.Fatal("a cycle was accepted")
			}
			if !strings.Contains(err.Error(), "cycle") {
				t.Fatalf("error %q does not name the cycle", err)
			}
		})
	}
}

func TestValidateSubAgentGraphRejectsBadEdges(t *testing.T) {
	cases := map[string]struct {
		agents []RuntimeAgentConfig
		want   string
	}{
		"self-reference": {
			agents: []RuntimeAgentConfig{agentCfg("a", "a")},
			want:   "itself",
		},
		"unknown target": {
			agents: []RuntimeAgentConfig{agentCfg("a", "ghost")},
			want:   "unknown agent",
		},
		"disabled target": {
			agents: []RuntimeAgentConfig{
				agentCfg("a", "b"),
				{Name: "b", Enabled: false},
			},
			want: "disabled agent",
		},
		"the curator is not delegable": {
			agents: []RuntimeAgentConfig{
				agentCfg("a", "curator"),
				agentCfg("curator"),
			},
			want: "curator",
		},
		"duplicate edge": {
			agents: []RuntimeAgentConfig{
				agentCfg("a", "b", "b"),
				agentCfg("b"),
			},
			want: "twice",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateSubAgentGraph(tc.agents)
			if err == nil {
				t.Fatalf("accepted an invalid edge (%s)", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// A disabled agent's edges are not enforced: disabling an agent must not be able
// to break the config by way of a dangling reference it no longer uses.
func TestValidateSubAgentGraphIgnoresDisabledCallers(t *testing.T) {
	agents := []RuntimeAgentConfig{
		{Name: "a", Enabled: false, SubAgents: []string{"ghost"}},
	}
	if err := validateSubAgentGraph(agents); err != nil {
		t.Fatalf("a disabled agent's dangling edge was enforced: %v", err)
	}
}

// The point of the closure: a pure gatherer serves ONE specialist without being a
// squad member. It must still be built.
func TestSubAgentClosurePullsInNonMemberGatherers(t *testing.T) {
	catalogue := []RuntimeAgentConfig{
		agentCfg("research_critic", "web_fetcher"),
		agentCfg("web_fetcher", "url_reader"),
		agentCfg("url_reader"),
		agentCfg("unrelated"),
	}
	members := []RuntimeAgentConfig{catalogue[0]} // only the critic is a squad member

	closure, err := subAgentClosure(members, catalogue)
	if err != nil {
		t.Fatalf("closure: %v", err)
	}
	got := names(closure)
	want := []string{"research_critic", "web_fetcher", "url_reader"}
	if len(got) != len(want) {
		t.Fatalf("closure = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("closure = %v, want %v (roots first, then breadth-first)", got, want)
		}
	}
	for _, n := range got {
		if n == "unrelated" {
			t.Fatal("closure pulled in an agent nothing references")
		}
	}
}

// The build wires an agent's nested tool from the already-constructed target, so
// every agent must be ordered after the agents it delegates to.
func TestTopoOrderPutsGatherersBeforeTheirCallers(t *testing.T) {
	cfgs := []RuntimeAgentConfig{
		agentCfg("research_critic", "web_fetcher"),
		agentCfg("web_fetcher", "url_reader"),
		agentCfg("url_reader"),
	}
	ordered, err := topoOrderSubAgents(cfgs)
	if err != nil {
		t.Fatalf("topo order: %v", err)
	}
	pos := map[string]int{}
	for i, c := range ordered {
		pos[c.Name] = i
	}
	if len(ordered) != 3 {
		t.Fatalf("ordered %d agents, want 3", len(ordered))
	}
	if pos["url_reader"] > pos["web_fetcher"] || pos["web_fetcher"] > pos["research_critic"] {
		t.Fatalf("order = %v, want dependencies first", names(ordered))
	}
}

// A gatherer shared by two callers must appear exactly once in the build, not once
// per caller — each caller gets its own tool WRAPPER, but they share the agent.
func TestTopoOrderDeduplicatesASharedGatherer(t *testing.T) {
	cfgs := []RuntimeAgentConfig{
		agentCfg("critic", "fetcher"),
		agentCfg("researcher", "fetcher"),
		agentCfg("fetcher"),
	}
	ordered, err := topoOrderSubAgents(cfgs)
	if err != nil {
		t.Fatalf("topo order: %v", err)
	}
	if len(ordered) != 3 {
		t.Fatalf("ordered = %v, want each agent exactly once", names(ordered))
	}
	pos := map[string]int{}
	for i, c := range ordered {
		pos[c.Name] = i
	}
	if pos["fetcher"] > pos["critic"] || pos["fetcher"] > pos["researcher"] {
		t.Fatalf("order = %v, want the shared gatherer before both callers", names(ordered))
	}
}

// No-op contract: a fleet with no `subagents` anywhere must behave exactly as
// before — the closure is the member list and the order is unchanged.
func TestNoSubAgentsIsAByteIdenticalNoOp(t *testing.T) {
	catalogue := []RuntimeAgentConfig{
		agentCfg("investigator"),
		agentCfg("summariser"),
		agentCfg("web_agent"),
	}
	if err := validateSubAgentGraph(catalogue); err != nil {
		t.Fatalf("plain fleet rejected: %v", err)
	}
	closure, err := subAgentClosure(catalogue, catalogue)
	if err != nil {
		t.Fatalf("closure: %v", err)
	}
	ordered, err := topoOrderSubAgents(closure)
	if err != nil {
		t.Fatalf("topo order: %v", err)
	}
	want := names(catalogue)
	if got := names(ordered); !equalStrings(got, want) {
		t.Fatalf("order = %v, want the declared order %v unchanged", got, want)
	}
}

func names(cfgs []RuntimeAgentConfig) []string {
	out := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, c.Name)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
