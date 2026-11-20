package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/blouargant/omnis/core/permissions"
	"github.com/blouargant/omnis/internal/lessons"
)

// callOutcome is what a finished tool call means to the lessons tracker.
type callOutcome int

const (
	// callNeither: the call was refused before it ran (a permission deny, an
	// auto-mode block, a budget or cap notice, a hook block the user declined).
	// It is not the agent's mistake and proves nothing about the arguments: it
	// opens no episode, closes none and settles no hint.
	callNeither callOutcome = iota
	// callFailed: an error worth learning from.
	callFailed
	// callSucceeded: the call ran and worked.
	callSucceeded
)

// bashExit is the exit status the Bash tool reported for one call (see
// fstools.SetBashExitObserver).
type bashExit struct {
	code     int
	timedOut bool
}

// bashNoMatchCommands answer "no match" / "differs" with exit status 1: for
// them, 1 is an answer, not a failure.
var bashNoMatchCommands = map[string]bool{
	"grep": true, "rg": true, "egrep": true, "fgrep": true,
	"diff": true, "cmp": true, "test": true, "[": true,
}

// reErrorText matches a result that is an error message: "Error:" / "Error
// <text>" / "error:" / "error <text>", not "Errors in…" or "ErrorHandler".
var reErrorText = regexp.MustCompile(`^(?:Error|error)(?::|\s+\S)`)

// classifyToolCall says what a finished tool call is: a failure worth learning
// from (with its error text), a success, or neither. Refusals that come from the
// user, a permission mode or a budget are decisions, not mistakes of the agent,
// and are neither; a hook's own block is a failure, since the agent can change
// the command to satisfy it. exit, when non-nil, is the Bash tool's exit status
// and decides a Bash call that ran.
func classifyToolCall(name string, args, result map[string]any, err error, exit *bashExit) (errText string, outcome callOutcome) {
	if err != nil {
		return err.Error(), callFailed
	}
	_, text := dominantString(result)
	trimmed := strings.TrimSpace(text)
	if isDecisionRefusal(trimmed) {
		return "", callNeither
	}
	if strings.HasPrefix(trimmed, hookBlockPrefix) {
		return trimmed, callFailed
	}
	if exit != nil && name == "Bash" {
		return classifyBashExit(args, result, trimmed, *exit)
	}
	if v, ok := result["error"]; ok && v != nil {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" {
			return s, callFailed
		}
	}
	if reErrorText.MatchString(trimmed) {
		return trimmed, callFailed
	}
	return "", callSucceeded
}

// classifyBashExit decides a Bash call from its exit status: non-zero or a
// timeout is a failure, except exit 1 from a command whose 1 means "no match"
// or "differs".
func classifyBashExit(args, result map[string]any, text string, exit bashExit) (string, callOutcome) {
	if !exit.timedOut && exit.code == 0 {
		return "", callSucceeded
	}
	if !exit.timedOut && exit.code == 1 {
		if w := strings.TrimPrefix(lessons.ToolKey("Bash", args), "Bash:"); bashNoMatchCommands[w] {
			return "", callSucceeded
		}
	}
	errText := text
	if out, ok := result["output"].(string); ok {
		errText = strings.TrimSpace(out)
	}
	if errText == "" {
		errText = fmt.Sprintf("exit status %d", exit.code)
	}
	return errText, callFailed
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
