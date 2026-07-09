// scheduler.go — server wiring for the /loop and /schedule commands. The
// process-wide scheduler (agent.Infrastructure.Scheduler) owns the jobs + timer;
// this file supplies the `fire` callback (started once from main.go) and the
// /api/schedules CRUD routes.
//
// fire reuses the turn-injection rail (mailbox_push.go injectTurn): a loop fires
// into its bound session; a schedule with no target spins up a fresh,
// auto-archived session per run (visible read-only in the sidebar).
package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/scheduler"
	"github.com/blouargant/omnis/internal/sessions"
)

// scheduleFire returns the scheduler's per-job callback. It is launched once
// from main.go via Scheduler.Run on the server root context.
func scheduleFire(d serverDeps) func(context.Context, scheduler.Job) {
	return func(ctx context.Context, job scheduler.Job) {
		switch job.Kind {
		case scheduler.KindLoop:
			// A loop runs in its bound session; drop it if the session is gone.
			if _, ok := d.Registry.Get(job.SessionID); !ok {
				d.Scheduler.Remove(job.ID)
				return
			}
			d.PushMgr.injectTurn(ctx, d, job.SessionID, userOrDefault(job.UserID), job.Prompt, "schedule_run")
			d.Scheduler.RecordRun(job.ID, scheduler.RunRecord{At: time.Now(), SessionID: job.SessionID, Status: "ok"})

		case scheduler.KindSchedule:
			// A schedule with an existing target session appends to it; otherwise
			// (or if the target was deleted) it runs in a fresh session per run.
			if job.SessionID != "" {
				if _, ok := d.Registry.Get(job.SessionID); ok {
					d.PushMgr.injectTurn(ctx, d, job.SessionID, userOrDefault(job.UserID), job.Prompt, "schedule_run")
					d.Scheduler.RecordRun(job.ID, scheduler.RunRecord{At: time.Now(), SessionID: job.SessionID, Status: "ok"})
					return
				}
			}
			sid := createScheduledSession(d, job.Squad, job.Prompt)
			if sid == "" {
				d.Scheduler.RecordRun(job.ID, scheduler.RunRecord{At: time.Now(), Status: "error", Note: "could not create session"})
				return
			}
			d.PushMgr.injectTurn(ctx, d, sid, sessions.DefaultUserID, job.Prompt, "schedule_run")
			archiveScheduledSession(d, sid)
			d.Scheduler.RecordRun(job.ID, scheduler.RunRecord{At: time.Now(), SessionID: sid, Status: "ok"})
		}
	}
}

func userOrDefault(u string) string {
	if u == "" {
		return sessions.DefaultUserID
	}
	return u
}

// createScheduledSession materialises a fresh session for one scheduled run,
// mirroring the POST /sessions wiring (register + pin + watch + broadcast).
// Returns the new session id, or "" on failure.
func createScheduledSession(d serverDeps, squad, prompt string) string {
	squad = strings.ToLower(strings.TrimSpace(squad))
	if squad == "" {
		squad = toolkitagent.DefaultSquadName
		if d.Manager != nil {
			if rs := d.Manager.RouterSquad(); rs != "" {
				squad = rs
			}
		}
	}
	if d.Manager != nil && !d.Manager.HasSquad(squad) {
		squad = toolkitagent.DefaultSquadName
	}
	meta := d.Registry.New(squad)
	if meta == nil {
		return ""
	}
	title := scheduledTitle(prompt)
	_ = sessions.SetConversationSquad(meta.ID, squad)
	d.Registry.SetTitle(meta.ID, title)
	_ = sessions.SetConversationTitle(meta.ID, title)
	if d.RegisterSession != nil {
		_ = d.RegisterSession(sessions.DefaultUserID, meta.ID, title)
	}
	if d.Manager != nil {
		d.Manager.Pin(meta.ID)
	}
	if d.PushMgr != nil {
		d.PushMgr.Watch(d.rootCtx, d, meta.ID, sessions.DefaultUserID)
	}
	if d.PushEvents != nil {
		d.PushEvents.broadcast("session_created", meta.ID)
	}
	return meta.ID
}

