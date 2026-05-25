package agent

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// InstructionFrontmatter mirrors the optional YAML block at the top of an
// agent's instruction.md. All fields are optional; an empty value means
// "not set" and leaves the corresponding agent.json field untouched.
//
// Frontmatter is a Claude Code–style convenience for shipping an agent in a
// single markdown file: the body holds the system instruction and the
// frontmatter declares tools / model recommendations / skills / mcp servers.
type InstructionFrontmatter struct {
	Name        string
	Description string
	Model       string
	Tools       []string
	Skills      []string
	MCPServers  []string
}

// HasAny reports whether the frontmatter carried any field worth applying.
func (fm InstructionFrontmatter) HasAny() bool {
	return fm.Name != "" || fm.Description != "" || fm.Model != "" ||
		len(fm.Tools) > 0 || len(fm.Skills) > 0 || len(fm.MCPServers) > 0
}

// ParseInstructionMarkdown extracts the optional YAML frontmatter delimited
// by `---` lines at the top of an instruction.md file. It returns the
// parsed frontmatter and the markdown body with the frontmatter block
// removed. When the file has no frontmatter (or the YAML is malformed) the
// returned struct is zero-valued and body equals the original content.
func ParseInstructionMarkdown(content []byte) (InstructionFrontmatter, string) {
	s := string(content)
	trimmed := strings.TrimLeft(s, "\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return InstructionFrontmatter{}, s
	}
	rest := trimmed[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return InstructionFrontmatter{}, s
	}
	yamlBody := rest[:end]
	after := rest[end:]
	after = strings.TrimPrefix(after, "\n---")
	body := strings.TrimLeft(after, "\n")

	var fm struct {
		Name        string         `yaml:"name"`
		Description string         `yaml:"description"`
		Model       string         `yaml:"model"`
		Tools       fmCommaOrList  `yaml:"tools"`
		Skills      fmScalarOrList `yaml:"skills"`
		MCPServers  fmScalarOrList `yaml:"mcpServers"`
	}
	if err := yaml.Unmarshal([]byte(yamlBody), &fm); err != nil {
		return InstructionFrontmatter{}, s
	}
	return InstructionFrontmatter{
		Name:        strings.TrimSpace(fm.Name),
		Description: strings.TrimSpace(fm.Description),
		Model:       strings.TrimSpace(fm.Model),
		Tools:       fm.Tools,
		Skills:      fm.Skills,
		MCPServers:  fm.MCPServers,
	}, body
}

// StripInstructionFrontmatter returns the body of an instruction.md with
// any YAML frontmatter block removed.
func StripInstructionFrontmatter(content []byte) string {
	_, body := ParseInstructionMarkdown(content)
	return body
}

// fmCommaOrList accepts either a comma-separated string ("Read, Write, Edit")
// or a YAML sequence.
type fmCommaOrList []string

func (t *fmCommaOrList) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		for _, p := range strings.Split(value.Value, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				*t = append(*t, p)
			}
		}
		return nil
	}
	var list []string
	if err := value.Decode(&list); err != nil {
		return err
	}
	*t = list
	return nil
}

// fmScalarOrList accepts a single scalar value or a YAML sequence and
// normalises both into a []string.
type fmScalarOrList []string

func (s *fmScalarOrList) UnmarshalYAML(value *yaml.Node) error {
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
