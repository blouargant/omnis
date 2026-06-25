// Package bg implements the article's "Background task execution with
// notifications" (Phase 3 / s08). A bash command is launched in a goroutine;
// when it finishes, its result is pushed into a notification channel that
// the agent loop drains between turns (CLI/TUI) or a host watcher injects as a
// synthetic turn (server mode — see server/mailbox_push.go).
//
// Two notification sources share one per-session Queue: one-shot commands
// (bash_background, Start) and streaming condition watchers (monitor,
// StartMonitor in monitor.go). Every launch registers a Task in the queue's
// registry so the lifecycle tools (bg_list/bg_cancel/bg_output) can see
// and control it.
//
// Like tasks/todo/compress, the queue can be made session-scoped so
// concurrent sessions do not see each other's notifications.
package bg

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/tools"
)

// Task kinds.
const (
	KindCommand = "command"
	KindMonitor = "monitor"
)

// Notification carries a completed (or streamed) background result.
type Notification struct {
	TaskID  string
	Label   string
	Kind    string // KindCommand | KindMonitor
	Status  string // completed | failed | timed_out | blocked | cancelled | event
	Output  string
	Started time.Time
	Ended   time.Time
}

// Queue is a thread-safe channel of notifications plus a registry of the
// tasks that feed it.
type Queue struct {
	ch      chan Notification
	counter atomic.Int64

	mu    sync.Mutex
	tasks map[string]*Task
}

// NewQueue returns a buffered notification queue.
func NewQueue(buf int) *Queue {
	if buf <= 0 {
		buf = 64
	}
	return &Queue{ch: make(chan Notification, buf), tasks: make(map[string]*Task)}
}

// Drain removes and returns all pending notifications without blocking.
func (q *Queue) Drain() []Notification {
	var out []Notification
	for {
		select {
		case n := <-q.ch:
			out = append(out, n)
		default:
			return out
		}
	}
}

// Wait blocks until at least one notification is available or ctx ends.
func (q *Queue) Wait(ctx context.Context) (Notification, bool) {
	select {
	case n := <-q.ch:
		return n, true
	case <-ctx.Done():
		return Notification{}, false
	}
}

// push delivers a notification, blocking only if the buffer is full (natural
// backpressure for a chatty monitor while a consumer catches up).
func (q *Queue) push(n Notification) { q.ch <- n }

// Start runs `command` in a goroutine and returns the new task's id.
func (q *Queue) Start(label, command string, timeout time.Duration) string {
	if label == "" {
		label = fmt.Sprintf("bg-%d", q.counter.Add(1))
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	id := newID()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t := q.register(id, label, command, KindCommand, cancel)
	go func() {
		defer cancel()
		// The hard safety floor applies to every shell surface, including the
		// background queue (which execs directly rather than via RunBash).
		if b, blocked := tools.SafetyFloorBlock(command); blocked {
			q.finish(t, "blocked", fmt.Sprintf("command blocked by safety floor (%q)", b))
			return
		}
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		out, err := cmd.CombinedOutput()
		status := "completed"
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			status = "timed_out"
		case ctx.Err() == context.Canceled:
			status = "cancelled"
		case err != nil:
			status = "failed"
		}
		s := strings.TrimRight(string(out), "\n")
		if s == "" {
			s = "(no output)"
		}
		if len(s) > 4000 {
			s = s[:4000] + "\n... (truncated)"
		}
		q.finish(t, status, s)
	}()
	return id
}

// SessionQueues holds one Queue per (user, session) so concurrent sessions
// each have an isolated notification stream + task registry. Use this in place
// of a single shared Queue when serving multiple sessions.
type SessionQueues struct {
	buf    int
	queues sync.Map // sessionKey -> *Queue
}

// NewSessionQueues returns a SessionQueues whose per-session queues are
// each created with the given channel buffer size.
func NewSessionQueues(buf int) *SessionQueues {
	if buf <= 0 {
		buf = 64
	}
	return &SessionQueues{buf: buf}
}

