package main

import (
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
	skillsReadDir string,
	skillsWriteDir string,
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

		name, srv, inputs, mdSkills, err := registries.ResolveMCPServer(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		added, err := registries.MergeMCPServer(mcpConfigRead(), mcpConfigWrite, name, srv, inputs)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		resp := gin.H{"name": name, "added": added}
		if len(mdSkills) > 0 {
			_, warns := tryAutoInstallSkills(mdSkills, skillsReadDir, skillsWriteDir, readPath())
			if len(warns) > 0 {
				resp["warnings"] = warns
			}
		}
		c.JSON(http.StatusCreated, resp)
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

// mcpRoutesDeps bundles the resolved paths required by the MCP remote-registry routes.
type mcpRoutesDeps struct {
	RemoteRegistriesWrite  string
	RemoteRegistriesRead   func() string
	MCPConfigRead          func() string
	MCPConfigWrite         string
	SkillsRegistryReadDir  string
	SkillsRegistryWriteDir string
}

// resolveMCPRoutesDeps derives the dep bundle from standard path conventions.
func resolveMCPRoutesDeps() mcpRoutesDeps {
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	absMCPWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "mcp_config.json"))
	skillsRead, _ := filepath.Abs(paths.SkillsRegistryDir())
	skillsWrite, _ := filepath.Abs(paths.SkillsRegistryWriteDir())
	if v := strings.TrimSpace(os.Getenv("YOKE_SKILLS_REGISTRY_DIR")); v != "" {
		skillsRead, _ = filepath.Abs(v)
		skillsWrite = skillsRead
	}
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
		MCPConfigWrite:         absMCPWrite,
		SkillsRegistryReadDir:  skillsRead,
		SkillsRegistryWriteDir: skillsWrite,
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
		deps.SkillsRegistryReadDir,
		deps.SkillsRegistryWriteDir,
	)
}
