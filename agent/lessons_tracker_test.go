package agent

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

var tk = runKey{"s", "inv", "coder"}

func obs(t *lessonTracker, k runKey, tool string, args map[string]any, errText string, failed bool, started time.Time) (string, *lessonEpisode) {
	return t.observe(k, "/root", tool, args, errText, failed, started)
}

func TestEpisodeIsFailureThenSuccessOfTheSameTool(t *testing.T) {
	var got []lessonEpisode
	tr := newLessonTracker(func(e lessonEpisode) { got = append(got, e) })
	if sig, ep := obs(tr, tk, "Edit", map[string]any{"file_path": "a"}, "Error: not found", true, time.Now()); sig == "" || ep != nil {
		t.Fatalf("failure: sig=%q ep=%v", sig, ep)
	}
	if _, ep := obs(tr, tk, "Read", map[string]any{"file_path": "a"}, "", false, time.Now()); ep != nil {
		t.Fatal("Read must not close an Edit episode")
	}
	time.Sleep(2 * time.Millisecond)
	_, ep := obs(tr, tk, "Edit", map[string]any{"file_path": "a", "ok": 1}, "", false, time.Now())
	if ep == nil || ep.Attempts != 1 || ep.Tool != "Edit" || ep.FailedArgs == ep.WorkedArgs || ep.ErrText == "" {
		t.Fatalf("episode = %+v", ep)
	}
	if len(got) != 1 {
		t.Fatalf("onEpisode calls = %d", len(got))
	}
}

func TestAttemptsCountRepeatedFailures(t *testing.T) {
	tr := newLessonTracker(nil)
	for i := 0; i < 3; i++ {
		obs(tr, tk, "Edit", map[string]any{"file_path": "a"}, "Error: not found", true, time.Now())
	}
	time.Sleep(2 * time.Millisecond)
	_, ep := obs(tr, tk, "Edit", map[string]any{"file_path": "a"}, "", false, time.Now())
	if ep == nil || ep.Attempts != 3 {
		t.Fatalf("episode = %+v", ep)
	}
	if _, ep := obs(tr, tk, "Edit", nil, "", false, time.Now()); ep != nil {
		t.Fatal("episode must close once")
	}
}

func TestNoEpisodeWithoutSuccess(t *testing.T) {
	tr := newLessonTracker(nil)
	if _, ep := obs(tr, tk, "Edit", nil, "Error: x", true, time.Now()); ep != nil {
		t.Fatal("unexpected episode")
	}
}

func TestNoEpisodeAcrossRuns(t *testing.T) {
	tr := newLessonTracker(nil)
	obs(tr, tk, "Edit", nil, "Error: x", true, time.Now())
	time.Sleep(2 * time.Millisecond)
	other := runKey{"s", "inv2", "coder"}
	if _, ep := obs(tr, other, "Edit", nil, "", false, time.Now()); ep != nil {
		t.Fatal("episode across invocations")
	}
}

func TestNoEpisodeAcrossAgents(t *testing.T) {
	tr := newLessonTracker(nil)
	obs(tr, tk, "Edit", nil, "Error: x", true, time.Now())
	time.Sleep(2 * time.Millisecond)
	other := runKey{"s", "inv", "reviewer"}
	if _, ep := obs(tr, other, "Edit", nil, "", false, time.Now()); ep != nil {
		t.Fatal("episode across agents")
	}
}

func TestParallelSiblingSuccessDoesNotCloseTheEpisode(t *testing.T) {
	tr := newLessonTracker(nil)
	base := time.Now()
	tr.now = func() time.Time { return base.Add(time.Second) }
	obs(tr, tk, "Edit", nil, "Error: x", true, base)
	if _, ep := obs(tr, tk, "Edit", nil, "", false, base.Add(500*time.Millisecond)); ep != nil {
		t.Fatal("sibling that started before the failure completed closed the episode")
	}
	if _, ep := obs(tr, tk, "Edit", nil, "", false, base.Add(2*time.Second)); ep == nil {
		t.Fatal("later success must close the episode")
	}
}

func TestRunStateIsBounded(t *testing.T) {
	tr := newLessonTracker(nil)
	for i := 0; i < 5000; i++ {
		obs(tr, runKey{"s" + strconv.Itoa(i%7), strconv.Itoa(i), "a"}, "Edit", nil, "Error: x", true, time.Now())
	}
	if n := len(tr.runs); n > 512 {
		t.Fatalf("run states = %d", n)
	}
	if _, ok := tr.runs[runKey{"s" + strconv.Itoa(4999%7), "4999", "a"}]; !ok {
		t.Fatal("newest run must be kept")
	}
	tr.forgetSession("s0")
	for k := range tr.runs {
		if k.session == "s0" {
			t.Fatal("forgetSession left a state")
		}
	}
}

func TestTrackerIsRaceFree(t *testing.T) {
	tr := newLessonTracker(func(lessonEpisode) {})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			obs(tr, tk, "Edit", map[string]any{"n": i}, "Error: x", i%2 == 0, time.Now())
		}(i)
	}
	wg.Wait()
}
