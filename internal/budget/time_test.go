package budget

import (
	"testing"
	"time"
)

func TestTimeLeft(t *testing.T) {
	s := New()
	if _, _, ok := s.TimeLeft("s"); ok {
		t.Fatal("no turn must report no limit")
	}
	s.StartTurn("s", Limits{MaxDuration: time.Hour})
	left, limit, ok := s.TimeLeft("s")
	if !ok || limit != time.Hour || left <= 59*time.Minute {
		t.Fatalf("left=%v limit=%v ok=%v", left, limit, ok)
	}
	// A time-only turn gates nothing.
	if v := s.Gate("s", func(Usage) Outcome { t.Fatal("asked"); return OutcomeStop }); v != Proceed {
		t.Fatal("a time limit must never halt a tool call")
	}
	s.StartTurn("n", Limits{MaxToolCalls: 5})
	if _, _, ok := s.TimeLeft("n"); ok {
		t.Fatal("a turn without a time limit must report none")
	}
}
