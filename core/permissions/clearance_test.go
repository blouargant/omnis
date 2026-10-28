package permissions

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

func TestVerdictDefaultAskOnlyForUnmatchedCalls(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"ask":["Bash(iapcli *)"],"deny":["Edit"]},"defaultMode":"default"}`)
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
	c.Grant("s", "c", "Bash")
	if !c.Take("s", "c", "Bash") || c.Take("s", "c", "Bash") {
		t.Error("expected true then false")
	}
}

func TestClearanceIsPerSession(t *testing.T) {
	c := NewClearances()
	c.Grant("A", "call_0", "Bash")
	if c.Take("B", "call_0", "Bash") {
		t.Error("clearance leaked across sessions")
	}
	if !c.Take("A", "call_0", "Bash") {
		t.Error("owner lost its clearance")
	}
}

func TestNilClearancesAreInert(t *testing.T) {
	var c *Clearances
	c.Grant("s", "c", "t")
	if c.Take("s", "c", "t") {
		t.Error("nil clearances granted")
	}
}

type gateCtx struct {
	adk.ToolContext
	context.Context
	session, callID string
}

func (g gateCtx) SessionID() string           { return g.session }
func (g gateCtx) FunctionCallID() string      { return g.callID }
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
	cfg := cfgFromJSON(t, `{"permissions":{"ask":["Bash(iapcli *)"],"deny":[]},"defaultMode":"default"}`)
	cl := NewClearances()
	a := &countAsker{}
	pc := PluginConfig{Source: &Static{C: cfg}, Asker: a, Clearances: cl}
	g, _ := NewGate(pc)
	ctx := func(call string) gateCtx { return gateCtx{Context: context.Background(), session: "s", callID: call} }

	cl.Grant("s", "c1", "demo_worker")
	if out, _ := g.Callback(ctx("c1"), gateTool{name: "demo_worker"}, map[string]any{}); out != nil || a.n != 0 {
		t.Errorf("cleared unmatched: out=%v asks=%d", out, a.n)
	}
	cl.Grant("s", "c2", "Bash")
	if out, _ := g.Callback(ctx("c2"), gateTool{name: "Bash"}, bash("iapcli x")); out != nil || a.n != 1 {
		t.Errorf("cleared ask rule must still ask: out=%v asks=%d", out, a.n)
	}
	cfg2 := cfgFromJSON(t, `{"permissions":{"deny":["Edit"]},"defaultMode":"default"}`)
	g2, _ := NewGate(PluginConfig{Source: &Static{C: cfg2}, Asker: a, Clearances: cl})
	cl.Grant("s", "c4", "Edit")
	if out, _ := g2.Callback(ctx("c4"), gateTool{name: "Edit"}, file("/x")); out == nil || a.n != 1 {
		t.Errorf("cleared deny must be denied: out=%v", out)
	}
}

func TestGateDefaultAllowHook(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"deny":["demo_denied"]},"defaultMode":"default"}`)
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a,
		DefaultAllow: func(_ adk.ToolContext, n string) bool { return n == "demo_worker" || n == "demo_denied" }})
	ctx := gateCtx{Context: context.Background(), session: "s", callID: "c"}
	if out, _ := g.Callback(ctx, gateTool{name: "demo_worker"}, map[string]any{}); out != nil || a.n != 0 {
		t.Errorf("default allow: out=%v asks=%d", out, a.n)
	}
	if out, _ := g.Callback(ctx, gateTool{name: "demo_denied"}, map[string]any{}); out == nil {
		t.Error("deny rule must still win over DefaultAllow")
	}
}

func TestGateClearanceIsNotCached(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{},"defaultMode":"default"}`)
	cl := NewClearances()
	a := &countAsker{}
	g, _ := NewGate(PluginConfig{Source: &Static{C: cfg}, Asker: a, Clearances: cl})
	args := map[string]any{"x": "1"}
	cl.Grant("s", "c1", "demo_worker")
	g.Callback(gateCtx{Context: context.Background(), session: "s", callID: "c1"}, gateTool{name: "demo_worker"}, args)
	g.Callback(gateCtx{Context: context.Background(), session: "s", callID: "c2"}, gateTool{name: "demo_worker"}, args)
	if a.n != 1 {
		t.Errorf("second call should ask once, asks=%d", a.n)
	}
}
