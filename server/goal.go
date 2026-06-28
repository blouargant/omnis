package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	goalpkg "github.com/blouargant/omnis/internal/goal"
	"github.com/blouargant/omnis/internal/sessions"
)

// goalEvalTimeout bounds one evaluator call. The judge is a cheap "small fast"
// model, but a hung gateway must not wedge the producer loop.
const goalEvalTimeout = 30 * time.Second

// buildGoalDirective is the synthetic user turn injected when the evaluator says
// the goal is not yet met. Delegates to the shared goal.Directive so every
// surface phrases the continuation identically.
func buildGoalDirective(condition, reason string) string {
	return goalpkg.Directive(condition, reason)
}

// goalRequest is the body of POST /api/sessions/:id/goal.
type goalRequest struct {
	Condition string `json:"condition"`
}

// goalStatePayload renders a goal snapshot for the API/SSE.
func goalStatePayload(g goalpkg.Goal, exists bool) gin.H {
	if !exists || g.Condition == "" {
		return gin.H{"active": false, "achieved": false}
	}
	return gin.H{
		"active":      g.Active(),
		"achieved":    g.Achieved,
		"condition":   g.Condition,
		"turns":       g.Turns,
		"max_turns":   goalpkg.MaxTurns(),
		"last_reason": g.LastReason,
		"duration_ms": g.Duration().Milliseconds(),
		"tokens":      g.TokensSpent,
	}
}

// handleGoalStatus reports the current (active or just-achieved) goal for a
// session, backing `/goal` with no argument.
func handleGoalStatus(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.GoalStore == nil {
			c.JSON(http.StatusOK, gin.H{"active": false, "achieved": false})
			return
		}
		g, ok := d.GoalStore.Get(id)
		c.JSON(http.StatusOK, goalStatePayload(g, ok))
	}
}

// handleGoalSet installs (or replaces) the session's completion goal. It only
// records the goal — the client then sends the condition as a normal turn, and
// the producer loop drives the autonomous work and evaluation. Returns the goal
// state and broadcasts goal_set so other browsers light their chip.
func handleGoalSet(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if meta.Archived {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived; unarchive it to set a goal"})
			return
		}
		if d.GoalStore == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "goals are not available"})
			return
		}
		var req goalRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		cond := goalpkg.CleanCondition(req.Condition)
		if cond == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "condition is required"})
			return
		}
		g, _ := d.GoalStore.Set(id, cond)
		_ = sessions.SetConversationGoal(id, cond)
		if d.PushEvents != nil {
			d.PushEvents.broadcast("goal_set", id)
		}
		c.JSON(http.StatusOK, goalStatePayload(g, true))
	}
}

// handleGoalClear removes the session's goal (active or achieved), backing
// `/goal clear`. Idempotent.
func handleGoalClear(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		cleared := false
		if d.GoalStore != nil {
			cleared = d.GoalStore.Clear(id)
		}
		_ = sessions.SetConversationGoal(id, "")
		if cleared && d.PushEvents != nil {
			d.PushEvents.broadcast("goal_cleared", id)
		}
		c.JSON(http.StatusOK, gin.H{"cleared": cleared})
	}
}
