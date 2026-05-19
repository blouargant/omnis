// Package tasks implements the article's "File-based task dependency graph"
// (Phase 2 / s07) plus "autonomous task self-assignment" (Phase 3 / s11).
// Tasks are persisted to a JSON file and protected by per-file mutexes so
// multiple goroutine agents can claim them atomically. The file path can
// be made session-scoped via PathFunc so concurrent sessions never share
// the same task graph.
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

// DefaultPath is the on-disk location used when no PathFunc is configured
// and no explicit path was passed to New.
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

// Graph is the persistent task graph. A single Graph value can serve many
// concurrent sessions when PathFunc is set: each resolved path gets its
// own mutex so cross-session calls do not block each other.
type Graph struct {
	defaultPath string
	pathFor     func(userID, sessionID string) string

	muIndex sync.Mutex
	muxes   map[string]*sync.Mutex
}

// New returns a Graph that uses a single shared on-disk file. Suitable for
// single-user demos. Use NewSessionScoped for multi-session setups.
func New(path string) *Graph {
	if path == "" {
		path = DefaultPath
	}
	return &Graph{defaultPath: path, muxes: map[string]*sync.Mutex{}}
}

// NewSessionScoped returns a Graph whose on-disk file is computed per call
// from the userID/sessionID exposed by tool.Context. This guarantees
// concurrent sessions each get their own isolated task graph. The default
// path (used by direct Go callers and as a fallback when callbacks fire
// before a session is bound) is kept for back-compat.
func NewSessionScoped(defaultPath string, pathFor func(userID, sessionID string) string) *Graph {
	if defaultPath == "" {
		defaultPath = DefaultPath
	}
	if pathFor == nil {
		pathFor = func(_, _ string) string { return defaultPath }
	}
	return &Graph{
		defaultPath: defaultPath,
		pathFor:     pathFor,
		muxes:       map[string]*sync.Mutex{},
	}
}

// resolvePath turns a tool.Context into the file path the call should use.
// Empty IDs (e.g. early callbacks) fall back to the default path.
func (g *Graph) resolvePath(ctx tool.Context) string {
	if g.pathFor == nil {
		return g.defaultPath
	}
	var u, s string
	if ctx != nil {
		u = ctx.UserID()
		s = ctx.SessionID()
	}
	if u == "" && s == "" {
		return g.defaultPath
	}
	return g.pathFor(u, s)
}

