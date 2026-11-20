package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/blouargant/omnis/core/permissions"
)

// refusalResults are the results of calls refused before they ran: decisions,
// not mistakes, and no proof that the arguments work.
func refusalResults() map[string]map[string]any {
	return map[string]map[string]any{
		"gate deny":       {"output": fmt.Sprintf(permissions.DeniedPrefix+" %s: %s", "Bash", "denied")},
		"auto block":      {"output": permissions.AutoBlockMessage("Bash", "nope")},
		"budget halt":     {"output": budgetHaltNotice},
		"agent cap":       {"output": agentCapNotice("coder")},
		"call cap":        {"output": callCapNotice("coder", 20)},
		"hook declined":   {"output": fmt.Sprintf(hookBlockPrefix+" %s: %s %s", "Bash", "why", hookDeclinedSuffix)},
		"hook dontAsk":    {"output": fmt.Sprintf(hookBlockPrefix+" %s: %s: x", "Bash", hookDontAskMarker)},
		"hook unattended": {"output": fmt.Sprintf(hookBlockPrefix+" %s: %s (%s, so x)", "Bash", "why", hookUnattendedMarker)},
	}
}

func Test_classifyToolCall(t *testing.T) {
	cases := []struct {
		name    string
		result  map[string]any
		err     error
		outcome callOutcome
	}{
		{"go error", nil, errors.New("boom"), callFailed},
		{"Error colon", map[string]any{"output": "Error: no such file"}, nil, callFailed},
		{"Error word", map[string]any{"output": "Error reading x.go: denied"}, nil, callFailed},
		{"error word", map[string]any{"output": "error fetching URL: HTTP 500"}, nil, callFailed},
		{"error colon", map[string]any{"output": "error: unable to recognize \"f.yaml\""}, nil, callFailed},
		{"Errors in", map[string]any{"output": "Errors in the log are expected here"}, nil, callSucceeded},
		{"ErrorHandler", map[string]any{"output": "ErrorHandler is defined in x.go"}, nil, callSucceeded},
		{"bare Error", map[string]any{"output": "Error"}, nil, callSucceeded},
		{"error key", map[string]any{"error": "bad"}, nil, callFailed},
		{"empty error key", map[string]any{"error": "", "output": "fine"}, nil, callSucceeded},
		{"hook block", map[string]any{"output": fmt.Sprintf(hookBlockPrefix+" %s: %s", "Bash", "no rm")}, nil, callFailed},
		{"normal", map[string]any{"output": "all good"}, nil, callSucceeded},
		{"nil", nil, nil, callSucceeded},
	}
	for name, r := range refusalResults() {
		cases = append(cases, struct {
			name    string
			result  map[string]any
			err     error
			outcome callOutcome
		}{name, r, nil, callNeither})
	}
	for _, c := range cases {
		text, got := classifyToolCall("Edit", nil, c.result, c.err, nil)
		if got != c.outcome {
			t.Errorf("%s: outcome=%v want %v (text %q)", c.name, got, c.outcome, text)
		}
		if got == callFailed && text == "" {
			t.Errorf("%s: failed with empty text", c.name)
		}
	}
}

// Every error message the fs tools produce is still read as an error.
func TestFsToolErrorsAreClassifiedAsErrors(t *testing.T) {
	for _, s := range []string{
		"Error: old_string not found in /a/x.go",
		"Error: empty old_string",
		"Error: command blocked by safety floor (\"rm -rf /\")",
		"Error: command timed out after 2s\npartial",
		"Error reading /a/x: permission denied",
		"Error writing /a/x: read-only file system",
		"Error reverting /a/x: no snapshot",
		"Error removing /a/x: busy",
		"Error detecting MIME type: boom",
		"error fetching URL: HTTP 404",
		"error parsing HTML: bad",
		"error section \"x\" not found",
	} {
		if _, got := classifyToolCall("Read", nil, map[string]any{"output": s}, nil, nil); got != callFailed {
			t.Errorf("%q: outcome %v, want failed", s, got)
		}
	}
}

func TestBashExitStatusDecidesABashCall(t *testing.T) {
	cases := []struct {
		name    string
		cmd     string
		out     string
		exit    bashExit
		outcome callOutcome
	}{
		{"exit 0", "git status", "On branch main", bashExit{code: 0}, callSucceeded},
		{"exit 0 output mentions error", "cat log", "error: this line is data", bashExit{code: 0}, callSucceeded},
		{"git non-repo", "git status", "fatal: not a git repository", bashExit{code: 128}, callFailed},
		{"kubectl error colon", "kubectl apply -f x", "error: unable to recognize", bashExit{code: 1}, callFailed},
		{"grep no match", "grep nomatch file.txt", "(no output)", bashExit{code: 1}, callSucceeded},
		{"rg no match", "rg x", "(no output)", bashExit{code: 1}, callSucceeded},
		{"diff differs", "diff a b", "1c1", bashExit{code: 1}, callSucceeded},
		{"test false", "test -f x", "(no output)", bashExit{code: 1}, callSucceeded},
		{"bracket false", "[ -f x ]", "(no output)", bashExit{code: 1}, callSucceeded},
		{"grep bad regex", "grep '[' file.txt", "grep: Unmatched [", bashExit{code: 2}, callFailed},
		{"wrapped grep", "sudo grep x f", "(no output)", bashExit{code: 1}, callSucceeded},
		{"timeout", "sleep 9", "Error: command timed out after 1s", bashExit{code: -1, timedOut: true}, callFailed},
		{"grep timeout", "grep x f", "Error: command timed out after 1s", bashExit{code: 1, timedOut: true}, callFailed},
	}
	for _, c := range cases {
		e := c.exit
		text, got := classifyToolCall("Bash", map[string]any{"command": c.cmd}, map[string]any{"output": c.out}, nil, &e)
		if got != c.outcome {
			t.Errorf("%s: outcome %v want %v", c.name, got, c.outcome)
		}
		if got == callFailed && text != c.out {
			t.Errorf("%s: error text %q, want the output %q", c.name, text, c.out)
		}
	}
	// A refused Bash call never ran: neither, whatever a stale status says.
	e := bashExit{code: 0}
	if _, got := classifyToolCall("Bash", nil, refusalResults()["gate deny"], nil, &e); got != callNeither {
		t.Fatalf("refusal with a status: %v", got)
	}
	// No status recorded ⇒ the text decides (CLI examples, observer absent).
	if _, got := classifyToolCall("Bash", nil, map[string]any{"output": "fatal: not a git repository"}, nil, nil); got != callSucceeded {
		t.Fatalf("no status: %v", got)
	}
}
