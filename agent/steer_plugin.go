package agent

import (
	"context"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/internal/steer"
)

// steerSessionKey tags a run context with the surface-level (web/TUI/CLI) session
// id so a sub-agent — which agenttool runs in a private runner under a fresh,
// ephemeral session id — can still resolve the steering store entry for the REAL
// session. The value propagates into sub-agent runs because agenttool passes the
// leader's tool context to its inner runner.Run (the same path WithCwd uses).
type steerSessionKeyT struct{}

var steerSessionKey = steerSessionKeyT{}

// WithSteerSession returns ctx carrying sessionID as the steering-store key for
// this turn. Planted by each surface before Runner.Run. A blank id is a no-op.
func WithSteerSession(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, steerSessionKey, sessionID)
}

// steerSessionID reads the surface-level session id planted by WithSteerSession,
// or "" when none (e.g. an example that never planted one).
func steerSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(steerSessionKey).(string); ok {
		return v
	}
	return ""
}

// steerPlugin builds the runner-level plugin that injects pending mid-turn
// steering notes into the running turn. It fires before every model call, so a
// note the user types while the agent is working reaches the model at its next
// reasoning step (after the current tool loop, say) rather than waiting for the
// whole turn to finish. With no pending notes it is a no-op, so behaviour is
// unchanged for turns the user does not steer.
//
// It is mounted on answering squad roots only (not the Omnis router — the router
// just routes and never answers, mirroring how lifecycle hooks are gated), so
// steering only ever reaches the squad that actually does the work.
func steerPlugin(name string, store *steer.Store) (*plugin.Plugin, error) {
	return plugin.New(plugin.Config{
		Name:                name,
		BeforeModelCallback: llmagent.BeforeModelCallback(injectSteeringCallback(store)),
	})
}

func injectSteeringCallback(store *steer.Store) llmagent.BeforeModelCallback {
	return func(ctx adkagent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
		if req == nil || store == nil {
			return nil, nil
		}
		// Prefer the surface session id planted on the context (so a sub-agent
		// resolves the real session, not its ephemeral agenttool one); fall back
		// to the runner's session id, which for the leader IS the real session.
		sid := steerSessionID(ctx)
		if sid == "" {
			sid = ctx.SessionID()
		}
		injectSteering(req, store.Drain(sid))
		return nil, nil
	}
}

// steerYieldNotice is the result a sub-agent returns to the leader when it is
// interrupted by a pending steering note. It deliberately does NOT contain the
// note (the leader, not the sub-agent, decides what to do with it).
const steerYieldNotice = "[Interrupted: the user sent new information while I was working, so I stopped and returned control to you (the coordinator) to decide how to proceed. I have not seen the new information.]"

// subAgentSteerYield is the BeforeModelCallback mounted on sub-agents. Unlike the
// leader's callback it does NOT consume or inject the note — it yields control
// back to the leader so the leader is the one that decides what to do with a
// steering note (forward it to this sub-agent, stop the sub-agent, handle it
// itself, or let the task continue). When a note is pending for the real session
// it short-circuits the sub-agent's next model call with a final text response
// (a no-function-call response ends the agenttool run), so the leader's tool call
// returns the yield notice plus whatever the sub-agent had produced so far; the
// leader's own callback then drains and injects the note at its next model step.
// With nothing pending — or no surface session planted (e.g. an example) — it is
// a no-op and the sub-agent runs normally.
func subAgentSteerYield(store *steer.Store) llmagent.BeforeModelCallback {
	return func(ctx adkagent.CallbackContext, req *model.LLMRequest) (*model.LLMResponse, error) {
		if store == nil || req == nil {
			return nil, nil
		}
		sid := steerSessionID(ctx)
		if sid == "" || store.PendingLen(sid) == 0 {
			return nil, nil
		}
		msg := steerYieldNotice
		if partial := lastAssistantText(req.Contents); partial != "" {
			msg += "\n\nWork completed before the interruption (salvage or discard as you see fit):\n" + partial
		}
		return &model.LLMResponse{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: msg}}},
			TurnComplete: true,
		}, nil
	}
}

// lastAssistantText returns the most recent model-authored text in contents, used
// to hand a yielding sub-agent's partial work back to the leader.
func lastAssistantText(contents []*genai.Content) string {
	for i := len(contents) - 1; i >= 0; i-- {
		c := contents[i]
		if c == nil || c.Role != "model" {
			continue
		}
		var b strings.Builder
		for _, p := range c.Parts {
			if p != nil && p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	return ""
}

// steeringAwarenessBlock tells an answering squad root that the user may steer a
// turn mid-flight and that IT — not the sub-agents — decides what to do with each
// note. Appended to non-router roots when steering is enabled (see
// buildSquadInstance).
func steeringAwarenessBlock() string {
	return "\n\n## Mid-turn steering\n\n" +
		"While you work, the user may send extra information, remarks, or corrections. " +
		"You — the coordinator — decide what to do with each one; sub-agents never act " +
		"on them by themselves.\n\n" +
		"- A steering note reaches you as a user message beginning " +
		"\"[The user sent the following while you were working …]\". Treat it as the " +
		"user's latest intent; never ignore it.\n" +
		"- If a sub-agent was running when the note arrived, it **stops and hands " +
		"control back to you** with a result saying it was interrupted by new user " +
		"input (and any work it had done so far). It did not see the note.\n" +
		"- Then decide: (a) the note changes the plan → drop or redo that sub-agent's " +
		"task; (b) the sub-agent needs the new information → re-invoke it with the note " +
		"folded into its instructions (pass back its partial work if useful); " +
		"(c) you should handle it yourself; or (d) the note does not affect that " +
		"sub-agent → re-invoke it to finish its original task.\n"
}

// injectSteering appends the steering notes to req as a user message so the model
// reads them as the user interjecting. To avoid emitting two consecutive
// user-role messages (which strict providers like Anthropic reject — omnis
// reaches Anthropic via LiteLLM, which coalesces, but other adapters may not),
// the note is merged into the trailing message when that is already a user turn
// (the common case mid-tool-loop, where the last content carries the tool
// results); otherwise a fresh user message is appended. Empty notes are a no-op.
func injectSteering(req *model.LLMRequest, notes []string) {
	if req == nil || len(notes) == 0 {
		return
	}
	text := "[The user sent the following while you were working — take it into account:]\n" + strings.Join(notes, "\n")
	part := &genai.Part{Text: text}
	if n := len(req.Contents); n > 0 && req.Contents[n-1] != nil && req.Contents[n-1].Role == "user" {
		req.Contents[n-1].Parts = append(req.Contents[n-1].Parts, part)
		return
	}
	req.Contents = append(req.Contents, &genai.Content{Role: "user", Parts: []*genai.Part{part}})
}
