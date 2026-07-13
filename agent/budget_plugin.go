package agent

import (
	"context"
	"fmt"
	"strings"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/budget"
)

// Per-turn spend ceiling.
//
// The ADK flow loop has no iteration cap (it ends only on a tool-call-free
// model response), and a sub-agent runs in agenttool's private, plugin-less
// runner where the surface cannot see it. So a squad whose instructions say
// "iterate until satisfied" — the deep-research playbook — can search
// indefinitely, and did: one turn reached ~295 WebSearch calls and 15.2M tokens
// across 39 minutes before answering.
//
// The gate below is the host-side ceiling an instruction cannot be. It is wired
// exactly like the permission gate: the plugin governs the squad root, and the
// SAME callbacks are attached to every sub-agent (see buildSubAgentsFromConfigs)
// so the whole turn — leader and fan-out alike — spends from one bucket keyed by
// the user-facing session. On overrun the user, not the model, decides.

// budgetExemptTools are never counted and never blocked. Gating them would be
// self-defeating: ask_user is how the budget question itself reaches the user,
// the routing tools are the squad's only way to hand control back, and the todo
// tools are bookkeeping the UI renders rather than work the turn is doing.
var budgetExemptTools = map[string]bool{
	"ask_user":          true,
	"route_to_squad":    true,
	"handoff_to_router": true,
	"ask_squad":         true,
	"todo_read":         true,
	"todo_write":        true,
	"todo_update":       true,
	"compact_now":       true,
}

// agentCapGraceCalls is how many blocked tool calls an over-cap agent may make
// while being told, by tool result alone, to stop and write up its findings.
// Past that we stop asking and end its flow loop. Small but non-zero: a
// cooperative agent needs at least one turn to see the notice and comply (that
// is how we keep its partial work), while a stubborn one must not be able to
// spin indefinitely.
const agentCapGraceCalls = 3

// Choice labels. Stop is first and is the default: the safe answer to "this
// turn is running away" is to stop it, and a user who hits Enter on the card
// should not accidentally buy another 2M tokens.
const (
	choiceBudgetStop      = "Stop and answer now"
	choiceBudgetContinue  = "Continue"
	choiceBudgetUnlimited = "Continue without a limit"
)

// budgetHaltNotice replaces the result of every tool call made after the user
// stops the turn. It has to do two jobs: end the loop (the model must stop
// calling tools) and salvage the work (it must answer with what it has rather
// than apologise and give up), so it is phrased as an instruction, not an error.
const budgetHaltNotice = "[BUDGET REACHED] The user stopped further research for this turn. " +
	"Do NOT call any more tools. Write your final answer now from the material you have already gathered, " +
	"and state plainly which parts are unverified or incomplete."

// agentCapNotice replaces the result of a tool call an agent makes past its own
// per-turn cap. Like budgetHaltNotice it is phrased as an instruction rather than
// an error: the agent must stop calling tools AND still deliver, from what it has.
func agentCapNotice(agentName string) string {
	return fmt.Sprintf("[TOOL BUDGET REACHED] %s has used its tool-call budget for this turn. "+
		"Do NOT call any more tools. Produce your result now from the material you have already gathered, "+
		"and state plainly what you were unable to verify.", agentName)
}

// budgetSessionID resolves the user-facing session id from any callback context.
// A sub-agent runs under an ephemeral agenttool session, so its own SessionID()
// is useless as a bucket key — the real id is whichever tag the surface planted
// on the run context (both propagate into sub-agents; either alone is enough).
func budgetSessionID(ctx context.Context) string {
	if id := steerSessionID(ctx); id != "" {
		return id
	}
	return events.RootSessionFromContext(ctx)
}

