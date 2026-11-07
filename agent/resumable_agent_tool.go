package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"

	"github.com/blouargant/omnis/core/adk"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/agenttool"
)

// Durable sub-agent session bounds. Sessions are kept in-memory for the lifetime
// of the agent generation (handles do NOT survive a hot-reload — a stale handle
// simply falls back to a fresh session, matching the leader's own per-session
// retention boundary). A TTL drops idle sessions and a cap LRU-evicts the oldest
// so an abandoned-handle leak stays bounded without any cross-session GC wiring.
const (
	resumeSessionTTL = 30 * time.Minute
	resumeSessionCap = 32
)

// resumableAgentTool is a drop-in replacement for the per-call agenttool used
// when a sub-agent opts into durable sessions (`resumable_sessions: true`).
// Unlike agenttool — which builds a throwaway session.InMemoryService per call —
// it owns ONE persistent service + runner and a handle→session map, so the leader
// can resume a prior conversation by passing back the `session` handle the tool
// returned. It implements runnableTool, so it plugs into newConcurrentAgentTool
// exactly like the plain agenttool: durability and parallel fan-out are orthogonal
// because each call gets its OWN handle — resume always addresses one specific
// handle, never "the agent".
type resumableAgentTool struct {
	runnableTool // underlying agenttool: provides Name / IsLongRunning / the base Declaration / ProcessRequest

	agent   adkagent.Agent
	runner  *runner.Runner
	svc     session.Service
	appName string
	ttl     time.Duration
	cap     int

	mu      sync.Mutex
	handles map[string]*subSession // ADK session id → state
	aliases map[string]string      // short handle ("reviewer-3") → ADK session id
	seq     int
}

type subSession struct {
	userID   string
	lastUsed time.Time
	inUse    bool
	// parent is the user-facing session that created it. A session is only
	// ever resumed from that same parent: the tool instance is shared by every
	// session of the generation, and a short handle is guessable.
	parent string
	alias  string
}

// newResumableAgentTool builds the persistent runner + service for sa.
func newResumableAgentTool(sa adkagent.Agent) (runnableTool, error) {
	svc := session.InMemoryService()
	r, err := runner.New(runner.Config{
		AppName:         sa.Name(),
		Agent:           sa,
		SessionService:  svc,
		ArtifactService: artifact.InMemoryService(),
		MemoryService:   memory.InMemoryService(),
	})
	if err != nil {
		return nil, err
	}
	base, ok := agenttool.New(sa, &agenttool.Config{}).(runnableTool)
	if !ok {
		return nil, fmt.Errorf("agenttool for %q is not runnable", sa.Name())
	}
	return &resumableAgentTool{
		runnableTool: base,
		agent:        sa,
		runner:       r,
		svc:          svc,
		appName:      sa.Name(),
		ttl:          resumeSessionTTL,
		cap:          resumeSessionCap,
		handles:      map[string]*subSession{},
		aliases:      map[string]string{},
	}, nil
}

// Description appends a note so the leader knows the call is resumable.
func (t *resumableAgentTool) Description() string {
	return t.runnableTool.Description() +
		"\n\nThe result carries a `session` handle (e.g. `" + t.Name() + "-1`): pass it, or \"last\", as " +
		"`resume_session` to continue that conversation with its context; omit it to start fresh."
}

// Declaration adds the optional `resume_session` parameter to the sub-agent's
// own input schema, leaving everything else untouched.
func (t *resumableAgentTool) Declaration() *genai.FunctionDeclaration {
	base := t.runnableTool.Declaration()
	if base == nil {
		return nil
	}
	decl := *base
	params := &genai.Schema{Type: genai.TypeObject, Properties: map[string]*genai.Schema{}}
	if base.Parameters != nil {
		p := *base.Parameters
		params = &p
	}
	props := make(map[string]*genai.Schema, len(params.Properties)+1)
	for k, v := range params.Properties {
		props[k] = v
	}
	props["resume_session"] = &genai.Schema{
		Type: genai.TypeString,
		Description: "optional: a `session` handle from an earlier call, or \"last\"",
	}
	params.Properties = props
	decl.Parameters = params
	decl.Description = t.Description()
	return &decl
}

