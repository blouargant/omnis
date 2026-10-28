// Package claudeplugins discovers Claude Code plugins installed on disk — the
// directories an Agent-Skills tool such as iapcli drops under
// <agent-skills dir>/claude/ (/etc/agentskills/claude by default) — and reads
// their agents, skills and hooks declaration.
//
// The layout it understands is Claude Code's marketplace layout:
//
//	<root>/.claude-plugin/marketplace.json     lists the plugins ("plugins[]")
//	<root>/<plugin>/.claude-plugin/plugin.json name, version, description
//	<root>/<plugin>/agents/*.md                sub-agents (Claude agent markdown)
//	<root>/<plugin>/skills/<name>/SKILL.md     skills
//	<root>/<plugin>/hooks/hooks.json           lifecycle hooks
//
// Discovery only reads and reports: it never writes, never runs a hook and
// never follows a marketplace entry out of its root. Anything unreadable
// becomes a warning and is skipped, so one broken plugin cannot take the rest
// down.
package claudeplugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/blouargant/omnis/internal/claudeformat"
)

// Agent is one sub-agent declared by a plugin (agents/<file>.md).
type Agent struct {
	Name        string
	Description string
	ModelAlias  string // the raw `model` frontmatter value ("sonnet", "haiku", "inherit")
	Body        string // the markdown body: the agent's system prompt
	Tools       []string
	Skills      []string
}

// Plugin is one Claude Code plugin found under the root.
type Plugin struct {
	Name        string
	Version     string
	Description string
	Dir         string  // absolute, symlinks resolved
	Agents      []Agent // sorted by name
	SkillsDir   string  // <Dir>/skills, or "" when the plugin has none
	SkillNames  []string
	HooksPath   string // <Dir>/hooks/hooks.json, or "" when absent
}

// namePattern is what a plugin name must look like: it becomes a namespace
// (`<plugin>:<agent>`) and a path segment downstream, so anything else is
// refused rather than sanitised.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type marketplace struct {
	Plugins []marketplaceEntry `json:"plugins"`
}

type marketplaceEntry struct {
	Name        string          `json:"name"`
	Source      json.RawMessage `json:"source"`
	Description string          `json:"description"`
}

type pluginManifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// Discover reads <root>/.claude-plugin/marketplace.json and loads every
// plugin it lists whose source is a relative path resolving inside root. A
// missing root (or a root with no marketplace) yields nil, nil.
func Discover(root string) (plugins []Plugin, warnings []string) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, []string{fmt.Sprintf("claude plugins: %v", err)}
	}
	rootDir, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("claude plugins: %v", err)}
	}

	mpPath := filepath.Join(rootDir, ".claude-plugin", "marketplace.json")
	data, err := os.ReadFile(mpPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("claude plugins: read %s: %v", mpPath, err)}
	}
	var mp marketplace
	if err := json.Unmarshal(data, &mp); err != nil {
		return nil, []string{fmt.Sprintf("claude plugins: parse %s: %v", mpPath, err)}
	}

	seen := map[string]bool{}
	for i, entry := range mp.Plugins {
		label := entry.Name
		if label == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		p, warns := loadPlugin(rootDir, entry)
		for _, w := range warns {
			warnings = append(warnings, fmt.Sprintf("claude plugin %q: %s", label, w))
		}
		if p == nil {
			continue
		}
		if seen[p.Name] {
			warnings = append(warnings, fmt.Sprintf("claude plugin %q: name %q is already provided by an earlier entry; skipped", label, p.Name))
			continue
		}
		seen[p.Name] = true
		plugins = append(plugins, *p)
	}
	return plugins, warnings
}

