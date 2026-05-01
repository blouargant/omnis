// Package teammates: FSM and message-passing tools (article Phase 3 / s10).
//
// State diagram (article §"Communication Protocols"):
//   IDLE → REQUESTING (sent a question, expects reply)
//   IDLE → RESPONDING (received a question, must answer)
//   REQUESTING → WAITING (sent, awaiting response)
//   WAITING → IDLE (response received)
//   RESPONDING → IDLE (sent the answer)
package teammates

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// AgentState is the FSM state.
type AgentState int

const (
	StateIdle AgentState = iota
	StateRequesting
	StateWaiting
	StateResponding
)

// String makes states printable.
func (s AgentState) String() string {
	switch s {
	case StateIdle:
		return "IDLE"
	case StateRequesting:
		return "REQUESTING"
	case StateWaiting:
		return "WAITING"
	case StateResponding:
		return "RESPONDING"
	}
	return "UNKNOWN"
}

// Agent wraps a Backend with an identity and FSM state.
type Agent struct {
	Name    string
	Backend Backend

	// NameFunc, if set, transforms (userID, sessionID, logicalName) into
	// the actual mailbox name used on the backend. Use it to namespace
	// mailboxes per session so two concurrent sessions running an agent
	// called "lead" do not share an inbox. When nil, names are passed
	// through unchanged (back-compat).
	NameFunc func(userID, sessionID, name string) string

	mu    sync.Mutex
	state AgentState
}

// New returns an Agent in IDLE.
func NewAgent(name string, b Backend) *Agent {
	return &Agent{Name: name, Backend: b, state: StateIdle}
}

// resolveName returns the effective mailbox name for `logical` given the
// caller's tool.Context. Falls back to logical when no NameFunc / no IDs.
func (a *Agent) resolveName(ctx tool.Context, logical string) string {
	if a.NameFunc == nil {
		return logical
	}
	var u, s string
	if ctx != nil {
		u = ctx.UserID()
		s = ctx.SessionID()
	}
	if u == "" && s == "" {
		return logical
	}
	return a.NameFunc(u, s, logical)
}

// State returns the current FSM state.
func (a *Agent) State() AgentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// transition checks legal moves; returns error if illegal.
func (a *Agent) transition(to AgentState) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	from := a.state
	legal := map[AgentState]map[AgentState]bool{
		StateIdle:       {StateRequesting: true, StateResponding: true},
		StateRequesting: {StateWaiting: true, StateIdle: true},
		StateWaiting:    {StateIdle: true},
		StateResponding: {StateIdle: true},
	}
	if !legal[from][to] {
		return fmt.Errorf("illegal transition %s → %s", from, to)
	}
	a.state = to
	return nil
}

// Ask sends a question to `to` and blocks until a reply or timeout.
func (a *Agent) Ask(ctx context.Context, to, question string, timeout time.Duration) (string, error) {
	if err := a.transition(StateRequesting); err != nil {
		return "", err
	}
	if err := a.Backend.Send(ctx, to, Message{From: a.Name, Body: question}); err != nil {
		_ = a.transition(StateIdle)
		return "", err
	}
	if err := a.transition(StateWaiting); err != nil {
		return "", err
	}
	m, err := a.Backend.Receive(ctx, a.Name, timeout)
	_ = a.transition(StateIdle)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "(no reply: timed out)", nil
	}
	return fmt.Sprintf("[%s] %s", m.From, m.Body), nil
}

// Tell sends a one-way message (no reply expected).
func (a *Agent) Tell(ctx context.Context, to, body string) error {
	return a.Backend.Send(ctx, to, Message{From: a.Name, Body: body})
}

// Check returns one pending message if any (no blocking beyond timeout).
func (a *Agent) Check(ctx context.Context, timeout time.Duration) (*Message, error) {
	if err := a.transition(StateResponding); err != nil {
		// caller may already be handling something; just observe.
	}
	m, err := a.Backend.Receive(ctx, a.Name, timeout)
	_ = a.transition(StateIdle)
	return m, err
}

