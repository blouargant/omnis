package scheduler

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestParseSpec(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	tests := []struct {
		in       string
		wantErr  bool
		check    func(Spec) bool
		describe string
	}{
		{"30m", false, func(s Spec) bool { return s.Interval == 30*time.Minute }, "bare duration → interval"},
		{"every 2h", false, func(s Spec) bool { return s.Interval == 2*time.Hour }, "every <dur>"},
		{"in 90m", false, func(s Spec) bool { return s.At.Equal(now.Add(90 * time.Minute)) }, "relative one-shot"},
		{"at 2026-06-29T09:00:00Z", false, func(s Spec) bool { return !s.At.IsZero() }, "RFC3339 one-shot"},
		{"0 9 * * 1-5", false, func(s Spec) bool { return s.Cron == "0 9 * * 1-5" }, "cron"},
		{"*/15 * * * *", false, func(s Spec) bool { return s.Cron == "*/15 * * * *" }, "cron step"},
		{"10s", true, nil, "below interval floor"},
		{"every 5s", true, nil, "every below floor"},
		{"", true, nil, "empty"},
		{"not a schedule", true, nil, "garbage"},
		{"in -5m", true, nil, "negative relative"},
	}
	for _, tc := range tests {
		got, err := ParseSpec(tc.in, now)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s: ParseSpec(%q) expected error, got %+v", tc.describe, tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: ParseSpec(%q) unexpected error: %v", tc.describe, tc.in, err)
			continue
		}
		if !tc.check(got) {
			t.Errorf("%s: ParseSpec(%q) = %+v failed check", tc.describe, tc.in, got)
		}
	}
}

func TestParseAtTimeOnlyRollsForward(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	// 09:00 already passed today → tomorrow.
	s, err := ParseSpec("at 09:00", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.At; !got.Equal(time.Date(2026, 6, 29, 9, 0, 0, 0, time.Local)) {
		t.Errorf("at 09:00 rolled to %v, want next day 09:00", got)
	}
	// 11:00 still ahead today → today.
	s2, _ := ParseSpec("at 11:00", now)
	if got := s2.At; !got.Equal(time.Date(2026, 6, 28, 11, 0, 0, 0, time.Local)) {
		t.Errorf("at 11:00 = %v, want same day 11:00", got)
	}
}

func TestJobNextRun(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)

	interval := Job{Interval: time.Hour}
	if got, ok := interval.nextRun(now); !ok || !got.Equal(now.Add(time.Hour)) {
		t.Errorf("interval next = %v %v", got, ok)
	}

	oneShot := Job{At: now.Add(time.Minute)}
	if got, ok := oneShot.nextRun(now); !ok || !got.Equal(now.Add(time.Minute)) {
		t.Errorf("one-shot next = %v %v", got, ok)
	}
	fired := Job{At: now.Add(time.Minute), Runs: 1}
	if _, ok := fired.nextRun(now); ok {
		t.Error("fired one-shot should not reschedule")
	}

	capped := Job{Interval: time.Hour, MaxRuns: 2, Runs: 2}
	if _, ok := capped.nextRun(now); ok {
		t.Error("exhausted job should not reschedule")
	}

	cronJob := Job{Cron: "0 * * * *"} // top of every hour
	if got, ok := cronJob.nextRun(now); !ok || !got.Equal(time.Date(2026, 6, 28, 11, 0, 0, 0, time.Local)) {
		t.Errorf("cron next = %v %v", got, ok)
	}
}

// newTestScheduler wires fire + runCtx so tick() can dispatch without Run().
func newTestScheduler(t *testing.T, now *time.Time) (*Scheduler, *fireRecorder) {
	t.Helper()
	s := New(filepath.Join(t.TempDir(), "schedules.json"))
	s.now = func() time.Time { return *now }
	rec := &fireRecorder{done: make(chan Job, 16)}
	s.fire = rec.fire
	s.runCtx = context.Background()
	return s, rec
}

type fireRecorder struct {
	mu   sync.Mutex
	jobs []Job
	done chan Job
}

func (r *fireRecorder) fire(_ context.Context, j Job) {
	r.mu.Lock()
	r.jobs = append(r.jobs, j)
	r.mu.Unlock()
	r.done <- j
}

