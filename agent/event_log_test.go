package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blouargant/omnis/core/events"
	"github.com/blouargant/omnis/internal/paths"
)

// newTestInfra returns a minimal Infrastructure with a live bus, rooted at a
// temp $OMNIS_HOME so the event log lands in the test's own directory.
func newTestInfra(t *testing.T) *Infrastructure {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	return &Infrastructure{
		AppName:        "omnis-test",
		BuildTimestamp: "20260713_120000",
		Bus:            events.NewBus(),
	}
}

func readLogLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan event log: %v", err)
	}
	return out
}

// TestInfrastructureEventLogIsBuiltOnce is the duplication regression guard.
// buildPlugins runs once per SQUAD (7 in the shipped config) and again for every
// squad of every hot-reloaded generation; it used to open a fresh FileLogger on
// the same path each time and subscribe it to the same process-wide bus, so a
// single event was written once per live logger. Infrastructure.EventLog must be
// idempotent: N calls ⇒ one file, one subscription, one line per event.
func TestInfrastructureEventLogIsBuiltOnce(t *testing.T) {
	infra := newTestInfra(t)

	// Simulate 7 squads × 2 generations calling in (some concurrently, as a
	// reload racing a build would).
	var wg sync.WaitGroup
	for i := 0; i < 14; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := infra.EventLog(false); err != nil {
				t.Errorf("EventLog() error = %v", err)
			}
		}()
	}
	wg.Wait()

	path := infra.EventLogPath()
	if path == "" {
		t.Fatal("EventLogPath() is empty — the log was never opened")
	}
	if want := filepath.Join(paths.LogsDir(), "agent_events_"+infra.BuildTimestamp+".log"); path != want {
		t.Fatalf("EventLogPath() = %q, want %q", path, want)
	}

	const emitted = 5
	for i := 0; i < emitted; i++ {
		infra.Bus.Emit(events.EventBeforeTool, map[string]any{"tool": fmt.Sprintf("probe-%d", i)})
	}
	if err := infra.closeEventLog(); err != nil {
		t.Fatalf("closeEventLog: %v", err)
	}

	lines := readLogLines(t, path)
	if len(lines) != emitted {
		t.Fatalf("event log has %d lines, want %d — the logger is subscribed more than once (duplication bug)\n%s",
			len(lines), emitted, strings.Join(lines, "\n"))
	}
	for i, ln := range lines {
		if !strings.Contains(ln, "before_tool tool=probe-") {
			t.Fatalf("line %d = %q, want a before_tool record", i, ln)
		}
	}
}

// TestInfrastructureEventLogSingleWriterUnderLoad asserts the one process-wide
// logger stays intact when the whole fleet (leader + sub-agents across squads)
// emits concurrently on the shared bus: every record whole, exactly once.
func TestInfrastructureEventLogSingleWriterUnderLoad(t *testing.T) {
	infra := newTestInfra(t)
	if err := infra.EventLog(false); err != nil {
		t.Fatalf("EventLog() error = %v", err)
	}

	const emitters, perEmitter = 12, 50
	var wg sync.WaitGroup
	for g := 0; g < emitters; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for m := 0; m < perEmitter; m++ {
				infra.Bus.Emit(events.EventAfterTool, map[string]any{
					"tool": fmt.Sprintf("t%d-%d", g, m),
				})
			}
		}(g)
	}
	wg.Wait()
	if err := infra.closeEventLog(); err != nil {
		t.Fatalf("closeEventLog: %v", err)
	}

	lines := readLogLines(t, infra.EventLogPath())
	if want := emitters * perEmitter; len(lines) != want {
		t.Fatalf("event log has %d lines, want %d", len(lines), want)
	}
	counts := map[string]int{}
	for i, ln := range lines {
		idx := strings.Index(ln, " tool=")
		if !strings.HasPrefix(ln, "[") || idx < 0 {
			t.Fatalf("line %d is malformed (interleaved write): %q", i, ln)
		}
		counts[ln[idx+len(" tool="):]]++
	}
	for g := 0; g < emitters; g++ {
		for m := 0; m < perEmitter; m++ {
			key := fmt.Sprintf("t%d-%d", g, m)
			if counts[key] != 1 {
				t.Fatalf("event %s logged %d times, want exactly 1", key, counts[key])
			}
		}
	}
}
