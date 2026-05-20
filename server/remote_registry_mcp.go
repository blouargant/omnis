package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/claudeformat"
	internalmcp "github.com/blouargant/yoke/internal/mcp"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
)

// registerRemoteMCPRegistryRoutes mounts /remotes endpoints scoped to "mcp" kind.
// Shares the backing remote_registries.json with the skills and agents tabs.
//
// mcpConfigRead re-resolves the 3-layer config chain on each request so a
// newly-saved override under $YOKE_HOME/config is picked up immediately.
// mcpConfigWrite is the fixed write target under $YOKE_HOME/config.
func registerRemoteMCPRegistryRoutes(
	rg *gin.RouterGroup,
	readPath func() string,
	writePath string,
	mcpConfigRead func() string,
	mcpConfigWrite string,
) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindMCP)

	// GET /remotes/:id/browse — list MCP servers discoverable in the remote tree.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindMCP)
		if !ok {
			return
		}
		installed := readInstalledMCPNames(mcpConfigRead())
		tools, err := registries.BrowseMCPTools(ref, reg.Token, installed)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"tools":    tools,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/readme/*dirpath — fetch documentation for a tool (for display in the UI).
	// For mcp.md manifests the markdown body is used; for json manifests a README.md is looked up.
	rg.GET("/remotes/:id/readme/*dirpath", func(c *gin.Context) {
		filePath := strings.Trim(c.Param("dirpath"), "/")
		if filePath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindMCP)
		if !ok {
			return
		}
		// mcp.md manifest: extract the markdown body as documentation.
		if path.Base(filePath) == registries.MCPMarkdownFile {
			raw, status, err := ref.RawFile(filePath, reg.Token)
			if err != nil || status != 200 {
				c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "mcp.md not found"))
				return
			}
			def, parseErr := claudeformat.ParseMCPMarkdown(raw)
			if parseErr != nil || def.Body == "" {
				c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "no documentation body in mcp.md"))
				return
			}
			c.JSON(http.StatusOK, gin.H{"content": def.Body})
			return
		}
		// json manifest: look for a README.md in the same directory.
		dirPath := filePath
		if strings.HasSuffix(filePath, ".json") {
			dirPath = path.Dir(filePath)
		}
		raw, status, err := ref.RawFile(dirPath+"/README.md", reg.Token)
		if err != nil || status != 200 {
			c.JSON(http.StatusNotFound, skillsErr("NOT_FOUND", "README.md not found"))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(raw)})
	})

	// GET /remotes/:id/tool/*dirpath — fetch raw mcp.json content for preview.
	rg.GET("/remotes/:id/tool/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindMCP)
		if !ok {
			return
		}
		body, err := registries.FetchMCPToolJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — download and merge MCP server into mcp_config.json.
	// The server name is taken from the directory leaf (or an optional "name" field in the manifest).
	// Installing an already-present server name is a no-op (idempotent).
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindMCP)
		if !ok {
			return
		}

		// mcp.md format: parse YAML frontmatter directly into a Server struct.
		if path.Base(dirPath) == registries.MCPMarkdownFile {
			raw, status, fetchErr := ref.RawFile(dirPath, reg.Token)
			if fetchErr != nil || status != 200 {
				c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", fmt.Sprintf("fetch mcp.md: HTTP %d", status)))
				return
			}
			def, parseErr := claudeformat.ParseMCPMarkdown(raw)
			if parseErr != nil {
				c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", fmt.Sprintf("parse mcp.md: %v", parseErr)))
				return
			}
			srv := internalmcp.Server{
				Type:    def.Type,
				Command: def.Command,
				Args:    def.Args,
				Env:     def.Env,
				URL:     def.URL,
			}
			added, err := mergeMCPServer(mcpConfigRead(), mcpConfigWrite, def.Name, srv)
			if err != nil {
				c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
				return
			}
			c.JSON(http.StatusCreated, gin.H{"name": def.Name, "added": added})
			return
		}

		// json manifest format.
		body, err := registries.FetchMCPToolJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
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
		if err := json.Unmarshal(body, &nameCheck); err == nil && strings.TrimSpace(nameCheck.Name) != "" {
			serverName = strings.TrimSpace(nameCheck.Name)
		}
		if serverName == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "could not determine server name from mcp.json"))
			return
		}

		// Parse the server definition — unknown fields (description, name) are silently
		// ignored by json.Unmarshal since Server has no matching exported fields for them.
		var srv internalmcp.Server
		if err := json.Unmarshal(body, &srv); err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", fmt.Sprintf("parse mcp.json: %v", err)))
			return
		}
		srv.Name = serverName

		added, err := mergeMCPServer(mcpConfigRead(), mcpConfigWrite, serverName, srv)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"name": serverName, "added": added})
	})
}

// readInstalledMCPNames returns the set of server names currently in mcp_config.json.
// Returns an empty map on any read/parse error so callers can safely use it for
// membership tests without crashing.
func readInstalledMCPNames(configPath string) map[string]bool {
	out := map[string]bool{}
	cfg, err := internalmcp.Load(configPath)
	if err != nil {
		return out
	}
	for _, s := range cfg.ServerList() {
		out[s.Name] = true
	}
	return out
}

// mergeMCPServer reads the current mcp_config.json, adds or updates a server entry,
// and writes it back to writePath atomically. Returns (added, error): added=false
// when a server with that name was already present (idempotent update).
func mergeMCPServer(readPath, writePath, serverName string, srv internalmcp.Server) (bool, error) {
	cfg, err := internalmcp.Load(readPath)
	if err != nil {
		return false, fmt.Errorf("read mcp_config.json: %w", err)
	}
	_, already := cfg.Servers[serverName]
	if cfg.Servers == nil {
		cfg.Servers = map[string]internalmcp.Server{}
	}
	cfg.Servers[serverName] = srv

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

// mcpRoutesDeps bundles the resolved paths required by the MCP remote-registry routes.
type mcpRoutesDeps struct {
	RemoteRegistriesWrite string
	RemoteRegistriesRead  func() string
	MCPConfigRead         func() string
	MCPConfigWrite        string
}

// resolveMCPRoutesDeps derives the dep bundle from standard path conventions.
func resolveMCPRoutesDeps() mcpRoutesDeps {
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	absMCPWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "mcp_config.json"))
	return mcpRoutesDeps{
		RemoteRegistriesWrite: absRemoteWrite,
		RemoteRegistriesRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig(registries.ConfigFileName))
			return p
		},
		MCPConfigRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig("mcp_config.json"))
			return p
		},
		MCPConfigWrite: absMCPWrite,
	}
}

// registerMCPRoutes mounts the /api/mcp/* remote-registry routes. Called from
// server.go alongside registerSkillsRoutes and registerAgentsRoutes.
func registerMCPRoutes(rg *gin.RouterGroup) {
	deps := resolveMCPRoutesDeps()
	registerRemoteMCPRegistryRoutes(
		rg,
		deps.RemoteRegistriesRead,
		deps.RemoteRegistriesWrite,
		deps.MCPConfigRead,
		deps.MCPConfigWrite,
	)
}
