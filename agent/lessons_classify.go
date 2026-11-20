package agent

import (
	"fmt"
	"strings"

	"github.com/blouargant/omnis/core/permissions"
)

// classifyToolFailure says whether a finished tool call is an error worth
// learning from. Refusals that come from the user, a permission mode or a
// budget are decisions, not mistakes of the agent, and are never failures; a
// hook's own block is one, since the agent can change the command to satisfy it.
func classifyToolFailure(result map[string]any, err error) (errText string, failed bool) {
	if err != nil {
		return err.Error(), true
	}
	_, text := dominantString(result)
	trimmed := strings.TrimSpace(text)
	if isDecisionRefusal(trimmed) {
		return "", false
	}
	if strings.HasPrefix(trimmed, hookBlockPrefix) {
		return trimmed, true
	}
	if v, ok := result["error"]; ok && v != nil {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			return s, true
		}
	}
	if strings.HasPrefix(trimmed, "Error") || strings.HasPrefix(trimmed, "error ") {
		return trimmed, true
	}
	return "", false
}

// isDecisionRefusal reports whether s is a refusal that is not the agent's
// mistake: a gate denial, an auto-mode block, a budget stop, or a hook block
// the user declined / the mode or an unattended run refused.
func isDecisionRefusal(s string) bool {
	for _, p := range []string{
		permissions.DeniedPrefix, permissions.AutoBlockPrefix,
		budgetHaltPrefix, agentCapPrefix, callCapPrefix,
	} {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	if strings.HasPrefix(s, hookBlockPrefix) {
		return strings.HasSuffix(s, hookDeclinedSuffix) ||
			strings.Contains(s, hookDontAskMarker) ||
			strings.Contains(s, hookUnattendedMarker)
	}
	return false
}
