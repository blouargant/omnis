package cache

import "testing"

func TestStatsHitRateAndSummary(t *testing.T) {
	t.Parallel()

	var s Stats
	s.calls.Store(3)
	s.prompt.Store(200)
	s.cached.Store(50)

	if got := s.Calls(); got != 3 {
		t.Fatalf("Calls() = %d, want 3", got)
	}
	if got := s.Prompt(); got != 200 {
		t.Fatalf("Prompt() = %d, want 200", got)
	}
	if got := s.Cached(); got != 50 {
		t.Fatalf("Cached() = %d, want 50", got)
	}
	if got := s.HitRate(); got != 25 {
		t.Fatalf("HitRate() = %v, want 25", got)
	}
	if got := s.Summary(); got != "cache: calls=3 prompt=200 cached=50 hit_rate=25.0%" {
		t.Fatalf("Summary() = %q", got)
	}
}

func TestStatsHitRateHandlesZeroPrompt(t *testing.T) {
	t.Parallel()

	var s Stats
	if got := s.HitRate(); got != 0 {
		t.Fatalf("HitRate() = %v, want 0", got)
	}
}
