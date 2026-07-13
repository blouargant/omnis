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
