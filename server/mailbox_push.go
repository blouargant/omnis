package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/blouargant/yoke/internal/bg"
	"github.com/blouargant/yoke/internal/sessions"
)

// sessionRunGuard ensures at most one runner.Run call is in flight per session.
// Both handleMessages (user turns) and pushManager (background turns) acquire
// the guard before calling the runner, so they never race each other.
type sessionRunGuard struct {
	m sync.Map // sessionID → chan struct{} (buffered capacity 1)
}

func newSessionRunGuard() *sessionRunGuard { return &sessionRunGuard{} }

func (g *sessionRunGuard) acquire(sessionID string) (release func()) {
	v, _ := g.m.LoadOrStore(sessionID, make(chan struct{}, 1))
	sem := v.(chan struct{})
	sem <- struct{}{} // blocks until any concurrent turn finishes
	return func() { <-sem }
}

// tryAcquire attempts to acquire the per-session guard without blocking.
// Returns (release, true) on success; (no-op, false) when another goroutine
// already holds the guard for this session — callers should skip and try
// again later instead of blocking. Used by the idle-rebind scanner to avoid
// tearing down an Instance that is mid-stream.
func (g *sessionRunGuard) tryAcquire(sessionID string) (release func(), ok bool) {
	v, _ := g.m.LoadOrStore(sessionID, make(chan struct{}, 1))
	sem := v.(chan struct{})
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	default:
		return func() {}, false
	}
}

// pushMsg is one multiplexed /api/events payload: a named event plus the
// session id it concerns. "mailbox_push" signals a completed background turn;
// "session_created"/"session_deleted"/"session_renamed" keep other open
// browsers' sidebars in sync with session-list changes.
type pushMsg struct {
	Event string
	SID   string
}

// sessionPushBroadcaster holds per-session channels that fire whenever a
// background turn completes for that session (used by the /events SSE endpoint).
type sessionPushBroadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan struct{}]struct{}
	// all holds multiplexed subscribers that want push notifications for every
	// session over a single connection (each receives the notifying session id
	// plus the event name). This lets one client hold ONE SSE connection for all
	// its open sessions instead of one per session — which would otherwise
	// exhaust the browser's ~6-per-host HTTP/1.1 connection limit and stall
	// further requests.
	all map[chan pushMsg]struct{}
}

func newSessionPushBroadcaster() *sessionPushBroadcaster {
	return &sessionPushBroadcaster{
		subs: make(map[string]map[chan struct{}]struct{}),
		all:  make(map[chan pushMsg]struct{}),
	}
}

