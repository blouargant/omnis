package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/fileref"
	"github.com/blouargant/yoke/internal/shellcomplete"
)

// handleCompleteFile returns filesystem completion candidates for the `path`
// query parameter — the token typed after a chat "@" reference — resolved
// against the session's working directory. Unlike /complete it never falls back
// to $PATH command completion (an "@" reference is always a path).
func handleCompleteFile(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Query("path")
		cwd := bashCwd.get(c.Query("session"))
		candidates := shellcomplete.CompletePath(path, cwd)
		if candidates == nil {
			candidates = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"candidates": candidates})
	}
}

// handleFileRefResolve classifies a batch of "@" reference tokens as file,
// dir, or missing so the web UI can render valid references as links and leave
// the rest as plain text.
func handleFileRefResolve(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Session string   `json:"session"`
			Paths   []string `json:"paths"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		cwd := bashCwd.get(req.Session)
		kinds := map[string]string{}
		for _, p := range req.Paths {
			kinds[p] = string(fileref.Classify(p, cwd).Kind)
		}
		c.JSON(http.StatusOK, gin.H{"kinds": kinds})
	}
}

// handleFileRefRaw serves the content of a referenced file (or a directory
// listing) so a rendered "@" reference link is openable in the browser. It is
// read-only and, like the "!" shell-escape and the Read tool, trusts the
// authenticated user with host file access.
func handleFileRefRaw(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Query("path")
		cwd := bashCwd.get(c.Query("session"))
		ref := fileref.Classify(path, cwd)
		switch ref.Kind {
		case fileref.KindFile:
			c.Header("Content-Disposition", `inline; filename="`+filepath.Base(ref.Abs)+`"`)
			c.File(ref.Abs)
		case fileref.KindDir:
			entries, err := os.ReadDir(ref.Abs)
			if err != nil {
				c.String(http.StatusInternalServerError, "read dir: %v", err)
				return
			}
			var b strings.Builder
			b.WriteString(ref.Abs + "\n\n")
			for _, e := range entries {
				name := e.Name()
				if e.IsDir() {
					name += "/"
				}
				b.WriteString(name + "\n")
			}
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(b.String()))
		default:
			c.String(http.StatusNotFound, "not found: %s", path)
		}
	}
}

// handleFileWrite saves new content to an existing file (the web-UI Monaco
// editor's Save / Ctrl+S). Like handleFileRefRaw it is gated only by the API
// token and trusts the authenticated user with host file access — the same
// trust model as the read route, the Read/Write tools, and the "!" shell-escape
// (no agent permission prompt). It edits existing files only: the path must
// classify as a regular file, so it can neither overwrite a directory nor
// create files in arbitrary new locations. The existing file mode is preserved.
func handleFileWrite(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Session string `json:"session"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		cwd := bashCwd.get(req.Session)
		ref := fileref.Classify(req.Path, cwd)
		if ref.Kind != fileref.KindFile {
			c.JSON(http.StatusBadRequest, gin.H{"error": "not an existing file"})
			return
		}
		mode := os.FileMode(0o644)
		if info, err := os.Stat(ref.Abs); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(ref.Abs, []byte(req.Content), mode); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "path": ref.Abs})
	}
}