// Run resolves (resume or mint) a session, runs the sub-agent on the persistent
// runner, and returns its output plus the `session` handle for later resumption.
func (t *resumableAgentTool) Run(toolCtx adk.ToolContext, args any) (map[string]any, error) {
	margs, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("resumable sub-agent %q expects object arguments, got %T", t.Name(), args)
	}

	// Pull the resume handle out so it never reaches the sub-agent's content.
	resume, _ := margs["resume_session"].(string)
	resume = strings.TrimSpace(resume)

	content, err := buildSubAgentContent(t.Name(), margs)
	if err != nil {
		return nil, err
	}

	handle, alias, note, err := t.resolveSession(toolCtx, resume)
	if err != nil {
		return nil, err
	}
	defer t.release(handle)

	eventCh := t.runner.Run(toolCtx, toolCtx.UserID(), handle, content,
		adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE})
	var lastEvent *session.Event
	for ev, err := range eventCh {
		if err != nil {
			return nil, fmt.Errorf("resumable sub-agent %q: %w", t.Name(), err)
		}
		if ev == nil {
			continue
		}
		if ev.ErrorCode != "" || ev.ErrorMessage != "" {
			return nil, fmt.Errorf("resumable sub-agent %q error (code %q): %s", t.Name(), ev.ErrorCode, ev.ErrorMessage)
		}
		if ev.LLMResponse.Content != nil {
			lastEvent = ev
		}
	}

	out := map[string]any{"session": alias}
	if note != "" {
		out["note"] = note
	}
	if lastEvent != nil {
		var b strings.Builder
		for _, p := range lastEvent.Content.Parts {
			if p != nil && p.Text != "" {
				b.WriteString(p.Text)
			}
		}
		out["result"] = b.String()
	}
	// A run that ended on a tool call (a host-side stop) has no text in its last
	// event. Say so rather than hand the caller an empty string it may read as
	// "nothing found".
	if r, _ := out["result"].(string); strings.TrimSpace(r) == "" {
		out["result"] = emptySubAgentResult
	}
	return out, nil
}

// emptySubAgentResult replaces an empty sub-agent result.
const emptySubAgentResult = "(The sub-agent stopped without writing a result. Its findings are lost; " +
	"if they are still needed, delegate a narrower task.)"

// buildSubAgentContent mirrors agenttool's content shaping without the internal
// schema package: a lone `request` string becomes the content text, otherwise
// the remaining arguments are marshalled as a JSON object.
func buildSubAgentContent(name string, margs map[string]any) (*genai.Content, error) {
	payload := make(map[string]any, len(margs))
	for k, v := range margs {
		if k != "resume_session" {
			payload[k] = v
		}
	}
	if len(payload) == 0 {
		return nil, fmt.Errorf("sub-agent %q: missing 'request'", name)
	}
	if req, ok := payload["request"].(string); ok && len(payload) == 1 {
		return genai.NewContentFromText(req, genai.RoleUser), nil
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("sub-agent %q: encode arguments: %w", name, err)
	}
	return genai.NewContentFromText(string(b), genai.RoleUser), nil
}

