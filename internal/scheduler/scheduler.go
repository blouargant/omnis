// Package scheduler runs prompts on a timer. It backs two surface commands:
//
//   - /loop — an in-memory, session-bound recurring prompt (Kind "loop"). It
//     dies when the session closes or the process restarts, matching Claude
//     Code's /loop. Never persisted.
//   - /schedule — a durable cron/interval/one-shot routine (Kind "schedule"),
//     persisted to schedules.json and resumed on boot.
//
// The core is surface-agnostic: it owns the job set, the durable store, and a
// single timer goroutine that decides which jobs are due. Each surface (server,
// CLI, TUI) supplies a `fire` callback that actually runs the prompt — under
// the server via the turn-injection rail (server/mailbox_push.go injectTurn),
// in CLI/TUI by running a turn in the current session.
//
// Mirrors the internal/bg design (a process-wide store drained by one goroutine)
// and lives on agent.Infrastructure so it survives hot-reload like BgQueues and
// SteerStore.
package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Job kinds.
const (
	KindLoop     = "loop"
	KindSchedule = "schedule"
)

// maxHistory bounds the per-job run history kept for the management UI.
const maxHistory = 10

// RunRecord is one past execution of a job: when it ran and the session its
// result landed in (the bound session for a loop, the fresh session for a
// scheduled run). Surfaced by the Automation page so results are findable.
type RunRecord struct {
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id,omitempty"`
	Status    string    `json:"status,omitempty"` // "ok" | "error" | ""
	Note      string    `json:"note,omitempty"`
}

