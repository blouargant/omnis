package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/blouargant/omnis/internal/selfupdate"
	"github.com/gin-gonic/gin"
)

// updateState caches the most recent self-update check so the UI can read it
// without hitting GitHub on every page load. It is populated by the background
// poller (startUpdatePoller) and by the on-demand /api/update/check route.
type updateState struct {
	mu      sync.RWMutex
	status  selfupdate.UpdateStatus
	checked bool
}

func newUpdateState(currentVersion string) *updateState {
	return &updateState{status: selfupdate.UpdateStatus{Current: currentVersion}}
}

func (u *updateState) get() (selfupdate.UpdateStatus, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.status, u.checked
}

func (u *updateState) set(st selfupdate.UpdateStatus) {
	u.mu.Lock()
	u.status = st
	u.checked = true
	u.mu.Unlock()
}

// updateCheckEnabled reports whether the background poller should run. It is off
// for "dev" builds (no real version to compare) and can be disabled explicitly
// via OMNIS_UPDATE_CHECK=false.
func updateCheckEnabled(currentVersion string) bool {
	if currentVersion == "" || currentVersion == "dev" {
		return false
	}
	if v := os.Getenv("OMNIS_UPDATE_CHECK"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return true
}

// updateInterval is how often the poller re-checks GitHub. Default 6h; override
// with OMNIS_UPDATE_INTERVAL (Go duration). Values below 1m are clamped up.
func updateInterval() time.Duration {
	d := 6 * time.Hour
	if v := os.Getenv("OMNIS_UPDATE_INTERVAL"); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
			d = parsed
		}
	}
	if d < time.Minute {
		d = time.Minute
	}
	return d
}

// startUpdatePoller runs a goroutine that periodically checks for a newer
// stable release, caches the result on state, and fires an "update_available"
// SSE the first time an update is found so open tabs light the button without
// polling. It is a no-op when updateCheckEnabled returns false.
func startUpdatePoller(ctx context.Context, state *updateState, currentVersion string, push *sessionPushBroadcaster) {
	if !updateCheckEnabled(currentVersion) {
		log.Printf("server: self-update check disabled (version=%s)", currentVersion)
		return
	}
	interval := updateInterval()
	log.Printf("server: self-update check enabled (interval=%s)", interval)

	go func() {
		// A short initial delay keeps the check off the boot hot path.
		timer := time.NewTimer(15 * time.Second)
		defer timer.Stop()
		announced := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			st, err := selfupdate.CheckLatest(ctx, selfupdate.Repo, currentVersion)
			if err != nil {
				log.Printf("server: self-update check failed: %v", err)
			} else {
				state.set(st)
				if st.Available && !announced && push != nil {
					announced = true
					push.broadcast("update_available", "")
				}
				if !st.Available {
					announced = false
				}
			}
			timer.Reset(interval)
		}
	}()
}

// registerUpdateRoutes wires the self-update endpoints onto the authenticated
// group.
func registerUpdateRoutes(rg *gin.RouterGroup, state *updateState, currentVersion string) {
	// GET /api/update/status — cached check result (never includes secrets).
	rg.GET("/update/status", func(c *gin.Context) {
		st, checked := state.get()
		if !checked {
			st.Current = currentVersion
			st.Method = selfupdate.DetectMethod(selfExePath()).String()
		}
		c.JSON(http.StatusOK, st)
	})

	// POST /api/update/check — force a re-check now.
	rg.POST("/update/check", func(c *gin.Context) {
		st, err := selfupdate.CheckLatest(c.Request.Context(), selfupdate.Repo, currentVersion)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		state.set(st)
		c.JSON(http.StatusOK, st)
	})

	// POST /api/update/install — attempt the install for the detected method.
	// The password (deb/rpm only) is read from the body, used transiently, and
	// never logged or persisted.
	rg.POST("/update/install", func(c *gin.Context) {
		var body struct {
			Password string `json:"password"`
		}
		_ = c.ShouldBindJSON(&body)

		st, checked := state.get()
		if !checked || !st.Available {
			// Re-check so a stale/empty cache doesn't block a real install.
			fresh, err := selfupdate.CheckLatest(c.Request.Context(), selfupdate.Repo, currentVersion)
			if err == nil {
				state.set(fresh)
				st = fresh
			}
		}
		if !st.Available {
			c.JSON(http.StatusConflict, gin.H{"ok": false, "error": "no update available"})
			return
		}

		err := selfupdate.Install(c.Request.Context(), st, body.Password)
		if err != nil {
			c.JSON(http.StatusOK, gin.H{
				"ok":           false,
				"error":        err.Error(),
				"manual_steps": st.ManualSteps,
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "restart_required": true})
	})
}

// selfExePath resolves the running executable path, mirroring the package
// helper for the status fallback (kept here to avoid exporting an internal).
func selfExePath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}
