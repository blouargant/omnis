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

// DefaultTimeout is used when a question has TimeoutSecs == 0.
const DefaultTimeout = 5 * time.Minute

// pending holds a question that is waiting for an answer.
type pending struct {
	q    Question
	ch   chan Answer // buffer 1; closed on resolution
	once sync.Once   // ensures ch is closed exactly once
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
func (r *Registry) Resolve(sessionID, questionID string, ans Answer) error {
	r.mu.Lock()
	sm := r.sessions[sessionID]
	if sm == nil {
		r.mu.Unlock()
		return fmt.Errorf("%w: session %q", ErrUnknownQuestion, sessionID)
	}
	p, ok := sm[questionID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownQuestion, questionID)
	}
	return r.resolveInternal(sessionID, questionID, ans, p)
}

func (r *Registry) resolveInternal(sessionID, questionID string, ans Answer, p *pending) error {
	var alreadyDone bool
	p.once.Do(func() {
		p.ch <- ans
		close(p.ch)
		r.mu.Lock()
		if r.sessions[sessionID] != nil {
			delete(r.sessions[sessionID], questionID)
		}
		r.mu.Unlock()
		if r.cancelFn != nil {
			r.cancelFn(p.q)
		}
	})
	if alreadyDone {
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
	return p
}
