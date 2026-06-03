package main

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// Folders-panel filesystem operations (download / delete / new / rename / move).
// All of these mutate (or stream) the host filesystem inside the Folders-panel
// working directory and share the same token-only trust model as the folder
// listing, upload, copy, and Monaco-save routes: they bypass the agent
// permission layer and trust the authenticated user with host file access.
// Each operation exposes a session route (cwd = bashCwd.get(id)) and a
// session-less global route (cwd = bashCwd.getGlobal()).

// sessionCwdOr404 resolves the :id session's working directory, writing a 404
// and returning ok=false when the session is unknown.
func sessionCwdOr404(c *gin.Context, d serverDeps) (string, bool) {
	id := c.Param("id")
	if _, ok := d.Registry.Get(id); !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return "", false
	}
	return bashCwd.get(id), true
}

// ─── Download ─────────────────────────────────────────────────────────────────

func handleFolderDownload(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			doFolderDownload(c, cwd)
		}
	}
}

func handleGlobalFolderDownload(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) { doFolderDownload(c, bashCwd.getGlobal()) }
}

// doFolderDownload streams a single file as an attachment, or a directory as a
// freshly-built zip (the directory's own name becomes the top-level folder in
// the archive). The client fetches it with the auth header and saves the blob.
func doFolderDownload(c *gin.Context, cwd string) {
	p := resolveAgainstCwd(cwd, strings.TrimSpace(c.Query("path")))
	if p == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if !info.IsDir() {
		c.FileAttachment(p, filepath.Base(p))
		return
	}

	name := filepath.Base(p)
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name+".zip"))
	zw := zip.NewWriter(c.Writer)
	defer zw.Close()
	base := filepath.Dir(p) // so paths in the zip are rooted at the dir's own name
	_ = filepath.WalkDir(p, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting the stream
		}
		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if de.IsDir() {
			if rel != "." {
				_, _ = zw.Create(rel + "/")
			}
			return nil
		}
		fi, fiErr := de.Info()
		if fiErr != nil || !fi.Mode().IsRegular() {
			return nil // skip symlinks / sockets / devices
		}
		w, cErr := zw.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Deflate, Modified: fi.ModTime()})
		if cErr != nil {
			return nil
		}
		f, oErr := os.Open(path)
		if oErr != nil {
			return nil
		}
		defer f.Close()
		_, _ = io.Copy(w, f)
		return nil
	})
}

// ─── Delete ───────────────────────────────────────────────────────────────────

func handleFolderDelete(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			doFolderDelete(c, cwd)
		}
	}
}

func handleGlobalFolderDelete(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) { doFolderDelete(c, bashCwd.getGlobal()) }
}

func doFolderDelete(c *gin.Context, cwd string) {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	p := resolveAgainstCwd(cwd, strings.TrimSpace(req.Path))
	if p == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}
	if _, err := os.Lstat(p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not found"})
		return
	}
	clean := filepath.Clean(p)
	if clean == filepath.Clean(cwd) || clean == string(os.PathSeparator) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "refusing to delete the working directory"})
		return
	}
	if err := os.RemoveAll(p); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ─── New file / new folder ──────────────────────────────────────────────────

func handleFolderNew(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			doFolderNew(c, cwd)
		}
	}
}

func handleGlobalFolderNew(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) { doFolderNew(c, bashCwd.getGlobal()) }
}

func doFolderNew(c *gin.Context, cwd string) {
	var req struct {
		Dir  string `json:"dir"`
		Name string `json:"name"`
		Kind string `json:"kind"` // "file" | "dir"
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	dir := resolveAgainstCwd(cwd, strings.TrimSpace(req.Dir))
	if dir == "" {
		dir = filepath.Clean(cwd)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination is not a directory"})
		return
	}
	name, ok := validLeafName(req.Name)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid name"})
		return
	}
	target := filepath.Join(dir, name)
	if _, err := os.Lstat(target); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "already exists"})
		return
	}
	if req.Kind == "dir" {
		if err := os.Mkdir(target, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else {
		f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		f.Close()
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": target})
}

// ─── Rename ───────────────────────────────────────────────────────────────────

func handleFolderRename(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			doFolderRename(c, cwd)
		}
	}
}

func handleGlobalFolderRename(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) { doFolderRename(c, bashCwd.getGlobal()) }
}

func doFolderRename(c *gin.Context, cwd string) {
	var req struct {
		Src  string `json:"src"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	src := resolveAgainstCwd(cwd, strings.TrimSpace(req.Src))
	if src == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src is required"})
		return
	}
	if _, err := os.Lstat(src); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source does not exist"})
		return
	}
	name, ok := validLeafName(req.Name)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid name"})
		return
	}
	target := filepath.Join(filepath.Dir(src), name)
	if filepath.Clean(target) == filepath.Clean(src) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "path": src}) // unchanged
		return
	}
	if _, err := os.Lstat(target); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "already exists"})
		return
	}
	if err := os.Rename(src, target); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": target})
}

// ─── Move ─────────────────────────────────────────────────────────────────────

func handleFolderMove(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			doFolderMove(c, cwd)
		}
	}
}

func handleGlobalFolderMove(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) { doFolderMove(c, bashCwd.getGlobal()) }
}

func doFolderMove(c *gin.Context, cwd string) {
	var req struct {
		Src  string `json:"src"`
		Dest string `json:"dest"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	src := resolveAgainstCwd(cwd, strings.TrimSpace(req.Src))
	dest := resolveAgainstCwd(cwd, strings.TrimSpace(req.Dest))
	if src == "" || dest == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "src and dest are required"})
		return
	}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source does not exist"})
		return
	}
	if destInfo, err := os.Stat(dest); err != nil || !destInfo.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination is not a directory"})
		return
	}
	target := uniquePath(filepath.Join(dest, filepath.Base(src)), srcInfo.IsDir())
	cleanSrc, cleanTarget := filepath.Clean(src), filepath.Clean(target)
	if cleanTarget == cleanSrc || strings.HasPrefix(cleanTarget, cleanSrc+string(os.PathSeparator)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot move a directory into itself"})
		return
	}
	if err := movePath(src, target); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("move: %v", err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": target})
}

// movePath renames src to dst, falling back to copy-then-delete when the two
// live on different filesystems (os.Rename returns a cross-device error).
func movePath(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyPath(src, dst); err != nil {
		return err
	}
	return os.RemoveAll(src)
}

// validLeafName trims and validates a single path component (a new/renamed
// file or folder name): non-empty, no path separators, not "." or "..".
func validLeafName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	return name, true
}
