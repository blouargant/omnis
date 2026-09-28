package events

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

// fakeToolCtx satisfies adk.ToolContext for the methods the bus callbacks
// read; anything else panics on the nil embedded interface.
type fakeToolCtx struct {
	adk.ToolContext
	context.Context
	agent, session, callID string
}

func (f fakeToolCtx) AgentName() string           { return f.agent }
func (f fakeToolCtx) SessionID() string           { return f.session }
func (f fakeToolCtx) FunctionCallID() string      { return f.callID }
func (f fakeToolCtx) UserID() string              { return "u" }
func (f fakeToolCtx) InvocationID() string        { return "run-" + f.session }
func (f fakeToolCtx) Value(k any) any             { return f.Context.Value(k) }
func (f fakeToolCtx) Deadline() (time.Time, bool) { return f.Context.Deadline() }
func (f fakeToolCtx) Done() <-chan struct{}       { return f.Context.Done() }
func (f fakeToolCtx) Err() error                  { return f.Context.Err() }

type namedTool struct{ tool.Tool }

func (namedTool) Name() string { return "web_agent" }

// Two sessions making the SAME call (same agent, tool and args) must each
// report their own duration. Keying the timer by (agent, tool, args) let the
// second call overwrite the first one's start time: the first to finish
// reported the second's elapsed time and the other reported 0.
func TestToolTimerIsPerCall(t *testing.T) {
	b := NewBus()
	durs := map[string]time.Duration{}
	b.Subscribe(EventAfterTool, func(_ string, p map[string]any) {
		durs[p["call_id"].(string)] = p["duration"].(time.Duration)
	})
	cb := b.AgentCallbacks(PluginOptions{})
	args := map[string]any{"request": "how does parsec work"}
	a := fakeToolCtx{Context: context.Background(), agent: "knowledge_leader", session: "gentle-duckling", callID: "call-a"}
	c := fakeToolCtx{Context: context.Background(), agent: "knowledge_leader", session: "social-moth", callID: "call-b"}
	tl := namedTool{}

	cb.BeforeTool(a, tl, args)
	time.Sleep(60 * time.Millisecond)
	cb.BeforeTool(c, tl, args)
	cb.AfterTool(a, tl, args, nil, nil)
	cb.AfterTool(c, tl, args, nil, nil)

	if durs["call-a"] < 60*time.Millisecond {
		t.Errorf("call-a duration = %v, want >= 60ms (its own start time)", durs["call-a"])
	}
	if durs["call-b"] <= 0 {
		t.Errorf("call-b duration = %v, want > 0 (its timer was consumed by call-a)", durs["call-b"])
	}
}

// ADK v2 builds a fresh callback context for every callback
// (NewCallbackContextWithDelta), so the before and after halves of one model
// call see different context objects. Keying the timer by the context's
// address never matched, and every after_model reported 0.
func TestModelTimerSurvivesFreshCallbackContexts(t *testing.T) {
	b := NewBus()
	var got time.Duration
	b.Subscribe(EventAfterModel, func(_ string, p map[string]any) {
		got = p["duration"].(time.Duration)
	})
	cb := b.AgentCallbacks(PluginOptions{})
	before := &fakeToolCtx{Context: context.Background(), agent: "knowledge_leader", session: "gentle-duckling"}
	after := &fakeToolCtx{Context: context.Background(), agent: "knowledge_leader", session: "gentle-duckling"}

	cb.BeforeModel(before, &model.LLMRequest{})
	time.Sleep(40 * time.Millisecond)
	cb.AfterModel(after, &model.LLMResponse{}, nil)

	if got < 40*time.Millisecond {
		t.Errorf("after_model duration = %v, want >= 40ms", got)
	}
}
