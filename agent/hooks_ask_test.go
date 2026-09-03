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
