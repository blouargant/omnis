// memory_tools.go — memory_save / memory_read / memory_delete, the tools an
// answering squad root uses to keep its collection's memory (internal/collmem).
// The handlers are thin: resolve the user-facing session, then call a pure core
// that is unit-tested without an ADK context.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/internal/collmem"
)

type memorySaveIn struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Content     string `json:"content"`
}

type memorySaveOut struct {
	OK     bool   `json:"ok"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"` // "created" | "updated"
	Error  string `json:"error,omitempty"`
}

type memoryReadIn struct {
	Name string `json:"name"`
}

type memoryReadOut struct {
	Found       bool   `json:"found"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type,omitempty"`
	Updated     string `json:"updated,omitempty"`
	Content     string `json:"content,omitempty"`
	Error       string `json:"error,omitempty"`
}

type memoryDeleteIn struct {
	Name string `json:"name"`
}

type memoryDeleteOut struct {
	OK    bool   `json:"ok"`
	Name  string `json:"name,omitempty"`
	Error string `json:"error,omitempty"`
}

const (
	errMemInvalidName = `invalid name: use lower-case letters, digits and hyphens (for example "no-force-push")`
	errMemInvalidType = `invalid type: use feedback, user, project or reference`
	errMemEmpty       = `content is empty`
	errMemTooLarge    = `content is too long (4000 characters at most): keep one fact per memory`
	errMemFull        = `this collection already holds 200 memories: replace or delete one first`
	errMemReadOnly    = `memory is read-only in this turn: the user did not start it`
)

func errMemNotFound(name string) string { return fmt.Sprintf("no memory named %q", name) }

// memoryReadOnlyKey marks a run whose memory writes are refused: turns the user
// did not start (a mailbox message, an inbound A2A call, a spawned session's
// result notice, a background-task notification) and every turn of a hidden
// utility session. Scheduled routines, loops, spawned tasks and durable-question
// resumes keep write access.
type memoryReadOnlyKey struct{}

// WithMemoryReadOnly marks a run the user did not start: saves and deletes are refused.
func WithMemoryReadOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, memoryReadOnlyKey{}, true)
}

func memoryReadOnly(ctx context.Context) bool {
	v, _ := ctx.Value(memoryReadOnlyKey{}).(bool)
	return v
}

var (
	memoryChangeHookMu sync.RWMutex
	memoryChangeHook   func(sessionID, collection, name, op string)
)

// SetMemoryChangeHook installs the server's notifier, called after every tool write.
// op is "created", "updated" or "deleted". Pass nil to clear it.
func SetMemoryChangeHook(f func(sessionID, collection, name, op string)) {
	memoryChangeHookMu.Lock()
	memoryChangeHook = f
	memoryChangeHookMu.Unlock()
}

func notifyMemoryChange(sessionID, collection, name, op string) {
	memoryChangeHookMu.RLock()
	f := memoryChangeHook
	memoryChangeHookMu.RUnlock()
	if f != nil {
		f(sessionID, collection, name, op)
	}
}

// memoryErrorMessage maps a collmem error to the message the model reads.
func memoryErrorMessage(err error) string {
	switch {
	case errors.Is(err, collmem.ErrInvalidName):
		return errMemInvalidName
	case errors.Is(err, collmem.ErrInvalidType):
		return errMemInvalidType
	case errors.Is(err, collmem.ErrTooLarge):
		return errMemTooLarge
	case errors.Is(err, collmem.ErrFull):
		return errMemFull
	case errors.Is(err, collmem.ErrCollection):
		return "this chat's collection name cannot hold a memory"
	default:
		return err.Error()
	}
}

func memorySave(ctx context.Context, sessionID string, in memorySaveIn) memorySaveOut {
	if memoryReadOnly(ctx) {
		return memorySaveOut{Error: errMemReadOnly}
	}
	if strings.TrimSpace(in.Content) == "" {
		return memorySaveOut{Error: errMemEmpty}
	}
	store, col := memoryStoreFor(sessionID)
	saved, created, err := store.Save(col, collmem.Memory{
		Name:        in.Name,
		Description: in.Description,
		Type:        strings.TrimSpace(in.Type),
		Body:        in.Content,
	})
	if err != nil {
		return memorySaveOut{Error: memoryErrorMessage(err)}
	}
	status := "updated"
	if created {
		status = "created"
	}
	notifyMemoryChange(sessionID, col, saved.Name, status)
	return memorySaveOut{OK: true, Name: saved.Name, Status: status}
}

