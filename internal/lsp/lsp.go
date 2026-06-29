// lsp.go — the lsp_config.json schema and loader. The file maps a language key
// to the server that handles it (command + how to recognise its files and
// workspace root). It is resolved through the same 3-layer config search chain
// as mcp_config.json (.agents → $HOME/.omnis → /etc/omnis).
package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blouargant/omnis/internal/deps"
	"github.com/blouargant/omnis/internal/paths"
)

// ConfigFileName is the on-disk name resolved through the config search chain.
const ConfigFileName = "lsp_config.json"

// Config is the parsed lsp_config.json.
type Config struct {
	Servers map[string]Server `json:"servers"`
}

// Server describes one language server: how to launch it, which files it
// handles, and how to find a project's workspace root.
type Server struct {
	// Name is the map key (the language key, e.g. "go"); stamped on load,
	// never read from JSON.
	Name string `json:"-"`
	// Command is the server executable (looked up on PATH).
	Command string `json:"command"`
	// Args are extra process arguments (e.g. ["--stdio"]).
	Args []string `json:"args,omitempty"`
	// Env are extra environment variables, layered over the process env.
	Env map[string]string `json:"env,omitempty"`
	// Extensions are the file suffixes this server handles, incl. dot (".go").
	Extensions []string `json:"extensions"`
	// RootMarkers are filenames whose presence marks a workspace root
	// (e.g. ["go.mod"], ["Cargo.toml"], ["package.json", "tsconfig.json"]).
	RootMarkers []string `json:"root_markers"`
	// LanguageID is the LSP languageId sent on didOpen. Defaults to Name when
	// empty (e.g. key "ts" → set "typescript" here).
	LanguageID string `json:"language_id,omitempty"`
	// Requires lists runtime tool dependencies (typically the server binary
	// itself) the host installs on first use via the dependency gate — same
	// mechanism as skill / MCP `requires`. Empty means the binary must already
	// be on PATH or the start fails with an exec error.
	Requires []deps.Requirement `json:"requires,omitempty"`
}

// langID returns the LSP languageId for didOpen, defaulting to the map key.
func (s Server) langID() string {
	if s.LanguageID != "" {
		return s.LanguageID
	}
	return s.Name
}

// Load parses the JSON at path. A missing file yields an empty config, so a
// fresh install with no LSP servers behaves as a no-op.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Servers: map[string]Server{}}, nil
		}
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Servers == nil {
		c.Servers = map[string]Server{}
	}
	for k, s := range c.Servers {
		s.Name = k
		c.Servers[k] = s
	}
	return &c, nil
}

// DefaultConfigPath resolves lsp_config.json through the config search chain.
func DefaultConfigPath() string { return paths.FindConfig(ConfigFileName) }

// LoadDefault loads the config from the search-chain location.
func LoadDefault() (*Config, error) { return Load(DefaultConfigPath()) }

// ServerList returns the servers in stable (lexicographic) key order, each
// stamped with its Name. Stable order makes extension resolution deterministic
// when two servers claim the same extension.
func (c *Config) ServerList() []Server {
	if c == nil || len(c.Servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Servers))
	for k := range c.Servers {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]Server, 0, len(names))
	for _, n := range names {
		s := c.Servers[n]
		s.Name = n
		out = append(out, s)
	}
	return out
}

// ServerForFile returns the server that handles path's extension. When two
// servers declare the same extension the lexicographically-first key wins
// (a config-authoring concern). Returns false when no server matches.
func (c *Config) ServerForFile(path string) (Server, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return Server{}, false
	}
	for _, s := range c.ServerList() {
		for _, e := range s.Extensions {
			if strings.EqualFold(e, ext) {
				return s, true
			}
		}
	}
	return Server{}, false
}
