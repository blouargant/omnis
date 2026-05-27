package agent

import (
	"os"
	"testing"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/softskills"
)

func emitLoad(bus *events.Bus, agent, sessionID, name string) {
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      agent,
		"tool":       "load_softskill",
		"input":      map[string]any{"name": name},
		"session_id": sessionID,
		"user_id":    "u1",
	})
}

func newTestLoadRecorder(t *testing.T, dir string) (*events.Bus, func()) {
	t.Helper()
	bus := events.NewBus()
	subs := registerLoadRecorderHook(bus, dir, []string{"leader"}, nil)
	return bus, func() {
		for _, s := range subs {
			s.Off()
		}
	}
}

func TestLoadRecorderLeaderOnly(t *testing.T) {
	// Phase 2 split: load_recorder only counts leader-loaded skills.
	// Sub-agent loads are handled by subagent_hook.
	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	emitLoad(bus, "leader", "sess-1", "foo")
	emitLoad(bus, "leader", "sess-1", "foo") // dedup within session
	emitLoad(bus, "leader", "sess-1", "bar")
	emitLoad(bus, "investigator", "sess-1", "baz") // ignored

	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-1",
	})

	s, err := softskills.LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Entries["foo"]; e == nil || e.LoadedCount != 1 {
		t.Errorf("foo entry = %+v, want LoadedCount=1 (deduped per session)", e)
	}
	if e := s.Entries["bar"]; e == nil || e.LoadedCount != 1 {
		t.Errorf("bar entry = %+v, want LoadedCount=1", e)
	}
	if s.Entries["investigator/baz"] != nil {
		t.Errorf("expected sub-agent load to be ignored, got %+v", s.Entries["investigator/baz"])
	}
}

func TestLoadRecorderIgnoresUnrelatedTools(t *testing.T) {
	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "leader",
		"tool":       "run_read",
		"input":      map[string]any{"path": "x"},
		"session_id": "sess-2",
	})
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-2",
	})
	s, err := softskills.LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected empty stats, got %+v", s.Entries)
	}
}

func TestLoadRecorderFlushesPerSession(t *testing.T) {
	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	emitLoad(bus, "leader", "sess-A", "foo")
	emitLoad(bus, "leader", "sess-B", "foo")

	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-A",
	})
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-B",
	})

	s, err := softskills.LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := s.Entries["foo"]; e == nil || e.LoadedCount != 2 {
		t.Errorf("foo entry = %+v, want LoadedCount=2 (one per session)", e)
	}
}

func TestLoadRecorderMissingSessionIDDropped(t *testing.T) {
	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	bus.Emit(events.EventAfterTool, map[string]any{
		"agent": "leader",
		"tool":  "load_softskill",
		"input": map[string]any{"name": "foo"},
		// session_id missing
	})
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-X",
	})

	s, err := softskills.LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries) != 0 {
		t.Errorf("expected zero entries when session_id is missing, got %+v", s.Entries)
	}
}

func TestLoadRecorderTagsNeutralWhenNoSignals(t *testing.T) {
	// No statelog, no user messages, no errors → ReflectHeuristic
	// returns Unknown, so loaded skills get a neutral tag.
	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	emitLoad(bus, "leader", "sess-N", "foo")
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-N",
	})

	s, _ := softskills.LoadStats(dir)
	e := s.Entries["foo"]
	if e == nil {
		t.Fatal("missing foo entry")
	}
	if e.LoadedCount != 1 {
		t.Errorf("LoadedCount=%d, want 1", e.LoadedCount)
	}
	// Unknown outcome → no tag bump
	if e.Helpful != 0 || e.Harmful != 0 || e.Neutral != 0 {
		t.Errorf("tags should all be zero on Unknown outcome, got h=%d harm=%d n=%d",
			e.Helpful, e.Harmful, e.Neutral)
	}
}

func TestLoadRecorderPicksUpExplicitFeedback(t *testing.T) {
	// When a feedback sidecar exists for the session's suffix, the
	// heuristic should see ExplicitFeedback. A positive answer drives
	// the verdict directly to Positive (no need for other signals),
	// which tags every loaded skill helpful.
	tdir := t.TempDir()
	t.Setenv("YOKE_HOME", tdir)
	logs := tdir + "/logs"
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := softskills.RecordFeedback(logs, "u1_sess-F", "anything off?", "thanks, that was perfect"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	emitLoad(bus, "leader", "sess-F", "deploy-skill")
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-F",
	})

	s, _ := softskills.LoadStats(dir)
	e := s.Entries["deploy-skill"]
	if e == nil {
		t.Fatal("missing deploy-skill entry")
	}
	if e.Helpful != 1 {
		t.Errorf("Helpful = %d, want 1 (positive explicit feedback should drive helpful tag); entry=%+v", e.Helpful, e)
	}
}

func TestLoadRecorderTagsHarmfulOnRecentToolError(t *testing.T) {
	// A leader load followed by a tool error during the same session
	// → reflector returns Negative, the skill is tagged harmful.
	dir := t.TempDir()
	bus, stop := newTestLoadRecorder(t, dir)
	defer stop()

	emitLoad(bus, "leader", "sess-E", "foo")
	bus.Emit(events.EventToolError, map[string]any{
		"agent":      "leader",
		"tool":       "run_bash",
		"input":      map[string]any{"cmd": "ls"},
		"error":      "exit 1",
		"session_id": "sess-E",
	})
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-E",
	})

	s, _ := softskills.LoadStats(dir)
	e := s.Entries["foo"]
	if e == nil {
		t.Fatal("missing foo entry")
	}
	if e.Harmful != 1 {
		t.Errorf("Harmful = %d, want 1; entry=%+v", e.Harmful, e)
	}
}
