// Package askuser implements a blocking per-session question registry that
// lets the agent ask the user a structured question and wait for the answer.
//
// Design:
//   - The LLM calls the ask_user tool which calls Registry.Ask; the call
//     blocks until a surface (web UI, TUI, or console) calls Registry.Resolve
//     with the user's answer.
//   - Every pending question is keyed by (sessionID, questionID) so multiple
//     concurrent sessions never cross-contaminate each other.
//   - Surfaces that connect after a question is already pending can call
//     Registry.Pending to replay unanswered questions.
package askuser

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Kind is the type of question presented to the user.
type Kind string

const (
	KindSingle  Kind = "single"  // choose exactly one from 2-4 choices
	KindMulti   Kind = "multi"   // choose one or more from a list
	KindText    Kind = "text"    // free-text answer
	KindConfirm Kind = "confirm" // yes / no (2 choices, safest first)
)

// Question holds everything a UI surface needs to render the prompt.
type Question struct {
	ID          string   `json:"question_id"`
	SessionID   string   `json:"session_id"`
	Kind        Kind     `json:"kind"`
	Prompt      string   `json:"prompt"`
	Choices     []string `json:"choices,omitempty"`
	AllowText   bool     `json:"allow_text,omitempty"`   // for single/multi: also accept free text
	Default     string   `json:"default,omitempty"`      // suggested default value / choice
	TimeoutSecs int      `json:"timeout_secs,omitempty"` // 0 → use registry default
	// Password, when true, hints to surfaces that they should render
	// the input as a masked field (e.g. <input type="password"> in the
	// web UI). Only meaningful for KindText.
	Password bool `json:"password,omitempty"`
	// Group, when non-empty, marks this question as part of a coalescible
	// group (e.g. a burst of install-permission prompts fired in one turn).
	// A surface MAY render every pending question sharing a (session, group)
	// as a single combined widget. This is purely a display hint — each
	// question is still registered and resolved independently by its own ID.
	Group string `json:"group,omitempty"`
	// Item carries structured metadata about the subject of the question
	// (e.g. what is about to be installed), so a grouped widget can show a
	// tidy "what will be installed" list instead of raw JSON args. Optional.
	Item *QuestionItem `json:"item,omitempty"`
	// Durable marks a question that must survive a server restart (see
	// Persister). Only AskUserQuestion sets it; permission and other technical
	// cards gate one tool call that does not exist after a restart.
	Durable bool `json:"durable,omitempty"`
	// Agent is the agent that asked the question (used to resume after a restart).
	Agent string `json:"agent,omitempty"`
	// Resumed is set by Restore: this question was asked before a server
	// restart and its answer resumes the task in a new turn.
	Resumed bool `json:"resumed,omitempty"`
}

// QuestionItem describes the subject of a question (typically an install
// permission prompt) so a UI surface can render a friendly summary grouped by
// kind rather than dumping the raw tool arguments.
type QuestionItem struct {
	Kind   string `json:"kind,omitempty"`   // e.g. "skill", "agent", "mcp"
	Name   string `json:"name,omitempty"`   // human-friendly item name
	Source string `json:"source,omitempty"` // origin (e.g. registry id/name)
}

// Answer is the user's response to a question.
type Answer struct {
	Selected  []string `json:"selected,omitempty"`  // for single/multi/confirm
	Text      string   `json:"text,omitempty"`      // for text or allow_text
	Cancelled bool     `json:"cancelled,omitempty"` // user dismissed / timed out
}

// ErrUnknownQuestion is returned by Resolve when the question_id is not found.
var ErrUnknownQuestion = errors.New("askuser: unknown question_id")

// ErrAlreadyResolved is returned by Resolve when the question was already answered.
var ErrAlreadyResolved = errors.New("askuser: question already resolved")

// Persister stores durable questions so they survive a restart. Only
// questions with Durable=true are passed to it. Implementations must be safe
// for concurrent use and must not block for long.
type Persister interface {
	// Save is called once a durable question is pending.
	Save(q Question)
	// Remove is called when a live durable question is resolved: answered is
	// true for a real answer, false for a cancellation or timeout.
	Remove(q Question, answered bool)
}

// DefaultTimeout is the registry's default per-question wait when a question
// sets no TimeoutSecs. It is 0 — an unanswered ask-user / permission card waits
// indefinitely rather than being auto-denied on a timer: denying an action the
// task needs is worse than waiting for the user to come back. The wait still
// ends on context cancellation (turn abort / Stop / shutdown), and a caller can
// re-arm a bounded wait per-question (Question.TimeoutSecs) or per-registry
// (WithDefaultTimeout) when a genuine deadline is wanted.
const DefaultTimeout = 0

