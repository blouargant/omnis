// Package todo implements the article's "TodoWrite planning before execution"
// (Phase 1 / s03). Three tools — todo_write, todo_read, todo_update — share
// a JSON file (.agent_todo.json by default). The system prompt is expected
// to make todo_write mandatory before any multi-step task.
//
// Like tasks/compress, the on-disk path can be made session-scoped via
// PathFunc so concurrent sessions never share a plan.
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

// DefaultPath is where the plan is persisted when no PathFunc is set.
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

// Store wraps the on-disk plan file. A single Store can serve many
// concurrent sessions when pathFor is set.
type Store struct {
	defaultPath string
	pathFor     func(userID, sessionID string) string

	muIndex sync.Mutex
	muxes   map[string]*sync.Mutex
}

// NewStore returns a Store using a single shared file. Suitable for
// single-user demos.
func NewStore(path string) *Store {
	if path == "" {
		path = DefaultPath
	}
	return &Store{defaultPath: path, muxes: map[string]*sync.Mutex{}}
}

// NewSessionScoped returns a Store whose plan file is computed per call
// from userID/sessionID. Concurrent sessions get isolated plans.
func NewSessionScoped(defaultPath string, pathFor func(userID, sessionID string) string) *Store {
	if defaultPath == "" {
		defaultPath = DefaultPath
	}
	if pathFor == nil {
		pathFor = func(_, _ string) string { return defaultPath }
	}
	return &Store{defaultPath: defaultPath, pathFor: pathFor, muxes: map[string]*sync.Mutex{}}
}

func (s *Store) resolvePath(ctx tool.Context) string {
	if s.pathFor == nil {
		return s.defaultPath
	}
	var u, sid string
	if ctx != nil {
		u = ctx.UserID()
		sid = ctx.SessionID()
	}
	if u == "" && sid == "" {
		return s.defaultPath
	}
	return s.pathFor(u, sid)
}

func (s *Store) lockFor(path string) *sync.Mutex {
	s.muIndex.Lock()
	defer s.muIndex.Unlock()
	if m, ok := s.muxes[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	s.muxes[path] = m
	return m
}

func loadFile(path string) ([]Task, error) {
	data, err := os.ReadFile(path)
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

func saveFile(path string, tasks []Task) error {
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// WriteAt replaces the plan at path with the given task descriptions.
func (s *Store) WriteAt(path string, tasks []string) (string, error) {
	m := s.lockFor(path)
	m.Lock()
	defer m.Unlock()
	out := make([]Task, len(tasks))
	for i, t := range tasks {
		out[i] = Task{ID: i, Task: t, Status: StatusPending}
	}
	if err := saveFile(path, out); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Plan written:\n")
	for _, t := range out {
		fmt.Fprintf(&b, "  [%d] %s\n", t.ID, t.Task)
	}
	return b.String(), nil
}

// Write is the back-compat wrapper using the default path.
func (s *Store) Write(tasks []string) (string, error) { return s.WriteAt(s.defaultPath, tasks) }

// ReadAt returns the current plan at path as a printable string.
func (s *Store) ReadAt(path string) (string, error) {
	m := s.lockFor(path)
	m.Lock()
	defer m.Unlock()
	tasks, err := loadFile(path)
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

// Read is the back-compat wrapper using the default path.
func (s *Store) Read() (string, error) { return s.ReadAt(s.defaultPath) }

// UpdateAt sets the status of one task by index in the given path.
func (s *Store) UpdateAt(path string, index int, status string) (string, error) {
	m := s.lockFor(path)
	m.Lock()
	defer m.Unlock()
	tasks, err := loadFile(path)
	if err != nil {
		return "", err
	}
	if index < 0 || index >= len(tasks) {
		return fmt.Sprintf("Error: index %d out of range", index), nil
	}
	tasks[index].Status = status
	if err := saveFile(path, tasks); err != nil {
		return "", err
	}
	return fmt.Sprintf("Task %d marked %s", index, status), nil
}

// Update is the back-compat wrapper using the default path.
func (s *Store) Update(index int, status string) (string, error) {
	return s.UpdateAt(s.defaultPath, index, status)
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

// Tools returns the three todo tools wired to s. Each call resolves its
// file path from the calling tool.Context (via PathFunc when configured).
func (s *Store) Tools() []tool.Tool {
	w, _ := functiontool.New(functiontool.Config{
		Name: "todo_write",
		Description: "Commit a complete plan as an ordered list of task descriptions. " +
			"ALWAYS call this first on any multi-step task before executing.",
	}, func(ctx tool.Context, in writeIn) (writeOut, error) {
		out, err := s.WriteAt(s.resolvePath(ctx), in.Tasks)
		if err != nil {
			return writeOut{Result: "Error: " + err.Error()}, nil
		}
		return writeOut{Result: out}, nil
	})
	r, _ := functiontool.New(functiontool.Config{
		Name:        "todo_read",
		Description: "Read the current plan and the status of each task.",
	}, func(ctx tool.Context, _ readIn) (readOut, error) {
		out, err := s.ReadAt(s.resolvePath(ctx))
		if err != nil {
			return readOut{Plan: "Error: " + err.Error()}, nil
		}
		return readOut{Plan: out}, nil
	})
	u, _ := functiontool.New(functiontool.Config{
		Name: "todo_update",
		Description: "Mark a task by index with a new status (pending, in_progress, done, failed). " +
			"Call after completing each step.",
	}, func(ctx tool.Context, in updateIn) (updateOut, error) {
		out, err := s.UpdateAt(s.resolvePath(ctx), in.Index, in.Status)
		if err != nil {
			return updateOut{Result: "Error: " + err.Error()}, nil
		}
		return updateOut{Result: out}, nil
	})
	return []tool.Tool{w, r, u}
}

var _ context.Context = nil
