package agent

import (
	"testing"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/softskills"
)

func TestSubAgentLoadHookRecordsLoads(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerSubAgentLoadHook(bus, dir, []string{"investigator", "web_agent"}, []string{"leader"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	// Two distinct skills loaded inside one investigator invocation
	// (same session_id), plus one inside a web_agent invocation.
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "k8s-pod-evidence"},
		"session_id": "sub-sess-A",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "k8s-pod-evidence"}, // dup within invocation
		"session_id": "sub-sess-A",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "log-grep"},
		"session_id": "sub-sess-A",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "web_agent",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "hn-fetch"},
		"session_id": "sub-sess-B",
	})

	s, _ := softskills.LoadStats(dir)
	if e := s.Entries["investigator/k8s-pod-evidence"]; e == nil || e.LoadedCount != 1 {
		t.Errorf("investigator/k8s-pod-evidence = %+v, want LoadedCount=1 (deduped per invocation)", e)
	}
	if e := s.Entries["investigator/log-grep"]; e == nil || e.LoadedCount != 1 {
		t.Errorf("investigator/log-grep = %+v, want LoadedCount=1", e)
	}
	if e := s.Entries["web_agent/hn-fetch"]; e == nil || e.LoadedCount != 1 {
		t.Errorf("web_agent/hn-fetch = %+v, want LoadedCount=1", e)
	}
}

func TestSubAgentLoadHookIgnoresLeaderLoads(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerSubAgentLoadHook(bus, dir, []string{"investigator"}, []string{"leader"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "leader",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "x"},
		"session_id": "sess-1",
	})
	s, _ := softskills.LoadStats(dir)
	if len(s.Entries) != 0 {
		t.Errorf("expected no entries, got %+v", s.Entries)
	}
}

func TestSubAgentLoadHookSeparatesInvocations(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerSubAgentLoadHook(bus, dir, []string{"investigator"}, []string{"leader"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	// Same skill, two separate invocations → LoadedCount=2.
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "foo"},
		"session_id": "sub-sess-1",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "foo"},
		"session_id": "sub-sess-2",
	})
	s, _ := softskills.LoadStats(dir)
	if e := s.Entries["investigator/foo"]; e == nil || e.LoadedCount != 2 {
		t.Errorf("investigator/foo = %+v, want LoadedCount=2 (one per invocation)", e)
	}
}

func TestSubAgentLoadHookEmptyNamesIsNoOp(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerSubAgentLoadHook(bus, dir, nil, nil)
	if len(subs) != 0 {
		t.Errorf("expected no subscriptions for empty names")
	}
}

// Phase 6: leader retried investigator inside the same run → the first
// invocation's loaded skill should be tagged harmful.
func TestSubAgentRunEndTagsRetryHarmful(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerSubAgentLoadHook(bus, dir, []string{"investigator"}, []string{"leader"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	// First invocation: empty result, leader will retry.
	bus.Emit(events.EventSubAgentStart, map[string]any{
		"agent":        "investigator",
		"caller_agent": "leader",
		"call_id":      "call-1",
		"run_id":       "run-A",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "k8s"},
		"session_id": "sub-sess-1",
	})
	bus.Emit(events.EventSubAgentEnd, map[string]any{
		"agent":        "investigator",
		"caller_agent": "leader",
		"call_id":      "call-1",
		"run_id":       "run-A",
		"output":       map[string]any{"result": ""},
	})

	// Second invocation: same agent, second call → retry detected.
	bus.Emit(events.EventSubAgentStart, map[string]any{
		"agent":        "investigator",
		"caller_agent": "leader",
		"call_id":      "call-2",
		"run_id":       "run-A",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "investigator",
		"tool":       "load_softskill",
		"input":      map[string]any{"name": "k8s"},
		"session_id": "sub-sess-2",
	})
	bus.Emit(events.EventSubAgentEnd, map[string]any{
		"agent":        "investigator",
		"caller_agent": "leader",
		"call_id":      "call-2",
		"run_id":       "run-A",
		"output":       map[string]any{"result": "Detailed findings inline."},
	})

	bus.Emit(events.EventRunEnd, map[string]any{
		"agent":  "leader",
		"run_id": "run-A",
	})

	s, _ := softskills.LoadStats(dir)
	e := s.Entries["investigator/k8s"]
	if e == nil {
		t.Fatal("missing investigator/k8s entry")
	}
	// LoadedCount=2 (one per invocation), Harmful≥1 (first call retried),
	// Neutral or Helpful from the second call.
	if e.LoadedCount != 2 {
		t.Errorf("LoadedCount = %d, want 2", e.LoadedCount)
	}
	if e.Harmful < 1 {
		t.Errorf("Harmful = %d, want ≥1 (retry should produce harmful tag); entry=%+v", e.Harmful, e)
	}
}

func TestSubAgentRunEndTagsHelpfulOnApproval(t *testing.T) {
	dir := t.TempDir()
	bus := events.NewBus()
	subs := registerSubAgentLoadHook(bus, dir, []string{"investigator"}, []string{"leader"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	bus.Emit(events.EventSubAgentStart, map[string]any{
		"agent": "investigator", "caller_agent": "leader",
		"call_id": "c1", "run_id": "run-B",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent": "investigator", "tool": "load_softskill",
		"input": map[string]any{"name": "k8s"}, "session_id": "sub-sess",
	})
	bus.Emit(events.EventSubAgentEnd, map[string]any{
		"agent": "investigator", "caller_agent": "leader",
		"call_id": "c1", "run_id": "run-B",
		"output": map[string]any{"result": "Two pods are crashlooping in ns/foo."},
	})
	// Leader's assistant text cites the investigator approvingly.
	bus.Emit(events.EventAfterModel, map[string]any{
		"agent":    "leader",
		"run_id":   "run-B",
		"response": "Per investigator, both pods crashloop on missing config. I will patch the deployment now.",
	})
	bus.Emit(events.EventRunEnd, map[string]any{"agent": "leader", "run_id": "run-B"})

	s, _ := softskills.LoadStats(dir)
	e := s.Entries["investigator/k8s"]
	if e == nil {
		t.Fatal("missing investigator/k8s entry")
	}
	if e.Helpful < 1 {
		t.Errorf("Helpful = %d, want ≥1 (leader approved); entry=%+v", e.Helpful, e)
	}
}
