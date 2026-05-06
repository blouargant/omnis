// Package events implements the article's "Event bus & lifecycle hooks"
// (Phase 4 / s16). Every significant moment in the harness fires a named
// event; any subscriber registered via Bus.On receives the payload. The
// resulting plugin observes every model + tool call so logging, timing,
// and stats hooks live outside the agent loop.
package events

import (
	"encoding/json"
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
//   - EventCurateNow is a manual trigger for immediate soft-skill
//     curation during an active session (for example, TUI /learn-now).
const (
	EventSessionStart       = "session_start"
	EventSessionEnd         = "session_end"
	EventRunStart           = "run_start"
	EventRunEnd             = "run_end"
	EventCurateNow          = "curate_now"
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

// PluginOptions controls how much data the event plugin emits.
type PluginOptions struct {
	// IncludeModelRequest emits the full model request on before_model events.
	IncludeModelRequest bool
}

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
	return b.PluginWithOptions(name, PluginOptions{})
}

// AgentCallbacks bundles the per-agent (tool + model) callbacks that publish
// to the bus. They can be attached directly to an llmagent.Config so that
// agents executed by their own internal runner (e.g. sub-agents wrapped by
// agenttool, which spins up a private runner without plugins) still emit
// events to the same bus.
type AgentCallbacks struct {
	BeforeTool  llmagent.BeforeToolCallback
	AfterTool   llmagent.AfterToolCallback
	OnToolError llmagent.OnToolErrorCallback
	BeforeModel llmagent.BeforeModelCallback
	AfterModel  llmagent.AfterModelCallback
}

// AgentCallbacks returns the per-agent (tool + model) callbacks bound to this
// bus. Use it to attach event emission directly on an agent (typically a
// sub-agent) when its execution does not flow through a runner that has the
// bus's plugin registered.
func (b *Bus) AgentCallbacks(opts PluginOptions) AgentCallbacks {
	toolTimers := sync.Map{}  // key = (agent, function-call id), value = time.Time
	modelTimers := sync.Map{} // key = (agent, callback-context ptr), value = time.Time

	beforeTool := func(tctx tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		agentName := tctx.AgentName()
		toolTimers.Store(scopedToolKey(agentName, t, args), time.Now())
		b.Emit(EventBeforeTool, map[string]any{
			"agent": agentName,
			"tool":  t.Name(),
			"input": args,
		})
		return nil, nil
	}
	afterTool := func(tctx tool.Context, t tool.Tool, args, result map[string]any, _ error) (map[string]any, error) {
		agentName := tctx.AgentName()
		var elapsed time.Duration
		if v, ok := toolTimers.LoadAndDelete(scopedToolKey(agentName, t, args)); ok {
			elapsed = time.Since(v.(time.Time))
		}
		b.Emit(EventAfterTool, map[string]any{
			"agent":    agentName,
			"tool":     t.Name(),
			"input":    args,
			"output":   result,
			"duration": elapsed,
		})
		return nil, nil
	}
	onToolErr := func(tctx tool.Context, t tool.Tool, args map[string]any, err error) (map[string]any, error) {
		agentName := tctx.AgentName()
		toolTimers.Delete(scopedToolKey(agentName, t, args))
		b.Emit(EventToolError, map[string]any{
			"agent": agentName,
			"tool":  t.Name(),
			"input": args,
			"error": err.Error(),
		})
		return nil, nil
	}
	beforeModel := func(cb agent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
		agentName := cb.AgentName()
		modelTimers.Store(modelKey(agentName, cb), time.Now())
		payload := map[string]any{"agent": agentName}
		if req != nil && req.Model != "" {
			payload["model"] = req.Model
		}
		if opts.IncludeModelRequest && req != nil {
			payload["request"] = req
		}
		b.Emit(EventBeforeModel, payload)
		return nil, nil
	}
	afterModel := func(cb agent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
		// Skip partial streaming chunks: ADK invokes the AfterModel
		// callback for every delta yielded by the LLM adapter. Emitting
		// one event per chunk floods subscribers (TUI, event logs) with
		// dozens of zero-duration / zero-token entries per turn. We only
		// surface the final aggregated response (Partial=false), which
		// also carries usage metadata.
		if resp != nil && resp.Partial {
			return nil, nil
		}
		agentName := cb.AgentName()
		var elapsed time.Duration
		if v, ok := modelTimers.LoadAndDelete(modelKey(agentName, cb)); ok {
			elapsed = time.Since(v.(time.Time))
		}
		payload := map[string]any{
			"agent":    agentName,
			"response": resp,
			"duration": elapsed,
		}
		if resp != nil && resp.UsageMetadata != nil {
			u := resp.UsageMetadata
			payload["usage"] = map[string]any{
				"prompt_tokens":     int64(u.PromptTokenCount),
				"candidates_tokens": int64(u.CandidatesTokenCount),
				"total_tokens":      int64(u.TotalTokenCount),
			}
		}
		b.Emit(EventAfterModel, payload)
		return nil, nil
	}

	return AgentCallbacks{
		BeforeTool:  llmagent.BeforeToolCallback(beforeTool),
		AfterTool:   llmagent.AfterToolCallback(afterTool),
		OnToolError: llmagent.OnToolErrorCallback(onToolErr),
		BeforeModel: llmagent.BeforeModelCallback(beforeModel),
		AfterModel:  llmagent.AfterModelCallback(afterModel),
	}
}

// PluginWithOptions wires the bus into ADK with configurable event payloads.
//
// Every emitted payload includes an "agent" key holding the name of the ADK
// agent currently executing (lead or sub-agent). Subscribers can use it to
// route events per agent — e.g. the TUI indents sub-agent events under their
// parent.
//
// Note: ADK plugins only fire for agents executed by the runner that owns
// the plugin. Sub-agents wrapped via agenttool spawn their own internal
// runner, so their events do not flow through this plugin. Attach
// AgentCallbacks directly to those sub-agents to capture their activity.
func (b *Bus) PluginWithOptions(name string, opts PluginOptions) (*plugin.Plugin, error) {
	cb := b.AgentCallbacks(opts)
	beforeRun := func(ctx agent.InvocationContext) (*genai.Content, error) {
		s := ctx.Session()
		b.Emit(EventRunStart, map[string]any{
			"agent":      agentNameOf(ctx),
			"user_id":    s.UserID(),
			"session_id": s.ID(),
		})
		return nil, nil
	}
	afterRun := func(ctx agent.InvocationContext) {
		s := ctx.Session()
		b.Emit(EventRunEnd, map[string]any{
			"agent":      agentNameOf(ctx),
			"user_id":    s.UserID(),
			"session_id": s.ID(),
		})
	}
	return plugin.New(plugin.Config{
		Name:                name,
		BeforeToolCallback:  cb.BeforeTool,
		AfterToolCallback:   cb.AfterTool,
		OnToolErrorCallback: cb.OnToolError,
		BeforeModelCallback: cb.BeforeModel,
		AfterModelCallback:  cb.AfterModel,
		BeforeRunCallback:   plugin.BeforeRunCallback(beforeRun),
		AfterRunCallback:    plugin.AfterRunCallback(afterRun),
	})
}

func toolKey(t tool.Tool, args map[string]any) string {
	return fmt.Sprintf("%s::%v", t.Name(), args)
}

// scopedToolKey is toolKey() namespaced by agent so concurrent sub-agent
// invocations of the same tool don't collide on the timer map.
func scopedToolKey(agentName string, t tool.Tool, args map[string]any) string {
	return agentName + "||" + toolKey(t, args)
}

// modelKey identifies an in-flight LLM call. ADK's CallbackContext does not
// expose an invocation ID directly through a stable interface here, so we
// scope by agent + identity of the callback context (its address). This is
// safe because before/after callbacks for a given call run on the same
// goroutine with the same context value.
func modelKey(agentName string, cb agent.CallbackContext) string {
	return fmt.Sprintf("%s||%p", agentName, cb)
}

// agentNameOf returns the running agent's name from an InvocationContext,
// or an empty string if unavailable.
func agentNameOf(ctx agent.InvocationContext) string {
	if a := ctx.Agent(); a != nil {
		return a.Name()
	}
	return ""
}

// FileLoggerOptions controls the event file logger output format.
type FileLoggerOptions struct {
	// FullPayload writes JSONL event records with the complete payload.
	FullPayload bool
}

// FileLogger writes events to a log file with timestamps. Returns a Handler
// suitable for Bus.On, plus a closer function.
func FileLogger(path string) (Handler, func() error, error) {
	return FileLoggerWithOptions(path, FileLoggerOptions{})
}

// FileLoggerWithOptions writes events to a log file with configurable payload detail.
func FileLoggerWithOptions(path string, opts FileLoggerOptions) (Handler, func() error, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	var mu sync.Mutex
	h := func(event string, payload map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		if opts.FullPayload {
			writeFullPayloadEvent(f, event, payload)
			return
		}
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

func writeFullPayloadEvent(f *os.File, event string, payload map[string]any) {
	record := map[string]any{
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"event":     event,
		"payload":   payload,
	}
	data, err := json.Marshal(record)
	if err != nil {
		record["payload"] = fmt.Sprintf("%+v", payload)
		record["payload_error"] = err.Error()
		data, err = json.Marshal(record)
		if err != nil {
			fmt.Fprintf(f, `{"timestamp":%q,"event":%q,"payload_error":%q}`+"\n", time.Now().Format(time.RFC3339Nano), event, err.Error())
			return
		}
	}
	f.Write(append(data, '\n'))
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
