package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/internal/hooks"
	"github.com/blouargant/yoke/internal/sessions"
)

// reseedTimeout bounds the in-memory context rebuild so a slow session service
// can never wedge a fork/rewind request.
const reseedTimeout = 30 * time.Second

// toExchanges flattens persisted turns into the agent package's text-only
// Exchange type for ReseedSessionContext (agent cannot import sessions).
func toExchanges(turns []sessions.ConversationTurn) []toolkitagent.Exchange {
	out := make([]toolkitagent.Exchange, 0, len(turns))
	for _, t := range turns {
		out = append(out, toolkitagent.Exchange{User: t.UserText, Assistant: t.AssistantText})
	}
	return out
}

// handleRewind truncates a session's history to its first `turn_index` turns
// (dropping that turn and everything after), then reseeds the model's in-memory
// context so it matches the now-shorter transcript. POST /api/sessions/:id/rewind
// body {turn_index}. Rejects archived sessions and refuses to run while a turn
// is in flight.
func handleRewind(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if meta.Archived {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived; unarchive it to rewind the conversation"})
			return
		}
		var req struct {
			TurnIndex int `json:"turn_index"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if req.TurnIndex < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "turn_index must be >= 0"})
			return
		}

		// Serialise against any live user/mailbox turn so we never truncate a
		// session mid-stream.
		release, okGuard := d.RunGuard.tryAcquire(id)
		if !okGuard {
			c.JSON(http.StatusConflict, gin.H{"error": "session is busy; wait for the current reply to finish"})
			return
		}
		defer release()

		kept, err := sessions.TruncateConversationTurns(id, req.TurnIndex)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		d.Registry.SetTurns(id, len(kept))

		// Reseed the in-memory model context from the kept turns so the next turn
		// continues coherently. Best-effort: a failure is logged inside the helper
		// and never corrupts the (already-truncated) display history.
		if d.Manager != nil {
			ctx, cancel := context.WithTimeout(d.rootCtx, reseedTimeout)
			_ = d.Manager.ReseedSessionContext(ctx, sessionUserID(meta), id, meta.Squad, toExchanges(kept))
			cancel()
		}

		// Tell open browsers (this one and others) to re-render the truncated
		// transcript. Idempotent on the originator.
		if d.PushEvents != nil {
			d.PushEvents.broadcast("session_rewound", id)
		}
		c.JSON(http.StatusOK, gin.H{"turns": kept})
	}
}

// handleFork creates a new session seeded with the first `turn_index` turns of
// the source (a branch point), reseeds its in-memory context, and wires it up
// exactly like POST /sessions. POST /api/sessions/:id/fork body {turn_index,
// title?}. The source session is left untouched, so forking an archived source
// is allowed. Returns {session_id, squad, dropped_user_text}.
func handleFork(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		var req struct {
			TurnIndex int    `json:"turn_index"`
			Title     string `json:"title"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if req.TurnIndex < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "turn_index must be >= 0"})
			return
		}

		// Snapshot the source under its run guard so the fork copies a consistent
		// history (not a half-written one mid-turn).
		release, okGuard := d.RunGuard.tryAcquire(id)
		if !okGuard {
			c.JSON(http.StatusConflict, gin.H{"error": "session is busy; wait for the current reply to finish"})
			return
		}
		srcTurns, _ := sessions.LoadConversationTurns(id)
		droppedUserText := ""
		if req.TurnIndex >= 0 && req.TurnIndex < len(srcTurns) {
			droppedUserText = srcTurns[req.TurnIndex].UserText
		}

		srcSquad := meta.Squad
		title := strings.TrimSpace(req.Title)
		if title == "" {
			base := meta.Title
			if base == "" {
				base = meta.ID
			}
			title = "Fork of " + base
		}

		newMeta := d.Registry.New(srcSquad)
		kept, err := sessions.ForkConversation(id, newMeta.ID, title, req.TurnIndex)
		release()
		if err != nil {
			// Roll back the empty registry entry so a failed fork leaves nothing.
			d.Registry.Delete(newMeta.ID)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		d.Registry.SetTurns(newMeta.ID, len(kept))
		d.Registry.SetTitle(newMeta.ID, title) // in-memory; ForkConversation wrote it to disk

		// The fork inherits the source's working directory so its tools/`!cd`
		// start in the same place.
		bashCwd.set(newMeta.ID, bashCwd.get(id))

		// Mirror the POST /sessions wiring so the fork is a first-class session.
		if d.RegisterSession != nil {
			name := newMeta.ID
			if title != "" {
				name = title
			}
			_ = d.RegisterSession(sessions.DefaultUserID, newMeta.ID, name)
		}
		if d.Manager != nil {
			d.Manager.Pin(newMeta.ID)
			ctx, cancel := context.WithTimeout(d.rootCtx, reseedTimeout)
			_ = d.Manager.ReseedSessionContext(ctx, sessionUserID(newMeta), newMeta.ID, srcSquad, toExchanges(kept))
			cancel()
		}
		if d.PushMgr != nil {
			d.PushMgr.Watch(d.rootCtx, d, newMeta.ID, sessions.DefaultUserID)
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("session_created", newMeta.ID)
		}
		if d.Manager != nil {
			go d.Manager.Infra().FireHook(context.Background(), hooks.SessionStart, "", hooks.Input{
				SessionID: newMeta.ID,
				Cwd:       bashCwd.get(newMeta.ID),
				Source:    "web",
			})
		}

		c.JSON(http.StatusCreated, gin.H{
			"session_id":        newMeta.ID,
			"squad":             newMeta.Squad,
			"dropped_user_text": droppedUserText,
		})
	}
}

// sessionUserID returns the session's user id, falling back to the default web
// user when unset (older persisted sessions).
func sessionUserID(m *sessions.SessionMeta) string {
	if m != nil && m.UserID != "" {
		return m.UserID
	}
	return sessions.DefaultUserID
}
