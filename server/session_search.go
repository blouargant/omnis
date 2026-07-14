// session_search.go — searching past chat sessions.
//
// Two paths, one corpus (the persisted conversation files):
//
//   - The LIVE search (this file's routes) answers as the user types. It queries
//     the semantic index when one is available and scans the conversation files
//     directly otherwise, telling the client which happened so it can warn that a
//     scan may be slow.
//   - The AGENT search (no route of its own) runs when the user presses Enter,
//     i.e. when the live list was not good enough. The web UI drives it through a
//     hidden session pinned to the "Session Search" squad — a leaderless, hidden
//     squad whose single member IS the session_search agent, so the query reaches
//     the agent directly with no leader in between. It therefore reuses the normal
//     POST /sessions/:id/messages streaming rail and needs nothing here.
//
// Index freshness has two triggers, deliberately: the idle-indexer rail
// (EventSessionIndexNow — fired 5 minutes after a session goes quiet, and
// immediately on archive) keeps the index warm in the background, and the search
// box kicks POST /search/sessions/refresh as soon as the user starts typing, which
// catches whatever the idle rail has not reached yet. Neither blocks the query:
// a cold index answers by scanning while it builds.
//
// ROUTE PLACEMENT: these live under /api/search/… and NOT /api/sessions/search,
// because a static `search` segment would collide with the /api/sessions/:id/…
// wildcard in gin's route tree (the same reason import lives at /api/import/session).
package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/sessindex"
	"github.com/blouargant/omnis/internal/sessions"
)

// SessionSearchSquad is the squad the web UI search box runs directly. Hidden
// (see SquadEntry.Hidden), so it is neither offered in the squad picker nor
// routable — it exists purely as this feature's entry point.
const SessionSearchSquad = "session search"

// searchWarning tells the client why a search was answered by scanning, so it can
// say something useful rather than silently being slow.
const (
	// warnNoEmbedder — no embedding model is configured. Every search will scan
	// the conversation files directly; on a large history that takes a while.
	warnNoEmbedder = "no_embedder"
	// warnIndexing — an embedder exists but the index is still cold (first search,
	// or a rebuild after the embedding model changed). This search scanned; the
	// index is building in the background and the next one will be instant.
	warnIndexing = "indexing"
)

// refreshGate single-flights index refreshes. The search box fires one on the
// user's first keystroke, and several browsers may be typing at once; without
// this they would each launch a full re-embed pass over the whole corpus.
type refreshGate struct {
	mu      sync.Mutex
	running bool
}

var sessionRefresh refreshGate

// start reports whether the caller acquired the gate (and must call done()).
func (g *refreshGate) start() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running {
		return false
	}
	g.running = true
	return true
}

func (g *refreshGate) done() {
	g.mu.Lock()
	g.running = false
	g.mu.Unlock()
}

func (g *refreshGate) busy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.running
}

// refreshSessionIndex runs an incremental reindex in the background, unless one
// is already in flight. Returns whether it started one.
func refreshSessionIndex(ctx context.Context, idx *sessindex.Index) bool {
	if idx == nil || !sessionRefresh.start() {
		return false
	}
	go func() {
		defer sessionRefresh.done()
		indexed, removed, err := idx.Reindex(ctx)
		if err != nil {
			log.Printf("session-search: reindex failed: %v", err)
			return
		}
		if indexed > 0 || removed > 0 {
			log.Printf("session-search: reindexed %d session(s), removed %d (%d chunks total)",
				indexed, removed, idx.Len())
		}
	}()
	return true
}

// handleSearchSessions answers the live search box.
//
// GET /api/search/sessions?q=…&k=…&exclude_archived=1
func handleSearchSessions(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := c.Query("q")
		k := 10
		if v, err := strconv.Atoi(c.Query("k")); err == nil && v > 0 && v <= 50 {
			k = v
		}
		excludeArchived := c.Query("exclude_archived") == "1" || c.Query("exclude_archived") == "true"

		idx := d.sessionIndex()
		results, mode, stats, err := sessindex.SearchOrScan(c.Request.Context(), idx, query, k, excludeArchived)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		warning := ""
		if mode == sessindex.ModeScan {
			if idx == nil {
				warning = warnNoEmbedder
			} else {
				// The index exists but is cold. Answer by scanning now, and build it
				// so the next search is instant.
				warning = warnIndexing
				refreshSessionIndex(d.rootCtx, idx)
			}
		}
		if results == nil {
			results = []sessindex.Result{}
		}
		c.JSON(http.StatusOK, gin.H{
			"mode":     mode,
			"results":  results,
			"warning":  warning,
			"scanned":  stats.Scanned,
			"took_ms":  stats.TookMs,
			"indexing": sessionRefresh.busy(),
		})
	}
}

