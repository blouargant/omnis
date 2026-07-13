package budget

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestUnarmedSessionIsUnbounded(t *testing.T) {
	s := New()
	for i := 0; i < 1000; i++ {
		if v := s.Gate("sid", nil); v != Proceed {
			t.Fatalf("call %d: got %v, want Proceed on an unarmed session", i, v)
		}
	}
}

func TestToolCallCeilingAsksThenHalts(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxToolCalls: 3})

	asked := 0
	stop := func(Usage) Outcome { asked++; return OutcomeStop }

	for i := 1; i <= 3; i++ {
		if v := s.Gate("sid", stop); v != Proceed {
			t.Fatalf("call %d: got %v, want Proceed (under ceiling)", i, v)
		}
	}
	if asked != 0 {
		t.Fatalf("asked %d times before the ceiling was crossed", asked)
	}
	if v := s.Gate("sid", stop); v != Halted {
		t.Fatalf("call 4: got %v, want Halted", v)
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want exactly 1", asked)
	}
	// Once halted, every later call is short-circuited without re-asking.
	for i := 0; i < 5; i++ {
		if v := s.Gate("sid", stop); v != Halted {
			t.Fatalf("post-halt call: got %v, want Halted", v)
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times after halt, want the single original ask", asked)
	}
}

func TestContinueGrantsAnotherSlice(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxToolCalls: 2})

	var asked int
	cont := func(Usage) Outcome { asked++; return OutcomeContinue }

	// 2 free, the 3rd asks and is granted, then 2 more free (ceiling now 4).
	for i := 1; i <= 4; i++ {
		if v := s.Gate("sid", cont); v != Proceed {
			t.Fatalf("call %d: got %v, want Proceed", i, v)
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want 1 (ceiling raised 2→4)", asked)
	}
	if v := s.Gate("sid", cont); v != Proceed {
		t.Fatalf("call 5: got %v, want Proceed after a second grant", v)
	}
	if asked != 2 {
		t.Fatalf("asked %d times, want 2", asked)
	}

	u, ok := s.Usage("sid")
	if !ok || u.Grants != 2 || u.ToolCalls != 5 {
		t.Fatalf("usage = %+v, want 5 calls / 2 grants", u)
	}
}

func TestUnlimitedRemovesTheCeiling(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxToolCalls: 1})

	var asked int32
	unl := func(Usage) Outcome { atomic.AddInt32(&asked, 1); return OutcomeUnlimited }

	for i := 0; i < 50; i++ {
		if v := s.Gate("sid", unl); v != Proceed {
			t.Fatalf("call %d: got %v, want Proceed once unlimited", i, v)
		}
	}
	if got := atomic.LoadInt32(&asked); got != 1 {
		t.Fatalf("asked %d times, want exactly 1", got)
	}
}

func TestTokenCeilingTripsTheGate(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxTokens: 1000})

	// Tool calls alone never trip it (no tool-call ceiling configured).
	if v := s.Gate("sid", nil); v != Proceed {
		t.Fatalf("got %v, want Proceed with no tokens spent", v)
	}
	s.AddTokens("sid", 1500) // a sub-agent's model call blows the token budget

	asked := 0
	if v := s.Gate("sid", func(Usage) Outcome { asked++; return OutcomeStop }); v != Halted {
		t.Fatalf("got %v, want Halted once tokens exceed the ceiling", v)
	}
	if asked != 1 {
		t.Fatalf("asked %d times, want 1", asked)
	}
}

// The fan-out case that motivated the single-flight: max_instances parallel
// sub-agents all cross the ceiling at once. The user must see ONE card, and the
// grant must apply to all of them.
func TestConcurrentFanOutAsksOnce(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxToolCalls: 5})

	var asked int32
	ask := func(Usage) Outcome {
		atomic.AddInt32(&asked, 1)
		return OutcomeStop
	}

	var wg sync.WaitGroup
	verdicts := make([]Verdict, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			verdicts[i] = s.Gate("sid", ask)
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&asked); got != 1 {
		t.Fatalf("asked %d times across a parallel fan-out, want exactly 1", got)
	}
	var proceeded, halted int
	for _, v := range verdicts {
		if v == Proceed {
			proceeded++
		} else {
			halted++
		}
	}
	if proceeded > 5 {
		t.Fatalf("%d calls proceeded, want at most the ceiling (5)", proceeded)
	}
	if halted == 0 {
		t.Fatal("no call was halted, want the over-budget ones short-circuited")
	}
}

func TestStartTurnResetsCounters(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxToolCalls: 2})
	s.Gate("sid", nil)
	s.Gate("sid", nil)
	s.AddTokens("sid", 999)

	s.StartTurn("sid", Limits{MaxToolCalls: 2}) // next turn
	u, ok := s.Usage("sid")
	if !ok || u.ToolCalls != 0 || u.Tokens != 0 || u.Grants != 0 {
		t.Fatalf("usage after StartTurn = %+v, want a clean slate", u)
	}
	if v := s.Gate("sid", nil); v != Proceed {
		t.Fatalf("got %v, want Proceed on the fresh turn", v)
	}
}

func TestUnlimitedLimitsArmNothing(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{}) // both axes zero → unbounded
	for i := 0; i < 500; i++ {
		if v := s.Gate("sid", nil); v != Proceed {
			t.Fatalf("got %v, want Proceed when limits are unlimited", v)
		}
	}
	if _, ok := s.Usage("sid"); ok {
		t.Fatal("an unlimited StartTurn should not arm a turn")
	}
}