func memoryRead(sessionID, name string) memoryReadOut {
	n, ok := collmem.NormalizeName(name)
	if !ok {
		return memoryReadOut{Error: errMemInvalidName}
	}
	store, col := memoryStoreFor(sessionID)
	m, found := store.Get(col, n)
	if !found {
		return memoryReadOut{Name: n, Error: errMemNotFound(n)}
	}
	return memoryReadOut{
		Found:       true,
		Name:        m.Name,
		Description: m.Description,
		Type:        m.Type,
		Updated:     m.Updated,
		Content:     m.Body,
	}
}

func memoryDelete(ctx context.Context, sessionID, name string) memoryDeleteOut {
	if memoryReadOnly(ctx) {
		return memoryDeleteOut{Error: errMemReadOnly}
	}
	n, ok := collmem.NormalizeName(name)
	if !ok {
		return memoryDeleteOut{Error: errMemInvalidName}
	}
	store, col := memoryStoreFor(sessionID)
	removed, err := store.Delete(col, n)
	if err != nil {
		return memoryDeleteOut{Name: n, Error: memoryErrorMessage(err)}
	}
	if !removed {
		return memoryDeleteOut{Name: n, Error: errMemNotFound(n)}
	}
	notifyMemoryChange(sessionID, col, n, "deleted")
	return memoryDeleteOut{OK: true, Name: n}
}

// maxMemoryWritesPerTurn bounds memory_save + memory_delete calls in one run of a
// session. The 2026-11-09 bench saw high call memory_save 77 times in one turn:
// the injected block changed after each save and the rule "save a correction
// before you answer" fired again on the same user message. A turn needs one or
// two writes; past the cap the call is refused and the model is told to answer.
const maxMemoryWritesPerTurn = 5

const errTooManyWrites = "memory was already updated in this turn: do not save again, answer the user now"

// writeBudget counts memory writes per (session, invocation). It keeps only the
// latest invocation of each session, so it stays bounded by the session count.
type writeBudget struct {
	mu sync.Mutex
	by map[string]writeCount
}

type writeCount struct {
	inv string
	n   int
}

var memoryWrites = &writeBudget{by: map[string]writeCount{}}

// allow records one write and reports whether it is within the per-turn cap.
func (w *writeBudget) allow(sessionID, invocationID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	c := w.by[sessionID]
	if c.inv != invocationID {
		c = writeCount{inv: invocationID}
	}
	c.n++
	w.by[sessionID] = c
	return c.n <= maxMemoryWritesPerTurn
}

func mustMemoryTool[A, R any](name, desc string, h functiontool.Func[A, R]) tool.Tool {
	t, err := functiontool.New(functiontool.Config{Name: name, Description: desc}, h)
	if err != nil {
		panic(fmt.Errorf("build tool %s: %w", name, err))
	}
	return t
}

// memoryTools returns memory_save, memory_read, memory_delete.
func memoryTools() []tool.Tool {
	return []tool.Tool{
		mustMemoryTool("memory_save",
			"Save or replace one memory of this collection, so later conversations here know it. Call it whenever the user corrects you, states a lasting rule or fact, or asks you to remember something. Use the name of an existing memory to replace it.",
			func(tc adk.ToolContext, in memorySaveIn) (memorySaveOut, error) {
				sid := realSessionID(tc)
				if !memoryWrites.allow(sid, tc.InvocationID()) {
					return memorySaveOut{OK: false, Name: in.Name, Error: errTooManyWrites}, nil
				}
				return memorySave(tc, sid, in), nil
			}),
		mustMemoryTool("memory_read",
			"Read one memory of this collection in full.",
			func(tc adk.ToolContext, in memoryReadIn) (memoryReadOut, error) {
				return memoryRead(realSessionID(tc), in.Name), nil
			}),
		mustMemoryTool("memory_delete",
			"Delete one memory of this collection.",
			func(tc adk.ToolContext, in memoryDeleteIn) (memoryDeleteOut, error) {
				sid := realSessionID(tc)
				if !memoryWrites.allow(sid, tc.InvocationID()) {
					return memoryDeleteOut{OK: false, Name: in.Name, Error: errTooManyWrites}, nil
				}
				return memoryDelete(tc, sid, in.Name), nil
			}),
	}
}
