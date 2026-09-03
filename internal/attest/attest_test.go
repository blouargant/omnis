package attest

import (
	"strings"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/hookstate"
)

func TestRecordedVerdictIsVisibleForItsSession(t *testing.T) {
	s := New()
	subj := hookstate.HashArgs(map[string]any{"command": "kubectl apply -f a.yaml"})
	s.Record("sess", subj, VerdictApproved, "helm-owned check passed")
	got := s.For("sess")
	rec, ok := got[subj].(map[string]any)
	if !ok {
		t.Fatalf("subject %q missing from %v", subj, got)
	}
	if rec["verdict"] != string(VerdictApproved) {
		t.Fatalf("verdict = %v, want APPROVED", rec["verdict"])
	}
	if len(s.For("other")) != 0 {
		t.Fatal("verdicts must not leak across sessions")
	}
}

// The subject is a hash of the change, so approving one manifest cannot
// authorise applying a different one.
func TestVerdictDoesNotCoverDifferentArgs(t *testing.T) {
	s := New()
	v1 := hookstate.HashArgs(map[string]any{"command": "kubectl apply -f v1.yaml"})
	v2 := hookstate.HashArgs(map[string]any{"command": "kubectl apply -f v2.yaml"})
	s.Record("sess", v1, VerdictApproved, "ok")
	if _, found := s.For("sess")[v2]; found {
		t.Fatal("a verdict on one change must not cover another")
	}
}

func TestExpiredVerdictIsNotReported(t *testing.T) {
	s := New()
	subj := "abc"
	s.Record("sess", subj, VerdictApproved, "ok")
	s.records["sess"][subj] = record{Verdict: VerdictApproved, At: time.Now().Add(-2 * TTL)}
	if _, found := s.For("sess")[subj]; found {
		t.Fatal("a verdict older than the TTL must not be reported")
	}
}

func TestForgetDropsASession(t *testing.T) {
	s := New()
	s.Record("sess", "abc", VerdictApproved, "ok")
	s.Forget("sess")
	if len(s.For("sess")) != 0 {
		t.Fatal("Forget must drop the session's verdicts")
	}
}

// A nil-ish/empty resolved session must not record a verdict under an empty
// key: with no real session to key it under, the verdict would be recorded
// where no hook will ever read it, so the tool must refuse loudly instead.
func TestRecordValidationWithEmptySessionYieldsErrorAndDoesNotRecord(t *testing.T) {
	s := New()
	out := runRecordValidation(s, "", recordIn{Subject: "abc", Verdict: "APPROVED", Reasons: "ok"})
	if !strings.Contains(out.Result, "no session could be resolved") {
		t.Fatalf("result = %q, want the no-session error", out.Result)
	}
	if len(s.For("")) != 0 {
		t.Fatal("a verdict must not be recorded under an empty session key")
	}
}
