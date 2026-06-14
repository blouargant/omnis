package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"

	"github.com/blouargant/yoke/core/events"
	fstools "github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/hooks"
	"github.com/blouargant/yoke/internal/paths"
)

// hookDefaultTimeout caps a single hook command when its entry sets no timeout.
const hookDefaultTimeout = 60 * time.Second

// hooksCache memoises the process-wide hooks engine on Infrastructure.
type hooksCache struct {
	once     sync.Once
	reloader *hooks.Reloader
}

// Hooks lazily builds the process-wide hooks engine and wires the fire-and-forget
// lifecycle listeners onto the bus exactly once. It returns the Reloader whose
// live Snapshot the per-squad runner plugin queries for PreToolUse / PostToolUse
// / UserPromptSubmit / Stop, so a hooks.json edit hot-reloads without a rebuild.
// Always returns non-nil (an absent/empty hooks.json yields an inert engine), so
// callers can build the per-squad plugin unconditionally. The base path is the
// resolved hooks.json from the search chain; a distinct user-layer hooks.json
// (where the web UI editor saves) is merged as an additive overlay.
func (i *Infrastructure) Hooks(runtime RuntimeSettings) *hooks.Reloader {
	if i == nil {
		return nil
	}
	i.hooks.once.Do(func() {
		base := runtime.HooksConfigPath
		if base == "" {
			base = paths.FindConfig("hooks.json")
		}
		userPath := filepath.Join(paths.WriteDirForLayer(layerForConfigFile("hooks.json")), "hooks.json")
		var overlays []string
		if userPath != base {
			overlays = append(overlays, userPath)
		}
		r := hooks.NewReloader(base, overlays)
		r.Start(context.Background())
		i.hooks.reloader = r
		wireHookListeners(context.Background(), i.Bus, r)
	})
	return i.hooks.reloader
}

