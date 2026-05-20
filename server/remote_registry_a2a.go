package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	internala2a "github.com/blouargant/yoke/internal/a2a"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
)

// registerRemoteA2ARegistryRoutes mounts /remotes endpoints scoped to "a2a" kind.
// Shares the backing remote_registries.json with the skills, agents, and mcp tabs.
//
// a2aConfigRead re-resolves the 3-layer config chain on each request so a
// newly-saved override under $YOKE_HOME/config is picked up immediately.
// a2aConfigWrite is the fixed write target under $YOKE_HOME/config.
func registerRemoteA2ARegistryRoutes(
	rg *gin.RouterGroup,
	readPath func() string,
	writePath string,
	a2aConfigRead func() string,
	a2aConfigWrite string,
) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindA2A)

	// GET /remotes/:id/browse — list A2A agents discoverable in the remote tree.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindA2A)
		if !ok {
			return
		}
		installed := readInstalledA2ANames(a2aConfigRead())
		agents, err := registries.BrowseA2AAgents(ref, reg.Token, installed)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"agents":   agents,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/agent/*dirpath — fetch raw a2a.json content for preview.
	rg.GET("/remotes/:id/agent/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindA2A)
		if !ok {
			return
		}
		body, err := registries.FetchA2AAgentJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — download and merge A2A agent into a2a_config.json.
	// The agent name is taken from the directory leaf or an optional "name" field in a2a.json.
	// Installing an already-present agent name is a no-op (idempotent).
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindA2A)
		if !ok {
			return
		}
		body, err := registries.FetchA2AAgentJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}

		// Resolve the agent name: directory leaf by default, manifest "name" overrides.
		agentName := dirPath
		if i := strings.LastIndex(dirPath, "/"); i >= 0 {
			agentName = dirPath[i+1:]
		}
		var nameCheck struct {
			Name string `json:"name,omitempty"`
		}
		if err := json.Unmarshal(body, &nameCheck); err == nil && strings.TrimSpace(nameCheck.Name) != "" {
			agentName = strings.TrimSpace(nameCheck.Name)
		}
		if agentName == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "could not determine agent name from a2a.json"))
			return
		}

		var agent internala2a.Agent
		if err := json.Unmarshal(body, &agent); err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", fmt.Sprintf("parse a2a.json: %v", err)))
			return
		}
		agent.Name = agentName

		added, err := mergeA2AAgent(a2aConfigRead(), a2aConfigWrite, agentName, agent)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusCreated, gin.H{"name": agentName, "added": added})
	})
}

// readInstalledA2ANames returns the set of agent names currently in a2a_config.json.
// Returns an empty map on any read/parse error so callers can safely use it for
// membership tests without failing.
func readInstalledA2ANames(configPath string) map[string]bool {
	out := map[string]bool{}
	cfg, err := internala2a.Load(configPath)
	if err != nil {
		return out
	}
	for _, a := range cfg.AgentList() {
		out[a.Name] = true
	}
	return out
}

// mergeA2AAgent reads the current a2a_config.json, adds or updates an agent entry,
// and writes it back to writePath atomically. Returns (added, error): added=false
// when an agent with that name was already present (idempotent update).
func mergeA2AAgent(readPath, writePath, agentName string, agent internala2a.Agent) (bool, error) {
	cfg, err := internala2a.Load(readPath)
	if err != nil {
		return false, fmt.Errorf("read a2a_config.json: %w", err)
	}
	_, already := cfg.Agents[agentName]
	if cfg.Agents == nil {
		cfg.Agents = map[string]internala2a.Agent{}
	}
	cfg.Agents[agentName] = agent

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal a2a_config.json: %w", err)
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

// a2aRoutesDeps bundles the resolved paths required by the A2A remote-registry routes.
type a2aRoutesDeps struct {
	RemoteRegistriesWrite string
	RemoteRegistriesRead  func() string
	A2AConfigRead         func() string
	A2AConfigWrite        string
}

// resolveA2ARoutesDeps derives the dep bundle from standard path conventions.
func resolveA2ARoutesDeps() a2aRoutesDeps {
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	absA2AWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "a2a_config.json"))
	return a2aRoutesDeps{
		RemoteRegistriesWrite: absRemoteWrite,
		RemoteRegistriesRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig(registries.ConfigFileName))
			return p
		},
		A2AConfigRead: func() string {
			p, _ := filepath.Abs(paths.FindConfig("a2a_config.json"))
			return p
		},
		A2AConfigWrite: absA2AWrite,
	}
}

// registerA2ARoutes mounts the /api/a2a/* remote-registry routes. Called from
// server.go alongside registerSkillsRoutes, registerAgentsRoutes, and registerMCPRoutes.
func registerA2ARoutes(rg *gin.RouterGroup) {
	deps := resolveA2ARoutesDeps()
	registerRemoteA2ARegistryRoutes(
		rg,
		deps.RemoteRegistriesRead,
		deps.RemoteRegistriesWrite,
		deps.A2AConfigRead,
		deps.A2AConfigWrite,
	)
}
