package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/sessions"
)

func frame(t *testing.T, sink func(string, []byte), event string, payload any) {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	sink(event, b)
}

func TestToolRecorderOffsetsAndResults(t *testing.T) {
	rec := &toolRecorder{}
	var seen []string
	sink := rec.wrap(func(ev string, _ []byte) { seen = append(seen, ev) })

	frame(t, sink, "token", map[string]string{"text": "Hello "})
	frame(t, sink, "tool_call", map[string]any{"name": "Bash", "args": map[string]any{"command": "ls"}, "call_id": "a"})
	frame(t, sink, "tool_result", map[string]any{"name": "Bash", "response": map[string]any{"output": "x"}, "call_id": "a", "duration_ms": 12})
	frame(t, sink, "token", map[string]string{"text": "done"})
	frame(t, sink, "tool_call", map[string]any{"name": "web_agent", "args": map[string]any{"request": "r"}, "call_id": "b"})
	frame(t, sink, "agent_tool_call", map[string]any{"agent": "web_agent", "name": "WebSearch", "args": map[string]any{"q": "z"}, "call_id": "b1"})
	frame(t, sink, "agent_tool_result", map[string]any{"agent": "web_agent", "name": "WebSearch", "response": map[string]any{"output": "y"}, "call_id": "b1"})
	frame(t, sink, "tool_result", map[string]any{"name": "web_agent", "response": map[string]any{"error": "boom"}, "call_id": "b"})

	if len(seen) != 8 {
		t.Fatalf("wrapped sink must forward every frame, got %d", len(seen))
	}
	got := rec.take()
	if len(got) != 2 {
		t.Fatalf("want 2 top-level calls, got %d", len(got))
	}
	a, b := got[0], got[1]
	if a.Name != "Bash" || a.Offset != 6 || a.DurationMs != 12 || !strings.Contains(string(a.Result), "x") {
		t.Fatalf("call a = %+v", a)
	}
	if b.Offset != 10 || b.Error != "boom" || len(b.Children) != 1 || b.Children[0].Name != "WebSearch" {
		t.Fatalf("call b = %+v", b)
	}
	if len(rec.take()) != 0 {
		t.Fatal("take must reset the recorder")
	}
}

func TestToolRecorderOffsetCountsUTF16(t *testing.T) {
	rec := &toolRecorder{}
	sink := rec.wrap(func(string, []byte) {})
	frame(t, sink, "message", map[string]string{"text": "é😀"}) // 1 + 2 UTF-16 units
	frame(t, sink, "tool_call", map[string]any{"name": "Read", "call_id": "a"})
	if got := rec.take(); got[0].Offset != 3 {
		t.Fatalf("offset = %d, want 3", got[0].Offset)
	}
}

func TestToolRecorderSkipsRoutingTools(t *testing.T) {
	rec := &toolRecorder{}
	sink := rec.wrap(func(string, []byte) {})
	frame(t, sink, "tool_call", map[string]any{"name": "route_to_squad", "call_id": "r"})
	if got := rec.take(); len(got) != 0 {
		t.Fatalf("routing tools must not be recorded, got %+v", got)
	}
}

func TestToolRecorderCaps(t *testing.T) {
	rec := &toolRecorder{}
	sink := rec.wrap(func(string, []byte) {})
	big := strings.Repeat("a", 20000)
	frame(t, sink, "tool_call", map[string]any{"name": "Bash", "args": map[string]any{"command": big}, "call_id": "a"})
	frame(t, sink, "tool_result", map[string]any{"response": map[string]any{"output": big}, "call_id": "a"})
	for i := 0; i < maxRecordedCalls+10; i++ {
		frame(t, sink, "tool_call", map[string]any{"name": "Bash", "call_id": "x"})
	}
	got := rec.take()
	if len(got) != maxRecordedCalls {
		t.Fatalf("calls = %d, want cap %d", len(got), maxRecordedCalls)
	}
	if len(got[0].Args) > maxRecordedBytes+64 || len(got[0].Result) > maxRecordedBytes+64 {
		t.Fatalf("args/result not capped: %d / %d", len(got[0].Args), len(got[0].Result))
	}
	if !json.Valid(got[0].Args) || !json.Valid(got[0].Result) {
		t.Fatal("capped values must stay valid JSON")
	}
}

func TestAppendTurnPersistsToolsAndTruncateKeepsThem(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	id := "tools-sess"
	tools := []sessions.ToolCallRecord{{CallID: "a", Name: "Bash", Offset: 3}}
	if err := sessions.AppendConversationTurnRecord(id, "q1", "abcdef", nil, 10, tools); err != nil {
		t.Fatal(err)
	}
	if err := sessions.AppendConversationTurnRecord(id, "q2", "r2", nil, 10, nil); err != nil {
		t.Fatal(err)
	}
	kept, err := sessions.TruncateConversationTurns(id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || len(kept[0].Tools) != 1 || kept[0].Tools[0].Name != "Bash" {
		t.Fatalf("kept = %+v", kept)
	}
}
