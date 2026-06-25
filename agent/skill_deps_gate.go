package agent

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"

	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/deps"
	"github.com/blouargant/omnis/internal/skills"
)

// newSkillDepGate builds the process-wide skills.DepGate that enforces a loaded
// skill's declared runtime dependencies at the application level: for each
// required binary that is missing it asks the user (in the active session) to
// install it, runs the install through the Bash safety floor, and rechecks. On
// success the skill proceeds silently; when a dependency stays unavailable
// (declined, no installer, or install failed) it returns a notice that the
// load_skill result carries back to the model so the skill's documented
// fallback applies — never a silent skip. Returns nil when there is no ask-user
// registry, which disables gating entirely.
func newSkillDepGate(reg *askuser.Registry) skills.DepGate {
	if reg == nil {
		return nil
	}
	confirm := deps.NewAskuserConfirmer(reg)
	return func(tc tool.Context, skillName string) string {
		reqs, err := skills.RequiresFor(skillName)
		if err != nil || len(reqs) == 0 {
			return ""
		}
		outcomes := deps.Ensure(tc, tc.SessionID(), reqs, confirm, deps.BashInstaller)
		var unmet []deps.Outcome
		for _, o := range outcomes {
			if !o.Available {
				unmet = append(unmet, o)
			}
		}
		if len(unmet) == 0 {
			return ""
		}
		var sb strings.Builder
		sb.WriteString("DEPENDENCY UNAVAILABLE — this skill needs tools that are not installed:\n")
		for _, o := range unmet {
			sb.WriteString(fmt.Sprintf("- `%s` (%s): %s\n", o.Requirement.Command, o.Requirement.Name(), o.Reason))
		}
		sb.WriteString("Do NOT pretend these tools ran. Follow this skill's documented fallback, " +
			"or tell the user the dependency is required and could not be installed.")
		return sb.String()
	}
}
