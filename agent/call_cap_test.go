package agent

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

type capCtx struct {
	adk.ToolContext
	inv     string
	actions *session.EventActions
}

func (c capCtx) Deadline() (time.Time, bool)    { return time.Time{}, false }
func (c capCtx) Done() <-chan struct{}          { return nil }
func (c capCtx) Err() error                     { return nil }
func (c capCtx) Value(any) any                  { return nil }
func (c capCtx) InvocationID() string           { return c.inv }
func (c capCtx) Actions() *session.EventActions { return c.actions }

type namedTool struct {
	tool.Tool
	name string
}

func (n namedTool) Name() string { return n.name }

func newCapCtx(inv string) capCtx { return capCtx{inv: inv, actions: &session.EventActions{}} }

// The cap counts one invocation: a sibling call of the same agent (a parallel
// fan-out instance, a new task) starts from zero — the property the per-turn
// max_tool_calls lacks.
func TestPerCallCapCountsOneInvocation(t *testing.T) {
	cb := perCallCapCallback("web_agent", 3)
	fetch := namedTool{name: "WebFetch"}
	a := newCapCtx("inv-a")
	for i := 1; i <= 3; i++ {
		if out, _ := cb(a, fetch, nil); out != nil {
			t.Fatalf("call %d under the cap was blocked", i)
		}
	}
	out, _ := cb(a, fetch, nil)
	if out == nil || !strings.Contains(out["output"].(string), "CALL BUDGET REACHED") {
		t.Fatalf("call 4 should be refused with the notice, got %v", out)
	}
	if out, _ := cb(newCapCtx("inv-b"), fetch, nil); out != nil {
		t.Error("another invocation of the same agent was charged for the first one's calls")
	}
}

// A model that ignores the notice gets its flow loop ended after the grace calls.
func TestPerCallCapEndsTheLoopAfterGrace(t *testing.T) {
	cb := perCallCapCallback("web_agent", 1)
	c := newCapCtx("inv")
	fetch := namedTool{name: "WebFetch"}
	cb(c, fetch, nil) // within the cap
	for i := 1; i <= agentCapGraceCalls; i++ {
		cb(c, fetch, nil)
		if c.actions.SkipSummarization {
			t.Fatalf("loop ended after %d over-cap calls; grace is %d", i, agentCapGraceCalls)
		}
	}
	cb(c, fetch, nil)
	if !c.actions.SkipSummarization {
		t.Error("loop not ended past the grace calls")
	}
}

func TestPerCallCapSkipsBookkeepingAndIsOffAtZero(t *testing.T) {
	if perCallCapCallback("x", 0) != nil {
		t.Error("a zero cap must be a no-op (nil callback)")
	}
	cb := perCallCapCallback("web_agent", 1)
	c := newCapCtx("inv")
	for _, n := range []string{"list_skills", "load_softskill", "todo_write", "list_softskills"} {
		if out, _ := cb(c, namedTool{name: n}, nil); out != nil {
			t.Errorf("%s was counted", n)
		}
	}
	if out, _ := cb(c, namedTool{name: "WebSearch"}, nil); out != nil {
		t.Error("the first real call was refused: bookkeeping tools were charged")
	}
}

func TestChainBeforeToolStopsAtFirstRefusal(t *testing.T) {
	ran := false
	second := func(adk.ToolContext, tool.Tool, map[string]any) (map[string]any, error) {
		ran = true
		return nil, nil
	}
	refuse := func(adk.ToolContext, tool.Tool, map[string]any) (map[string]any, error) {
		return map[string]any{"output": "no"}, nil
	}
	chainBeforeTool(refuse, second)(newCapCtx("i"), namedTool{name: "WebFetch"}, nil)
	if ran {
		t.Error("the turn budget was charged for a call the per-call cap refused")
	}
	if chainBeforeTool(nil, nil) != nil {
		t.Error("two nil callbacks must chain to nil")
	}
}

// The sub-agent is told once, before the cap, that its budget is nearly spent,
// so it writes up its result instead of burning the refused grace calls.
func TestPerCallCapWarnsOnceBeforeTheCap(t *testing.T) {
	before, warn := perCallCapCallbacks("web_agent", 5)
	c := newCapCtx("inv")
	fetch := namedTool{name: "WebFetch"}
	var notes []int
	for i := 1; i <= 5; i++ {
		before(c, fetch, nil)
		out, _ := warn(c, fetch, nil, map[string]any{"output": "page"}, nil)
		if out != nil {
			if out["output"] != "page" {
				t.Fatal("the warning dropped the tool's own result")
			}
			if !strings.Contains(out["call_budget"].(string), "left for this task") {
				t.Fatalf("unexpected note %v", out["call_budget"])
			}
			notes = append(notes, i)
		}
	}
	if len(notes) != 1 || notes[0] != 2 {
		t.Errorf("warned after calls %v, want once after call 2 (3 left of 5)", notes)
	}
	if out, _ := warn(newCapCtx("other"), fetch, nil, map[string]any{"output": "x"}, nil); out != nil {
		t.Error("an invocation with no counted calls was warned")
	}
	if b, w := perCallCapCallbacks("x", 0); b != nil || w != nil {
		t.Error("a zero cap must yield no callbacks")
	}
}

func TestChainAfterToolFeedsTheRewrittenResult(t *testing.T) {
	shape := func(_ adk.ToolContext, _ tool.Tool, _, r map[string]any, _ error) (map[string]any, error) {
		return map[string]any{"output": "short"}, nil
	}
	note := func(_ adk.ToolContext, _ tool.Tool, _, r map[string]any, _ error) (map[string]any, error) {
		out := map[string]any{"note": "n"}
		for k, v := range r {
			out[k] = v
		}
		return out, nil
	}
	noop := func(adk.ToolContext, tool.Tool, map[string]any, map[string]any, error) (map[string]any, error) {
		return nil, nil
	}
	ctx, tl := newCapCtx("i"), namedTool{name: "WebFetch"}
	out, _ := chainAfterTool(shape, note)(ctx, tl, nil, map[string]any{"output": "long"}, nil)
	if out["output"] != "short" || out["note"] != "n" {
		t.Errorf("second callback did not see the shaped result: %v", out)
	}
	out, _ = chainAfterTool(shape, noop)(ctx, tl, nil, map[string]any{"output": "long"}, nil)
	if out["output"] != "short" {
		t.Errorf("the first callback's rewrite was lost: %v", out)
	}
	if out, _ := chainAfterTool(noop, noop)(ctx, tl, nil, map[string]any{"output": "x"}, nil); out != nil {
		t.Error("two no-op callbacks must leave the result alone")
	}
}
