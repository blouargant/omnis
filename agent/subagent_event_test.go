package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/blouargant/omnis/core/events"
)

type captured struct {
	name    string
	payload map[string]any
}

func newCaptureSub(bus *events.Bus, name string, dst *[]captured, mu *sync.Mutex) *events.Subscription {
	return bus.Subscribe(name, func(ev string, p map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		*dst = append(*dst, captured{name: ev, payload: p})
	})
}

func TestSubAgentBoundaryEmitsStartAndEnd(t *testing.T) {
	bus := events.NewBus()
	subs := registerSubAgentBoundary(bus, []string{"investigator", "web_agent"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	var got []captured
	var mu sync.Mutex
	defer newCaptureSub(bus, events.EventSubAgentStart, &got, &mu).Off()
	defer newCaptureSub(bus, events.EventSubAgentEnd, &got, &mu).Off()

	bus.Emit(events.EventBeforeTool, map[string]any{
		"agent":      "leader",
		"tool":       "investigator",
		"input":      map[string]any{"prompt": "look at pod X"},
		"call_id":    "call-1",
		"session_id": "sess-1",
		"user_id":    "u1",
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent":      "leader",
		"tool":       "investigator",
		"input":      map[string]any{"prompt": "look at pod X"},
		"output":     map[string]any{"result": "ok"},
		"duration":   100 * time.Millisecond,
		"call_id":    "call-1",
		"session_id": "sess-1",
		"user_id":    "u1",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].name != events.EventSubAgentStart || got[1].name != events.EventSubAgentEnd {
		t.Errorf("event order = %s, %s; want start then end", got[0].name, got[1].name)
	}
	start := got[0].payload
	if start["agent"] != "investigator" {
		t.Errorf("start agent = %v, want investigator", start["agent"])
	}
	if start["caller_agent"] != "leader" {
		t.Errorf("caller_agent = %v, want leader", start["caller_agent"])
	}
	if start["session_id"] != "sess-1" {
		t.Errorf("session_id = %v, want sess-1", start["session_id"])
	}
	end := got[1].payload
	if end["agent"] != "investigator" {
		t.Errorf("end agent = %v, want investigator", end["agent"])
	}
	if end["output"] == nil {
		t.Error("end output missing")
	}
	if end["duration"] != 100*time.Millisecond {
		t.Errorf("end duration = %v, want 100ms", end["duration"])
	}
}

func TestSubAgentBoundaryIgnoresOtherTools(t *testing.T) {
	bus := events.NewBus()
	subs := registerSubAgentBoundary(bus, []string{"investigator"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	var got []captured
	var mu sync.Mutex
	defer newCaptureSub(bus, events.EventSubAgentStart, &got, &mu).Off()
	defer newCaptureSub(bus, events.EventSubAgentEnd, &got, &mu).Off()

	bus.Emit(events.EventAfterTool, map[string]any{
		"agent": "leader",
		"tool":  "run_read",
		"input": map[string]any{"path": "/x"},
	})
	bus.Emit(events.EventAfterTool, map[string]any{
		"agent": "investigator",
		"tool":  "run_bash",
		"input": map[string]any{"cmd": "ls"},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Errorf("expected no subagent events, got %+v", got)
	}
}

func TestSubAgentBoundaryToolErrorEmitsEnd(t *testing.T) {
	bus := events.NewBus()
	subs := registerSubAgentBoundary(bus, []string{"investigator"})
	defer func() {
		for _, s := range subs {
			s.Off()
		}
	}()

	var got []captured
	var mu sync.Mutex
	defer newCaptureSub(bus, events.EventSubAgentEnd, &got, &mu).Off()

	bus.Emit(events.EventToolError, map[string]any{
		"agent":      "leader",
		"tool":       "investigator",
		"input":      map[string]any{"prompt": "x"},
		"error":      "boom",
		"call_id":    "call-7",
		"session_id": "sess-2",
		"user_id":    "u1",
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("got %d, want 1: %+v", len(got), got)
	}
	if got[0].payload["error"] != "boom" {
		t.Errorf("error payload = %v, want boom", got[0].payload["error"])
	}
}

func TestSubAgentBoundaryHotReloadCleansUp(t *testing.T) {
	bus := events.NewBus()
	subs := registerSubAgentBoundary(bus, []string{"investigator"})

	var got []captured
	var mu sync.Mutex
	defer newCaptureSub(bus, events.EventSubAgentEnd, &got, &mu).Off()

	// Detach (simulating hot-reload tear-down).
	for _, s := range subs {
		s.Off()
	}

	bus.Emit(events.EventAfterTool, map[string]any{
		"agent": "leader",
		"tool":  "investigator",
		"input": map[string]any{"prompt": "x"},
	})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Errorf("expected zero events after Off(), got %+v", got)
	}
}

func TestSubAgentBoundaryEmptyNamesIsNoOp(t *testing.T) {
	bus := events.NewBus()
	subs := registerSubAgentBoundary(bus, nil)
	if len(subs) != 0 {
		t.Errorf("expected no subscriptions for empty names, got %d", len(subs))
	}
	subs2 := registerSubAgentBoundary(bus, []string{"", ""})
	if len(subs2) != 0 {
		t.Errorf("expected no subscriptions for all-blank names, got %d", len(subs2))
	}
}
