package agent

import (
	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/claudecode"
	"github.com/blouargant/omnis/internal/deps"
)

// newClaudeCodeDepGate builds the process-wide claudecode.DepGate that enforces
// the `claude` (Claude Code CLI) binary at the application level: on the first
// claude_code call, if the binary is missing it asks the user (in the active
// session) to install it, runs the install through the Bash safety floor, and
// rechecks. On success the tool proceeds; when it stays unavailable (declined,
// no installer, or install failed) it returns a notice the tool result carries
// back so the model can report the worker as unavailable instead of pretending
// it ran. Returns nil when there is no ask-user registry (gating disabled — the
// tool then only does a plain PATH check). Mirrors newAstgrepDepGate /
// newSkillDepGate / newLSPDepGate.
func newClaudeCodeDepGate(reg *askuser.Registry) claudecode.DepGate {
	if reg == nil {
		return nil
	}
	confirm := deps.NewAskuserConfirmer(reg)
	req := claudecode.Requirement()
	return func(tc adk.ToolContext) string {
		outcomes := deps.Ensure(tc, tc.SessionID(), []deps.Requirement{req}, confirm, deps.BashInstaller)
		for _, o := range outcomes {
			if !o.Available {
				return "DEPENDENCY UNAVAILABLE — the Claude Code CLI (`claude`) could not be installed (" + o.Reason +
					"). Do NOT pretend it ran; tell the user the claude_code worker is unavailable " +
					"and could not be installed."
			}
		}
		return ""
	}
}