func (r *fireRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

func waitFire(t *testing.T, rec *fireRecorder) Job {
	t.Helper()
	select {
	case j := <-rec.done:
		return j
	case <-time.After(2 * time.Second):
		t.Fatal("expected a fire, got none")
		return Job{}
	}
}

func TestTickFiresDueIntervalJob(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	s, rec := newTestScheduler(t, &now)

	job, err := s.Add(Job{Kind: KindLoop, Prompt: "tick", Interval: time.Minute, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	// Not yet due (NextRun = now+1m).
	if w := s.tick(); rec.count() != 0 {
		t.Fatalf("fired too early (wait=%v)", w)
	}
	// Advance past NextRun.
	now = now.Add(90 * time.Second)
	s.tick()
	waitFire(t, rec)
	if rec.count() != 1 {
		t.Fatalf("want 1 fire, got %d", rec.count())
	}
	got, _ := s.Get(job.ID)
	if got.Runs != 1 || !got.NextRun.Equal(now.Add(time.Minute)) {
		t.Errorf("after fire Runs=%d NextRun=%v", got.Runs, got.NextRun)
	}
}

func TestTickOneShotFiresOnceThenDropped(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	s, rec := newTestScheduler(t, &now)
	job, _ := s.Add(Job{Kind: KindSchedule, Prompt: "once", At: now.Add(time.Minute)})

	now = now.Add(2 * time.Minute)
	s.tick()
	waitFire(t, rec)
	if _, ok := s.Get(job.ID); ok {
		t.Error("one-shot should be dropped after firing")
	}
	// Subsequent ticks fire nothing.
	now = now.Add(time.Hour)
	s.tick()
	if rec.count() != 1 {
		t.Errorf("one-shot fired %d times, want 1", rec.count())
	}
}

func TestTickSkipsInFlight(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	s := New(filepath.Join(t.TempDir(), "schedules.json"))
	s.now = func() time.Time { return now }
	s.runCtx = context.Background()
	release := make(chan struct{})
	var fires int
	var mu sync.Mutex
	s.fire = func(_ context.Context, _ Job) {
		mu.Lock()
		fires++
		mu.Unlock()
		<-release // block so the job stays in-flight
	}
	job, _ := s.Add(Job{Kind: KindLoop, Prompt: "x", Interval: time.Minute, SessionID: "s1"})

	now = now.Add(2 * time.Minute)
	s.tick() // fires; job now in-flight (blocked)
	// Give the goroutine a moment to register itself as fired.
	time.Sleep(50 * time.Millisecond)
	now = now.Add(2 * time.Minute)
	s.tick() // due again, but in-flight → skipped
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	got := fires
	mu.Unlock()
	if got != 1 {
		t.Errorf("in-flight job fired %d times, want 1", got)
	}
	close(release)
	_ = job
}

func TestRemoveLoopsForSession(t *testing.T) {
	now := time.Now()
	s := New(filepath.Join(t.TempDir(), "schedules.json"))
	s.now = func() time.Time { return now }
	_, _ = s.Add(Job{Kind: KindLoop, Prompt: "a", Interval: time.Minute, SessionID: "s1"})
	_, _ = s.Add(Job{Kind: KindLoop, Prompt: "b", Interval: time.Minute, SessionID: "s1"})
	_, _ = s.Add(Job{Kind: KindLoop, Prompt: "c", Interval: time.Minute, SessionID: "s2"})
	if n := s.RemoveLoopsForSession("s1"); n != 2 {
		t.Errorf("removed %d, want 2", n)
	}
	if len(s.List()) != 1 {
		t.Errorf("remaining %d, want 1", len(s.List()))
	}
}

func TestRecordRunCapsHistory(t *testing.T) {
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)
	s := New(filepath.Join(t.TempDir(), "schedules.json"))
	s.now = func() time.Time { return now }
	job, _ := s.Add(Job{Kind: KindSchedule, Prompt: "x", Cron: "0 * * * *", Spec: "0 * * * *"})
	for i := 0; i < maxHistory+5; i++ {
		s.RecordRun(job.ID, RunRecord{At: now.Add(time.Duration(i) * time.Minute), SessionID: "s", Status: "ok"})
	}
	got, _ := s.Get(job.ID)
	if len(got.History) != maxHistory {
		t.Errorf("history len = %d, want capped at %d", len(got.History), maxHistory)
	}
	if got.LastRun.IsZero() {
		t.Error("LastRun not updated by RecordRun")
	}
	// A no-op for an unknown job (e.g. a dropped one-shot).
	s.RecordRun("nope", RunRecord{At: now})
}

func TestDurablePersistAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schedules.json")
	now := time.Date(2026, 6, 28, 10, 0, 0, 0, time.Local)

	s1 := New(path)
	s1.now = func() time.Time { return now }
	_, _ = s1.Add(Job{Kind: KindSchedule, Prompt: "durable", Cron: "0 9 * * *", Spec: "0 9 * * *"})
	_, _ = s1.Add(Job{Kind: KindLoop, Prompt: "ephemeral", Interval: time.Minute, SessionID: "s1"})

	// A fresh Scheduler over the same file sees only the durable job.
	s2 := New(path)
	list := s2.List()
	if len(list) != 1 || list[0].Kind != KindSchedule {
		t.Fatalf("reloaded %d jobs (%+v), want 1 schedule", len(list), list)
	}
}
