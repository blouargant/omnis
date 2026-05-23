// idle_curator.go — the idle harvester: fires the soft-skills curator for
// sessions that have been idle longer than IdleTimeout.
//
// The harvester runs every CheckInterval (default: IdleTimeout/3, capped at
// 5 minutes). For each session it checks:
//
//  1. Session is not already marked as Harvested (skip entirely if true).
//  2. Session has been idle for at least IdleTimeout.
//  3. Session has at least 2 turns (Turns >= 2); trivial sessions are skipped.
//
// When all conditions pass, the session is marked Harvested (in-memory and
// persisted to disk) and EventCurateNow is emitted. A harvested session is
// never re-evaluated until Touch() clears the flag on new user activity.
//
// The Harvested flag survives server restarts (persisted in the conversation
// file), unlike the previous timestamp-based approach. The curator hook's own
// concurrency guard prevents double-firing even if the harvester and an
// explicit /learn-now overlap.
package main

import (
	"context"
	"log"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/sessions"
)

// minCheckInterval caps how frequently the idle scanner runs, regardless of
// the configured IdleTimeout, to avoid busy-polling on very short timeouts.
const minCheckInterval = 30 * time.Second

// IdleCuratorConfig configures the idle session scanner.
type IdleCuratorConfig struct {
	Registry      *sessions.Registry
	Bus           *events.Bus
	IdleTimeout   time.Duration // 0 = disabled
	CheckInterval time.Duration // 0 = auto (IdleTimeout/3, capped at 5min)
}

func (c IdleCuratorConfig) checkInterval() time.Duration {
	if c.CheckInterval > 0 {
		return c.CheckInterval
	}
	auto := c.IdleTimeout / 3
	if auto < minCheckInterval {
		auto = minCheckInterval
	}
	if auto > 5*time.Minute {
		auto = 5 * time.Minute
	}
	return auto
}

// startIdleCurator starts a background goroutine that periodically checks for
// idle sessions and emits EventCurateNow for those that qualify. It returns
// immediately when IdleTimeout is zero (disabled).
func startIdleCurator(ctx context.Context, cfg IdleCuratorConfig) {
	if cfg.IdleTimeout <= 0 || cfg.Bus == nil || cfg.Registry == nil {
		return
	}
	interval := cfg.checkInterval()
	log.Printf("harvester: enabled (idle_timeout=%s, check_interval=%s)", cfg.IdleTimeout, interval)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				scanIdleSessions(cfg)
			}
		}
	}()
}

func scanIdleSessions(cfg IdleCuratorConfig) {
	now := time.Now()
	for _, meta := range cfg.Registry.List() {
		// Already harvested — skip entirely until new activity resets the flag.
		if meta.Harvested {
			continue
		}
		idle := now.Sub(meta.LastUsedAt)
		if idle < cfg.IdleTimeout {
			continue
		}
		// Skip trivial sessions unlikely to have learnable content.
		if meta.Turns < 2 {
			continue
		}
		// Mark harvested before emitting so the flag is set even if the
		// curator's pre-flight gate decides to skip the LLM evaluation.
		cfg.Registry.MarkHarvested(meta.ID)
		log.Printf("harvester: firing curation for session %s (idle=%s, turns=%d)", meta.ID, idle.Round(time.Second), meta.Turns)
		cfg.Bus.Emit(events.EventCurateNow, map[string]any{
			"user_id":    meta.UserID,
			"session_id": meta.ID,
		})
	}
}
