package agent

import (
	"fmt"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

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

// callCapWarnRemaining is when the sub-agent is told its budget is nearly spent.
// Without it the cap arrives unannounced: in the 2026-11-01 Knowledge bench
// nearly every web_agent call ended at 18 tool calls — the 15 allowed plus the 3
// refused grace calls, each a full model round on the call's largest context —
// instead of writing up what it had.
const callCapWarnRemaining = 3

// callCapWarning is added to a tool result once the invocation has at most
// callCapWarnRemaining tool calls left.
func callCapWarning(left int) string {
	return fmt.Sprintf("[CALL BUDGET] %d tool call(s) left for this task. Use them only for what is "+
		"essential, then write your result; list anything you could not check under open questions.", left)
}

type callCounter struct {
	n      int
	warned bool
	seen   time.Time
}

// perCallCap counts the tool calls of each invocation of one sub-agent.
type perCallCap struct {
	agent  string
	limit  int
	mu     sync.Mutex
	counts map[string]*callCounter
}

// newPerCallCap returns nil when limit <= 0 (no cap).
func newPerCallCap(agentName string, limit int) *perCallCap {
	if limit <= 0 {
		return nil
	}
	return &perCallCap{agent: agentName, limit: limit, counts: map[string]*callCounter{}}
}

func counted(name string) bool {
	return !budgetExemptTools[name] && !callCapFreeTools[name]
}

func (p *perCallCap) charge(inv string) (overBy int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	c := p.counts[inv]
	if c == nil {
		// An invocation's entry is dead once its call returned; drop the
		// idle ones so a long-lived generation does not keep one per call.
		for k, old := range p.counts {
			if now.Sub(old.seen) > callCapIdleTTL {
				delete(p.counts, k)
			}
		}
		c = &callCounter{}
		p.counts[inv] = c
	}
	c.n++
	c.seen = now
	return c.n - p.limit
}

// takeWarning reports how many calls the invocation has left when it is time
// to warn (once per invocation), else -1.
func (p *perCallCap) takeWarning(inv string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.counts[inv]
	if c == nil || c.warned {
		return -1
	}
	left := p.limit - c.n
	if left <= 0 || left > callCapWarnRemaining {
		return -1
	}
	c.warned = true
	return left
}

// before is the BeforeToolCallback enforcing the cap.
func (p *perCallCap) before(tc adk.ToolContext, t tool.Tool, _ map[string]any) (map[string]any, error) {
	if !counted(t.Name()) {
		return nil, nil
	}
	inv := tc.InvocationID()
	if inv == "" {
		return nil, nil
	}
	overBy := p.charge(inv)
	if overBy <= 0 {
		return nil, nil
	}
	// Same escalation as the per-turn cap: a few notices let a cooperative
	// agent write up what it has; past that, end its flow loop.
	if overBy > agentCapGraceCalls {
		adk.EndTurnAfterToolCall(tc)
	}
	return map[string]any{"output": callCapNotice(p.agent, p.limit)}, nil
}

// warn is the AfterToolCallback adding the low-budget note to a result.
func (p *perCallCap) warn(tc adk.ToolContext, t tool.Tool, _, result map[string]any, err error) (map[string]any, error) {
	if err != nil || result == nil || !counted(t.Name()) {
		return nil, nil
	}
	left := p.takeWarning(tc.InvocationID())
	if left < 0 {
		return nil, nil
	}
	out := make(map[string]any, len(result)+1)
	for k, v := range result {
		out[k] = v
	}
	out["call_budget"] = callCapWarning(left)
	return out, nil
}

// callCapFinalRound is shown to a sub-agent whose budget is spent, on the model
// call where its tools are switched off.
//
// It insists on citing what was gathered: without that, a broad task's final
// answer was written from memory — no URLs or invented references — in 2 of 3
// cheap-bench runs (2026-11-01), although the fetched pages were in context.
func callCapFinalRound(limit int) string {
	return fmt.Sprintf("[CALL BUDGET REACHED] You have used all %d tool calls for this task and tools are "+
		"now disabled. Write your final result now, using ONLY what the tool results above contain: "+
		"back each claim with the URL of the page it came from (copy the URLs exactly as fetched; never "+
		"invent or guess one). Do not fill gaps from memory — put anything you could not check under "+
		"open questions.", limit)
}

// exhausted reports whether the invocation has used its whole budget.
func (p *perCallCap) exhausted(inv string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := p.counts[inv]
	return c != nil && c.n >= p.limit
}

// finalRound is the BeforeModelCallback that switches tools off once the budget
// is spent, so the next model call writes the result.
//
// A note is not enough: models ignore it and keep calling tools until the
// refusals end the loop host-side — and a sub-agent's result is the text of its
// LAST event, which is then a tool response with no text. The caller got an
// EMPTY result and lost everything the 15 calls had found, which is what drove
// the leader's re-delegation waves (2026-11-01 Knowledge bench).
func (p *perCallCap) finalRound(ctx adk.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
	if req == nil || !p.exhausted(ctx.InvocationID()) {
		return nil, nil
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	req.Config.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{
		Mode: genai.FunctionCallingConfigModeNone,
	}}
	appendUserText(req, callCapFinalRound(p.limit))
	return nil, nil
}

// appendUserText adds text as a user turn, merged into a trailing user turn
// (the tool results) so two user messages never follow each other.
func appendUserText(req *model.LLMRequest, text string) {
	part := &genai.Part{Text: text}
	if n := len(req.Contents); n > 0 && req.Contents[n-1] != nil && req.Contents[n-1].Role == "user" {
		req.Contents[n-1].Parts = append(req.Contents[n-1].Parts, part)
		return
	}
	req.Contents = append(req.Contents, &genai.Content{Role: "user", Parts: []*genai.Part{part}})
}

// perCallCapHooks are the per-call cap's callbacks; all nil when there is no cap.
type perCallCapHooks struct {
	Before     llmagent.BeforeToolCallback
	Warn       llmagent.AfterToolCallback
	FinalRound llmagent.BeforeModelCallback
}

func newPerCallCapHooks(agentName string, limit int) perCallCapHooks {
	p := newPerCallCap(agentName, limit)
	if p == nil {
		return perCallCapHooks{}
	}
	return perCallCapHooks{Before: p.before, Warn: p.warn, FinalRound: p.finalRound}
}

// perCallCapCallbacks returns the cap's before- and after-tool callbacks, or
// two nils when limit <= 0 (no cap, no-op).
func perCallCapCallbacks(agentName string, limit int) (llmagent.BeforeToolCallback, llmagent.AfterToolCallback) {
	p := newPerCallCap(agentName, limit)
	if p == nil {
		return nil, nil
	}
	return p.before, p.warn
}

// perCallCapCallback returns only the enforcing BeforeToolCallback.
func perCallCapCallback(agentName string, limit int) llmagent.BeforeToolCallback {
	before, _ := perCallCapCallbacks(agentName, limit)
	return before
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

// chainAfterTool runs first, then second on the (possibly rewritten) result.
// ADK stops at the first after-tool callback that returns a result, so two
// callbacks that both need to see the result must be composed. Either may be nil.
func chainAfterTool(first, second llmagent.AfterToolCallback) llmagent.AfterToolCallback {
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return func(tc adk.ToolContext, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		out, ferr := first(tc, t, args, result, err)
		if ferr != nil {
			return nil, ferr
		}
		base := result
		if out != nil {
			base = out
		}
		out2, serr := second(tc, t, args, base, err)
		if serr != nil {
			return nil, serr
		}
		if out2 != nil {
			return out2, nil
		}
		return out, nil
	}
}
