package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/deps"
	"github.com/blouargant/omnis/internal/lsp"
)

// newLSPDepGate builds the process-wide lsp.DepGate that enforces a language
// server's declared `requires` at the application level: for each required
// binary that is missing it asks the user (in the active session) to install
// it, runs the install through the Bash safety floor, and rechecks. On success
// the server starts; when a dependency stays unavailable (declined, no
// installer, or install failed) it returns an error that blocks the start, so
// the lsp_* tool reports the server as unavailable and the agent falls back to
// Grep/Read. Returns nil when there is no ask-user registry (gating disabled).
func newLSPDepGate(reg *askuser.Registry) lsp.DepGate {
	if reg == nil {
		return nil
	}
	confirm := deps.NewAskuserConfirmer(reg)
	return func(ctx context.Context, sessionID string, reqs []deps.Requirement) error {
		if len(reqs) == 0 {
			return nil
		}
		outcomes := deps.Ensure(ctx, sessionID, reqs, confirm, deps.BashInstaller)
		var unmet []string
		for _, o := range outcomes {
			if !o.Available {
				unmet = append(unmet, fmt.Sprintf("%s (%s)", o.Requirement.Command, o.Reason))
			}
		}
		if len(unmet) > 0 {
			return fmt.Errorf("language server dependency unavailable: %s", strings.Join(unmet, "; "))
		}
		return nil
	}
}
