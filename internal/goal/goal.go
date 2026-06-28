// Package goal implements session-scoped completion goals: a user sets a
// condition with `/goal <condition>` and the agent keeps taking turns on its own
// until a small fast model (the evaluator, see agent/goal_eval.go) judges the
// condition met. This is omnis's port of Claude Code's `/goal` — the same
// "keep working toward a verifiable end state" affordance.
//
// One goal is active per session. The same store records the achieved goal after
// completion so `/goal` (no argument) can report it until the session is cleared.
//
// The store is process-wide (held on agent.Infrastructure) so it survives a
// hot-reload, exactly like the mid-turn steering store and the scheduler. It is
// in-memory; durability across a process restart is handled by the surface
// persisting the active condition on the session and calling Restore on boot
// (resume semantics: the condition carries over, the timer/turns/token baseline
// reset).
package goal

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ConditionMaxLen caps a goal condition, matching Claude Code's 4,000-char limit.
const ConditionMaxLen = 4000

// defaultMaxTurns is the hard host ceiling on how many turns one goal may drive
// before the loop stops regardless of the condition. It is a runaway-cost guard
// (each goal turn runs on the expensive main model); a user can also bound a goal
// from inside the condition ("or stop after N turns"), which the evaluator judges
// from the transcript. Override with OMNIS_GOAL_MAX_TURNS.
const defaultMaxTurns = 30

var maxTurnsOnce struct {
	sync.Once
	n int
}

// MaxTurns is the resolved hard turn ceiling (OMNIS_GOAL_MAX_TURNS, else the
// default). Resolved once.
func MaxTurns() int {
	maxTurnsOnce.Do(func() {
		maxTurnsOnce.n = defaultMaxTurns
		if raw := strings.TrimSpace(os.Getenv("OMNIS_GOAL_MAX_TURNS")); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v > 0 {
				maxTurnsOnce.n = v
			}
		}
	})
	return maxTurnsOnce.n
}

// Goal is one session's completion goal — active, or achieved-and-not-yet-cleared.
type Goal struct {
	// Condition is the user-supplied completion condition (the evaluator judges
	// it against the conversation transcript).
	Condition string
	// SetAt is when the goal was set or last restored (the timer origin).
	SetAt time.Time
	// Turns is how many turns the evaluator has assessed for this goal.
	Turns int
	// TokensSpent accumulates token usage attributed to the goal's turns, for the
	// status view. Best-effort; surfaces feed it when they have usage numbers.
	TokensSpent int64
	// LastReason is the evaluator's most recent short reason (why the condition is
	// or isn't met), surfaced in the status view and the next turn's guidance.
	LastReason string
	// Achieved is true once the condition was met. The entry is kept so `/goal`
	// can report the achieved goal until the session is cleared.
	Achieved bool
	// AchievedAt is when the condition was met (zero while active).
	AchievedAt time.Time
}

// Active reports whether g is a running (not-yet-achieved) goal.
func (g Goal) Active() bool { return g.Condition != "" && !g.Achieved }

// Duration is how long the goal has been (or was) running.
func (g Goal) Duration() time.Duration {
	if g.SetAt.IsZero() {
		return 0
	}
	end := time.Now()
	if g.Achieved && !g.AchievedAt.IsZero() {
		end = g.AchievedAt
	}
	return end.Sub(g.SetAt)
}

// Store holds at most one Goal per session id, process-wide.
type Store struct {
	mu sync.Mutex
	m  map[string]*Goal
}

// New returns an empty goal store.
func New() *Store { return &Store{m: make(map[string]*Goal)} }

