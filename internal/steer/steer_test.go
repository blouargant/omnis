package steer

import (
	"reflect"
	"testing"
)

func TestDrainMovesPendingToConsumed(t *testing.T) {
	s := New()
	s.Enqueue("sess", "first")
	s.Enqueue("sess", "  second  ") // trimmed
	s.Enqueue("sess", "   ")        // blank — ignored
	s.Enqueue("", "no-session")     // empty sid — ignored

	if got := s.PendingLen("sess"); got != 2 {
		t.Fatalf("PendingLen = %d, want 2", got)
	}

	drained := s.Drain("sess")
	if !reflect.DeepEqual(drained, []string{"first", "second"}) {
		t.Fatalf("Drain = %v, want [first second]", drained)
	}
	if got := s.PendingLen("sess"); got != 0 {
		t.Fatalf("PendingLen after drain = %d, want 0", got)
	}
	// Drained notes are now consumed (kept for persistence folding).
	if got := s.TakeConsumed("sess"); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("TakeConsumed = %v, want [first second]", got)
	}
	// TakeConsumed clears the consumed list.
	if got := s.TakeConsumed("sess"); got != nil {
		t.Fatalf("TakeConsumed second call = %v, want nil", got)
	}
}

func TestTakePendingReturnsUnconsumed(t *testing.T) {
	s := New()
	s.Enqueue("a", "one")
	s.Drain("a") // one → consumed
	s.Enqueue("a", "two")

	// Only the un-drained note is pending.
	if got := s.TakePending("a"); !reflect.DeepEqual(got, []string{"two"}) {
		t.Fatalf("TakePending = %v, want [two]", got)
	}
	if got := s.TakePending("a"); got != nil {
		t.Fatalf("TakePending second call = %v, want nil", got)
	}
	// The earlier drained note is still recoverable as consumed.
	if got := s.TakeConsumed("a"); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("TakeConsumed = %v, want [one]", got)
	}
}

func TestForgetDropsState(t *testing.T) {
	s := New()
	s.Enqueue("x", "note")
	s.Forget("x")
	if got := s.PendingLen("x"); got != 0 {
		t.Fatalf("PendingLen after Forget = %d, want 0", got)
	}
	if got := s.Drain("x"); got != nil {
		t.Fatalf("Drain after Forget = %v, want nil", got)
	}
}

func TestIsolationBySession(t *testing.T) {
	s := New()
	s.Enqueue("s1", "a")
	s.Enqueue("s2", "b")
	if got := s.Drain("s1"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("Drain(s1) = %v, want [a]", got)
	}
	if got := s.PendingLen("s2"); got != 1 {
		t.Fatalf("PendingLen(s2) = %d, want 1", got)
	}
}