// buildHooksPlugin builds the per-squad runner-level plugin that carries the
// blocking / injecting Claude Code-style hooks:
//
//   - PreToolUse       → BeforeToolCallback (a deny short-circuits the tool,
//     exactly like the permissions DENY path).
//   - PostToolUse      → AfterToolCallback (a block appends the hook's reason to
//     the tool output so the model sees the feedback).
//   - UserPromptSubmit → OnUserMessageCallback (additionalContext is appended to
//     the user turn; a block aborts the turn with the reason).
//   - Stop             → AfterRunCallback.
//
// engine is the process-wide hooks Reloader (hot-reloaded config); the plugin
// always reads its live Snapshot, so edits to hooks.json apply without a
// rebuild. The fire-and-forget lifecycle events (SessionStart/End, SubagentStop,
// PreCompact, Notification) are wired once on the bus by wireHookListeners — not
// here — to avoid firing once per squad.
//
// isRouter skips the whole plugin for the Omnis router squad: the router only
// routes (it runs no real tools and the user turn it sees is a clean router
// view), so UserPromptSubmit / tool hooks must fire on the answering squad, not
// the router hop. Returns (nil, nil) for the router so the caller mounts nothing.
func buildHooksPlugin(engine *hooks.Reloader, isRouter bool) (*plugin.Plugin, error) {
	if engine == nil || isRouter {
		return nil, nil
	}

	beforeTool := func(tc tool.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		cfg := engine.Snapshot()
		if len(cfg.Match(hooks.PreToolUse, t.Name())) == 0 {
			return nil, nil
		}
		cwd := fstools.CwdForContext(tc)
		in := hooks.Input{
			SessionID: tc.SessionID(),
			Cwd:       cwd,
			ToolName:  t.Name(),
			ToolInput: args,
		}
		out := cfg.Run(tc, hooks.PreToolUse, t.Name(), in, cwd, hookDefaultTimeout)
		if out.Blocked() {
			reason := out.Reason
			if reason == "" {
				reason = "blocked by PreToolUse hook"
			}
			return map[string]any{
				"output": fmt.Sprintf("[BLOCKED BY HOOK] %s: %s", t.Name(), reason),
			}, nil
		}
		return nil, nil
	}

	afterTool := func(tc tool.Context, t tool.Tool, args, result map[string]any, _ error) (map[string]any, error) {
		cfg := engine.Snapshot()
		if len(cfg.Match(hooks.PostToolUse, t.Name())) == 0 {
			return nil, nil
		}
		cwd := fstools.CwdForContext(tc)
		in := hooks.Input{
			SessionID:    tc.SessionID(),
			Cwd:          cwd,
			ToolName:     t.Name(),
			ToolInput:    args,
			ToolResponse: result,
		}
		out := cfg.Run(tc, hooks.PostToolUse, t.Name(), in, cwd, hookDefaultTimeout)
		if out.Blocked() && out.Reason != "" {
			// Surface the hook's feedback to the model by appending it to the
			// tool output (the tool has already run).
			merged := map[string]any{}
			for k, v := range result {
				merged[k] = v
			}
			if s, ok := merged["output"].(string); ok && s != "" {
				merged["output"] = s + "\n\n[PostToolUse hook] " + out.Reason
			} else {
				merged["output"] = "[PostToolUse hook] " + out.Reason
			}
			return merged, nil
		}
		return nil, nil
	}

	onUserMsg := func(ctx adkagent.InvocationContext, msg *genai.Content) (*genai.Content, error) {
		cfg := engine.Snapshot()
		if len(cfg.Match(hooks.UserPromptSubmit, "")) == 0 {
			return nil, nil
		}
		sid := sessionIDOf(ctx)
		cwd := fstools.CwdFor(ctx, sid)
		in := hooks.Input{
			SessionID: sid,
			Cwd:       cwd,
			Prompt:    contentText(msg),
		}
		out := cfg.Run(ctx, hooks.UserPromptSubmit, "", in, cwd, hookDefaultTimeout)
		if out.Blocked() {
			reason := out.Reason
			if reason == "" {
				reason = "blocked by UserPromptSubmit hook"
			}
			return nil, fmt.Errorf("prompt blocked by hook: %s", reason)
		}
		if out.AdditionalContext != "" {
			return appendContextPart(msg, out.AdditionalContext), nil
		}
		return nil, nil
	}

	afterRun := func(ctx adkagent.InvocationContext) {
		cfg := engine.Snapshot()
		if len(cfg.Match(hooks.Stop, "")) == 0 {
			return
		}
		sid := sessionIDOf(ctx)
		cwd := fstools.CwdFor(ctx, sid)
		in := hooks.Input{SessionID: sid, Cwd: cwd, StopHookActive: true}
		cfg.Run(ctx, hooks.Stop, "", in, cwd, hookDefaultTimeout)
	}

	return plugin.New(plugin.Config{
		Name:                  "hooks",
		BeforeToolCallback:    llmagent.BeforeToolCallback(beforeTool),
		AfterToolCallback:     llmagent.AfterToolCallback(afterTool),
		OnUserMessageCallback: plugin.OnUserMessageCallback(onUserMsg),
		AfterRunCallback:      plugin.AfterRunCallback(afterRun),
	})
}

// FireHook runs the hook commands configured for (event, subject) directly,
// bypassing the event bus. The server uses it for lifecycle moments that must
// not flow through the bus — notably SessionStart / SessionEnd, since the bus
// EventSessionEnd also drives the reflection/curation pipeline that web-UI
// sessions deliberately skip. It is a no-op when the hooks engine has not been
// built yet (no instance constructed) or when no hook matches. The hook input's
// cwd defaults to the session's working directory when unset.
func (i *Infrastructure) FireHook(ctx context.Context, event, subject string, in hooks.Input) {
	if i == nil {
		return
	}
	r := i.hooks.reloader
	if r == nil {
		return
	}
	cfg := r.Snapshot()
	if len(cfg.Match(event, subject)) == 0 {
		return
	}
	in.HookEventName = event
	cwd := in.Cwd
	if cwd == "" {
		cwd = fstools.CwdFor(context.Background(), in.SessionID)
		in.Cwd = cwd
	}
	cfg.Run(ctx, event, subject, in, cwd, hookDefaultTimeout)
}

