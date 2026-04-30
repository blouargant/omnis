// Package todo implements the article's "TodoWrite planning before execution"
// (Phase 1 / s03). Three tools — todo_write, todo_read, todo_update — share
// a JSON file (.agent_todo.json by default). The system prompt is expected
// to make todo_write mandatory before any multi-step task.
package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// DefaultPath is where the plan is persisted.
const DefaultPath = ".agent_todo.json"

// Status values used by the plan.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusFailed     = "failed"
)

// Task is one entry in the plan.
type Task struct {
	ID     int    `json:"id"`
	Task   string `json:"task"`
	Status string `json:"status"`
}

// Store wraps the on-disk plan file.
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore returns a Store backed by `path`.
func NewStore(path string) *Store {
	if path == "" {
		path = DefaultPath
	}
	return &Store{path: path}
}

func (s *Store) load() ([]Task, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var t []Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) save(tasks []Task) error {
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o644)
}

// Write replaces the plan with the given task descriptions, all pending.
func (s *Store) Write(tasks []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, len(tasks))
	for i, t := range tasks {
		out[i] = Task{ID: i, Task: t, Status: StatusPending}
	}
	if err := s.save(out); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Plan written:\n")
	for _, t := range out {
		fmt.Fprintf(&b, "  [%d] %s\n", t.ID, t.Task)
	}
	return b.String(), nil
}

// Read returns the current plan as a printable string.
func (s *Store) Read() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.load()
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "(no plan)", nil
	}
	var b strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&b, "  [%d] [%-12s] %s\n", t.ID, t.Status, t.Task)
	}
	return b.String(), nil
}

// Update sets the status of one task by index.
func (s *Store) Update(index int, status string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks, err := s.load()
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(tasks) {
		return fmt.Sprintf("Error: index %d out of range", index), nil
	}
	tasks[index].Status = status
	if err := s.save(tasks); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task %d marked %s", index, status), nil
}

// ----------------------------------------------------------------------
// ADK tool wrappers
// ----------------------------------------------------------------------

type writeIn struct {
	Tasks []string `json:"tasks"`
}
type writeOut struct {
	Result string `json:"result"`
}
type readIn struct{}
type readOut struct {
	Plan string `json:"plan"`
}
type updateIn struct {
	Index  int    `json:"index"`
	Status string `json:"status"`
}
type updateOut struct {
	Result string `json:"result"`
}

// Tools returns the three todo tools wired to s.
func (s *Store) Tools() []tool.Tool {
	w, _ := functiontool.New(functiontool.Config{
		Name: "todo_write",
		Description: "Commit a complete plan as an ordered list of task descriptions. " +
			"ALWAYS call this first on any multi-step task before executing.",
	}, func(_ tool.Context, in writeIn) (writeOut, error) {
		out, err := s.Write(in.Tasks)
		if err != nil {
			return writeOut{Result: "Error: " + err.Error()}, nil
		}
		return writeOut{Result: out}, nil
	})
	r, _ := functiontool.New(functiontool.Config{
		Name:        "todo_read",
		Description: "Read the current plan and the status of each task.",
	}, func(_ tool.Context, _ readIn) (readOut, error) {
		out, err := s.Read()
		if err != nil {
			return readOut{Plan: "Error: " + err.Error()}, nil
		}
		return readOut{Plan: out}, nil
	})
	u, _ := functiontool.New(functiontool.Config{
		Name: "todo_update",
		Description: "Mark a task by index with a new status (pending, in_progress, done, failed). " +
			"Call after completing each step.",
	}, func(_ tool.Context, in updateIn) (updateOut, error) {
		out, err := s.Update(in.Index, in.Status)
		if err != nil {
			return updateOut{Result: "Error: " + err.Error()}, nil
		}
		return updateOut{Result: out}, nil
	})
	return []tool.Tool{w, r, u}
}

var _ context.Context = nil