// pending holds a question that is waiting for an answer.
type pending struct {
	q    Question
	ch   chan Answer // buffer 1; closed on resolution
	once sync.Once   // ensures ch is closed exactly once
	// orphan marks a question restored after a restart: no tool call waits on
	// ch, and its storage is owned by the caller of Restore, so the persister
	// is never called for it. onAnswer (read/written under Registry.mu) is its
	// completion callback; CancelSession clears it so an ending session is
	// never resumed.
	orphan   bool
	onAnswer func(Question, Answer)
}

// Registry is a per-session-scoped concurrent map from questionID → pending.
// A single Registry instance must be shared across all sessions; it is safe
// for concurrent use by multiple goroutines.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]map[string]*pending // [sessionID][questionID]

	// notifyFn is called whenever a new question is registered. It is used
	// by the server/TUI to emit the question to the correct surface.
	// It receives the question and must not block.
	notifyFn func(q Question)
	// cancelFn is called when a question is resolved (by Resolve or timeout)
	// so the surface can dismiss the widget. Receives the resolved Question.
	cancelFn func(q Question)

	// persister stores durable questions so they survive a restart. Set via
	// SetPersister; nil by default, in which case durability is a no-op.
	persister Persister

	defaultTimeout time.Duration
}

// RegistryOption configures a Registry.
type RegistryOption func(*Registry)

// WithNotify sets a callback invoked (non-blocking) when a new question
// is registered. Used to push the question to the UI surface.
func WithNotify(fn func(q Question)) RegistryOption {
	return func(r *Registry) { r.notifyFn = fn }
}

// WithCancel sets a callback invoked (non-blocking) when a question is
// resolved (answered or cancelled). Used to dismiss the UI widget.
func WithCancel(fn func(q Question)) RegistryOption {
	return func(r *Registry) { r.cancelFn = fn }
}

// WithDefaultTimeout overrides the built-in 5-minute question timeout.
func WithDefaultTimeout(d time.Duration) RegistryOption {
	return func(r *Registry) { r.defaultTimeout = d }
}

// NewRegistry creates an empty Registry.
func NewRegistry(opts ...RegistryOption) *Registry {
	r := &Registry{
		sessions:       map[string]map[string]*pending{},
		defaultTimeout: DefaultTimeout,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Ask registers a question for sessionID, calls notifyFn if set, then
// blocks until the user answers (via Resolve), the ctx is cancelled, or
// the question's timeout elapses.
//
// The returned Answer has Cancelled=true if the call timed out or the
// context was cancelled rather than an explicit user response.
func (r *Registry) Ask(ctx context.Context, sessionID string, q Question) (Answer, error) {
	if q.ID == "" {
		q.ID = uuid.NewString()
	}
	q.SessionID = sessionID

	p := &pending{
		q:  q,
		ch: make(chan Answer, 1),
	}
	r.mu.Lock()
	if r.sessions[sessionID] == nil {
		r.sessions[sessionID] = map[string]*pending{}
	}
	r.sessions[sessionID][q.ID] = p
	r.mu.Unlock()

	if r.notifyFn != nil {
		r.notifyFn(q)
	}

	if q.Durable {
		if pr := r.getPersister(); pr != nil {
			pr.Save(q)
		}
	}

	timeout := r.defaultTimeout
	if q.TimeoutSecs > 0 {
		timeout = time.Duration(q.TimeoutSecs) * time.Second
	}

	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}

	var ans Answer
	select {
	case ans = <-p.ch:
	case <-timer:
		ans = Answer{Cancelled: true}
		r.resolveInternal(sessionID, q.ID, ans, p)
	case <-ctx.Done():
		ans = Answer{Cancelled: true}
		r.resolveInternal(sessionID, q.ID, ans, p)
	}
	return ans, nil
}

// Resolve provides the user's answer for a pending question. Returns
// ErrUnknownQuestion if the question_id is not known, ErrAlreadyResolved if
// it was already answered.
//
// If sessionID has no record of questionID, Resolve falls back to a
// global lookup by questionID across every session. This handles
// questions whose owning session does not match the UI session that
// surfaces them — notably MCP input prompts, which are registered from
// inside a sub-agent runner (or at pool-acquisition time) with a
// session context that does not carry the user-facing session ID, yet
// must be answerable from whichever active session the user is in.
// Question IDs are UUIDs so the fallback cannot mis-route.
func (r *Registry) Resolve(sessionID, questionID string, ans Answer) error {
	r.mu.Lock()
	if sm := r.sessions[sessionID]; sm != nil {
		if p, ok := sm[questionID]; ok {
			r.mu.Unlock()
			return r.resolveInternal(sessionID, questionID, ans, p)
		}
	}
	// Cross-session fallback: the question was registered under a
	// different session id than the UI's. Locate it by UUID.
	for sid, sm := range r.sessions {
		if p, ok := sm[questionID]; ok {
			r.mu.Unlock()
			return r.resolveInternal(sid, questionID, ans, p)
		}
	}
	r.mu.Unlock()
	return fmt.Errorf("%w: %q", ErrUnknownQuestion, questionID)
}

func (r *Registry) resolveInternal(sessionID, questionID string, ans Answer, p *pending) error {
	resolved := false
	p.once.Do(func() {
		resolved = true
		p.ch <- ans
		close(p.ch)
		r.mu.Lock()
		if r.sessions[sessionID] != nil {
			delete(r.sessions[sessionID], questionID)
		}
		cancelFn := r.cancelFn
		persister := r.persister
		onAnswer := p.onAnswer
		r.mu.Unlock()
		if cancelFn != nil {
			cancelFn(p.q)
		}
		switch {
		case p.orphan:
			if onAnswer != nil {
				onAnswer(p.q, ans)
			}
		case p.q.Durable && persister != nil:
			persister.Remove(p.q, !ans.Cancelled)
		}
	})
	if !resolved {
		return ErrAlreadyResolved
	}
	return nil
}

// Pending returns a snapshot of all unanswered questions for a session,
// in registration order (undefined for concurrent questions). Used by
// surfaces that reconnect after a question was already emitted.
func (r *Registry) Pending(sessionID string) []Question {
	r.mu.Lock()
	defer r.mu.Unlock()
	sm := r.sessions[sessionID]
	if len(sm) == 0 {
		return nil
	}
	out := make([]Question, 0, len(sm))
	for _, p := range sm {
		out = append(out, p.q)
	}
	return out
}

// SetNotify replaces the notification callback. Thread-safe; intended for
// use when a surface (e.g. TUI) attaches after the registry is created.
func (r *Registry) SetNotify(fn func(q Question)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notifyFn = fn
}

// SetCancel replaces the cancel callback. Thread-safe.
func (r *Registry) SetCancel(fn func(q Question)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelFn = fn
}

// SetPersister installs the durable-question store. Thread-safe.
func (r *Registry) SetPersister(p Persister) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persister = p
}

