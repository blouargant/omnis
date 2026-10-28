package permissions

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

func TestVerdictDefaultAskOnlyForUnmatchedCalls(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"ask":["Bash(iapcli *)"],"deny":["Edit"],"defaultMode":"default"}}`)
	if v := cfg.CheckArgsVerdict("demo_worker", nil, ""); v.Decision != DecisionAsk || !v.DefaultAsk {
		t.Errorf("unmatched: %+v", v)
	}
	if v := cfg.CheckArgsVerdict("Bash", bash("iapcli x"), ""); v.Decision != DecisionAsk || v.DefaultAsk {
		t.Errorf("ask rule: %+v", v)
	}
	if v := cfg.CheckArgsVerdict("Edit", file("/x"), ""); v.Decision != DecisionDeny || v.DefaultAsk {
		t.Errorf("deny rule: %+v", v)
	}
}

func TestVerdictPlanModeNeverDefaultAsk(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"defaultMode":"plan"}}`)
	if v := cfg.CheckArgsVerdict("mcp__s__t", nil, ""); v.Decision != DecisionAsk || v.DefaultAsk {
		t.Errorf("plan: %+v", v)
	}
}

func TestClearanceTakenOnce(t *testing.T) {
	c := NewClearances()
	c.Grant("s", "i1", "c", "Bash")
	if !c.Take("s", "i1", "c", "Bash") || c.Take("s", "i1", "c", "Bash") {
		t.Error("expected true then false")
	}
}

func TestClearanceIsPerSession(t *testing.T) {
	c := NewClearances()
	c.Grant("A", "i1", "call_0", "Bash")
	if c.Take("B", "i1", "call_0", "Bash") {
		t.Error("clearance leaked across sessions")
	}
	if !c.Take("A", "i1", "call_0", "Bash") {
		t.Error("owner lost its clearance")
	}
}

func TestNilClearancesAreInert(t *testing.T) {
	var c *Clearances
	c.Grant("s", "i1", "c", "t")
	if c.Take("s", "i1", "c", "t") {
		t.Error("nil clearances granted")
	}
}

type gateCtx struct {
	adk.ToolContext
	context.Context
	session, callID, inv string
}

func (g gateCtx) SessionID() string           { return g.session }
func (g gateCtx) FunctionCallID() string      { return g.callID }
func (g gateCtx) InvocationID() string        { return g.inv }
func (g gateCtx) Value(k any) any             { return g.Context.Value(k) }
func (g gateCtx) Deadline() (time.Time, bool) { return g.Context.Deadline() }
func (g gateCtx) Done() <-chan struct{}       { return g.Context.Done() }
func (g gateCtx) Err() error                  { return g.Context.Err() }

type gateTool struct {
	tool.Tool
	name string
}

func (g gateTool) Name() string { return g.name }

type countAsker struct{ n int }

func (a *countAsker) Ask(adk.ToolContext, string, string, string) AskOutcome {
	a.n++
	return OutcomeAllowOnce
}

func TestGateClearanceSkipsDefaultAskOnly(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"ask":["Bash(iapcli *)"],"deny":[],"defaultMode":"default"}}`)
	cl := NewClearances()
	a := &countAsker{}
	pc := PluginConfig{Source: &Static{C: cfg}, Asker: a, Clearances: cl}
	g, _ := NewGate(pc)
	ctx := func(call string) gateCtx {
		return gateCtx{Context: context.Background(), session: "s", callID: call, inv: "i1"}
	}

	cl.Grant("s", "i1", "c1", "demo_worker")
	if out, _ := g.Callback(ctx("c1"), gateTool{name: "demo_worker"}, map[string]any{}); out != nil || a.n != 0 {
		t.Errorf("cleared unmatched: out=%v asks=%d", out, a.n)
	}
	cl.Grant("s", "i1", "c2", "Bash")
	if out, _ := g.Callback(ctx("c2"), gateTool{name: "Bash"}, bash("iapcli x")); out != nil || a.n != 1 {
		t.Errorf("cleared ask rule must still ask: out=%v asks=%d", out, a.n)
	}
	cfg2 := cfgFromJSON(t, `{"permissions":{"deny":["Edit"],"defaultMode":"default"}}`)
	g2, _ := NewGate(PluginConfig{Source: &Static{C: cfg2}, Asker: a, Clearances: cl})
	cl.Grant("s", "i1", "c4", "Edit")
	if out, _ := g2.Callback(ctx("c4"), gateTool{name: "Edit"}, file("/x")); out == nil || a.n != 1 {
		t.Errorf("cleared deny must be denied: out=%v", out)
	}
}

func TestGateDefaultAllowHook(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"deny":["demo_denied"],"defaultMode":"default"}}`)
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a,
		DefaultAllow: func(_ adk.ToolContext, n string) bool { return n == "demo_worker" || n == "demo_denied" }})
	ctx := gateCtx{Context: context.Background(), session: "s", callID: "c", inv: "i1"}
	if out, _ := g.Callback(ctx, gateTool{name: "demo_worker"}, map[string]any{}); out != nil || a.n != 0 {
		t.Errorf("default allow: out=%v asks=%d", out, a.n)
	}
	if out, _ := g.Callback(ctx, gateTool{name: "demo_denied"}, map[string]any{}); out == nil {
		t.Error("deny rule must still win over DefaultAllow")
	}
}

