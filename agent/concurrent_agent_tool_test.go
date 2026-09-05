package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// countingRunnableTool stands in for a sub-agent's agenttool: it records peak
// concurrency and how many times it actually ran, and echoes its request.
type countingRunnableTool struct {
	inFlight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
	hold     time.Duration
}

func (c *countingRunnableTool) Name() string        { return "investigator" }
func (c *countingRunnableTool) Description() string { return "test sub-agent" }
func (c *countingRunnableTool) IsLongRunning() bool { return false }

func (c *countingRunnableTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name: c.Name(),
		Parameters: &genai.Schema{
			Type:       genai.TypeObject,
			Properties: map[string]*genai.Schema{"request": {Type: genai.TypeString}},
			Required:   []string{"request"},
		},
	}
}

func (c *countingRunnableTool) Run(_ adk.ToolContext, args any) (map[string]any, error) {
	c.calls.Add(1)
	n := c.inFlight.Add(1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if c.hold > 0 {
		time.Sleep(c.hold)
	}
	c.inFlight.Add(-1)
	req, _ := args.(map[string]any)
	return map[string]any{"echo": req["request"]}, nil
}

// fireConcurrently mimics ADK's Flow.handleFunctionCalls: N goroutines dispatching
// against ONE shared tool object, which is exactly what happens when a model emits
// N calls to the same sub-agent in a single response.
func fireConcurrently(t *testing.T, tl tool.Tool, n int) (ok, failed int32) {
	t.Helper()
	runner, isRunnable := tl.(runnableTool)
	if !isRunnable {
		t.Fatalf("wrapper %T is not runnable", tl)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := runner.Run(nil, map[string]any{"request": "q"}); err != nil {
				atomic.AddInt32(&failed, 1)
			} else {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	return ok, failed
}

// The regression this whole wrapper exists for.
//
// ADK dispatches every function call in one model response CONCURRENTLY, so a
// caller that wants four lookups emits four calls to the same sub-agent and they
// arrive at one shared tool object at once. research_critic did exactly that: four
// web_fetcher calls, four before_tool events in the same millisecond.
//
// The old wrapper at max_instances <= 1 took a TryLock and rejected the losers with
// "already running": ONE retrieval ran and THREE were thrown away, surfacing only as
// tool errors the model had to notice and retry. Work the caller explicitly asked
// for silently disappeared.
//
// Queueing is the fix: at width 1 the four siblings serialise, and all four run.
func TestNativeFanOutIsNeverThrownAway(t *testing.T) {
	inner := &countingRunnableTool{hold: 20 * time.Millisecond}
	wrapped := newConcurrentAgentTool(inner, 1) // the DEFAULT width

	ok, failed := fireConcurrently(t, wrapped, 4)

	if ok != 4 || failed != 0 {
		t.Fatalf("4 native parallel calls -> ok=%d failed=%d; want 4 ok / 0 failed. "+
			"A rejected sibling is a retrieval the caller asked for and silently lost.", ok, failed)
	}
	if calls := inner.calls.Load(); calls != 4 {
		t.Fatalf("the sub-agent ran %d times, want 4 — %d invocations were discarded", calls, 4-calls)
	}
	if peak := inner.peak.Load(); peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1 (width 1 serialises, it does not overlap)", peak)
	}
}

// max_instances is a concurrency LIMIT, not a schema switch: the calls overlap up
// to the width and every one of them still runs.
func TestConcurrentAgentToolBoundsConcurrencyAtMaxInstances(t *testing.T) {
	inner := &countingRunnableTool{hold: 30 * time.Millisecond}
	wrapped := newConcurrentAgentTool(inner, 2)

	ok, failed := fireConcurrently(t, wrapped, 5)

	if ok != 5 || failed != 0 {
		t.Fatalf("5 calls at width 2 -> ok=%d failed=%d; want 5 ok / 0 failed (excess siblings queue)", ok, failed)
	}
	if peak := inner.peak.Load(); peak > 2 {
		t.Fatalf("peak concurrency = %d, want <= 2 (the semaphore bounds it)", peak)
	}
	if peak := inner.peak.Load(); peak < 2 {
		t.Fatalf("peak concurrency = %d, want >= 2 (width 2 should actually overlap)", peak)
	}
}

// The schema the model sees must stay the sub-agent's own single-task shape. The
// batch schema this replaced ({tasks: [...]}) was silently un-invokable by a
// non-premium caller: correctly mounted, correctly declared, never called.
func TestConcurrentAgentToolKeepsInnerSingleTaskSchema(t *testing.T) {
	for _, max := range []int{1, 8} {
		wrapped := newConcurrentAgentTool(&countingRunnableTool{}, max).(*concurrentAgentTool)
		decl := wrapped.Declaration()
		if _, isBatch := decl.Parameters.Properties["tasks"]; isBatch {
			t.Fatalf("max_instances=%d exposes a batch schema; it must expose the inner single-task schema", max)
		}
		if _, ok := decl.Parameters.Properties["request"]; !ok {
			t.Fatalf("max_instances=%d: declaration lost the inner's own parameters: %+v",
				max, decl.Parameters.Properties)
		}
	}
}

// A fan-out-capable sub-agent looks identical to a serial one from the schema alone,
// so the invitation to fan out has to be in the description or the model will never
// use the parallelism.
func TestConcurrentAgentToolAdvertisesFanOutOnlyWhenWide(t *testing.T) {
	serial := newConcurrentAgentTool(&countingRunnableTool{}, 1)
	if got := serial.Description(); got != "test sub-agent" {
		t.Fatalf("width 1 description = %q, want the inner's verbatim (no fan-out invitation)", got)
	}
	wide := newConcurrentAgentTool(&countingRunnableTool{}, 4)
	if got := wide.Description(); !strings.Contains(got, "SEVERAL TIMES IN THE SAME RESPONSE") || !strings.Contains(got, "up to 4") {
		t.Fatalf("width 4 description does not advertise parallel calls: %q", got)
	}
}

// ProcessRequest must register the WRAPPER: ADK dispatches function calls via the
// object in req.Tools, so packing the inner would call it directly and the
// semaphore would silently not exist.
func TestConcurrentAgentToolProcessRequestPacksItself(t *testing.T) {
	wrapped := newConcurrentAgentTool(&countingRunnableTool{}, 2)

	req := &model.LLMRequest{}
	if err := wrapped.(interface {
		ProcessRequest(adk.ToolContext, *model.LLMRequest) error
	}).ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest() error = %v", err)
	}
	got, ok := req.Tools["investigator"]
	if !ok {
		t.Fatalf("req.Tools missing 'investigator': %#v", req.Tools)
	}
	if got != tool.Tool(wrapped) {
		t.Fatalf("req.Tools[investigator] = %T, want the wrapper itself (the semaphore would be bypassed)", got)
	}
}

// A queued sibling waits on the semaphore. If that wait ignored the context, a
// cancelled turn (Stop button, session end, shutdown) would strand the goroutine
// for the life of the process behind an in-flight call nobody is waiting for.
func TestConcurrentAgentToolCancelledWaitDoesNotStrand(t *testing.T) {
	inner := &countingRunnableTool{hold: 2 * time.Second} // long enough that only cancellation can end the wait
	wrapped := newConcurrentAgentTool(inner, 1).(runnableTool)

	// Occupy the single slot.
	busy := make(chan struct{})
	go func() {
		defer close(busy)
		_, _ = wrapped.Run(toolCtxForSession("same-session"), map[string]any{"request": "first"})
	}()
	waitFor(t, func() bool { return inner.inFlight.Load() == 1 }, "first call to start")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := wrapped.Run(toolCtxFor(ctx, "same-session"), map[string]any{"request": "queued"})
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("queued call returned nil error after cancellation, want a cancellation error")
		}
		if !strings.Contains(err.Error(), "cancelled while waiting") {
			t.Fatalf("queued call error = %q, want a cancellation error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued call did not unblock on cancellation — the semaphore wait ignores ctx.Done()")
	}
	<-busy
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// sessionToolCtx is an adk.ToolContext carrying a real user-facing session id the
// way the surfaces plant it (WithSteerSession), which is how it reaches a
// sub-agent at all. SessionID() deliberately returns "" — for a sub-agent that
// method yields agenttool's EPHEMERAL per-call session, so a wrapper that keyed on
// it would mint a fresh semaphore per invocation and the limit would never bind.
type sessionToolCtx struct {
	adk.ToolContext
	ctx context.Context
}

func toolCtxForSession(sid string) sessionToolCtx { return toolCtxFor(context.Background(), sid) }

func toolCtxFor(ctx context.Context, sid string) sessionToolCtx {
	return sessionToolCtx{ctx: WithSteerSession(ctx, sid)}
}

func (s sessionToolCtx) Deadline() (time.Time, bool) { return s.ctx.Deadline() }
func (s sessionToolCtx) Done() <-chan struct{}       { return s.ctx.Done() }
func (s sessionToolCtx) Err() error                  { return s.ctx.Err() }
func (s sessionToolCtx) Value(k any) any             { return s.ctx.Value(k) }
func (s sessionToolCtx) SessionID() string           { return "" }

// wedgeableRunnableTool blocks the one call whose request equals wedgeOn until
// release is closed; every other call runs normally. It stands in for a sub-agent
// invocation that never returns — a k8s_validator parked on an unanswered
// permission card, which has NO timeout: askuser.DefaultTimeout is 0 and the run
// context survives a client disconnect, so only Stop/session-end ever ends it.
type wedgeableRunnableTool struct {
	countingRunnableTool
	wedgeOn string
	release chan struct{}
}

func (w *wedgeableRunnableTool) Run(ctx adk.ToolContext, args any) (map[string]any, error) {
	req, _ := args.(map[string]any)
	if s, _ := req["request"].(string); s == w.wedgeOn {
		w.inFlight.Add(1)
		defer w.inFlight.Add(-1)
		<-w.release
		return map[string]any{"echo": s}, nil
	}
	return w.countingRunnableTool.Run(ctx, args)
}

// A hung sub-agent invocation must not be able to wedge every OTHER session.
//
// The semaphore lives on the wrapper, and wrapSubAgentTool mints one wrapper per
// MOUNT POINT — built by BuildInstance, i.e. once per config generation and never
// per session. So at max_instances 1 (k8s_validator) one invocation that never
// returned held the only token for the whole server: every later session's
// Kubernetes mutation queued behind a validation belonging to somebody else's
// chat. Nothing bounded the wait — there is no deadline on inner.Run, and the
// realistic wedge (an unanswered permission card inside the validator) blocks
// until that other user's session ends.
func TestConcurrentAgentToolSemaphoreIsPerSession(t *testing.T) {
	inner := &wedgeableRunnableTool{wedgeOn: "wedged", release: make(chan struct{})}
	defer close(inner.release)
	wrapped := newConcurrentAgentTool(inner, 1).(runnableTool)

	// Session A occupies its slot and never gives it back.
	go func() {
		_, _ = wrapped.Run(toolCtxForSession("session-A"), map[string]any{"request": "wedged"})
	}()
	waitFor(t, func() bool { return inner.inFlight.Load() == 1 }, "session A's call to start")

	done := make(chan error, 1)
	go func() {
		_, err := wrapped.Run(toolCtxForSession("session-B"), map[string]any{"request": "other session"})
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("session B returned %v, want a clean run", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("session B blocked behind session A's in-flight call: one session's wedged " +
			"sub-agent freezes the same delegation for every other session on the server")
	}
}

// Within ONE session the width must still bind — otherwise the per-session fix
// would have quietly removed the limit instead of scoping it.
func TestConcurrentAgentToolStillBoundsConcurrencyWithinOneSession(t *testing.T) {
	inner := &countingRunnableTool{hold: 30 * time.Millisecond}
	wrapped := newConcurrentAgentTool(inner, 2).(runnableTool)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := wrapped.Run(toolCtxForSession("one-session"), map[string]any{"request": "q"}); err != nil {
				t.Errorf("call failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if calls := inner.calls.Load(); calls != 5 {
		t.Fatalf("sub-agent ran %d times, want 5 (queued siblings must still run)", calls)
	}
	if peak := inner.peak.Load(); peak > 2 {
		t.Fatalf("peak concurrency within one session = %d, want <= 2 — the width no longer binds", peak)
	}
}

// A generation lives until the next hot-reload, so a per-session map that is never
// pruned leaks one entry for every session the server ever serves.
func TestConcurrentAgentToolForgetsIdleSessions(t *testing.T) {
	inner := &countingRunnableTool{}
	wrapped := newConcurrentAgentTool(inner, 1).(*concurrentAgentTool)

	for i := 0; i < 5; i++ {
		if _, err := wrapped.Run(toolCtxForSession(fmt.Sprintf("session-%d", i)), map[string]any{"request": "q"}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	wrapped.mu.Lock()
	n := len(wrapped.sems)
	wrapped.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d per-session semaphores retained after every call finished, want 0", n)
	}
}
