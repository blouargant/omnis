// Conversion helpers between ADK/genai types and the JSON shapes used by
// OpenAI and Anthropic.
package llm

import (
	"encoding/json"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/model"
)

// schemaToJSON converts a *genai.Schema into a JSON-Schema-compatible
// map[string]any. genai.Type is upper-case (e.g. "STRING") whereas JSON
// Schema expects lower-case ("string").
func schemaToJSON(s *genai.Schema) map[string]any {
	if s == nil {
		// Both providers want at least an object schema for tool params.
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	out := map[string]any{}
	if s.Type != "" {
		out["type"] = strings.ToLower(string(s.Type))
	}
	if s.Description != "" {
		out["description"] = s.Description
	}
	if len(s.Enum) > 0 {
		anyEnum := make([]any, len(s.Enum))
		for i, e := range s.Enum {
			anyEnum[i] = e
		}
		out["enum"] = anyEnum
	}
	if s.Format != "" {
		out["format"] = s.Format
	}
	if s.Items != nil {
		out["items"] = schemaToJSON(s.Items)
	}
	if len(s.Properties) > 0 {
		props := map[string]any{}
		for k, v := range s.Properties {
			props[k] = schemaToJSON(v)
		}
		out["properties"] = props
	}
	if len(s.Required) > 0 {
		req := make([]any, len(s.Required))
		for i, r := range s.Required {
			req[i] = r
		}
		out["required"] = req
	}
	if len(s.AnyOf) > 0 {
		any := make([]any, len(s.AnyOf))
		for i, a := range s.AnyOf {
			any[i] = schemaToJSON(a)
		}
		out["anyOf"] = any
	}
	if s.Minimum != nil {
		out["minimum"] = *s.Minimum
	}
	if s.Maximum != nil {
		out["maximum"] = *s.Maximum
	}
	// Both OpenAI/Anthropic require type:object at the root for tool args.
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if out["type"] == "object" {
		if _, ok := out["properties"]; !ok {
			out["properties"] = map[string]any{}
		}
	}
	return out
}

// toolDecls flattens req.Config.Tools into a slice of FunctionDeclaration.
func toolDecls(cfg *genai.GenerateContentConfig) []*genai.FunctionDeclaration {
	if cfg == nil {
		return nil
	}
	var out []*genai.FunctionDeclaration
	for _, t := range cfg.Tools {
		out = append(out, t.FunctionDeclarations...)
	}
	return out
}

// systemText extracts plain text from a SystemInstruction Content.
func systemText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var sb strings.Builder
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// systemTextFromReq pulls the system instruction text off a request.
func systemTextFromReq(req *model.LLMRequest) string {
	if req == nil || req.Config == nil {
		return ""
	}
	return systemText(req.Config.SystemInstruction)
}

// jsonString marshals v to a stable JSON string; used for tool arguments
// being sent to the model.
func jsonString(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// argsFromJSON parses a JSON object into map[string]any. Empty / invalid
// input returns an empty map rather than failing the whole turn.
func argsFromJSON(s string) map[string]any {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{"_raw": s}
	}
	return m
}

// renderFunctionResponse converts a *genai.FunctionResponse into the plain
// text payload that OpenAI / Anthropic tool messages expect.
func renderFunctionResponse(r *genai.FunctionResponse) string {
	if r == nil {
		return ""
	}
	if r.Response == nil {
		return ""
	}
	// If the tool put its payload under "result"/"output", surface that
	// directly to keep prompts compact.
	for _, k := range []string{"result", "output", "content"} {
		if v, ok := r.Response[k]; ok {
			switch s := v.(type) {
			case string:
				return s
			default:
				return jsonString(v)
			}
		}
	}
	return jsonString(r.Response)
}