// Job is one scheduled prompt. Exactly one of Interval / Cron / At is set (see
// Spec). A "loop" job is in-memory only; a "schedule" job is persisted.
type Job struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Prompt string `json:"prompt"`
	Spec   string `json:"spec"` // raw user spec, for display

	Interval time.Duration `json:"interval,omitempty"`
	Cron     string        `json:"cron,omitempty"`
	At       time.Time     `json:"at,omitempty"`

	// SessionID — for a loop, the bound session it runs in. For a schedule, an
	// optional target session; empty means "fresh session per run" (server).
	SessionID string `json:"session_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	// Squad names the squad a fresh scheduled session runs under (schedule only).
	Squad string `json:"squad,omitempty"`

	MaxRuns int `json:"max_runs,omitempty"` // 0 = unlimited
	Runs    int `json:"runs"`

	Enabled   bool      `json:"enabled"`
	NextRun   time.Time `json:"next_run"`
	LastRun   time.Time `json:"last_run,omitempty"`
	CreatedAt time.Time `json:"created_at"`

	// History holds the most recent runs (newest last), capped at maxHistory.
	History []RunRecord `json:"history,omitempty"`
}

// recurring reports whether the job repeats (interval or cron) vs. a one-shot.
func (j *Job) recurring() bool { return j.Interval > 0 || j.Cron != "" }

// nextRun computes the next fire time at/after `from`. ok=false means the job is
// finished (a one-shot already fired, or MaxRuns reached) and should be dropped.
func (j *Job) nextRun(from time.Time) (time.Time, bool) {
	if j.MaxRuns > 0 && j.Runs >= j.MaxRuns {
		return time.Time{}, false
	}
	switch {
	case !j.At.IsZero(): // one-shot
		if j.Runs > 0 {
			return time.Time{}, false
		}
		return j.At, true
	case j.Interval > 0:
		return from.Add(j.Interval), true
	case j.Cron != "":
		sched, err := cronParser.Parse(j.Cron)
		if err != nil {
			return time.Time{}, false
		}
		return sched.Next(from), true
	}
	return time.Time{}, false
}

// fileFormat is the on-disk envelope (durable "schedule" jobs only).
type fileFormat struct {
	Jobs []*Job `json:"jobs"`
}

// Scheduler holds the live job set plus a single timer loop. Safe for
// concurrent use.
type Scheduler struct {
	storePath string

	mu       sync.Mutex
	jobs     map[string]*Job
	inFlight map[string]bool // jobs whose fire callback is still running

	fire   func(context.Context, Job)
	runCtx context.Context

	wake chan struct{}
	now  func() time.Time // injectable for tests
}

// New builds a Scheduler, loading any durable jobs from storePath. A missing or
// unparseable file is treated as empty.
func New(storePath string) *Scheduler {
	s := &Scheduler{
		storePath: storePath,
		jobs:      make(map[string]*Job),
		inFlight:  make(map[string]bool),
		wake:      make(chan struct{}, 1),
		now:       time.Now,
	}
	s.load()
	return s
}

func (s *Scheduler) load() {
	data, err := os.ReadFile(s.storePath)
	if err != nil {
		return
	}
	var f fileFormat
	if json.Unmarshal(data, &f) != nil {
		return
	}
	now := s.now()
	for _, j := range f.Jobs {
		if j == nil || j.Kind != KindSchedule || j.ID == "" {
			continue
		}
		// Recompute NextRun: cron skips missed ticks, a past-due interval skips
		// to now+interval, a past-due one-shot fires once on the next tick.
		if !j.recurring() {
			// one-shot: keep At (fires once even if in the past).
		} else if next, ok := j.nextRun(now); ok {
			j.NextRun = next
		}
		s.jobs[j.ID] = j
	}
}

// save persists the durable (schedule) jobs. Caller must hold s.mu.
func (s *Scheduler) save() {
	var durable []*Job
	for _, j := range s.jobs {
		if j.Kind == KindSchedule {
			durable = append(durable, j)
		}
	}
	sort.Slice(durable, func(i, k int) bool { return durable[i].CreatedAt.Before(durable[k].CreatedAt) })
	data, err := json.MarshalIndent(fileFormat{Jobs: durable}, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.storePath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(s.storePath, data, 0o644)
}

// Add registers a job and computes its first NextRun. The Spec must already be
// resolved onto Interval/Cron/At (use ParseSpec). Defaults ID/Enabled/CreatedAt
// when unset. Returns the stored job.
func (s *Scheduler) Add(j Job) (Job, error) {
	if j.Kind != KindLoop && j.Kind != KindSchedule {
		return Job{}, fmt.Errorf("invalid job kind %q", j.Kind)
	}
	if j.Prompt == "" {
		return Job{}, fmt.Errorf("prompt is required")
	}
	if j.Interval == 0 && j.Cron == "" && j.At.IsZero() {
		return Job{}, fmt.Errorf("job has no schedule")
	}
	s.mu.Lock()
	if j.ID == "" {
		j.ID = newID()
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = s.now()
	}
	j.Enabled = true
	if next, ok := j.nextRun(s.now()); ok {
		j.NextRun = next
	}
	cp := j
	s.jobs[j.ID] = &cp
	if cp.Kind == KindSchedule {
		s.save()
	}
	s.mu.Unlock()
	s.signal()
	return cp, nil
}

// Remove deletes a job by id. Returns whether it existed.
func (s *Scheduler) Remove(id string) bool {
	s.mu.Lock()
	j, ok := s.jobs[id]
	delete(s.jobs, id)
	if ok && j.Kind == KindSchedule {
		s.save()
	}
	s.mu.Unlock()
	if ok {
		s.signal()
	}
	return ok
}

// RemoveLoopsForSession drops every in-memory loop bound to sessionID (called
// when a session is deleted or archived). Returns the count removed.
func (s *Scheduler) RemoveLoopsForSession(sessionID string) int {
	s.mu.Lock()
	n := 0
	for id, j := range s.jobs {
		if j.Kind == KindLoop && j.SessionID == sessionID {
			delete(s.jobs, id)
			n++
		}
	}
	s.mu.Unlock()
	if n > 0 {
		s.signal()
	}
	return n
}

// SetEnabled toggles a job. Returns whether it existed.
func (s *Scheduler) SetEnabled(id string, enabled bool) bool {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if ok {
		j.Enabled = enabled
		if enabled {
			if next, k := j.nextRun(s.now()); k {
				j.NextRun = next
			}
		}
		if j.Kind == KindSchedule {
			s.save()
		}
	}
	s.mu.Unlock()
	if ok {
		s.signal()
	}
	return ok
}

// Update changes a job's spec and/or prompt. Empty arguments are left
// unchanged. spec, when set, must already be parsed into newSpec.
func (s *Scheduler) Update(id, prompt string, newSpec *Spec, rawSpec string) (Job, bool) {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok {
		s.mu.Unlock()
		return Job{}, false
	}
	if prompt != "" {
		j.Prompt = prompt
	}
	if newSpec != nil {
		j.Interval, j.Cron, j.At = newSpec.Interval, newSpec.Cron, newSpec.At
		j.Spec = rawSpec
		if next, k := j.nextRun(s.now()); k {
			j.NextRun = next
		}
	}
	if j.Kind == KindSchedule {
		s.save()
	}
	cp := *j
	s.mu.Unlock()
	s.signal()
	return cp, true
}

// List returns a snapshot of all jobs (copies), newest-created last.
func (s *Scheduler) List() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, *j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.Before(out[k].CreatedAt) })
	return out
}

// Get returns a copy of one job.
func (s *Scheduler) Get(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[id]; ok {
		return *j, true
	}
	return Job{}, false
}

// RunNow fires a job immediately (off-schedule), if a fire callback is wired and
// the job isn't already running. Does not consume a scheduled occurrence.
func (s *Scheduler) RunNow(id string) bool {
	s.mu.Lock()
	j, ok := s.jobs[id]
	if !ok || s.fire == nil || s.inFlight[id] {
		s.mu.Unlock()
		return false
	}
	cp := *j
	s.dispatchLocked(cp)
	s.mu.Unlock()
	return true
}

// RecordRun appends a run record to a job (capped at maxHistory, newest last),
// updates LastRun, and persists durable jobs. The surface fire callback calls
// it after a run so the management UI can show recent results. A no-op when the
// job is gone (e.g. a one-shot already dropped).
func (s *Scheduler) RecordRun(jobID string, rec RunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[jobID]
	if !ok {
		return
	}
	j.History = append(j.History, rec)
	if len(j.History) > maxHistory {
		j.History = j.History[len(j.History)-maxHistory:]
	}
	if rec.At.After(j.LastRun) {
		j.LastRun = rec.At
	}
	if j.Kind == KindSchedule {
		s.save()
	}
}

// Run is the timer loop. It blocks until ctx is cancelled. `fire` runs a due
// job's prompt; it is invoked in its own goroutine so a slow turn never stalls
// the scheduler. Call at most once per Scheduler.
func (s *Scheduler) Run(ctx context.Context, fire func(context.Context, Job)) {
	s.mu.Lock()
	s.fire = fire
	s.runCtx = ctx
	s.mu.Unlock()

	for {
		wait := s.tick()
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// tick fires every due+enabled job and returns how long to sleep before the
// next one. A job whose previous fire is still running is skipped this round.
func (s *Scheduler) tick() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var drop []string
	durableChanged := false
	for id, j := range s.jobs {
		if !j.Enabled || j.NextRun.IsZero() || j.NextRun.After(now) {
			continue
		}
		if s.inFlight[id] {
			// Skip this occurrence; advance so we don't busy-loop on it.
			if next, ok := j.nextRun(now); ok {
				j.NextRun = next
			}
			continue
		}
		// Fire it: count the run, advance NextRun (or drop a finished job).
		j.Runs++
		j.LastRun = now
		cp := *j
		next, ok := j.nextRun(now)
		if ok {
			j.NextRun = next
		} else {
			drop = append(drop, id)
		}
		if j.Kind == KindSchedule {
			durableChanged = true // run-count / next-run advanced (or about to drop)
		}
		s.dispatchLocked(cp)
	}

	for _, id := range drop {
		delete(s.jobs, id)
	}
	// Persist only when a durable job actually changed (fired/advanced/dropped),
	// not merely because durable jobs exist — the 1h wait cap would otherwise
	// rewrite the file every hour while idle.
	if durableChanged {
		s.save()
	}

	return s.nextWaitLocked(now)
}

// dispatchLocked launches a job's fire callback in a goroutine and tracks it as
// in-flight. Caller must hold s.mu.
func (s *Scheduler) dispatchLocked(job Job) {
	if s.fire == nil || s.runCtx == nil {
		return
	}
	s.inFlight[job.ID] = true
	ctx := s.runCtx
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.inFlight, job.ID)
			s.mu.Unlock()
			s.signal()
		}()
		s.fire(ctx, job)
	}()
}

// nextWaitLocked returns the delay until the earliest enabled NextRun, clamped
// to [0, 1h]. Caller must hold s.mu.
func (s *Scheduler) nextWaitLocked(now time.Time) time.Duration {
	const maxWait = time.Hour
	wait := maxWait
	for _, j := range s.jobs {
		if !j.Enabled || j.NextRun.IsZero() {
			continue
		}
		d := j.NextRun.Sub(now)
		if d < 0 {
			d = 0
		}
		if d < wait {
			wait = d
		}
	}
	return wait
}

// signal nudges the Run loop to recompute its sleep (non-blocking).
func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
