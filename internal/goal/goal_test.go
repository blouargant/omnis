package goal

import (
	"strings"
	"testing"
)

func TestSetGetActiveClear(t *testing.T) {
	s := New()
	if s.Active("sid") {
		t.Fatal("expected no active goal initially")
	}
	if _, ok := s.Set("sid", "  all tests pass  "); !ok {
		t.Fatal("Set should succeed for a non-empty condition")
	}
	g, ok := s.Get("sid")
	if !ok || g.Condition != "all tests pass" {
		t.Fatalf("Get returned %+v ok=%v; want trimmed condition", g, ok)
	}
	if !g.Active() || !s.Active("sid") {
		t.Fatal("goal should be active after Set")
	}
	if !s.Clear("sid") {
		t.Fatal("Clear should report it removed a goal")
	}
	if s.Active("sid") {
		t.Fatal("goal should be gone after Clear")
	}
	if s.Clear("sid") {
		t.Fatal("Clear on an empty session should report false")
	}
}

func TestSetEmptyIsNoOp(t *testing.T) {
	s := New()
	if _, ok := s.Set("sid", "   "); ok {
		t.Fatal("blank condition must not set a goal")
	}
	if _, ok := s.Set("", "x"); ok {
		t.Fatal("blank session id must not set a goal")
	}
}

func TestRecordTurnAndAchieve(t *testing.T) {
	s := New()
	s.Set("sid", "done")
	if n := s.RecordTurn("sid", "not yet", 10); n != 1 {
		t.Fatalf("RecordTurn = %d; want 1", n)
	}
	s.RecordTurn("sid", "still not", 5)
	g, _ := s.Get("sid")
	if g.Turns != 2 || g.LastReason != "still not" || g.TokensSpent != 15 {
		t.Fatalf("after two turns: %+v", g)
	}
	ach, ok := s.MarkAchieved("sid", "all green")
	if !ok || !ach.Achieved || ach.Active() {
		t.Fatalf("MarkAchieved: %+v ok=%v", ach, ok)
	}
	// A recorded turn on an achieved goal is ignored.
	if n := s.RecordTurn("sid", "x", 1); n != 0 {
		t.Fatalf("RecordTurn on achieved goal = %d; want 0", n)
	}
}

func TestCapReached(t *testing.T) {
	t.Setenv("OMNIS_GOAL_MAX_TURNS", "3")
	// MaxTurns memoises; this test relies on a fresh process value. Since other
	// tests may have resolved it, assert relative to whatever MaxTurns reports.
	max := MaxTurns()
	s := New()
	s.Set("sid", "loop")
	for i := 0; i < max; i++ {
		if s.CapReached("sid") {
			t.Fatalf("cap reached early at turn %d/%d", i, max)
		}
		s.RecordTurn("sid", "again", 0)
	}
	if !s.CapReached("sid") {
		t.Fatalf("cap should be reached after %d turns", max)
	}
}

func TestCleanConditionCaps(t *testing.T) {
	long := strings.Repeat("x", ConditionMaxLen+50)
	got := CleanCondition(long)
	if len([]rune(got)) > ConditionMaxLen {
		t.Fatalf("CleanCondition len = %d; want <= %d", len([]rune(got)), ConditionMaxLen)
	}
}

func TestIsClearAlias(t *testing.T) {
	for _, a := range []string{"clear", "STOP", " off ", "Reset", "none", "cancel"} {
		if !IsClearAlias(a) {
			t.Errorf("IsClearAlias(%q) = false; want true", a)
		}
	}
	for _, a := range []string{"", "done", "go"} {
		if IsClearAlias(a) {
			t.Errorf("IsClearAlias(%q) = true; want false", a)
		}
	}
}

func TestDirectiveContainsConditionAndReason(t *testing.T) {
	d := Directive("ship it", "tests fail")
	if !strings.Contains(d, "ship it") || !strings.Contains(d, "tests fail") {
		t.Fatalf("Directive missing condition/reason: %q", d)
	}
}
