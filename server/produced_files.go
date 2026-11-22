package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/genai"

	toolkitagent "github.com/blouargant/omnis/agent"
	fstools "github.com/blouargant/omnis/core/tools"

	"github.com/blouargant/omnis/internal/sessions"
)

// producedChangedHook is called after a produced file is recorded for a
// session; main installs one that broadcasts `produced_changed` on
// /api/events so the environment panel's Résultats card refreshes. Nil in
// tests ⇒ the file is only persisted.
var producedChangedHook func(sessionID string)

// producedByWrite reports whether a tool result is a successful Write: a file
// the agent wrote in full — new, or rewritten (a report produced again under
// the same name by a later chat overwrites the earlier one, and it is still
// this chat's result). Partial edits (Edit, MultiEdit) are not results.
func producedByWrite(tool string, resp map[string]any) bool {
	if !strings.EqualFold(tool, "write") || resp == nil {
		return false
	}
	if created, _ := resp["created"].(bool); created {
		return true
	}
	res, _ := resp["result"].(string)
	return strings.HasPrefix(res, "wrote ")
}

// shellNoiseExts are the extensions of files a shell command writes that are
// not results (logs, temp, lock and build files).
var shellNoiseExts = map[string]bool{
	".log": true, ".out": true, ".err": true, ".tmp": true, ".pid": true, ".lock": true,
	".bak": true, ".swp": true, ".pyc": true, ".o": true,
}

// shellNoiseDirs are path components whose files are never results.
var shellNoiseDirs = map[string]bool{
	"node_modules": true, "__pycache__": true, "target": true, "dist": true, "build": true,
}

// shellProducedWorthy reports whether a file a shell command created belongs
// in the session's produced files: it has an extension that is not a log,
// temp or build artefact, and no hidden or build-output path component. Files
// under /tmp pass when they qualify (reports are often written there).
func shellProducedWorthy(path string) bool {
	clean := filepath.Clean(path)
	ext := strings.ToLower(filepath.Ext(clean))
	if ext == "" || shellNoiseExts[ext] {
		return false
	}
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") || shellNoiseDirs[part] {
			return false
		}
	}
	return true
}

// applyShellWrites handles the files a Bash call wrote (stored by the Bash
// tool under its call id; see core/tools/shell_writes.go): it takes the entry,
// tells emit about each file (nil when the turn has no stream), records a new
// file that is a result as produced, and tells running language servers. A
// new file that is a result is announced as a "write" — the web UI offers a
// download card for it; anything else (a modified file, a new log or temp
// file) as an "edit", which only refreshes the editor and the Files panel.
// A no-op for any other tool.
func applyShellWrites(sessionID, toolName, callID string, emit func(path, tool string)) {
	if toolName != "Bash" || callID == "" {
		return
	}
	for _, w := range fstools.TakeShellWrites(callID) {
		produced := w.Created && shellProducedWorthy(w.Path)
		tool := "edit"
		if produced {
			tool = "write"
		}
		if emit != nil {
			emit(w.Path, tool)
		}
		if produced {
			recordProducedFile(sessionID, w.Path)
		}
		toolkitagent.FireFileChange(w.Path)
	}
}

// injectedShellWrites is applyShellWrites for an injected turn (scheduled
// routine, spawned task, mailbox, background notification): those turns emit
// no SSE tool frames, so there is no file_changed, but a report the turn
// wrote with the shell still lands in Results. Root agent only: a sub-agent's
// Bash calls of an injected turn are not seen (their entries expire).
func injectedShellWrites(sessionID string, fr *genai.FunctionResponse) {
	if fr == nil {
		return
	}
	applyShellWrites(sessionID, fr.Name, fr.ID, nil)
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
