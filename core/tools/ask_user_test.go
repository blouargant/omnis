package tools

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"

	"github.com/blouargant/omnis/internal/askuser"
)

// fakeToolCtx is a minimal adk.ToolContext: Done/Err come from Ctx, the
// methods the handler uses are overridden, everything else panics.
type fakeToolCtx struct {
	agent.StrictContextMock
	sid, agentName string
}

func (f *fakeToolCtx) SessionID() string { return f.sid }
func (f *fakeToolCtx) AgentName() string { return f.agentName }

func TestAskUserQuestionIsDurableAndNamesTheAgent(t *testing.T) {
	reg := askuser.NewRegistry()
	h := askUserHandler(reg)
	tc := &fakeToolCtx{StrictContextMock: agent.StrictContextMock{Ctx: context.Background()}, sid: "s1", agentName: "leader"}

	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if qs := reg.Pending("s1"); len(qs) == 1 {
				if !qs[0].Durable || qs[0].Agent != "leader" {
					t.Errorf("question must be durable and name the agent, got %+v", qs[0])
				}
				_ = reg.Resolve("s1", qs[0].ID, askuser.Answer{Text: "ok"})
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	out, err := h(tc, askUserIn{Kind: "text", Prompt: "p"})
	if err != nil || out.Text != "ok" {
		t.Fatalf("got %+v, %v", out, err)
	}
}

// Regression: the tool used to wait on context.Background(), so Stop (which
// cancels the run context) left the turn blocked until the user answered.
func TestAskUserQuestionReleasedByRunContextCancel(t *testing.T) {
	reg := askuser.NewRegistry()
	h := askUserHandler(reg)
	ctx, cancel := context.WithCancel(context.Background())
	tc := &fakeToolCtx{StrictContextMock: agent.StrictContextMock{Ctx: ctx}, sid: "s1"}

	done := make(chan askUserOut, 1)
	go func() {
		out, _ := h(tc, askUserIn{Kind: "text", Prompt: "p"})
		done <- out
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case out := <-done:
		if !out.Cancelled {
			t.Fatalf("cancel must return Cancelled, got %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AskUserQuestion is still blocked after the run context was cancelled")
	}
}