// budgetCallbacks builds the pair that enforces the per-turn ceiling: a
// BeforeToolCallback that counts tool calls and short-circuits once the user
// has stopped the turn, and an AfterModelCallback that accumulates tokens.
//
// Returns (nil, nil) — a byte-identical no-op — when there is no store, no
// ceiling configured, or this is the router squad (which only routes: it runs
// no research, and blocking its handoff would strand the turn).
func budgetCallbacks(store *budget.Store, reg *askuser.Registry, limits budget.Limits, isRouterSquad bool) (llmagent.BeforeToolCallback, llmagent.AfterModelCallback) {
	if store == nil || isRouterSquad || limits.Unlimited() {
		return nil, nil
	}

	beforeTool := func(tc tool.Context, t tool.Tool, _ map[string]any) (map[string]any, error) {
		if budgetExemptTools[t.Name()] {
			return nil, nil
		}
		sid := realSessionID(tc)
		if sid == "" {
			return nil, nil
		}
		// Per-agent cap first. It is a design limit ("the critic verifies with at
		// most N searches"), not a spend surprise, so it never asks the user — it
		// just tells the agent to conclude. Checked before the shared gate so a
		// call this cap rejects is not charged to the turn's budget: it is work
		// that never happened.
		if agentName := tc.AgentName(); agentName != "" {
			if over, overBy := store.ChargeAgent(sid, agentName); over {
				// The notice is an instruction, and a model may simply ignore it: in a
				// live run a capped web_agent issued 16 more tool calls across 13 model
				// round-trips after being told to stop. Nothing else would end that —
				// the flow loop has no iteration cap, and a blocked call is not charged
				// to the shared turn budget. So: a few blocked calls carry the notice
				// alone, which is enough for a cooperative agent to stop and write up
				// what it has (the salvage we want); past that, terminate its flow loop
				// for real. SkipSummarization makes this function-response event
				// IsFinalResponse(), which ends the loop — a host-side guarantee, the
				// same one the routing tools rely on, rather than another instruction.
				if overBy > agentCapGraceCalls {
					tc.Actions().SkipSummarization = true
				}
				return map[string]any{"output": agentCapNotice(agentName)}, nil
			}
		}
		verdict := store.Gate(sid, func(u budget.Usage) budget.Outcome {
			// tc carries the run context, so an unanswered card is ended by a
			// Stop/shutdown but survives a mere client disconnect — the same
			// contract as the permission prompt.
			return askBudget(tc, reg, sid, u)
		})
		if verdict == budget.Halted {
			return map[string]any{"output": budgetHaltNotice}, nil
		}
		return nil, nil
	}

	afterModel := func(cb adkagent.CallbackContext, resp *model.LLMResponse, _ error) (*model.LLMResponse, error) {
		// Streaming deltas carry no usage; only the final aggregated response
		// does (same reason core/events skips partials).
		if resp == nil || resp.Partial || resp.UsageMetadata == nil {
			return nil, nil
		}
		sid := budgetSessionID(cb)
		if sid == "" {
			return nil, nil
		}
		u := resp.UsageMetadata
		store.AddTokens(sid, int64(u.PromptTokenCount)+int64(u.CandidatesTokenCount))
		return nil, nil
	}

	return beforeTool, afterModel
}

// budgetPlugin mounts the budget callbacks on a squad root's runner. Sub-agents
// get the same two callbacks attached directly (runner plugins do not reach
// them), so both halves of a turn spend from one bucket.
func budgetPlugin(name string, beforeTool llmagent.BeforeToolCallback, afterModel llmagent.AfterModelCallback) (*plugin.Plugin, error) {
	if beforeTool == nil && afterModel == nil {
		return nil, nil
	}
	return plugin.New(plugin.Config{
		Name:               name,
		BeforeToolCallback: beforeTool,
		AfterModelCallback: afterModel,
	})
}

// askBudget raises the "this turn is expensive — keep going?" card and maps the
// answer onto an Outcome. With no registry (a CLI one-shot, an example) there is
// nobody to ask, so it stops: an unattended runaway is exactly what this exists
// to prevent.
func askBudget(ctx context.Context, reg *askuser.Registry, sid string, u budget.Usage) budget.Outcome {
	if reg == nil {
		return budget.OutcomeStop
	}
	ans, err := reg.Ask(ctx, sid, askuser.Question{
		Kind:    askuser.KindSingle,
		Prompt:  budgetPrompt(u),
		Choices: []string{choiceBudgetStop, choiceBudgetContinue, choiceBudgetUnlimited},
		Default: choiceBudgetStop,
	})
	// A cancelled card (session end, shutdown, Stop) means nobody is going to
	// authorise more spend — stop rather than run on.
	if err != nil || ans.Cancelled || len(ans.Selected) == 0 {
		return budget.OutcomeStop
	}
	switch ans.Selected[0] {
	case choiceBudgetContinue:
		return budget.OutcomeContinue
	case choiceBudgetUnlimited:
		return budget.OutcomeUnlimited
	}
	return budget.OutcomeStop
}

// budgetPrompt renders the card. It leads with what was actually spent, because
// that — not the abstract limit — is what the user needs in order to decide.
func budgetPrompt(u budget.Usage) string {
	var sb strings.Builder
	sb.WriteString("**This turn has hit its budget.**\n\n")
	sb.WriteString(fmt.Sprintf("- Tool calls: **%d**", u.ToolCalls))
	if u.Limits.MaxToolCalls > 0 {
		sb.WriteString(fmt.Sprintf(" / %d", u.Limits.MaxToolCalls))
	}
	sb.WriteString(fmt.Sprintf("\n- Tokens: **%s**", humanTokens(u.Tokens)))
	if u.Limits.MaxTokens > 0 {
		sb.WriteString(" / " + humanTokens(u.Limits.MaxTokens))
	}
	sb.WriteString("\n")
	if u.Grants > 0 {
		sb.WriteString(fmt.Sprintf("\n_You have already extended this turn %d time(s)._\n", u.Grants))
	}
	sb.WriteString("\nStopping keeps everything gathered so far — the agent answers with what it has.")
	return sb.String()
}

// humanTokens renders a token count compactly (1_234_567 → "1.2M").
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
