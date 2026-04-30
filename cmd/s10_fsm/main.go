// Component s10 — FSM communication protocol (Phase 3 / s10). The FSM is
// internal to teammates.Agent; this demo just exercises a full ask/reply
// round-trip using the Backend directly to make the state machine visible.
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/blouargant/agent-toolkit/internal/teammates"
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
		m, err := be.Receive(ctx, "reviewer", 4*time.Second)
		if err != nil || m == nil {
			return
		}
		_ = be.Send(ctx, m.From, teammates.Message{From: "reviewer", Body: "LGTM"})
	}()

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
