package permissions

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blouargant/omnis/core/adk"
)

type scriptAsker struct {
	n       int
	outcome AskOutcome
}

func (a *scriptAsker) Ask(adk.ToolContext, string, string, string) AskOutcome {
	a.n++
	return a.outcome
}

type modeFixture struct {
	g      *Gate
	modes  *ModeStore
	asker  *scriptAsker
	judged *int
	nonInt *bool
	call   int
}

func newModeFixture(t *testing.T, cfgJSON string, judge AutoJudge) *modeFixture {
	t.Helper()
	cfg := cfgFromJSON(t, cfgJSON)
	src := &Static{C: cfg}
	f := &modeFixture{modes: NewModeStore(), asker: &scriptAsker{outcome: OutcomeAllowOnce}}
	f.modes.SetConfigSource(src)
	ni := false
	f.nonInt = &ni
	n := 0
	f.judged = &n
	var j AutoJudge
	if judge != nil {
		j = func(tc adk.ToolContext, tool string, args map[string]any, cwd, why string) (AutoVerdict, error) {
			n++
			return judge(tc, tool, args, cwd, why)
		}
	}
	g, err := NewGate(PluginConfig{Source: src, Asker: f.asker, Modes: f.modes, AutoJudge: j,
		NonInteractive: func() bool { return ni }})
	if err != nil {
		t.Fatal(err)
	}
	f.g = g
	return f
}

func (f *modeFixture) run(tool string, args map[string]any) map[string]any {
	f.call++
	ctx := gateCtx{Context: context.Background(), session: "s", callID: string(rune('a' + f.call)), inv: "i"}
	out, _ := f.g.Callback(ctx, gateTool{name: tool}, args)
	return out
}

func outStr(m map[string]any) string {
	if m == nil {
		return ""
	}
	s, _ := m["output"].(string)
	return s
}

func TestGateUsesSessionOverride(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{"defaultMode":"default"}}`, nil)
	f.modes.Set("s", ModeDontAsk)
	out := f.run("Bash", bash("make deploy"))
	if !strings.HasPrefix(outStr(out), "[DENIED]") || f.asker.n != 0 {
		t.Errorf("dontAsk: out=%v asks=%d", out, f.asker.n)
	}
	f.modes.Set("s", ModeBypass)
	if out := f.run("Bash", bash("make deploy2")); out != nil || f.asker.n != 0 {
		t.Errorf("bypass: out=%v asks=%d", out, f.asker.n)
	}
}

func TestGatePlanIgnoresToolGrant(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{"defaultMode":"default"}}`, nil)
	f.asker.outcome = OutcomeAllowToolSession
	if out := f.run("Edit", file("/x/a.go")); out != nil || f.asker.n != 1 {
		t.Fatalf("first: out=%v asks=%d", out, f.asker.n)
	}
	if out := f.run("Edit", file("/x/b.go")); out != nil || f.asker.n != 1 {
		t.Fatalf("grant should allow in default: out=%v asks=%d", out, f.asker.n)
	}
	f.modes.Set("s", ModePlan)
	if got := outStr(f.run("Edit", file("/x/c.go"))); got != "[DENIED] Edit: plan mode forbids edits" {
		t.Errorf("plan: %q", got)
	}
}

func TestGateAutoAllow(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{}}`, func(adk.ToolContext, string, map[string]any, string, string) (AutoVerdict, error) {
		return AutoVerdict{Allow: true}, nil
	})
	f.modes.Set("s", ModeAuto)
	if out := f.run("Bash", bash("make x")); out != nil || f.asker.n != 0 || *f.judged != 1 {
		t.Errorf("out=%v asks=%d judged=%d", out, f.asker.n, *f.judged)
	}
}

func blockJudge(adk.ToolContext, string, map[string]any, string, string) (AutoVerdict, error) {
	return AutoVerdict{Reason: "deletes outside the workdir"}, nil
}

func TestGateAutoBlock(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{}}`, blockJudge)
	f.modes.Set("s", ModeAuto)
	got := outStr(f.run("Bash", bash("rm -rf x")))
	if got != AutoBlockMessage("Bash", "deletes outside the workdir") {
		t.Errorf("got %q", got)
	}
}

