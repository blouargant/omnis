package agent

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/tool"
)

// slowGatherer stands in for a retrieval sub-agent: every call takes real time
// (a web fetch), and calls are independent — nothing is shared between them.
type slowGatherer struct {
	name     string
	inFlight int32
	peak     int32
	calls    int32
}

func (g *slowGatherer) Name() string        { return g.name }
func (g *slowGatherer) Description() string { return "test gatherer" }
func (g *slowGatherer) IsLongRunning() bool { return false }
func (g *slowGatherer) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{Name: g.name}
}

func (g *slowGatherer) Run(tool.Context, any) (map[string]any, error) {
	atomic.AddInt32(&g.calls, 1)
	n := atomic.AddInt32(&g.inFlight, 1)
	for {
		peak := atomic.LoadInt32(&g.peak)
		if n <= peak || atomic.CompareAndSwapInt32(&g.peak, peak, n) {
			break
		}
	}
	time.Sleep(120 * time.Millisecond)
	atomic.AddInt32(&g.inFlight, -1)
	return map[string]any{"output": "quote"}, nil
}

// ADK's Flow.handleFunctionCalls spawns ONE GOROUTINE PER FUNCTION CALL in a
// single model response (sync.WaitGroup + `go func`), so a model that emits N
// calls to the same sub-agent in one turn has them dispatched CONCURRENTLY
// against the one shared tool object in toolsDict.
//
// That is exactly what a gatherer's caller does: research_critic emitted four
// web_fetcher calls in a single response (four before_tool events at the same
// millisecond). This test pins down what the wrapper does with them, because the
// two failure modes are opposite and both are silent:
//
//   - max_instances: 1 → newNonConcurrentTool, whose mutex is a TryLock: the first
//     sibling wins and the REST ARE REJECTED ("already running"). The caller loses
//     N-1 of its retrievals and only learns about it as tool errors.
//   - max_instances: N → newParallelAgentTool, a BATCH tool ({tasks: [...]}) — which
//     the `high` model will not invoke at all (verified live: the critic silently
//     never called it and wrote "I cannot confirm this claim without fetching").
//
// So the shipped default is not a free choice: it decides whether a gatherer can
// be fanned out at all, and by whom.
func TestNonConcurrentWrapperRejectsNativeFanOut(t *testing.T) {
	g := &slowGatherer{name: "web_fetcher"}
	wrapped := wrapMust(t, g, RuntimeAgentConfig{Name: "web_fetcher", MaxInstances: 1})

	ok, rejected := fireConcurrently(t, wrapped, 4)

	if ok != 1 || rejected != 3 {
		t.Fatalf("4 native parallel calls -> ok=%d rejected=%d; want 1 ok / 3 rejected "+
			"(TryLock admits one sibling). If this changed, the gatherer fan-out story changed with it.", ok, rejected)
	}
	if peak := atomic.LoadInt32(&g.peak); peak != 1 {
		t.Fatalf("peak concurrency = %d, want 1 (the mutex serialises to a single in-flight call)", peak)
	}
	if calls := atomic.LoadInt32(&g.calls); calls != 1 {
		t.Fatalf("the gatherer actually ran %d times, want 1 — the other %d retrievals were thrown away", calls, 4-calls)
	}
}

// wrapMust builds the real mount-point wrapper (the same call the build uses).
func wrapMust(t *testing.T, inner runnableTool, cfg RuntimeAgentConfig) tool.Tool {
	t.Helper()
	// wrapSubAgentTool takes a built agent; here we exercise the wrapper layer
	// directly against a stub inner, which is the part that decides concurrency.
	if cfg.MaxInstances > 1 {
		return newParallelAgentTool(inner, cfg.MaxInstances)
	}
	return newNonConcurrentTool(inner)
}

// fireConcurrently mimics ADK: N goroutines, one shared tool object.
func fireConcurrently(t *testing.T, tl tool.Tool, n int) (ok, rejected int32) {
	t.Helper()
	runner, isRunnable := tl.(interface {
		Run(tool.Context, any) (map[string]any, error)
	})
	if !isRunnable {
		t.Fatalf("wrapper %T is not runnable", tl)
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := runner.Run(nil, map[string]any{"request": "q"}); err != nil {
				atomic.AddInt32(&rejected, 1)
			} else {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	wg.Wait()
	return ok, rejected
}