// lockFor returns the per-path mutex, allocating it on first use.
func (g *Graph) lockFor(path string) *sync.Mutex {
	g.muIndex.Lock()
	defer g.muIndex.Unlock()
	if m, ok := g.muxes[path]; ok {
		return m
	}
	m := &sync.Mutex{}
	g.muxes[path] = m
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

func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// CreateAt appends a new task to the given path. Returns its assigned id.
func (g *Graph) CreateAt(path, description string, dependsOn []string, priority string) (string, error) {
	if priority == "" {
		priority = PriorityMedium
	}
	m := g.lockFor(path)
	m.Lock()
	defer m.Unlock()
	tasks, err := loadFile(path)
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
	if err := saveFile(path, tasks); err != nil {
		return "", err
	}
	return t.ID, nil
}

// Create is the back-compat wrapper that uses the Graph's default path.
func (g *Graph) Create(description string, dependsOn []string, priority string) (string, error) {
	return g.CreateAt(g.defaultPath, description, dependsOn, priority)
}

// ListAt returns all tasks at the given path in a readable form.
func (g *Graph) ListAt(path string) (string, error) {
	m := g.lockFor(path)
	m.Lock()
	defer m.Unlock()
	tasks, err := loadFile(path)
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

// List is the back-compat wrapper that uses the Graph's default path.
func (g *Graph) List() (string, error) { return g.ListAt(g.defaultPath) }

// ClaimNextAt atomically finds the highest-priority unblocked pending task
// in the given path, marks it in_progress + claimed_by=agentID, and
// returns it.
func (g *Graph) ClaimNextAt(path, agentID string) (*Task, error) {
	m := g.lockFor(path)
	m.Lock()
	defer m.Unlock()
	tasks, err := loadFile(path)
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
	if err := saveFile(path, tasks); err != nil {
		return nil, err
	}
	t := tasks[bestIdx]
	return &t, nil
}

// ClaimNext is the back-compat wrapper that uses the Graph's default path.
func (g *Graph) ClaimNext(agentID string) (*Task, error) {
	return g.ClaimNextAt(g.defaultPath, agentID)
}

// UpdateAt sets the status (and optional result) of one task in the given
// path.
func (g *Graph) UpdateAt(path, id, status, result string) (string, error) {
	m := g.lockFor(path)
	m.Lock()
	defer m.Unlock()
	tasks, err := loadFile(path)
	if err != nil {
		return "", err
	}
	for i := range tasks {
		if tasks[i].ID == id {
			tasks[i].Status = status
			if result != "" {
				tasks[i].Result = result
			}
			if err := saveFile(path, tasks); err != nil {
				return "", err
			}
			return fmt.Sprintf("task %s → %s", id, status), nil
		}
	}
	return fmt.Sprintf("Error: unknown task %s", id), nil
}

// Update is the back-compat wrapper that uses the Graph's default path.
func (g *Graph) Update(id, status, result string) (string, error) {
	return g.UpdateAt(g.defaultPath, id, status, result)
}

// ----------------------------------------------------------------------
// ADK tool wrappers
// ----------------------------------------------------------------------

// coerceDependsOn turns the loose value the LLM may emit for the
// `depends_on` argument into a clean []string. We accept any of:
//
//   - nil / missing
//   - "abc"                     → ["abc"]
//   - "abc, def" / "abc def"   → ["abc", "def"]
//   - ["abc", "def"]            → ["abc", "def"]
//   - ["abc", 123, nil]         → ["abc", "123"]  (numbers stringified, nils dropped)
//
// Being lenient here avoids burning tokens on retry loops when the model
// emits a bare string instead of a one-element array. We use `any` for
// the field type so JSON-schema reflection produces a permissive schema
// that won't reject the call before this coercion runs.
func coerceDependsOn(v any) []string {
	split := func(s string) []string {
		var out []string
		for _, part := range strings.FieldsFunc(s, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return split(x)
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			switch s := e.(type) {
			case nil:
				continue
			case string:
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			default:
				out = append(out, fmt.Sprintf("%v", s))
			}
		}
		return out
	}
	return nil
}

type createIn struct {
	Description string `json:"description"`
	// DependsOn is intentionally typed as `any` so the auto-generated
	// JSON schema doesn't reject calls where the model passed a bare
	// string instead of an array. coerceDependsOn normalises it.
	DependsOn any    `json:"depends_on,omitempty"`
	Priority  string `json:"priority,omitempty"`
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

// Tools returns the four task-graph tools. Each tool resolves its file
// path from the calling tool.Context (via PathFunc when configured), so
// concurrent sessions get isolated task graphs automatically.
func (g *Graph) Tools() []tool.Tool {
	c, _ := functiontool.New(functiontool.Config{
		Name: "TaskCreate",
		Description: "Create a task in the durable task graph. " +
			"Arguments: " +
			"`description` (string, required); " +
			"`depends_on` (array of task-id strings, optional — MUST be a JSON array, e.g. [\"1ed6204a\"]; use [] when there are no dependencies); " +
			"`priority` (one of \"high\"|\"medium\"|\"low\", default \"medium\"). " +
			"Returns the new task id.",
	}, func(ctx tool.Context, in createIn) (createOut, error) {
		id, err := g.CreateAt(g.resolvePath(ctx), in.Description, coerceDependsOn(in.DependsOn), in.Priority)
		if err != nil {
			return createOut{ID: "Error: " + err.Error()}, nil
		}
		return createOut{ID: id}, nil
	})
	l, _ := functiontool.New(functiontool.Config{
		Name:        "task_list",
		Description: "List every task in the graph with id, priority, status, description.",
	}, func(ctx tool.Context, _ listIn) (listOut, error) {
		s, err := g.ListAt(g.resolvePath(ctx))
		if err != nil {
			return listOut{Tasks: "Error: " + err.Error()}, nil
		}
		return listOut{Tasks: s}, nil
	})
	n, _ := functiontool.New(functiontool.Config{
		Name:        "task_next",
		Description: "Atomically claim the next unblocked pending task. Returns the claimed task or '(none)'.",
	}, func(ctx tool.Context, in nextIn) (nextOut, error) {
		t, err := g.ClaimNextAt(g.resolvePath(ctx), in.AgentID)
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
		Name:        "TaskUpdate",
		Description: "Update a task's status (pending|in_progress|done|failed) and optional result string.",
	}, func(ctx tool.Context, in updateIn) (updateOut, error) {
		s, err := g.UpdateAt(g.resolvePath(ctx), in.ID, in.Status, in.Result)
		if err != nil {
			return updateOut{Result: "Error: " + err.Error()}, nil
		}
		return updateOut{Result: s}, nil
	})
	return []tool.Tool{c, l, n, u}
}
