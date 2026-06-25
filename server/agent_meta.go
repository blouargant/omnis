package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/agent"
)

// registerAgentMetaRoutes mounts read-only endpoints that expose static
// metadata about the agent runtime. The UI calls these once to render
// built-in agent cards (description + system instruction) as read-only and
// to refuse deletion of agents wired into the binary.
func registerAgentMetaRoutes(rg *gin.RouterGroup) {
	rg.GET("/agent/builtin-defaults", func(c *gin.Context) {
		out := make(map[string]gin.H, len(agent.BuiltinAgentNames))
		for _, name := range agent.BuiltinAgentNames {
			desc, instr := agent.BuiltinAgentDefault(name)
			out[name] = gin.H{
				"description": desc,
				"instruction": instr,
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"names":  agent.BuiltinAgentNames,
			"agents": out,
		})
	})
}
