// spawn.go — server side of session spawning (the /spawn command + the
// spawn_session leader tool). A spawned session starts with a FRESH, empty
// context (unlike fork, which copies the parent's turns) and inherits the
// parent's working directory. When given an initial task it runs it in the
// background and notifies on completion.
//
// The leader tool (agent/spawn.go) only records intent in
// Infrastructure.SpawnDirectives; the server drains that registry after each
// turn (drainSpawns, called from sse.go handleMessages) and materialises the
// real sessions here. The /spawn command hits handleSpawn directly.
//
// materializeSession mirrors the POST /sessions wiring (register + pin + watch +
// broadcast + SessionStart hook), plus cwd inheritance like handleFork.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/hooks"
	"github.com/blouargant/omnis/internal/sessions"
)

// spawnOptions parameterises materializeSession.
type spawnOptions struct {
	Squad        string // explicit target squad (may be empty)
	DefaultSquad string // used when Squad is empty (may be empty ⇒ DefaultSquadName)
	Title        string // friendly session title (empty ⇒ keep the petname id)
	Dir          string // working directory to inherit (empty ⇒ default root)
	UserID       string // owning user (empty ⇒ DefaultUserID)
}

// materializeSession creates a fresh, first-class session (fresh context) wired
// exactly like POST /sessions — register + pin + watch + broadcast +
// SessionStart hook — and inherits the given working directory. Returns nil on
// failure. The squad is resolved/validated here so every caller lands on a real
// squad.
func materializeSession(d serverDeps, o spawnOptions) *sessions.SessionMeta {
	squad := strings.ToLower(strings.TrimSpace(o.Squad))
	if squad == "" {
		squad = strings.ToLower(strings.TrimSpace(o.DefaultSquad))
	}
	if squad == "" {
		squad = toolkitagent.DefaultSquadName
	}
	if d.Manager != nil && !d.Manager.HasSquad(squad) {
		squad = toolkitagent.DefaultSquadName
	}
	meta := d.Registry.New(squad)
	if meta == nil {
		return nil
	}
	userID := userOrDefault(o.UserID)

	// Inherit the parent's working directory so the fresh session's tools / `!cd`
	// / Folders panel start in the same place (like handleFork). The persist hook
	// on bashCwd records it durably.
	if dir := strings.TrimSpace(o.Dir); dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			bashCwd.set(meta.ID, dir)
		}
	}

	title := strings.TrimSpace(o.Title)
	_ = sessions.SetConversationSquad(meta.ID, squad)
	if title != "" {
		d.Registry.SetTitle(meta.ID, title)
		_ = sessions.SetConversationTitle(meta.ID, title)
	}
	if d.RegisterSession != nil {
		name := meta.ID
		if title != "" {
			name = title
		}
		_ = d.RegisterSession(userID, meta.ID, name)
	}
	if d.Manager != nil {
		d.Manager.Pin(meta.ID)
	}
	if d.PushMgr != nil {
		d.PushMgr.Watch(d.rootCtx, d, meta.ID, userID)
	}
	if d.PushEvents != nil {
		d.PushEvents.broadcast("session_created", meta.ID)
	}
	if d.Manager != nil {
		go d.Manager.Infra().FireHook(context.Background(), hooks.SessionStart, "", hooks.Input{
			SessionID: meta.ID,
			Cwd:       bashCwd.get(meta.ID),
			Source:    "web",
		})
	}
	return meta
}

// spawnDefaultSquad picks the squad for a spawned session that named none. It
// defaults to the Omnis router (when routing is enabled), for both idle and
// task-bearing sessions: injectTurn now drives the routing dispatch loop, so an
// initial task starting at the router is routed to the proper squad (and the
// user's first message on an idle session likewise), exactly like a new chat.
// An explicit squad on the spawn directive still wins in materializeSession.
func spawnDefaultSquad(d serverDeps) string {
	if d.Manager != nil {
		if rs := d.Manager.RouterSquad(); rs != "" {
			return rs
		}
	}
	return toolkitagent.DefaultSquadName
}

// runSpawnedTask runs a spawned session's initial task in the background and
// pings the user (task_notification) when its first reply lands.
func runSpawnedTask(d serverDeps, sessionID, userID, prompt string) {
	if strings.TrimSpace(prompt) == "" || d.PushMgr == nil {
		return
	}
	go d.PushMgr.injectTurn(d.rootCtx, d, sessionID, userOrDefault(userID), prompt, "task_notification")
}

// drainSpawns materialises every session the leader requested via spawn_session
// during the just-finished exchange on parentID. Each new session inherits the
// parent's working directory and, when given a task, auto-runs it in the
// background. Uses the server root context so a client disconnect / Stop on the
// parent turn never cancels the spawn.
func drainSpawns(d serverDeps, parentID, parentUserID string) {
	if d.Manager == nil {
		return
	}
	infra := d.Manager.Infra()
	if infra == nil || infra.SpawnDirectives == nil {
		return
	}
	dirs := infra.SpawnDirectives.Drain(parentID)
	if len(dirs) == 0 {
		return
	}
	parentDir := bashCwd.get(parentID)
	for _, sd := range dirs {
		if sd == nil {
			continue
		}
		meta := materializeSession(d, spawnOptions{
			Squad:        sd.Squad,
			DefaultSquad: spawnDefaultSquad(d),
			Title:        sd.Name,
			Dir:          parentDir,
			UserID:       parentUserID,
		})
		if meta == nil {
			continue
		}
		runSpawnedTask(d, meta.ID, parentUserID, sd.Prompt)
	}
}

// handleSpawn backs POST /api/sessions/:id/spawn {name, squad, prompt}: the
// user-facing /spawn command. It creates a fresh session inheriting the parent's
// working directory and, when a task is given, auto-runs it in the background.
func handleSpawn(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		parentID := c.Param("id")
		parentMeta, ok := d.Registry.Get(parentID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		var req struct {
			Name   string `json:"name"`
			Squad  string `json:"squad"`
			Prompt string `json:"prompt"`
		}
		_ = c.ShouldBindJSON(&req)

		squad := strings.ToLower(strings.TrimSpace(req.Squad))
		if squad != "" && d.Manager != nil && !d.Manager.HasSquad(squad) {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("unknown squad %q", squad)})
			return
		}
		meta := materializeSession(d, spawnOptions{
			Squad:        squad,
			DefaultSquad: spawnDefaultSquad(d),
			Title:        strings.TrimSpace(req.Name),
			Dir:          bashCwd.get(parentID),
			UserID:       sessionUserID(parentMeta),
		})
		if meta == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create session"})
			return
		}
		runSpawnedTask(d, meta.ID, sessionUserID(parentMeta), req.Prompt)
		c.JSON(http.StatusCreated, gin.H{"session_id": meta.ID, "squad": meta.Squad})
	}
}
