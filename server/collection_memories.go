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

// memoryStoreForRequest is the caller's memory store (per-user root in cookie
// mode), with the collection's legacy memory.md already imported: every route
// reads or writes through it, and the first Save would create the memory
// directory and strand the legacy file for good. General ("") has no legacy file.
func memoryStoreForRequest(c *gin.Context, collection string) *collmem.Store {
	s := collmem.In(userRoot(requestLogin(c)))
	s.EnsureMigrated(collection)
	return s
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
		mems := memoryStoreForRequest(c, col).List(col)
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
		saved, created, err := memoryStoreForRequest(c, col).Save(col, collmem.Memory{
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
		removed, err := memoryStoreForRequest(c, col).Delete(col, name)
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
// session, an inbound A2A call, a spawned session's result notice, a
// background-task notification — must not write memory: their content did not
// come from the user. Interactive turns, the user's own scheduled routines and
// loops, spawned tasks and durable-question resumes keep write access.
func memoryReadOnlyFor(kind string) bool {
	switch kind {
	case "mailbox", "a2a", "spawn_result", "background":
		return true
	}
	return false
}

// memoryReadOnlyForSession is memoryReadOnlyFor plus the session rule: every
// turn of a hidden utility session (Settings assistant, drafting assistants,
// session search) is memory read-only — its writes would be invisible to the
// user and land in General.
func memoryReadOnlyForSession(kind string, hidden bool) bool {
	return hidden || memoryReadOnlyFor(kind)
}

// handleReviewMemories: POST /api/collections/:name/memories/review → {"ops":[…],
// "error"?}. One model call proposes merges, rewrites and deletions; checks for
// stale project facts and references are added by the host. Nothing is written.
func handleReviewMemories(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		col, ok := resolveMemoryCollection(c)
		if !ok {
			return
		}
		mems := memoryStoreForRequest(c, col).List(col)
		ops, err := d.Manager.ReviewMemories(c.Request.Context(), mems)
		if ops == nil {
			ops = []toolkitagent.ReviewOp{}
		}
		out := gin.H{"ops": ops, "count": len(mems)}
		if err != nil {
			out["error"] = err.Error()
		}
		c.JSON(http.StatusOK, out)
	}
}

// reviewDecision is one approved operation: the op as proposed, plus for a
// "check" the user's answer ("keep" = still true, counted as a confirmation;
// "delete").
type reviewDecision struct {
	toolkitagent.ReviewOp
	Decision string `json:"decision,omitempty"`
}

// handleApplyReview: POST /api/collections/:name/memories/review/apply with
// {"ops":[…approved…]} → {"applied":n,"skipped":[{"names","error"}]}. An
// operation whose memories changed since the proposal is skipped.
func handleApplyReview(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		col, ok := resolveMemoryCollection(c)
		if !ok {
			return
		}
		var in struct {
			Ops []reviewDecision `json:"ops"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		store := memoryStoreForRequest(c, col)
		applied, skipped := applyReviewOps(store, col, in.Ops)
		if applied > 0 {
			broadcastMemoryChangeFromRoute(d, c, col, "", "updated")
		}
		if skipped == nil {
			skipped = []gin.H{}
		}
		c.JSON(http.StatusOK, gin.H{"applied": applied, "skipped": skipped})
	}
}

// applyReviewOps applies approved operations one by one.
func applyReviewOps(store *collmem.Store, col string, ops []reviewDecision) (int, []gin.H) {
	applied := 0
	var skipped []gin.H
	skip := func(op reviewDecision, msg string) {
		skipped = append(skipped, gin.H{"names": op.Names, "error": msg})
	}
	for _, op := range ops {
		if len(op.Names) == 0 {
			continue
		}
		changed := false
		for _, n := range op.Names {
			m, found := store.Get(col, n)
			if !found || toolkitagent.MemoryVersion(m) != op.Versions[n] {
				changed = true
				break
			}
		}
		if changed {
			skip(op, "changed since the review")
			continue
		}
		var err error
		switch op.Op {
		case toolkitagent.ReviewMerge, toolkitagent.ReviewUpdate:
			if op.Into == nil {
				skip(op, "nothing to write")
				continue
			}
			into := collmem.Memory{Name: op.Into.Name, Type: op.Into.Type, Description: op.Into.Description, Body: op.Into.Content}
			_, err = store.MergeInto(col, into, op.Names)
		case toolkitagent.ReviewDelete:
			for _, n := range op.Names {
				if _, derr := store.Delete(col, n); derr != nil && err == nil {
					err = derr
				}
			}
		case toolkitagent.ReviewCheck:
			m, _ := store.Get(col, op.Names[0])
			switch op.Decision {
			case "keep":
				_, _, err = store.SaveWith(col, m, collmem.SaveConfirm)
			case "delete":
				_, err = store.Delete(col, m.Name)
			default:
				continue
			}
		default:
			skip(op, "unknown operation")
			continue
		}
		if err != nil {
			_, msg := memoryRouteError(err)
			skip(op, msg)
			continue
		}
		applied++
	}
	return applied, skipped
}
