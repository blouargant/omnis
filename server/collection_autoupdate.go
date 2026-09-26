// collection_autoupdate.go — the per-collection auto-update worker. When a
// collection has auto_update on, it re-distils the collection's recent chats into
// its memory and COMMITS the result automatically, keeping a memory.prev.md
// snapshot so the change can be reverted (see server/collections.go revert route).
//
// It rides the EXISTING idle rail (EventSessionIndexNow — fired for a session
// idle ≥5 min and on archive), so "after some idle time" needs no new timer. Two
// further gates keep it cheap and safe: a content hash (skip when the recent
// chats have not changed since the last distill) and a min-interval (a busy
// collection cannot churn the model). Server-only; a collection with auto_update
// off makes the per-event handler return immediately.
package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/sessions"
)

// shouldAutoUpdate is the pure gate predicate: fire only when the collection opts
// in, its recent chats changed, and the min-interval since the last auto-commit
// has elapsed.
func shouldAutoUpdate(autoUpdate, changed bool, sinceLast, minInterval time.Duration) bool {
	return autoUpdate && changed && sinceLast >= minInterval
}

// autoUpdateMinInterval reads OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL (default 30m).
func autoUpdateMinInterval() time.Duration {
	const def = 30 * time.Minute
	if v := strings.TrimSpace(os.Getenv("OMNIS_COLLECTION_AUTOUPDATE_MIN_INTERVAL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// autoUpdater holds the worker's dependencies. gather/distill are injectable so
// the commit logic is testable without a live model.
type autoUpdater struct {
	deps        serverDeps
	minInterval time.Duration
	gather      func(collection, owner string) string
	distill     func(ctx context.Context, current, material string, wordLimit int) (string, error)

	mu          sync.Mutex
	inflight    map[string]bool
	lastHash    map[string]string
	lastAttempt map[string]time.Time
}

// runCollection applies the gates and, if they pass, distils + commits the
// collection's memory. Safe to call concurrently for different collections; a
// per-collection in-flight guard prevents overlapping runs of the same one.
// owner scopes the resulting collections_changed broadcast (cookie identity
// mode): the UserID of the session whose idle tick triggered this run — the
// collection itself carries no owner, so this is the only owner available.
func (au *autoUpdater) runCollection(ctx context.Context, collection, owner string) {
	if collection == "" {
		return
	}
	// Per-user state in cookie mode (owner's store); the shared root otherwise.
	login := au.deps.ownerRootLogin(owner)
	cols := sessions.CollectionsIn(userRoot(login))
	store := ctxStoreFor(login)
	prof := cols.CollectionProfileFull(collection)
	if !prof.AutoUpdate {
		return
	}
	material := au.gather(collection, owner)
	if strings.TrimSpace(material) == "" {
		return
	}
	h := materialHash(material)
	// Gate state is keyed per (owner root, collection): two users' same-named
	// collections are distinct collections.
	key := login + "\x00" + collection

	now := time.Now()
	au.mu.Lock()
	gateFrom := time.Unix(prof.LastMemoryUpdate, 0)
	if la := au.lastAttempt[key]; la.After(gateFrom) {
		gateFrom = la
	}
	changed := au.lastHash[key] != h
	if !shouldAutoUpdate(prof.AutoUpdate, changed, now.Sub(gateFrom), au.minInterval) || au.inflight[key] {
		au.mu.Unlock()
		return
	}
	au.inflight[key] = true
	au.lastAttempt[key] = now // advance the throttle even if the distill below fails
	au.mu.Unlock()
	defer func() {
		au.mu.Lock()
		delete(au.inflight, key)
		au.mu.Unlock()
	}()

	cur := store.ReadMemory(collection)
	proposed, err := au.distill(ctx, cur, material, toolkitagent.SizeWordLimit(prof.MemorySize))
	if err != nil {
		log.Printf("collection auto-update: distill %q: %v", collection, err)
		return
	}
	proposed = strings.TrimSpace(proposed)
	// Record the hash even on a no-op so we don't re-distill identical material.
	au.mu.Lock()
	au.lastHash[key] = h
	au.mu.Unlock()
	if proposed == "" || proposed == strings.TrimSpace(cur) {
		return // nothing changed — no write, no snapshot
	}
	// A manual edit (or anything) may have changed memory.md during the slow
	// distill above. Re-read and skip the auto-commit if so, rather than
	// clobbering the user's edit (its snapshot would also be wrong).
	if store.ReadMemory(collection) != cur {
		log.Printf("collection auto-update: %q memory changed during distill; skipping commit", collection)
		return
	}
	if strings.TrimSpace(cur) != "" {
		_ = store.WritePrevMemory(collection, cur)
	}
	if err := store.WriteMemory(collection, proposed); err != nil {
		log.Printf("collection auto-update: write %q: %v", collection, err)
		return
	}
	_ = cols.SetCollectionMemoryUpdate(collection, now.Unix())
	if au.deps.PushEvents != nil {
		au.deps.PushEvents.broadcastOwned("collections_changed", "", owner)
	}
	log.Printf("collection auto-update: committed memory for %q", collection)
}

// newAutoUpdater builds the worker with the production gather: only the
// owner's sessions in cookie mode (every session otherwise). distill is left
// for the caller to set.
func newAutoUpdater(d serverDeps, minInterval time.Duration) *autoUpdater {
	return &autoUpdater{
		deps:        d,
		minInterval: minInterval,
		gather: func(c, owner string) string {
			return gatherCollectionMaterialFor(d, c, d.ownerRootLogin(owner))
		},
		inflight:    map[string]bool{},
		lastHash:    map[string]string{},
		lastAttempt: map[string]time.Time{},
	}
}

// startCollectionAutoUpdate subscribes to the idle rail and drives runCollection
// off the main thread (Emit is synchronous; distillation is a slow LLM call).
func startCollectionAutoUpdate(ctx context.Context, d serverDeps, minInterval time.Duration) {
	if d.EventBus == nil || d.Manager == nil || d.Registry == nil {
		return
	}
	au := newAutoUpdater(d, minInterval)
	au.distill = func(ctx context.Context, cur, material string, wl int) (string, error) {
		return d.Manager.DistillCollectionMemory(ctx, cur, material, wl)
	}
	log.Printf("collection auto-update: enabled (min_interval=%s)", minInterval)
	d.EventBus.Subscribe(events.EventSessionIndexNow, func(_ string, payload map[string]any) {
		sid, _ := payload["session_id"].(string)
		if sid == "" {
			return
		}
		meta, ok := d.Registry.Get(sid)
		if !ok {
			return
		}
		collection := sessions.NormalizeCollectionName(meta.Collection)
		if collection == "" {
			return // General has no context
		}
		go au.runCollection(ctx, collection, meta.UserID)
	})
}
