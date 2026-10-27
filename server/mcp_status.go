package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	mcpcfg "github.com/blouargant/omnis/internal/mcp"
)

type mcpServerView struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}

// mcpStatusView joins a squad's MCP server names with the pool's connection
// states; a server the pool has no state for is "idle" (never connected).
func mcpStatusView(names []string, status map[string]mcpcfg.ServerStatus) []mcpServerView {
	out := make([]mcpServerView, 0, len(names))
	for _, n := range names {
		v := mcpServerView{Name: n, State: mcpcfg.StateIdle}
		if st, ok := status[n]; ok {
			v.State, v.Error = st.State, st.Err
		}
		out = append(out, v)
	}
	return out
}

// handleSessionMCP serves GET /api/sessions/:id/mcp — the MCP servers of the
// squad the session is currently on, with the shared pool's connection state.
// Read-only: it never starts a server.
func handleSessionMCP(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.Manager == nil {
			c.JSON(http.StatusOK, gin.H{"servers": []mcpServerView{}})
			return
		}
		names := d.Manager.SquadMCPServers(id, meta.Squad)
		var status map[string]mcpcfg.ServerStatus
		if infra := d.Manager.Infra(); infra != nil && infra.MCPPool != nil {
			status = infra.MCPPool.Status()
		}
		c.JSON(http.StatusOK, gin.H{"servers": mcpStatusView(names, status)})
	}
}
