// Package registries provides remote skill and agent registry browsing,
// installation, and per-agent linking. It is shared between the HTTP
// server's web UI handlers and the in-agent Helper tool group:
// both surfaces use the same providers (GitHub/GitLab/Gitea), the same
// remote_registries.json config file, and the same on-disk layout.
package registries

import "strings"

// Provider identifiers used in remote_registries.json.
const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
	ProviderGitea  = "gitea"
)

// Kind values a registry may serve. KindBoth is a legacy alias that expands to
// skills+agents; a registry can serve any combination of the real kinds via
// Registry.Kinds. An empty/absent kind is treated as KindSkills for backwards
// compatibility with pre-existing remote_registries.json entries.
const (
	KindSkills      = "skills"
	KindAgents      = "agents"
	KindBoth        = "both"
	KindMCP         = "mcp"
	KindA2A         = "a2a"
	KindSquads      = "squads"
	KindCommands    = "commands"
	KindPermissions = "permissions"
)

// kindSet is the set of real (non-alias) content kinds.
var kindSet = map[string]bool{
	KindSkills: true, KindAgents: true, KindMCP: true, KindA2A: true,
	KindSquads: true, KindCommands: true, KindPermissions: true,
}

// ValidKind reports whether k is a recognised content kind. The "both" alias
// is NOT valid here (it expands to skills+agents via EffectiveKinds).
func ValidKind(k string) bool { return kindSet[k] }

// Registry is one entry in remote_registries.json. A registry can serve any
// combination of content kinds. The canonical multi-kind field is Kinds; the
// legacy single-kind Kind field ("" ⇒ skills, "both" ⇒ skills+agents) is still
// read for backwards compatibility and superseded by Kinds when both are set.
type Registry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Provider string   `json:"provider,omitempty"`
	Kind     string   `json:"kind,omitempty"`  // legacy single-kind (read compat)
	Kinds    []string `json:"kinds,omitempty"` // canonical multi-kind set
	Token    string   `json:"token,omitempty"` // PAT; stored server-side, never exposed to the browser.
}

// EffectiveKinds returns the de-duplicated, validated set of content kinds this
// registry serves. It prefers the canonical Kinds slice and falls back to the
// legacy Kind string — expanding the "both" alias to skills+agents and applying
// the empty ⇒ skills default. Order is preserved; the result is never empty.
func (r Registry) EffectiveKinds() []string {
	var raw []string
	if len(r.Kinds) > 0 {
		raw = r.Kinds
	} else {
		s := strings.TrimSpace(r.Kind)
		if s == "" {
			return []string{KindSkills}
		}
		// Tolerate a legacy joined value ("skills+agents", "skills,mcp") too.
		raw = strings.FieldsFunc(s, func(c rune) bool { return c == '+' || c == ',' || c == ' ' })
	}
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	add := func(k string) {
		if k == "" || seen[k] {
			return
		}
		seen[k] = true
		out = append(out, k)
	}
	for _, k := range raw {
		k = strings.TrimSpace(k)
		if k == KindBoth {
			add(KindSkills)
			add(KindAgents)
			continue
		}
		if ValidKind(k) {
			add(k)
		}
	}
	if len(out) == 0 {
		return []string{KindSkills}
	}
	return out
}

// CanonicalKind is the "+"-joined EffectiveKinds — a stable string form used
// for display and for the semantic index's corpus hash (so adding a kind to an
// existing registry invalidates the index).
func (r Registry) CanonicalKind() string { return strings.Join(r.EffectiveKinds(), "+") }

// NormalizedKind returns the canonical joined kind set. (Kept under its
// historical name; it used to return a single kind and now returns the full
// set as a "+"-joined string.)
func (r Registry) NormalizedKind() string { return r.CanonicalKind() }