// ----------------------------------------------------------------------
// ADK tool wrappers
// ----------------------------------------------------------------------

type askIn struct {
	To       string `json:"to"`
	Question string `json:"question"`
}
type askOut struct {
	Reply string `json:"reply"`
}
type tellIn struct {
	To   string `json:"to"`
	Body string `json:"body"`
}
type tellOut struct {
	Result string `json:"result"`
}
type checkIn struct{}
type checkOut struct {
	Message string `json:"message"`
}

// askWith / tellWith / checkWith mirror Ask/Tell/Check but use explicit
// (sender, recipient) names so the Tools() wrappers can substitute the
// session-scoped mailbox names without touching the Agent's own Name.
func (a *Agent) askWith(ctx context.Context, fromName, toName, question string, timeout time.Duration) (string, error) {
	if err := a.transition(StateRequesting); err != nil {
		return "", err
	}
	if err := a.Backend.Send(ctx, toName, Message{From: fromName, Body: question}); err != nil {
		_ = a.transition(StateIdle)
		return "", err
	}
	if err := a.transition(StateWaiting); err != nil {
		return "", err
	}
	m, err := a.Backend.Receive(ctx, fromName, timeout)
	_ = a.transition(StateIdle)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "(no reply: timed out)", nil
	}
	return fmt.Sprintf("[%s] %s", m.From, m.Body), nil
}

func (a *Agent) tellWith(ctx context.Context, fromName, toName, body string) error {
	return a.Backend.Send(ctx, toName, Message{From: fromName, Body: body})
}

func (a *Agent) checkWith(ctx context.Context, name string, timeout time.Duration) (*Message, error) {
	_ = a.transition(StateResponding)
	m, err := a.Backend.Receive(ctx, name, timeout)
	_ = a.transition(StateIdle)
	return m, err
}

// Tools returns the three communication tools wired to this agent. When
// NameFunc is set, mailbox names are namespaced per session so concurrent
// sessions cannot read each other's messages.
func (a *Agent) Tools() []tool.Tool {
	ask, _ := functiontool.New(functiontool.Config{
		Name: "teammate_ask",
		Description: "Send a question to another agent (by name) and wait up to 30s for a reply. " +
			"Use when you need information another agent owns.",
	}, func(ctx tool.Context, in askIn) (askOut, error) {
		from := a.resolveName(ctx, a.Name)
		to := a.resolveName(ctx, in.To)
		reply, err := a.askWith(context.Background(), from, to, in.Question, 30*time.Second)
		if err != nil {
			return askOut{Reply: "Error: " + err.Error()}, nil
		}
		return askOut{Reply: reply}, nil
	})
	tell, _ := functiontool.New(functiontool.Config{
		Name:        "teammate_tell",
		Description: "Send a one-way message to another agent. Required input fields: to (recipient mailbox name), body (message text). No reply.",
	}, func(ctx tool.Context, in tellIn) (tellOut, error) {
		from := a.resolveName(ctx, a.Name)
		to := a.resolveName(ctx, in.To)
		if err := a.tellWith(context.Background(), from, to, in.Body); err != nil {
			return tellOut{Result: "Error: " + err.Error()}, nil
		}
		return tellOut{Result: "delivered"}, nil
	})
	check, _ := functiontool.New(functiontool.Config{
		Name:        "teammate_check",
		Description: "Check your own mailbox for a pending message (waits up to 1s). Returns '(none)' if empty.",
	}, func(ctx tool.Context, _ checkIn) (checkOut, error) {
		name := a.resolveName(ctx, a.Name)
		m, err := a.checkWith(context.Background(), name, time.Second)
		if err != nil {
			return checkOut{Message: "Error: " + err.Error()}, nil
		}
		if m == nil {
			return checkOut{Message: "(none)"}, nil
		}
		return checkOut{Message: fmt.Sprintf("[%s] %s", m.From, m.Body)}, nil
	})
	return []tool.Tool{ask, tell, check}
}
