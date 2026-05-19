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

// sessionPushBroadcaster holds per-session channels that fire whenever a
// background turn completes for that session (used by the /events SSE endpoint).
type sessionPushBroadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan struct{}]struct{}
}

func newSessionPushBroadcaster() *sessionPushBroadcaster {
	return &sessionPushBroadcaster{subs: make(map[string]map[chan struct{}]struct{})}
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

func (b *sessionPushBroadcaster) notify(sessionID string) {
	b.mu.RLock()
	for ch := range b.subs[sessionID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	b.mu.RUnlock()
}

// pushManager starts one background goroutine per active session that polls
// the leader mailbox. When a cross-session message arrives it injects a
// synthetic runner turn so the agent can process and reply to it.
type pushManager struct {
	guard   *sessionRunGuard
	bcast   *sessionPushBroadcaster
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
	watchFn func(ctx context.Context, userID, sessionID string, onMessage func(from, body string))
}

func newPushManager(
	guard *sessionRunGuard,
	bcast *sessionPushBroadcaster,
	watchFn func(ctx context.Context, userID, sessionID string, onMessage func(from, body string)),
) *pushManager {
	return &pushManager{
		guard:   guard,
		bcast:   bcast,
		cancels: make(map[string]context.CancelFunc),
		watchFn: watchFn,
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

// inject runs a synthetic agent turn for the received mailbox message.
func (pm *pushManager) inject(ctx context.Context, d serverDeps, sessionID, userID, from, body string) {
	if ctx.Err() != nil {
		return
	}

	prompt := fmt.Sprintf("[mailbox] Cross-session message received:\nFrom: %s\nBody: %s", from, body)

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
		if err := appendConversationTurn(sessionID, prompt, reply); err != nil {
			log.Printf("mailbox push: persist failed for %s: %v", sessionID, err)
		}
		d.Registry.Touch(sessionID)
	}

	// Signal any open /events SSE connections so the UI can refresh.
	pm.bcast.notify(sessionID)
}
