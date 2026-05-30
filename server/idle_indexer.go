// idle_indexer.go — the idle indexer: pushes a session's StateLog (goal +
// decisions) into the cross-session precedent index once the session has been
// idle longer than indexStaleAfter.
//
// Web UI sessions never fire EventSessionEnd, so the reflection pipeline (and
// with it the precedents hook) never runs for them. This scanner closes that
// gap with a lightweight, indexing-only trigger that is independent of the
// soft-skills curator: it runs on a fixed 5-minute staleness threshold whether
// or not YOKE_CURATOR_IDLE_TIMEOUT is set.
//
// For each session it checks:
//
//  1. Session is not already Indexed (skip until new activity resets the flag).
//  2. Session has been idle for at least indexStaleAfter.
//  3. Session has at least one recorded turn (trivial sessions have no goal).
//
// When all conditions pass the session is marked Indexed (in-memory) and
// EventSessionIndexNow is emitted. The precedents hook reads the on-disk
// StateLog and upserts it; when no embedder is configured the hook is not
// registered and the event is a harmless no-op.
package main

import (
	"context"
	"log"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/sessions"
)

// indexStaleAfter is the fixed staleness threshold after which an idle session
// is indexed into the precedent store.
const indexStaleAfter = 5 * time.Minute

// indexCheckInterval is how often the idle indexer scans the registry.
const indexCheckInterval = time.Minute

// IdleIndexerConfig configures the idle session indexer.
type IdleIndexerConfig struct {
	Registry *sessions.Registry
	Bus      *events.Bus
}

// startIdleIndexer starts a background goroutine that periodically indexes
// idle sessions into the precedent store. It returns immediately when the
// registry or bus is nil.
func startIdleIndexer(ctx context.Context, cfg IdleIndexerConfig) {
	if cfg.Bus == nil || cfg.Registry == nil {
		return
	}
	log.Printf("indexer: enabled (stale_after=%s, check_interval=%s)", indexStaleAfter, indexCheckInterval)
	go func() {
		t := time.NewTicker(indexCheckInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				scanStaleSessions(cfg)
			}
		}
	}()
}

func scanStaleSessions(cfg IdleIndexerConfig) {
	now := time.Now()
	for _, meta := range cfg.Registry.List() {
		if meta.Indexed {
			continue
		}
		if now.Sub(meta.LastUsedAt) < indexStaleAfter {
			continue
		}
		// A session with no turns has no goal/decisions to index.
		if meta.Turns < 1 {
			continue
		}
		indexSession(cfg.Registry, cfg.Bus, meta.UserID, meta.ID)
	}
}

// indexSession marks a session Indexed and emits EventSessionIndexNow so the
// precedents hook upserts its StateLog. Shared by the idle scanner and the
// archive handler.
func indexSession(registry *sessions.Registry, bus *events.Bus, userID, sessionID string) {
	if bus == nil || sessionID == "" {
		return
	}
	if registry != nil {
		registry.MarkIndexed(sessionID)
	}
	bus.Emit(events.EventSessionIndexNow, map[string]any{
		"user_id":    userID,
		"session_id": sessionID,
	})
}
