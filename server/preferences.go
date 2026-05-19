package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/yoke/internal/paths"
)

// preferences holds user-visible UI preferences that should survive server
// restarts. It is intentionally narrow: anything that can be reconstructed
// from a YAML file does not belong here.
type preferences struct {
	Theme string `json:"theme"`
}

// preferencesStore persists preferences to a JSON file next to the YAML
// configuration. All reads/writes are serialised by a single mutex; the file
// is tiny (a handful of bytes) so we don't need anything fancier.
type preferencesStore struct {
	path string
	mu   sync.Mutex
}

func newPreferencesStore(_ configFiles) *preferencesStore {
	// User preferences are mutable state; always anchor them under the
	// write root ($YOKE_HOME/config), never alongside a lower-precedence
	// agent.yaml read from ./config or /etc/yoke.
	return &preferencesStore{path: filepath.Join(paths.ConfigWriteDir(), "preferences.json")}
}

func (s *preferencesStore) load() preferences {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p preferences
	data, err := os.ReadFile(s.path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(data, &p)
	return p
}

func (s *preferencesStore) save(p preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

func registerPreferencesRoutes(rg *gin.RouterGroup, store *preferencesStore) {
	rg.GET("/preferences", func(c *gin.Context) {
		c.JSON(http.StatusOK, store.load())
	})
	rg.PUT("/preferences", func(c *gin.Context) {
		var req preferences
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if err := store.save(req); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})
}
