package main

import (
	"net/http"
	"strings"

	"github.com/blouargant/omnis/internal/sessions"
	"github.com/gin-gonic/gin"
)

// collectionInfo is one row in the GET /api/collections response: the display
// name plus the number of (non-hidden) sessions filed under it.
type collectionInfo struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	// General is true only for the synthetic default bucket, so the client can
	// pin it on top and hide its rename/delete affordances.
	General bool `json:"general,omitempty"`
}

// collectionCounts tallies non-hidden sessions by their effective collection.
// A session whose Collection field is blank — or names a collection no longer in
// the stored list — folds into General, so the totals always reconcile with the
// session list the sidebar shows.
func collectionCounts(d serverDeps, known []string) map[string]int {
	knownByFold := make(map[string]string, len(known))
	for _, n := range known {
		knownByFold[strings.ToLower(n)] = n
	}
	counts := map[string]int{sessions.GeneralCollection: 0}
	for _, n := range known {
		counts[n] = 0
	}
	for _, m := range d.Registry.List() {
		if m.Hidden {
			continue
		}
		name := sessions.NormalizeCollectionName(m.Collection)
		if name == "" {
			counts[sessions.GeneralCollection]++
			continue
		}
		if canon, ok := knownByFold[strings.ToLower(name)]; ok {
			counts[canon]++
		} else {
			// Session references a deleted/unknown collection → General.
			counts[sessions.GeneralCollection]++
		}
	}
	return counts
}

// handleListCollections returns the ordered collection list (General first, then
// user-created collections in their stored order), each with a live session count.
func handleListCollections(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		known, err := sessions.ListCollections()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		counts := collectionCounts(d, known)
		out := make([]collectionInfo, 0, len(known)+1)
		out = append(out, collectionInfo{Name: sessions.GeneralCollection, Count: counts[sessions.GeneralCollection], General: true})
		for _, n := range known {
			out = append(out, collectionInfo{Name: n, Count: counts[n]})
		}
		c.JSON(http.StatusOK, gin.H{"collections": out})
	}
}

// handleCreateCollection adds a new (empty) collection.
func handleCreateCollection(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var body struct {
			Name string `json:"name"`
		}
		_ = c.ShouldBindJSON(&body)
		name := strings.TrimSpace(body.Name)
		if !sessions.ValidCollectionName(name) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection name"})
			return
		}
		if _, _, err := sessions.AddCollection(name); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusCreated, gin.H{"name": name})
	}
}

// handleRenameCollection renames a collection and cascades the change onto every
// session filed under the old name, so no session is orphaned.
func handleRenameCollection(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		old := strings.TrimSpace(c.Param("name"))
		if old == "" || strings.EqualFold(old, sessions.GeneralCollection) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the General collection cannot be renamed"})
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		_ = c.ShouldBindJSON(&body)
		newName := strings.TrimSpace(body.Name)
		if !sessions.ValidCollectionName(newName) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid collection name"})
			return
		}
		_, ok, err := sessions.RenameCollection(old, newName)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
			return
		}
		// Cascade onto member sessions (in-memory + persisted).
		for _, m := range d.Registry.List() {
			if strings.EqualFold(sessions.NormalizeCollectionName(m.Collection), old) {
				d.Registry.SetCollection(m.ID, newName)
			}
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"name": newName})
	}
}

// handleDeleteCollection removes a collection; its member sessions fall back to
// the General bucket (their Collection field is cleared).
func handleDeleteCollection(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimSpace(c.Param("name"))
		if name == "" || strings.EqualFold(name, sessions.GeneralCollection) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "the General collection cannot be deleted"})
			return
		}
		_, ok, err := sessions.RemoveCollection(name)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
			return
		}
		for _, m := range d.Registry.List() {
			if strings.EqualFold(sessions.NormalizeCollectionName(m.Collection), name) {
				d.Registry.SetCollection(m.ID, "") // → General
			}
		}
		if d.PushEvents != nil {
			d.PushEvents.broadcast("collections_changed", "")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// handleMoveSession files a session under a collection (empty ⇒ General). The
// target must be General or an existing collection; an unknown name is rejected
// so a typo can't strand a session under a phantom collection.
func handleMoveSession(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		var body struct {
			Collection string `json:"collection"`
		}
		_ = c.ShouldBindJSON(&body)
		target := sessions.NormalizeCollectionName(body.Collection)
		if target != "" {
			known, err := sessions.ListCollections()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			found := ""
			for _, n := range known {
				if strings.EqualFold(n, target) {
					found = n
					break
				}
			}
			if found == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown collection"})
				return
			}
			target = found // canonical stored casing
		}
		if !d.Registry.SetCollection(id, target) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if d.PushEvents != nil {
			name := target
			if name == "" {
				name = sessions.GeneralCollection
			}
			d.PushEvents.broadcastData("session_moved", id, map[string]any{"collection": name})
		}
		meta, _ := d.Registry.Get(id)
		c.JSON(http.StatusOK, meta)
	}
}
