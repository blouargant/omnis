// infrastructure.go — process-wide / cross-generation state used by every
// agent instance. Hot-reload swaps the agent Instance but keeps this in place,
// so cross-session mailbox addressing, the event bus, and ask_user pending
// questions survive a config reload transparently.
package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/blouargant/yoke/core/events"
	"github.com/blouargant/yoke/internal/askuser"
	"github.com/blouargant/yoke/internal/bg"
	mcpcfg "github.com/blouargant/yoke/internal/mcp"
	"github.com/blouargant/yoke/internal/paths"
	"github.com/blouargant/yoke/internal/skills"
	"github.com/blouargant/yoke/internal/tasks"
	"github.com/blouargant/yoke/internal/teammates"
	"github.com/blouargant/yoke/internal/todo"
)

// Infrastructure holds the long-lived components shared across every agent
// generation. A generation rebuild (config reload) creates a new Instance on
// top of the same Infrastructure: the mailbox backend, session registry,
// event bus and ask_user registry stay put so in-flight messages, pending
// questions, and cross-session addressing are not disturbed.
type Infrastructure struct {
	AppName        string
	Repo           string
	BuildTimestamp string

	Backend         teammates.Backend
	Registry        *teammates.SessionRegistry
	Bus             *events.Bus
	AskUserRegistry *askuser.Registry

	// RouteDirectives holds the pending Omnis routing directive per session.
	// Written by the route_to_squad / handoff_to_router tools during a turn and
	// consumed by Manager.RunWithRouting after the turn's runner finishes.
	// Process-wide so it survives hot-reload (directives are transient per turn).
	RouteDirectives *RouteRegistry

	// Session-scoped state holders. Each is a process-singleton that lazily
	// creates per-(userID, sessionID) entries on disk, so they trivially
	// outlive any single Instance.
	TaskGraph *tasks.Graph
	BgQueues  *bg.SessionQueues
	TodoStore *todo.Store

	// MCPPool dedups MCP toolset construction so two agent generations that
	// mount the same server share one subprocess. Each Instance acquires
	// handles at build time and releases them when Close is called, so a
	// hot-reload that only changes one server doesn't restart the others.
	MCPPool *mcpcfg.Pool

	// embed memoises the process-wide semantic embedder (see Embedder). Built
	// lazily from the first generation's runtime settings and reused across
	// reloads — the client is expensive and the indexes it feeds live on disk.
	embed embedderCache

	// precedents memoises the process-wide cross-session precedent index (see
	// Precedents). Backed by the same embedder; on-disk so it survives reloads.
	precedents precedentsCache

	// codeIndex memoises the process-wide project code-search index (see
	// CodeIndex). Keyed by the repo root; backed by the same embedder.
	codeIndex codeIndexCache

	// regIndex memoises the process-wide semantic index over remote registry
	// items (see RegistryIndex). Backed by the same embedder; rebuilt on
	// registry config change via registries.OnSave.
	regIndex regIndexCache

	// docIndex memoises the process-wide semantic index over yoke's own
	// documentation (see DocIndex). Backed by the same embedder; on-disk so it
	// survives reloads, refreshed by the startup docs indexer.
	docIndex docIndexCache

	// hooks memoises the process-wide Claude Code-style hooks engine (a
	// hot-reloading Reloader over hooks.json plus the fire-and-forget lifecycle
	// bus listeners, wired exactly once). Built lazily from the first
	// generation's runtime settings; see Hooks.
	hooks hooksCache
}

