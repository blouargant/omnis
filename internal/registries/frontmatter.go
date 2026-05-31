package registries

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// SkillNameRe is the validation pattern enforced on installed skill directory
// names (and on the `name:` field of SKILL.md frontmatter).
var SkillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Frontmatter mirrors the YAML block at the top of a SKILL.md file.
type Frontmatter struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Metadata    map[string]interface{} `yaml:"metadata"`
	// Commands and Permissions are dependency lists, mirroring how an
	// agent.json declares skills/mcp_servers: each name is resolved from a
	// configured commands / permissions registry and installed when the skill
	// itself is installed, so a skill arrives with everything it needs.
	Commands    []string `yaml:"commands"`
	Permissions []string `yaml:"permissions"`
}

// Author extracts metadata.author, returning "" when absent.
func (fm Frontmatter) Author() string {
	if fm.Metadata == nil {
		return ""
	}
	s, _ := fm.Metadata["author"].(string)
	return s
}

// Tags extracts metadata.tags, returning nil when absent or malformed.
func (fm Frontmatter) Tags() []string {
	if fm.Metadata == nil {
		return nil
	}
	raw, ok := fm.Metadata["tags"]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// ParseFrontmatter extracts the YAML front matter delimited by `---` lines
// at the top of a SKILL.md file.
func ParseFrontmatter(content []byte) (Frontmatter, error) {
	s := string(content)
	s = strings.TrimLeft(s, "\r\n")
	if !strings.HasPrefix(s, "---") {
		return Frontmatter{}, fmt.Errorf("no YAML frontmatter (missing opening ---)")
	}
	rest := s[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return Frontmatter{}, fmt.Errorf("unclosed frontmatter (missing closing ---)")
	}
	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(rest[:idx]), &fm); err != nil {
		return Frontmatter{}, fmt.Errorf("invalid frontmatter YAML: %w", err)
	}
	return fm, nil
}
