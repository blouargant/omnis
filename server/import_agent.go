package main

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/claudeformat"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/registries"
)

// registerImportAgentRoute mounts POST /import on rg.
func registerImportAgentRoute(rg *gin.RouterGroup) {
	agentsRegistryDir := paths.AgentsRegistryWriteDir()
	agentsConfigWrite := filepath.Join(paths.ConfigWriteDir(), "agents.json")
	agentsConfigRead := func() string {
		p, _ := filepath.Abs(paths.FindConfig("agents.json"))
		return p
	}

	// POST /api/agents/import
	// Body: {"content": "<raw file text>", "enable": true|false}
	// Response: {"agents": [{"name":"…","description":"…","enabled":false}]}
	rg.POST("/import", func(c *gin.Context) {
		var req struct {
			Content string `json:"content"`
			Enable  bool   `json:"enable"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Content == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "body must be JSON with a non-empty 'content' field"})
			return
		}

		defs, err := claudeformat.Parse([]byte(req.Content))
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}

		if err := os.MkdirAll(agentsRegistryDir, 0o755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		type importedAgent struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Enabled     bool   `json:"enabled"`
		}
		result := make([]importedAgent, 0, len(defs))

		for _, def := range defs {
			if !registries.SkillNameRe.MatchString(def.Name) {
				c.JSON(http.StatusUnprocessableEntity, gin.H{
					"error": "agent name " + def.Name + " is not valid (must be lowercase kebab-case)",
				})
				return
			}

			if err := claudeformat.InstallAgent(def, agentsRegistryDir); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}

			enabled := false
			if req.Enable {
				added, err := appendAgentToConfig(agentsConfigRead(), agentsConfigWrite, def.Name)
				if err == nil {
					enabled = added
				}
			}
			result = append(result, importedAgent{
				Name:        def.Name,
				Description: def.Description,
				Enabled:     enabled,
			})
		}

		c.JSON(http.StatusCreated, gin.H{"agents": result})
	})
}