func TestGateClearanceIsNotCached(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"defaultMode":"default"}}`)
	cl := NewClearances()
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a, Clearances: cl})
	args := map[string]any{"x": "1"}
	cl.Grant("s", "i1", "c1", "demo_worker")
	g.Callback(gateCtx{Context: context.Background(), session: "s", callID: "c1", inv: "i1"}, gateTool{name: "demo_worker"}, args)
	g.Callback(gateCtx{Context: context.Background(), session: "s", callID: "c2", inv: "i1"}, gateTool{name: "demo_worker"}, args)
	if a.n != 1 {
		t.Errorf("second call should ask once, asks=%d", a.n)
	}
}

func TestClearanceIsPerInvocation(t *testing.T) {
	c := NewClearances()
	c.Grant("s", "inv-A", "call_0", "Bash")
	if c.Take("s", "inv-B", "call_0", "Bash") {
		t.Error("clearance consumed by another invocation")
	}
	if !c.Take("s", "inv-A", "call_0", "Bash") {
		t.Error("owner lost its clearance")
	}
}

func TestClearanceExpires(t *testing.T) {
	base := time.Now()
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return base }
	c := NewClearances()
	c.Grant("s", "i", "c", "Bash")
	now = func() time.Time { return base.Add(11 * time.Minute) }
	if c.Take("s", "i", "c", "Bash") {
		t.Error("expired grant honoured")
	}
}

func TestGateAgentRuleSpecs(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"deny":["Agent(demo_denied)"],"ask":["Agent(demo_asked)"],"defaultMode":"default"}}`)
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a,
		DefaultAllow: func(adk.ToolContext, string) bool { return true }})
	ctx := gateCtx{Context: context.Background(), session: "s", callID: "c", inv: "i1"}
	if out, _ := g.Callback(ctx, gateTool{name: "demo_denied"}, map[string]any{}); out == nil {
		t.Error("deny rule must beat DefaultAllow")
	}
	g.Callback(ctx, gateTool{name: "demo_asked"}, map[string]any{})
	if a.n != 1 {
		t.Errorf("ask rule must still prompt, asks=%d", a.n)
	}
}

func TestGateClearedPlanModeStillAsks(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"defaultMode":"plan"}}`)
	cl := NewClearances()
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a, Clearances: cl})
	cl.Grant("s", "i1", "c", "mcp__s__t")
	g.Callback(gateCtx{Context: context.Background(), session: "s", callID: "c", inv: "i1"}, gateTool{name: "mcp__s__t"}, map[string]any{})
	if a.n != 1 {
		t.Errorf("plan mode must still ask, asks=%d", a.n)
	}
}

func TestGateCacheHitStillConsumesClearance(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{}}`)
	cl := NewClearances()
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a, Clearances: cl})
	ctx := gateCtx{Context: context.Background(), session: "s", callID: "c", inv: "i1"}
	args := map[string]any{"x": "1"}
	g.Callback(ctx, gateTool{name: "demo_worker"}, args) // asks, caches (allow once)
	cl.Grant("s", "i1", "c", "demo_worker")
	g.Callback(ctx, gateTool{name: "demo_worker"}, args) // cache hit
	if cl.Take("s", "i1", "c", "demo_worker") {
		t.Error("clearance survived a cache hit")
	}
}
