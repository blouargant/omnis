package agent

import (
	"context"
	"testing"
	"time"

	"github.com/blouargant/omnis/internal/askuser"
)

// With no registry there is nobody to authorise the call, so the escalation must
// deny — the same fail-safe the budget gate uses.
func TestAskHookPermissionDeniesWithoutRegistry(t *testing.T) {
	if askHookPermission(context.Background(), nil, "sess", "Bash", "why") {
		t.Fatal("no registry must deny, not allow")
	}
}

func TestAskHookPermissionAllowsOnlyOnTheAllowChoice(t *testing.T) {
	reg := askuser.NewRegistry()
	answer := func(sel string) bool {
		done := make(chan bool, 1)
		go func() {
			done <- askHookPermission(context.Background(), reg, "sess", "Bash", "validation failed 3x")
		}()
		q := awaitPending(t, reg, "sess")
		if err := reg.Resolve("sess", q.ID, askuser.Answer{Selected: []string{sel}}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return <-done
	}
	if !answer(choiceHookAllowOnce) {
		t.Fatal("the allow choice must allow")
	}
	if answer(choiceHookDeny) {
		t.Fatal("the deny choice must deny")
	}
}

// awaitPending waits for askHookPermission's card to be registered. Ask blocks
// in a goroutine, so the test must poll rather than assume it has landed.
func awaitPending(t *testing.T, reg *askuser.Registry, sid string) askuser.Question {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if qs := reg.Pending(sid); len(qs) > 0 {
			return qs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending question appeared within 2s")
	return askuser.Question{}
}

// The gap this closes: askHookPermission already denied with no registry, but
// every shipped surface HAS one, so an unattended run (a bench, CI) reached
// reg.Ask and blocked on a card nobody would ever resolve —
// askuser.DefaultTimeout is 0 by design, so the wait ends only with the run
// context. The assertion that matters is therefore the DEADLINE: without the
// flag this same call blocks (TestAskHookPermissionAllowsOnlyOnTheAllowChoice
// has to poll for the pending card and resolve it by hand).
func TestNonInteractiveDeniesInsteadOfWaiting(t *testing.T) {
	t.Setenv("OMNIS_NON_INTERACTIVE", "1")
	reg := askuser.NewRegistry()

	done := make(chan bool, 1)
	go func() { done <- askHookPermission(context.Background(), reg, "sess", "Bash", "why") }()

	select {
	case allowed := <-done:
		if allowed {
			t.Fatal("an unanswerable escalation must deny, not allow")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("askHookPermission blocked on a question nobody can answer")
	}

	// And it must not have left a card behind for a client that reconnects
	// later to find and be confused by.
	if qs := reg.Pending("sess"); len(qs) != 0 {
		t.Fatalf("a refused escalation registered %d pending question(s)", len(qs))
	}
}

// The flag is opt-in: an unset or unrecognised value must leave the waiting
// behaviour exactly as it was, because waiting is the correct default — a
// backgrounded tab, a reload or a network blip must not turn into a hard block
// on the user's work.
func TestNonInteractiveIsOptIn(t *testing.T) {
	for _, v := range []string{"", "0", "false", "no", "maybe"} {
		t.Setenv("OMNIS_NON_INTERACTIVE", v)
		if nonInteractive() {
			t.Fatalf("OMNIS_NON_INTERACTIVE=%q must not disable escalation", v)
		}
	}
	for _, v := range []string{"1", "true", "YES", " on "} {
		t.Setenv("OMNIS_NON_INTERACTIVE", v)
		if !nonInteractive() {
			t.Fatalf("OMNIS_NON_INTERACTIVE=%q must disable escalation", v)
		}
	}
}
