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
	// Filenames are basename glob patterns this server handles, matched with
	// filepath.Match against filepath.Base(path) (e.g. ["Dockerfile",
	// "Dockerfile.*", "Makefile"]). This is how extensionless files — which have
	// no suffix for Extensions to key on — get routed to a server. A file matches
	// a server if its extension is in Extensions OR its basename matches any
	// Filenames pattern.
	Filenames []string `json:"filenames,omitempty"`
	// RootMarkers are filenames whose presence marks a workspace root
	// (e.g. ["go.mod"], ["Cargo.toml"], ["package.json", "tsconfig.json"]).
	RootMarkers []string `json:"root_markers"`
	// LanguageID is the LSP languageId sent on didOpen. Defaults to Name when
	// empty (e.g. key "ts" → set "typescript" here).
	LanguageID string `json:"language_id,omitempty"`
	// LanguageIDs overrides the languageId per file extension (lowercased, incl.
	// dot), for a server that handles several languages under one process — e.g.
	// clangd with {".c": "c"} so pure-C files open as "c" while the rest fall back
	// to LanguageID/Name ("cpp"). An extension absent here uses langID().
	LanguageIDs map[string]string `json:"language_ids,omitempty"`
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

// langIDForPath returns the languageId for a specific file: a per-extension
// override from LanguageIDs when present, else the server default (langID). This
// lets one server serve several languages with the right per-file id — e.g.
// clangd sending "c" for .c and "cpp" for .cpp.
func (s Server) langIDForPath(path string) string {
	if len(s.LanguageIDs) > 0 {
		ext := strings.ToLower(filepath.Ext(path))
		if ext != "" {
			if id := strings.TrimSpace(s.LanguageIDs[ext]); id != "" {
				return id
			}
		}
	}
	return s.langID()
}

// handles reports whether this server should service path: true when path's
// extension is one of Extensions, or its basename matches any Filenames glob.
// The Filenames branch is what routes extensionless files (Dockerfile, Makefile).
func (s Server) handles(path string) bool {
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" {
		for _, e := range s.Extensions {
			if strings.EqualFold(e, ext) {
				return true
			}
		}
	}
	base := filepath.Base(path)
	for _, pat := range s.Filenames {
		if strings.EqualFold(pat, base) {
			return true
		}
		if ok, err := filepath.Match(pat, base); err == nil && ok {
			return true
		}
	}
	return false
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

// ServerForFile returns the server that handles path — by extension or by a
// Filenames glob on its basename (so extensionless files like Dockerfile route).
// When two servers both match, the lexicographically-first key wins (a
// config-authoring concern). Returns false when no server matches.
func (c *Config) ServerForFile(path string) (Server, bool) {
	for _, s := range c.ServerList() {
		if s.handles(path) {
			return s, true
		}
	}
	return Server{}, false
}