// loadPlugin loads one marketplace entry. It returns a nil plugin (with the
// reason among the warnings) when the entry cannot be used at all; a plugin
// whose agents or skills partly fail still loads, with a warning per failure.
func loadPlugin(rootDir string, entry marketplaceEntry) (*Plugin, []string) {
	var source string
	if err := json.Unmarshal(entry.Source, &source); err != nil || strings.TrimSpace(source) == "" {
		return nil, []string{"source is not a relative path; only local plugins are supported"}
	}
	dir, err := resolveInside(rootDir, source)
	if err != nil {
		return nil, []string{err.Error()}
	}

	m, err := readManifest(dir)
	if err != nil {
		return nil, []string{err.Error()}
	}
	name := strings.TrimSpace(m.Name)
	if name == "" {
		name = strings.TrimSpace(entry.Name)
	}
	if !namePattern.MatchString(name) {
		return nil, []string{fmt.Sprintf("name %q is not a valid plugin name (want %s)", name, namePattern)}
	}
	desc := m.Description
	if desc == "" {
		desc = entry.Description
	}

	p := &Plugin{Name: name, Version: m.Version, Description: desc, Dir: dir}
	var warns []string
	p.Agents, warns = loadAgents(dir)
	p.SkillsDir, p.SkillNames = loadSkills(dir)
	hooks := filepath.Join(dir, "hooks", "hooks.json")
	if st, err := os.Stat(hooks); err == nil && st.Mode().IsRegular() {
		p.HooksPath = hooks
	}
	return p, warns
}

// resolveInside joins source onto root and returns the real directory it names,
// refusing an absolute source, one that climbs out of root lexically, and one
// that only gets out through a symlink.
func resolveInside(root, source string) (string, error) {
	src := filepath.FromSlash(source)
	if filepath.IsAbs(src) {
		return "", fmt.Errorf("source %q is absolute; it must be relative to the marketplace root", source)
	}
	joined := filepath.Join(root, src)
	if !within(root, joined) {
		return "", fmt.Errorf("source %q resolves outside the marketplace root", source)
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("source %q: %v", source, err)
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("source %q resolves outside the marketplace root", source)
	}
	if st, err := os.Stat(resolved); err != nil || !st.IsDir() {
		return "", fmt.Errorf("source %q is not a directory", source)
	}
	return resolved, nil
}

// within reports whether path is root itself or lies beneath it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readManifest reads <dir>/.claude-plugin/plugin.json. A plugin without one is
// legal (the marketplace entry names it); one that is present but unreadable
// is an error, since its name and version cannot be trusted.
func readManifest(dir string) (pluginManifest, error) {
	var m pluginManifest
	path := filepath.Join(dir, ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return m, nil
		}
		return m, fmt.Errorf("read plugin.json: %v", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse plugin.json: %v", err)
	}
	return m, nil
}

// loadAgents parses <dir>/agents/*.md, one warning per file that fails.
func loadAgents(dir string) ([]Agent, []string) {
	entries, err := os.ReadDir(filepath.Join(dir, "agents"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("read agents/: %v", err)}
	}
	var agents []Agent
	var warns []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "agents", e.Name()))
		if err != nil {
			warns = append(warns, fmt.Sprintf("agents/%s: %v", e.Name(), err))
			continue
		}
		def, err := claudeformat.ParseMarkdown(data)
		if err != nil {
			warns = append(warns, fmt.Sprintf("agents/%s: %v", e.Name(), err))
			continue
		}
		agents = append(agents, Agent{
			Name:        def.Name,
			Description: def.Description,
			ModelAlias:  def.ModelAlias,
			Body:        def.Instruction,
			Tools:       def.Tools,
			Skills:      def.Skills,
		})
	}
	sort.SliceStable(agents, func(i, j int) bool { return agents[i].Name < agents[j].Name })
	return agents, warns
}

// loadSkills lists <dir>/skills/<name>/SKILL.md. It returns "" for the
// directory when the plugin has no skills/ at all.
func loadSkills(dir string) (skillsDir string, names []string) {
	sd := filepath.Join(dir, "skills")
	entries, err := os.ReadDir(sd)
	if err != nil {
		return "", nil
	}
	for _, e := range entries {
		if st, err := os.Stat(filepath.Join(sd, e.Name(), "SKILL.md")); err == nil && st.Mode().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return sd, names
}
