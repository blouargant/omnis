package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/blouargant/omnis/core/permissions"
)

func Test_classifyToolFailure(t *testing.T) {
	cases := []struct {
		name   string
		result map[string]any
		err    error
		failed bool
	}{
		{"go error", nil, errors.New("boom"), true},
		{"Error prefix", map[string]any{"output": "Error: no such file"}, nil, true},
		{"error prefix", map[string]any{"output": "error fetching URL: HTTP 500"}, nil, true},
		{"error key", map[string]any{"error": "bad"}, nil, true},
		{"empty error key", map[string]any{"error": "", "output": "fine"}, nil, false},
		{"hook block", map[string]any{"output": fmt.Sprintf(hookBlockPrefix+" %s: %s", "Bash", "no rm")}, nil, true},
		{"hook declined", map[string]any{"output": fmt.Sprintf(hookBlockPrefix+" %s: %s %s", "Bash", "why", hookDeclinedSuffix)}, nil, false},
		{"hook dontAsk", map[string]any{"output": fmt.Sprintf(hookBlockPrefix+" %s: %s: x", "Bash", hookDontAskMarker)}, nil, false},
		{"hook unattended", map[string]any{"output": fmt.Sprintf(hookBlockPrefix+" %s: %s (%s, so x)", "Bash", "why", hookUnattendedMarker)}, nil, false},
		{"auto block", map[string]any{"output": permissions.AutoBlockMessage("Bash", "nope")}, nil, false},
		{"gate deny", map[string]any{"output": fmt.Sprintf(permissions.DeniedPrefix+" %s: %s", "Edit", "denied")}, nil, false},
		{"budget halt", map[string]any{"output": budgetHaltNotice}, nil, false},
		{"agent cap", map[string]any{"output": agentCapNotice("web_agent")}, nil, false},
		{"call cap", map[string]any{"output": callCapNotice("web_agent", 20)}, nil, false},
		{"normal", map[string]any{"output": "all good"}, nil, false},
		{"nil", nil, nil, false},
	}
	for _, c := range cases {
		text, failed := classifyToolFailure(c.result, c.err)
		if failed != c.failed {
			t.Errorf("%s: failed=%v want %v (text %q)", c.name, failed, c.failed, text)
		}
		if failed && text == "" {
			t.Errorf("%s: failed with empty text", c.name)
		}
	}
}
