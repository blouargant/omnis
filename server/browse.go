package main

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

type browseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

func handleBrowse(root string) gin.HandlerFunc {
	return func(c *gin.Context) {
		reqPath := c.Query("path")
		if reqPath == "" {
			reqPath = root
		}

		clean := filepath.Clean(reqPath)
		rel, err := filepath.Rel(root, clean)
		if err != nil || strings.HasPrefix(rel, "..") {
			c.JSON(http.StatusForbidden, gin.H{"error": "path outside allowed root"})
			return
		}

		entries, err := os.ReadDir(clean)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		var result []browseEntry
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			info, _ := e.Info()
			var size int64
			if info != nil && !e.IsDir() {
				size = info.Size()
			}
			result = append(result, browseEntry{
				Name:  e.Name(),
				Path:  filepath.Join(clean, e.Name()),
				IsDir: e.IsDir(),
				Size:  size,
			})
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].IsDir != result[j].IsDir {
				return result[i].IsDir
			}
			return result[i].Name < result[j].Name
		})

		parent := ""
		if clean != root {
			parent = filepath.Dir(clean)
		}
		c.JSON(http.StatusOK, gin.H{
			"path":    clean,
			"parent":  parent,
			"entries": result,
		})
	}
}