// archiveScheduledSession sets a finished scheduled-run session read-only and
// detaches it from its generation, mirroring the archive route.
func archiveScheduledSession(d serverDeps, id string) {
	if !d.Registry.SetArchived(id, true) {
		return
	}
	if d.PushMgr != nil {
		d.PushMgr.Stop(id)
	}
	if d.Manager != nil {
		d.Manager.Release(id)
	}
	if d.PushEvents != nil {
		d.PushEvents.notify(id)
	}
}

// scheduledTitle derives a short, recognisable sidebar title for a scheduled run.
func scheduledTitle(prompt string) string {
	s := strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " "))
	if len(s) > 48 {
		s = strings.TrimSpace(s[:48]) + "…"
	}
	if s == "" {
		s = "scheduled run"
	}
	return "⏰ " + s
}

// ---------------------------------------------------------------------------
// HTTP routes (registered on the auth group in server.go)
// ---------------------------------------------------------------------------

// handleListSchedules returns every job (loops + routines). GET /api/schedules.
func handleListSchedules(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"jobs": d.Scheduler.List()})
	}
}

// handleCreateSchedule creates a /loop or /schedule job. POST /api/schedules
// body {kind, spec, prompt, session_id?, squad?, max_runs?}.
func handleCreateSchedule(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Kind      string `json:"kind"`
			Spec      string `json:"spec"`
			Prompt    string `json:"prompt"`
			SessionID string `json:"session_id"`
			Squad     string `json:"squad"`
			MaxRuns   int    `json:"max_runs"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
		if req.Kind == "" {
			req.Kind = scheduler.KindSchedule
		}
		if req.Kind != scheduler.KindLoop && req.Kind != scheduler.KindSchedule {
			c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be 'loop' or 'schedule'"})
			return
		}
		if strings.TrimSpace(req.Prompt) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "prompt is required"})
			return
		}
		if req.Kind == scheduler.KindLoop {
			if req.SessionID == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "a loop requires a session"})
				return
			}
			if _, ok := d.Registry.Get(req.SessionID); !ok {
				c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
				return
			}
		}
		spec, err := scheduler.ParseSpec(req.Spec, time.Now())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		job, err := d.Scheduler.Add(scheduler.Job{
			Kind:      req.Kind,
			Prompt:    strings.TrimSpace(req.Prompt),
			Spec:      strings.TrimSpace(req.Spec),
			Interval:  spec.Interval,
			Cron:      spec.Cron,
			At:        spec.At,
			SessionID: req.SessionID,
			UserID:    sessions.DefaultUserID,
			Squad:     strings.TrimSpace(req.Squad),
			MaxRuns:   req.MaxRuns,
		})
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		broadcastScheduleChanged(d)
		c.JSON(http.StatusCreated, job)
	}
}

// handleUpdateSchedule toggles/edits a job. PATCH /api/schedules/:id
// body {enabled?, spec?, prompt?}.
func handleUpdateSchedule(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Scheduler.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "schedule not found"})
			return
		}
		var req struct {
			Enabled *bool  `json:"enabled"`
			Spec    string `json:"spec"`
			Prompt  string `json:"prompt"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		if req.Enabled != nil {
			d.Scheduler.SetEnabled(id, *req.Enabled)
		}
		var newSpec *scheduler.Spec
		raw := strings.TrimSpace(req.Spec)
		if raw != "" {
			s, err := scheduler.ParseSpec(raw, time.Now())
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			newSpec = &s
		}
		if newSpec != nil || strings.TrimSpace(req.Prompt) != "" {
			d.Scheduler.Update(id, strings.TrimSpace(req.Prompt), newSpec, raw)
		}
		broadcastScheduleChanged(d)
		job, _ := d.Scheduler.Get(id)
		c.JSON(http.StatusOK, job)
	}
}

