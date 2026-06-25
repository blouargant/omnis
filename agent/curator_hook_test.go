package agent

import (
	"testing"
	"time"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/softskills"
)

func TestApplyTagDeltasOverridesHeuristic(t *testing.T) {
	dir := t.TempDir()
	s := &softskills.Stats{Version: 1, Entries: map[string]*softskills.StatsEntry{}}
	s.RecordLoad("foo", "sess", time.Now())
	s.RecordLoad("bar", "sess", time.Now())
	// Heuristic-applied tags as load_recorder would have written them.
	s.RecordTag("foo", "helpful")
	s.RecordTag("bar", "neutral")
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}

	// LLM disagrees on foo (harmful) and adds a new tag for baz.
	payload := map[string]any{
		"heuristic_tags": map[string]string{"foo": "helpful", "bar": "neutral"},
	}
	llm := softskills.Outcome{
		Tags: map[string]string{
			"foo": "harmful", // override heuristic
			"bar": "neutral", // unchanged → no-op
			"baz": "helpful", // new key (was unloaded — should this even happen?)
		},
	}
	if err := applyTagDeltas(dir, payload, llm); err != nil {
		t.Fatal(err)
	}

	got, err := softskills.LoadStats(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e := got.Entries["foo"]; e == nil || e.Helpful != 0 || e.Harmful != 1 {
		t.Errorf("foo entry = %+v, want Helpful=0 Harmful=1 after override", e)
	}
	if e := got.Entries["bar"]; e == nil || e.Neutral != 1 {
		t.Errorf("bar entry = %+v, want Neutral=1 unchanged", e)
	}
	if e := got.Entries["baz"]; e == nil || e.Helpful != 1 {
		t.Errorf("baz entry = %+v, want Helpful=1 (newly tagged by LLM)", e)
	}
}

func TestApplyTagDeltasNoChange(t *testing.T) {
	dir := t.TempDir()
	// LLM matches heuristic exactly → no save (idempotent).
	payload := map[string]any{
		"heuristic_tags": map[string]string{"foo": "helpful"},
	}
	llm := softskills.Outcome{Tags: map[string]string{"foo": "helpful"}}
	if err := applyTagDeltas(dir, payload, llm); err != nil {
		t.Fatal(err)
	}
	// No stats file should be created since changed=false.
	s, _ := softskills.LoadStats(dir)
	if e := s.Entries["foo"]; e != nil {
		t.Errorf("unchanged path should not touch stats, got entry %+v", e)
	}
}

func TestApplyTagDeltasFromInterfaceMap(t *testing.T) {
	// json round-trip yields map[string]any rather than map[string]string;
	// applyTagDeltas must handle both shapes.
	dir := t.TempDir()
	s := &softskills.Stats{Version: 1, Entries: map[string]*softskills.StatsEntry{
		"foo": {LoadedCount: 1, Helpful: 1},
	}}
	if err := s.Save(dir); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"heuristic_tags": map[string]any{"foo": "helpful"},
	}
	llm := softskills.Outcome{Tags: map[string]string{"foo": "harmful"}}
	if err := applyTagDeltas(dir, payload, llm); err != nil {
		t.Fatal(err)
	}
	got, _ := softskills.LoadStats(dir)
	if e := got.Entries["foo"]; e == nil || e.Helpful != 0 || e.Harmful != 1 {
		t.Errorf("foo = %+v, want H=0 Harm=1", e)
	}
}

func TestLoadRecorderEmitsSessionReflected(t *testing.T) {
	// End-to-end: load_recorder → EventSessionReflected with the right shape.
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerLoadRecorderHook(bus, dir, []string{"leader"}, nil)
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	var captured map[string]any
	bus.Subscribe(events.EventSessionReflected, func(_ string, p map[string]any) {
		captured = p
	})

	emitLoad(bus, "leader", "sess-R", "foo")
	bus.Emit(events.EventSessionEnd, map[string]any{
		"user_id":    "u1",
		"session_id": "sess-R",
	})

	if captured == nil {
		t.Fatal("EventSessionReflected was not fired")
	}
	if captured["session_id"] != "sess-R" {
		t.Errorf("session_id = %v, want sess-R", captured["session_id"])
	}
	loaded := stringSliceFromPayload(captured["loaded_skills"])
	if len(loaded) != 1 || loaded[0] != "foo" {
		t.Errorf("loaded_skills = %v, want [foo]", loaded)
	}
}
