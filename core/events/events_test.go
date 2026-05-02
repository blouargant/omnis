package events

import (
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