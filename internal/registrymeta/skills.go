// Package registrymeta reads and lists the on-disk metadata for skills
// and agents installed under a config search chain. Both the HTTP server
// and the embedded TUI use it as the single source of truth for what's
// installed and where it lives in the layer hierarchy.
//
// This package is intentionally read/write of on-disk files only — it
// does not parse the runtime agents.json or apply skills to agents at
// runtime. For that, see internal/skills and agent/.
package registrymeta

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/blouargant/omnis/internal/paths"
)

// Frontmatter is the YAML envelope at the top of a SKILL.md file.
type Frontmatter struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Metadata    map[string]interface{} `yaml:"metadata"`
}

// Author returns the optional author field stored in metadata.
func (fm Frontmatter) Author() string {
	if fm.Metadata == nil {
		return ""
	}
	s, _ := fm.Metadata["author"].(string)
	return s
}

// Tags returns the optional tags field, normalised to a string slice.
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

// ParseFrontmatter extracts the YAML envelope from a SKILL.md.
// Returns an error when the document has no frontmatter or it is
// malformed. The remainder of the file (the markdown body) is not
// returned — callers that need it have the raw bytes already.
func ParseFrontmatter(content []byte) (Frontmatter, error) {
	s := strings.TrimLeft(string(content), "\r\n")
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

// SkillInfo is the resolved metadata for one installed skill.
type SkillInfo struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Author      string                 `json:"author,omitempty"`
	Tags        []string               `json:"tags,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	MTime       time.Time              `json:"mtime"`
	Size        int64                  `json:"size"`
	// LinkedIn holds the agent names that reference this skill. Filled in
	// by the caller (after cross-referencing agents.json); the lister
	// always returns an empty slice.
	LinkedIn []string `json:"linked_in"`
	// Source is the layer the skill was found in ("local"/"user"/"system").
	Source string `json:"source"`
	// Path is the absolute path to the SKILL.md file. Used by the TUI
	// for the detail viewer; the web UI ignores it.
	Path string `json:"-"`
	// Dir is the absolute path to the skill's directory (parent of
	// SKILL.md). Used by deletion + asset listing.
	Dir string `json:"-"`
}

// ListSkills scans every directory in precedence order and returns the
// merged metadata. The first occurrence of a skill name wins (matches
// the config chain layering). A missing directory is not an error.
func ListSkills(dirs ...string) ([]SkillInfo, error) {
	seen := make(map[string]bool)
	var out []SkillInfo
	for _, registryDir := range dirs {
		entries, err := os.ReadDir(registryDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			skillMD := filepath.Join(registryDir, e.Name(), "SKILL.md")
			data, err := os.ReadFile(skillMD)
			if err != nil {
				continue
			}
			seen[e.Name()] = true
			st, _ := os.Stat(skillMD)
			fm, _ := ParseFrontmatter(data)
			displayName := fm.Name
			if displayName == "" {
				displayName = e.Name()
			}
			info := SkillInfo{
				Name:        displayName,
				Description: fm.Description,
				Author:      fm.Author(),
				Tags:        fm.Tags(),
				Metadata:    fm.Metadata,
				Size:        int64(len(data)),
				LinkedIn:    []string{},
				Source:      paths.Layer(registryDir),
				Path:        skillMD,
				Dir:         filepath.Join(registryDir, e.Name()),
			}
			if st != nil {
				info.MTime = st.ModTime()
			}
			out = append(out, info)
		}
	}
	if out == nil {
		return []SkillInfo{}, nil
	}
	return out, nil
}

// FindSkillDir returns the absolute path of <name>'s skill directory in
// the highest-precedence layer that contains it. Empty string when the
// skill is not installed anywhere.
func FindSkillDir(name string) string {
	for _, dir := range paths.SkillsAllSearchDirs() {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(filepath.Join(candidate, "SKILL.md")); err == nil {
			return candidate
		}
	}
	return ""
}
