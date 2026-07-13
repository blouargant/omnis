package agent

import (
	"strings"
	"testing"

	"google.golang.org/adk/tool"
)

// shaperTestTool is a minimal tool.Tool whose only interesting property is its
// name — the shaper keys on that and on the result map, nothing else.
type shaperTestTool struct {
	tool.Tool
	name string
}

func (t shaperTestTool) Name() string { return t.name }

// The runner-level shaper never reached sub-agents (runner plugins do not cross
// into agenttool's private runner), so a fetched web page landed WHOLE in a
// sub-agent's context and was re-billed on every subsequent model call of its
// own flow loop — cost quadratic in tool calls. That is how research_critic
// reached 9.1M prompt tokens. This callback is what caps it.
func TestSubAgentShaperCapsBigToolOutput(t *testing.T) {
	cb := subAgentShaperCallback()
	if cb == nil {
		t.Fatal("no shaper callback for sub-agents")
	}

	page := strings.Repeat("lorem ipsum dolor sit amet\n", 8000) // ~216k chars
	result := map[string]any{"content": page}

	out, err := cb(nil, shaperTestTool{name: "WebFetch"}, nil, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("a 216k-char WebFetch result was not shaped")
	}
	got := out["content"].(string)
	if len(got) >= len(page) {
		t.Fatalf("result not capped: %d chars in, %d out", len(page), len(got))
	}
	if len(got) > shaperMaxChars+2000 { // +slack for the truncation notice
		t.Fatalf("shaped result is %d chars, want ~<= %d", len(got), shaperMaxChars)
	}
	// The caller's map must not be mutated in place.
	if len(result["content"].(string)) != len(page) {
		t.Fatal("shaper mutated the original result map")
	}
}

// A small result must pass through untouched (nil = "no change"), so the shaper
// is a no-op for the overwhelming majority of calls.
func TestSubAgentShaperNoOpOnSmallOutput(t *testing.T) {
	cb := subAgentShaperCallback()
	out, err := cb(nil, shaperTestTool{name: "WebFetch"}, nil, map[string]any{"content": "short"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatalf("small result was rewritten: %v", out)
	}
}

// ask_user is exempt: truncating a question would corrupt the very prompt the
// user has to answer.
func TestSubAgentShaperRespectsExemptions(t *testing.T) {
	cb := subAgentShaperCallback()
	big := strings.Repeat("q", shaperMaxChars*2)
	out, err := cb(nil, shaperTestTool{name: "ask_user"}, nil, map[string]any{"prompt": big}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatal("ask_user output was shaped; its prompt must reach the user intact")
	}
}
