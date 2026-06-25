// Package claudeformat parses Claude Code sub-agent definitions (markdown and
// JSON) and converts them into omnis AgentEntry values.
//
// Supported input formats:
//
//  1. Markdown with YAML frontmatter — the canonical Claude Code file format
//     stored in ~/.claude/agents/<name>.md or .claude/agents/<name>.md:
//
//     ---
//     name: code-reviewer
//     description: Expert code reviewer
//     tools: Read, Glob, Grep, Bash
//     model: sonnet
//     skills: [pdf]
//     mcpServers: [my-server]
//     ---
//     System prompt markdown body…
//
//  2. JSON map — the Claude Code CLI --agents flag format, one or more agents
//     keyed by name:
//
//     {
//     "code-reviewer": {
//     "description": "…",
//     "prompt": "…",
//     "tools": ["Read", "Grep"],
//     "model": "sonnet"
//     }
//     }
//
//  3. JSON object with a "name" field — a single agent as a plain object.
//
// Fields that have no omnis equivalent (permissionMode, maxTurns, hooks,
// memory, background, effort, isolation, color, initialPrompt, …) are
// silently ignored.
package claudeformat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentDef is the normalised representation produced after parsing either
// a markdown or JSON Claude Code agent definition.
type AgentDef struct {
	Name        string
	Description string
	Tools       []string
	Model       string
	Skills      []string
	MCPServers  []string
	Instruction string
}

// Parse autodetects the format (markdown vs JSON) and returns one or more
// agent definitions. Markdown always produces exactly one entry.
func Parse(content []byte) ([]*AgentDef, error) {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty content")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		return ParseJSON(content)
	}
	def, err := ParseMarkdown(content)
	if err != nil {
		return nil, err
	}
	return []*AgentDef{def}, nil
}

// ParseMarkdown parses a Claude Code markdown agent file (YAML frontmatter +
// markdown body).
func ParseMarkdown(content []byte) (*AgentDef, error) {
	const sep = "---"
	s := string(content)

	// Require a leading "---" delimiter.
	if !strings.HasPrefix(strings.TrimSpace(s), sep) {
		return nil, fmt.Errorf("not a Claude Code markdown agent: missing frontmatter delimiter")
	}

	// Trim the leading "---\n" and find the closing "---".
	rest := strings.TrimSpace(s)
	rest = strings.TrimPrefix(rest, sep)
	// rest now starts at the first byte after the opening "---"
	end := strings.Index(rest, "\n---")
	if end < 0 {
		// Try "---" at end-of-string without a trailing newline.
		if strings.HasSuffix(strings.TrimSpace(rest), sep) {
			end = strings.LastIndex(rest, sep)
		}
	}
	var yamlBody, body string
	if end >= 0 {
		yamlBody = rest[:end]
		after := rest[end:]
		after = strings.TrimPrefix(after, "\n---")
		after = strings.TrimPrefix(after, "---")
		body = strings.TrimLeft(after, "\n")
	} else {
		// No closing delimiter — treat all of rest as frontmatter.
		yamlBody = rest
	}

	var fm struct {
		Name        string      `yaml:"name"`
		Description string      `yaml:"description"`
		Tools       toolsField  `yaml:"tools"`
		Model       string      `yaml:"model"`
		Skills      stringSlice `yaml:"skills"`
		MCPServers  stringSlice `yaml:"mcpServers"`
	}
	if err := yaml.Unmarshal([]byte(yamlBody), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	if fm.Name == "" {
		return nil, fmt.Errorf("frontmatter missing required 'name' field")
	}

	return &AgentDef{
		Name:        fm.Name,
		Description: fm.Description,
		Tools:       fm.Tools,
		Model:       normalizeModel(fm.Model),
		Skills:      fm.Skills,
		MCPServers:  fm.MCPServers,
		Instruction: strings.TrimRight(body, "\n"),
	}, nil
}

// ParseJSON parses the Claude Code JSON agent format. Two sub-formats are
// supported:
//   - Map format: {"agent-name": {"description": "…", "prompt": "…", …}}
//   - Single-object format: {"name": "agent-name", "description": "…", …}
func ParseJSON(content []byte) ([]*AgentDef, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	// Detect single-object format: has a "name" string key at the top level.
	if nameRaw, ok := raw["name"]; ok {
		var name string
		if err := json.Unmarshal(nameRaw, &name); err == nil && name != "" {
			def, err := parseJSONObject(name, content)
			if err != nil {
				return nil, err
			}
			return []*AgentDef{def}, nil
		}
	}

	// Map format: each key is an agent name.
	out := make([]*AgentDef, 0, len(raw))
	for name, body := range raw {
		def, err := parseJSONObject(name, body)
		if err != nil {
			return nil, fmt.Errorf("agent %q: %w", name, err)
		}
		out = append(out, def)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("JSON contained no agent definitions")
	}
	return out, nil
}

// jsonAgentObject is the common set of fields in a JSON agent definition.
type jsonAgentObject struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Prompt      string      `json:"prompt"`
	Tools       toolsField  `json:"tools"`
	Model       string      `json:"model"`
	Skills      stringSlice `json:"skills"`
	MCPServers  stringSlice `json:"mcpServers"`
}

func parseJSONObject(name string, data []byte) (*AgentDef, error) {
	var obj jsonAgentObject
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("decode agent object: %w", err)
	}
	if obj.Name != "" {
		name = obj.Name
	}
	if name == "" {
		return nil, fmt.Errorf("agent object missing name")
	}
	return &AgentDef{
		Name:        name,
		Description: obj.Description,
		Tools:       obj.Tools,
		Model:       normalizeModel(obj.Model),
		Skills:      obj.Skills,
		MCPServers:  obj.MCPServers,
		Instruction: obj.Prompt,
	}, nil
}

// Instruction returns just the instruction text (convenience accessor).
func (d *AgentDef) InstructionText() string { return d.Instruction }

// normalizeModel maps Claude Code shorthand model names to fully-qualified
// Claude model IDs. Unknown values are returned unchanged.
func normalizeModel(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "sonnet":
		return "claude-sonnet-4-6"
	case "opus":
		return "claude-opus-4-7"
	case "haiku":
		return "claude-haiku-4-5-20251001"
	case "inherit", "":
		return ""
	default:
		return m
	}
}

// --- custom YAML/JSON unmarshal helpers ---

// toolsField accepts either a comma-separated string or a YAML/JSON array.
type toolsField []string

func (t *toolsField) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		*t = splitCommaList(value.Value)
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*t = list
	return nil
}

func (t *toolsField) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*t = splitCommaList(s)
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*t = list
	return nil
}

// stringSlice accepts either a YAML/JSON string or array of strings.
type stringSlice []string

func (s *stringSlice) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		if v := strings.TrimSpace(value.Value); v != "" {
			*s = []string{v}
		}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*s = list
	return nil
}

func (s *stringSlice) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		if v := strings.TrimSpace(str); v != "" {
			*s = []string{v}
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*s = list
	return nil
}

func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
