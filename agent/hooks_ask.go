package agent

import (
	"context"
	"fmt"

	"github.com/blouargant/omnis/internal/askuser"
)

// The two choices offered when a hook escalates. Deliberately NOT the five
// scopes of the permission asker: an "allow always" there is persisted as an
// allow rule, which would permanently disable the guard that asked. A hook
// question is per-call by nature.
const (
	choiceHookAllowOnce = "Allow this once"
	choiceHookDeny      = "Deny"
)

// askHookPermission puts a hook's escalation to the user and reports whether to
// allow this one call.
//
// ctx is the tool's run context, which gives the right lifetime for free: an
// unanswered card is ended by a Stop / session end / shutdown but survives a
// mere client disconnect, so a backgrounded tab keeps the question pending.
//
// With no registry it denies: nobody is going to authorise the call, and
// proceeding unvalidated is the one outcome the guard exists to prevent. Note
// this is a caller-passes-nil case only — every shipped surface builds a registry
// (Infrastructure sets AskUserRegistry unconditionally), so in practice it is
// reached from tests and embedders, NOT from a CLI one-shot. A real CLI run has a
// registry and therefore waits on the question rather than auto-denying.
func askHookPermission(ctx context.Context, reg *askuser.Registry, sid, toolName, reason string) bool {
	if reg == nil {
		return false
	}
	prompt := fmt.Sprintf("**A validation hook is refusing `%s`.**\n\n%s\n\nAllow this call anyway?", toolName, reason)
	ans, err := reg.Ask(ctx, sid, askuser.Question{
		Kind:    askuser.KindSingle,
		Prompt:  prompt,
		Choices: []string{choiceHookAllowOnce, choiceHookDeny},
		Default: choiceHookDeny,
	})
	if err != nil || ans.Cancelled || len(ans.Selected) == 0 {
		return false
	}
	return ans.Selected[0] == choiceHookAllowOnce
}