// BuildInfrastructure constructs the shared infrastructure for the agent.
// Call exactly once per process; pass the returned *Infrastructure to
// BuildInstance for each generation.
func BuildInfrastructure(ctx context.Context, opts Options) (*Infrastructure, error) {
	_ = ctx // reserved for future use (e.g. backend dial)
	repo := opts.Repo
	if repo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("bootstrap: getwd: %w", err)
		}
		repo = cwd
	}

	appName := opts.AppName
	if appName == "" {
		appName = "yoke"
	}

	buildTimestamp := time.Now().Format("20060102_150405")

	be, err := teammates.ChooseBackend()
	if err != nil {
		return nil, fmt.Errorf("mailbox backend: %w", err)
	}
	reg := teammates.NewSessionRegistry(paths.MailboxesDir())
	bus := events.NewBus()
	var askUserOpts []askuser.RegistryOption
	if opts.DisableAskUserTimeout {
		// Web UI surfaces keep the ask-user / permission card up and wait for
		// the user; a timeout of 0 disarms the per-question timer (context
		// cancellation on turn abort still ends the wait).
		askUserOpts = append(askUserOpts, askuser.WithDefaultTimeout(0))
	}
	askUserReg := askuser.NewRegistry(askUserOpts...)

	// Wire ask_user registry notifications through the event bus so server
	// and TUI surfaces receive questions and cancellations as bus events.
	askUserReg.SetNotify(func(q askuser.Question) {
		bus.Emit(events.EventAskUser, askuser.QuestionToPayload(q))
	})
	askUserReg.SetCancel(func(q askuser.Question) {
		bus.Emit(events.EventAskUserCancel, map[string]any{
			"question_id": q.ID,
			"session_id":  q.SessionID,
		})
	})

	// Install the process-wide skill dependency gate: when a loaded skill
	// declares `requires:`, load_skill checks/installs the missing binaries
	// (asking the user first) before the model proceeds. Process-wide because
	// the ask-user registry is process-wide and survives hot-reload.
	skills.SetDepGate(newSkillDepGate(askUserReg))

	suffix := func(userID, sessionID string) string {
		u := sanitizeID(userID)
		if u == "" {
			u = "anon"
		}
		if s := sanitizeID(sessionID); s != "" {
			return u + "_" + s
		}
		return u + "_" + buildTimestamp
	}

	taskGraph := tasks.NewSessionScoped("", func(u, s string) string {
		return filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_tasks_%s.json", suffix(u, s)))
	})
	bgQueues := bg.NewSessionQueues(32)
	todoStore := todo.NewSessionScoped("", func(u, s string) string {
		return filepath.Join(paths.LogsDir(), fmt.Sprintf("agent_todo_%s.json", suffix(u, s)))
	})

	return &Infrastructure{
		AppName:         appName,
		Repo:            repo,
		BuildTimestamp:  buildTimestamp,
		Backend:         be,
		Registry:        reg,
		Bus:             bus,
		AskUserRegistry: askUserReg,
		TaskGraph:       taskGraph,
		BgQueues:        bgQueues,
		TodoStore:       todoStore,
		MCPPool:         mcpcfg.NewPool(mcpcfg.NewInputResolver(askUserReg)),
		RouteDirectives: NewRouteRegistry(),
	}, nil
}

// SessionSuffix returns the deterministic per-session filename suffix used
// across all components (tasks, todo, mailbox, audit, statelog). The
// infrastructure's BuildTimestamp is used as the fallback when sessionID is
// empty (CLI mode).
func (i *Infrastructure) SessionSuffix(userID, sessionID string) string {
	u := sanitizeID(userID)
	if u == "" {
		u = "anon"
	}
	if s := sanitizeID(sessionID); s != "" {
		return u + "_" + s
	}
	return u + "_" + i.BuildTimestamp
}

// NameFunc returns the canonical mailbox-address derivation for an agent
// within a session. Shared across generations so addressing stays stable.
func (i *Infrastructure) NameFunc(userID, sessionID, agentName string) string {
	return i.SessionSuffix(userID, sessionID) + ":" + agentName
}

// RegisterSession registers a session's leader mailbox in the cross-session
// registry under displayName.
func (i *Infrastructure) RegisterSession(userID, sessionID, displayName string) error {
	return i.Registry.Register(displayName, i.NameFunc(userID, sessionID, "leader"))
}

// RenameSession moves an entry from oldName to newName.
func (i *Infrastructure) RenameSession(oldName, newName string) error {
	return i.Registry.Rename(oldName, newName)
}

// UnregisterSession removes the session from the cross-session registry.
func (i *Infrastructure) UnregisterSession(displayName string) error {
	return i.Registry.Unregister(displayName)
}

// ListSessionRegistry returns a snapshot of all registered (displayName → addr).
func (i *Infrastructure) ListSessionRegistry() map[string]string {
	return i.Registry.List()
}

// WatchMailbox starts a goroutine that polls the leader mailbox for
// (userID, sessionID) and calls onMessage when a message arrives. Exits when
// ctx is cancelled.
func (i *Infrastructure) WatchMailbox(ctx context.Context, userID, sessionID string, onMessage func(from, body string)) {
	addr := i.NameFunc(userID, sessionID, "leader")
	go func() {
		for {
			m, err := i.Backend.Receive(ctx, addr, 2*time.Second)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				continue
			}
			if m == nil {
				continue
			}
			// Resolve the sender's mailbox address to a friendly session
			// name via registry reverse-lookup; fall back to the raw address.
			from := m.From
			for name, maddr := range i.Registry.List() {
				if maddr == m.From {
					from = name
					break
				}
			}
			onMessage(from, m.Body)
		}
	}()
}

// Close releases shared resources held by the infrastructure. Safe to call
// at most once; subsequent calls are no-ops at the backend level.
func (i *Infrastructure) Close() error {
	if i == nil || i.Backend == nil {
		return nil
	}
	return i.Backend.Close()
}
