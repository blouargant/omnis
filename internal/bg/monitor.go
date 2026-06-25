package bg

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"github.com/blouargant/omnis/core/tools"
)

// monitorBatchWindow coalesces matched lines arriving close together into a
// single notification, so a burst of matches wakes the conversation once.
const monitorBatchWindow = 200 * time.Millisecond

// StartMonitor runs `command` as a long-lived process, streams its stdout, and
// emits a notification for each line matching `filter` (a regexp; empty matches
// every line). Matched lines within monitorBatchWindow are coalesced. The
// monitor stops on command exit, on `timeout` (when > 0), or on bg_cancel;
// `persistent` is informational (log tailers vs bounded runs) — timeout/cancel
// still terminate. Returns the new task id.
func (q *Queue) StartMonitor(label, command, filter string, timeout time.Duration, persistent bool) (string, error) {
	re, err := regexp.Compile(filter)
	if err != nil {
		return "", fmt.Errorf("invalid filter regex %q: %w", filter, err)
	}
	if b, blocked := tools.SafetyFloorBlock(command); blocked {
		return "", fmt.Errorf("command blocked by safety floor (%q)", b)
	}
	if label == "" {
		label = fmt.Sprintf("mon-%d", q.counter.Add(1))
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	taskID := newID()
	t := q.register(taskID, label, command, KindMonitor, cancel)
	go q.runMonitor(ctx, t, re)
	return taskID, nil
}

func (q *Queue) runMonitor(ctx context.Context, t *Task, re *regexp.Regexp) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", t.Command)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		q.finishMonitor(t, "failed")
		return
	}
	if err := cmd.Start(); err != nil {
		q.finishMonitor(t, "failed")
		return
	}

	lines := make(chan string, 256)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if re.MatchString(line) {
				t.appendOutput(line)
				select {
				case lines <- line:
				default: // drop on overflow rather than block the reader
				}
			}
		}
		close(lines)
	}()

	var batch []string
	flush := func() {
		if len(batch) == 0 {
			return
		}
		out := strings.Join(batch, "\n")
		batch = batch[:0]
		q.push(Notification{
			TaskID: t.ID, Label: t.Label, Kind: KindMonitor, Status: "event",
			Output: out, Started: t.started, Ended: time.Now(),
		})
	}

	timer := time.NewTimer(time.Hour)
	timer.Stop()
	pending := false

loop:
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				break loop
			}
			batch = append(batch, l)
			if !pending {
				pending = true
				timer.Reset(monitorBatchWindow)
			}
		case <-timer.C:
			pending = false
			flush()
		case <-ctx.Done():
			break loop
		}
	}
	flush()
	_ = cmd.Wait()

	status := "completed"
	switch ctx.Err() {
	case context.DeadlineExceeded:
		status = "timed_out"
	case context.Canceled:
		status = "cancelled"
	}
	q.finishMonitor(t, status)
}

// finishMonitor records a monitor's terminal status and emits a final stopped
// notification.
func (q *Queue) finishMonitor(t *Task, status string) {
	t.setEnded(status)
	q.push(Notification{
		TaskID: t.ID, Label: t.Label, Kind: KindMonitor, Status: status,
		Started: t.started, Ended: t.ended,
	})
}

// ----------------------------------------------------------------------
// monitor tool
// ----------------------------------------------------------------------

type monitorIn struct {
	Command    string `json:"command"`
	Filter     string `json:"filter,omitempty"`
	Label      string `json:"label,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
	Persistent bool   `json:"persistent,omitempty"`
}
type monitorOut struct {
	Result string `json:"result"`
}

func monitorTool(resolve queueResolver) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name: "monitor",
		Description: "Watch a long-running command's output and get notified when lines match a filter. " +
			"`command` is a shell command that streams output (e.g. a log tail); `filter` is a regexp — " +
			"only matching lines notify you (empty = every line). For piped greps use --line-buffered " +
			"(e.g. `tail -f app.log | grep --line-buffered ERROR`) or output is buffered and notifications lag. " +
			"Set `timeout_sec` to auto-stop, or `persistent` for a session-long tail. Returns a task id; " +
			"use bg_cancel(id) to stop it and bg_output(id) to read buffered matches.",
	}, func(ctx tool.Context, in monitorIn) (monitorOut, error) {
		id, err := resolve(ctx).StartMonitor(in.Label, in.Command, in.Filter,
			time.Duration(in.TimeoutSec)*time.Second, in.Persistent)
		if err != nil {
			return monitorOut{}, err
		}
		label := in.Label
		if label == "" {
			label = in.Command
			if len(label) > 40 {
				label = label[:40] + "..."
			}
		}
		return monitorOut{Result: fmt.Sprintf("Monitor started: %q (id=%s). You'll be notified on matching lines.", label, id)}, nil
	})
	return t
}
