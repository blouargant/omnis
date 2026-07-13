package agent

import (
	"context"
	"iter"
	"reflect"
	"testing"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/events"
)

// fakeLLM satisfies model.LLM so the sub-agent tree can be BUILT without any
// provider credentials. Nothing here is ever run — these tests assert wiring
// (who can reach whom), not behaviour.
type fakeLLM struct{ name string }

func (f fakeLLM) Name() string { return f.name }
func (f fakeLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

func buildTree(t *testing.T, members []RuntimeAgentConfig, catalogue []RuntimeAgentConfig) (map[string]adkagent.Agent, []tool.Tool) {
	t.Helper()
	runtime := RuntimeSettings{Agents: catalogue}
	modelFor := func(cfg RuntimeAgentConfig) (model.LLM, error) { return fakeLLM{name: cfg.Name}, nil }

	subAgentMap, _, leaderTools, _, err := buildSubAgentsFromConfigs(
		context.Background(), members, runtime,
		nil, nil, nil, nil,
		modelFor, events.AgentCallbacks{},
		nil, nil, nil, nil,
		nil, nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return subAgentMap, leaderTools
}

// agentToolNames reads the tool list off a BUILT agent. ADK's llmAgent is
// unexported, but its embedded llminternal.State carries an exported Tools field,
// so reflection can reach it. This is the assertion that actually matters and that
// nothing else makes: that the gatherer is mounted ON THE SPECIALIST. Building it
// and keeping it off the leader (the checks below) is necessary but not sufficient
// — a nested tool that is never appended to the caller's tool list leaves the
// specialist unable to delegate, and the failure is silent: the model simply says
// it "cannot verify" and answers from memory.
func agentToolNames(t *testing.T, a adkagent.Agent) []string {
	t.Helper()
	v := reflect.ValueOf(a)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	st := v.FieldByName("State")
	if !st.IsValid() {
		t.Fatalf("built agent has no State field (ADK layout changed?): %T", a)
	}
	tf := st.FieldByName("Tools")
	if !tf.IsValid() || !tf.CanInterface() {
		t.Fatalf("built agent's State has no readable Tools field (ADK layout changed?)")
	}
	tools, ok := tf.Interface().([]tool.Tool)
	if !ok {
		t.Fatalf("State.Tools is %T, want []tool.Tool", tf.Interface())
	}
	return toolNames(tools)
}

func toolNames(tools []tool.Tool) []string {
	out := make([]string, 0, len(tools))
	for _, tl := range tools {
		out = append(out, tl.Name())
	}
	return out
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// The whole point of the feature, end to end: a gatherer that is NOT a squad
// member is still built (so the specialist can call it), and is NOT handed to the
// leader (so the coordinator's tool list does not grow every time a specialist
// gains a helper).
func TestNestedGathererIsBuiltButNotMountedOnTheLeader(t *testing.T) {
	catalogue := []RuntimeAgentConfig{
		{Name: "research_critic", Enabled: true, MaxInstances: 1, SubAgents: []string{"web_fetcher"}},
		{Name: "web_fetcher", Enabled: true, MaxInstances: 8},
		{Name: "summariser", Enabled: true, MaxInstances: 1},
	}
	members := []RuntimeAgentConfig{catalogue[0], catalogue[2]} // web_fetcher is NOT a member

	built, leaderTools := buildTree(t, members, catalogue)

	critic, ok := built["research_critic"]
	if !ok {
		t.Fatal("the specialist was never built")
	}
	if _, ok := built["web_fetcher"]; !ok {
		t.Fatal("the nested-only gatherer was never built — the specialist could not call it")
	}

	// THE assertion: the gatherer is a tool ON THE SPECIALIST. Without this the
	// feature is inert — the critic just reports it "cannot verify" and answers
	// from memory, which is exactly the silent failure this guards.
	if got := agentToolNames(t, critic); !has(got, "web_fetcher") {
		t.Fatalf("research_critic's tools = %v, want its declared subagent web_fetcher mounted", got)
	}
	// And the gatherer itself has no team, so it must not have gained one.
	if got := agentToolNames(t, built["web_fetcher"]); has(got, "research_critic") {
		t.Fatalf("web_fetcher's tools = %v, want no delegation back to its caller", got)
	}

	names := toolNames(leaderTools)
	if has(names, "web_fetcher") {
		t.Fatalf("leader tools = %v; a nested-only gatherer must not be mounted on the leader", names)
	}
	if !has(names, "research_critic") || !has(names, "summariser") {
		t.Fatalf("leader tools = %v, want both direct members", names)
	}
}

// Regression guard for the no-op contract: a fleet declaring no `subagents` must
// mount exactly the direct members on the leader, in declared order — byte-identical
// to the pre-nesting build.
func TestPlainFleetMountsMembersInDeclaredOrder(t *testing.T) {
	catalogue := []RuntimeAgentConfig{
		{Name: "investigator", Enabled: true, MaxInstances: 1},
		{Name: "web_agent", Enabled: true, MaxInstances: 10},
		{Name: "summariser", Enabled: true, MaxInstances: 1},
	}
	built, leaderTools := buildTree(t, catalogue, catalogue)

	if len(built) != 3 {
		t.Fatalf("built %d agents, want 3", len(built))
	}
	got := toolNames(leaderTools)
	want := []string{"investigator", "web_agent", "summariser"}
	if !equalStrings(got, want) {
		t.Fatalf("leader tools = %v, want %v", got, want)
	}
}

// A gatherer shared by two specialists is built ONCE, but each caller gets its own
// tool wrapper. Sharing one wrapper would make them contend on the non-concurrent
// wrapper's mutex — a call from one would report the agent "already running" to the
// other.
func TestSharedGathererIsBuiltOnceAndWrappedPerCaller(t *testing.T) {
	catalogue := []RuntimeAgentConfig{
		{Name: "research_critic", Enabled: true, MaxInstances: 1, SubAgents: []string{"web_fetcher"}},
		{Name: "web_agent", Enabled: true, MaxInstances: 1, SubAgents: []string{"web_fetcher"}},
		{Name: "web_fetcher", Enabled: true, MaxInstances: 8},
	}
	members := []RuntimeAgentConfig{catalogue[0], catalogue[1]}

	built, leaderTools := buildTree(t, members, catalogue)
	if len(built) != 3 {
		t.Fatalf("built %v, want the shared gatherer constructed exactly once", len(built))
	}
	if names := toolNames(leaderTools); len(names) != 2 {
		t.Fatalf("leader tools = %v, want only the two direct members", names)
	}

	// Every mount point gets its OWN wrapper over the one shared agent. The
	// non-concurrent wrapper's mutex (and the resumable wrapper's handle map) is
	// per-tool state: were the wrapper shared, one caller's in-flight call would make
	// the agent report "already running" to the other.
	w1, err := wrapSubAgentTool(built["web_fetcher"], catalogue[2])
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	w2, err := wrapSubAgentTool(built["web_fetcher"], catalogue[2])
	if err != nil {
		t.Fatalf("wrap: %v", err)
	}
	if w1 == w2 {
		t.Fatal("two mount points share one wrapper — they would contend on its mutex")
	}
	if w1.Name() != w2.Name() {
		t.Fatalf("wrappers disagree on the tool name: %q vs %q", w1.Name(), w2.Name())
	}
}
