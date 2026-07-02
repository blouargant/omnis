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

// runSpawnedTask runs a spawned session's initial task in the background and,
// when it finishes, delivers the result back into the ORIGINATING session so its
// leader can react to it (the "delegate a sub-task and bring the findings back"
// workflow). Delivery is one-way: the parent turn is injected with replyTo=""
// (via injectTurn), so the parent reacts but never bounces a reply back to the
// spawned session. A task_notification also fires on the spawned session so an
// away user is pinged. parentID/childLabel are empty ⇒ no delivery (just run).
func runSpawnedTask(d serverDeps, childID, childLabel, parentID, userID, task string) {
	if strings.TrimSpace(task) == "" || d.PushMgr == nil {
		return
	}
	go func() {
		// Announce the turn is starting so an open (or just-opened) spawned session
		// shows the request + a processing state immediately, instead of looking
		// idle until the background run finishes. Carries the task text as the
		// request to render.
		if d.PushEvents != nil {
			d.PushEvents.broadcastWithText("turn_started", childID, task)
		}
		reply := d.PushMgr.injectTurn(d.rootCtx, d, childID, userOrDefault(userID), task, "task_notification")
		// Deliver the result back to the session that launched the spawn.
		if parentID == "" || parentID == childID || strings.TrimSpace(reply) == "" {
			return
		}
		if _, ok := d.Registry.Get(parentID); !ok {
			return // parent gone (deleted/archived) — nothing to deliver into
		}
		notice := formatSpawnResultNotice(childLabel, task, reply)
		// "mailbox_push" makes an open parent tab append the delivered turn (the
		// notice + the leader's reaction) via appendNewPushTurns.
		d.PushMgr.injectTurn(d.rootCtx, d, parentID, userOrDefault(userID), notice, "mailbox_push")
	}()
}

// formatSpawnResultNotice builds the one-way message injected into the parent
// session carrying a finished spawned session's result. It frames the result as
// coming from a separate session and tells the leader not to reply back to it, so
// the delegation is one-way (no cross-session ping-pong).
func formatSpawnResultNotice(childLabel, task, reply string) string {
	label := strings.TrimSpace(childLabel)
	if label == "" {
		label = "a spawned session"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Spawned session %q finished the task you delegated to it.]\n", label)
	if t := strings.TrimSpace(task); t != "" {
		fmt.Fprintf(&b, "Task: %s\n", t)
	}
	b.WriteString("\nResult:\n")
	b.WriteString(strings.TrimSpace(reply))
	b.WriteString("\n\n(This result was produced by a separate session you spawned; use it as needed. " +
		"You do not need to reply to that session.)")
	return b.String()
}

// spawnLabel is the friendly label used for a spawned session in the deliver-back
// notice: its title when named, else its id.
func spawnLabel(name, id string) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return id
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
		// Deliver the result back to the leader's own session (parentID) when done.
		runSpawnedTask(d, meta.ID, spawnLabel(sd.Name, meta.ID), parentID, parentUserID, sd.Prompt)
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
		// When done, deliver the result back into the session that ran /spawn.
		runSpawnedTask(d, meta.ID, spawnLabel(req.Name, meta.ID), parentID, sessionUserID(parentMeta), req.Prompt)
		// routed=true means no explicit squad was given and the session was pinned
		// to the Omnis router, which will pick the best squad for the task — so the
		// client shouldn't claim it's "on the <router> squad".
		routed := d.Manager != nil && meta.Squad == d.Manager.RouterSquad()
		c.JSON(http.StatusCreated, gin.H{"session_id": meta.ID, "squad": meta.Squad, "routed": routed})
	}
}
