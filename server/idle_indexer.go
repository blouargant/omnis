// idle_indexer.go — the idle indexer: emits EventSessionIndexNow for a
// session once it has been idle longer than indexStaleAfter, so the
// past-session search index picks it up.
//
// Web UI sessions never fire EventSessionEnd. This scanner gives them a
// lightweight, indexing-only trigger on a fixed 5-minute staleness threshold.
//
// For each session it checks:
//
//  1. Session is not already Indexed (skip until new activity resets the flag).
//  2. Session has been idle for at least indexStaleAfter.
//  3. Session has at least one recorded turn (nothing to index otherwise).
//
// When all conditions pass the session is marked Indexed (in-memory) and
// EventSessionIndexNow is emitted. The session-search hook re-indexes the
// conversation (hash-gated, so an unchanged session is a no-op).
package main

import (
	"context"
	"log"
	"time"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/sessions"
)

// indexStaleAfter is the fixed staleness threshold after which an idle session
// is indexed for session search.
const indexStaleAfter = 5 * time.Minute

// indexCheckInterval is how often the idle indexer scans the registry.
const indexCheckInterval = time.Minute

// IdleIndexerConfig configures the idle session indexer.
type IdleIndexerConfig struct {
	Registry *sessions.Registry
	Bus      *events.Bus
}

// startIdleIndexer starts a background goroutine that periodically indexes
// idle sessions for session search. It returns immediately when the
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
		// A session with no turns has nothing to index.
		if meta.Turns < 1 {
			continue
		}
		indexSession(cfg.Registry, cfg.Bus, meta.UserID, meta.ID)
	}
}

// indexSession marks a session Indexed and emits EventSessionIndexNow so the
// session-search hook re-indexes the conversation. Shared by the idle scanner
// and the archive handler.
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
