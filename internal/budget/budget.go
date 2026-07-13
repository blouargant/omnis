// Package budget bounds what a single turn is allowed to spend.
//
// The ADK LLM flow loop has no iteration cap: it ends only when the model
// returns a tool-call-free response. A model that keeps deciding to search
// therefore searches forever, and because a sub-agent runs inside agenttool's
// private runner, the surface that started the turn cannot see — let alone stop
// — what it is doing. Prompts cannot close that hole: an instruction like "stop
// when a wave stops opening new rows" is advice, not a guarantee.
//
// This package is the guarantee. It counts a turn's tool calls and model tokens
// across the squad root AND every sub-agent (one bucket per user-facing
// session), and when the ceiling is reached it hands the decision to the user:
// stop and answer with what you have, or grant another slice.
//
// No-op contract: a session for which StartTurn was never called is unbounded,
// so a surface that does not opt in behaves exactly as before.
package budget

import "sync"

// Defaults chosen so an ordinary turn never sees the gate: a normal chat turn
// spends a handful of tool calls and well under 500k tokens, while the runaway
// this was written for reached ~295 tool calls / 15.2M tokens.
const (
	DefaultMaxToolCalls = 100
	DefaultMaxTokens    = 2_000_000
)

// Limits is a per-turn ceiling. A zero field means "no limit on that axis";
// all zero/empty means the turn is unbounded.
type Limits struct {
	MaxToolCalls int
	MaxTokens    int64

	// PerAgent caps how many tool calls ONE agent may make in a turn (agent name
	// → cap; absent or 0 = uncapped). This is a different instrument from
	// MaxToolCalls: that one is a spend ceiling on the whole turn and asks the
	// user when it trips, whereas this is a per-agent DESIGN limit — "the critic
	// verifies with at most N searches" — that simply tells the agent to conclude.
	//
	// It exists because a sub-agent's cost is quadratic in its tool calls: it runs
	// its own flow loop, re-sending its whole accumulated context (including every
	// fetched page) on each model call. research_critic reached 9.1M prompt tokens
	// from ~20 fetches × 2 invocations. Bounding N is therefore worth far more than
	// bounding the tokens directly.
	PerAgent map[string]int
}

// Unlimited reports whether the limits impose no ceiling at all.
func (l Limits) Unlimited() bool {
	return l.MaxToolCalls <= 0 && l.MaxTokens <= 0 && len(l.PerAgent) == 0
}

// Usage is what a turn has spent so far, plus the ceiling it is spending against.
type Usage struct {
	ToolCalls int
	Tokens    int64
	Grants    int // how many times the user extended the budget this turn
	Limits    Limits
}

// Verdict is what the caller should do with the tool call it is gating.
type Verdict int

const (
	Proceed Verdict = iota // under budget — run the tool
	Halted                 // the user chose to stop — short-circuit the tool
)

// Outcome is the user's answer when the ceiling is reached.
type Outcome int

const (
	OutcomeStop      Outcome = iota // stop researching, answer with what you have
	OutcomeContinue                 // grant one more slice of the same size
	OutcomeUnlimited                // remove the ceiling for the rest of this turn
)

type turn struct {
	mu        sync.Mutex
	toolCalls int
	tokens    int64
	agents    map[string]int // agent name → tool calls it has made this turn
	limits    Limits         // current ceiling; raised by each grant
	grant     Limits         // the slice added per "Continue" (the original limits)
	grants    int
	halted    bool
	unlimited bool

	// askMu single-flights the user prompt. Parallel sub-agents (max_instances
	// fan-out) all hit the ceiling at once; the first one asks while the rest
	// queue here and then re-check, so the user sees one card, not ten.
	askMu sync.Mutex
}

// over reports whether either axis has crossed its ceiling. Caller holds mu.
func (t *turn) over() bool {
	if t.unlimited {
		return false
	}
	if t.limits.MaxToolCalls > 0 && t.toolCalls > t.limits.MaxToolCalls {
		return true
	}
	if t.limits.MaxTokens > 0 && t.tokens > t.limits.MaxTokens {
		return true
	}
	return false
}

func (t *turn) usage() Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Usage{ToolCalls: t.toolCalls, Tokens: t.tokens, Grants: t.grants, Limits: t.limits}
}

// Store holds one live budget per user-facing session.
type Store struct {
	mu sync.Mutex
	m  map[string]*turn
}

// New builds an empty store.
func New() *Store { return &Store{m: map[string]*turn{}} }

func (s *Store) get(sid string) *turn {
	if s == nil || sid == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[sid]
}

// StartTurn arms (or re-arms) the budget for a session's next turn. Every
// counter resets, so a user who granted an extension last turn starts the next
// one at the configured ceiling again. Unlimited limits arm nothing.
func (s *Store) StartTurn(sid string, l Limits) {
	if s == nil || sid == "" || l.Unlimited() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sid] = &turn{limits: l, grant: l, agents: map[string]int{}}
}