func TestPerAgentCapIsIndependentOfTheTurnCeiling(t *testing.T) {
	s := New()
	// No global ceiling at all — only a per-agent cap. The turn must still arm,
	// otherwise a per-agent design limit would silently depend on the user having
	// configured a spend budget.
	s.StartTurn("sid", Limits{PerAgent: map[string]int{"research_critic": 3}})

	for i := 1; i <= 3; i++ {
		if over, _ := s.ChargeAgent("sid", "research_critic"); over {
			t.Fatalf("call %d: capped early, want the first 3 to pass", i)
		}
	}
	if over, _ := s.ChargeAgent("sid", "research_critic"); !over {
		t.Fatal("4th call was not capped")
	}
	// Still capped afterwards.
	if over, _ := s.ChargeAgent("sid", "research_critic"); !over {
		t.Fatal("5th call escaped the cap")
	}
	// An uncapped agent in the same turn is untouched.
	for i := 0; i < 50; i++ {
		if over, _ := s.ChargeAgent("sid", "web_agent"); over {
			t.Fatal("an agent with no configured cap was capped")
		}
	}
	// The global gate is unaffected: no MaxToolCalls was set.
	for i := 0; i < 20; i++ {
		if v := s.Gate("sid", nil); v != Proceed {
			t.Fatalf("global gate halted with no MaxToolCalls set: %v", v)
		}
	}
}

// A "Continue" grant buys more turn budget; it must not re-open an agent's
// bounded verification pass — that cap expresses how the agent is designed to
// work, not how much the user is willing to spend.
func TestContinueDoesNotRaiseAPerAgentCap(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{MaxToolCalls: 1, PerAgent: map[string]int{"research_critic": 1}})

	if over, _ := s.ChargeAgent("sid", "research_critic"); over {
		t.Fatal("first call capped")
	}
	if v := s.Gate("sid", func(Usage) Outcome { return OutcomeContinue }); v != Proceed {
		t.Fatalf("got %v, want Proceed", v)
	}
	// Turn budget was extended, but the critic's own cap still holds.
	if over, _ := s.ChargeAgent("sid", "research_critic"); !over {
		t.Fatal("a Continue grant re-opened the per-agent cap")
	}
}

func TestStartTurnResetsPerAgentCounters(t *testing.T) {
	s := New()
	l := Limits{PerAgent: map[string]int{"research_critic": 2}}
	s.StartTurn("sid", l)
	_, _ = s.ChargeAgent("sid", "research_critic")
	_, _ = s.ChargeAgent("sid", "research_critic")
	if over, _ := s.ChargeAgent("sid", "research_critic"); !over {
		t.Fatal("3rd call not capped")
	}
	s.StartTurn("sid", l) // next turn
	if n := s.AgentCalls("sid", "research_critic"); n != 0 {
		t.Fatalf("agent counter = %d after StartTurn, want a clean slate", n)
	}
	if over, _ := s.ChargeAgent("sid", "research_critic"); over {
		t.Fatal("capped on the first call of a fresh turn")
	}
}

// A per-agent cap is per-TURN and keyed by agent NAME, so every parallel
// instance of a max_instances fan-out agent draws from the SAME counter. That
// makes it a total work budget for that agent in the turn, not a per-instance
// one — capping a 10-way fan-out at 12 gives each researcher barely one call.
// This is why the cap is set on research_critic (max_instances: 1) and NOT on
// web_agent (max_instances: 10), whose cost is bounded by the output shaper
// instead. Locking the semantics in so the trap is visible if someone changes it.
func TestPerAgentCapIsSharedAcrossParallelInstances(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{PerAgent: map[string]int{"web_agent": 4}})

	// Two concurrent instances of the same agent (a fan-out) share the counter.
	var over int
	for i := 0; i < 6; i++ {
		if blocked, _ := s.ChargeAgent("sid", "web_agent"); blocked {
			over++
		}
	}
	if got := s.AgentCalls("sid", "web_agent"); got != 6 {
		t.Fatalf("agent calls = %d, want 6 counted across instances", got)
	}
	if over != 2 {
		t.Fatalf("%d calls over cap, want 2 (the cap is a shared total, not per-instance)", over)
	}
}

// overBy is what lets the caller escalate from "please stop" to "you are stopped".
// A notice is only an instruction: in a live run a capped web_agent issued 16
// further tool calls across 13 model round-trips after being told to stop, and
// nothing would ever have ended that — its flow loop has no iteration cap, and a
// blocked call is not charged to the shared turn budget. overBy counts how far
// past the cap each blocked call is, so the caller can terminate the loop for
// real once its grace is spent.
func TestChargeAgentReportsHowFarPastTheCap(t *testing.T) {
	s := New()
	s.StartTurn("sid", Limits{PerAgent: map[string]int{"research_critic": 2}})

	for i := 1; i <= 2; i++ {
		if over, by := s.ChargeAgent("sid", "research_critic"); over || by != 0 {
			t.Fatalf("call %d: over=%v by=%d, want under the cap", i, over, by)
		}
	}
	for want := 1; want <= 5; want++ {
		over, by := s.ChargeAgent("sid", "research_critic")
		if !over {
			t.Fatalf("blocked call %d reported under the cap", want)
		}
		if by != want {
			t.Fatalf("overBy = %d, want %d (it must keep climbing so the caller can escalate)", by, want)
		}
	}
}
