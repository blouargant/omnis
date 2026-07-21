// Package adk is omnis's anti-corruption layer over the churny surface of
// google.golang.org/adk. It exists so the ADK v1->v2 migration is a one-file
// change instead of a ~55-file sweep: v2 merges the three context types into
// agent.Context, changes session.NewEvent to take a context, and reworks the
// run/flow runtime. Every omnis package names those seams through the aliases
// and helpers here, so migrating is: repoint the imports below to
// ".../adk/v2/..." and adjust these few lines.
//
// It deliberately does NOT wrap the STABLE ADK types (model.LLM, agent.Agent,
// tool.Tool, session.Event, functiontool) — those are imported directly across
// the codebase and v2 does not break them, so wrapping them would be cost with
// no payoff.
//
// This package imports ONLY google.golang.org/adk/* + stdlib, so it is a pure
// leaf and can never form an import cycle with the rest of omnis.
package adk

import (
	"context"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
)

// Context aliases. In ADK v1 these are three distinct interfaces; ADK v2 merges
// them into a single agent.Context. TO MIGRATE: change every right-hand side
// below to agent.Context (they all become the same type). Call sites, which use
// only the alias names on the left, do not change.
type (
	// ToolContext is the context a tool handler receives.
	ToolContext = tool.Context // v2: = agent.Context
	// CallbackContext is the context a before/after model-or-tool callback receives.
	CallbackContext = agent.CallbackContext // v2: = agent.Context
	// ReadonlyContext is the read-only context some callbacks receive.
	ReadonlyContext = agent.ReadonlyContext // v2: = agent.Context
	// InvocationContext is the context a run-level (before/after-run, user-message) callback receives.
	InvocationContext = agent.InvocationContext // v2: = agent.Context
)

// EndTurnAfterToolCall marks the current function-response event as final, so
// the ADK flow loop stops immediately after this tool call instead of handing
// the model another (possibly looping) turn. It is the host-side turn-
// termination guarantee behind route_to_squad / handoff_to_router, the per-
// agent budget cap, and session-search's report_sessions.
//
// This is THE single place that pokes SkipSummarization. ADK v2 drives even a
// plain LlmAgent through the workflow node runtime, which may change how a run
// terminates; if it does, re-implement termination here (e.g. via a v2 route or
// HITL primitive) and every call site is fixed at once. TestSkipSummarization*
// in this package is the canary that fails the moment the mechanism changes.
func EndTurnAfterToolCall(ctx ToolContext) {
	ctx.Actions().SkipSummarization = true
}

// NewEvent builds a session.Event, threading ctx as ADK v2 requires. It wraps
// session.NewEventWithContext, which already exists in v1.5 and is the v2-shaped
// constructor, so call sites are v2-ready today. TO MIGRATE: swap the body to
// session.NewEvent(ctx, invocationID).
func NewEvent(ctx context.Context, invocationID string) *session.Event {
	return session.NewEventWithContext(ctx, invocationID)
}
