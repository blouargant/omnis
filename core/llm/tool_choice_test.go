package llm

import (
	"encoding/json"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func toolChoiceRequestBody(t *testing.T, mode genai.FunctionCallingConfigMode, withTools bool) string {
	t.Helper()
	req := cacheTestRequest()
	if withTools {
		req.Config.Tools = []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "WebFetch"}}}}
	} else {
		req.Config.Tools = nil
	}
	if mode != "" {
		req.Config.ToolConfig = &genai.ToolConfig{FunctionCallingConfig: &genai.FunctionCallingConfig{Mode: mode}}
	}
	body, err := json.Marshal((&openAI{model: "m"}).buildRequest(req, false))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Mode NONE keeps the tools declared (a history holding tool calls needs them)
// but forbids calling one: the per-call cap's final round relies on it.
func TestBuildRequestToolModeNoneSendsToolChoiceNone(t *testing.T) {
	if b := toolChoiceRequestBody(t, genai.FunctionCallingConfigModeNone, true); !strings.Contains(b, `"tool_choice":"none"`) {
		t.Fatalf("want tool_choice none in %s", b)
	}
}

func TestBuildRequestOmitsToolChoiceOtherwise(t *testing.T) {
	for name, b := range map[string]string{
		"no tool config":   toolChoiceRequestBody(t, "", true),
		"auto mode":        toolChoiceRequestBody(t, genai.FunctionCallingConfigModeAuto, true),
		"none but no tool": toolChoiceRequestBody(t, genai.FunctionCallingConfigModeNone, false),
	} {
		if strings.Contains(b, "tool_choice") {
			t.Errorf("%s: unexpected tool_choice in %s", name, b)
		}
	}
}