func (r *Registry) getPersister() Persister {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.persister
}

// Restore re-registers a question persisted before a restart. No tool call
// waits for it: when it is answered, onAnswer runs instead, and the persister
// is not called (the caller owns the stored entry). The question is marked
// Resumed and announced through notifyFn like a new question.
func (r *Registry) Restore(q Question, onAnswer func(Question, Answer)) {
	q.Resumed = true
	p := &pending{q: q, ch: make(chan Answer, 1), orphan: true, onAnswer: onAnswer}
	r.mu.Lock()
	if r.sessions[q.SessionID] == nil {
		r.sessions[q.SessionID] = map[string]*pending{}
	}
	r.sessions[q.SessionID][q.ID] = p
	notify := r.notifyFn
	r.mu.Unlock()
	if notify != nil {
		notify(q)
	}
}

// CancelSession ends every pending question of a session (archive, delete).
// A live question is resolved as cancelled; a restored orphan is dropped
// without calling its onAnswer, since the session is going away.
func (r *Registry) CancelSession(sessionID string) {
	r.mu.Lock()
	var ps []*pending
	for _, p := range r.sessions[sessionID] {
		p.onAnswer = nil // never resume a session that is ending
		ps = append(ps, p)
	}
	r.mu.Unlock()
	for _, p := range ps {
		_ = r.resolveInternal(sessionID, p.q.ID, Answer{Cancelled: true}, p)
	}
}

// QuestionToPayload converts a Question to a map[string]any suitable for
// use as an event bus payload (e.g. events.EventAskUser).
func QuestionToPayload(q Question) map[string]any {
	p := map[string]any{
		"question_id": q.ID,
		"session_id":  q.SessionID,
		"kind":        string(q.Kind),
		"prompt":      q.Prompt,
	}
	if len(q.Choices) > 0 {
		p["choices"] = q.Choices
	}
	if q.AllowText {
		p["allow_text"] = true
	}
	if q.Default != "" {
		p["default"] = q.Default
	}
	if q.TimeoutSecs > 0 {
		p["timeout_secs"] = q.TimeoutSecs
	}
	if q.Password {
		p["password"] = true
	}
	if q.Resumed {
		p["resumed"] = true
	}
	if q.Group != "" {
		p["group"] = q.Group
	}
	if q.Item != nil {
		item := map[string]any{}
		if q.Item.Kind != "" {
			item["kind"] = q.Item.Kind
		}
		if q.Item.Name != "" {
			item["name"] = q.Item.Name
		}
		if q.Item.Source != "" {
			item["source"] = q.Item.Source
		}
		p["item"] = item
	}
	return p
}
