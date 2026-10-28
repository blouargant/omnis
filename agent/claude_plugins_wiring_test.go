package agent

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/core/permissions"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/hookstate"
)

// wiringCtx is a ToolContext fake carrying what the clearance key reads: the
// session ids, the agent name, and the invocation / function-call ids.
type wiringCtx struct {
	adk.ToolContext
	ctx        context.Context
	sessionID  string
	agentName  string
	invocation string
	callID     string
}

func (c wiringCtx) Deadline() (time.Time, bool) { return c.ctx.Deadline() }
func (c wiringCtx) Done() <-chan struct{}       { return c.ctx.Done() }
func (c wiringCtx) Err() error                  { return c.ctx.Err() }
func (c wiringCtx) Value(key any) any           { return c.ctx.Value(key) }
func (c wiringCtx) SessionID() string           { return c.sessionID }
func (c wiringCtx) AgentName() string           { return c.agentName }
func (c wiringCtx) InvocationID() string        { return c.invocation }
func (c wiringCtx) FunctionCallID() string      { return c.callID }

type countingAsker struct{ n atomic.Int32 }

func (a *countingAsker) Ask(adk.ToolContext, string, string, string) permissions.AskOutcome {
	a.n.Add(1)
	return permissions.OutcomeDeny
}

// wiringRig is the hook callback + permission gate built from one resolved
// plugin runtime, exactly as buildSquadInstance chains them (hooks → gate).
type wiringRig struct {
	rt      RuntimeSettings
	hook    func(adk.ToolContext, string, map[string]any) map[string]any
	gate    func(adk.ToolContext, string, map[string]any) map[string]any
	asker   *countingAsker
	engine  *hooks.Reloader
	record  string
	reg     *askuser.Registry
	asked   atomic.Int32
	answers string
}

func newWiringRig(t *testing.T, decision, agentsJSON string, mutate func(*RuntimeSettings), extraPerms string) *wiringRig {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook exec assumes a POSIX /bin/sh")
	}
	sys, _ := pluginResolveEnv(t, agentsJSON, "")
	if extraPerms != "" {
		writeFile(t, filepath.Join(sys, "permissions.json"), extraPerms)
	}
	r := &wiringRig{record: filepath.Join(t.TempDir(), "record"), asker: &countingAsker{}, reg: askuser.NewRegistry()}
	t.Setenv("HOOK_RECORD", r.record)
	t.Setenv("HOOK_DECISION", decision)

	rt, err := ResolveRuntimeSettings(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(&rt)
	}
	r.rt = rt
	infra := &Infrastructure{Bus: events.NewBus(), HookState: hookstate.New(), HookClearances: permissions.NewClearances()}
	r.engine = infra.Hooks(rt)
	before, _ := hookToolCallbacks(r.engine, r.reg, infra.HookState, nil, infra.HookClearances, pluginAgentTypes(rt), false)
	gate, closeGate, err := buildPermissionGate(rt, r.asker, infra.Bus, infra.HookClearances)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeGate)

	r.reg.SetNotify(func(q askuser.Question) {
		r.asked.Add(1)
		go func() { _ = r.reg.Resolve(q.SessionID, q.ID, askuser.Answer{Selected: []string{choiceHookAllowOnce}}) }()
	})
	r.hook = func(tc adk.ToolContext, name string, args map[string]any) map[string]any {
		out, _ := before(tc, hookTestTool{name: name}, args)
		return out
	}
	r.gate = func(tc adk.ToolContext, name string, args map[string]any) map[string]any {
		out, _ := gate.Callback(tc, hookTestTool{name: name}, args)
		return out
	}
	return r
}

func (r *wiringRig) records(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(r.record)
	return string(b)
}

// call chains hook then gate; a non-nil result is the short-circuit output.
func (r *wiringRig) call(tc adk.ToolContext, name string, args map[string]any) map[string]any {
	if out := r.hook(tc, name, args); out != nil {
		return out
	}
	return r.gate(tc, name, args)
}

func wtc(agent string) wiringCtx {
	return wiringCtx{ctx: context.Background(), sessionID: "s1", agentName: agent, invocation: "inv1", callID: "call1"}
}

var bashArgs = map[string]any{"command": "make deploy"}

func TestPluginHookAllowSkipsDefaultPermissionAsk(t *testing.T) {
	r := newWiringRig(t, "allow", "", nil, "")
	if out := r.call(wtc("demo_worker"), "Bash", bashArgs); out != nil {
		t.Fatalf("call blocked: %v", out)
	}
	if !strings.Contains(r.records(t), `"tool_name":"Bash"`) {
		t.Fatalf("hook did not run: %q", r.records(t))
	}
	if n := r.asker.n.Load(); n != 0 {
		t.Fatalf("gate asked %d times, want 0", n)
	}
}

