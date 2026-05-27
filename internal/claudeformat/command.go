package claudeformat

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// CommandDef is the normalised representation of a Claude Code slash-command
// markdown file. The filename (without .md) supplies Name when frontmatter
// omits it; the body (after the frontmatter block) becomes Prompt.
//
// Supported frontmatter fields (all optional):
//
//	---
//	name: foo
//	description: Run the foo workflow
//	argument-hint: <target>
//	---
//	Prompt body with $1, $2, $* placeholders…
type CommandDef struct {
	Name         string
	Description  string
	ArgumentHint string
	Prompt       string
}

// ParseCommandMarkdown parses a Claude Code slash-command markdown file. The
// frontmatter block is optional — a file with no leading "---" is treated as
// a pure-prompt command (Name must then be supplied by the caller, e.g. from
// the filename).
func ParseCommandMarkdown(content []byte) (*CommandDef, error) {
	const sep = "---"
	s := string(content)
	trimmed := strings.TrimSpace(s)

	// No frontmatter: the whole file is the prompt.
	if !strings.HasPrefix(trimmed, sep) {
		return &CommandDef{Prompt: strings.TrimRight(s, "\n")}, nil
	}

	rest := strings.TrimPrefix(trimmed, sep)
	end := strings.Index(rest, "\n---")
	var yamlBody, body string
	if end >= 0 {
		yamlBody = rest[:end]
		after := rest[end:]
		after = strings.TrimPrefix(after, "\n---")
		after = strings.TrimPrefix(after, "---")
		body = strings.TrimLeft(after, "\n")
	} else {
		yamlBody = rest
	}

	var fm struct {
		Name         string       `yaml:"name"`
		Description  string       `yaml:"description"`
		ArgumentHint flexibleText `yaml:"argument-hint"`
	}
	if err := yaml.Unmarshal([]byte(yamlBody), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter: %w", err)
	}

	return &CommandDef{
		Name:         strings.TrimSpace(fm.Name),
		Description:  strings.TrimSpace(fm.Description),
		ArgumentHint: strings.TrimSpace(string(fm.ArgumentHint)),
		Prompt:       strings.TrimRight(body, "\n"),
	}, nil
}

// flexibleText accepts either a YAML scalar or a sequence. In the real
// Claude Code corpus argument-hint is often written as a placeholder list
// (e.g. `[name] [target]`) which YAML treats as a flow sequence — without
// this fallback the whole frontmatter unmarshal would fail and we'd lose
// the description too.
type flexibleText string

func (f *flexibleText) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*f = flexibleText(value.Value)
	case yaml.SequenceNode:
		var parts []string
		if err := value.Decode(&parts); err != nil {
			return err
		}
		*f = flexibleText(strings.Join(parts, " "))
	default:
		// Other kinds (map, alias) aren't expected here — ignore them so a
		// quirky entry doesn't sink the whole parse.
		*f = ""
	}
	return nil
}
