// Component s10 — FSM communication protocol (Phase 3 / s10). The FSM is
// internal to teammates.Agent; this demo just exercises a full ask/reply
// round-trip using the Backend directly to make the state machine visible.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/blouargant/yoke/internal/teammates"
)

func main() {
	be, err := teammates.ChooseBackend()
	must(err)
	defer be.Close()
	lead := teammates.NewAgent("lead", be)
	rev := teammates.NewAgent("reviewer", be)

	// Reviewer waits in the background and replies.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		m, err := rev.Check(ctx, 4*time.Second)
		if err != nil || m == nil {
			return
		}
		_ = rev.Tell(ctx, m.From, "LGTM")
	}()

	// Wait briefly for reviewer.Check() to enter RESPONDING.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) && rev.State() != teammates.StateResponding {
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("reviewer state before ask:", rev.State())

	fmt.Printf("lead state before ask: %s\n", lead.State())
	reply, err := lead.Ask(ctx, "reviewer", "ok to merge?", 3*time.Second)
	must(err)
	fmt.Printf("lead state after  ask: %s\n", lead.State())
	fmt.Println("reply:", reply)
	fmt.Println("reviewer final state:", rev.State())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
