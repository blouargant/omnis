package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"
	"testing"
)

// summaryLineRe matches a well-formed one-line event record. A line that was
// interleaved with another writer's line fails it (that is the whole point).
//
//	[15:04:05.000] before_tool tool=t3-17 dur=1.5s err=boom
var summaryLineRe = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\.\d{3}\] [a-z_]+( tool=\S+)?( dur=\S+)?( err=\S+)?$`)

// readLines returns every line of path (no trailing-empty artefact).
func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan log: %v", err)
	}
	return out
}

// TestFileLoggerConcurrentLinesAreWellFormed fires N goroutines × M events at a
// single handler and asserts every line is intact and every event appears
// exactly once. Regression guard for the garbled audit log: the record used to
// be emitted as five separate Fprintf calls, so two concurrent writers on the
// same path spliced their fragments into each other's lines.
func TestFileLoggerConcurrentLinesAreWellFormed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.log")
	h, closeFn, err := FileLogger(path)
	if err != nil {
		t.Fatalf("FileLogger() error = %v", err)
	}

	const goroutines, perGoroutine = 16, 60
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for m := 0; m < perGoroutine; m++ {
				h(EventBeforeTool, map[string]any{"tool": fmt.Sprintf("t%d-%d", g, m)})
			}
		}(g)
	}
	wg.Wait()
	if err := closeFn(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines := readLines(t, path)
	want := goroutines * perGoroutine
	if len(lines) != want {
		t.Fatalf("line count = %d, want %d (duplicated or lost records)", len(lines), want)
	}
	for i, ln := range lines {
		if !summaryLineRe.MatchString(ln) {
			t.Fatalf("line %d is malformed (interleaved write): %q", i, ln)
		}
	}
	// Exactly-once, keyed on the unique tool token each event carries.
	toolRe := regexp.MustCompile(`tool=(\S+)$`)
	counts := map[string]int{}
	for _, ln := range lines {
		m := toolRe.FindStringSubmatch(ln)
		if m == nil {
			t.Fatalf("line lost its tool token: %q", ln)
		}
		counts[m[1]]++
	}
	for g := 0; g < goroutines; g++ {
		for m := 0; m < perGoroutine; m++ {
			key := fmt.Sprintf("t%d-%d", g, m)
			if counts[key] != 1 {
				t.Fatalf("event %s written %d times, want exactly 1", key, counts[key])
			}
		}
	}
}

// TestFileLoggerFullPayloadConcurrentJSONL is the same guard for debug mode:
// every line must be a standalone, parseable JSON record, exactly once.
func TestFileLoggerFullPayloadConcurrentJSONL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "events.jsonl")
	h, closeFn, err := FileLoggerWithOptions(path, FileLoggerOptions{FullPayload: true})
	if err != nil {
		t.Fatalf("FileLoggerWithOptions() error = %v", err)
	}

	const goroutines, perGoroutine = 16, 60
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for m := 0; m < perGoroutine; m++ {
				h(EventAfterTool, map[string]any{"tool": fmt.Sprintf("t%d-%d", g, m)})
			}
		}(g)
	}
	wg.Wait()
	if err := closeFn(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines := readLines(t, path)
	want := goroutines * perGoroutine
	if len(lines) != want {
		t.Fatalf("line count = %d, want %d", len(lines), want)
	}
	counts := map[string]int{}
	for i, ln := range lines {
		var rec struct {
			Timestamp string         `json:"timestamp"`
			Event     string         `json:"event"`
			Payload   map[string]any `json:"payload"`
		}
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			t.Fatalf("line %d is not valid JSON (interleaved write): %v\n%q", i, err, ln)
		}
		if rec.Event != EventAfterTool {
			t.Fatalf("line %d event = %q", i, rec.Event)
		}
		tool, _ := rec.Payload["tool"].(string)
		if tool == "" {
			t.Fatalf("line %d lost its payload: %q", i, ln)
		}
		counts[tool]++
	}
	for g := 0; g < goroutines; g++ {
		for m := 0; m < perGoroutine; m++ {
			key := fmt.Sprintf("t%d-%d", g, m)
			if counts[key] != 1 {
				t.Fatalf("event %s written %d times, want exactly 1", key, counts[key])
			}
		}
	}
}

// TestFileLoggerTwoWritersSamePathNeverInterleave pins the format-level
// guarantee: even if two independent loggers (each with its own fd and its own
// private mutex — the shape the old per-squad wiring produced) share a path,
// every line stays intact, because each record is one write(2) on an O_APPEND
// fd. Records are duplicated (that is the caller's bug, fixed by owning one
// logger on Infrastructure), but the file remains parseable.
func TestFileLoggerTwoWritersSamePathNeverInterleave(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("single-write O_APPEND atomicity is a POSIX guarantee")
	}

	path := filepath.Join(t.TempDir(), "events.log")
	var handlers []Handler
	for i := 0; i < 4; i++ {
		h, closeFn, err := FileLogger(path)
		if err != nil {
			t.Fatalf("FileLogger() error = %v", err)
		}
		t.Cleanup(func() { _ = closeFn() })
		handlers = append(handlers, h)
	}

	const perHandler = 200
	var wg sync.WaitGroup
	for i, h := range handlers {
		wg.Add(1)
		go func(i int, h Handler) {
			defer wg.Done()
			for m := 0; m < perHandler; m++ {
				h(EventBeforeTool, map[string]any{"tool": fmt.Sprintf("w%d-%d", i, m)})
			}
		}(i, h)
	}
	wg.Wait()

	lines := readLines(t, path)
	if got, want := len(lines), len(handlers)*perHandler; got != want {
		t.Fatalf("line count = %d, want %d", got, want)
	}
	for i, ln := range lines {
		if !summaryLineRe.MatchString(ln) {
			t.Fatalf("line %d is malformed (interleaved write): %q", i, ln)
		}
	}
}
