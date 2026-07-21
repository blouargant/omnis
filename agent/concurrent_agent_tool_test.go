package agent

import (
	"context"
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

// cancelCtx is an adk.ToolContext that is only ever asked whether it is done. The
// embedded interface is nil: any other method would panic, which is the point —
// the wrapper must not touch the context for anything but cancellation.
type cancelCtx struct {
	adk.ToolContext
	ctx context.Context
}

func (c cancelCtx) Done() <-chan struct{} { return c.ctx.Done() }
func (c cancelCtx) Err() error            { return c.ctx.Err() }

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
		_, _ = wrapped.Run(nil, map[string]any{"request": "first"})
	}()
	waitFor(t, func() bool { return inner.inFlight.Load() == 1 }, "first call to start")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := wrapped.Run(cancelCtx{ctx: ctx}, map[string]any{"request": "queued"})
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
