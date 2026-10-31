package main

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/adk/v2/session"

	"github.com/blouargant/omnis/core/events"
)

// streamBusEvents runs streamEvents for ownSession over an empty ADK stream with
// the given bus events already queued, and returns every frame it emitted.
func streamBusEvents(t *testing.T, ownSession string, bus []agentBusEvent) []struct {
	event string
	data  map[string]any
} {
	t.Helper()
	subCh := make(chan agentBusEvent, len(bus))
	for _, be := range bus {
		subCh <- be
	}
	var frames []struct {
		event string
		data  map[string]any
	}
	sink := func(event string, data []byte) {
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		frames = append(frames, struct {
			event string
			data  map[string]any
		}{event, m})
	}
	empty := func(func(*session.Event, error) bool) {}
	if _, err := streamEvents(context.Background(), sink, empty, subCh, "", "leader", ownSession, false, nil, nil); err != nil {
		t.Fatalf("streamEvents: %v", err)
	}
	return frames
}

// Two concurrent turns share the process-wide bus. Session A's stream must
// never carry session B's questions (or their cancellations), while A's own
// questions still come through.
func TestStreamEventsScopesAskUserToOwnSession(t *testing.T) {
	bus := []agentBusEvent{
		{Event: events.EventAskUser, Payload: map[string]any{"id": "qB", "session_id": "sess-b", "question": "B's secret"}},
		{Event: events.EventAskUserCancel, Payload: map[string]any{"question_id": "qB", "session_id": "sess-b"}},
		{Event: events.EventAskUser, Payload: map[string]any{"id": "qA", "session_id": "sess-a", "question": "A's question"}},
		{Event: events.EventAskUserCancel, Payload: map[string]any{"question_id": "qA", "session_id": "sess-a"}},
	}
	frames := streamBusEvents(t, "sess-a", bus)

	var asks, cancels int
	for _, f := range frames {
		switch f.event {
		case "ask_user", "ask_user_cancel":
			if sid, _ := f.data["session_id"].(string); sid != "sess-a" {
				t.Fatalf("session A's stream emitted %s for session %q: %v", f.event, sid, f.data)
			}
			if f.event == "ask_user" {
				asks++
			} else {
				cancels++
			}
		}
	}
	if asks != 1 || cancels != 1 {
		t.Fatalf("want A's own ask_user + ask_user_cancel once each, got asks=%d cancels=%d", asks, cancels)
	}
}

func TestForeignBusEvent(t *testing.T) {
	cases := []struct {
		name  string
		event string
		p     map[string]any
		own   string
		want  bool
	}{
		{"foreign question", events.EventAskUser, map[string]any{"session_id": "b"}, "a", true},
		{"foreign cancel", events.EventAskUserCancel, map[string]any{"session_id": "b"}, "a", true},
		{"own question", events.EventAskUser, map[string]any{"session_id": "a"}, "a", false},
		{"untagged question", events.EventAskUser, map[string]any{}, "a", false},
		{"no own session (CLI/TUI)", events.EventAskUser, map[string]any{"session_id": "b"}, "", false},
		{"foreign root tag", events.EventBeforeTool, map[string]any{"root_session_id": "b"}, "a", true},
		// session_id on a tool event is the sub-agent's ephemeral session, never a filter key.
		{"tool session_id ignored", events.EventBeforeTool, map[string]any{"session_id": "ephemeral"}, "a", false},
	}
	for _, c := range cases {
		if got := foreignBusEvent(c.event, c.p, c.own); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
