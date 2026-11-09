package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/sessions"
)

// memoryCollectionLabel is the collection name shown to the web UI: General
// for the "" collection.
func memoryCollectionLabel(collection string) string {
	if collection == "" {
		return sessions.GeneralCollection
	}
	return collection
}

// resolveMemoryCollection maps the :name path param to the collection the
// memory store addresses: General (any case) ⇒ "", any other name ⇒ the
// canonical stored name of one of the caller's collections. It writes the 4xx
// response itself when the name is unknown.
func resolveMemoryCollection(c *gin.Context) (string, bool) {
	name := strings.TrimSpace(c.Param("name"))
	if strings.EqualFold(name, sessions.GeneralCollection) {
		return "", true
	}
	return resolveKnownCollection(c)
}

// memoryStoreForRequest is the caller's memory store (per-user root in cookie mode).
func memoryStoreForRequest(c *gin.Context) *collmem.Store {
	return collmem.In(userRoot(requestLogin(c)))
}

// memoryRouteError maps a collmem error to an HTTP status + message.
func memoryRouteError(err error) (int, string) {
	switch {
	case errors.Is(err, collmem.ErrInvalidName):
		return http.StatusBadRequest, "invalid memory name: use lower-case letters, digits and hyphens"
	case errors.Is(err, collmem.ErrInvalidType):
		return http.StatusBadRequest, "invalid memory type: use feedback, user, project or reference"
	case errors.Is(err, collmem.ErrTooLarge):
		return http.StatusBadRequest, "memory body is too long (4000 characters at most)"
	case errors.Is(err, collmem.ErrFull):
		return http.StatusBadRequest, "this collection already holds 200 memories: delete one first"
	case errors.Is(err, collmem.ErrCollection):
		return http.StatusBadRequest, "this collection name cannot hold a memory"
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

// broadcastMemoryChangeFromRoute sends memory_changed to the caller's browsers.
func broadcastMemoryChangeFromRoute(d serverDeps, c *gin.Context, collection, name, op string) {
	if d.PushEvents == nil {
		return
	}
	d.PushEvents.broadcastOwnedData("memory_changed", "", requestLogin(c), map[string]any{
		"collection": memoryCollectionLabel(collection), "name": name, "op": op,
	})
}

// handleListMemories: GET /api/collections/:name/memories → {"memories":[…]}.
func handleListMemories(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		col, ok := resolveMemoryCollection(c)
		if !ok {
			return
		}
		mems := memoryStoreForRequest(c).List(col)
		if mems == nil {
			mems = []collmem.Memory{}
		}
		c.JSON(http.StatusOK, gin.H{"memories": mems})
	}
}

// handlePutMemory: PUT /api/collections/:name/memories/:mem with
// {description,type,body} → {"memory":Memory,"created":bool}.
func handlePutMemory(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		col, ok := resolveMemoryCollection(c)
		if !ok {
			return
		}
		var in struct {
			Description string `json:"description"`
			Type        string `json:"type"`
			Body        string `json:"body"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if strings.TrimSpace(in.Body) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "memory body is empty"})
			return
		}
		saved, created, err := memoryStoreForRequest(c).Save(col, collmem.Memory{
			Name:        c.Param("mem"),
			Description: in.Description,
			Type:        strings.TrimSpace(in.Type),
			Body:        in.Body,
		})
		if err != nil {
			code, msg := memoryRouteError(err)
			c.JSON(code, gin.H{"error": msg})
			return
		}
		op := "updated"
		if created {
			op = "created"
		}
		broadcastMemoryChangeFromRoute(d, c, col, saved.Name, op)
		c.JSON(http.StatusOK, gin.H{"memory": saved, "created": created})
	}
}

// handleDeleteMemory: DELETE /api/collections/:name/memories/:mem → {"ok":true}.
func handleDeleteMemory(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		col, ok := resolveMemoryCollection(c)
		if !ok {
			return
		}
		name, valid := collmem.NormalizeName(c.Param("mem"))
		if !valid {
			code, msg := memoryRouteError(collmem.ErrInvalidName)
			c.JSON(code, gin.H{"error": msg})
			return
		}
		removed, err := memoryStoreForRequest(c).Delete(col, name)
		if err != nil {
			code, msg := memoryRouteError(err)
			c.JSON(code, gin.H{"error": msg})
			return
		}
		if !removed {
			c.JSON(http.StatusNotFound, gin.H{"error": "memory not found"})
			return
		}
		broadcastMemoryChangeFromRoute(d, c, col, name, "deleted")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// newMemoryChangeHook returns the notifier the agent's memory tools call after
// a write: a session-scoped memory_changed (owner-filtered like any session event).
func newMemoryChangeHook(d serverDeps) func(sessionID, collection, name, op string) {
	return func(sessionID, collection, name, op string) {
		if d.PushEvents == nil {
			return
		}
		d.PushEvents.broadcastData("memory_changed", sessionID, map[string]any{
			"collection": memoryCollectionLabel(collection), "name": name, "op": op,
		})
	}
}

// installMemoryChangeHook installs newMemoryChangeHook on the agent package and
// returns the func that removes it.
func installMemoryChangeHook(d serverDeps) func() {
	toolkitagent.SetMemoryChangeHook(newMemoryChangeHook(d))
	return func() { toolkitagent.SetMemoryChangeHook(nil) }
}

// memoryReadOnlyFor reports whether a turn of this kind runs with a read-only
// collection memory. Turns the user did not start — a message from another
// session, an inbound A2A call, a spawned session's result notice — must not
// write memory: their content did not come from the user. Interactive turns,
// the user's own scheduled routines and loops, background-task notifications
// and durable-question resumes keep write access.
func memoryReadOnlyFor(kind string) bool {
	switch kind {
	case "mailbox", "a2a", "spawn_result":
		return true
	}
	return false
}