// wireHookListeners subscribes the fire-and-forget lifecycle hooks to the event
// bus exactly once (called from Infrastructure.Hooks under a sync.Once). These
// events carry no blocking semantics in v1 — the hook commands run for their
// side effects and any additionalContext they emit is currently informational:
//
//	EventSubAgentEnd      → SubagentStop  (subject = sub-agent name)
//	EventSessionStart     → SessionStart
//	EventSessionEnd       → SessionEnd
//	EventCompressionStart → PreCompact    (subject = trigger)
//	EventAskUser          → Notification  (run async so a permission prompt is
//	                                       never blocked by a slow hook)
func wireHookListeners(ctx context.Context, bus *events.Bus, engine *hooks.Reloader) {
	if bus == nil || engine == nil {
		return
	}
	fire := func(event, subject string, in hooks.Input, async bool) {
		cfg := engine.Snapshot()
		if len(cfg.Match(event, subject)) == 0 {
			return
		}
		in.HookEventName = event
		cwd := in.Cwd
		if cwd == "" {
			cwd = fstools.CwdFor(context.Background(), in.SessionID)
			in.Cwd = cwd
		}
		run := func() { cfg.Run(ctx, event, subject, in, cwd, hookDefaultTimeout) }
		if async {
			go run()
		} else {
			run()
		}
	}

	bus.On(events.EventSubAgentEnd, func(_ string, p map[string]any) {
		agentName := strFromPayload(p, "agent")
		fire(hooks.SubagentStop, agentName, hooks.Input{
			SessionID: strFromPayload(p, "session_id"),
			ToolName:  agentName,
		}, false)
	})
	bus.On(events.EventSessionStart, func(_ string, p map[string]any) {
		fire(hooks.SessionStart, "", hooks.Input{
			SessionID: strFromPayload(p, "session_id"),
			Source:    strFromPayload(p, "source"),
		}, false)
	})
	bus.On(events.EventSessionEnd, func(_ string, p map[string]any) {
		fire(hooks.SessionEnd, "", hooks.Input{
			SessionID: strFromPayload(p, "session_id"),
			Reason:    strFromPayload(p, "reason"),
		}, false)
	})
	bus.On(events.EventCompressionStart, func(_ string, p map[string]any) {
		trigger := strFromPayload(p, "trigger")
		fire(hooks.PreCompact, trigger, hooks.Input{
			SessionID: strFromPayload(p, "session_id"),
			Trigger:   trigger,
		}, false)
	})
	bus.On(events.EventAskUser, func(_ string, p map[string]any) {
		fire(hooks.Notification, "", hooks.Input{
			SessionID: strFromPayload(p, "session_id"),
			Message:   strFromPayload(p, "prompt"),
		}, true)
	})
}

// sessionIDOf returns the session id from an InvocationContext, or "".
func sessionIDOf(ctx adkagent.InvocationContext) string {
	if ctx == nil {
		return ""
	}
	if s := ctx.Session(); s != nil {
		return s.ID()
	}
	return ""
}

// contentText flattens a genai.Content's text parts into a single string.
func contentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var b []byte
	for _, p := range c.Parts {
		if p != nil && p.Text != "" {
			if len(b) > 0 {
				b = append(b, '\n')
			}
			b = append(b, p.Text...)
		}
	}
	return string(b)
}

// appendContextPart returns a copy of msg with an extra text part carrying the
// hook's additionalContext, so the model receives it alongside the user prompt.
func appendContextPart(msg *genai.Content, extra string) *genai.Content {
	part := &genai.Part{Text: "\n\n<hook-context source=\"UserPromptSubmit\">\n" + extra + "\n</hook-context>"}
	if msg == nil {
		return &genai.Content{Role: "user", Parts: []*genai.Part{part}}
	}
	out := &genai.Content{Role: msg.Role}
	out.Parts = append(append([]*genai.Part(nil), msg.Parts...), part)
	return out
}

func strFromPayload(p map[string]any, key string) string {
	if p == nil {
		return ""
	}
	if v, ok := p[key].(string); ok {
		return v
	}
	return ""
}
