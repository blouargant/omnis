// subagent_event.go — synthesise EventSubAgentStart / EventSubAgentEnd
// from the leader's before_tool / after_tool payloads.
//
// Background: sub-agents wrapped via agenttool spawn their own internal
// runner, which does NOT carry the bus's runner-level plugin, so the
// only run boundary the bus actually observes is the leader's tool call.
// Re-emitting it under sub-agent-specific names lets reflection hooks
// subscribe to "a sub-agent invocation finished" cleanly, without having
// to filter every after_tool event by tool name.

package agent

import (
	"github.com/blouargant/yoke/core/events"
)

// registerSubAgentBoundary subscribes to EventBeforeTool / EventAfterTool
// and EventToolError, filters those whose `tool` matches one of the given
// sub-agent names, and re-emits them as EventSubAgentStart / EventSubAgentEnd.
//
// Returns the bus subscriptions so the caller (typically Instance) can
// detach them on hot-reload teardown.
func registerSubAgentBoundary(bus *events.Bus, subAgentNames []string) []*events.Subscription {
	if bus == nil || len(subAgentNames) == 0 {
		return nil
	}
	set := map[string]struct{}{}
	for _, n := range subAgentNames {
		if n != "" {
			set[n] = struct{}{}
		}
	}
	if len(set) == 0 {
		return nil
	}

	isSubAgent := func(payload map[string]any) (string, bool) {
		t, _ := payload["tool"].(string)
		if t == "" {
			return "", false
		}
		_, ok := set[t]
		return t, ok
	}

	before := func(_ string, payload map[string]any) {
		name, ok := isSubAgent(payload)
		if !ok {
			return
		}
		out := map[string]any{
			"agent":      name,
			"user_id":    payload["user_id"],
			"session_id": payload["session_id"],
			"input":      payload["input"],
			"call_id":    payload["call_id"],
			"run_id":     payload["run_id"],
		}
		// Preserve the calling agent (the leader, in practice) so a
		// reflector can distinguish nested sub-agent calls if those ever
		// appear. The payload's "agent" key is overwritten with the
		// sub-agent's own name to keep semantics consistent across both
		// new event names.
		if caller, ok := payload["agent"].(string); ok {
			out["caller_agent"] = caller
		}
		bus.Emit(events.EventSubAgentStart, out)
	}

	after := func(_ string, payload map[string]any) {
		name, ok := isSubAgent(payload)
		if !ok {
			return
		}
		out := map[string]any{
			"agent":      name,
			"user_id":    payload["user_id"],
			"session_id": payload["session_id"],
			"input":      payload["input"],
			"output":     payload["output"],
			"duration":   payload["duration"],
			"call_id":    payload["call_id"],
			"run_id":     payload["run_id"],
		}
		if caller, ok := payload["agent"].(string); ok {
			out["caller_agent"] = caller
		}
		bus.Emit(events.EventSubAgentEnd, out)
	}

	onErr := func(_ string, payload map[string]any) {
		name, ok := isSubAgent(payload)
		if !ok {
			return
		}
		out := map[string]any{
			"agent":      name,
			"user_id":    payload["user_id"],
			"session_id": payload["session_id"],
			"input":      payload["input"],
			"error":      payload["error"],
			"call_id":    payload["call_id"],
			"run_id":     payload["run_id"],
		}
		if caller, ok := payload["agent"].(string); ok {
			out["caller_agent"] = caller
		}
		bus.Emit(events.EventSubAgentEnd, out)
	}

	return []*events.Subscription{
		bus.Subscribe(events.EventBeforeTool, before),
		bus.Subscribe(events.EventAfterTool, after),
		bus.Subscribe(events.EventToolError, onErr),
	}
}
