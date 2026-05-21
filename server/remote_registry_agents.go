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
	skillsReadDir string,
	skillsWriteDir string,
	mcpConfigRead func() string,
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
		// Reconcile disk-based Installed flag with the runtime config list.
		// An agent whose directory exists on disk but was removed from
		// config/agents.json must show as not-installed.
		configured := readConfiguredAgentNames(agentsConfigRead())
		for i := range agents {
			if agents[i].Installed && !configured[agents[i].Name] {
				agents[i].Installed = false
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"agents":   agents,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/agent/*dirpath — fetch raw agent content.
	// For native format agents dirPath is a directory; we append /agent.json.
	// For Claude Code format agents dirPath is the .md file path itself.
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
		var body []byte
		var err error
		if strings.HasSuffix(dirPath, ".md") {
			// Claude Code markdown format: dirPath is the file path itself.
			var status int
			body, status, err = ref.RawFile(dirPath, reg.Token)
			if err == nil && status != 200 {
				err = fmt.Errorf("HTTP %d fetching %s", status, dirPath)
			}
		} else {
			body, err = registries.FetchAgentJSON(ref, reg.Token, dirPath)
		}
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
					"name":         agentName,
					"enabled":      false,
					"enable_error": err.Error(),
				})
				return
			}
			enabled = added
		}

		// Resolve skill and MCP server dependencies declared in the installed agent.json.
		var warnings []string
		skills, mcpServers := parseAgentJSONDeps(filepath.Join(agentsRegistryDir, agentName, "agent.json"))
		if len(skills) > 0 {
			_, skillWarns := tryAutoInstallSkills(skills, skillsReadDir, skillsWriteDir, readPath())
			warnings = append(warnings, skillWarns...)
		}
		if len(mcpServers) > 0 {
			warnings = append(warnings, checkMCPServerDeps(mcpServers, mcpConfigRead())...)
		}

		resp := gin.H{"name": agentName, "enabled": enabled}
		if len(warnings) > 0 {
			resp["warnings"] = warnings
		}
		c.JSON(http.StatusCreated, resp)
	})
}

// readConfiguredAgentNames returns the set of agent names currently listed in
// config/agents.json. Returns an empty (non-nil) map on any read/parse error
// so callers can safely use the result for membership tests.
func readConfiguredAgentNames(configPath string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return out
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return out
	}
	rawAgents, _ := cfg["agents"].([]any)
	for _, item := range rawAgents {
		if s, ok := item.(string); ok && s != "" {
			out[strings.TrimSpace(s)] = true
		}
	}
	return out
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
	AgentsRegistryDir       string        // abs $YOKE_HOME/registry/agents (or env-override)
	RemoteRegistriesWrite   string        // abs $YOKE_HOME/config/remote_registries.json
	RemoteRegistriesRead    func() string // re-resolves the 3-layer chain on each request
	AgentsConfigRead        func() string // re-resolves config/agents.json read path
	AgentsConfigWrite       string        // abs $YOKE_HOME/config/agents.json
	SkillsRegistryReadDir   string        // abs path to skills registry for dependency resolution
	SkillsRegistryWriteDir  string        // abs write target for auto-installed skills
	MCPConfigRead           func() string // re-resolves mcp_config.json read path
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
	skillsRead, _ := filepath.Abs(paths.SkillsRegistryDir())
	skillsWrite, _ := filepath.Abs(paths.SkillsRegistryWriteDir())
	if v := strings.TrimSpace(os.Getenv("YOKE_SKILLS_REGISTRY_DIR")); v != "" {
		skillsRead, _ = filepath.Abs(v)
		skillsWrite = skillsRead
	}
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
		AgentsConfigWrite:      absAgentsWrite,
		SkillsRegistryReadDir:  skillsRead,
		SkillsRegistryWriteDir: skillsWrite,
		MCPConfigRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig("mcp_config.json"))
			return p
		},
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
		deps.SkillsRegistryReadDir,
		deps.SkillsRegistryWriteDir,
		deps.MCPConfigRead,
	)
	registerImportAgentRoute(rg)
}
