package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/paths"
)

// uploadsBaseDir returns the per-user uploads directory
// ($OMNIS_HOME/logs/uploads). Resolved at each call so tests can redirect
// via t.Setenv("OMNIS_HOME", ...).
func uploadsBaseDir() string { return paths.UploadsDir() }

func sessionUploadDir(sessionID string) string {
	return filepath.Join(uploadsBaseDir(), sessionID)
}

func handleFileUpload(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}

		dir := sessionUploadDir(id)
		if err := os.MkdirAll(dir, 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload directory"})
			return
		}

		form, err := c.MultipartForm()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart form"})
			return
		}

		type fileResult struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Size int64  `json:"size"`
		}

		var results []fileResult
		for _, fhs := range form.File {
			for _, fh := range fhs {
				dst := filepath.Join(dir, filepath.Base(fh.Filename))
				src, err := fh.Open()
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("open upload: %v", err)})
					return
				}
				out, err := os.Create(dst)
				if err != nil {
					src.Close()
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create file: %v", err)})
					return
				}
				n, copyErr := io.Copy(out, src)
				out.Close()
				src.Close()
				if copyErr != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("write file: %v", copyErr)})
					return
				}
				results = append(results, fileResult{Name: fh.Filename, Path: dst, Size: n})
			}
		}

		if len(results) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no files provided"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"files": results})
	}
}

func deleteSessionUploads(sessionID string) {
	_ = os.RemoveAll(sessionUploadDir(sessionID))
}

// handleFolderUpload writes uploaded files **directly to the host filesystem**
// inside the Folders-panel working directory (the session's bashCwd). Folder
// uploads preserve their relative structure: each file's multipart filename
// carries its path within the dropped folder, and an optional `dest` form field
// targets a sub-directory of the cwd. Unlike handleFileUpload (which stages chat
// attachments under $OMNIS_HOME/logs/uploads), this lands files on the user's
// system. Like the folder listing, the Monaco Save route, and the "!"
// shell-escape it is gated only by the API token and trusts the authenticated
// user with host file access (no agent permission prompt).
func handleFolderUpload(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			writeFolderUploads(c, cwd)
		}
	}
}

// handleGlobalFolderUpload is the session-less variant of handleFolderUpload,
// writing into the global "no session" working directory.
func handleGlobalFolderUpload(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		writeFolderUploads(c, bashCwd.getGlobal())
	}
}

// writeFolderUploads writes every uploaded file under `files` into baseDir
// (optionally a `dest` sub-directory of it), preserving each file's relative
// path (carried in its multipart filename). Paths are sanitised so an upload
// can never escape the target directory.
func writeFolderUploads(c *gin.Context, baseDir string) {
	target := baseDir
	if dest := strings.TrimSpace(c.PostForm("dest")); dest != "" {
		t, err := safeJoinUnder(baseDir, dest)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid destination"})
			return
		}
		if info, err := os.Stat(t); err != nil || !info.IsDir() {
			c.JSON(http.StatusBadRequest, gin.H{"error": "destination is not a directory"})
			return
		}
		target = t
	}

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart form"})
		return
	}
	fhs := form.File["files"]
	if len(fhs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no files provided"})
		return
	}

	written := 0
	for _, fh := range fhs {
		rel := strings.TrimSpace(filepath.FromSlash(fh.Filename))
		if rel == "" {
			continue
		}
		dst, err := safeJoinUnder(target, rel)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid file path %q", fh.Filename)})
			return
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create directory: %v", err)})
			return
		}
		src, err := fh.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("open upload: %v", err)})
			return
		}
		out, err := os.Create(dst)
		if err != nil {
			src.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("create file: %v", err)})
			return
		}
		_, copyErr := io.Copy(out, src)
		out.Close()
		src.Close()
		if copyErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("write file: %v", copyErr)})
			return
		}
		written++
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "count": written, "dir": target})
}

// safeJoinUnder joins a relative path onto base and verifies the cleaned result
// stays within base (no "../" escape, no absolute path), so an upload can only
// land inside the targeted directory tree.
func safeJoinUnder(base, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path not allowed")
	}
	cleanBase := filepath.Clean(base)
	joined := filepath.Clean(filepath.Join(cleanBase, rel))
	if joined != cleanBase && !strings.HasPrefix(joined, cleanBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("path escapes base directory")
	}
	return joined, nil
}

// handleFolderCopy copies a host file or directory (`src`) into a destination
// directory (`dest`) on the host — the filesystem Copy/Paste of the Folders-panel
// context menu. Both paths are resolved against the session's Folders-panel cwd
// (absolute paths honoured as-is, relative ones joined). A name collision in the
// destination is auto-renamed ("… copy", "… copy 2", …). Same token-only trust
// model as the upload + Monaco Save routes — it bypasses the agent permission
// layer and trusts the authenticated user with host file access.
func handleFolderCopy(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if cwd, ok := sessionCwdOr404(c, d); ok {
			doFolderCopy(c, cwd)
		}
	}
}

// handleGlobalFolderCopy is the session-less variant of handleFolderCopy.
func handleGlobalFolderCopy(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		doFolderCopy(c, bashCwd.getGlobal())
	}
}

func doFolderCopy(c *gin.Context, cwd string) {
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
	// Refuse to copy a directory into itself or one of its own descendants.
	cleanSrc, cleanTarget := filepath.Clean(src), filepath.Clean(target)
	if cleanTarget == cleanSrc || strings.HasPrefix(cleanTarget, cleanSrc+string(os.PathSeparator)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot copy a directory into itself"})
		return
	}
	if err := copyPath(src, target); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("copy: %v", err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "path": target})
}

// resolveAgainstCwd resolves a Folders-panel path against the cwd: absolute as
// is, relative joined onto cwd. Empty input stays empty.
func resolveAgainstCwd(cwd, p string) string {
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// uniquePath returns p when it does not exist, otherwise appends " copy" (then
// " copy 2", …) before the extension (for files) or to the leaf (for dirs)
// until it finds a free name.
func uniquePath(p string, isDir bool) string {
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	dir, name := filepath.Dir(p), filepath.Base(p)
	ext := ""
	if !isDir {
		ext = filepath.Ext(name)
		name = strings.TrimSuffix(name, ext)
	}
	for i := 1; ; i++ {
		suffix := " copy"
		if i > 1 {
			suffix = fmt.Sprintf(" copy %d", i)
		}
		cand := filepath.Join(dir, name+suffix+ext)
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
}

// copyPath recursively copies src to dst, preserving file modes and replicating
// symlinks as symlinks.
func copyPath(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	return copyFile(src, dst, info.Mode().Perm())
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
