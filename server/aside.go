// aside.go — read-only "aside" endpoints that answer about a session without
// touching its conversation: /btw (a quick side question) and /recap (a short
// summary). Both load the persisted transcript, flatten it to the agent
// package's text-only Exchange type, and run one off-stream LLM call via the
// Manager (no runner, no tools, no persistence). The reply is returned in the
// HTTP response only — never appended to the conversation file or the model's
// in-memory context.
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// asideTimeout bounds the one-off LLM call so a slow/unreachable model can't
// wedge the request.
const asideTimeout = 90 * time.Second

// handleBtw answers a quick side question in the context of the session without
// persisting anything. POST /api/sessions/:id/btw body {question} → {answer}.
func handleBtw(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		var req struct {
			Question string `json:"question"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Question == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "question is required"})
			return
		}
		turns, _ := sessions.LoadConversationTurns(id)
		ctx, cancel := context.WithTimeout(c.Request.Context(), asideTimeout)
		defer cancel()
		answer, err := d.Manager.AskAside(ctx, id, req.Question, toExchanges(turns))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"answer": answer})
	}
}

// handleRecap returns a short summary of the session so far. POST
// /api/sessions/:id/recap → {recap}. Reads the persisted transcript only.
func handleRecap(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		turns, _ := sessions.LoadConversationTurns(id)
		ctx, cancel := context.WithTimeout(c.Request.Context(), asideTimeout)
		defer cancel()
		recap, err := d.Manager.Recap(ctx, id, toExchanges(turns))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"recap": recap})
	}
}
