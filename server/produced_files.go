package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/sessions"
)

// producedChangedHook is called after a produced file is recorded for a
// session; main installs one that broadcasts `produced_changed` on
// /api/events so the environment panel's Résultats card refreshes. Nil in
// tests ⇒ the file is only persisted.
var producedChangedHook func(sessionID string)

// producedByWrite reports whether a tool result is a Write that created a new
// file (core/tools sets `created` when the path did not exist before).
func producedByWrite(tool string, resp map[string]any) bool {
	if !strings.EqualFold(tool, "write") || resp == nil {
		return false
	}
	created, _ := resp["created"].(bool)
	return created
}

// recordProducedFile persists a file the agent created and notifies browsers.
func recordProducedFile(sessionID, path string) {
	if sessionID == "" || path == "" {
		return
	}
	if err := sessions.RecordProducedFile(sessionID, path); err != nil {
		log.Printf("server: record produced file %q for %s: %v", path, sessionID, err)
		return
	}
	if producedChangedHook != nil {
		producedChangedHook(sessionID)
	}
}

type producedFileView struct {
	Path   string    `json:"path"`
	Name   string    `json:"name"`
	Turn   int       `json:"turn"`
	At     time.Time `json:"at"`
	Exists bool      `json:"exists"`
}

// handleSessionProduced serves GET /api/sessions/:id/produced — the files
// agents created in the session, newest first, each flagged with whether it
// still exists as a regular file.
func handleSessionProduced(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		files, err := sessions.LoadProducedFiles(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		out := make([]producedFileView, 0, len(files))
		for i := len(files) - 1; i >= 0; i-- {
			f := files[i]
			st, err := os.Stat(f.Path)
			out = append(out, producedFileView{
				Path: f.Path, Name: filepath.Base(f.Path), Turn: f.Turn, At: f.At,
				Exists: err == nil && st.Mode().IsRegular(),
			})
		}
		c.JSON(http.StatusOK, gin.H{"files": out})
	}
}