// ChargeAgent counts one tool call made by `agent` and reports whether that
// agent has now exceeded its OWN per-turn cap (Limits.PerAgent).
//
// Unlike Gate this never asks the user: a per-agent cap is a design limit on how
// much work that agent does, not a surprise bill, so the caller simply tells the
// agent to conclude with what it has. An uncapped agent (or an unarmed session)
// always reports false, so this is a no-op unless a cap is configured.
//
// A "Continue" grant on the shared turn budget deliberately does NOT raise a
// per-agent cap: the user agreeing to spend more should not silently re-open an
// agent's bounded verification pass.
// overBy is how far past the cap this call is: 1 for the first blocked call, 2
// for the second, and so on. The caller uses it to escalate — a notice is only
// an instruction, and a model that ignores it would otherwise keep issuing tool
// calls forever (its flow loop has no iteration cap, and a blocked call is not
// charged to the shared turn budget, so nothing else would ever stop it).
func (s *Store) ChargeAgent(sid, agent string) (over bool, overBy int) {
	t := s.get(sid)
	if t == nil || agent == "" {
		return false, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	limit := t.limits.PerAgent[agent]
	if limit <= 0 {
		return false, 0 // uncapped agent
	}
	if t.agents == nil {
		t.agents = map[string]int{}
	}
	t.agents[agent]++
	if n := t.agents[agent]; n > limit {
		return true, n - limit
	}
	return false, 0
}

// AgentCalls reports how many tool calls `agent` has made in the session's
// current turn.
func (s *Store) AgentCalls(sid, agent string) int {
	t := s.get(sid)
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.agents[agent]
}

// Forget drops a session's budget (session deleted / turn finished).
func (s *Store) Forget(sid string) {
	if s == nil || sid == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, sid)
}

// AddTokens accumulates a model call's token usage against the session's turn.
// Safe on an unarmed session (no-op).
func (s *Store) AddTokens(sid string, n int64) {
	t := s.get(sid)
	if t == nil || n <= 0 {
		return
	}
	t.mu.Lock()
	t.tokens += n
	t.mu.Unlock()
}

// Usage reports what the session's current turn has spent. The bool is false
// when no turn is armed.
func (s *Store) Usage(sid string) (Usage, bool) {
	t := s.get(sid)
	if t == nil {
		return Usage{}, false
	}
	return t.usage(), true
}

// Gate counts one tool call and decides whether it may run.
//
// When the call pushes the turn past its ceiling, ask is invoked EXACTLY ONCE
// per overrun — concurrent callers block until the first one returns, then
// re-evaluate against the (possibly raised) ceiling rather than each raising
// their own card. A nil ask, or a session with no armed turn, means no ceiling
// is enforced.
//
// ask runs while the session's ask-lock is held, which is deliberate: every
// other agent in the turn is paused at its next tool call until the user
// answers, so nothing races ahead while the decision is pending.
func (s *Store) Gate(sid string, ask func(Usage) Outcome) Verdict {
	t := s.get(sid)
	if t == nil {
		return Proceed // unarmed session: unbounded, as before
	}

	t.mu.Lock()
	if t.halted {
		t.mu.Unlock()
		return Halted
	}
	t.toolCalls++
	if !t.over() {
		t.mu.Unlock()
		return Proceed
	}
	t.mu.Unlock()

	t.askMu.Lock()
	defer t.askMu.Unlock()

	// Re-check under the ask-lock: while we queued, another goroutine may have
	// already asked and had the ceiling raised (→ we can just proceed) or had
	// the turn halted (→ we must not ask again).
	t.mu.Lock()
	switch {
	case t.halted:
		t.mu.Unlock()
		return Halted
	case !t.over():
		t.mu.Unlock()
		return Proceed
	}
	u := Usage{ToolCalls: t.toolCalls, Tokens: t.tokens, Grants: t.grants, Limits: t.limits}
	t.mu.Unlock()

	// No way to reach the user: fail safe by halting rather than letting an
	// unattended runaway keep spending.
	if ask == nil {
		t.mu.Lock()
		t.halted = true
		t.mu.Unlock()
		return Halted
	}

	switch ask(u) {
	case OutcomeContinue:
		t.mu.Lock()
		t.limits.MaxToolCalls += t.grant.MaxToolCalls
		t.limits.MaxTokens += t.grant.MaxTokens
		t.grants++
		t.mu.Unlock()
		return Proceed
	case OutcomeUnlimited:
		t.mu.Lock()
		t.unlimited = true
		t.grants++
		t.mu.Unlock()
		return Proceed
	default:
		t.mu.Lock()
		t.halted = true
		t.mu.Unlock()
		return Halted
	}
}