// Serves reports whether the registry exposes content of the given kind.
func (r Registry) Serves(kind string) bool {
	for _, k := range r.EffectiveKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// primaryKind collapses the served set to a single dispatch kind for the legacy
// single-kind get/install paths in tools.go: the skills+agents combo maps back
// to the historical "both" (skill-oriented) dispatch, a lone kind returns
// itself, and any other multi-kind set dispatches on its first kind (browse
// covers the rest).
func (r Registry) primaryKind() string {
	ks := r.EffectiveKinds()
	if len(ks) == 2 {
		a, b := ks[0], ks[1]
		if (a == KindSkills && b == KindAgents) || (a == KindAgents && b == KindSkills) {
			return KindBoth
		}
	}
	return ks[0]
}

// SkillInfo is one skill returned when browsing a remote registry.
type SkillInfo struct {
	Name        string   `json:"name"`
	DirPath     string   `json:"dir_path"`        // path relative to registry root, e.g. "engineering/diagnose"
	Group       string   `json:"group,omitempty"` // intermediate dirs before the skill dir
	Description string   `json:"description,omitempty"`
	Author      string   `json:"author,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Installed   bool     `json:"installed"`
}

// MCPToolInfo is one MCP server returned when browsing a remote MCP registry.
type MCPToolInfo struct {
	Name        string `json:"name"`
	DirPath     string `json:"dir_path"`
	Group       string `json:"group,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"` // "stdio" or "http"
	HasReadme   bool   `json:"has_readme,omitempty"`
	Installed   bool   `json:"installed"`
}

// SquadInfo is one squad returned when browsing a remote squad registry.
type SquadInfo struct {
	Name        string   `json:"name"`
	DirPath     string   `json:"dir_path"`
	Group       string   `json:"group,omitempty"`
	Description string   `json:"description,omitempty"`
	Leader      string   `json:"leader,omitempty"`
	Members     []string `json:"members,omitempty"`
	Installed   bool     `json:"installed"`
}

// CommandInfo is one slash-command returned when browsing a remote command
// registry. Each command corresponds to a single Claude Code-style markdown
// file: filename (without .md) is the command name; YAML frontmatter (if any)
// provides description/argument-hint; body is the prompt template.
type CommandInfo struct {
	Name         string `json:"name"`
	DirPath      string `json:"dir_path"`        // path relative to registry root, e.g. "commands/foo.md"
	Group        string `json:"group,omitempty"` // intermediate dirs before the file
	Description  string `json:"description,omitempty"`
	ArgumentHint string `json:"argument_hint,omitempty"`
	Installed    bool   `json:"installed"`
}

// PermissionInfo is one permission rule-set returned when browsing a remote
// permissions registry. Each item is a directory containing a permissions.json
// (the same permissions.{allow, ask, deny} shape as the local permissions.json;
// old always_* files are auto-converted on install); the directory leaf is the
// rule-set name. Installing merges its rules into the user's permissions.json.
type PermissionInfo struct {
	Name        string `json:"name"`
	DirPath     string `json:"dir_path"`        // path to the permissions.json, relative to registry root
	Group       string `json:"group,omitempty"` // intermediate dirs before the rule-set dir
	Description string `json:"description,omitempty"`
	Rules       int    `json:"rules,omitempty"` // total rule count across the three tiers
	Installed   bool   `json:"installed"`
}

// A2AAgentInfo is one A2A agent returned when browsing a remote A2A registry.
type A2AAgentInfo struct {
	Name        string `json:"name"`
	DirPath     string `json:"dir_path"`
	Group       string `json:"group,omitempty"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"` // agent card URL if advertised in the manifest
	Installed   bool   `json:"installed"`
}

// AgentInfo is one agent returned when browsing a remote registry.
type AgentInfo struct {
	Name        string   `json:"name"`
	DirPath     string   `json:"dir_path"`        // path relative to registry root, e.g. "research/web_agent"
	Group       string   `json:"group,omitempty"` // intermediate dirs before the agent dir
	Description string   `json:"description,omitempty"`
	Builtin     bool     `json:"builtin,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Installed   bool     `json:"installed"`
	Format      string   `json:"format,omitempty"` // "claude" for Claude Code .md format; empty for native omnis format
	// Tools/Model/Skills/MCPServers surface the same hints the agent's
	// definition declares (agent.json or frontmatter), so the browse cards
	// can show them next to the description before install.
	Tools      []string `json:"tools,omitempty"`
	Model      string   `json:"model,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	MCPServers []string `json:"mcp_servers,omitempty"`
}

// RepoRef is the provider-agnostic interface that browse/install use.
type RepoRef interface {
	ProviderName() string
	AutoName() string
	TreeEntries(token string) ([]TreeEntry, error)
	RawFile(relPath, token string) ([]byte, int, error)
	DirFiles(dirPath, token string) ([]InstallableFile, error)
}

// TreeEntry is one node from a repository's recursive tree listing.
type TreeEntry struct {
	Path string // relative to the registry root
	Type string // "blob" or "tree"
}

// InstallableFile is one file inside a skill directory, ready to download.
type InstallableFile struct {
	Name    string // filename only
	RelPath string // path relative to the registry root (dirPath + "/" + Name)
}
