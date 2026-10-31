package agent

import (
	"fmt"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"

	"github.com/blouargant/omnis/core/adk"
)

// Per-call tool budget for a sub-agent (agent.json `max_tool_calls_per_call`).
//
// The per-turn cap (`max_tool_calls`) is keyed by agent name, so every parallel
// instance of a fan-out agent shares it — useless for web_agent ×10. And a page
// budget written in the instruction is not obeyed: in the 2026-10-31 A/B
// (omnis-benches reports/call-volume-ab-2026-10-31) web_agent still read 20–30
// pages per call with "fetch at most 8" in its prompt, and web_fetcher made up
// to 230 searches per run against "2 per question".
//
// This cap counts the tool calls of ONE invocation of the sub-agent: agenttool
// starts a fresh runner run per call (a resume is a new run too), so the ADK
// invocation id identifies exactly one call. Past the cap the agent gets the
// same instruction-style notice as the per-turn cap, and after a few ignored
// notices its flow loop is ended host-side.

// callCapFreeTools are not counted: bookkeeping and skill discovery, which a
// sub-agent does once per call and which retrieve nothing.
var callCapFreeTools = map[string]bool{
	"list_skills": true, "load_skill": true, "load_skill_resource": true,
	"list_softskills": true, "load_softskill": true, "load_softskill_resource": true,
	"recall_softskills": true,
}

// callCapNotice is returned in place of a tool result past the per-call cap.
func callCapNotice(agentName string, limit int) string {
	return fmt.Sprintf("[CALL BUDGET REACHED] %s may make at most %d tool calls per task, and this task has used them. "+
		"Do NOT call any more tools. Write your result now from what you have already gathered, "+
		"and list what you could not check under open questions.", agentName, limit)
}

const callCapIdleTTL = 2 * time.Hour

type callCounter struct {
	n    int
	seen time.Time
}

// perCallCapCallback returns the BeforeToolCallback enforcing limit tool calls
// per invocation of agentName, or nil when limit <= 0 (no cap, no-op).
func perCallCapCallback(agentName string, limit int) llmagent.BeforeToolCallback {
	if limit <= 0 {
		return nil
	}
	var mu sync.Mutex
	counts := map[string]*callCounter{}

	charge := func(inv string) (overBy int) {
		mu.Lock()
		defer mu.Unlock()
		now := time.Now()
		c := counts[inv]
		if c == nil {
			// An invocation's entry is dead once its call returned; drop the
			// idle ones so a long-lived generation does not keep one per call.
			for k, old := range counts {
				if now.Sub(old.seen) > callCapIdleTTL {
					delete(counts, k)
				}
			}
			c = &callCounter{}
			counts[inv] = c
		}
		c.n++
		c.seen = now
		return c.n - limit
	}

	return func(tc adk.ToolContext, t tool.Tool, _ map[string]any) (map[string]any, error) {
		name := t.Name()
		if budgetExemptTools[name] || callCapFreeTools[name] {
			return nil, nil
		}
		inv := tc.InvocationID()
		if inv == "" {
			return nil, nil
		}
		overBy := charge(inv)
		if overBy <= 0 {
			return nil, nil
		}
		// Same escalation as the per-turn cap: a few notices let a cooperative
		// agent write up what it has; past that, end its flow loop.
		if overBy > agentCapGraceCalls {
			adk.EndTurnAfterToolCall(tc)
		}
		return map[string]any{"output": callCapNotice(agentName, limit)}, nil
	}
}

// chainBeforeTool runs first, then second when first let the call through.
// Either may be nil.
func chainBeforeTool(first, second llmagent.BeforeToolCallback) llmagent.BeforeToolCallback {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return func(tc adk.ToolContext, t tool.Tool, args map[string]any) (map[string]any, error) {
		if out, err := first(tc, t, args); out != nil || err != nil {
			return out, err
		}
		return second(tc, t, args)
	}
}
