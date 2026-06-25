package registries

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/blouargant/omnis/internal/claudeformat"
	mcpcfg "github.com/blouargant/omnis/internal/mcp"
)

// ResolveMCPServer fetches and parses the manifest at dirPath inside a remote
// registry into a ready-to-merge MCP server definition. It handles both the
// mcp.md (YAML frontmatter) and JSON manifest formats. mdSkills carries any
// skills declared by an mcp.md manifest so the caller can resolve them as
// dependencies.
//
// Shared by the web-UI MCP install route, the agent-dependency auto-installer,
// and the helper agent's install_remote_item tool — so every surface gains
// mcp.md support and stays in lock-step.
func ResolveMCPServer(ref RepoRef, token, dirPath string) (name string, srv mcpcfg.Server, inputs []mcpcfg.Input, mdSkills []string, err error) {
	// mcp.md format: parse YAML frontmatter directly into a Server struct.
	if path.Base(dirPath) == MCPMarkdownFile {
		raw, status, fetchErr := ref.RawFile(dirPath, token)
		if fetchErr != nil || status != 200 {
			return "", mcpcfg.Server{}, nil, nil, fmt.Errorf("fetch mcp.md: HTTP %d", status)
		}
		def, parseErr := claudeformat.ParseMCPMarkdown(raw)
		if parseErr != nil {
			return "", mcpcfg.Server{}, nil, nil, fmt.Errorf("parse mcp.md: %v", parseErr)
		}
		srv = mcpcfg.Server{
			Type:    def.Type,
			Command: def.Command,
			Args:    def.Args,
			Env:     def.Env,
			URL:     def.URL,
			Headers: def.Headers,
		}
		inputs = make([]mcpcfg.Input, len(def.Inputs))
		for i, inp := range def.Inputs {
			inputs[i] = mcpcfg.Input{
				ID:          inp.ID,
				Type:        inp.Type,
				Description: inp.Description,
				Password:    inp.Password,
				Options:     inp.Options,
				Default:     inp.Default,
			}
		}
		return def.Name, srv, inputs, def.Skills, nil
	}

	// json manifest format.
	body, err := FetchMCPToolJSON(ref, token, dirPath)
	if err != nil {
		return "", mcpcfg.Server{}, nil, nil, err
	}

	// Resolve the server name: directory leaf by default, manifest "name" overrides.
	// DirPath may be a full file path (e.g. "mcp/srv/tokensave.json"), so strip the
	// filename to get the directory before extracting the leaf name.
	namePath := dirPath
	if strings.HasSuffix(dirPath, ".json") {
		namePath = path.Dir(dirPath)
	}
	serverName := namePath
	if i := strings.LastIndex(namePath, "/"); i >= 0 {
		serverName = namePath[i+1:]
	}
	var nameCheck struct {
		Name string `json:"name,omitempty"`
	}
	if e := json.Unmarshal(body, &nameCheck); e == nil && strings.TrimSpace(nameCheck.Name) != "" {
		serverName = strings.TrimSpace(nameCheck.Name)
	}
	if serverName == "" {
		return "", mcpcfg.Server{}, nil, nil, fmt.Errorf("could not determine server name from mcp.json")
	}

	// Parse the server definition — unknown fields (description, name) are silently
	// ignored by json.Unmarshal since Server has no matching exported fields for them.
	if e := json.Unmarshal(body, &srv); e != nil {
		return "", mcpcfg.Server{}, nil, nil, fmt.Errorf("parse mcp.json: %v", e)
	}
	srv.Name = serverName
	return serverName, srv, nil, nil, nil
}

// MergeMCPServer reads the current mcp_config.json at readPath, adds or updates
// the named server entry, merges any new inputs (by ID) into the top-level
// inputs array, then writes atomically to writePath. Returns (added, error):
// added=false when the server name was already present.
func MergeMCPServer(readPath, writePath, serverName string, srv mcpcfg.Server, inputs []mcpcfg.Input) (bool, error) {
	cfg, err := mcpcfg.Load(readPath)
	if err != nil {
		return false, fmt.Errorf("read mcp_config.json: %w", err)
	}
	_, already := cfg.Servers[serverName]
	if cfg.Servers == nil {
		cfg.Servers = map[string]mcpcfg.Server{}
	}
	cfg.Servers[serverName] = srv

	// Merge inputs: add any input not already present (matched by ID).
	for _, newIn := range inputs {
		found := false
		for _, existing := range cfg.Inputs {
			if existing.ID == newIn.ID {
				found = true
				break
			}
		}
		if !found {
			cfg.Inputs = append(cfg.Inputs, newIn)
		}
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal mcp_config.json: %w", err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return false, fmt.Errorf("mkdir: %w", err)
	}
	if err := atomicWriteFile(writePath, out); err != nil {
		return false, fmt.Errorf("write %s: %w", writePath, err)
	}
	return !already, nil
}
