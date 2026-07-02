// spawn.go — the `spawn_session` leader tool: a squad leader hands a separate,
// parallel task to a BRAND-NEW session that starts with a fresh, empty context
// and inherits this session's working directory.
//
// Like the Omnis routing tools (routing.go), this is deliberately tiny and
// host-side: the tool only *records intent* (a SpawnDirective in SpawnRegistry,
// keyed by the parent session), and the surface materialises the real session
// after the turn finishes. The agent package cannot import the server, so this
// record-then-drain indirection is mandatory — the server drains the registry in
// handleMessages and creates + optionally auto-runs each requested session.
//
// Unlike route_to_squad, spawn_session does NOT end the run (no
// SkipSummarization): spawning is fire-and-forget delegation, so the leader keeps
// working after it. A leader may spawn several sessions in one turn (bounded by
// maxSpawnsPerSession).
package agent

import (
	"fmt"
	"strings"
	"sync"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// maxSpawnsPerSession bounds how many sessions one parent turn may spawn, so a
// runaway leader can't materialise sessions without limit.
const maxSpawnsPerSession = 8

// SpawnDirective is one queued "create a new session" request recorded by the
// spawn_session tool and consumed by the surface after the turn. It lives in
// SpawnRegistry keyed by the parent session id.
type SpawnDirective struct {
	Name   string // friendly title for the new session (may be empty)
	Squad  string // target squad (lower-cased; empty ⇒ surface default)
	Prompt string // initial task to run in the background (empty ⇒ idle session)
}

// SpawnRegistry holds pending spawn requests per parent session. It is
// process-wide (lives on Infrastructure, survives hot-reload); a leader may
// enqueue several in one turn, so unlike RouteRegistry it stores a slice per
// session. Requests are Enqueued by the tool during a turn and Drained
// (read+cleared) by the surface immediately after that turn's runner finishes.
type SpawnRegistry struct {
	mu sync.Mutex
	m  map[string][]*SpawnDirective
}

// NewSpawnRegistry returns an empty registry.
func NewSpawnRegistry() *SpawnRegistry {
	return &SpawnRegistry{m: map[string][]*SpawnDirective{}}
}

// Enqueue records a spawn request for sessionID. It returns false when the
// per-turn cap for the session is already reached (the tool surfaces that as an
// error so the leader stops spawning).
func (r *SpawnRegistry) Enqueue(sessionID string, d *SpawnDirective) bool {
	if r == nil || sessionID == "" || d == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.m[sessionID]) >= maxSpawnsPerSession {
		return false
	}
	r.m[sessionID] = append(r.m[sessionID], d)
	return true
}

// Drain returns and clears the queued spawn requests for sessionID (nil when
// none), in enqueue order.
func (r *SpawnRegistry) Drain(sessionID string) []*SpawnDirective {
	if r == nil || sessionID == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	ds := r.m[sessionID]
	delete(r.m, sessionID)
	return ds
}

type spawnIn struct {
	Name   string `json:"name" jsonschema:"a short, human-readable name/title for the new session (e.g. 'add tests for parser')"`
	Squad  string `json:"squad" jsonschema:"which squad the new session should run; one of the available squads listed in your instruction, or empty to let the Omnis router pick the best-suited squad for the task"`
	Prompt string `json:"prompt" jsonschema:"the initial task the new session starts working on immediately in the background; leave empty to create an idle session the user drives"`
}
type spawnOut struct {
	Result string `json:"result"`
}

// spawnSessionTool builds the leader-only `spawn_session` tool. validSquads is
// the set of squad names a spawned session may run (the surface still applies its
// own default/validation); an empty squad is accepted and resolved by the surface.
func spawnSessionTool(reg *SpawnRegistry, validSquads []string) tool.Tool {
	valid := make(map[string]bool, len(validSquads))
	for _, s := range validSquads {
		valid[lowerTrim(s)] = true
	}
	list := strings.Join(validSquads, ", ")
	t, err := functiontool.New(functiontool.Config{
		Name: "spawn_session",
		Description: "Spawn a NEW chat session that starts with a fresh, empty context and inherits this " +
			"session's working directory. Use it to hand off a separate, parallel task to a clean session — " +
			"it does NOT share this conversation's history, so restate everything the task needs in `prompt`. " +
			"If you provide `prompt`, the new session immediately starts working on it in the background, and when " +
			"it finishes its result is delivered back to YOU (this session) as a follow-up message you can act on; " +
			"leave `prompt` empty to create an idle session for the user. " +
			"Leave `squad` empty to let the Omnis router pick the best-suited squad for the task (recommended " +
			"unless you have a specific reason to force one). Available squads: " + list + ".",
	}, func(ctx tool.Context, in spawnIn) (spawnOut, error) {
		squad := lowerTrim(in.Squad)
		if squad != "" && len(valid) > 0 && !valid[squad] {
			return spawnOut{}, fmt.Errorf("unknown squad %q; choose one of: %s (or leave empty for the default)", in.Squad, list)
		}
		if !reg.Enqueue(ctx.SessionID(), &SpawnDirective{
			Name:   strings.TrimSpace(in.Name),
			Squad:  squad,
			Prompt: strings.TrimSpace(in.Prompt),
		}) {
			return spawnOut{}, fmt.Errorf("too many sessions spawned this turn (max %d) — wait for the current ones before spawning more", maxSpawnsPerSession)
		}
		name := strings.TrimSpace(in.Name)
		var b strings.Builder
		b.WriteString("Spawned a new session")
		if name != "" {
			b.WriteString(" (\"" + name + "\")")
		}
		if squad != "" {
			b.WriteString(" on the " + squad + " squad")
		}
		if strings.TrimSpace(in.Prompt) != "" {
			b.WriteString("; it is working on the task in the background and the user will be notified when it replies.")
		} else {
			b.WriteString("; it is idle and ready for the user to open.")
		}
		return spawnOut{Result: b.String()}, nil
	})
	if err != nil {
		panic(fmt.Errorf("spawn_session tool: %w", err))
	}
	return t
}
