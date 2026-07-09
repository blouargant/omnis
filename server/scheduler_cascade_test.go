package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/scheduler"
)

// TestPerRunSessionIDs locks in the safety invariant of the schedule delete
// cascade: it exposes ONLY the fresh, per-run sessions a schedule created on its
// own — never a schedule's fixed target session and never a loop's bound session
// (both of which are user-owned and must survive a routine/history cleanup).
func TestPerRunSessionIDs(t *testing.T) {
	sched := scheduler.New(filepath.Join(t.TempDir(), "s.json"))
	d := serverDeps{Scheduler: sched}

	// Schedule with no fixed target → each run makes a fresh, deletable session.
	sj, err := sched.Add(scheduler.Job{Kind: scheduler.KindSchedule, Prompt: "x", Cron: "0 * * * *", Spec: "0 * * * *"})
	if err != nil {
		t.Fatal(err)
	}
	sched.RecordRun(sj.ID, scheduler.RunRecord{At: time.Now(), SessionID: "fresh-1", Status: "ok"})
	sched.RecordRun(sj.ID, scheduler.RunRecord{At: time.Now(), SessionID: "fresh-2", Status: "ok"})
	sched.RecordRun(sj.ID, scheduler.RunRecord{At: time.Now(), Status: "error"}) // no session (create failed)
	got := perRunSessionIDs(d, sj.ID)
	if len(got) != 2 || got[0] != "fresh-1" || got[1] != "fresh-2" {
		t.Fatalf("want [fresh-1 fresh-2], got %v", got)
	}

	// Schedule with a fixed target → runs reuse the target; never deletable.
	tj, err := sched.Add(scheduler.Job{Kind: scheduler.KindSchedule, Prompt: "y", Cron: "0 * * * *", Spec: "0 * * * *", SessionID: "target"})
	if err != nil {
		t.Fatal(err)
	}
	sched.RecordRun(tj.ID, scheduler.RunRecord{At: time.Now(), SessionID: "target", Status: "ok"})
	if got := perRunSessionIDs(d, tj.ID); len(got) != 0 {
		t.Errorf("fixed-target schedule must expose no per-run sessions, got %v", got)
	}

	// Loop → bound session; never deletable.
	lj, err := sched.Add(scheduler.Job{Kind: scheduler.KindLoop, Prompt: "z", Interval: time.Minute, Spec: "1m", SessionID: "bound"})
	if err != nil {
		t.Fatal(err)
	}
	sched.RecordRun(lj.ID, scheduler.RunRecord{At: time.Now(), SessionID: "bound", Status: "ok"})
	if got := perRunSessionIDs(d, lj.ID); len(got) != 0 {
		t.Errorf("loop must expose no per-run sessions, got %v", got)
	}

	// perRunSessionForRun: the fresh run resolves to its session; the fixed-target
	// run resolves to "" (not deletable).
	var freshRunID, targetRunID string
	sjNow, _ := sched.Get(sj.ID)
	for _, r := range sjNow.History {
		if r.SessionID == "fresh-1" {
			freshRunID = r.ID
		}
	}
	tjNow, _ := sched.Get(tj.ID)
	for _, r := range tjNow.History {
		targetRunID = r.ID
	}
	if sid := perRunSessionForRun(d, sj.ID, freshRunID); sid != "fresh-1" {
		t.Errorf("perRunSessionForRun(fresh) = %q, want fresh-1", sid)
	}
	if sid := perRunSessionForRun(d, tj.ID, targetRunID); sid != "" {
		t.Errorf("perRunSessionForRun(fixed target) = %q, want \"\"", sid)
	}
	if sid := perRunSessionForRun(d, sj.ID, "nope"); sid != "" {
		t.Errorf("perRunSessionForRun(unknown run) = %q, want \"\"", sid)
	}
}
