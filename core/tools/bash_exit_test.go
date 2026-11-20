package tools

import (
	"context"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolconfirmation"

	"github.com/blouargant/omnis/core/adk"
)

// bashCtx is fakeToolCtx plus what functiontool.Run reads before the handler.
type bashCtx struct{ fakeToolCtx }

func (*bashCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }

func newBashCtx() *bashCtx {
	return &bashCtx{fakeToolCtx{StrictContextMock: agent.StrictContextMock{Ctx: context.Background()}, sid: "s1"}}
}

type bashRunner interface {
	Run(ctx agent.Context, args any) (map[string]any, error)
}

func toolNamed(t *testing.T, ts []tool.Tool, name string) bashRunner {
	t.Helper()
	for _, tl := range ts {
		if tl.Name() == name {
			r, ok := tl.(bashRunner)
			if !ok {
				t.Fatalf("%s is not runnable", name)
			}
			return r
		}
	}
	t.Fatalf("no %s tool", name)
	return nil
}

type exitRecord struct {
	code     int
	timedOut bool
	n        int
}

func installRecorder(t *testing.T) *exitRecord {
	t.Helper()
	var mu sync.Mutex
	rec := &exitRecord{}
	SetBashExitObserver(func(_ adk.ToolContext, code int, timedOut bool) {
		mu.Lock()
		defer mu.Unlock()
		rec.code, rec.timedOut = code, timedOut
		rec.n++
	})
	t.Cleanup(func() { SetBashExitObserver(nil) })
	return rec
}

// The Bash tool reports each command's exit status to the observer and
// returns exactly RunBash's text.
func TestBashToolReportsExitStatusWithoutChangingItsOutput(t *testing.T) {
	rec := installRecorder(t)
	bash := toolNamed(t, New(), "Bash")
	tc := newBashCtx()
	for _, c := range []struct {
		cmd  string
		code int
	}{
		{"echo hi", 0},
		{"echo oops; exit 3", 3},
		{"exit 1", 1},
	} {
		want, _ := RunBash(context.Background(), BashIn{Command: c.cmd})
		out, err := bash.Run(tc, map[string]any{"command": c.cmd})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := out["output"].(string); got != want {
			t.Errorf("%q: output %q, want RunBash's %q", c.cmd, got, want)
		}
		if rec.code != c.code || rec.timedOut {
			t.Errorf("%q: observed exit %d timedOut %v, want %d", c.cmd, rec.code, rec.timedOut, c.code)
		}
	}
	if rec.n != 3 {
		t.Fatalf("observer called %d times, want 3", rec.n)
	}
	// A command the safety floor blocks never ran: not reported.
	if _, err := bash.Run(tc, map[string]any{"command": "rm -rf /"}); err != nil {
		t.Fatal(err)
	}
	if rec.n != 3 {
		t.Fatal("a blocked command was reported")
	}
}

func TestBashToolReportsATimeout(t *testing.T) {
	rec := installRecorder(t)
	bash := toolNamed(t, New(), "Bash")
	tc := newBashCtx()
	if _, err := bash.Run(tc, map[string]any{"command": "sleep 5", "timeout": 1}); err != nil {
		t.Fatal(err)
	}
	if rec.n != 1 || !rec.timedOut {
		t.Fatalf("timeout not reported: %+v", rec)
	}
}

func TestReadOnlyBashReportsExitStatus(t *testing.T) {
	rec := installRecorder(t)
	ro, ok := NewReadOnlyBash().(bashRunner)
	if !ok {
		t.Fatal("read-only Bash is not runnable")
	}
	tc := newBashCtx()
	if _, err := ro.Run(tc, map[string]any{"command": "ls /nonexistent-omnis-dir"}); err != nil {
		t.Fatal(err)
	}
	if rec.n != 1 || rec.code == 0 {
		t.Fatalf("read-only Bash exit not reported: %+v", rec)
	}
}

func TestNoObserverIsHarmless(t *testing.T) {
	SetBashExitObserver(nil)
	bash := toolNamed(t, New(), "Bash")
	tc := newBashCtx()
	out, err := bash.Run(tc, map[string]any{"command": "exit 2"})
	if err != nil || out["output"] == nil {
		t.Fatalf("%v %v", out, err)
	}
}

// A command that leaves a background child holding its output is judged by the
// shell's own exit status: Wait's ErrWaitDelay after a clean exit is exit 0, and
// a non-zero exit is still that code. The text the model sees is RunBash's.
func TestBashExitStatusWithABackgroundChildHoldingTheOutput(t *testing.T) {
	saved := bashWaitDelay
	bashWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() { bashWaitDelay = saved })
	rec := installRecorder(t)
	bash := toolNamed(t, New(), "Bash")
	tc := newBashCtx()
	for _, c := range []struct {
		cmd  string
		code int
	}{
		{"sleep 1 & echo started", 0},
		{"sleep 1 & echo started; exit 3", 3},
	} {
		want, _ := RunBash(context.Background(), BashIn{Command: c.cmd})
		out, err := bash.Run(tc, map[string]any{"command": c.cmd})
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := out["output"].(string); got != want {
			t.Errorf("%q: output %q, want RunBash's %q", c.cmd, got, want)
		}
		if rec.code != c.code || rec.timedOut {
			t.Errorf("%q: observed exit %d timedOut %v, want %d", c.cmd, rec.code, rec.timedOut, c.code)
		}
	}
}
