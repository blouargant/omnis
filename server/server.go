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

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/askuser"
	"github.com/blouargant/omnis/internal/compress"
	goalpkg "github.com/blouargant/omnis/internal/goal"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/scheduler"
	"github.com/blouargant/omnis/internal/sessions"
	"github.com/blouargant/omnis/internal/steer"
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
	// LiveTurns buffers each in-flight user turn's SSE frames so the agent run
	// survives a client disconnect and a reconnecting browser can replay what it
	// missed. Also backs the Stop button's cancel endpoint.
	LiveTurns *liveTurnRegistry
	// SteerStore holds per-session mid-turn steering notes (extra info the user
	// types while a turn is computing). Backs the /steer endpoint; drained into
	// the running turn by the steering plugin and folded/looped by the producer.
	SteerStore *steer.Store
	// GoalStore holds the per-session /goal completion condition. Backs the
	// /api/sessions/:id/goal routes; the producer loop evaluates it after each
	// turn and keeps working (or stops) accordingly.
	GoalStore *goalpkg.Store
	// PushMgr manages per-session background mailbox watchers.
	PushMgr *pushManager
	// Scheduler runs /loop and /schedule jobs on a timer (process-wide, from
	// the infrastructure). Backs the /api/schedules routes; its fire callback
	// (scheduleFire) is started once from main.go.
	Scheduler *scheduler.Scheduler
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
	// Updates caches the self-update check result; backs the /api/update/* routes.
	Updates *updateState
	// Version is the running server build version (ldflags-injected); reported
	// by /api/update/status and used to gate the self-update check.
	Version string
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
		// Skip the default leader's events — they are already surfaced by the ADK
		// event stream. NOTE: squad roots are named e.g. "omnis"/"knowledge_leader",
		// not "leader", so they slip past this name-based filter; streamEvents drops
		// each hop's actual root by name (rootAgent) so the root is never counted /
		// displayed twice. This filter is just an early-out for the common case.
		if agent, _ := p["agent"].(string); agent == "leader" {
			return
		}
		broadcast(ev, p)
	}
	bus.On(events.EventBeforeTool, forward)
	bus.On(events.EventAfterTool, forward)
	bus.On(events.EventToolError, forward)
	bus.On(events.EventAfterModel, forward) // root deduped per-hop in streamEvents
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
	// Interactive terminal WebSocket. Registered on the unauthenticated group
	// because a browser cannot set an Authorization header on a WebSocket
	// handshake; handleTerminal verifies the bearer token from the `token` query
	// param itself (see its doc comment).
	api.GET("/terminal/ws", handleTerminal(d))

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
			Squad  string `json:"squad"`
			Title  string `json:"title"`
			Dir    string `json:"dir"`
			Hidden bool   `json:"hidden"`
		}
		_ = c.ShouldBindJSON(&body)
		squad := strings.ToLower(strings.TrimSpace(body.Squad))
		if squad == "" {
			// New chats default to the Omnis router squad when routing is
			// enabled, so the first message is routed to the best-suited squad;
			// fall back to the default squad when routing is disabled.
			squad = toolkitagent.DefaultSquadName
			if d.Manager != nil {
				if rs := d.Manager.RouterSquad(); rs != "" {
					squad = rs
				}
			}
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
		// New sessions start at the fixed initial root (bashCwd.get falls back
		// to it), independent of where the global Folders panel has browsed —
		// unless the caller pins a starting folder ("Open Chat here").
		if dir := strings.TrimSpace(body.Dir); dir != "" {
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				bashCwd.set(meta.ID, dir)
			}
		}
		if title := strings.TrimSpace(body.Title); title != "" {
			d.Registry.SetTitle(meta.ID, title)
		}
		// Persist the squad immediately so a server restart before the
		// first turn still sees the right squad on the session.
		_ = sessions.SetConversationSquad(meta.ID, squad)
		// Hidden utility sessions (e.g. the in-Settings assistant) are kept out
		// of the sidebar list but otherwise behave normally.
		if body.Hidden {
			d.Registry.SetHidden(meta.ID, true)
			_ = sessions.SetConversationHidden(meta.ID, true)
		}
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
		// Tell other open browsers a session appeared so their sidebars refresh.
		if d.PushEvents != nil {
			d.PushEvents.broadcast("session_created", meta.ID)
		}
		// Fire SessionStart hooks for this new session. Web-UI sessions never
		// emit the bus EventSessionStart (that is a CLI/TUI front-end signal), so
		// the server drives the hook directly via FireHook. Async so a slow hook
		// never delays the create response.
		if d.Manager != nil {
			go d.Manager.Infra().FireHook(context.Background(), hooks.SessionStart, "", hooks.Input{
				SessionID: meta.ID,
				Cwd:       bashCwd.get(meta.ID),
				Source:    "web",
			})
		}
		c.JSON(http.StatusCreated, gin.H{
			"session_id": meta.ID,
			"created_at": meta.CreatedAt,
			"squad":      meta.Squad,
			"hidden":     body.Hidden,
		})
	})
	auth.GET("/sessions", func(c *gin.Context) {
		// Hidden utility sessions (e.g. the in-Settings assistant) stay in the
		// registry (pinned/watched/retained) but are omitted from the sidebar.
		all := d.Registry.List()
		out := make([]*sessions.SessionMeta, 0, len(all))
		for _, m := range all {
			if m.Hidden {
				continue
			}
			out = append(out, m)
		}
		c.JSON(http.StatusOK, gin.H{"sessions": out})
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
		// Fire SessionEnd hooks. Driven directly (not via the bus EventSessionEnd,
		// which also runs the reflection/curation pipeline that web-UI sessions
		// deliberately skip). Async so a slow hook never delays the response.
		if d.Manager != nil {
			go d.Manager.Infra().FireHook(context.Background(), hooks.SessionEnd, "", hooks.Input{
				SessionID: id,
				Cwd:       bashCwd.get(id),
				Reason:    "delete",
			})
		}
		if d.UnregisterSession != nil && displayName != "" {
			_ = d.UnregisterSession(displayName)
		}
		sessions.DeleteSessionLogs(userID, id)
		deleteSessionUploads(id)
		if d.SteerStore != nil {
			d.SteerStore.Forget(id)
		}
		if d.GoalStore != nil {
			d.GoalStore.Forget(id)
		}
		if d.Scheduler != nil {
			d.Scheduler.RemoveLoopsForSession(id)
		}
		if d.PushMgr != nil {
			d.PushMgr.Stop(id)
		}
		// Release the agent generation pin so an old (draining) generation
		// can be torn down when its last session finishes.
		if d.Manager != nil {
			d.Manager.Release(id)
		}
		// Tell other open browsers the session is gone so they drop it (and
		// close any tab holding it).
		if d.PushEvents != nil {
			d.PushEvents.broadcast("session_deleted", id)
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

	// GET /api/events — a SINGLE multiplexed SSE stream carrying push events for
	// every session, each tagged with its session_id. The browser opens this
	// once instead of one /sessions/:id/events connection per open session,
	// which would otherwise exhaust the browser's ~6-per-host HTTP/1.1
	// connection limit and stall all further requests once enough sessions are
	// open. Delivers the same events as the per-session route (mailbox_push +
	// ask_user / ask_user_cancel), plus session-list changes
	// (session_created / session_deleted / session_renamed) so other open
	// browsers stay in sync, plus a connect-time replay of every pending
	// ask_user question.
	auth.GET("/events", func(c *gin.Context) {
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

		var pushCh chan pushMsg
		if d.PushEvents != nil {
			pushCh = d.PushEvents.subscribeAll()
			defer d.PushEvents.unsubscribeAll(pushCh)
		} else {
			pushCh = make(chan pushMsg)
		}

		// Live ask_user / ask_user_cancel for any session (each payload carries
		// its own session_id, so the client routes it to the right widget).
		var busCh chan agentBusEvent
		if d.AgentEvents != nil {
			busCh = d.AgentEvents.subscribe()
			defer d.AgentEvents.unsubscribe(busCh)
		}

		// Replay every session's pending ask_user questions so a (re)connecting
		// client renders widgets for questions asked before it connected.
		if d.AskUserRegistry != nil && d.Registry != nil {
			for _, meta := range d.Registry.List() {
				for _, q := range d.AskUserRegistry.Pending(meta.ID) {
					data, _ := json.Marshal(askuser.QuestionToPayload(q))
					_, _ = fmt.Fprintf(c.Writer, "event: ask_user\ndata: %s\n\n", data)
				}
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
			case msg := <-pushCh:
				payload := map[string]any{"session_id": msg.SID}
				for k, v := range msg.Data {
					payload[k] = v
				}
				if msg.Text != "" {
					payload["text"] = msg.Text
				}
				data, _ := json.Marshal(payload)
				_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", msg.Event, data)
				flush()
			case be := <-busCh:
				switch be.Event {
				case events.EventAskUser:
					data, _ := json.Marshal(be.Payload)
					_, _ = fmt.Fprintf(c.Writer, "event: ask_user\ndata: %s\n\n", data)
					flush()
				case events.EventAskUserCancel:
					data, _ := json.Marshal(map[string]any{
						"question_id": be.Payload["question_id"],
						"session_id":  be.Payload["session_id"],
					})
					_, _ = fmt.Fprintf(c.Writer, "event: ask_user_cancel\ndata: %s\n\n", data)
					flush()
				}
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

		// Tell other open browsers the title changed so their sidebars refresh.
		if d.PushEvents != nil {
			d.PushEvents.broadcast("session_renamed", id)
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
		if d.Scheduler != nil {
			d.Scheduler.RemoveLoopsForSession(id)
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
		// runningCtx simulates the rolling context window so the context ring can
		// show how full the window is (each turn pays for the full accumulated
		// context as its prompt). That is a *text estimate* and is kept ONLY for
		// the ring (`tokens_used`) + the legacy budget fallback below — it is not
		// the billed cost.
		//
		// The budget is instead the sum of the per-agent per-turn usage persisted
		// from the live `turn_usage` events (real billed counts). Each turn's cost
		// uses the price that was frozen onto that turn, so a later model/price
		// change never rewrites a past turn's budget. Legacy turns with no frozen
		// price fall back to the default rate.
		const defInPerM, defOutPerM = 3.0, 15.0
		type agentAgg struct {
			prompt, output int64
			cost           float64
		}
		var runningCtx, totalPrompt, totalOutput int
		agg := map[string]*agentAgg{}
		for _, turn := range turns {
			userTok := compress.CountText(turn.UserText)
			asstTok := compress.CountText(turn.AssistantText)
			totalPrompt += runningCtx + userTok
			totalOutput += asstTok
			runningCtx += userTok + asstTok
			for name, u := range turn.Usage {
				a := agg[name]
				if a == nil {
					a = &agentAgg{}
					agg[name] = a
				}
				a.prompt += u.Prompt
				a.output += u.Output
				a.cost += u.CostUSD(defInPerM, defOutPerM)
			}
		}
		// Per-agent breakdown + total budget = sum of frozen per-agent costs. When
		// no per-agent usage was ever persisted (fully-legacy session) fall back to
		// pricing the text estimate at the default rate so the budget isn't blank.
		agents := make(map[string]gin.H, len(agg))
		var budget float64
		for name, a := range agg {
			agents[name] = gin.H{"prompt": a.prompt, "output": a.output, "cost": a.cost}
			budget += a.cost
		}
		if len(agg) == 0 {
			budget = (float64(totalPrompt)*defInPerM + float64(totalOutput)*defOutPerM) / 1_000_000
		}
		c.JSON(http.StatusOK, gin.H{
			"tokens_used":   runningCtx,
			"prompt_total":  totalPrompt,
			"output_total":  totalOutput,
			"agents":        agents,
			"budget":        budget,
			"window_tokens": compress.DefaultWindowTokens,
			"soft_limit":    int(float64(compress.DefaultWindowTokens) * compress.DefaultSoftRatio),
			"hard_limit":    int(float64(compress.DefaultWindowTokens) * compress.DefaultHardRatio),
		})
	})

	auth.POST("/sessions/:id/messages", handleMessages(d))
	auth.GET("/sessions/:id/messages/stream", handleMessageStream(d))
	auth.POST("/sessions/:id/cancel", handleCancel(d))
	auth.POST("/sessions/:id/steer", handleSteer(d))
	// /goal — set / inspect / clear a session completion goal. The agent keeps
	// working across turns until the evaluator judges the condition met. See
	// server/goal.go.
	auth.GET("/sessions/:id/goal", handleGoalStatus(d))
	auth.POST("/sessions/:id/goal", handleGoalSet(d))
	auth.DELETE("/sessions/:id/goal", handleGoalClear(d))
	// Read-only "aside" endpoints: answer about the session without touching its
	// conversation (one-off LLM call, nothing persisted). Back /btw and /recap.
	auth.POST("/sessions/:id/btw", handleBtw(d))
	auth.POST("/sessions/:id/recap", handleRecap(d))
	// Scheduled prompts: /loop (in-memory, session-bound) and /schedule (durable
	// routines). See server/scheduler.go.
	auth.GET("/schedules", handleListSchedules(d))
	auth.POST("/schedules", handleCreateSchedule(d))
	auth.PATCH("/schedules/:id", handleUpdateSchedule(d))
	auth.DELETE("/schedules/:id", handleDeleteSchedule(d))
	auth.POST("/schedules/:id/run", handleRunSchedule(d))
	// Conversation fork / rewind: branch a new session at a turn, or rewind the
	// live session to before a turn. Both reseed the model's in-memory context
	// from the kept turns (see server/fork_rewind.go).
	auth.POST("/sessions/:id/rewind", handleRewind(d))
	auth.POST("/sessions/:id/fork", handleFork(d))
	// Spawn a fresh session (empty context) inheriting this session's working
	// directory; an initial task runs in the background (see server/spawn.go).
	auth.POST("/sessions/:id/spawn", handleSpawn(d))
	auth.POST("/sessions/:id/bash", handleBash(d))
	auth.GET("/sessions/:id/folder", handleFolder(d))
	auth.POST("/sessions/:id/folder", handleFolder(d))
	// Upload files/folders from the local machine into the Folders-panel working
	// directory on the host (drag-and-drop / paste).
	auth.POST("/sessions/:id/folder/upload", handleFolderUpload(d))
	// Filesystem Copy/Paste within the Folders panel (host-side file/dir copy).
	auth.POST("/sessions/:id/folder/copy", handleFolderCopy(d))
	// Standard Folders-panel filesystem ops (host-side).
	auth.GET("/sessions/:id/folder/download", handleFolderDownload(d))
	auth.POST("/sessions/:id/folder/delete", handleFolderDelete(d))
	auth.POST("/sessions/:id/folder/new", handleFolderNew(d))
	auth.POST("/sessions/:id/folder/rename", handleFolderRename(d))
	auth.POST("/sessions/:id/folder/move", handleFolderMove(d))
	// Global "no session" environment: browse/navigate the default working
	// directory even when no chat session is active (e.g. a Monaco editor tab).
	auth.GET("/folder", handleGlobalFolder(d))
	auth.POST("/folder", handleGlobalFolder(d))
	auth.POST("/folder/upload", handleGlobalFolderUpload(d))
	auth.POST("/folder/copy", handleGlobalFolderCopy(d))
	auth.GET("/folder/download", handleGlobalFolderDownload(d))
	auth.POST("/folder/delete", handleGlobalFolderDelete(d))
	auth.POST("/folder/new", handleGlobalFolderNew(d))
	auth.POST("/folder/rename", handleGlobalFolderRename(d))
	auth.POST("/folder/move", handleGlobalFolderMove(d))
	// AGENT.md project memory: the "#" quick-append shortcut and the shared
	// "/init" bootstrap prompt.
	auth.GET("/agentmd/init-prompt", handleAgentMDInitPrompt)
	auth.POST("/sessions/:id/agentmd/append", handleAgentMDAppend(d))
	auth.POST("/agentmd/append", handleGlobalAgentMDAppend(d))
	auth.GET("/complete", handleComplete(d))
	auth.GET("/complete-file", handleCompleteFile(d))
	auth.POST("/fileref/resolve", handleFileRefResolve(d))
	auth.GET("/file", handleFileRefRaw(d))
	auth.PUT("/file", handleFileWrite(d))
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
	if d.Updates != nil {
		registerUpdateRoutes(auth, d.Updates, d.Version)
	}
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
