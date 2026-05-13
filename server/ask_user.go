package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/askuser"
)

// askUserResponseRequest is the JSON body for POST /api/sessions/:id/ask-user/:qid.
type askUserResponseRequest struct {
	Selected  []string `json:"selected,omitempty"`
	Text      string   `json:"text,omitempty"`
	Cancelled bool     `json:"cancelled,omitempty"`
}

// handleAskUserResponse receives the user's answer from the web UI and
// delivers it to the registry so the blocked ask_user tool call can return.
func handleAskUserResponse(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.AskUserRegistry == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ask_user not enabled"})
			return
		}

		sessionID := c.Param("id")
		questionID := c.Param("qid")

		if _, ok := d.Registry.Get(sessionID); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}

		var req askUserResponseRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}

		ans := askuser.Answer{
			Selected:  req.Selected,
			Text:      req.Text,
			Cancelled: req.Cancelled,
		}
		if err := d.AskUserRegistry.Resolve(sessionID, questionID, ans); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	}
}