func TestGateAutoErrorAsks(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{}}`, func(adk.ToolContext, string, map[string]any, string, string) (AutoVerdict, error) {
		return AutoVerdict{}, errors.New("boom")
	})
	f.modes.Set("s", ModeAuto)
	if out := f.run("Bash", bash("make a")); out != nil || f.asker.n != 1 {
		t.Errorf("interactive: out=%v asks=%d", out, f.asker.n)
	}
	*f.nonInt = true
	got := outStr(f.run("Bash", bash("make b")))
	if got != "[DENIED] Bash: auto mode could not judge this call" || f.asker.n != 1 {
		t.Errorf("non-interactive: %q asks=%d", got, f.asker.n)
	}
}

func TestGateAutoEscalates(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{}}`, blockJudge)
	f.modes.Set("s", ModeAuto)
	for i := 0; i < 3; i++ {
		if !strings.HasPrefix(outStr(f.run("Bash", bash("rm "+string(rune('a'+i))))), "[BLOCKED BY AUTO MODE]") {
			t.Fatalf("call %d not blocked", i)
		}
	}
	if out := f.run("Bash", bash("rm d")); out != nil || f.asker.n != 1 || *f.judged != 3 {
		t.Fatalf("4th: out=%v asks=%d judged=%d", out, f.asker.n, *f.judged)
	}
	// the answer reset the counters: the 5th call is judged again
	if got := outStr(f.run("Bash", bash("rm e"))); !strings.HasPrefix(got, "[BLOCKED BY AUTO MODE]") || *f.judged != 4 {
		t.Errorf("5th: %q judged=%d", got, *f.judged)
	}
}

func TestGateAutoEscalatedNonInteractiveStillJudges(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{}}`, blockJudge)
	f.modes.Set("s", ModeAuto)
	*f.nonInt = true
	for i := 0; i < 4; i++ {
		got := outStr(f.run("Bash", bash("rm "+string(rune('a'+i)))))
		if !strings.HasPrefix(got, "[BLOCKED BY AUTO MODE]") {
			t.Fatalf("call %d: %q", i, got)
		}
	}
	if f.asker.n != 0 || *f.judged != 4 {
		t.Errorf("asks=%d judged=%d", f.asker.n, *f.judged)
	}
}

func TestGateAutoNeverOverridesAskRule(t *testing.T) {
	f := newModeFixture(t, `{"permissions":{"ask":["Bash(iapcli *)"]}}`, blockJudge)
	f.modes.Set("s", ModeAuto)
	if out := f.run("Bash", bash("iapcli x")); out != nil || f.asker.n != 1 || *f.judged != 0 {
		t.Errorf("out=%v asks=%d judged=%d", out, f.asker.n, *f.judged)
	}
}

func TestGateClearanceBeforeJudge(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{}}`)
	src := &Static{C: cfg}
	modes := NewModeStore()
	modes.SetConfigSource(src)
	modes.Set("s", ModeAuto)
	cl := NewClearances()
	judged := 0
	g, _ := NewGate(PluginConfig{Source: src, Asker: &scriptAsker{}, Modes: modes, Clearances: cl,
		AutoJudge: func(adk.ToolContext, string, map[string]any, string, string) (AutoVerdict, error) {
			judged++
			return AutoVerdict{}, nil
		}})
	cl.Grant("s", "i", "c", "demo_worker")
	out, _ := g.Callback(gateCtx{Context: context.Background(), session: "s", callID: "c", inv: "i"}, gateTool{name: "demo_worker"}, map[string]any{})
	if out != nil || judged != 0 {
		t.Errorf("out=%v judged=%d", out, judged)
	}
}
