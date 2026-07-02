package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/core/llm"
	"github.com/blouargant/omnis/internal/bg"
	"github.com/blouargant/omnis/internal/compress"
	"github.com/blouargant/omnis/internal/sessions"
	"github.com/blouargant/omnis/internal/teammates"
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

// busy reports (best-effort, non-blocking) whether a turn is currently in flight
// for sessionID — i.e. the guard is held. Used to let a steer note be enqueued
// for a background/spawned turn that has no liveTurn buffer: the squad's steering
// plugin drains SteerStore at the next model boundary regardless of how the turn
// was started.
func (g *sessionRunGuard) busy(sessionID string) bool {
	v, ok := g.m.Load(sessionID)
	if !ok {
		return false
	}
	return len(v.(chan struct{})) > 0
}

// pushMsg is one multiplexed /api/events payload: a named event plus the
// session id it concerns. "mailbox_push" signals a completed background turn;
// "session_created"/"session_deleted"/"session_renamed" keep other open
// browsers' sidebars in sync with session-list changes.
type pushMsg struct {
	Event string
	SID   string
	// Text carries an optional payload (e.g. a short reply preview for the
	// chat_reply event); empty for events that only need the session id.
	Text string
	// Data carries an optional structured payload merged into the SSE data object
	// alongside session_id (e.g. the context_usage / turn_usage frames delivered
	// for a background turn, which has no per-turn SSE stream). Nil for most events.
	Data map[string]any
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
	b.broadcastWithText(event, sessionID, "")
}

// broadcastWithText is broadcast plus an optional text payload (carried on
// pushMsg.Text and serialised as the SSE data field's "text"). Used by
// chat_reply to ship a short reply preview to every open browser so an
// OS notification can show the first lines of the answer.
func (b *sessionPushBroadcaster) broadcastWithText(event, sessionID, text string) {
	b.mu.RLock()
	for ch := range b.all {
		select {
		case ch <- pushMsg{Event: event, SID: sessionID, Text: text}:
		default:
		}
	}
	b.mu.RUnlock()
}

