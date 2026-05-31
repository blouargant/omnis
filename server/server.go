package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/yoke/agent"
	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/askuser"
	"github.com/blouargant/yoke/internal/compress"
	"github.com/blouargant/yoke/internal/sessions"
)

// curateSSETimeout is how long the /curate SSE stream waits for curator_end
// before giving up. Should exceed the curator's own 2-minute run timeout.
const curateSSETimeout = 3 * time.Minute

type renameRequest struct {
	Title string `json:"title"`
}

type serverDeps struct {
	Token string
	// Manager owns the live agent generations. Look up the runner per
	// session via Manager.Lookup(sessionID).Runner so each session uses
	// the agent build it was pinned to.
	Manager     *toolkitagent.Manager
	Registry    *sessions.Registry
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
	// ListSessionRegistry returns the current cross-session registry
	// (display-name → mailbox-address). Used by the GC to detect orphan
	// entries left behind by interrupted session deletions.
	ListSessionRegistry func() map[string]string
	// WatchMailbox is forwarded from the infrastructure to wire up per-session push.
	WatchMailbox func(ctx context.Context, userID, sessionID string, onMessage func(from, body string))
	// RunGuard serialises runner calls per session (shared with pushManager).
	RunGuard *sessionRunGuard
	// PushMgr manages per-session background mailbox watchers.
	PushMgr *pushManager
	// PushEvents broadcasts push notifications to open /events SSE connections.
	PushEvents *sessionPushBroadcaster
	// rootCtx is the server's root context; used to scope watcher goroutines.
	rootCtx context.Context
	// AskUserRegistry receives ask_user tool questions and delivers answers.
	AskUserRegistry *askuser.Registry
	// ConfigFiles holds the absolute paths of the YAML files editable from
	// the web UI (resolved once at startup; never derived from URLs).
	ConfigFiles configFiles
	// SkillsDeps holds the registry and config paths for skills management.
	SkillsDeps skillsDeps
	// AgentOptions are the Options used to build the current agent
	// generation; carried so the hot-reload endpoint can rebuild against the
	// same provider/skills/MCP paths.
	AgentOptions toolkitagent.Options
	// Restart triggers an in-place self re-exec of the server process.
	Restart *restartCoordinator
	// BasePath is the URL prefix under which all routes (UI and API) are
	// served. Empty means root. Always starts with "/" and has no trailing
	// slash when non-empty.
	BasePath string
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
	broadcast := func(ev string, p map[string]any) {
		b.mu.RLock()
		for ch := range b.subs {
			select {
			case ch <- agentBusEvent{ev, p}:
			default: // slow subscriber; drop rather than block the tool callback
			}
		}
		b.mu.RUnlock()
	}
	forward := func(ev string, p map[string]any) {
		// Skip leader events — they are already surfaced by the ADK event stream.
		if agent, _ := p["agent"].(string); agent == "leader" {
			return
		}
		broadcast(ev, p)
	}
	bus.On(events.EventBeforeTool, forward)
	bus.On(events.EventAfterTool, forward)
	bus.On(events.EventToolError, forward)
	bus.On(events.EventAfterModel, forward) // sub-agent model calls only; leader is in ADK stream
	bus.On(events.EventCompressionSkipped, broadcast)
	bus.On(events.EventCompressionEnd, broadcast)
	// ask_user events are forwarded verbatim to all active SSE streams so
	// the browser can render the question widget. The cancel event lets
	// the browser dismiss the widget when a question is resolved server-side.
	bus.On(events.EventAskUser, broadcast)
	bus.On(events.EventAskUserCancel, broadcast)
	bus.On(events.EventCuratorStart, broadcast)
	bus.On(events.EventCuratorEnd, broadcast)
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

	// Propagate server shutdown into every request context so long-lived SSE
	// handlers terminate promptly when rootCtx is cancelled (e.g. on restart).
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithCancel(c.Request.Context())
		stopFn := context.AfterFunc(d.rootCtx, cancel)
		defer stopFn()
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})

	// base is the router group that all routes (UI and API) are registered
	// under. When BasePath is empty every route sits at the root; otherwise
	// they all live under e.g. "/my-company/myself".
	base := r.Group(d.BasePath)

	// Static UI (no auth — served at the root of the base path).
	indexPath := filepath.Join(d.WebDir, "index.html")
	faviconPath := filepath.Join(d.WebDir, "favicon.svg")
	indexHandler := serveIndex(indexPath, d.BasePath)
	base.GET("/", indexHandler)
	base.GET("/index.html", indexHandler)
	base.StaticFile("/favicon.svg", faviconPath)
	base.StaticFile("/favicon.ico", faviconPath)
	base.Static("/assets", d.WebDir)

	api := base.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	auth := api.Group("", authMiddleware(d.Token))
	// POST /api/admin/gc — trigger a one-shot garbage-collection sweep over
	// logs/ and logs/uploads/. Returns counts per category so ops tooling
	// can verify what was cleaned. The periodic background sweep keeps
	// running independently.
	auth.POST("/admin/gc", func(c *gin.Context) {
		stats := runGC(d.Registry, gcDeps{
			listRegistry: d.ListSessionRegistry,
			unregister:   d.UnregisterSession,
		})
		logGCStats("admin", stats)
		c.JSON(http.StatusOK, stats)
	})
	// POST /api/registries/reindex — rebuild the semantic registry index over
	// the configured remote registries (skills + agents metadata). Backs the
	// "Reindex" button in the consolidated Settings → Registries section.
	// Returns 400 when no embedding model is configured (the index is absent
	// and every recall path falls back to glob/browse).
	auth.POST("/registries/reindex", func(c *gin.Context) {
		if d.Manager == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no agent instance"})
			return
		}
		inst := d.Manager.Current()
		if inst == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no agent instance"})
			return
		}
		idx := d.Manager.Infra().RegistryIndex(c.Request.Context(), inst.Settings)
		if idx == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "registry index unavailable (no embedding model configured)"})
			return
		}
		n, err := idx.Reindex(c.Request.Context())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"indexed": n})
	})
	auth.POST("/sessions", func(c *gin.Context) {
		// Body is optional: clients that don't care about squads can POST
		// with no body or `{}` and get the default squad.
		var body struct {
			Squad string `json:"squad"`
			Title string `json:"title"`
		}
		_ = c.ShouldBindJSON(&body)
		squad := strings.ToLower(strings.TrimSpace(body.Squad))
		if squad == "" {
			squad = toolkitagent.DefaultSquadName
		}
		// Reject unknown squad names so the client sees the misconfiguration
		// immediately rather than silently falling back to default later.
		if d.Manager != nil && !d.Manager.HasSquad(squad) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("unknown squad %q", squad),
			})
			return
		}
		meta := d.Registry.New(squad)
		if title := strings.TrimSpace(body.Title); title != "" {
			d.Registry.SetTitle(meta.ID, title)
		}
		// Persist the squad immediately so a server restart before the
		// first turn still sees the right squad on the session.
		_ = sessions.SetConversationSquad(meta.ID, squad)
		if title := strings.TrimSpace(body.Title); title != "" {
			_ = sessions.SetConversationTitle(meta.ID, title)
		}
		if d.RegisterSession != nil {
			name := meta.ID
			if meta.Title != "" {
				name = meta.Title
			}
			_ = d.RegisterSession(sessions.DefaultUserID, meta.ID, name)
		}
		// Pin the new session to the current agent generation so it stays
		// on that generation even if a reload happens mid-conversation.
		if d.Manager != nil {
			d.Manager.Pin(meta.ID)
		}
		if d.PushMgr != nil {
			d.PushMgr.Watch(d.rootCtx, d, meta.ID, sessions.DefaultUserID)
		}
		c.JSON(http.StatusCreated, gin.H{
			"session_id": meta.ID,
			"created_at": meta.CreatedAt,
			"squad":      meta.Squad,
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
		userID := sessions.DefaultUserID
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
		sessions.DeleteSessionLogs(userID, id)
		deleteSessionUploads(id)
		if d.PushMgr != nil {
			d.PushMgr.Stop(id)
		}
		// Release the agent generation pin so an old (draining) generation
		// can be torn down when its last session finishes.
		if d.Manager != nil {
			d.Manager.Release(id)
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

		// Replay any pending ask_user questions so a reconnecting client
		// can render widgets for questions that were asked before reconnect.
		if d.AskUserRegistry != nil {
			for _, q := range d.AskUserRegistry.Pending(id) {
				data, _ := json.Marshal(askuser.QuestionToPayload(q))
				_, _ = fmt.Fprintf(c.Writer, "event: ask_user\ndata: %s\n\n", data)
			}
			flush()
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
		if err := sessions.SetConversationTitle(id, title); err != nil {
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
	// POST /api/sessions/:id/archive — set a session aside as read-only. The
	// session stays in the registry (so the GC keeps its files and it keeps
	// feeding semantic-recall indexes) but is detached from its agent
	// generation: the push watcher stops and the generation pin is released.
	auth.POST("/sessions/:id/archive", func(c *gin.Context) {
		id := c.Param("id")
		if !d.Registry.SetArchived(id, true) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.PushMgr != nil {
			d.PushMgr.Stop(id)
		}
		if d.Manager != nil {
			d.Manager.Release(id)
		}
		if d.PushEvents != nil {
			d.PushEvents.notify(id)
		}
		// Archiving is a natural session-end signal: push the session's
		// StateLog into the precedent index now rather than waiting for the
		// idle indexer's staleness threshold.
		meta, _ := d.Registry.Get(id)
		if meta != nil && d.EventBus != nil {
			indexSession(d.Registry, d.EventBus, meta.UserID, id)
		}
		c.JSON(http.StatusOK, meta)
	})
	// POST /api/sessions/:id/unarchive — restore an archived session to active.
	// Re-pin it to the current generation and restart its push watcher so
	// background mailbox turns are delivered again.
	auth.POST("/sessions/:id/unarchive", func(c *gin.Context) {
		id := c.Param("id")
		if !d.Registry.SetArchived(id, false) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		userID := sessions.DefaultUserID
		if meta, ok := d.Registry.Get(id); ok && meta.UserID != "" {
			userID = meta.UserID
		}
		if d.Manager != nil {
			d.Manager.Pin(id)
		}
		if d.PushMgr != nil {
			d.PushMgr.Watch(d.rootCtx, d, id, userID)
		}
		if d.PushEvents != nil {
			d.PushEvents.notify(id)
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
		turns, err := sessions.LoadConversationTurns(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if turns == nil {
			turns = []sessions.ConversationTurn{}
		}
		c.JSON(http.StatusOK, gin.H{"turns": turns})
	})
	auth.GET("/sessions/:id/usage-estimate", func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		turns, err := sessions.LoadConversationTurns(id)
		if err != nil || len(turns) == 0 {
			c.JSON(http.StatusOK, gin.H{
				"tokens_used": 0, "prompt_total": 0, "output_total": 0,
				"window_tokens": compress.DefaultWindowTokens,
				"soft_limit":    int(float64(compress.DefaultWindowTokens) * compress.DefaultSoftRatio),
				"hard_limit":    int(float64(compress.DefaultWindowTokens) * compress.DefaultHardRatio),
			})
			return
		}
		// Simulate the rolling context to estimate API billing:
		// each turn pays for the full accumulated context as its prompt.
		var runningCtx, totalPrompt, totalOutput int
		for _, turn := range turns {
			userTok := compress.CountText(turn.UserText)
			asstTok := compress.CountText(turn.AssistantText)
			totalPrompt += runningCtx + userTok
			totalOutput += asstTok
			runningCtx += userTok + asstTok
		}
		c.JSON(http.StatusOK, gin.H{
			"tokens_used":   runningCtx,
			"prompt_total":  totalPrompt,
			"output_total":  totalOutput,
			"window_tokens": compress.DefaultWindowTokens,
			"soft_limit":    int(float64(compress.DefaultWindowTokens) * compress.DefaultSoftRatio),
			"hard_limit":    int(float64(compress.DefaultWindowTokens) * compress.DefaultHardRatio),
		})
	})

	auth.POST("/sessions/:id/messages", handleMessages(d))
	auth.POST("/sessions/:id/bash", handleBash(d))
	auth.GET("/complete", handleComplete(d))
	auth.GET("/complete-file", handleCompleteFile(d))
	auth.POST("/fileref/resolve", handleFileRefResolve(d))
	auth.GET("/file", handleFileRefRaw(d))
	auth.POST("/sessions/:id/files", handleFileUpload(d))
	auth.GET("/sessions/:id/media", handleMedia(d))
	auth.POST("/sessions/:id/ask-user/:qid", handleAskUserResponse(d))

	root, _ := os.Getwd()
	auth.GET("/browse", handleBrowse(root))

	auth.POST("/sessions/:id/compact", func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.EventBus != nil {
			d.EventBus.Emit(events.EventCompressNow, map[string]any{
				"user_id":    meta.UserID,
				"session_id": id,
			})
		}
		c.JSON(http.StatusOK, gin.H{"message": "compression queued for next turn"})
	})

	registerConfigRoutes(auth, d.ConfigFiles, d.Restart, d.Manager, d.AgentOptions)
	registerPreferencesRoutes(auth, newPreferencesStore(d.ConfigFiles))
	userCmdStore := newUserCommandsStore()
	registerUserCommandsRoutes(auth, userCmdStore)
	registerCommandsRoutes(auth.Group("/commands"), userCmdStore)
	registerProviderModelsRoute(auth)
	registerAgentMetaRoutes(auth)
	registerSkillsRoutes(auth.Group("/skills"), d.SkillsDeps)
	registerAgentsRoutes(auth.Group("/agents"))
	registerMCPRoutes(auth.Group("/mcp"))
	registerA2ARoutes(auth.Group("/a2a"))
	registerSquadsRegistryRoutes(auth.Group("/squads-registry"))
	registerPermissionsRoutes(auth.Group("/permissions-registry"))

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
		_, err := toolkitagent.RequestCurateSession(meta.UserID, id, req.Reason)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if req.Immediate && d.EventBus != nil {
			// Return an SSE stream so the browser can render a live curator block.
			h := c.Writer.Header()
			h.Set("Content-Type", "text/event-stream")
			h.Set("Cache-Control", "no-cache")
			h.Set("Connection", "keep-alive")
			h.Set("X-Accel-Buffering", "no")
			c.Writer.WriteHeader(http.StatusOK)
			c.Writer.Flush()

			sseEmit := func(event string, payload any) {
				data, err := json.Marshal(payload)
				if err != nil {
					return
				}
				_, _ = io.WriteString(c.Writer, "event: "+event+"\ndata: "+string(data)+"\n\n")
				if f, ok := c.Writer.(interface{ Flush() }); ok {
					f.Flush()
				}
			}

			// Subscribe before emitting the trigger so no events are missed.
			subCh := d.AgentEvents.subscribe()
			defer d.AgentEvents.unsubscribe(subCh)

			d.EventBus.Emit(events.EventCurateNow, map[string]any{
				"user_id":    meta.UserID,
				"session_id": id,
			})

			ctx := c.Request.Context()
			timeout := time.NewTimer(curateSSETimeout)
			defer timeout.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timeout.C:
					sseEmit("done", map[string]any{})
					return
				case be := <-subCh:
					// Filter events to this session only.
					if sid, _ := be.Payload["session_id"].(string); sid != id {
						continue
					}
					switch be.Event {
					case events.EventCuratorStart:
						sseEmit("curator_start", map[string]any{"session_id": id})
					case events.EventCuratorEnd:
						p := be.Payload
						summary, _ := p["summary"].(string)
						skipped, _ := p["skipped"].(bool)
						reason, _ := p["reason"].(string)
						errMsg, _ := p["error"].(string)
						sseEmit("curator_end", map[string]any{
							"session_id": id,
							"summary":    summary,
							"skipped":    skipped,
							"reason":     reason,
							"error":      errMsg,
						})
						sseEmit("done", map[string]any{})
						return
					}
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{"message": "curation scheduled for session end"})
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
			strconv.Itoa(c.Writer.Status()) + " " +
			time.Since(start).Truncate(time.Millisecond).String() + "\n"))
	}
}

// serveIndex returns a handler that serves index.html with a <base> tag and
// window.BASE_PATH injected so the web UI works correctly under a sub-path.
// The injection is a simple string insertion; the file is read once on the
// first request and then served from the cached bytes.
func serveIndex(indexPath, basePath string) gin.HandlerFunc {
	baseHref := basePath + "/"
	if basePath == "" {
		baseHref = "/"
	}
	injection := "<base href=\"" + baseHref + "\" />\n  " +
		"<script>window.BASE_PATH = \"" + basePath + "\";</script>"

	var once sync.Once
	var cached []byte
	var cacheErr error

	load := func() {
		data, err := os.ReadFile(indexPath)
		if err != nil {
			cacheErr = err
			return
		}
		cached = []byte(strings.Replace(string(data), "<meta charset", injection+"\n  <meta charset", 1))
	}

	return func(c *gin.Context) {
		once.Do(load)
		if cacheErr != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Data(http.StatusOK, "text/html; charset=utf-8", cached)
	}
}
