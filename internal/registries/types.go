// Package registries provides remote skill and agent registry browsing,
// installation, and per-agent linking. It is shared between the HTTP
// server's web UI handlers and the in-agent registries_crawler tool group:
// both surfaces use the same providers (GitHub/GitLab/Gitea), the same
// remote_registries.json config file, and the same on-disk layout.
package registries

// Provider identifiers used in remote_registries.json.
const (
	ProviderGitHub = "github"
	ProviderGitLab = "gitlab"
	ProviderGitea  = "gitea"
)

// Kind values for Registry.Kind. An empty value is treated as KindSkills for
// backwards compatibility with pre-existing remote_registries.json entries.
const (
	KindSkills = "skills"
	KindAgents = "agents"
	KindBoth   = "both"
	KindMCP    = "mcp"
	KindA2A    = "a2a"
	KindSquads = "squads"
)

// Registry is one entry in remote_registries.json.
type Registry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Provider string `json:"provider,omitempty"`
	Kind     string `json:"kind,omitempty"`  // "skills" (default if empty), "agents", "both", "mcp", or "a2a"
	Token    string `json:"token,omitempty"` // PAT; stored server-side, never exposed to the browser.
}

// NormalizedKind returns r.Kind with the empty-string default applied.
func (r Registry) NormalizedKind() string {
	if r.Kind == "" {
		return KindSkills
	}
	return r.Kind
}

// Serves reports whether the registry exposes content of the given kind.
// "both" serves either; an empty Kind serves only skills (legacy default).
func (r Registry) Serves(kind string) bool {
	k := r.NormalizedKind()
	if k == KindBoth {
		return kind == KindSkills || kind == KindAgents
	}
	return k == kind
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
	DirPath     string   `json:"dir_path"`          // path relative to registry root, e.g. "research/web_agent"
	Group       string   `json:"group,omitempty"`   // intermediate dirs before the agent dir
	Description string   `json:"description,omitempty"`
	Builtin     bool     `json:"builtin,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Installed   bool     `json:"installed"`
	Format      string   `json:"format,omitempty"` // "claude" for Claude Code .md format; empty for native yoke format
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
