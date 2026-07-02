package agent

import (
	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/astgrep"
	"github.com/blouargant/omnis/internal/deps"
)

// newAstgrepDepGate builds the process-wide astgrep.DepGate that enforces the
// ast-grep binary at the application level: on the first ast_grep_* call, if the
// binary is missing it asks the user (in the active session) to install it, runs
// the install through the Bash safety floor, and rechecks. On success the tool
// proceeds; when it stays unavailable (declined, no installer, or install
// failed) it returns a notice the tool result carries back so the model falls
// back to Grep + Edit. Returns nil when there is no ask-user registry (gating
// disabled — the tools then only do a plain PATH check). Mirrors
// newSkillDepGate / newLSPDepGate.
func newAstgrepDepGate(reg *askuser.Registry) astgrep.DepGate {
	if reg == nil {
		return nil
	}
	confirm := deps.NewAskuserConfirmer(reg)
	req := astgrep.Requirement()
	return func(tc tool.Context) string {
		outcomes := deps.Ensure(tc, tc.SessionID(), []deps.Requirement{req}, confirm, deps.BashInstaller)
		for _, o := range outcomes {
			if !o.Available {
				return "DEPENDENCY UNAVAILABLE — ast-grep could not be installed (" + o.Reason +
					"). Do NOT pretend it ran; use Grep to find matches and Edit/MultiEdit to change them, " +
					"or tell the user ast-grep is required and could not be installed."
			}
		}
		return ""
	}
}
