// Package tasks implements the article's "File-based task dependency graph"
// (Phase 2 / s07) plus "autonomous task self-assignment" (Phase 3 / s11).
// Tasks are persisted to .agent_tasks.json and protected by a mutex so
// multiple goroutine agents can claim them atomically.
package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// DefaultPath is the on-disk location.
const DefaultPath = ".agent_tasks.json"

// Status values.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusFailed     = "failed"
)

// Priority levels.
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// Task is one node in the dependency graph.
type Task struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Priority    string   `json:"priority"`
	DependsOn   []string `json:"depends_on"`
	ClaimedBy   string   `json:"claimed_by,omitempty"`
	Result      string   `json:"result,omitempty"`
}

// Graph is the persistent task graph.
type Graph struct {
	mu   sync.Mutex
	path string
}

// New returns a Graph backed by `path`.
func New(path string) *Graph {
	if path == "" {
		path = DefaultPath
	}
	return &Graph{path: path}
}

func (g *Graph) load() ([]Task, error) {
	data, err := os.ReadFile(g.path)
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

func (g *Graph) save(tasks []Task) error {
	data, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(g.path, data, 0o644)
}

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Create appends a new task. Returns its assigned id.
func (g *Graph) Create(description string, dependsOn []string, priority string) (string, error) {
	if priority == "" {
		priority = PriorityMedium
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	tasks, err := g.load()
	if err != nil {
		return "", err
	}
	t := Task{
		ID:          newID(),
		Description: description,
		Status:      StatusPending,
		Priority:    priority,
		DependsOn:   dependsOn,
	}
	tasks = append(tasks, t)
	if err := g.save(tasks); err != nil {
		return "", err
	}
	return t.ID, nil
}

// List returns all tasks in a readable form.
func (g *Graph) List() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	tasks, err := g.load()
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "(no tasks)", nil
	}
	var b strings.Builder
	for _, t := range tasks {
		fmt.Fprintf(&b, "  [%s] [%-7s] [%-12s] %s\n", t.ID, t.Priority, t.Status, t.Description)
	}
	return b.String(), nil
}

// ClaimNext atomically finds the highest-priority unblocked pending task,
// marks it in_progress + claimed_by=agentID, and returns it.
func (g *Graph) ClaimNext(agentID string) (*Task, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	tasks, err := g.load()
	if err != nil {
		return nil, err
	}
	doneIDs := map[string]bool{}
	for _, t := range tasks {
		if t.Status == StatusDone {
			doneIDs[t.ID] = true
		}
	}
	prio := func(p string) int {
		switch p {
		case PriorityHigh:
			return 0
		case PriorityMedium:
			return 1
		default:
			return 2
		}
	}
	bestIdx := -1
	for i := range tasks {
		t := &tasks[i]
		if t.Status != StatusPending {
			continue
		}
		ok := true
		for _, d := range t.DependsOn {
			if !doneIDs[d] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		if bestIdx < 0 || prio(t.Priority) < prio(tasks[bestIdx].Priority) {
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		return nil, nil
	}
	tasks[bestIdx].Status = StatusInProgress
	tasks[bestIdx].ClaimedBy = agentID
	if err := g.save(tasks); err != nil {
		return nil, err
	}
	t := tasks[bestIdx]
	return &t, nil
}

// Update sets the status (and optional result) of one task.
func (g *Graph) Update(id, status, result string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	tasks, err := g.load()
	if err != nil {
		return "", err
	}
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Status = status
			if result != "" {
				tasks[i].Result = result
			}
			if err := g.save(tasks); err != nil {
				return "", err
			}
			return fmt.Sprintf("task %s → %s", id, status), nil
		}
	}
	return fmt.Sprintf("Error: unknown task %s", id), nil
}

// ----------------------------------------------------------------------
// ADK tool wrappers
// ----------------------------------------------------------------------

type createIn struct {
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Priority    string   `json:"priority,omitempty"`
}
type createOut struct {
	ID string `json:"id"`
}
type listIn struct{}
type listOut struct {
	Tasks string `json:"tasks"`
}
type nextIn struct {
	AgentID string `json:"agent_id,omitempty"`
}
type nextOut struct {
	Task string `json:"task"`
}
type updateIn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Result string `json:"result,omitempty"`
}
type updateOut struct {
	Result string `json:"result"`
}

// Tools returns the four task-graph tools.
func (g *Graph) Tools() []tool.Tool {
	c, _ := functiontool.New(functiontool.Config{
		Name:        "task_create",
		Description: "Create a task in the durable task graph. Optional depends_on (task ids) and priority (high|medium|low). Returns the new task id.",
	}, func(_ tool.Context, in createIn) (createOut, error) {
		id, err := g.Create(in.Description, in.DependsOn, in.Priority)
		if err != nil {
			return createOut{ID: "Error: " + err.Error()}, nil
		}
		return createOut{ID: id}, nil
	})
	l, _ := functiontool.New(functiontool.Config{
		Name:        "task_list",
		Description: "List every task in the graph with id, priority, status, description.",
	}, func(_ tool.Context, _ listIn) (listOut, error) {
		s, err := g.List()
		if err != nil {
			return listOut{Tasks: "Error: " + err.Error()}, nil
		}
		return listOut{Tasks: s}, nil
	})
	n, _ := functiontool.New(functiontool.Config{
		Name:        "task_next",
		Description: "Atomically claim the next unblocked pending task. Returns the claimed task or '(none)'.",
	}, func(_ tool.Context, in nextIn) (nextOut, error) {
		t, err := g.ClaimNext(in.AgentID)
		if err != nil {
			return nextOut{Task: "Error: " + err.Error()}, nil
		}
		if t == nil {
			return nextOut{Task: "(none)"}, nil
		}
		b, _ := json.Marshal(t)
		return nextOut{Task: string(b)}, nil
	})
	u, _ := functiontool.New(functiontool.Config{
		Name:        "task_update",
		Description: "Update a task's status (pending|in_progress|done|failed) and optional result string.",
	}, func(_ tool.Context, in updateIn) (updateOut, error) {
		s, err := g.Update(in.ID, in.Status, in.Result)
		if err != nil {
			return updateOut{Result: "Error: " + err.Error()}, nil
		}
		return updateOut{Result: s}, nil
	})
	return []tool.Tool{c, l, n, u}
}
