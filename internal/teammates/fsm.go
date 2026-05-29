// Package teammates: FSM and message-passing tools (article Phase 3 / s10).
//
// State diagram (article §"Communication Protocols"):
//
//	IDLE → REQUESTING (sent a question, expects reply)
//	IDLE → RESPONDING (received a question, must answer)
//	REQUESTING → WAITING (sent, awaiting response)
//	WAITING → IDLE (response received)
//	RESPONDING → IDLE (sent the answer)
package teammates

import (
	"context"
	"fmt"
	"strings"
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

	// Registry, if set, is consulted before NameFunc. If the logical name
	// resolves to a known session alias (petname or user-assigned title),
	// the stored mailbox address is returned directly without applying
	// NameFunc, enabling cross-session communication.
	Registry *SessionRegistry

	// SuppressInboxPolling omits the teammate_check tool from Tools() when
	// set. Use it when the host already drains this agent's inbox in the
	// background (e.g. the server's pushManager) and delivers messages as
	// synthetic turns — model-driven polling would then be redundant and,
	// because Receive consumes the message, would race the background drainer
	// for the single-consumer inbox. teammate_ask/tell/list stay available.
	SuppressInboxPolling bool

	mu    sync.Mutex
	state AgentState
}

// New returns an Agent in IDLE.
func NewAgent(name string, b Backend) *Agent {
	return &Agent{Name: name, Backend: b, state: StateIdle}
}

// resolveName returns the effective mailbox name for `logical` given the
// caller's tool.Context.
//
// Resolution order:
//  1. Cross-session registry: if Registry is set and logical matches a known
//     session alias, return the stored mailbox address directly (bypasses
//     NameFunc so the address is not re-scoped to the current session).
//  2. NameFunc: apply the per-session namespace transformation.
//  3. Fall back to logical unchanged.
func (a *Agent) resolveName(ctx tool.Context, logical string) string {
	if a.Registry != nil {
		if addr, ok := a.Registry.Lookup(logical); ok {
			return addr
		}
	}
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
	To       string `json:"to,omitempty"`
	Question string `json:"question,omitempty"`
}
type askOut struct {
	Reply string `json:"reply"`
}
type tellIn struct {
	To   string `json:"to,omitempty"`
	Body string `json:"body,omitempty"`
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
		Description: "Send a question to another agent and block up to 30s for a reply. " +
			"`to` accepts an intra-session agent name (e.g. 'investigator') or a cross-session name " +
			"(petname such as 'happy-panda', or user-assigned title such as 'my-project'). " +
			"Use teammate_list to discover available cross-session peers. " +
			"Arguments: `to` (string, required), `question` (string, required).",
	}, func(ctx tool.Context, in askIn) (askOut, error) {
		if strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Question) == "" {
			return askOut{Reply: "Error: required arguments: to and question"}, nil
		}
		from := a.resolveName(ctx, a.Name)
		to := a.resolveName(ctx, in.To)
		reply, err := a.askWith(context.Background(), from, to, in.Question, 30*time.Second)
		if err != nil {
			return askOut{Reply: "Error: " + err.Error()}, nil
		}
		return askOut{Reply: reply}, nil
	})
	tell, _ := functiontool.New(functiontool.Config{
		Name: "teammate_tell",
		Description: "Send a one-way message to another agent (no reply expected). " +
			"`to` accepts an intra-session agent name or a cross-session name (petname or title). " +
			"The recipient's next teammate_check will surface this message along with your session name in the [From] field. " +
			"Arguments: `to` (string, required), `body` (string, required).",
	}, func(ctx tool.Context, in tellIn) (tellOut, error) {
		if strings.TrimSpace(in.To) == "" || strings.TrimSpace(in.Body) == "" {
			return tellOut{Result: "Error: required arguments: to and body"}, nil
		}
		from := a.resolveName(ctx, a.Name)
		to := a.resolveName(ctx, in.To)
		if err := a.tellWith(context.Background(), from, to, in.Body); err != nil {
			return tellOut{Result: "Error: " + err.Error()}, nil
		}
		return tellOut{Result: "delivered"}, nil
	})
	check, _ := functiontool.New(functiontool.Config{
		Name: "teammate_check",
		Description: "Poll your own mailbox once for a pending cross-session message (waits up to 1s). " +
			"Returns the message with a [From] field showing the sender's session name, or '(none)' if empty. " +
			"Call this on demand — e.g. when the user asks whether a peer has replied, or after a 'teammate_tell' you expect a follow-up to. " +
			"You do NOT need to poll every turn: incoming messages are normally delivered to you automatically.",
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

	type listOut struct {
		Sessions        map[string]string `json:"sessions"`
		YourSessionName string            `json:"your_session_name,omitempty"`
	}
	list, _ := functiontool.New(functiontool.Config{
		Name: "teammate_list",
		Description: "List all known agent sessions available for cross-session communication. " +
			"Returns `sessions` (map of session name to mailbox address) and `your_session_name` " +
			"(the name other sessions use to address THIS session). " +
			"When a received message refers to `your_session_name`, it is referring to you.",
	}, func(ctx tool.Context, _ struct{}) (listOut, error) {
		if a.Registry == nil {
			return listOut{Sessions: map[string]string{}}, nil
		}
		sessions := a.Registry.List()
		myAddr := a.resolveName(ctx, a.Name)
		var myName string
		for name, addr := range sessions {
			if addr == myAddr {
				myName = name
				break
			}
		}
		return listOut{Sessions: sessions, YourSessionName: myName}, nil
	})

	tools := []tool.Tool{ask, tell}
	if !a.SuppressInboxPolling {
		tools = append(tools, check)
	}
	tools = append(tools, list)
	return tools
}