// perRunSessionIDs returns the ids of the fresh, auto-archived sessions a
// schedule job created on its own — one per run — so a delete can cascade to
// them. It deliberately EXCLUDES shared sessions: a loop's bound session and a
// schedule's fixed target session (both have run.SessionID == job.SessionID),
// which must never be deleted by a routine/history cleanup. Empty for loops.
func perRunSessionIDs(d serverDeps, jobID string) []string {
	if d.Scheduler == nil {
		return nil
	}
	job, ok := d.Scheduler.Get(jobID)
	if !ok || job.Kind != scheduler.KindSchedule {
		return nil
	}
	var out []string
	for _, r := range job.History {
		if r.SessionID != "" && r.SessionID != job.SessionID {
			out = append(out, r.SessionID)
		}
	}
	return out
}

// handleDeleteSchedule removes a job. DELETE /api/schedules/:id. When
// ?delete_runs=true it also deletes the fresh per-run archived sessions the
// routine produced (the "also delete associated runs" toggle in the UI).
func handleDeleteSchedule(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		var sids []string
		if truthy(c.Query("delete_runs")) {
			sids = perRunSessionIDs(d, id) // capture before Remove drops the job
		}
		if !d.Scheduler.Remove(id) {
			c.JSON(http.StatusNotFound, gin.H{"error": "schedule not found"})
			return
		}
		for _, sid := range sids {
			deleteSession(d, sid)
		}
		broadcastScheduleChanged(d)
		c.Status(http.StatusNoContent)
	}
}

// truthy reports whether a query-string flag reads as true.
func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// handleRunSchedule fires a job immediately. POST /api/schedules/:id/run.
func handleRunSchedule(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !d.Scheduler.RunNow(c.Param("id")) {
			c.JSON(http.StatusConflict, gin.H{"error": "schedule not found or already running"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// handleClearScheduleHistory drops a job's past-run history (the results list)
// and, since each scheduled run produces a fresh archived session, also deletes
// those per-run sessions (never a shared bound/target session). DELETE
// /api/schedules/:id/history.
func handleClearScheduleHistory(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		sids := perRunSessionIDs(d, id) // capture before ClearHistory wipes it
		if !d.Scheduler.ClearHistory(id) {
			c.JSON(http.StatusNotFound, gin.H{"error": "schedule not found"})
			return
		}
		for _, sid := range sids {
			deleteSession(d, sid)
		}
		broadcastScheduleChanged(d)
		c.Status(http.StatusNoContent)
	}
}

// handleDeleteScheduleRun removes a single past-run entry (by its id) from a
// job's history and deletes the fresh archived session that run produced (never
// a shared bound/target session). DELETE /api/schedules/:id/history/:runID.
func handleDeleteScheduleRun(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, runID := c.Param("id"), c.Param("runID")
		sid := perRunSessionForRun(d, id, runID) // capture before the record is removed
		if !d.Scheduler.DeleteRun(id, runID) {
			c.JSON(http.StatusNotFound, gin.H{"error": "run not found"})
			return
		}
		if sid != "" {
			deleteSession(d, sid)
		}
		broadcastScheduleChanged(d)
		c.Status(http.StatusNoContent)
	}
}

// perRunSessionForRun returns the fresh per-run session id for one run record
// (empty when the run reused a shared session or isn't found) — see
// perRunSessionIDs for the shared-session exclusion rationale.
func perRunSessionForRun(d serverDeps, jobID, runID string) string {
	if d.Scheduler == nil {
		return ""
	}
	job, ok := d.Scheduler.Get(jobID)
	if !ok || job.Kind != scheduler.KindSchedule {
		return ""
	}
	for _, r := range job.History {
		if r.ID == runID {
			if r.SessionID != "" && r.SessionID != job.SessionID {
				return r.SessionID
			}
			return ""
		}
	}
	return ""
}

func broadcastScheduleChanged(d serverDeps) {
	if d.PushEvents != nil {
		d.PushEvents.broadcast("schedule_changed", "")
	}
}
