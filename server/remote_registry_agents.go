package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
)

// registerRemoteAgentRegistryRoutes mounts /remotes endpoints scoped to
// "agents" kind on rg. Shares the backing remote_registries.json with the
// skills tab: an entry with kind="both" is visible from both sides.
//
// agentsRegistryDir is where installed agents land on disk
// ($YOKE_HOME/registry/agents by default). agentsConfigRead/Write resolve
// config/agents.json — the runtime's enabled-agents list. The "Enable"
// toggle in the install dialog appends the installed agent's name to that
// list so the next hot-reload picks it up.
func registerRemoteAgentRegistryRoutes(
	rg *gin.RouterGroup,
	readPath func() string,
	writePath string,
	agentsRegistryDir string,
	agentsConfigRead func() string,
	agentsConfigWrite string,
) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindAgents)

	// GET /remotes/:id/browse — list agents discoverable in the remote tree.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindAgents)
		if !ok {
			return
		}
		agents, err := registries.BrowseAgents(ref, reg.Token, agentsRegistryDir)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"agents":   agents,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/agent/*dirpath — fetch raw agent.json content.
	rg.GET("/remotes/:id/agent/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindAgents)
		if !ok {
			return
		}
		body, err := registries.FetchAgentJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — download and install an agent.
	// Body: {"enable": true|false}. When enable is true the agent name is
	// appended to config/agents.json's `agents` list so the next reload
	// wires it into the running fleet.
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		var req struct {
			Enable bool `json:"enable"`
		}
		_ = c.ShouldBindJSON(&req) // body is optional

		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindAgents)
		if !ok {
			return
		}
		if err := os.MkdirAll(agentsRegistryDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		agentName, err := registries.InstallAgent(ref, reg.Token, dirPath, agentsRegistryDir)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}
		enabled := false
		if req.Enable {
			added, err := appendAgentToConfig(agentsConfigRead(), agentsConfigWrite, agentName)
			if err != nil {
				// Install succeeded; just report the enable failure so the
				// UI can show "installed but not enabled" rather than rolling
				// back the on-disk install.
				c.JSON(http.StatusOK, gin.H{
					"name":          agentName,
					"enabled":       false,
					"enable_error":  err.Error(),
				})
				return
			}
			enabled = added
		}
		c.JSON(http.StatusCreated, gin.H{
			"name":    agentName,
			"enabled": enabled,
		})
	})
}

// appendAgentToConfig adds name to the `agents` list in the runtime config
// file (config/agents.json). The read path uses the 3-layer chain so the
// current effective config wins; writes always fork to writePath under
// $YOKE_HOME/config. Returns (added, error): added is false when the agent
// was already in the list (idempotent no-op).
func appendAgentToConfig(readPath, writePath, name string) (bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("agent name is empty")
	}
	data, err := os.ReadFile(readPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read %s: %w", readPath, err)
	}
	var cfg map[string]any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return false, fmt.Errorf("decode %s: %w", readPath, err)
		}
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	rawAgents, _ := cfg["agents"].([]any)
	for _, item := range rawAgents {
		if s, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(s), name) {
			return false, nil // already enabled
		}
	}
	rawAgents = append(rawAgents, name)
	cfg["agents"] = rawAgents

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode config: %w", err)
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(writePath), 0o755); err != nil {
		return false, fmt.Errorf("mkdir: %w", err)
	}
	if err := atomicWriteFile(writePath, out); err != nil {
		return false, fmt.Errorf("write %s: %w", writePath, err)
	}
	return true, nil
}

// agentsRoutesDeps bundles the resolved paths required by the agents-side
// remote registry routes. Built once at server startup.
type agentsRoutesDeps struct {
	AgentsRegistryDir       string                // abs $YOKE_HOME/registry/agents (or env-override)
	RemoteRegistriesWrite   string                // abs $YOKE_HOME/config/remote_registries.json
	RemoteRegistriesRead    func() string         // re-resolves the 3-layer chain on each request
	AgentsConfigRead        func() string         // re-resolves config/agents.json read path
	AgentsConfigWrite       string                // abs $YOKE_HOME/config/agents.json
}

// resolveAgentsRoutesDeps mirrors resolveSkillsDeps for the agents side.
// $YOKE_AGENTS_REGISTRY_DIR can override the on-disk install location.
func resolveAgentsRoutesDeps() agentsRoutesDeps {
	registryDir := paths.AgentsRegistryWriteDir()
	if v := strings.TrimSpace(os.Getenv("YOKE_AGENTS_REGISTRY_DIR")); v != "" {
		registryDir = v
	}
	absRegistryDir, _ := filepath.Abs(registryDir)
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	absAgentsWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "agents.json"))
	return agentsRoutesDeps{
		AgentsRegistryDir:     absRegistryDir,
		RemoteRegistriesWrite: absRemoteWrite,
		RemoteRegistriesRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig(registries.ConfigFileName))
			return p
		},
		AgentsConfigRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig("agents.json"))
			return p
		},
		AgentsConfigWrite: absAgentsWrite,
	}
}

// registerAgentsRoutes mounts the /api/agents/* routes. Called from server.go
// alongside registerSkillsRoutes.
func registerAgentsRoutes(rg *gin.RouterGroup) {
	deps := resolveAgentsRoutesDeps()
	registerRemoteAgentRegistryRoutes(
		rg,
		deps.RemoteRegistriesRead,
		deps.RemoteRegistriesWrite,
		deps.AgentsRegistryDir,
		deps.AgentsConfigRead,
		deps.AgentsConfigWrite,
	)
	registerImportAgentRoute(rg)
}
