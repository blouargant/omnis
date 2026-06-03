package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

// handleFolder lists the session's current working directory (GET) or changes
// it (POST with {path}). The working directory is the same process-wide bashCwd
// store the interactive "!cd" shell-escape mutates, so navigating in the web-UI
// Folders panel and typing "!cd" stay in sync. A relative path is resolved
// against the current directory and an absolute path is used as-is; ".." walks
// up. Like the "!" shell-escape and the Read tool it is read-only filesystem
// access and trusts the authenticated user with host file access.
func handleFolder(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		dir := bashCwd.get(id)
		if c.Request.Method == http.MethodPost {
			var req struct {
				Path string `json:"path"`
			}
			if err := c.ShouldBindJSON(&req); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
				return
			}
			target := strings.TrimSpace(req.Path)
			if target != "" {
				if !filepath.IsAbs(target) {
					target = filepath.Join(dir, target)
				}
				target = filepath.Clean(target)
				info, err := os.Stat(target)
				if err != nil || !info.IsDir() {
					c.JSON(http.StatusBadRequest, gin.H{"error": "not a directory"})
					return
				}
				dir = target
				bashCwd.set(id, dir)
				d.Registry.Touch(id)
			}
		} else if sub := strings.TrimSpace(c.Query("sub")); sub != "" {
			// Tree expansion: list a sub-directory relative to the cwd WITHOUT
			// mutating the session's working directory.
			target := sub
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			target = filepath.Clean(target)
			info, err := os.Stat(target)
			if err != nil || !info.IsDir() {
				c.JSON(http.StatusBadRequest, gin.H{"error": "not a directory"})
				return
			}
			dir = target
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		type folderEntry struct {
			Name string `json:"name"`
			Dir  bool   `json:"dir"`
		}
		out := make([]folderEntry, 0, len(entries))
		for _, e := range entries {
			isDir := e.IsDir()
			// Resolve symlinks so a link to a directory is navigable.
			if !isDir && e.Type()&os.ModeSymlink != 0 {
				if info, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && info.IsDir() {
					isDir = true
				}
			}
			out = append(out, folderEntry{Name: e.Name(), Dir: isDir})
		}
		// Directories first, then files, each alphabetical (case-insensitive).
		sort.Slice(out, func(i, j int) bool {
			if out[i].Dir != out[j].Dir {
				return out[i].Dir
			}
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
		c.JSON(http.StatusOK, gin.H{"dir": dir, "entries": out})
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