func (b *sessionPushBroadcaster) subscribe(sessionID string) chan struct{} {
	ch := make(chan struct{}, 4)
	b.mu.Lock()
	if b.subs[sessionID] == nil {
		b.subs[sessionID] = make(map[chan struct{}]struct{})
	}
	b.subs[sessionID][ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sessionPushBroadcaster) unsubscribe(sessionID string, ch chan struct{}) {
	b.mu.Lock()
	if m, ok := b.subs[sessionID]; ok {
		delete(m, ch)
	}
	b.mu.Unlock()
}

// subscribeAll registers a multiplexed subscriber that receives a pushMsg for
// every session event. Used by the single /api/events stream.
func (b *sessionPushBroadcaster) subscribeAll() chan pushMsg {
	ch := make(chan pushMsg, 16)
	b.mu.Lock()
	b.all[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *sessionPushBroadcaster) unsubscribeAll(ch chan pushMsg) {
	b.mu.Lock()
	delete(b.all, ch)
	b.mu.Unlock()
}

// notify signals both the per-session subscribers (the legacy
// /sessions/:id/events route) and every multiplexed /api/events subscriber that
// a background turn completed for sessionID (a "mailbox_push").
func (b *sessionPushBroadcaster) notify(sessionID string) {
	b.mu.RLock()
	for ch := range b.subs[sessionID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	b.mu.RUnlock()
	b.broadcast("mailbox_push", sessionID)
}

// broadcast sends a named event carrying sessionID to every multiplexed
// /api/events subscriber so other open browsers stay in sync. Used for
// session-list changes (created/deleted/renamed); the per-session subs are
// untouched because they only understand mailbox_push.
func (b *sessionPushBroadcaster) broadcast(event, sessionID string) {
	b.mu.RLock()
	for ch := range b.all {
		select {
		case ch <- pushMsg{Event: event, SID: sessionID}:
		default:
		}
	}
	b.mu.RUnlock()
}

// pushManager starts one background goroutine per active session that polls
// the leader mailbox. When a cross-session message arrives it injects a
// synthetic runner turn so the agent can process and reply to it.
type pushManager struct {
	guard     *sessionRunGuard
	bcast     *sessionPushBroadcaster
	mu        sync.Mutex
	cancels   map[string]context.CancelFunc
	watchFn   func(ctx context.Context, userID, sessionID string, onMessage func(from, body string))
	watchBgFn func(ctx context.Context, userID, sessionID string, onNotify func([]bg.Notification))
	// activeWake controls whether a completed background task injects a synthetic
	// turn (model reacts) or merely fires a UI toast (passive). Set from
	// YOKE_TASK_NOTIFY. The bg watcher always drains either way so the queue
	// never wedges at its buffer limit.
	activeWake bool
}

func newPushManager(
	guard *sessionRunGuard,
	bcast *sessionPushBroadcaster,
	watchFn func(ctx context.Context, userID, sessionID string, onMessage func(from, body string)),
	watchBgFn func(ctx context.Context, userID, sessionID string, onNotify func([]bg.Notification)),
	activeWake bool,
) *pushManager {
	return &pushManager{
		guard:      guard,
		bcast:      bcast,
		cancels:    make(map[string]context.CancelFunc),
		watchFn:    watchFn,
		watchBgFn:  watchBgFn,
		activeWake: activeWake,
	}
}

// Watch starts watching the mailbox for sessionID. Subsequent calls for the
// same sessionID are no-ops. rootCtx should be the server's root context.
func (pm *pushManager) Watch(rootCtx context.Context, d serverDeps, sessionID, userID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if _, already := pm.cancels[sessionID]; already {
		return
	}
	ctx, cancel := context.WithCancel(rootCtx)
	pm.cancels[sessionID] = cancel

	pm.watchFn(ctx, userID, sessionID, func(from, body string) {
		pm.inject(ctx, d, sessionID, userID, from, body)
	})
	if pm.watchBgFn != nil {
		pm.watchBgFn(ctx, userID, sessionID, func(batch []bg.Notification) {
			pm.injectNotification(ctx, d, sessionID, userID, batch)
		})
	}
}

// Stop cancels the watcher for sessionID (call on session deletion).
func (pm *pushManager) Stop(sessionID string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if cancel, ok := pm.cancels[sessionID]; ok {
		cancel()
		delete(pm.cancels, sessionID)
	}
}

// inject runs a synthetic agent turn for a received cross-session mailbox
// message.
func (pm *pushManager) inject(ctx context.Context, d serverDeps, sessionID, userID, from, body string) {
	prompt := fmt.Sprintf("[mailbox] Cross-session message received:\nFrom: %s\nBody: %s", from, body)
	pm.injectTurn(ctx, d, sessionID, userID, prompt, "mailbox_push")
}

// injectNotification delivers a batch of completed/streamed background-task
// notifications. With active wake it injects a synthetic turn so the model
// reacts to the result; otherwise it just fires a UI toast (the result stays
// readable via task_output). The bg watcher has already drained the queue, so
// either path keeps it from wedging.
func (pm *pushManager) injectNotification(ctx context.Context, d serverDeps, sessionID, userID string, batch []bg.Notification) {
	if ctx.Err() != nil || len(batch) == 0 {
		return
	}
	if !pm.activeWake {
		pm.bcast.broadcast("task_notification", sessionID)
		return
	}
	pm.injectTurn(ctx, d, sessionID, userID, bg.FormatBatch(batch), "task_notification")
}

// injectTurn runs a synthetic, run-guarded agent turn carrying prompt, persists
// the reply, and fires the given SSE event so open UI tabs refresh. Shared by
// the mailbox and background-task delivery paths.
func (pm *pushManager) injectTurn(ctx context.Context, d serverDeps, sessionID, userID, prompt, sseEvent string) {
	if ctx.Err() != nil {
		return
	}

	// Serialize with any concurrent user turn for this session.
	release := pm.guard.acquire(sessionID)
	defer release()

	if ctx.Err() != nil {
		return // session deleted while waiting for the lock
	}

	// Resolve the session's squad inside its pinned generation. Fall back
	// to the default squad when the recorded squad is no longer present
	// (e.g. it was renamed/removed by a later config edit).
	meta, ok := d.Registry.Get(sessionID)
	if !ok {
		return
	}
	// We hold the run-guard for this session, so any hot-reload that
	// happened between turns can now be applied: migrate the pin to the
	// current generation before resolving the squad.
	d.Manager.MigrateToCurrent(sessionID)
	sq := d.Manager.LookupSquad(sessionID, meta.Squad)
	if sq == nil || sq.Runner == nil {
		return
	}

	seq := sq.Runner.Run(
		ctx, userID, sessionID,
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
		adkagent.RunConfig{},
	)

	var buf strings.Builder
	seq(func(ev *session.Event, err error) bool {
		if err != nil {
			return false
		}
		if ev == nil || ev.Content == nil {
			return true
		}
		// Skip partial streaming tokens; collect only final text.
		if ev.LLMResponse.Partial {
			return true
		}
		for _, p := range ev.Content.Parts {
			if p != nil && p.Text != "" && p.FunctionCall == nil && p.FunctionResponse == nil {
				buf.WriteString(p.Text)
			}
		}
		return true
	})

	reply := strings.TrimSpace(buf.String())
	if reply != "" {
		if err := sessions.AppendConversationTurn(sessionID, prompt, reply); err != nil {
			log.Printf("mailbox push: persist failed for %s: %v", sessionID, err)
		}
		d.Registry.Touch(sessionID)
	}

	// Signal any open /events SSE connections so the UI can refresh.
	if sseEvent == "mailbox_push" {
		pm.bcast.notify(sessionID)
	} else {
		pm.bcast.broadcast(sseEvent, sessionID)
	}
}
