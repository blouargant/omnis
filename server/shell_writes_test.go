package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/events"
	fstools "github.com/blouargant/omnis/core/tools"
	"github.com/blouargant/omnis/internal/sessions"
)

func TestShellProducedWorthy(t *testing.T) {
	yes := []string{"/tmp/report.docx", "/home/u/notes.md", "/work/out/data.json"}
	no := []string{"/work/build.log", "/work/Makefile", "/work/a.tmp", "/work/.cache/x.md",
		"/work/node_modules/p/i.js", "/work/__pycache__/m.pyc", "/work/dist/app.js", "/work/x.pid"}
	for _, p := range yes {
		if !shellProducedWorthy(p) {
			t.Errorf("shellProducedWorthy(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if shellProducedWorthy(p) {
			t.Errorf("shellProducedWorthy(%q) = true, want false", p)
		}
	}
}

type swFrame struct {
	event string
	data  map[string]any
}

// streamFrames runs streamEvents over the given ADK events and bus events.
func streamFrames(t *testing.T, ownSession string, evs []*session.Event, bus []agentBusEvent) []swFrame {
	t.Helper()
	subCh := make(chan agentBusEvent, len(bus))
	for _, be := range bus {
		subCh <- be
	}
	var frames []swFrame
	sink := func(event string, data []byte) {
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		frames = append(frames, swFrame{event, m})
	}
	seq := func(yield func(*session.Event, error) bool) {
		for _, ev := range evs {
			if !yield(ev, nil) {
				return
			}
		}
	}
	if _, err := streamEvents(context.Background(), sink, seq, subCh, "", "leader", ownSession, false, nil, nil); err != nil {
		t.Fatalf("streamEvents: %v", err)
	}
	return frames
}

func bashEvents(callID string) []*session.Event {
	return []*session.Event{
		{LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "model", Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{ID: callID, Name: "Bash", Args: map[string]any{"command": "x"}}},
		}}}},
		{LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: callID, Name: "Bash", Response: map[string]any{"output": "ok"}}},
		}}}},
	}
}

func fileChangedFrames(frames []swFrame) []map[string]any {
	var out []map[string]any
	for _, f := range frames {
		if f.event == "file_changed" {
			out = append(out, f.data)
		}
	}
	return out
}

// setupShellWrites enables tracking, records two writes for callID and returns
// their paths (both exist on disk).
func setupShellWrites(t *testing.T, callID string) (report, old string) {
	t.Helper()
	t.Setenv("OMNIS_HOME", t.TempDir())
	fstools.EnableShellWriteTracking(true)
	t.Cleanup(func() { fstools.EnableShellWriteTracking(false) })
	dir := t.TempDir()
	report = filepath.Join(dir, "report.md")
	old = filepath.Join(dir, "old.go")
	for _, p := range []string{report, old} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fstools.RecordShellWrites(callID, []fstools.ShellWrite{{Path: report, Created: true}, {Path: old}})
	return report, old
}

func assertShellWriteFrames(t *testing.T, frames []swFrame, report, old, sid, callID string) {
	t.Helper()
	fc := fileChangedFrames(frames)
	if len(fc) != 2 {
		t.Fatalf("file_changed frames = %v, want 2", fc)
	}
	if fc[0]["path"] != report || fc[0]["tool"] != "write" || fc[1]["path"] != old || fc[1]["tool"] != "edit" {
		t.Fatalf("file_changed frames = %v", fc)
	}
	got, err := sessions.LoadProducedFiles(sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != report {
		t.Fatalf("produced = %+v, want only %s", got, report)
	}
	if ws := fstools.TakeShellWrites(callID); ws != nil {
		t.Fatalf("entry not taken: %v", ws)
	}
}

func TestStreamEmitsFileChangedForBashWrites(t *testing.T) {
	report, old := setupShellWrites(t, "sw-c1")
	frames := streamFrames(t, "sw-sess", bashEvents("sw-c1"), nil)
	assertShellWriteFrames(t, frames, report, old, "sw-sess", "sw-c1")
}

func TestStreamBashWithoutWritesEmitsNothing(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	fstools.EnableShellWriteTracking(true)
	t.Cleanup(func() { fstools.EnableShellWriteTracking(false) })
	frames := streamFrames(t, "sw-sess2", bashEvents("sw-c2"), nil)
	if fc := fileChangedFrames(frames); len(fc) != 0 {
		t.Fatalf("file_changed frames = %v, want none", fc)
	}
}

func TestStreamNonBashToolIgnoresShellWrites(t *testing.T) {
	setupShellWrites(t, "sw-c4")
	evs := []*session.Event{
		{LLMResponse: model.LLMResponse{Content: &genai.Content{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{ID: "sw-c4", Name: "Grep", Response: map[string]any{"output": "ok"}}},
		}}}},
	}
	if fc := fileChangedFrames(streamFrames(t, "sw-sess4", evs, nil)); len(fc) != 0 {
		t.Fatalf("file_changed frames = %v, want none", fc)
	}
}