// resolveSession returns the ADK session id and short handle to run on, and a
// note when the requested handle could not be honoured as given.
//
//   - "" starts a fresh session.
//   - a known short handle (or raw session id) of the caller's own session is
//     resumed; "last"/"latest" resumes the caller's most recent one.
//   - an unknown handle resumes the caller's most recent session with this
//     sub-agent, and says so. Models paraphrase opaque ids: in the 2026-11-07
//     smoke the coder passed resume_session "sentinel" for a UUID handle, the
//     reviewer silently restarted from scratch and re-read everything. The caller
//     meant "continue", so continuing its latest session is closer to the intent
//     than a fresh one.
//
// It marks the chosen session in-use (released by Run's defer) and bounds the
// map by TTL + cap.
func (t *resumableAgentTool) resolveSession(toolCtx adk.ToolContext, resume string) (id, alias, note string, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked()

	now := time.Now()
	parent := ""
	if toolCtx != nil {
		parent = realSessionID(toolCtx)
	}
	if resume != "" {
		target := ""
		switch strings.ToLower(resume) {
		case "last", "latest", "previous":
			target = t.latestLocked(parent)
		default:
			if aid, ok := t.aliases[resume]; ok {
				target = aid
			} else if _, ok := t.handles[resume]; ok {
				target = resume
			}
			if s := t.handles[target]; target != "" && (s == nil || s.parent != parent) {
				target = "" // not the caller's: treat as unknown
			}
			if target == "" {
				if latest := t.latestLocked(parent); latest != "" {
					target = latest
					note = fmt.Sprintf("resume_session %q is not a handle of yours; continued your latest session %s instead.", resume, t.handles[latest].alias)
				}
			}
		}
		if s := t.handles[target]; target != "" && s != nil {
			if s.inUse {
				return "", "", "", fmt.Errorf("sub-agent session %q is already running; wait for it to finish before resuming it", s.alias)
			}
			s.inUse = true
			s.lastUsed = now
			return target, s.alias, note, nil
		}
		note = fmt.Sprintf("resume_session %q: no earlier session to continue; started a fresh one.", resume)
	}

	stateMap := map[string]any{}
	for k, v := range toolCtx.State().All() {
		if !strings.HasPrefix(k, "_adk") {
			stateMap[k] = v
		}
	}
	resp, err := t.svc.Create(toolCtx, &session.CreateRequest{
		AppName: t.appName,
		UserID:  toolCtx.UserID(),
		State:   stateMap,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("resumable sub-agent %q: create session: %w", t.Name(), err)
	}
	id = resp.Session.ID()
	t.seq++
	alias = fmt.Sprintf("%s-%d", t.Name(), t.seq)
	t.handles[id] = &subSession{userID: toolCtx.UserID(), lastUsed: now, inUse: true, parent: parent, alias: alias}
	t.aliases[alias] = id
	t.evictLocked()
	return id, alias, note, nil
}

// latestLocked is the caller's most recently used idle session, "" if none.
func (t *resumableAgentTool) latestLocked(parent string) string {
	best := ""
	var bestAt time.Time
	for id, s := range t.handles {
		if s.parent == parent && !s.inUse && (best == "" || s.lastUsed.After(bestAt)) {
			best, bestAt = id, s.lastUsed
		}
	}
	return best
}

func (t *resumableAgentTool) release(handle string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.handles[handle]; ok {
		s.inUse = false
		s.lastUsed = time.Now()
	}
}

// sweepLocked drops idle sessions older than the TTL. Caller holds t.mu.
func (t *resumableAgentTool) sweepLocked() {
	if t.ttl <= 0 {
		return
	}
	cutoff := time.Now().Add(-t.ttl)
	for h, s := range t.handles {
		if !s.inUse && s.lastUsed.Before(cutoff) {
			t.dropLocked(h, s)
		}
	}
}

// evictLocked LRU-evicts the oldest idle sessions beyond the cap. Caller holds t.mu.
func (t *resumableAgentTool) evictLocked() {
	for len(t.handles) > t.cap {
		var oldestH string
		var oldest *subSession
		for h, s := range t.handles {
			if s.inUse {
				continue
			}
			if oldest == nil || s.lastUsed.Before(oldest.lastUsed) {
				oldestH, oldest = h, s
			}
		}
		if oldest == nil {
			return // everything is in use; nothing evictable
		}
		t.dropLocked(oldestH, oldest)
	}
}

func (t *resumableAgentTool) dropLocked(handle string, s *subSession) {
	_ = t.svc.Delete(context.Background(), &session.DeleteRequest{
		AppName:   t.appName,
		UserID:    s.userID,
		SessionID: handle,
	})
	delete(t.handles, handle)
	delete(t.aliases, s.alias)
}