// Directive is the synthetic user turn injected when the evaluator judges the
// goal not yet met. It restates the condition, feeds back the evaluator's reason
// as guidance, and reminds the agent to make the completion evidence visible in
// its reply (the evaluator judges only the transcript). Shared by every surface.
func Directive(condition, reason string) string {
	var b strings.Builder
	b.WriteString("[Goal check] The completion condition is NOT yet satisfied. Keep working toward it.\n\n")
	b.WriteString("Condition:\n")
	b.WriteString(strings.TrimSpace(condition))
	if r := strings.TrimSpace(reason); r != "" {
		b.WriteString("\n\nEvaluator feedback (why it is not met yet):\n")
		b.WriteString(r)
	}
	b.WriteString("\n\nContinue the work. When you believe the condition holds, make the evidence explicit in your reply " +
		"(e.g. show the passing test output or the clean status) so it can be verified.")
	return b.String()
}

// clearAliases are the words accepted as `/goal clear` (Claude Code parity).
var clearAliases = map[string]bool{
	"clear": true, "stop": true, "off": true, "reset": true, "none": true, "cancel": true,
}

// IsClearAlias reports whether s is one of the `/goal clear` aliases.
func IsClearAlias(s string) bool {
	return clearAliases[strings.ToLower(strings.TrimSpace(s))]
}

// CleanCondition trims a condition and caps it to ConditionMaxLen runes.
func CleanCondition(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > ConditionMaxLen {
		s = strings.TrimSpace(string(r[:ConditionMaxLen]))
	}
	return s
}

// Set installs (or replaces) the active goal for sid, resetting the timer, turn
// count and token spend. A blank condition or sid is a no-op returning false.
func (s *Store) Set(sid, condition string) (Goal, bool) {
	condition = CleanCondition(condition)
	if sid == "" || condition == "" {
		return Goal{}, false
	}
	g := &Goal{Condition: condition, SetAt: time.Now()}
	s.mu.Lock()
	s.m[sid] = g
	s.mu.Unlock()
	return *g, true
}

// Restore reinstalls an active goal on a process restart (resume semantics): the
// condition carries over but the timer/turns/token baseline reset. Equivalent to
// Set; named separately so call sites read clearly.
func (s *Store) Restore(sid, condition string) (Goal, bool) {
	return s.Set(sid, condition)
}

// Get returns a snapshot of sid's goal and whether one exists (active or
// achieved-and-not-cleared).
func (s *Store) Get(sid string) (Goal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.m[sid]
	if g == nil {
		return Goal{}, false
	}
	return *g, true
}

// Active reports whether sid has an active (un-achieved, un-cleared) goal.
func (s *Store) Active(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.m[sid]
	return g != nil && g.Active()
}

// RecordTurn increments the evaluated-turn counter, stores the latest evaluator
// reason, and adds best-effort token spend. Returns the updated turn count (0 if
// no active goal). A "no" verdict from the evaluator drives this each iteration.
func (s *Store) RecordTurn(sid, reason string, tokens int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.m[sid]
	if g == nil || !g.Active() {
		return 0
	}
	g.Turns++
	if r := strings.TrimSpace(reason); r != "" {
		g.LastReason = r
	}
	if tokens > 0 {
		g.TokensSpent += tokens
	}
	return g.Turns
}

// MarkAchieved flips sid's goal to achieved and records the final reason. The
// entry is retained for status display until the session is cleared.
func (s *Store) MarkAchieved(sid, reason string) (Goal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.m[sid]
	if g == nil || !g.Active() {
		return Goal{}, false
	}
	g.Achieved = true
	g.AchievedAt = time.Now()
	if r := strings.TrimSpace(reason); r != "" {
		g.LastReason = r
	}
	return *g, true
}

// CapReached reports whether sid's active goal has hit the hard turn ceiling.
func (s *Store) CapReached(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.m[sid]
	return g != nil && g.Active() && g.Turns >= MaxTurns()
}

// Clear removes sid's goal entirely (active or achieved). Returns whether one
// was present.
func (s *Store) Clear(sid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[sid]; !ok {
		return false
	}
	delete(s.m, sid)
	return true
}

// Forget drops all goal state for sid (call on session deletion).
func (s *Store) Forget(sid string) { s.Clear(sid) }