func TestStreamSubAgentBashWrites(t *testing.T) {
	report, old := setupShellWrites(t, "sw-c3")
	bus := []agentBusEvent{
		{Event: events.EventBeforeTool, Payload: map[string]any{
			"agent": "investigator", "tool": "Bash", "input": map[string]any{"command": "x"},
			"call_id": "sw-c3", "root_session_id": "sw-sess3",
		}},
		{Event: events.EventAfterTool, Payload: map[string]any{
			"agent": "investigator", "tool": "Bash", "output": map[string]any{"output": "ok"},
			"call_id": "sw-c3", "root_session_id": "sw-sess3",
		}},
	}
	frames := streamFrames(t, "sw-sess3", nil, bus)
	assertShellWriteFrames(t, frames, report, old, "sw-sess3", "sw-c3")
}

// A new file the Results filter rejects (a log, a temp file) still refreshes
// the editor and the Files panel, but as an "edit": "write" would make the web
// UI offer it as a download card.
func TestStreamNewNoiseFileIsNotOfferedAsDownload(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	fstools.EnableShellWriteTracking(true)
	t.Cleanup(func() { fstools.EnableShellWriteTracking(false) })
	logFile := filepath.Join(t.TempDir(), "t.log")
	if err := os.WriteFile(logFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fstools.RecordShellWrites("sw-log", []fstools.ShellWrite{{Path: logFile, Created: true}})
	fc := fileChangedFrames(streamFrames(t, "sw-log-sess", bashEvents("sw-log"), nil))
	if len(fc) != 1 || fc[0]["path"] != logFile || fc[0]["tool"] != "edit" {
		t.Fatalf("file_changed frames = %v, want one edit of %s", fc, logFile)
	}
	if got, _ := sessions.LoadProducedFiles("sw-log-sess"); len(got) != 0 {
		t.Fatalf("produced = %+v, want none", got)
	}
}

// Injected turns (scheduled routine, spawned task, mailbox) emit no SSE tool
// frames, but a file their Bash call created is still recorded as produced and
// the entry is taken.
func TestInjectedTurnShellWrites(t *testing.T) {
	report, old := setupShellWrites(t, "sw-inj")
	logFile := filepath.Join(filepath.Dir(report), "run.log")
	if err := os.WriteFile(logFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fstools.RecordShellWrites("sw-inj", []fstools.ShellWrite{{Path: report, Created: true}, {Path: old}, {Path: logFile, Created: true}})
	injectedShellWrites("sw-inj-sess", &genai.FunctionResponse{ID: "sw-inj", Name: "Bash"})
	got, err := sessions.LoadProducedFiles("sw-inj-sess")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != report {
		t.Fatalf("produced = %+v, want only %s", got, report)
	}
	if ws := fstools.TakeShellWrites("sw-inj"); ws != nil {
		t.Fatalf("entry not taken: %v", ws)
	}
	// Another tool's response leaves a stored entry alone.
	fstools.RecordShellWrites("sw-inj2", []fstools.ShellWrite{{Path: report, Created: true}})
	injectedShellWrites("sw-inj-sess", &genai.FunctionResponse{ID: "sw-inj2", Name: "Grep"})
	injectedShellWrites("sw-inj-sess", nil)
	if fstools.ShellWrites("sw-inj2") == nil {
		t.Fatal("a non-Bash response took the entry")
	}
}
