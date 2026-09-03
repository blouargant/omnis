package attest

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
)

type recordIn struct {
	Subject string `json:"subject" jsonschema:"required,the change identifier to attest — the exact subject hash given to you by the caller; never invent one"`
	Verdict string `json:"verdict" jsonschema:"required,APPROVED or REJECTED"`
	Reasons string `json:"reasons" jsonschema:"required,one line per finding: what you checked and what you concluded, with the field or resource you based it on"`
}

type recordOut struct {
	Result string `json:"result"`
}

// Tools returns the attestation tool set (one tool). Mount it ONLY on a reviewer
// agent: an agent that can attest its own work has no reviewer.
//
// sessionOf resolves the USER-FACING session from a tool context, and is injected
// rather than chosen here for a reason that is easy to get wrong. A reviewer runs
// as a sub-agent, so its own ctx.SessionID() is an ephemeral agenttool session;
// a verdict recorded under that id is invisible to the hook, which reads
// attestations by the real session. The codebase has exactly one correct resolver
// (agent.realSessionID: steer-session first, then ctx.SessionID()), and attest
// cannot import agent without a cycle — so the caller passes it in, and both
// sides of the attestation are keyed by literally the same function.
//
// Do NOT substitute events.RootSessionFromContext here: WithRootSession is planted
// only by the server (server/sse.go, server/mailbox_push.go, server/a2a_server.go)
// and NOT by the CLI or TUI, so on those surfaces it resolves empty and every
// Kubernetes mutation would be refused as "not reviewed" — on the one refusal path
// that deliberately never escalates to the user.
func Tools(store *Store, sessionOf func(adk.ToolContext) string) []tool.Tool {
	t, err := functiontool.New(functiontool.Config{
		Name: "record_validation",
		Description: "Record your review verdict for one specific change so the host can act on it. " +
			"Call this exactly once, as your final step, with the `subject` identifier you were given. " +
			"Arguments: `subject` (string, required) — the change identifier supplied by the caller, copied verbatim; " +
			"`verdict` (string, required) — APPROVED or REJECTED; " +
			"`reasons` (string, required) — what you checked and concluded, citing the resource fields you read. " +
			"An APPROVED verdict lets the change proceed, so do not approve anything you did not verify yourself.",
	}, func(ctx adk.ToolContext, in recordIn) (recordOut, error) {
		return runRecordValidation(store, sessionOf(ctx), in), nil
	})
	if err != nil {
		panic(fmt.Errorf("build record_validation tool: %w", err))
	}
	return []tool.Tool{t}
}

// runRecordValidation is the tool handler's body, factored out of the ADK
// closure above so it can be tested directly against a plain session id
// instead of a real adk.ToolContext — which cannot be constructed outside the
// adk module (its only constructor path runs through an unexported internal
// package) — the same reason internal/testrun factors its handler body out of
// the functiontool closure.
func runRecordValidation(store *Store, sid string, in recordIn) recordOut {
	v := VerdictRejected
	if strings.EqualFold(strings.TrimSpace(in.Verdict), string(VerdictApproved)) {
		v = VerdictApproved
	}
	if sid == "" {
		// Loud rather than silent: a verdict with no session to key it under
		// would be recorded where no hook will ever read it.
		return recordOut{Result: "Error: no session could be resolved, so the verdict cannot be recorded."}
	}
	if strings.TrimSpace(in.Subject) == "" {
		return recordOut{Result: "Error: subject is required — use the change identifier you were given."}
	}
	store.Record(sid, strings.TrimSpace(in.Subject), v, in.Reasons)
	return recordOut{Result: fmt.Sprintf("Recorded %s for %s.", v, in.Subject)}
}
