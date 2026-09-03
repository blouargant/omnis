package hookstate

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// The exact counter answers "how many times has this same command been tried",
// so a genuinely corrected command starts over at 1 — the counter only climbs
// when the agent retries the identical thing.
func TestAttemptCountsIdenticalArgsAndResetsOnChange(t *testing.T) {
	s := New()
	a1, _ := s.Attempt("sess", "Bash", map[string]any{"command": "kubectl apply -f a.yaml"})
	a2, _ := s.Attempt("sess", "Bash", map[string]any{"command": "kubectl apply -f a.yaml"})
	a3, _ := s.Attempt("sess", "Bash", map[string]any{"command": "kubectl apply -f b.yaml"})
	if a1 != 1 || a2 != 2 {
		t.Fatalf("attempts = %d,%d want 1,2", a1, a2)
	}
	if a3 != 1 {
		t.Fatalf("changed args attempt = %d, want 1 (a corrected command gets a fresh budget)", a3)
	}
}

// The coarse counter closes the hole the exact one leaves: an agent retrying
// endlessly DIFFERENT but still-wrong commands would otherwise never escalate.
func TestConsecutiveCountsBlocksAcrossDifferentArgs(t *testing.T) {
	s := New()
	for i, cmd := range []string{"a", "b", "c"} {
		_, cons := s.Attempt("sess", "Bash", map[string]any{"command": cmd})
		if cons != i {
			t.Fatalf("call %d saw consecutive = %d, want %d", i, cons, i)
		}
		s.RecordOutcome("sess", "Bash", true)
	}
}

func TestConsecutiveResetsOnASuccessfulCall(t *testing.T) {
	s := New()
	s.Attempt("sess", "Bash", map[string]any{"command": "a"})
	s.RecordOutcome("sess", "Bash", true)
	s.Attempt("sess", "Bash", map[string]any{"command": "b"})
	s.RecordOutcome("sess", "Bash", false)
	_, cons := s.Attempt("sess", "Bash", map[string]any{"command": "c"})
	if cons != 0 {
		t.Fatalf("consecutive = %d, want 0 after a non-blocked call", cons)
	}
}

func TestSessionsAreIsolatedAndForgettable(t *testing.T) {
	s := New()
	s.Attempt("a", "Bash", map[string]any{"command": "x"})
	if n, _ := s.Attempt("b", "Bash", map[string]any{"command": "x"}); n != 1 {
		t.Fatalf("other session attempt = %d, want 1", n)
	}
	s.Forget("a")
	if n, _ := s.Attempt("a", "Bash", map[string]any{"command": "x"}); n != 1 {
		t.Fatalf("after Forget attempt = %d, want 1", n)
	}
}

// The hash must not depend on map iteration order, or every call would look new.
// The Python hook script recomputes this hash with
// json.dumps(..., sort_keys=True, separators=(",",":")), which does not escape
// HTML. Go's encoding/json escapes &, < and > by default, and every compound
// shell command contains "&&" — so a regression back to json.Marshal would make
// the two sides disagree on exactly the commands that matter most, and every
// compound command would be refused as "not reviewed". Pin the exact bytes.
func TestHashArgsMatchesPlainJSONWithoutHTMLEscaping(t *testing.T) {
	args := map[string]any{"command": "kubectl get pods && kubectl delete pod x"}
	canonical := `{"command":"kubectl get pods && kubectl delete pod x"}`
	sum := sha256.Sum256([]byte(canonical))
	if got, want := HashArgs(args), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("HashArgs = %s, want %s — the hash of the UN-escaped canonical JSON %s", got, want, canonical)
	}
}

func TestHashArgsIsStableAcrossKeyOrder(t *testing.T) {
	h1 := HashArgs(map[string]any{"command": "x", "timeout": 5})
	h2 := HashArgs(map[string]any{"timeout": 5, "command": "x"})
	if h1 != h2 {
		t.Fatalf("hash is order-dependent: %q vs %q", h1, h2)
	}
	if h1 == HashArgs(map[string]any{"command": "y"}) {
		t.Fatal("different args must hash differently")
	}
}
