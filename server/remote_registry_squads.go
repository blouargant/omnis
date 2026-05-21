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

// registerRemoteSquadRegistryRoutes mounts /remotes endpoints scoped to
// "squads" kind on rg. Shares the backing remote_registries.json with the
// other registry tabs.
//
// Install merges the squad entry into config/agents.json's "squads" array.
// An existing squad with the same name is replaced (idempotent update).
func registerRemoteSquadRegistryRoutes(
	rg *gin.RouterGroup,
	readPath func() string,
	writePath string,
	agentsConfigRead func() string,
	agentsConfigWrite string,
	agentsRegistryDir string,
) {
	registerRemoteRegistryCRUD(rg, readPath, writePath, registries.KindSquads)

	// GET /remotes/:id/browse — list squads discoverable in the remote tree.
	rg.GET("/remotes/:id/browse", func(c *gin.Context) {
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindSquads)
		if !ok {
			return
		}
		installed := readInstalledSquadNames(agentsConfigRead())
		squads, err := registries.BrowseSquads(ref, reg.Token, installed)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"squads":   squads,
			"registry": toPublicRemote(*reg),
		})
	})

	// GET /remotes/:id/squad/*dirpath — fetch raw squad.json content for preview.
	rg.GET("/remotes/:id/squad/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindSquads)
		if !ok {
			return
		}
		body, err := registries.FetchSquadJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("REMOTE_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"content": string(body)})
	})

	// POST /remotes/:id/install/*dirpath — download and merge squad into agents.json.
	// An existing squad with the same name is replaced (update-in-place).
	rg.POST("/remotes/:id/install/*dirpath", func(c *gin.Context) {
		dirPath := strings.Trim(c.Param("dirpath"), "/")
		if dirPath == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "dirpath is required"))
			return
		}
		reg, ref, ok := loadRegistryForKind(c, readPath, c.Param("id"), registries.KindSquads)
		if !ok {
			return
		}
		body, err := registries.FetchSquadJSON(ref, reg.Token, dirPath)
		if err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", err.Error()))
			return
		}

		var sq squadJSON
		if err := json.Unmarshal(body, &sq); err != nil {
			c.JSON(http.StatusBadGateway, skillsErr("INSTALL_ERROR", fmt.Sprintf("parse squad.json: %v", err)))
			return
		}
		// Fall back to the directory leaf name when squad.json omits "name".
		if sq.Name == "" {
			leaf := dirPath
			if i := strings.LastIndex(dirPath, "/"); i >= 0 {
				leaf = dirPath[i+1:]
			}
			sq.Name = leaf
		}
		if sq.Name == "" {
			c.JSON(http.StatusBadRequest, skillsErr("BAD_REQUEST", "could not determine squad name from squad.json"))
			return
		}

		added, err := mergeSquadIntoConfig(agentsConfigRead(), agentsConfigWrite, sq)
		if err != nil {
			c.JSON(http.StatusInternalServerError, skillsErr("FS_ERROR", err.Error()))
			return
		}

		// Auto-install agents referenced by the squad that are not yet present.
		allAgents := make([]string, 0, 1+len(sq.Members))
		if sq.Leader != "" {
			allAgents = append(allAgents, sq.Leader)
		}
		allAgents = append(allAgents, sq.Members...)
		_, agentWarns := tryAutoInstallAgents(allAgents, agentsRegistryDir, agentsConfigRead, agentsConfigWrite, readPath())

		resp := gin.H{"name": sq.Name, "added": added}
		if len(agentWarns) > 0 {
			resp["warnings"] = agentWarns
		}
		c.JSON(http.StatusCreated, resp)
	})
}

// squadJSON is the on-disk shape of a remote squad.json, duplicated here to
// avoid an import cycle with the agent package.
type squadJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Leader      string   `json:"leader"`
	Members     []string `json:"members"`
}

// readInstalledSquadNames returns the set of squad names currently listed in
// config/agents.json's "squads" array. Returns an empty map on any error.
func readInstalledSquadNames(configPath string) map[string]bool {
	out := map[string]bool{}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return out
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		return out
	}
	rawSquads, _ := cfg["squads"].([]any)
	for _, item := range rawSquads {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := m["name"].(string); name != "" {
			out[strings.TrimSpace(name)] = true
		}
	}
	return out
}

// mergeSquadIntoConfig reads the current agents.json, upserts the squad by
// name in the "squads" array (appends when new, replaces when found), and
// writes it back atomically. Returns (added, error): added=false when an
// existing entry was replaced.
func mergeSquadIntoConfig(readPath, writePath string, sq squadJSON) (bool, error) {
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

	entry := map[string]any{
		"name":        sq.Name,
		"description": sq.Description,
		"leader":      sq.Leader,
		"members":     sq.Members,
	}

	rawSquads, _ := cfg["squads"].([]any)
	added := true
	replaced := false
	for i, item := range rawSquads {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, _ := m["name"].(string); strings.EqualFold(strings.TrimSpace(name), sq.Name) {
			rawSquads[i] = entry
			replaced = true
			added = false
			break
		}
	}
	if !replaced {
		rawSquads = append(rawSquads, entry)
	}
	cfg["squads"] = rawSquads

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
	return added, nil
}

// squadsRoutesDeps bundles the resolved paths required by the squad remote-registry routes.
type squadsRoutesDeps struct {
	RemoteRegistriesWrite string
	RemoteRegistriesRead  func() string
	AgentsConfigRead      func() string
	AgentsConfigWrite     string
	AgentsRegistryDir     string // abs path for installing missing squad member agents
}

// resolveSquadsRoutesDeps derives the dep bundle from standard path conventions.
func resolveSquadsRoutesDeps() squadsRoutesDeps {
	absRemoteWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), registries.ConfigFileName))
	absAgentsWrite, _ := filepath.Abs(filepath.Join(paths.ConfigWriteDir(), "agents.json"))
	agentsDir := paths.AgentsRegistryWriteDir()
	if v := strings.TrimSpace(os.Getenv("YOKE_AGENTS_REGISTRY_DIR")); v != "" {
		agentsDir = v
	}
	absAgentsDir, _ := filepath.Abs(agentsDir)
	return squadsRoutesDeps{
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
		AgentsRegistryDir: absAgentsDir,
	}
}

// registerSquadsRegistryRoutes mounts the /api/squads-registry/* remote-registry routes.
// Called from server.go alongside the other registry route registrations.
func registerSquadsRegistryRoutes(rg *gin.RouterGroup) {
	deps := resolveSquadsRoutesDeps()
	registerRemoteSquadRegistryRoutes(
		rg,
		deps.RemoteRegistriesRead,
		deps.RemoteRegistriesWrite,
		deps.AgentsConfigRead,
		deps.AgentsConfigWrite,
		deps.AgentsRegistryDir,
	)
}
