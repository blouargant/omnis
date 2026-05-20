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

	toolkitagent "github.com/blouargant/yoke/agent"
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

// a2aServer is the A2A protocol HTTP server.
type a2aServer struct {
	manager *toolkitagent.Manager
	token   string // optional Bearer token; empty = no auth
	mu      sync.RWMutex
	records map[string]*taskRecord
}

func newA2AServer(manager *toolkitagent.Manager, token string) *a2aServer {
	return &a2aServer{
		manager: manager,
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
		Name:    "Yoke Agent",
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

	ctx, cancel := context.WithCancel(r.Context())
	rec := s.newRecord(p.ID, p.SessionID, p.Message, cancel)

	rec.mu.Lock()
	rec.task.Status.State = a2aStateWorking
	rec.task.Status.Timestamp = nowRFC3339()
	rec.mu.Unlock()

	responseText, runErr := s.runAgent(ctx, rec.adkSessionID(), p.ID, a2aMessageText(p.Message))
	cancel()

	rec.mu.Lock()
	switch {
	case ctx.Err() != nil:
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
	rec.mu.Unlock()
	close(rec.doneCh)

	writeRPCOK(w, req.ID, taskCopy)
}

// ─── tasks/sendSubscribe (streaming SSE) ─────────────────────────────────────

func (s *a2aServer) tasksSendSubscribe(w http.ResponseWriter, r *http.Request, req rpcRequest) {
	var p taskSendParams
	if err := json.Unmarshal(req.Params, &p); err != nil || p.ID == "" {
		writeRPCErr(w, req.ID, -32602, "invalid params: id required")
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

	rec := s.newRecord(p.ID, p.SessionID, p.Message, cancel)

	emitSSE := func(event string, data any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flush()
	}

	emitSSE("task_status_update", a2aStatusEvent(p.ID, a2aStateWorking, nil, false))

	sq := s.manager.LookupSquad("", toolkitagent.DefaultSquadName)
	if sq == nil || sq.Runner == nil {
		msg := "agent not available"
		emitSSE("task_status_update", a2aStatusEvent(p.ID, a2aStateFailed, &msg, true))
		rec.mu.Lock()
		rec.task.Status.State = a2aStateFailed
		rec.mu.Unlock()
		close(rec.doneCh)
		return
	}

	seq := sq.Runner.Run(ctx, defaultUserID, rec.adkSessionID(),
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: a2aMessageText(p.Message)}}},
		adkagent.RunConfig{StreamingMode: adkagent.StreamingModeSSE})

	type adkEvt struct {
		ev  *adksession.Event
		err error
	}
	adkCh := make(chan adkEvt, 4)
	go func() {
		defer close(adkCh)
		seq(func(ev *adksession.Event, err error) bool {
			select {
			case adkCh <- adkEvt{ev, err}:
				return err == nil
			case <-ctx.Done():
				return false
			}
		})
	}()

	var respBuf strings.Builder
	var sawPartial bool
	artifactIdx := 0

	finalize := func(state a2aTaskState, errMsg *string) {
		rec.mu.Lock()
		rec.task.Status.State = state
		rec.task.Status.Timestamp = nowRFC3339()
		if state == a2aStateCompleted {
			result := respBuf.String()
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
	}

	for {
		select {
		case <-ctx.Done():
			finalize(a2aStateCanceled, nil)
			return

		case aev, ok := <-adkCh:
			if !ok {
				finalize(a2aStateCompleted, nil)
				return
			}
			if aev.err != nil {
				msg := aev.err.Error()
				finalize(a2aStateFailed, &msg)
				return
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
					respBuf.WriteString(part.Text)
					emitSSE("task_artifact_update", map[string]any{
						"id": p.ID,
						"artifact": a2aArtifact{
							Parts:  []a2aPart{{Type: "text", Text: part.Text}},
							Index:  artifactIdx,
							Append: artifactIdx > 0,
						},
					})
					artifactIdx++
					sawPartial = true
				} else if !sawPartial {
					// Non-streaming model: collect final text
					respBuf.WriteString(part.Text)
				}
			}
		}
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

func (s *a2aServer) runAgent(ctx context.Context, sessionID, taskID, prompt string) (string, error) {
	sq := s.manager.LookupSquad("", toolkitagent.DefaultSquadName)
	if sq == nil || sq.Runner == nil {
		return "", fmt.Errorf("agent not available")
	}

	seq := sq.Runner.Run(ctx, defaultUserID, sessionID,
		&genai.Content{Role: "user", Parts: []*genai.Part{{Text: prompt}}},
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
			return "", ctx.Err()
		case aev, ok := <-ch:
			if !ok {
				return buf.String(), nil
			}
			if aev.err != nil {
				return "", aev.err
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
				} else if !sawPartial {
					buf.WriteString(part.Text)
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