// For returns the Queue for the given (userID, sessionID), creating it
// on first use.
func (s *SessionQueues) For(userID, sessionID string) *Queue {
	key := userID + "\x00" + sessionID
	if v, ok := s.queues.Load(key); ok {
		return v.(*Queue)
	}
	v, _ := s.queues.LoadOrStore(key, NewQueue(s.buf))
	return v.(*Queue)
}

// resolveQueue picks the Queue matching the calling tool.Context.
func (s *SessionQueues) resolveQueue(ctx tool.Context) *Queue {
	var u, sid string
	if ctx != nil {
		u = ctx.UserID()
		sid = ctx.SessionID()
	}
	return s.For(u, sid)
}

// ----------------------------------------------------------------------
// ADK tool wrappers
// ----------------------------------------------------------------------

type startIn struct {
	Command string `json:"command"`
	Label   string `json:"label,omitempty"`
}
type startOut struct {
	Result string `json:"result"`
}

func startResult(id, label, command string) string {
	if label == "" {
		label = command
		if len(label) > 40 {
			label = label[:40] + "..."
		}
	}
	return fmt.Sprintf("Started %q (id=%s) in the background.", label, id)
}

func newStartTool(start func(label, command string, timeout time.Duration) string) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name: "bash_background",
		Description: "Start a shell command in the background. Returns immediately with a task id. " +
			"You will be notified of the result in a later turn. Use for long-running operations like test suites or builds.",
	}, func(_ tool.Context, in startIn) (startOut, error) {
		id := start(in.Label, in.Command, 0)
		return startOut{Result: startResult(id, in.Label, in.Command)}, nil
	})
	return t
}

// Tool returns the bash_background tool wired to q (single-queue / CLI use).
func (q *Queue) Tool() tool.Tool {
	return newStartTool(q.Start)
}

// Tool returns the bash_background tool. Each call routes to the queue
// matching the calling session, so notifications never cross between
// concurrent sessions.
func (s *SessionQueues) Tool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name: "bash_background",
		Description: "Start a shell command in the background. Returns immediately with a task id. " +
			"You will be notified of the result in a later turn. Use for long-running operations like test suites or builds.",
	}, func(ctx tool.Context, in startIn) (startOut, error) {
		id := s.resolveQueue(ctx).Start(in.Label, in.Command, 0)
		return startOut{Result: startResult(id, in.Label, in.Command)}, nil
	})
	return t
}

// Tools returns the full background/monitor/lifecycle toolset routed to the
// per-session queue: bash_background, monitor, bg_list, bg_cancel,
// bg_output. This is what the "bg" tool group mounts.
func (s *SessionQueues) Tools() []tool.Tool {
	resolve := func(ctx tool.Context) *Queue { return s.resolveQueue(ctx) }
	return []tool.Tool{
		s.Tool(),
		monitorTool(resolve),
		taskListTool(resolve),
		taskCancelTool(resolve),
		taskOutputTool(resolve),
	}
}

// FormatNotification renders a Notification as the kind of message the loop
// should inject as a user turn.
func FormatNotification(n Notification) string {
	id := n.TaskID
	switch {
	case n.Kind == KindMonitor && n.Status == "event":
		return fmt.Sprintf("[Monitor %q id=%s] matched:\n%s", n.Label, id, n.Output)
	case n.Kind == KindMonitor:
		return fmt.Sprintf("[Monitor %q id=%s] stopped (status=%s)", n.Label, id, n.Status)
	default:
		return fmt.Sprintf("[Background %q id=%s] status=%s duration=%s\n%s",
			n.Label, id, n.Status, n.Ended.Sub(n.Started).Round(time.Millisecond), n.Output)
	}
}

// FormatBatch renders a coalesced batch of notifications for a single injected
// turn (server active-wake) or drained context block (CLI/TUI).
func FormatBatch(ns []Notification) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, FormatNotification(n))
	}
	return strings.Join(parts, "\n\n")
}
