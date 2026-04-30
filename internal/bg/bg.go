// Package bg implements the article's "Background task execution with
// notifications" (Phase 3 / s08). A bash command is launched in a goroutine;
// when it finishes, its result is pushed into a notification channel that
// the agent loop drains between turns.
package bg

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// Notification carries a completed background result.
type Notification struct {
	Label   string
	Status  string
	Output  string
	Started time.Time
	Ended   time.Time
}

// Queue is a thread-safe channel of notifications.
type Queue struct {
	ch chan Notification
}

// NewQueue returns a buffered notification queue.
func NewQueue(buf int) *Queue {
	if buf <= 0 {
		buf = 64
	}
	return &Queue{ch: make(chan Notification, buf)}
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

// startedCount tracks how many tasks have been launched (for default labels).
var startedCount int
var startedMu sync.Mutex

// Start runs `command` in a goroutine and returns immediately.
func (q *Queue) Start(label, command string, timeout time.Duration) {
	if label == "" {
		startedMu.Lock()
		startedCount++
		label = fmt.Sprintf("bg-%d", startedCount)
		startedMu.Unlock()
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	go func() {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		out, err := cmd.CombinedOutput()
		status := "completed"
		if ctx.Err() == context.DeadlineExceeded {
			status = "timed_out"
		} else if err != nil {
			status = "failed"
		}
		s := strings.TrimRight(string(out), "\n")
		if s == "" {
			s = "(no output)"
		}
		if len(s) > 4000 {
			s = s[:4000] + "\n... (truncated)"
		}
		q.ch <- Notification{
			Label:   label,
			Status:  status,
			Output:  s,
			Started: started,
			Ended:   time.Now(),
		}
	}()
}

// ----------------------------------------------------------------------
// ADK tool wrapper
// ----------------------------------------------------------------------

type startIn struct {
	Command string `json:"command"`
	Label   string `json:"label,omitempty"`
}
type startOut struct {
	Result string `json:"result"`
}

// Tool returns the bash_background tool wired to q.
func (q *Queue) Tool() tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name: "bash_background",
		Description: "Start a shell command in the background. Returns immediately. " +
			"You will be notified of the result in a later turn. Use for long-running operations like test suites or builds.",
	}, func(_ tool.Context, in startIn) (startOut, error) {
		q.Start(in.Label, in.Command, 0)
		label := in.Label
		if label == "" {
			label = in.Command
			if len(label) > 40 {
				label = label[:40] + "..."
			}
		}
		return startOut{Result: fmt.Sprintf("Background task started: %q. You will be notified when done.", label)}, nil
	})
	return t
}

// FormatNotification renders a Notification as the kind of message the loop
// should inject as a user turn.
func FormatNotification(n Notification) string {
	return fmt.Sprintf("[Background %q] status=%s duration=%s\n%s",
		n.Label, n.Status, n.Ended.Sub(n.Started), n.Output)
}
