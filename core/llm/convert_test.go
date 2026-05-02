package llm

import (
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

func TestSchemaToJSONAndToolDecls(t *testing.T) {
	t.Parallel()

	minimum := float64(1)
	maximum := float64(5)
	schema := &genai.Schema{
		Type:        genai.TypeObject,
		Description: "input",
		Properties: map[string]*genai.Schema{
			"name": {Type: genai.TypeString},
			"count": {Type: genai.TypeInteger, Minimum: &minimum, Maximum: &maximum},
		},
		Required: []string{"name"},
	}
	got := schemaToJSON(schema)
	if got["type"] != "object" {
		t.Fatalf("schemaToJSON(type) = %v", got["type"])
	}
	props := got["properties"].(map[string]any)
	if props["name"].(map[string]any)["type"] != "string" {
		t.Fatalf("name property = %+v", props["name"])
	}
	if props["count"].(map[string]any)["minimum"] != minimum {
		t.Fatalf("count property = %+v", props["count"])
	}

	decl := &genai.FunctionDeclaration{Name: "demo", Parameters: schema}
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{decl}}}}}
	decls := toolDecls(req.Config)
	if len(decls) != 1 || decls[0].Name != "demo" {
		t.Fatalf("toolDecls() = %+v", decls)
	}
}

func TestSystemTextArgsAndFunctionResponseHelpers(t *testing.T) {
	t.Parallel()

	content := &genai.Content{Parts: []*genai.Part{{Text: "alpha"}, {Text: "beta"}}}
	if got := systemText(content); got != "alphabeta" {
		t.Fatalf("systemText() = %q", got)
	}
	req := &model.LLMRequest{Config: &genai.GenerateContentConfig{SystemInstruction: content}}
	if got := systemTextFromReq(req); got != "alphabeta" {
		t.Fatalf("systemTextFromReq() = %q", got)
	}
	if got := jsonString(map[string]any{"a": 1}); got != "{\"a\":1}" {
		t.Fatalf("jsonString() = %q", got)
	}
	if got := argsFromJSON("not-json"); got["_raw"] != "not-json" {
		t.Fatalf("argsFromJSON(invalid) = %+v", got)
	}
	if got := argsFromJSON("{\"a\":1}"); got["a"].(float64) != 1 {
		t.Fatalf("argsFromJSON(valid) = %+v", got)
	}

	resp := &genai.FunctionResponse{Name: "tool", Response: map[string]any{"result": "ok"}}
	if got := renderFunctionResponse(resp); got != "ok" {
		t.Fatalf("renderFunctionResponse(result) = %q", got)
	}
	resp = &genai.FunctionResponse{Name: "tool", Response: map[string]any{"other": map[string]any{"a": 1}}}
	if got := renderFunctionResponse(resp); got != "{\"other\":{\"a\":1}}" {
		t.Fatalf("renderFunctionResponse(other) = %q", got)
	}
}