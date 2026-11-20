package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/blouargant/omnis/core/permissions"
	fstools "github.com/blouargant/omnis/core/tools"
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

// reBareExitStatus is the Bash tool's text for a command that failed with no
// output: "Error: exit status N".
var reBareExitStatus = regexp.MustCompile(`^Error: exit status -?\d+$`)

// classifyBashExit decides a Bash call from its exit status: non-zero or a
// timeout is a failure, except exit 1 when the command the shell ran last (the
// last segment of a pipeline or of a && / || / ; chain) answers "no match" or
// "differs" with 1. A failure with no output to learn from (empty, "(no
// output)", or the bare "Error: exit status N") is neither: it opens no
// episode and settles nothing.
func classifyBashExit(args, result map[string]any, text string, exit bashExit) (string, callOutcome) {
	if !exit.timedOut && exit.code == 0 {
		return "", callSucceeded
	}
	if !exit.timedOut && exit.code == 1 && bashNoMatchCommands[lastBashCommand(args)] {
		return "", callSucceeded
	}
	errText := text
	if out, ok := result["output"].(string); ok {
		errText = strings.TrimSpace(out)
	}
	if !exit.timedOut && (errText == "" || errText == "(no output)" || reBareExitStatus.MatchString(errText)) {
		return "", callNeither
	}
	return errText, callFailed
}

// lastBashCommand is the program of the last segment of a Bash call's command
// (split on unquoted |, &&, ||, ; and newlines), wrappers skipped: the shell
// returns that segment's exit status.
func lastBashCommand(args map[string]any) string {
	cmd, _ := args["command"].(string)
	segs := permissions.SplitCompound(cmd)
	if len(segs) == 0 {
		return ""
	}
	last := segs[len(segs)-1]
	return strings.TrimPrefix(lessons.ToolKey("Bash", map[string]any{"command": last}), "Bash:")
}

// isDecisionRefusal reports whether s is a refusal that is not the agent's
// mistake: a gate denial, an auto-mode block, a budget stop, a Bash command the
// safety floor or a read-only agent's confinement refused, or a hook block the
// user declined / the mode or an unattended run refused.
func isDecisionRefusal(s string) bool {
	for _, p := range []string{
		permissions.DeniedPrefix, permissions.AutoBlockPrefix,
		budgetHaltPrefix, agentCapPrefix, callCapPrefix,
		fstools.SafetyFloorBlockedPrefix, fstools.ReadOnlyRefusedPrefix,
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
