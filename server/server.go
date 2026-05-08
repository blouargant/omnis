package main

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/adk/runner"

	toolkitagent "github.com/blouargant/agent-toolkit/agent"
	"github.com/blouargant/agent-toolkit/core/events"
)

type renameRequest struct {
	Title string `json:"title"`
}

type serverDeps struct {
	Token       string
	Runner      *runner.Runner
	Registry    *registry
	WebDir      string
	AgentEvents *agentEventBroadcaster
	// EventBus is the agent event bus, used to emit curate-now events.
	EventBus *events.Bus
	// RegisterSession registers a newly created session in the cross-session
	// mailbox registry so other leaders can address it by name.
	RegisterSession func(userID, sessionID, displayName string) error
	// RenameSession updates the cross-session registry when a session title changes.
	RenameSession func(oldName, newName string) error
	// UnregisterSession removes the session from the cross-session mailbox registry.
	UnregisterSession func(displayName string) error
	// WatchMailbox is forwarded from AgentResult to wire up per-session push.
	WatchMailbox func(ctx context.Context, userID, sessionID string, onMessage func(from, body string))
	// RunGuard serialises runner calls per session (shared with pushManager).
	RunGuard *sessionRunGuard
	// PushMgr manages per-session background mailbox watchers.
	PushMgr *pushManager
	// PushEvents broadcasts push notifications to open /events SSE connections.
	PushEvents *sessionPushBroadcaster
	// rootCtx is the server's root context; used to scope watcher goroutines.
	rootCtx context.Context
}

// agentBusEvent is a single event from the shared event bus forwarded to an
// active SSE stream.
type agentBusEvent struct {
	Event   string
	Payload map[string]any
}

// agentEventBroadcaster registers once on the event bus and fans out sub-agent
// tool events to any number of per-request channels. Using a single persistent
// handler avoids handler accumulation on the bus across many requests.
type agentEventBroadcaster struct {
	mu   sync.RWMutex
	subs map[chan<- agentBusEvent]struct{}
}

func newAgentEventBroadcaster(bus *events.Bus) *agentEventBroadcaster {
	b := &agentEventBroadcaster{subs: make(map[chan<- agentBusEvent]struct{})}
	forward := func(ev string, p map[string]any) {
		// Skip leader events — they are already surfaced by the ADK event stream.
		if agent, _ := p["agent"].(string); agent == "leader" {
			return
		}
		b.mu.RLock()
		for ch := range b.subs {
			select {
			case ch <- agentBusEvent{ev, p}:
			default: // slow subscriber; drop rather than block the tool callback
			}
		}
		b.mu.RUnlock()
	}
	bus.On(events.EventBeforeTool, forward)
	bus.On(events.EventAfterTool, forward)
	bus.On(events.EventToolError, forward)
	return b
}

func (b *agentEventBroadcaster) subscribe() chan agentBusEvent {
	ch := make(chan agentBusEvent, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *agentEventBroadcaster) unsubscribe(ch chan agentBusEvent) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	// Drain so any blocked broadcaster send can proceed.
	for len(ch) > 0 {
		<-ch
	}
}