// broadcastData is broadcast plus a structured JSON payload merged into the SSE
// data object (alongside session_id). Used to deliver live usage frames
// (context_usage / turn_usage) for background/injected turns, which have no
// per-turn SSE stream, over the multiplexed /api/events channel so an open (or
// remoteBusy) session's context ring + budget update live.
func (b *sessionPushBroadcaster) broadcastData(event, sessionID string, data map[string]any) {
	b.mu.RLock()
	for ch := range b.all {
		select {
		case ch <- pushMsg{Event: event, SID: sessionID, Data: data}:
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
	// OMNIS_TASK_NOTIFY. The bg watcher always drains either way so the queue
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
// message. The turn is driven through the Omnis routing dispatch loop
// (injectTurnRouted): a freshly-started session is pinned to the router, so the
// router routes the message to the proper squad; that squad then owns the
// exchange. Because the sender is another live session waiting on this reply,
// the answering squad is given a MANDATORY directive to reply via its mailbox —
// a missing reply would strand the sender's workflow.
func (pm *pushManager) inject(ctx context.Context, d serverDeps, sessionID, userID, from, body string) {
	// The clean record of the message. This is also the view the router sees when
	// it routes: the router has no reply duty, so it must not be shown the reply
	// directive below (it would try to act on it).
	message := fmt.Sprintf("[mailbox] Cross-session message received:\nFrom: %s\nBody: %s", from, body)
	// The answering squad additionally gets the imperative reply directive. The
	// squad root always carries the teammate mailbox tools, so `teammate_tell` /
	// `teammate_ask` addressed to `from` reach the sender's inbox (delivered to
	// them as their own injected turn), enabling both the reply and any follow-up
	// exchange needed to finish the task.
	answer := message + fmt.Sprintf(
		"\n\n[REQUIRED ACTION — do not skip] This message came from another session (%q) that is BLOCKED waiting for your answer. "+
			"When the task is done you MUST send your result back to the sender by calling `teammate_tell` with to=%q "+
			"(use `teammate_ask` with the same `to` if you still need information from them). "+
			"Do NOT end your turn without delivering that reply: the sender's workflow cannot continue until it arrives. "+
			"You may exchange as many messages with %q as the task requires.",
		from, from, from)
	// replyTo=from arms the host-side reply backstop: if the answering squad does
	// not itself send a mailbox reply during the turn, the host forwards the reply
	// to `from` once, so the sender's workflow is never stranded on a missed reply.
	pm.injectTurnRouted(ctx, d, sessionID, userID, answer, message, "mailbox_push", from)
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
// the background-task, scheduler, and spawned-task delivery paths (which have no
// separate router-view / answering-view of the prompt).
// injectTurn returns the assistant reply text so callers that want to act on the
// result (e.g. a spawned task delivering its result back to the originating
// session) can, without re-reading the transcript.
func (pm *pushManager) injectTurn(ctx context.Context, d serverDeps, sessionID, userID, prompt, sseEvent string) string {
	// replyTo="" — these paths (background tasks, scheduler, spawned tasks) have no
	// cross-session sender to reply to, so the backstop is inert.
	return pm.injectTurnRouted(ctx, d, sessionID, userID, prompt, prompt, sseEvent, "")
}

// recordInjectedUsage accumulates one model call's usage for a background/injected
// turn (into acc, freezing the agent's prices) and broadcasts live context_usage +
// turn_usage frames on the multiplexed /api/events stream — a background turn has
// no per-turn SSE stream, so this is how an open (or remoteBusy) session's context
// ring + budget update while it runs. Called only for the answering root agent
// from its session-scoped ADK stream (never the shared bus), so concurrent turns
// on other sessions cannot contaminate it.
func (pm *pushManager) recordInjectedUsage(sessionID, agent string, prompt, output, cacheRead, cacheCreate int64, acc map[string]sessions.TokenUsage, priceFor func(string) agentPrices) {
	if agent == "" {
		return
	}
	pr := priceFor(agent)
	e := acc[agent]
	e.Prompt += prompt
	e.Output += output
	e.CacheRead += cacheRead
	e.CacheCreate += cacheCreate
	if pr.in > 0 || pr.out > 0 {
		e.InPricePerM, e.OutPricePerM = pr.in, pr.out
		e.CacheReadPricePerM, e.CacheCreatePricePerM = pr.cacheRead, pr.cacheCreate
	}
	acc[agent] = e
	if pm.bcast == nil {
		return
	}
	pm.bcast.broadcastData("turn_usage", sessionID, map[string]any{
		"agent":                    agent,
		"prompt_tokens":            prompt,
		"output_tokens":            output,
		"cache_read_tokens":        cacheRead,
		"cache_create_tokens":      cacheCreate,
		"in_price_per_m":           pr.in,
		"out_price_per_m":          pr.out,
		"cache_read_price_per_m":   pr.cacheRead,
		"cache_create_price_per_m": pr.cacheCreate,
	})
	// The latest prompt size IS the current context-window fill; use the default
	// window (the same basis as the cold usage-estimate endpoint) so the ring is
	// consistent whether it's fed live or rebuilt from history on reload.
	window := compress.DefaultWindowTokens
	pm.bcast.broadcastData("context_usage", sessionID, map[string]any{
		"tokens_used":   int(prompt),
		"soft_limit":    int(float64(window) * compress.DefaultSoftRatio),
		"hard_limit":    int(float64(window) * compress.DefaultHardRatio),
		"window_tokens": window,
	})
}

// injectTurnRouted runs a synthetic, run-guarded turn through the Omnis routing
// dispatch loop and persists the reply. It starts at the session's pinned squad
// (meta.Squad): a freshly-started session is pinned to the router, so the router
// routes the message to the proper squad; an already-routed session runs its
// pinned squad directly with no re-route (one hop — byte-identical to a direct
// Runner.Run). This is what lets an inbound cross-session message reach the right
// squad even though the router "has the hand" at session start.
//
// answerPrompt is what every answering (non-router) squad receives; routerPrompt
// is the clean text-only view the router sees when deciding where to route (the
// router has no mailbox/file tools, so it must not see reply directives or
// attachment notes). For every caller but the mailbox they are identical. The
// clean routerPrompt is what gets persisted as the session's user turn.
//
// replyTo, when non-empty (the mailbox path), is the sender's friendly session
// name and arms the host-side reply backstop: if the answering squad did not
// itself call teammate_tell/teammate_ask during the turn, the host forwards the
// turn's reply to that sender exactly once (see sendMailboxBackstop), so a
// workflow-critical reply is never dropped just because the model forgot to send
// it. A squad that did reply/interact suppresses the backstop (no double reply).
func (pm *pushManager) injectTurnRouted(ctx context.Context, d serverDeps, sessionID, userID, answerPrompt, routerPrompt, sseEvent, replyTo string) string {
	if ctx.Err() != nil {
		return ""
	}

	// Serialize with any concurrent user turn for this session.
	release := pm.guard.acquire(sessionID)
	defer release()

	if ctx.Err() != nil {
		return "" // session deleted while waiting for the lock
	}

	meta, ok := d.Registry.Get(sessionID)
	if !ok {
		return ""
	}
	// We hold the run-guard for this session, so any hot-reload that happened
	// between turns can now be applied: migrate the pin to the current generation
	// before the dispatch loop resolves squads.
	d.Manager.MigrateToCurrent(sessionID)
	if d.Manager.LookupSquad(sessionID, meta.Squad) == nil {
		return "" // no runnable squad (e.g. session dropped mid-flight)
	}

	routerSquad := d.Manager.RouterSquad()

	// Per-agent token usage accumulated across every answering hop, persisted on
	// the turn (below) so the session's context ring + cost survive a reload —
	// same data the interactive path records. priceFor freezes the agent's prices
	// onto the usage so a later price change never rewrites this turn's budget.
	// Attributed to the answering ROOT agent from the per-session ADK stream (not
	// the shared, session-unfiltered event bus), so a concurrent turn on another
	// session can never contaminate it; sub-agent tokens are consequently not
	// separately captured for background turns.
	usageAccum := map[string]sessions.TokenUsage{}
	priceFor := agentPriceMap(d.Manager.Lookup(sessionID))
	turnStart := time.Now()

	// repliedToSender records whether the answering squad sent any mailbox message
	// during the turn (teammate_tell/teammate_ask, the squad root's always-on
	// reply channel). When it did, the host backstop below stands down so the
	// sender never gets a duplicate reply.
	repliedToSender := false

	// run executes one squad hop and returns its final assistant text. On the
	// router hop we suppress the router's chatter when it actually routed
	// (PendingRoute) — mirroring the interactive runHop — so only the answering
	// squad's reply is persisted; a router hop that instead talks to the "user"
	// (no route) is kept as the reply.
	run := func(rctx context.Context, sq *toolkitagent.SquadInstance, squadName string, hopParts []*genai.Part) (string, error) {
		rootAgent := "leader"
		if sq.Leader != nil {
			rootAgent = sq.Leader.Name()
		}
		// The router hop's own tiny LLM call is not attributed to a user-facing
		// squad (and its text is dropped), so skip usage accounting for it.
		countUsage := !(routerSquad != "" && squadName == routerSquad)
		seq := sq.Runner.Run(rctx, userID, sessionID,
			&genai.Content{Role: "user", Parts: hopParts}, adkagent.RunConfig{})
		var buf strings.Builder
		seq(func(ev *session.Event, err error) bool {
			if err != nil {
				return false
			}
			if ev == nil {
				return true
			}
			// Usage may arrive on a content-less final event, so account for it
			// before the ev.Content nil-check below. Session-scoped (this runner's
			// stream), so no cross-session contamination.
			if countUsage {
				if u := ev.LLMResponse.UsageMetadata; u != nil {
					cacheRead, cacheCreate := llm.CacheCounts(u)
					pm.recordInjectedUsage(sessionID, rootAgent, int64(u.PromptTokenCount),
						int64(u.CandidatesTokenCount), cacheRead, cacheCreate, usageAccum, priceFor)
				}
			}
			if ev.Content == nil {
				return true
			}
			if ev.LLMResponse.Partial { // skip partial streaming tokens
				return true
			}
			for _, p := range ev.Content.Parts {
				if p == nil {
					continue
				}
				// A mailbox send by the answering squad disarms the backstop.
				if p.FunctionCall != nil {
					if replyTo != "" && (p.FunctionCall.Name == "teammate_tell" || p.FunctionCall.Name == "teammate_ask") {
						repliedToSender = true
					}
					continue
				}
				if p.Text != "" && p.FunctionResponse == nil {
					buf.WriteString(p.Text)
				}
			}
			return true
		})
		if routerSquad != "" && squadName == routerSquad && d.Manager.PendingRoute(sessionID) {
			return "", nil // routed → drop the router's chatter
		}
		return buf.String(), nil
	}

	// notify fires when control moves to another squad: persist the new squad on
	// the session (so follow-up messages from the sender continue in it and it
	// survives a restart) and tell open browsers to update the squad label.
	notify := func(from, to, reason string) {
		d.Registry.SetSquad(sessionID, to)
		_ = sessions.SetConversationSquad(sessionID, to)
		pm.bcast.broadcast("routing", sessionID)
	}

	initialParts := []*genai.Part{{Text: answerPrompt}}
	routerParts := []*genai.Part{{Text: routerPrompt}}
	_, reply, err := d.Manager.RunWithRouting(
		ctx, userID, sessionID, meta.Squad, initialParts, routerParts, run, notify)
	if err != nil {
		log.Printf("mailbox push: routing run error for %s: %v", sessionID, err)
	}

	reply = strings.TrimSpace(reply)
	if reply != "" {
		// Persist the clean message (routerPrompt), not the answer-only reply
		// directive, so the transcript reads as the received message.
		if perr := sessions.AppendConversationTurnFull(sessionID, routerPrompt, reply, usageAccum, time.Since(turnStart).Milliseconds()); perr != nil {
			log.Printf("mailbox push: persist failed for %s: %v", sessionID, perr)
		}
		d.Registry.Touch(sessionID)
	}

	// Host-side reply backstop: a mailbox-originated turn MUST send its result
	// back to the waiting sender. If the answering squad already did so
	// (repliedToSender) we stand down to avoid a duplicate; otherwise the host
	// forwards the reply once so the sender's workflow is never stranded.
	if replyTo != "" && !repliedToSender && reply != "" {
		pm.sendMailboxBackstop(d, userID, sessionID, replyTo, reply)
	}

	// Signal any open /events SSE connections so the UI can refresh.
	if sseEvent == "mailbox_push" {
		pm.bcast.notify(sessionID)
	} else {
		pm.bcast.broadcast(sseEvent, sessionID)
	}
	return reply
}

// sendMailboxBackstop forwards a routed turn's reply back to the originating
// sender when the answering squad did not itself reply. It addresses the sender
// exactly like a normal squad reply would: `replyTo` (the sender's friendly
// session name) is resolved to its canonical mailbox address via the registry,
// and the From is this session's canonical address (NameFunc(...,"leader") — the
// address it is registered/watched under), so the sender can reverse-resolve it
// to this session's friendly name and reply back. A single Send to a single
// inbound message, so it cannot ping-pong. Uses context.Background() (like the
// teammate tools) so delivery is decoupled from the turn's context.
func (pm *pushManager) sendMailboxBackstop(d serverDeps, userID, sessionID, replyTo, body string) {
	if d.Manager == nil {
		return
	}
	infra := d.Manager.Infra()
	if infra == nil || infra.Backend == nil {
		return
	}
	toAddr := replyTo
	if infra.Registry != nil {
		if addr, ok := infra.Registry.Lookup(replyTo); ok {
			toAddr = addr
		}
	}
	fromAddr := infra.NameFunc(userID, sessionID, "leader")
	if err := infra.Backend.Send(context.Background(), toAddr, teammates.Message{From: fromAddr, Body: body}); err != nil {
		log.Printf("mailbox backstop: reply to %q failed for %s: %v", replyTo, sessionID, err)
	}
}
