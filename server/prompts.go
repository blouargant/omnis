package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handlePrompts lists the current generation's overridable prompt texts
// (prompt_overrides.json): each with its default, its override if any and the
// agents it reaches. Texts only — no credential is part of any of them. Used
// by the omnis-benches GEPA tool; not shown in Settings.
func handlePrompts(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Manager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no agent generation"})
			return
		}
		inst := d.Manager.Current()
		if inst == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no agent generation"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"prompts": inst.Prompts()})
	}
}
