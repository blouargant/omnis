// idle_rebind.go — releases the pin of Web UI sessions that are idle and
// still attached to a previous agent generation, so the old generation can
// be torn down (MCP subprocesses freed, plugins closed) instead of
// lingering for as long as the chat tab stays open.
//
// The scanner is race-safe against an in-flight turn: it uses the per-
// session run-guard via tryAcquire (non-blocking) and skips any session
// that currently holds the guard. The session is unpinned via
// Manager.Release; the next user turn naturally re-pins to the current
// generation through Manager.Lookup.
//
// Configurable via OMNIS_SESSION_REBIND_IDLE. Default 5s. Set to "0" to
// disable.
package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

const defaultRebindIdleTimeout = 5 * time.Second

// IdleRebindConfig wires the scanner to its dependencies.
type IdleRebindConfig struct {
	Manager     *agent.Manager
	Registry    *sessions.Registry
	Guard       *sessionRunGuard
	IdleTimeout time.Duration // 0 disables
}

// resolveRebindIdle parses OMNIS_SESSION_REBIND_IDLE. Empty/invalid →
// defaultRebindIdleTimeout. "0" → disabled.
func resolveRebindIdle() time.Duration {
	raw := strings.TrimSpace(os.Getenv("OMNIS_SESSION_REBIND_IDLE"))
	if raw == "" {
		return defaultRebindIdleTimeout
	}
	if raw == "0" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return defaultRebindIdleTimeout
	}
	return d
}

func (c IdleRebindConfig) checkInterval() time.Duration {
	interval := c.IdleTimeout / 3
	if interval < time.Second {
		interval = time.Second
	}
	if interval > 30*time.Second {
		interval = 30 * time.Second
	}
	return interval
}

// startIdleRebind launches a background goroutine that periodically
// releases pins for idle sessions on old generations. Returns immediately
// when IdleTimeout <= 0 (feature disabled).
func startIdleRebind(ctx context.Context, cfg IdleRebindConfig) {
	if cfg.IdleTimeout <= 0 || cfg.Manager == nil || cfg.Registry == nil || cfg.Guard == nil {
		return
	}
	interval := cfg.checkInterval()
	log.Printf("rebind: enabled (idle_timeout=%s, check_interval=%s)", cfg.IdleTimeout, interval)
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				scanIdleRebind(cfg)
			}
		}
	}()
}

func scanIdleRebind(cfg IdleRebindConfig) {
	current := cfg.Manager.CurrentGeneration()
	now := time.Now()
	for _, meta := range cfg.Registry.List() {
		pinned := cfg.Manager.PinnedGeneration(meta.ID)
		if pinned == 0 || pinned == current {
			continue
		}
		if !meta.LastUsedAt.IsZero() && now.Sub(meta.LastUsedAt) < cfg.IdleTimeout {
			continue
		}
		release, ok := cfg.Guard.tryAcquire(meta.ID)
		if !ok {
			// A turn is in progress for this session — skip; we'll
			// retry on the next tick.
			continue
		}
		cfg.Manager.Release(meta.ID)
		release()
		log.Printf("rebind: session %s released from gen %d (current=%d)", meta.ID, pinned, current)
	}
}
