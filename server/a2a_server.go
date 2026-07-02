package main

// A2A protocol server — runs on a separate port alongside the web server.
// Implements the Agent-to-Agent (A2A) JSON-RPC 2.0 protocol:
//   GET  /.well-known/agent.json  — Agent Card
//   POST /                        — JSON-RPC: tasks/send, tasks/sendSubscribe,
//                                             tasks/get, tasks/cancel

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	adkagent "google.golang.org/adk/agent"
	adksession "google.golang.org/adk/session"
	"google.golang.org/genai"

	toolkitagent "github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/sessions"
)

// ─── A2A protocol types ───────────────────────────────────────────────────────

type a2aAgentCard struct {
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	URL                string          `json:"url"`
	Version            string          `json:"version"`
	Capabilities       a2aCapabilities `json:"capabilities"`
	DefaultInputModes  []string        `json:"defaultInputModes"`
	DefaultOutputModes []string        `json:"defaultOutputModes"`
}

type a2aCapabilities struct {
	Streaming              bool `json:"streaming"`
	PushNotifications      bool `json:"pushNotifications"`
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

type a2aTaskState string

const (
	a2aStateSubmitted a2aTaskState = "submitted"
	a2aStateWorking   a2aTaskState = "working"
	a2aStateCompleted a2aTaskState = "completed"
	a2aStateCanceled  a2aTaskState = "canceled"
	a2aStateFailed    a2aTaskState = "failed"
)

type a2aTaskStatus struct {
	State     a2aTaskState `json:"state"`
	Message   *a2aMessage  `json:"message,omitempty"`
	Timestamp string       `json:"timestamp"`
}

type a2aMessage struct {
	Role  string    `json:"role"`
	Parts []a2aPart `json:"parts"`
}

type a2aPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type a2aArtifact struct {
	Name      string    `json:"name,omitempty"`
	Parts     []a2aPart `json:"parts"`
	Index     int       `json:"index"`
	Append    bool      `json:"append,omitempty"`
	LastChunk bool      `json:"lastChunk,omitempty"`
}

type a2aTask struct {
	ID        string        `json:"id"`
	SessionID string        `json:"sessionId,omitempty"`
	Status    a2aTaskStatus `json:"status"`
	Artifacts []a2aArtifact `json:"artifacts,omitempty"`
	History   []a2aMessage  `json:"history,omitempty"`
}

// ─── JSON-RPC 2.0 types ──────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
	ID      json.RawMessage `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type taskSendParams struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionId,omitempty"`
	Message   a2aMessage     `json:"message"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type taskGetParams struct {
	ID string `json:"id"`
}

type taskCancelParams struct {
	ID string `json:"id"`
}

// ─── Server ──────────────────────────────────────────────────────────────────

// taskRecord holds in-flight and completed task state.
type taskRecord struct {
	mu     sync.Mutex
	task   a2aTask
	cancel context.CancelFunc
	doneCh chan struct{}
}

// a2aDeps bundles the optional plumbing the A2A server uses when a request
// targets a real web UI session (vs. an ephemeral one). All fields are
// optional: when omitted, session-name and auto-create paths simply degrade
// to clear -32602 errors.
type a2aDeps struct {
	Manager         *toolkitagent.Manager
	Registry        *sessions.Registry
	RunGuard        *sessionRunGuard
	PushEvents      *sessionPushBroadcaster
	PushMgr         *pushManager
	RegisterSession func(userID, sessionID, displayName string) error
	RootCtx         context.Context // used to start pushMgr.Watch for auto-created sessions
}

// a2aServer is the A2A protocol HTTP server.
type a2aServer struct {
	deps    a2aDeps
	token   string // optional Bearer token; empty = no auth
	mu      sync.RWMutex
	records map[string]*taskRecord
}

func newA2AServer(deps a2aDeps, token string) *a2aServer {
	return &a2aServer{
		deps:    deps,
		token:   token,
		records: make(map[string]*taskRecord),
	}
}

// serve starts listening on addr and blocks until ctx is cancelled.
func (s *a2aServer) serve(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/agent.json", s.handleAgentCard)
	mux.HandleFunc("POST /", s.handleRPC)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Printf("a2a: listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// authOK returns true when the request carries a valid token (or no token is required).
func (s *a2aServer) authOK(r *http.Request) bool {
	if s.token == "" {
		return true
	}
	hdr := r.Header.Get("Authorization")
	return strings.TrimPrefix(hdr, "Bearer ") == s.token
}

func (s *a2aServer) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	card := a2aAgentCard{
		Name:    "Omnis Agent",
		URL:     fmt.Sprintf("%s://%s/", scheme, r.Host),
		Version: "1.0.0",
		Capabilities: a2aCapabilities{
			Streaming:              true,
			PushNotifications:      false,
			StateTransitionHistory: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(card)
}

func (s *a2aServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	if !s.authOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCErr(w, nil, -32700, "parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCErr(w, req.ID, -32600, "invalid request")
		return
	}

	switch req.Method {
	case "tasks/send":
		s.tasksSend(w, r, req)
	case "tasks/sendSubscribe":
		s.tasksSendSubscribe(w, r, req)
	case "tasks/get":
		s.tasksGet(w, r, req)
	case "tasks/cancel":
		s.tasksCancel(w, r, req)
	default:
		writeRPCErr(w, req.ID, -32601, "method not found")
	}
}

// ─── tasks/send (synchronous) ────────────────────────────────────────────────

func (s *a2aServer) tasksSend(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p taskSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		writeRPCErr(w, req.ID, -32602, "invalid params: id required")
		return
	}
	routing, err := s.resolveRouting(p.Metadata, p.ID)
	if err != nil {
		writeRPCErr(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	parentCtx := r.Context()
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	// When targeting a real session, the raw sessionId from the client is
	// ignored — registry meta is authoritative.
	rec := s.newRecord(p.ID, routing.SessionID, p.Message, cancel)

	// Serialise with any other turn (web UI or A2A) running on this session.
	if routing.Persistent && s.deps.RunGuard != nil {
		release := s.deps.RunGuard.acquire(routing.SessionID)
		defer release()
		// Apply any pending hot-reload to this session before we start the
		// turn (we hold the run-guard, so no other turn is mid-flight).
		if s.deps.Manager != nil {
			s.deps.Manager.MigrateToCurrent(routing.SessionID)
		}
	}

	rec.mu.Lock()
	rec.task.Status.State = a2aStateWorking
	rec.task.Status.Timestamp = nowRFC3339()
	rec.mu.Unlock()

	promptText := a2aMessageText(p.Message)
	responseText, runErr := s.runRouted(ctx, routing, promptText, nil)

	rec.mu.Lock()
	switch {
	case parentCtx.Err() != nil:
		rec.task.Status.State = a2aStateCanceled
	case runErr != nil:
		rec.task.Status.State = a2aStateFailed
		errMsg := runErr.Error()
		rec.task.Status.Message = &a2aMessage{
			Role:  "agent",
			Parts: []a2aPart{{Type: "text", Text: errMsg}},
		}
	default:
		rec.task.Status.State = a2aStateCompleted
		rec.task.Artifacts = []a2aArtifact{{
			Parts:     []a2aPart{{Type: "text", Text: responseText}},
			Index:     0,
			LastChunk: true,
		}}
		rec.task.History = append(rec.task.History, a2aMessage{
			Role:  "agent",
			Parts: []a2aPart{{Type: "text", Text: responseText}},
		})
	}
	rec.task.Status.Timestamp = nowRFC3339()
	taskCopy := rec.task
	finalState := rec.task.Status.State
	rec.mu.Unlock()
	close(rec.doneCh)

	if routing.Persistent && finalState == a2aStateCompleted {
		s.persistA2ATurn(routing, promptText, responseText)
	}

	writeRPCOK(w, req.ID, taskCopy)
}

// persistA2ATurn appends the turn to the conversation file, bumps the
// session's LastUsedAt, and pushes a mailbox_push SSE event so any open
// web UI tab on that session reloads its history live. Errors are logged
// but never fail the call: persistence is a side effect of the turn, not
// its purpose.
func (s *a2aServer) persistA2ATurn(routing *sessionRouting, prompt, response string) {
	if s.deps.Registry != nil {
		s.deps.Registry.Touch(routing.SessionID)
	}
	if err := sessions.AppendConversationTurn(routing.SessionID, prompt, response); err != nil {
		log.Printf("a2a: persist turn for session %q: %v", routing.SessionID, err)
	}
	if s.deps.PushEvents != nil {
		s.deps.PushEvents.notify(routing.SessionID)
	}
}

// autoCreateSession materialises a new web UI session under the
// caller-supplied name, mirroring what POST /api/sessions does: register
// the name in the registry, persist the squad to the conversation file,
// register the mailbox display name, pin the session to the current
// generation, and start mailbox watching. Returns a *routingError when
// the request can't be honoured.
func (s *a2aServer) autoCreateSession(name, squad string) (*sessions.SessionMeta, *routingError) {
	if s.deps.Registry == nil {
		return nil, &routingError{"session_name routing not available on this server"}
	}
	chosenSquad := squad
	if chosenSquad == "" {
		chosenSquad = s.a2aDefaultSquad()
	}
	if s.deps.Manager != nil && !s.deps.Manager.HasSquad(chosenSquad) {
		return nil, &routingError{fmt.Sprintf("unknown squad %q", chosenSquad)}
	}
	sm, ok := s.deps.Registry.NewWithName(name, chosenSquad)
	if !ok {
		// Either the name collides (a concurrent caller created it) or it
		// fails sanitisation. Recheck collision so the second caller gets a
		// useful error rather than a generic "invalid name".
		if existing, found := s.deps.Registry.Get(name); found {
			return existing, nil
		}
		return nil, &routingError{fmt.Sprintf("invalid session name %q", name)}
	}
	if err := sessions.SetConversationSquad(sm.ID, chosenSquad); err != nil {
		log.Printf("a2a: persist squad for new session %q: %v", sm.ID, err)
	}
	if s.deps.RegisterSession != nil {
		_ = s.deps.RegisterSession(sm.UserID, sm.ID, sm.ID)
	}
	if s.deps.Manager != nil {
		s.deps.Manager.Pin(sm.ID)
	}
	if s.deps.PushMgr != nil && s.deps.RootCtx != nil {
		s.deps.PushMgr.Watch(s.deps.RootCtx, serverDeps{
			Manager:      s.deps.Manager,
			Registry:     s.deps.Registry,
			RunGuard:     s.deps.RunGuard,
			PushEvents:   s.deps.PushEvents,
			WatchMailbox: nil,
		}, sm.ID, sm.UserID)
	}
	return sm, nil
}

// ─── tasks/sendSubscribe (streaming SSE) ─────────────────────────────────────

func (s *a2aServer) tasksSendSubscribe(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p taskSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		writeRPCErr(w, req.ID, -32602, "invalid params: id required")
		return
	}
	routing, err := s.resolveRouting(p.Metadata, p.ID)
	if err != nil {
		writeRPCErr(w, req.ID, -32602, "invalid params: "+err.Error())
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	rec := s.newRecord(p.ID, routing.SessionID, p.Message, cancel)

	if routing.Persistent && s.deps.RunGuard != nil {
		release := s.deps.RunGuard.acquire(routing.SessionID)
		defer release()
		if s.deps.Manager != nil {
			s.deps.Manager.MigrateToCurrent(routing.SessionID)
		}
	}

	emitSSE := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flush()
	}

	emitSSE("task_status_update", a2aStatusEvent(p.ID, a2aStateWorking, nil, false))

	promptText := a2aMessageText(p.Message)
	artifactIdx := 0
	// Stream each answering-hop partial to the caller as an artifact delta; the
	// final/full artifact is emitted by finalize once the turn completes.
	onPart := func(text string, partial bool) {
		if !partial {
			return
		}
		emitSSE("task_artifact_update", map[string]any{
			"id": p.ID,
			"artifact": a2aArtifact{
				Parts:  []a2aPart{{Type: "text", Text: text}},
				Index:  artifactIdx,
				Append: artifactIdx > 0,
			},
		})
		artifactIdx++
	}

	// Route the inbound message through the Omnis dispatch loop (starting at the
	// resolved squad — the router for a fresh/unspecified target) and collect the
	// answering squad's reply, which becomes the final artifact.
	result, runErr := s.runRouted(ctx, routing, promptText, onPart)

	finalize := func(state a2aTaskState, errMsg *string) {
		rec.mu.Lock()
		rec.task.Status.State = state
		rec.task.Status.Timestamp = nowRFC3339()
		if state == a2aStateCompleted {
			rec.task.Artifacts = []a2aArtifact{{
				Parts:     []a2aPart{{Type: "text", Text: result}},
				Index:     0,
				LastChunk: true,
			}}
			rec.task.History = append(rec.task.History, a2aMessage{
				Role:  "agent",
				Parts: []a2aPart{{Type: "text", Text: result}},
			})
		}
		rec.mu.Unlock()
		emitSSE("task_status_update", a2aStatusEvent(p.ID, state, errMsg, true))
		close(rec.doneCh)
		if routing.Persistent && state == a2aStateCompleted {
			s.persistA2ATurn(routing, promptText, result)
		}
	}

	switch {
	case ctx.Err() != nil:
		finalize(a2aStateCanceled, nil)
	case runErr != nil:
		msg := runErr.Error()
		finalize(a2aStateFailed, &msg)
	default:
		finalize(a2aStateCompleted, nil)
	}
}

// ─── tasks/get ────────────────────────────────────────────────────────────────

func (s *a2aServer) tasksGet(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	if !s.authOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p taskGetParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		writeRPCErr(w, req.ID, -32602, "invalid params: id required")
		return
	}
	rec := s.getRecord(p.ID)
	if rec == nil {
		writeRPCErr(w, req.ID, -32001, "task not found")
		return
	}
	rec.mu.Lock()
	taskCopy := rec.task
	rec.mu.Unlock()
	writeRPCOK(w, req.ID, taskCopy)
}

// ─── tasks/cancel ─────────────────────────────────────────────────────────────

func (s *a2aServer) tasksCancel(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	if !s.authOK(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var p taskCancelParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		writeRPCErr(w, req.ID, -32602, "invalid params: id required")
		return
	}
	rec := s.getRecord(p.ID)
	if rec == nil {
		writeRPCErr(w, req.ID, -32001, "task not found")
		return
	}
	rec.cancel()
	select {
	case <-rec.doneCh:
	case <-time.After(5 * time.Second):
	}
	rec.mu.Lock()
	taskCopy := rec.task
	rec.mu.Unlock()
	writeRPCOK(w, req.ID, taskCopy)
}

// ─── agent runner ─────────────────────────────────────────────────────────────

// runRouted drives one inbound A2A turn through the Omnis routing dispatch loop,
// starting at routing.Squad. A freshly-created / router-pinned session has the
// router route the message to the proper squad; an already-routed session runs
// its pinned squad directly (one hop — byte-identical to the old direct run when
// routing is disabled). The answering squad's text is returned as the A2A reply
// (the RPC response / final artifact); onPart streams the answering hop's parts
// so a subscribe caller can emit artifacts (the router hop's chatter is never
// streamed). For a persistent session the routed squad is pinned onto it (so the
// sender's follow-ups continue there and it survives a restart); an ephemeral
// task's transient pin (Lookup auto-pins the sessionID that keys the routing
// directive) is released after the turn so it never leaks a generation refcount.
func (s *a2aServer) runRouted(ctx context.Context, routing *sessionRouting, prompt string, onPart func(text string, partial bool)) (string, error) {
	if s.deps.Manager == nil {
		return "", fmt.Errorf("agent not available")
	}
	if !routing.Persistent {
		defer s.deps.Manager.Release(routing.SessionID)
	}
	routerSquad := s.deps.Manager.RouterSquad()
	parts := []*genai.Part{{Text: prompt}}

	run := func(rctx context.Context, sq *toolkitagent.SquadInstance, squadName string, hopParts []*genai.Part) (string, error) {
		isRouter := routerSquad != "" && squadName == routerSquad
		var emit func(string, bool)
		if !isRouter {
			emit = onPart // only the answering hop streams to the caller
		}
		text, err := s.consumeHop(rctx, sq, routing.UserID, routing.SessionID, hopParts, emit)
		if err != nil {
			return text, err
		}
		if isRouter && s.deps.Manager.PendingRoute(routing.SessionID) {
			return "", nil // routed → drop the router's chatter
		}
		return text, nil
	}
	notify := func(from, to, reason string) {
		if !routing.Persistent {
			return // ephemeral task: nothing to pin
		}
		if s.deps.Registry != nil {
			s.deps.Registry.SetSquad(routing.SessionID, to)
		}
		_ = sessions.SetConversationSquad(routing.SessionID, to)
	}

	// A2A prompts are plain text (no attachments / reply directives), so the
	// router and answering views are identical — pass nil routerParts.
	_, text, err := s.deps.Manager.RunWithRouting(
		ctx, routing.UserID, routing.SessionID, routing.Squad, parts, nil, run, notify)
	return text, err
}

// consumeHop runs ONE squad hop under (userID, sessionID) and returns its final
// assistant text. onPart, when non-nil, is invoked for each text part (streaming
// deltas with partial=true, or the whole text with partial=false for a
// non-streaming model) so a subscribe caller can emit A2A artifacts as they
// arrive; the non-partial duplicate a streamed turn also emits at the end is
// skipped via sawPartial. This is the per-hop runner RunWithRouting calls.
func (s *a2aServer) consumeHop(ctx context.Context, sq *toolkitagent.SquadInstance, userID, sessionID string, parts []*genai.Part, onPart func(text string, partial bool)) (string, error) {
	seq := sq.Runner.Run(ctx, userID, sessionID,
		&genai.Content{Role: "user", Parts: parts},
		adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE})

	type adkEvt struct {
		ev  *adksession.Event
		err error
	}
	ch := make(chan adkEvt, 4)
	go func() {
		defer close(ch)
		seq(func(ev *adksession.Event, err error) bool {
			select {
			case ch <- adkEvt{ev, err}:
				return err == nil
			case <-ctx.Done():
				return false
			}
		})
	}()

	var buf strings.Builder
	var sawPartial bool
	for {
		select {
		case <-ctx.Done():
			return buf.String(), ctx.Err()
		case aev, ok := <-ch:
			if !ok {
				return buf.String(), nil
			}
			if aev.err != nil {
				return buf.String(), aev.err
			}
			if aev.ev == nil || aev.ev.Content == nil {
				continue
			}
			isPartial := aev.ev.LLMResponse.Partial
			for _, part := range aev.ev.Content.Parts {
				if part == nil || part.Text == "" {
					continue
				}
				if isPartial {
					buf.WriteString(part.Text)
					sawPartial = true
					if onPart != nil {
						onPart(part.Text, true)
					}
				} else if !sawPartial {
					buf.WriteString(part.Text)
					if onPart != nil {
						onPart(part.Text, false)
					}
				}
				// skip non-partial when sawPartial (duplicate of streamed content)
			}
		}
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (s *a2aServer) newRecord(id, sessionID string, msg a2aMessage, cancel context.CancelFunc) *taskRecord {
	rec := &taskRecord{
		cancel: cancel,
		doneCh: make(chan struct{}),
		task: a2aTask{
			ID:        id,
			SessionID: sessionID,
			Status: a2aTaskStatus{
				State:     a2aStateSubmitted,
				Timestamp: nowRFC3339(),
			},
			History: []a2aMessage{msg},
		},
	}
	s.mu.Lock()
	s.records[id] = rec
	s.mu.Unlock()
	return rec
}

// adkSessionID returns the session ID to pass to the ADK runner.
// Uses the provided sessionId when set, otherwise falls back to the task ID
// so each task gets its own isolated conversation history.
func (r *taskRecord) adkSessionID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.SessionID != "" {
		return r.task.SessionID
	}
	return r.task.ID
}

func (s *a2aServer) getRecord(id string) *taskRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.records[id]
}

// metaBool pulls a bool-valued key out of the JSON-RPC metadata map.
// JSON numbers and strings are coerced: 1 / "true" / "1" / "yes" → true.
func metaBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}
	switch v := meta[key].(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y":
			return true
		}
	case float64:
		return v != 0
	}
	return false
}

// metaString pulls a string-valued key out of the JSON-RPC metadata map.
// Returns "" when missing, not-a-string, or whitespace-only.
func metaString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}
	v, ok := meta[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// sessionRouting captures the resolved target for one A2A call: which squad
// to invoke, which ADK session/user IDs to address, and whether the session
// is a real persisted web UI session (vs. an ephemeral A2A-only one).
type sessionRouting struct {
	Squad      string
	UserID     string
	SessionID  string
	Meta       *sessions.SessionMeta // non-nil when targeting a registered session
	Persistent bool                  // true when the call should persist + lock
}

// routingError is a typed rejection that the caller maps to a -32602.
type routingError struct{ msg string }

func (e *routingError) Error() string { return e.msg }

// a2aDefaultSquad is the squad an inbound A2A call lands on when it names none:
// the Omnis router (so the message is routed to the proper squad, exactly like a
// new web chat) when routing is enabled, else the default team. Manager==nil
// (unit tests) falls back to the default team. Applied only to the ephemeral and
// auto-create (genuinely new) paths — an existing session already carries its
// own pinned squad, which the router pins onto the session on its first route.
func (s *a2aServer) a2aDefaultSquad() string {
	if s.deps.Manager != nil {
		if rs := s.deps.Manager.RouterSquad(); rs != "" {
			return rs
		}
	}
	return toolkitagent.DefaultSquadName
}

// resolveRouting picks squad + session for one A2A call. Empty / missing
// session_name → ephemeral session (task ID + defaultUserID + chosen squad).
// Non-empty session_name → lookup in the registry; conflict on squad
// rejected. Returns *routingError when the caller's request can't be
// satisfied without misleading them.
func (s *a2aServer) resolveRouting(meta map[string]any, taskID string) (*sessionRouting, error) {
	wantSquad := metaString(meta, "squad")
	wantSession := metaString(meta, "session_name")

	// Ephemeral path: no named session.
	if wantSession == "" {
		squad := wantSquad
		if squad == "" {
			squad = s.a2aDefaultSquad()
		} else if s.deps.Manager != nil && !s.deps.Manager.HasSquad(squad) {
			return nil, &routingError{fmt.Sprintf("unknown squad %q", squad)}
		}
		return &sessionRouting{
			Squad:     squad,
			UserID:    sessions.DefaultUserID,
			SessionID: taskID,
		}, nil
	}

	// Persistent path: look up the session in the web UI registry.
	if s.deps.Registry == nil {
		return nil, &routingError{"session_name routing not available on this server"}
	}
	sm, ok := s.deps.Registry.Get(wantSession)
	if !ok {
		// Auto-create when the caller opted in. The squad picked here is
		// pinned to the new session for life.
		if metaBool(meta, "create") {
			created, cerr := s.autoCreateSession(wantSession, wantSquad)
			if cerr != nil {
				return nil, cerr
			}
			return &sessionRouting{
				Squad:      created.Squad,
				UserID:     created.UserID,
				SessionID:  created.ID,
				Meta:       created,
				Persistent: true,
			}, nil
		}
		return nil, &routingError{fmt.Sprintf("unknown session %q", wantSession)}
	}
	squad := sm.Squad
	if squad == "" {
		squad = toolkitagent.DefaultSquadName
	}
	// A caller-supplied squad must match the session's pinned squad. Silently
	// switching to the session's squad would mislead them; silently honouring
	// the caller would split-brain the session.
	if wantSquad != "" && !strings.EqualFold(wantSquad, squad) {
		return nil, &routingError{fmt.Sprintf(
			"session %q is pinned to squad %q; cannot retarget to %q",
			wantSession, squad, wantSquad)}
	}
	return &sessionRouting{
		Squad:      squad,
		UserID:     sm.UserID,
		SessionID:  sm.ID,
		Meta:       sm,
		Persistent: true,
	}, nil
}

func a2aMessageText(msg a2aMessage) string {
	var sb strings.Builder
	for _, p := range msg.Parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func a2aStatusEvent(taskID string, state a2aTaskState, errMsg *string, final bool) map[string]any {
	status := map[string]any{
		"state":     state,
		"timestamp": nowRFC3339(),
	}
	if errMsg != nil {
		status["message"] = a2aMessage{
			Role:  "agent",
			Parts: []a2aPart{{Type: "text", Text: *errMsg}},
		}
	}
	return map[string]any{
		"id":     taskID,
		"status": status,
		"final":  final,
	}
}

func writeRPCOK(w http.ResponseWriter, id json.RawMessage, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{JSONRPC: "2.0", Result: result, ID: id})
}

func writeRPCErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResponse{
		JSONRPC: "2.0",
		Error:   &rpcError{Code: code, Message: msg},
		ID:      id,
	})
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339)
}