func TestPluginHookAskApprovedSkipsSecondCard(t *testing.T) {
	r := newWiringRig(t, "ask", "", nil, "")
	if out := r.call(wtc("demo_worker"), "Bash", bashArgs); out != nil {
		t.Fatalf("call blocked: %v", out)
	}
	if n := r.asked.Load(); n != 1 {
		t.Fatalf("hook escalation asked %d times, want 1", n)
	}
	if n := r.asker.n.Load(); n != 0 {
		t.Fatalf("gate asked %d times after an approved escalation, want 0", n)
	}
}

func TestPluginHookAllowDoesNotBeatAskRule(t *testing.T) {
	r := newWiringRig(t, "allow", "", nil, `{"permissions":{"ask":["Bash(make *)"]}}`)
	r.call(wtc("demo_worker"), "Bash", bashArgs)
	if n := r.asker.n.Load(); n != 1 {
		t.Fatalf("gate asked %d times, want 1 (an explicit ask rule outranks a hook allow)", n)
	}
}

func TestMissingGuardLeavesDefaultAsk(t *testing.T) {
	dir := t.TempDir()
	hp := filepath.Join(dir, "hooks.json")
	writeFile(t, hp, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"/nonexistent/guard"}]}]}}`)
	r := newWiringRig(t, "", "", func(rt *RuntimeSettings) {
		rt.PluginHookLayers = []hooks.PluginLayer{{Name: "demo", Root: dir, Path: hp}}
	}, "")
	if out := r.call(wtc("demo_worker"), "Bash", bashArgs); out != nil && strings.Contains(out["output"].(string), "BLOCKED BY HOOK") {
		t.Fatalf("a missing plugin guard blocked the call: %v", out)
	}
	if n := r.asker.n.Load(); n != 1 {
		t.Fatalf("gate asked %d times, want 1", n)
	}
}

func TestHookInputCarriesAgentType(t *testing.T) {
	r := newWiringRig(t, "", "", nil, "")
	r.hook(wtc("demo_worker"), "Bash", bashArgs)
	if rec := r.records(t); !strings.Contains(rec, `"agent_type":"demo:worker"`) {
		t.Fatalf("no agent_type for a plugin agent: %q", rec)
	}
	os.Remove(r.record)
	r.hook(wtc("coder"), "Bash", map[string]any{"command": "make other"})
	if rec := r.records(t); !strings.Contains(rec, `"tool_name":"Bash"`) || strings.Contains(rec, "agent_type") {
		t.Fatalf("native agent record: %q", rec)
	}
}

func TestSamePluginDelegationIsDefaultAllowed(t *testing.T) {
	r := newWiringRig(t, "", "", func(rt *RuntimeSettings) {
		rt.Agents = append(rt.Agents, RuntimeAgentConfig{Name: "investigator"})
	}, "")
	f := samePluginDelegation(r.rt)
	if !f(wtc("demo_lead"), "demo_worker") {
		t.Error("demo_lead -> demo_worker should be allowed")
	}
	if f(wtc("demo_lead"), "investigator") {
		t.Error("demo_lead -> investigator must not be allowed")
	}
	if f(wtc("leader"), "demo_worker") {
		t.Error("leader -> demo_worker must not be allowed")
	}
}

func TestDisabledPluginHasNoHooks(t *testing.T) {
	r := newWiringRig(t, "", `{"agents": ["leader"], "router_squad": "none", "disabled_plugins": ["demo"]}`, nil, "")
	if n := len(r.engine.Snapshot().Match(hooks.PreToolUse, "Bash")); n != 0 {
		t.Fatalf("disabled plugin still has %d hook commands", n)
	}
}

func TestClearanceMatchesUnderRootSessionOnly(t *testing.T) {
	r := newWiringRig(t, "allow", "", nil, "")
	tc := wtc("demo_worker")
	tc.sessionID = "ephemeral"
	tc.ctx = events.WithRootSession(context.Background(), "real")
	if out := r.call(tc, "Bash", bashArgs); out != nil {
		t.Fatalf("call blocked: %v", out)
	}
	if n := r.asker.n.Load(); n != 0 {
		t.Fatalf("gate asked %d times, want 0 (grant and take must share the root session id)", n)
	}
}