// handleRefreshSessionIndex kicks a background incremental reindex. Fired by the
// search box on the user's first keystroke so a conversation that has not yet
// gone idle (and so has not hit the idle-indexer rail) is still findable.
//
// POST /api/search/sessions/refresh
func handleRefreshSessionIndex(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		idx := d.sessionIndex()
		if idx == nil {
			c.JSON(http.StatusOK, gin.H{"started": false, "semantic": false})
			return
		}
		started := refreshSessionIndex(d.rootCtx, idx)
		c.JSON(http.StatusAccepted, gin.H{"started": started, "semantic": true, "indexing": sessionRefresh.busy()})
	}
}

// handleSessionSearchStatus lets the UI warn about a slow scan BEFORE the user
// types, and decide whether to show an "indexing…" hint.
//
// GET /api/search/sessions/status
func handleSessionSearchStatus(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		idx := d.sessionIndex()
		c.JSON(http.StatusOK, gin.H{
			"semantic": idx != nil,
			"chunks":   idx.Len(), // nil-safe
			"indexing": sessionRefresh.busy(),
			"squad":    SessionSearchSquad,
		})
	}
}

// registerSessionIndexHook keeps the session index current in the background.
//
// It rides the EXISTING idle-indexer rail (EventSessionIndexNow), which the
// server already fires for a session that has been idle 5 minutes and for one
// that was just archived — the same trigger the precedent index uses. Two
// properties come free with it: on a fresh boot every persisted session is
// un-indexed, so the first scan pass doubles as the backfill (no separate
// boot-time build), and the whole thing is hash-gated, so on later boots it is a
// no-op that reads each conversation file once.
//
// The index is resolved lazily inside the handler (not at wiring time) so a
// server whose sessions are never searched never opens it.
func registerSessionIndexHook(ctx context.Context, bus *events.Bus, index func() *sessindex.Index) {
	if bus == nil || index == nil {
		return
	}
	bus.Subscribe(events.EventSessionIndexNow, func(_ string, payload map[string]any) {
		sessionID, _ := payload["session_id"].(string)
		if sessionID == "" {
			return
		}
		idx := index()
		if idx == nil {
			return // no embedder: search falls back to scanning, nothing to index
		}
		if err := idx.IndexSession(ctx, sessionID); err != nil {
			log.Printf("session-search: index session %s: %v", sessionID, err)
		}
	})
}

// resetSessionContext makes a session stateless for the turn about to run: it
// drops the persisted history AND clears the model's in-memory context, so the
// turn sees nothing that came before it.
//
// The caller MUST hold the session's run guard. That is the whole reason this
// lives on the turn path (messageRequest.ResetContext) rather than being a
// separate POST /rewind the client fires first: /rewind is tryAcquire-and-409-if-
// busy, while a turn QUEUES on the guard — so a reset racing an in-flight turn was
// silently dropped and the turn ran on the previous turn's context. For the
// session-search agent that meant it saw its own earlier answer, replied "you
// already asked me that, I already found it", and never re-reported its results —
// so the user was told nothing was found for a query it had answered correctly.
//
// Best-effort: a failure is logged and the turn still runs (with its old context)
// rather than failing the user's search outright.
func resetSessionContext(d serverDeps, meta *sessions.SessionMeta) {
	if _, err := sessions.TruncateConversationTurns(meta.ID, 0); err != nil {
		log.Printf("session: reset context for %s: %v", meta.ID, err)
		return
	}
	if d.Registry != nil {
		d.Registry.SetTurns(meta.ID, 0)
	}
	if d.Manager == nil {
		return
	}
	ctx, cancel := context.WithTimeout(d.rootCtx, reseedTimeout)
	defer cancel()
	_ = d.Manager.ReseedSessionContext(ctx, sessionUserID(meta), meta.ID, meta.Squad, nil)
}
