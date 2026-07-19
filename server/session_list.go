package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// handleListSessions backs GET /api/sessions. It has two shapes decided by the
// presence of the `limit` query param:
//
//   - **legacy** (no `limit`): returns the full non-hidden session list, exactly
//     as the original handler did — `{sessions:[…]}`, no total. Keeps every other
//     consumer (and the tests) byte-compatible.
//   - **paginated** (`limit` present): filters by effective collection, archived
//     flag, and a title/id substring `q`, applies `sort`, then slices
//     `[offset:offset+limit]` and returns `{sessions, total, offset, limit}` where
//     `total` is the filtered count BEFORE slicing (drives the toolbar count and
//     the client's exhaustion check).
//
// Hidden utility sessions (e.g. the in-Settings assistant) are always excluded.
// See docs/superpowers/specs/2026-07-19-session-list-pagination-design.md.
func handleListSessions(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		all := d.Registry.List() // already last_used desc, tie-break created desc

		// Legacy path — no pagination requested.
		if c.Query("limit") == "" {
			out := make([]*sessions.SessionMeta, 0, len(all))
			for _, m := range all {
				if m.Hidden {
					continue
				}
				out = append(out, m)
			}
			c.JSON(http.StatusOK, gin.H{"sessions": out})
			return
		}

		// Paginated path.
		limit, err := strconv.Atoi(c.Query("limit"))
		if err != nil || limit < 0 {
			limit = 0
		}
		offset, err := strconv.Atoi(c.Query("offset"))
		if err != nil || offset < 0 {
			offset = 0
		}
		collection := strings.TrimSpace(c.Query("collection"))
		filterColl := collection != ""
		wantArchived := strings.EqualFold(c.Query("archived"), "true")
		q := strings.ToLower(strings.TrimSpace(c.Query("q")))

		// Known user collections (lower-cased) so a blank or dangling collection
		// folds to General, matching the client's effectiveCollection().
		known := map[string]bool{}
		if names, err := sessions.ListCollections(); err == nil {
			for _, n := range names {
				known[strings.ToLower(n)] = true
			}
		}

		filtered := make([]*sessions.SessionMeta, 0, len(all))
		for _, m := range all {
			if m.Hidden {
				continue
			}
			if m.Archived != wantArchived {
				continue
			}
			if filterColl && !strings.EqualFold(effectiveCollection(m, known), collection) {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(sessionDisplayName(m)), q) &&
				!strings.Contains(strings.ToLower(m.ID), q) {
				continue
			}
			filtered = append(filtered, m)
		}

		// Sort. "recent" keeps Registry.List() order (last_used desc); the others
		// re-sort so ordering stays stable across page boundaries.
		switch c.Query("sort") {
		case "created":
			sort.SliceStable(filtered, func(i, j int) bool {
				return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
			})
		case "az":
			sort.SliceStable(filtered, func(i, j int) bool {
				return strings.ToLower(sessionDisplayName(filtered[i])) <
					strings.ToLower(sessionDisplayName(filtered[j]))
			})
		}

		total := len(filtered)
		start := offset
		if start > total {
			start = total
		}
		end := start + limit
		if end > total {
			end = total
		}
		page := filtered[start:end]
		if page == nil {
			page = []*sessions.SessionMeta{}
		}
		c.JSON(http.StatusOK, gin.H{
			"sessions": page,
			"total":    total,
			"offset":   offset,
			"limit":    limit,
		})
	}
}

// handleSessionIDs backs GET /api/session-ids — the slim id-only list of every
// non-hidden session. The web UI fetches it once at boot to validate its
// persisted pane layout (drop tabs for sessions deleted while the app was
// closed): with the session list now paginated, the loaded window is not the full
// universe, so a full-payload validation would wrongly drop off-page tabs. Lives
// at /api/session-ids (NOT /api/sessions/ids, which would collide with the
// /api/sessions/:id/… wildcard in gin's route tree and panic at startup — same
// reason import lives at /api/import/session).
func handleSessionIDs(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		all := d.Registry.List()
		ids := make([]string, 0, len(all))
		for _, m := range all {
			if m.Hidden {
				continue
			}
			ids = append(ids, m.ID)
		}
		c.JSON(http.StatusOK, gin.H{"ids": ids})
	}
}

// effectiveCollection resolves the collection a session actually shows under,
// mirroring the client's fold: a blank collection, or one naming a collection no
// longer in `known`, belongs to the virtual General bucket.
func effectiveCollection(m *sessions.SessionMeta, known map[string]bool) string {
	n := sessions.NormalizeCollectionName(m.Collection) // "" for blank/General
	if n == "" || !known[strings.ToLower(n)] {
		return sessions.GeneralCollection
	}
	return n
}

// sessionDisplayName is the label the UI shows for a session: its title, or the
// petname id when untitled. Used for `q` matching and `az` sorting.
func sessionDisplayName(m *sessions.SessionMeta) string {
	if strings.TrimSpace(m.Title) != "" {
		return m.Title
	}
	return m.ID
}
