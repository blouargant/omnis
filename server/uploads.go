package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/paths"
)

// uploadsBaseDir returns the per-user uploads directory
// ($YOKE_HOME/logs/uploads). Resolved at each call so tests can redirect
// via t.Setenv("YOKE_HOME", ...).
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
