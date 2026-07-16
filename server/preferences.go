package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/blouargant/omnis/internal/configedit"
)

// preferences holds user-visible UI preferences that should survive server
// restarts. It is intentionally narrow: anything that can be reconstructed
// from a YAML file does not belong here.
type preferences struct {
	// Theme is the selected UI theme id (e.g. "vscode-dark", "github-dark"). A
	// pointer so an absent value (never chosen — first run) is distinguishable
	// from an explicit choice. The web UI applies its default skin (VS Code
	// Light) whenever this is unset OR empty: an empty "" is treated as "no
	// real choice" and resolves to the default, not to a base dark palette.
	Theme *string `json:"theme,omitempty"`
	// Notifications records whether the user opted into desktop notifications
	// (background-task completions and finished chat replies while away). A
	// pointer so an absent value (never chosen — first run) is distinguishable
	// from an explicit false; the web UI shows the first-run opt-in only while
	// this is unset.
	Notifications *bool `json:"notifications,omitempty"`
	// Locale is the selected UI language id ("en"/"fr"/"es"/"de"). A pointer so
	// an absent value (never chosen — first run) is distinguishable from an
	// explicit choice; when unset the web UI falls back to the browser language
	// then to English. Reconciled across devices on boot by the web UI.
	Locale *string `json:"locale,omitempty"`
	// WhatsNewVersion records the version whose "What's new" feed the user last
	// saw, so the modal is shown at most once per upgrade. A pointer so an absent
	// value (never recorded) is distinguishable from an explicit one; when unset
	// the web UI assumes the previous install was 1.0.0 (see internal/features).
	WhatsNewVersion *string `json:"whats_new_version,omitempty"`
}

// preferencesStore persists preferences to a JSON file next to the YAML
// configuration. All reads/writes are serialised by a single mutex; the file
// is tiny (a handful of bytes) so we don't need anything fancier.
type preferencesStore struct {
	path string
	mu   sync.Mutex
}

func newPreferencesStore(_ configFiles) *preferencesStore {
	// User preferences are mutable state; always anchor them under the write root
	// ($OMNIS_HOME), never alongside a lower-precedence config read from ./config
	// or /etc/omnis. configedit.PreferencesPath() is the single source of truth so
	// the in-process settings tools write the very same file.
	return &preferencesStore{path: configedit.PreferencesPath()}
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
		// Merge onto the current prefs (unmarshal only sets fields present in
		// the body) so a theme-only PUT doesn't wipe notifications, and
		// vice-versa.
		cur := store.load()
		if err := c.ShouldBindJSON(&cur); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
			return
		}
		if err := store.save(cur); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, cur)
	})
}
