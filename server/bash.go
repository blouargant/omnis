package main

import (
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/core/tools"
	"github.com/blouargant/yoke/internal/shellcomplete"
)

// bashCwdStore tracks the working directory of each session's interactive "!"
// shell-escape, so an embedded `cd` persists between commands. State is
// in-memory and per-process — it is intentionally not persisted (the shell
// escape is a live convenience, not part of the conversation history).
type bashCwdStore struct {
	mu sync.Mutex
	m  map[string]string
}

func newBashCwdStore() *bashCwdStore { return &bashCwdStore{m: map[string]string{}} }

// get returns the stored working directory for id, falling back to the
// process working directory (also used when id is empty, e.g. completion
// requested from a draft tab with no session yet).
func (s *bashCwdStore) get(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.m[id]; ok && d != "" {
		return d
	}
	wd, _ := os.Getwd()
	return wd
}

func (s *bashCwdStore) set(id, dir string) {
	if id == "" || dir == "" {
		return
	}
	s.mu.Lock()
	s.m[id] = dir
	s.mu.Unlock()
}

// bashCwd is the process-wide working-directory store shared by handleBash and
// handleComplete.
var bashCwd = newBashCwdStore()

// handleBash runs an interactive "!" shell command for a session and returns
// its output plus the resulting working directory. It bypasses the agent
// permission layer by design (the user typed the command), but RunBashInteractive
// still enforces the hard safety floor. The command is not added to the
// conversation history.
func handleBash(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		meta, ok := d.Registry.Get(id)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		if meta.Archived {
			c.JSON(http.StatusConflict, gin.H{"error": "session is archived"})
			return
		}
		var req struct {
			Command string `json:"command"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		cmd := strings.TrimSpace(req.Command)
		if cmd == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "command is required"})
			return
		}
		out, newCwd, _ := tools.RunBashInteractive(c.Request.Context(), cmd, bashCwd.get(id), 0)
		bashCwd.set(id, newCwd)
		d.Registry.Touch(id)
		c.JSON(http.StatusOK, gin.H{"output": out, "dir": newCwd})
	}
}

// handleComplete returns bash-like completion candidates for the `line` query
// parameter (the text after the leading "!"), resolved against the optional
// `session`'s working directory.
func handleComplete(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		line := c.Query("line")
		cwd := bashCwd.get(c.Query("session"))
		start, candidates := shellcomplete.Complete(line, cwd)
		if candidates == nil {
			candidates = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"start": start, "candidates": candidates})
	}
}
