package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/agentmd"
)

// handleAgentMDInitPrompt returns the shared "/init" bootstrap prompt so the
// web UI submits the exact same instruction as the TUI and CLI.
func handleAgentMDInitPrompt(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"prompt": agentmd.InitPrompt()})
}

// handleAgentMDAppend appends a one-line memory (the "#" shortcut) to the
// project AGENT.md resolved from the session's working directory. Like the "!"
// shell-escape and the Monaco save route it writes straight to the host file
// and bypasses the agent permission layer (the authenticated user already has
// host file access). The line is not added to the conversation history.
func handleAgentMDAppend(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if meta.Archived {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived"})
			return
		}
		appendMemory(c, bashCwd.get(id))
		d.Registry.Touch(id)
	}
}

// handleGlobalAgentMDAppend is the session-less variant (draft/editor tabs):
// it targets the global "no session" working directory.
func handleGlobalAgentMDAppend(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		appendMemory(c, bashCwd.getGlobal())
	}
}

func appendMemory(c *gin.Context, cwd string) {
	var req struct {
		Text string `json:"text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text is required"})
		return
	}
	path, err := agentmd.AppendMemory(cwd, req.Text)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": path})
}
