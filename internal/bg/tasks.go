package bg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const maxTaskLines = 500

// Task is one registered background launch (a one-shot command or a streaming
// monitor). It is the unit the lifecycle tools list, cancel, and read.
type Task struct {
	ID      string
	Label   string
	Kind    string
	Command string
	cancel  context.CancelFunc

	mu      sync.Mutex
	status  string
	started time.Time
	ended   time.Time
	lines   []string // bounded output ring
}

// TaskInfo is the JSON-friendly snapshot returned by bg_list.
type TaskInfo struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Kind    string    `json:"kind"`
	Status  string    `json:"status"`
	Command string    `json:"command,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitempty"`
	Lines   int       `json:"output_lines"`
}

func newID() string {
	var b [5]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (t *Task) appendOutput(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, s)
	if len(t.lines) > maxTaskLines {
		t.lines = t.lines[len(t.lines)-maxTaskLines:]
	}
}

func (t *Task) setEnded(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = status
	t.ended = time.Now()
}

func (t *Task) info() TaskInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return TaskInfo{
		ID: t.ID, Label: t.Label, Kind: t.Kind, Status: t.status,
		Command: t.Command, Started: t.started, Ended: t.ended, Lines: len(t.lines),
	}
}

func (t *Task) output() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// ----------------------------------------------------------------------
// Queue registry
// ----------------------------------------------------------------------

func (q *Queue) register(id, label, command, kind string, cancel context.CancelFunc) *Task {
	t := &Task{
		ID: id, Label: label, Command: command, Kind: kind, cancel: cancel,
		status: "running", started: time.Now(),
	}
	q.mu.Lock()
	if q.tasks == nil {
		q.tasks = make(map[string]*Task)
	}
	q.tasks[id] = t
	q.mu.Unlock()
	return t
}

func (q *Queue) task(id string) (*Task, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[id]
	return t, ok
}

// finish records a terminal status + output for a one-shot command task and
// emits its notification.
func (q *Queue) finish(t *Task, status, output string) {
	if output != "" {
		t.appendOutput(output)
	}
	t.setEnded(status)
	q.push(Notification{
		TaskID: t.ID, Label: t.Label, Kind: t.Kind, Status: status,
		Output: output, Started: t.started, Ended: t.ended,
	})
}

// ListTasks returns a snapshot of every registered task, newest first.
func (q *Queue) ListTasks() []TaskInfo {
	q.mu.Lock()
	tasks := make([]*Task, 0, len(q.tasks))
	for _, t := range q.tasks {
		tasks = append(tasks, t)
	}
	q.mu.Unlock()
	out := make([]TaskInfo, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

// Cancel stops a running task (no-op if already finished / unknown id).
func (q *Queue) Cancel(id string) (TaskInfo, bool) {
	t, ok := q.task(id)
	if !ok {
		return TaskInfo{}, false
	}
	if t.cancel != nil {
		t.cancel()
	}
	return t.info(), true
}

// Output returns a task's buffered output.
func (q *Queue) Output(id string) (string, TaskInfo, bool) {
	t, ok := q.task(id)
	if !ok {
		return "", TaskInfo{}, false
	}
	return t.output(), t.info(), true
}

// ----------------------------------------------------------------------
// Lifecycle tools (shared by Queue and SessionQueues via a resolver)
// ----------------------------------------------------------------------

type queueResolver func(tool.Context) *Queue

type taskListOut struct {
	Tasks []TaskInfo `json:"tasks"`
}

func taskListTool(resolve queueResolver) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "bg_list",
		Description: "List background tasks and monitors for this session (id, label, kind, status).",
	}, func(ctx tool.Context, _ struct{}) (taskListOut, error) {
		return taskListOut{Tasks: resolve(ctx).ListTasks()}, nil
	})
	return t
}

type taskIDIn struct {
	ID string `json:"id"`
}
type taskCancelOut struct {
	Result string `json:"result"`
}

func taskCancelTool(resolve queueResolver) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "bg_cancel",
		Description: "Cancel a running background task or monitor by its id.",
	}, func(ctx tool.Context, in taskIDIn) (taskCancelOut, error) {
		info, ok := resolve(ctx).Cancel(in.ID)
		if !ok {
			return taskCancelOut{Result: fmt.Sprintf("no task with id %q", in.ID)}, nil
		}
		return taskCancelOut{Result: fmt.Sprintf("cancel requested for %q (%s)", info.Label, info.ID)}, nil
	})
	return t
}

type taskOutputOut struct {
	Status string `json:"status"`
	Output string `json:"output"`
}

func taskOutputTool(resolve queueResolver) tool.Tool {
	t, _ := functiontool.New(functiontool.Config{
		Name:        "bg_output",
		Description: "Read the buffered output of a background task or monitor by its id.",
	}, func(ctx tool.Context, in taskIDIn) (taskOutputOut, error) {
		out, info, ok := resolve(ctx).Output(in.ID)
		if !ok {
			return taskOutputOut{Status: "unknown", Output: fmt.Sprintf("no task with id %q", in.ID)}, nil
		}
		if out == "" {
			out = "(no output yet)"
		}
		return taskOutputOut{Status: info.Status, Output: out}, nil
	})
	return t
}
