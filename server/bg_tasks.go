package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/bg"
)

// A session's background tasks — the commands and monitors its agent launched
// with bash_background / monitor — for the environment panel's Background
// tasks card: list them, stop one, read one's output.

// sessionBgQueue returns the task queue of a session, keyed like the agent's
// own tools and the background watcher: (the session's user, its id). Nil
// without a Manager. A variable so tests can hand in a queue of their own.
var sessionBgQueue = func(d serverDeps, userID, sessionID string) *bg.Queue {
	if d.Manager == nil {
		return nil
	}
	infra := d.Manager.Infra()
	if infra == nil || infra.BgQueues == nil {
		return nil
	}
	return infra.BgQueues.For(userID, sessionID)
}

// handleSessionTasks serves GET /api/sessions/:id/tasks: the session's tasks,
// newest first.
func handleSessionTasks(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		tasks := []bg.TaskInfo{}
		if q := sessionBgQueue(d, meta.UserID, id); q != nil {
			tasks = q.ListTasks()
		}
		c.JSON(http.StatusOK, gin.H{"tasks": tasks})
	}
}

// handleCancelSessionTask serves POST /api/sessions/:id/tasks/:tid/cancel —
// the card's stop button. Stopping an already finished task is a no-op.
func handleCancelSessionTask(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		q := sessionBgQueue(d, meta.UserID, id)
		if q == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}
		info, ok := q.Cancel(c.Param("tid"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"task": info})
	}
}

// handleSessionTaskOutput serves GET /api/sessions/:id/tasks/:tid/output: the
// task's buffered output (its last 500 lines).
func handleSessionTaskOutput(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		q := sessionBgQueue(d, meta.UserID, id)
		if q == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}
		out, info, ok := q.Output(c.Param("tid"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"task": info, "output": out})
	}
}
