package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBusEmitAndPanicIsolation(t *testing.T) {
	t.Parallel()

	b := NewBus()
	called := 0
	b.On(EventBeforeTool, func(string, map[string]any) {
		panic("boom")
	}).On(EventBeforeTool, func(_ string, payload map[string]any) {
		called++
		if payload["tool"] != "bash" {
			t.Fatalf("payload = %+v", payload)
		}
	})

	b.Emit(EventBeforeTool, map[string]any{"tool": "bash"})
	if called != 1 {
		t.Fatalf("called = %d, want 1", called)
	}
}

// TestPayloadAgentField documents the plugin contract: every payload emitted
// by PluginWithOptions carries an "agent" key so subscribers can route per
// agent (lead vs sub-agent). Here we exercise the plain bus path used by
// front-ends that emit synthesized events themselves (e.g. session start).
func TestPayloadAgentField(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var got map[string]any
	b.On(EventAfterTool, func(_ string, p map[string]any) { got = p })

	b.Emit(EventAfterTool, map[string]any{
		"agent":    "investigator",
		"tool":     "read_file",
		"duration": "12ms",
	})

	if got["agent"] != "investigator" {
		t.Fatalf("agent = %v, want investigator", got["agent"])
	}
}

func TestFileLoggerWithOptionsFullPayload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.log")
	h, closeFn, err := FileLoggerWithOptions(path, FileLoggerOptions{FullPayload: true})
	if err != nil {
		t.Fatalf("FileLoggerWithOptions() error = %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	h(EventAfterTool, map[string]any{
		"tool":     "read",
		"duration": "10ms",
		"input":    map[string]any{"path": "README.md"},
		"output":   map[string]any{"text": "ok"},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record struct {
		Timestamp string         `json:"timestamp"`
		Event     string         `json:"event"`
		Payload   map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
		t.Fatalf("Unmarshal() error = %v; data = %q", err, data)
	}
	if record.Timestamp == "" {
		t.Fatal("timestamp is empty")
	}
	if record.Event != EventAfterTool {
		t.Fatalf("event = %q, want %q", record.Event, EventAfterTool)
	}
	if got := record.Payload["tool"]; got != "read" {
		t.Fatalf("payload.tool = %v, want read", got)
	}
	input, ok := record.Payload["input"].(map[string]any)
	if !ok {
		t.Fatalf("payload.input = %T, want map", record.Payload["input"])
	}
	if got := input["path"]; got != "README.md" {
		t.Fatalf("payload.input.path = %v, want README.md", got)
	}
	output, ok := record.Payload["output"].(map[string]any)
	if !ok {
		t.Fatalf("payload.output = %T, want map", record.Payload["output"])
	}
	if got := output["text"]; got != "ok" {
		t.Fatalf("payload.output.text = %v, want ok", got)
	}
}

func TestFileLoggerWithOptionsFullPayloadFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.log")
	h, closeFn, err := FileLoggerWithOptions(path, FileLoggerOptions{FullPayload: true})
	if err != nil {
		t.Fatalf("FileLoggerWithOptions() error = %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	h(EventAfterTool, map[string]any{"bad": make(chan int)})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(data))), &record); err != nil {
		t.Fatalf("Unmarshal() error = %v; data = %q", err, data)
	}
	if record["event"] != EventAfterTool {
		t.Fatalf("event = %v, want %q", record["event"], EventAfterTool)
	}
	if record["payload_error"] == "" {
		t.Fatalf("payload_error = %v, want non-empty", record["payload_error"])
	}
}

func TestFileLoggerAndCounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.log")
	h, closeFn, err := FileLogger(path)
	if err != nil {
		t.Fatalf("FileLogger() error = %v", err)
	}
	t.Cleanup(func() { _ = closeFn() })

	h(EventAfterTool, map[string]any{"tool": "read", "duration": "10ms"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := string(data); !strings.Contains(got, "after_tool") || !strings.Contains(got, "tool=read") {
		t.Fatalf("log contents = %q", got)
	}

	counter, handler := NewCounter()
	handler(EventAfterTool, map[string]any{"tool": "read"})
	handler(EventSessionStart, nil)
	summary := counter.Summary()
	if !strings.Contains(summary, "tool:read = 1") || !strings.Contains(summary, "session_start = 1") {
		t.Fatalf("Summary() = %q", summary)
	}
}