func newEngine(d serverDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	// Static UI (no auth — served at the root).
	indexPath := filepath.Join(d.WebDir, "index.html")
	r.StaticFile("/", indexPath)
	r.StaticFile("/index.html", indexPath)
	r.Static("/assets", d.WebDir)

	api := r.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	auth := api.Group("", authMiddleware(d.Token))
	auth.POST("/sessions", func(c *gin.Context) {
		meta := d.Registry.New()
		if d.RegisterSession != nil {
			_ = d.RegisterSession(defaultUserID, meta.ID, meta.ID)
		}
		if d.PushMgr != nil {
			d.PushMgr.Watch(d.rootCtx, d, meta.ID, defaultUserID)
		}
		c.JSON(http.StatusCreated, gin.H{
			"session_id": meta.ID,
			"created_at": meta.CreatedAt,
		})
	})
	auth.GET("/sessions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"sessions": d.Registry.List()})
	})
	auth.DELETE("/sessions/:id", func(c *gin.Context) {
		id := c.Param("id")
		// Capture metadata before deleting: display name for the teammate
		// registry and userID for log file paths.
		var displayName string
		userID := defaultUserID
		if meta, ok := d.Registry.Get(id); ok {
			userID = meta.UserID
			if meta.Title != "" {
				displayName = meta.Title
			} else {
				displayName = meta.ID
			}
		}
		if !d.Registry.Delete(id) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.UnregisterSession != nil && displayName != "" {
			_ = d.UnregisterSession(displayName)
		}
		deleteSessionLogs(userID, id)
		if d.PushMgr != nil {
			d.PushMgr.Stop(id)
		}
		c.Status(http.StatusNoContent)
	})
	// GET /api/sessions/:id/events — persistent SSE stream that fires a
	// "mailbox_push" event whenever a background mailbox turn completes for
	// this session. The browser subscribes once per session and reloads
	// conversation history on each push.
	auth.GET("/sessions/:id/events", func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}

		h := c.Writer.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		c.Writer.WriteHeader(http.StatusOK)
		flusher, _ := c.Writer.(interface{ Flush() })
		flush := func() {
			if flusher != nil {
				flusher.Flush()
			}
		}
		flush()

		var ch chan struct{}
		if d.PushEvents != nil {
			ch = d.PushEvents.subscribe(id)
			defer d.PushEvents.unsubscribe(id, ch)
		} else {
			ch = make(chan struct{})
		}

		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()
		ctx := c.Request.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				_, _ = io.WriteString(c.Writer, "event: mailbox_push\ndata: {}\n\n")
				flush()
			case <-heartbeat.C:
				_, _ = io.WriteString(c.Writer, ": keepalive\n\n")
				flush()
			}
		}
	})
	auth.PATCH("/sessions/:id", func(c *gin.Context) {
		id := c.Param("id")
		var body renameRequest
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON"})
			return
		}
		title := strings.TrimSpace(body.Title)

		// Capture the current display name before updating so we can rename
		// the mailbox registry entry from the old name to the new one.
		var oldName string
		if meta, ok := d.Registry.Get(id); ok {
			if meta.Title != "" {
				oldName = meta.Title
			} else {
				oldName = meta.ID
			}
		}

		if !d.Registry.SetTitle(id, title) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if err := setConversationTitle(id, title); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Keep the mailbox registry in sync: move the entry from the old
		// display name to the new title (or back to petname if title cleared).
		if d.RenameSession != nil && oldName != "" {
			newName := title
			if newName == "" {
				newName = id // title cleared → revert to petname
			}
			_ = d.RenameSession(oldName, newName)
		}

		meta, _ := d.Registry.Get(id)
		c.JSON(http.StatusOK, meta)
	})
	auth.GET("/sessions/:id/messages", func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		turns, err := loadConversationTurns(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if turns == nil {
			turns = []ConversationTurn{}
		}
		c.JSON(http.StatusOK, gin.H{"turns": turns})
	})
	auth.POST("/sessions/:id/messages", handleMessages(d))

	auth.POST("/sessions/:id/curate", func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		var req struct {
			Reason    string `json:"reason"`
			Immediate bool   `json:"immediate"`
		}
		_ = c.ShouldBindJSON(&req)
		if req.Reason == "" {
			req.Reason = "manual /learn request from web UI"
		}
		msg, err := toolkitagent.RequestCurateSession(meta.UserID, id, req.Reason)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if req.Immediate && d.EventBus != nil {
			d.EventBus.Emit(events.EventCurateNow, map[string]any{
				"user_id":    meta.UserID,
				"session_id": id,
			})
		}
		c.JSON(http.StatusOK, gin.H{"message": msg})
	})

	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/assets/") {
			return
		}
		gin.DefaultWriter.Write([]byte("" +
			time.Now().Format("15:04:05") + " " +
			c.Request.Method + " " + path + " " +
			itoa(c.Writer.Status()) + " " +
			time.Since(start).Truncate(time.Millisecond).String() + "\n"))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
