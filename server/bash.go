package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/shellcomplete"
)

// bashCwdStore tracks the working directory of each session — the dir its agent
// tools, interactive "!" shell-escape, and Folders panel operate in — so an
// embedded `cd` persists between commands. The map is the in-memory source of
// truth; per-session entries are ALSO durably recorded via the optional persist
// hook (server-only) so a session, and any fork of it, resumes in the same
// environment after a restart instead of falling back to the process root. The
// fixed `root` and the global "no session" browse cwd (`def`) stay in-memory
// only (transient UI state, not tied to a session).
type bashCwdStore struct {
	mu      sync.Mutex
	m       map[string]string
	root    string               // fixed initial root — where new chat sessions start
	def     string               // global "no session" browse cwd (navigable Folders panel)
	persist func(id, dir string) // optional: durably record a session's cwd on change
}

func newBashCwdStore() *bashCwdStore {
	wd, _ := os.Getwd()
	return &bashCwdStore{m: map[string]string{}, root: wd, def: wd}
}

// setPersist installs the durable-write hook fired by set when a session's cwd
// changes (the server wires it to sessions.SetConversationCwd). Nil disables
// persistence — the default for CLI/TUI/tests, where behaviour is unchanged.
func (s *bashCwdStore) setPersist(fn func(id, dir string)) {
	s.mu.Lock()
	s.persist = fn
	s.mu.Unlock()
}

// seed sets a session's cwd WITHOUT firing the persist hook — used to restore a
// persisted cwd on boot, so reloading the saved value doesn't immediately
// rewrite it.
func (s *bashCwdStore) seed(id, dir string) {
	if id == "" || dir == "" {
		return
	}
	s.mu.Lock()
	s.m[id] = dir
	s.mu.Unlock()
}

// get returns the stored working directory for id, falling back to the fixed
// initial root. A new (or un-navigated) session therefore starts at the root,
// independent of where the global Folders panel has browsed — which is the
// separate `def` cwd (getGlobal/setGlobal). `get("")` (a draft/editor tab with
// no session) likewise resolves to the root.
func (s *bashCwdStore) get(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.m[id]; ok && d != "" {
		return d
	}
	return s.root
}

func (s *bashCwdStore) set(id, dir string) {
	if id == "" || dir == "" {
		return
	}
	s.mu.Lock()
	changed := s.m[id] != dir
	s.m[id] = dir
	persist := s.persist
	s.mu.Unlock()
	// Durably record the new cwd only when it actually changed, so a "!ls" (no
	// cd) or a re-set to the same dir never triggers a redundant disk write.
	// Synchronous: the write is a tiny atomic file op serialised per-session by
	// the conversation lock, and ordering matters (a rapid "cd a; cd b" must end
	// on b).
	if changed && persist != nil {
		persist(id, dir)
	}
}

// getGlobal / setGlobal manage the **navigable** global browse cwd — the
// "default environment" the Folders panel browses when no chat session is
// active. It is deliberately distinct from the fixed `root`: browsing here lets
// the user find and open files in the editor, but never changes where new chats
// start (that stays the initial root; per-folder "Open Chat" is a separate
// future feature).
func (s *bashCwdStore) getGlobal() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.def
}

func (s *bashCwdStore) setGlobal(dir string) {
	if dir == "" {
		return
	}
	s.mu.Lock()
	s.def = dir
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
		switch c.Request.Method {
		case http.MethodPost:
			target, ok := resolveFolderTarget(c, dir, true)
			if !ok {
				return
			}
			if target != "" {
				dir = target
				bashCwd.set(id, dir)
				d.Registry.Touch(id)
			}
		default:
			if sub, ok := resolveFolderTarget(c, dir, false); ok && sub != "" {
				dir = sub // tree expansion: list without mutating the session cwd
			} else if !ok {
				return
			}
		}
		writeFolderListing(c, dir)
	}
}

// handleGlobalFolder browses the **global default** working directory (the web
// app's "no session" environment). Same shape as handleFolder but keyed on the
// process-wide global cwd rather than a session: GET lists it (or a `?sub=`
// child without mutating), POST `{path}` navigates it. It needs no session id,
// so the Folders panel keeps working while a Monaco editor / draft tab is active.
func handleGlobalFolder(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		dir := bashCwd.getGlobal()
		switch c.Request.Method {
		case http.MethodPost:
			target, ok := resolveFolderTarget(c, dir, true)
			if !ok {
				return
			}
			if target != "" {
				dir = target
				bashCwd.setGlobal(dir)
			}
		default:
			if sub, ok := resolveFolderTarget(c, dir, false); ok && sub != "" {
				dir = sub
			} else if !ok {
				return
			}
		}
		writeFolderListing(c, dir)
	}
}

// resolveFolderTarget resolves a navigation/expansion target against dir. When
// post is true it reads `{path}` from the JSON body (the navigate-into target);
// otherwise it reads the `sub` query param (the tree-expansion target). A
// relative target is joined onto dir, absolute is used as-is, then cleaned and
// validated to be an existing directory. Returns ("", true) when no target was
// supplied, (abs, true) on success, and (_, false) after it has already written
// an error response (the caller must return).
func resolveFolderTarget(c *gin.Context, dir string, post bool) (string, bool) {
	var raw string
	if post {
		var req struct {
			Path string `json:"path"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return "", false
		}
		raw = req.Path
	} else {
		raw = c.Query("sub")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(dir, target)
	}
	target = filepath.Clean(target)
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not a directory"})
		return "", false
	}
	return target, true
}

// writeFolderListing reads dir and writes the {dir, entries} JSON (directories
// first, then files, each case-insensitive alphabetical; symlinked dirs
// resolved so they stay navigable).
func writeFolderListing(c *gin.Context, dir string) {
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
		if !isDir && e.Type()&os.ModeSymlink != 0 {
			if info, err := os.Stat(filepath.Join(dir, e.Name())); err == nil && info.IsDir() {
				isDir = true
			}
		}
		out = append(out, folderEntry{Name: e.Name(), Dir: isDir})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	c.JSON(http.StatusOK, gin.H{"dir": dir, "entries": out})
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
