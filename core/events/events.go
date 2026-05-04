// Package events implements the article's "Event bus & lifecycle hooks"
// (Phase 4 / s16). Every significant moment in the harness fires a named
// event; any subscriber registered via Bus.On receives the payload. The
// resulting plugin observes every model + tool call so logging, timing,
// and stats hooks live outside the agent loop.
package events

import (
	"fmt"
	"os"
	"sync"
	"time"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"
)

// Event names emitted by the bus.
//
// Lifecycle distinction:
//   - EventSessionStart / EventSessionEnd fire ONCE per real session
//     (e.g. TUI launch / quit). They are emitted by the front-end
//     entry point, not by the per-run plugin callbacks.
//   - EventRunStart / EventRunEnd fire on every Runner.Run() invocation
//     (i.e. every user turn). They are emitted from the plugin's
//     BeforeRun / AfterRun callbacks.
const (
	EventSessionStart       = "session_start"
	EventSessionEnd         = "session_end"
	EventRunStart           = "run_start"
	EventRunEnd             = "run_end"
	EventBeforeModel        = "before_model"
	EventAfterModel         = "after_model"
	EventBeforeTool         = "before_tool"
	EventAfterTool          = "after_tool"
	EventToolError          = "tool_error"
	EventCompressionStart   = "compression_start"
	EventCompressionEnd     = "compression_end"
	EventCompressionSkipped = "compression_skipped"
)

// Handler receives the event name and a free-form payload map.
type Handler func(event string, payload map[string]any)

// Bus is a tiny in-process publish/subscribe bus.
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]Handler
}

// NewBus returns an empty bus.
func NewBus() *Bus { return &Bus{handlers: map[string][]Handler{}} }

// On registers a handler for an event. Returns the bus for chaining.
func (b *Bus) On(event string, h Handler) *Bus {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[event] = append(b.handlers[event], h)
	return b
}

// Emit fires an event to all registered handlers. Errors inside a handler
// are isolated.
func (b *Bus) Emit(event string, payload map[string]any) {
	b.mu.RLock()
	hs := append([]Handler(nil), b.handlers[event]...)
	b.mu.RUnlock()
	for _, h := range hs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "[events] handler panic on %s: %v\n", event, r)
				}
			}()
			h(event, payload)
		}()
	}
}

// Plugin wires the bus into ADK as before/after model + tool callbacks plus
// a session start/end signal. Pass the resulting plugin to runner.Config.
func (b *Bus) Plugin(name string) (*plugin.Plugin, error) {
	timers := sync.Map{} // key = function-call id, value = time.Time

	beforeTool := func(_ tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		timers.Store(toolKey(t, args), time.Now())
		b.Emit(EventBeforeTool, map[string]any{
			"tool":  t.Name(),
			"input": args,
		})
		return nil, nil
	}
	afterTool := func(_ tool.Context, t tool.Tool, args, result map[string]any, _ error) (map[string]any, error) {
		var elapsed time.Duration
		if v, ok := timers.LoadAndDelete(toolKey(t, args)); ok {
			elapsed = time.Since(v.(time.Time))
		}
		b.Emit(EventAfterTool, map[string]any{
			"tool":     t.Name(),
			"input":    args,
			"output":   result,
			"duration": elapsed,
		})
		return nil, nil
	}
	onToolErr := func(_ tool.Context, t tool.Tool, args map[string]any, err error) (map[string]any, error) {
		b.Emit(EventToolError, map[string]any{
			"tool":  t.Name(),
			"input": args,
			"error": err.Error(),
		})
		return nil, nil
	}
	beforeModel := func(_ agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
		payload := map[string]any{}
		if req != nil && req.Model != "" {
			payload["model"] = req.Model
		}
		b.Emit(EventBeforeModel, payload)
		return nil, nil
	}
	afterModel := func(_ agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
		b.Emit(EventAfterModel, map[string]any{"response": resp})
		return nil, nil
	}
	beforeRun := func(ctx agent.InvocationContext) (*genai.Content, error) {
		s := ctx.Session()
		b.Emit(EventRunStart, map[string]any{
			"user_id":    s.UserID(),
			"session_id": s.ID(),
		})
		return nil, nil
	}
	afterRun := func(ctx agent.InvocationContext) {
		s := ctx.Session()
		b.Emit(EventRunEnd, map[string]any{
			"user_id":    s.UserID(),
			"session_id": s.ID(),
		})
	}
	return plugin.New(plugin.Config{
		Name:                name,
		BeforeToolCallback:  llmagent.BeforeToolCallback(beforeTool),
		AfterToolCallback:   llmagent.AfterToolCallback(afterTool),
		OnToolErrorCallback: llmagent.OnToolErrorCallback(onToolErr),
		BeforeModelCallback: llmagent.BeforeModelCallback(beforeModel),
		AfterModelCallback:  llmagent.AfterModelCallback(afterModel),
		BeforeRunCallback:   plugin.BeforeRunCallback(beforeRun),
		AfterRunCallback:    plugin.AfterRunCallback(afterRun),
	})
}

func toolKey(t tool.Tool, args map[string]any) string {
	return fmt.Sprintf("%s::%v", t.Name(), args)
}

// FileLogger writes events to a log file with timestamps. Returns a Handler
// suitable for Bus.On, plus a closer function.
func FileLogger(path string) (Handler, func() error, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	var mu sync.Mutex
	h := func(event string, payload map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		ts := time.Now().Format("15:04:05.000")
		fmt.Fprintf(f, "[%s] %s", ts, event)
		if payload != nil {
			if t, ok := payload["tool"]; ok {
				fmt.Fprintf(f, " tool=%v", t)
			}
			if d, ok := payload["duration"]; ok {
				fmt.Fprintf(f, " dur=%v", d)
			}
			if e, ok := payload["error"]; ok {
				fmt.Fprintf(f, " err=%v", e)
			}
		}
		fmt.Fprintln(f)
	}
	return h, f.Close, nil
}

// Counter tallies events of a given name and prints a summary on demand.
type Counter struct {
	mu     sync.Mutex
	counts map[string]int
}

// NewCounter returns a Counter and a Handler that increments it.
func NewCounter() (*Counter, Handler) {
	c := &Counter{counts: map[string]int{}}
	return c, func(event string, payload map[string]any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		key := event
		if event == EventAfterTool && payload != nil {
			if t, ok := payload["tool"]; ok {
				key = fmt.Sprintf("tool:%v", t)
			}
		}
		c.counts[key]++
	}
}

// Summary returns a sorted "name=count" string.
func (c *Counter) Summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := ""
	for k, v := range c.counts {
		out += fmt.Sprintf("  %s = %d\n", k, v)
	}
	return out
}
