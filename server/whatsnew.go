package main

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/features"
)

// registerWhatsNewRoutes wires the "What's new" feed. The GET is side-effect
// free — it compares the caller's last-seen version (from preferences.json,
// assuming 1.0.0 when none is recorded) against the running build and returns
// the compacted feature feed to show. The client marks the feed seen with the
// POST once it has rendered the modal, so it appears at most once per upgrade.
func registerWhatsNewRoutes(rg *gin.RouterGroup, currentVersion string, store *preferencesStore) {
	rg.GET("/whatsnew", func(c *gin.Context) {
		lastSeen := ""
		if p := store.load(); p.WhatsNewVersion != nil {
			lastSeen = *p.WhatsNewVersion
		}
		c.JSON(http.StatusOK, features.WhatsNew(currentVersion, lastSeen))
	})

	// POST /api/whatsnew/seen — record that the current version's feed has been
	// shown. Merges onto the current prefs so unrelated fields survive.
	rg.POST("/whatsnew/seen", func(c *gin.Context) {
		cur := store.load()
		v := currentVersion
		cur.WhatsNewVersion = &v
		if err := store.save(cur); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "version": currentVersion})
	})
}
